# otvali-xray-bot Architecture

## Project Overview

**otvali-xray-bot** is a Telegram bot written in Go that manages VPN (Xray/X3UI) access keys for users. The bot connects to X3UI panel servers via SSH tunnels and provides a user-friendly interface for VPN key distribution and management.

### Core Purpose
- Distribute VPN keys to authorized Telegram users
- Manage multiple X3UI servers across different locations
- Provide platform-specific VPN setup instructions
- Control access through invite-only system
- Support exclusive servers for premium users

## Technology Stack

### Core Technologies
- **Language**: Go 1.23.3
- **Database**: PostgreSQL 15 (via GORM)
- **Bot Framework**: Telego v0.31.4
- **X3UI Client**: github.com/supercakecrumb/go-x3ui
- **Deployment**: Docker + Docker Compose

### Key Dependencies
- `telego` - Telegram Bot API wrapper
- `gorm` - ORM for database operations
- `go-x3ui` - X3UI panel API client
- `go-resty` - HTTP client for API calls
- `golang.org/x/crypto/ssh` - SSH tunneling
- `skeema/knownhosts` - SSH host key verification

## Project Structure

```
otvali-xray-bot/
├── cmd/                      # Application entry points
│   ├── main.go              # Main bot application
│   └── testenv/main.go      # Test environment utilities
├── internal/                # Private application code (not importable)
│   ├── database/           # Data layer
│   │   ├── db.go           # Database initialization
│   │   ├── models.go       # Data models (User, Server)
│   │   ├── users.go        # User CRUD operations
│   │   └── server.go       # Server CRUD operations
│   ├── telegram/           # Bot layer
│   │   ├── bot.go          # Bot initialization & lifecycle
│   │   ├── command.go      # User commands (/start, /help, /invite)
│   │   ├── admincommand.go # Admin commands (/add_server, /list_servers)
│   │   ├── keys.go         # VPN key generation & distribution
│   │   ├── help.go         # Help system & inline keyboards
│   │   ├── instructions.go # Platform-specific VPN instructions
│   │   ├── middleware.go   # Bot middleware (auth, database)
│   │   ├── responses.go    # Response formatting utilities
│   │   ├── util.go         # Helper functions
│   │   └── flag.go         # Country flag emoji mapping
│   └── x3ui/               # X3UI integration layer
│       ├── xuiclient.go    # X3UI client initialization
│       ├── serverhandler.go # Server connection management
│       ├── ssh.go          # SSH tunnel management
│       ├── resources.go    # VPN key generation logic
│       └── info.go         # Internal data structures
├── pkg/                    # Public reusable packages
│   ├── config/
│   │   └── config.go       # Environment configuration
│   └── logger/
│       └── logger.go       # Structured logging setup
├── aurora_aeza/            # Deployment scripts (cloud-specific)
├── .changes/               # Changelog entries
├── .github/                # GitHub workflows
├── docker-compose.yaml     # Multi-container orchestration
├── Dockerfile             # Container image definition
├── go.mod                 # Go module dependencies
└── README.md              # Setup documentation
```

## Architectural Patterns

### 1. Layered Architecture

The application follows a clean layered architecture:

```
┌─────────────────────────────────────┐
│     Telegram Bot Interface Layer    │  (internal/telegram/)
│   - Command handlers                │
│   - Callback query handlers         │
│   - Message formatting              │
└──────────────┬──────────────────────┘
               │
┌──────────────▼──────────────────────┐
│      Business Logic Layer           │  (internal/x3ui/)
│   - VPN key generation              │
│   - SSH tunnel management           │
│   - Server health monitoring        │
└──────────────┬──────────────────────┘
               │
┌──────────────▼──────────────────────┐
│       Data Access Layer             │  (internal/database/)
│   - User management                 │
│   - Server configuration storage    │
│   - GORM models & queries           │
└──────────────┬──────────────────────┘
               │
┌──────────────▼──────────────────────┐
│      External Services              │
│   - PostgreSQL Database             │
│   - X3UI Panel API (via SSH)        │
│   - Telegram Bot API                │
└─────────────────────────────────────┘
```

### 2. SSH Tunnel Management Pattern

Each X3UI server connection uses SSH port forwarding:

```go
// Pattern: Persistent SSH connections with local port forwarding
Server → SSH Client → Local Port Forward → X3UI API Client
```

**Benefits**:
- Secure API access without exposing X3UI panels publicly
- Connection pooling per server (stored in ServerHandler)
- Automatic reconnection on failure (monitorSSHConnections)

### 3. Middleware Pattern

Bot handlers use middleware for cross-cutting concerns:

```go
// Middleware chain
Request → PanicRecovery() → userUsernameMiddleware() → userDatabaseMiddleware() → Handler
```

**Middleware Functions**:
- `PanicRecovery()` - Catches panics to prevent bot crashes
- `userUsernameMiddleware()` - Ensures user has username
- `userDatabaseMiddleware()` - Auto-registers users in database

### 4. Command Handler Pattern

Commands are registered using the handler pattern:

```go
// User commands
b.bh.Handle(b.handleStart, th.CommandEqual("start"))
b.bh.Handle(b.handleHelp, th.CommandEqual("help"))
b.bh.Handle(b.handleGetKey, th.CommandEqual("get_key"))

// Admin commands
b.bh.Handle(b.handleAddServer, th.CommandEqual("add_server"))
b.bh.Handle(b.handleListServers, th.CommandEqual("list_servers"))

// Callback queries (inline keyboards)
b.bh.Handle(b.handleHelpCallback, th.CallbackDataContains("help_"))
b.bh.Handle(b.handleGetKeyCallback, th.CallbackDataContains("getkey_"))
```

## Data Models

### User Model

```go
type User struct {
    ID                int64     // Primary key
    TelegramID        *int64    // Telegram user ID (nullable)
    Username          string    // Telegram username (unique)
    IsAdmin           bool      // Admin privileges flag
    InvitedByID       *int64    // Who invited this user
    InvitedByUsername string    // Inviter's username
    Invited           bool      // Whether user was invited
    ExclusiveAccess   bool      // Access to exclusive servers
    CreatedAt         time.Time // Account creation timestamp
}
```

### Server Model

```go
type Server struct {
    ID           int64     // Primary key
    Name         string    // Server identifier (unique)
    Country      string    // Server location (country)
    City         string    // Server location (city)
    IP           string    // Server IP address
    SSHPort      int       // SSH connection port
    SSHUser      string    // SSH username
    APIPort      int       // X3UI API port
    Username     string    // X3UI panel username
    Password     string    // X3UI panel password
    RealityCover string    // Reality protocol cover domain
    InboundID    *int      // X3UI inbound ID (nullable)
    IsExclusive  bool      // Exclusive server flag
    CreatedAt    time.Time // Server registration timestamp
}
```

## Key Components

### 1. Bot Lifecycle (`internal/telegram/bot.go`)

```go
// Initialization
NewBot(token, logger, db, serverHandler) → Bot instance

// Startup sequence
bot.Start() → 
  - Initialize long polling
  - Register middleware
  - Register command handlers
  - Start bot handler
  - Notify admins of startup

// Shutdown sequence
bot.Stop() →
  - Notify admins of shutdown
  - Stop bot handler
  - Close ServerHandler connections
```

### 2. Server Handler (`internal/x3ui/serverhandler.go`)

**Purpose**: Manages persistent SSH connections to X3UI servers

**Key Responsibilities**:
- Establish SSH tunnels to servers
- Maintain connection pool (map[serverID]*ssh.Client)
- Monitor connection health
- Provide X3UI client instances
- Handle graceful cleanup on shutdown

**Connection Flow**:
```go
AddClient(server) →
  connectToServer(server) →
    StartSSHPortForward(server) → SSH tunnel established
    InitializeX3uiClient(localPort, creds) → X3UI client ready
    monitorSSHConnections(server) → Health monitoring goroutine
```

### 3. VPN Key Generation (`internal/x3ui/resources.go`)

**Process Flow**:
1. User requests key for specific server
2. Check if user already has client for this server
3. If not, create new client in X3UI panel
4. Generate VLESS URI with Reality protocol
5. Return formatted key to user

**Client Management**:
- One client per user per server
- Email format: `{username}_{chatID}@domain.com`
- Automatic client reuse on subsequent requests

### 4. Invite System

**Access Control**:
- New users must be invited by existing users
- Inviter tracked in database (`InvitedByID`, `InvitedByUsername`)
- Regular users: Access to non-exclusive servers only
- Exclusive access users: Access to all servers

**Command**: `/invite <username>`

## Environment Configuration

Required environment variables (`.env` file):

```bash
# Telegram Bot Configuration
TELEGRAM_TOKEN=<bot_token_from_botfather>

# Database Configuration
DATABASE_URL=postgresql://user:password@host:port/dbname

# SSH Configuration
SSH_KEY_PATH=/path/to/ssh/private/key

# Logging Configuration
LOG_LEVEL=debug|info  # Default: debug
```

## Security Considerations

### 1. SSH Key Authentication
- SSH connections use key-based authentication only
- Private keys mounted as Docker volumes (read-only)
- No password authentication for SSH

### 2. Database Security
- Passwords stored in plain text (X3UI API requirement)
- Database access via internal Docker network only
- PostgreSQL port exposed only for development

### 3. Bot Access Control
- Invite-only user registration
- Admin commands protected by `IsUserAdmin()` check
- Middleware ensures username presence before processing

### 4. API Security
- X3UI APIs accessed via SSH tunnels (localhost only)
- TLS verification disabled for localhost connections
- Each server has isolated SSH connection

## Deployment Architecture

### Docker Compose Setup

```yaml
services:
  app:                    # Bot application
    build: Dockerfile
    env_file: .env
    volumes:
      - ssh_key:/key:ro   # SSH private key (read-only)
    depends_on:
      - db
  
  db:                     # PostgreSQL database
    image: postgres:15
    environment:
      POSTGRES_USER: bot
      POSTGRES_PASSWORD: botpassword
      POSTGRES_DB: botdb
    volumes:
      - db_data:/var/lib/postgresql/data
```

### Container Structure
- **app**: Single-binary Go application
- **db**: PostgreSQL with persistent volume
- Containers communicate via Docker network
- SSH key mounted from host filesystem

## Development Patterns

### 1. Error Handling

```go
// Pattern: Log and return user-friendly messages
if err != nil {
    b.logger.Error("Operation failed", slog.String("error", err.Error()))
    bot.SendMessage(tu.Message(chatID, "Произошла ошибка. Попробуйте позже."))
    return
}
```

### 2. Logging

```go
// Structured logging with slog
logger.Info("Bot is starting...")
logger.Error("Failed to connect", slog.String("server", name), slog.String("error", err.Error()))
logger.Debug("Initializing x3ui client", slog.String("server", name))
```

### 3. Graceful Shutdown

```go
// Pattern: Context-based shutdown signaling
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

// Wait for signal
<-ctx.Done()
logger.Info("Shutting down gracefully...")

// Cleanup
bot.Stop()
serverHandler.Close()
```

### 4. Concurrent Operations

```go
// Pattern: Goroutines for long-running operations
go b.generateKeyProcess(serverID, update)  // Key generation
go sh.monitorSSHConnections(server)        // Connection monitoring
```

## Testing Strategy

### Test Environment Setup
- `cmd/testenv/main.go` - Test utilities
- Separate test environment configuration
- Mock X3UI API responses where needed

### Areas for Testing
1. User CRUD operations
2. Server connection management
3. Command handler logic
4. SSH tunnel reliability
5. VPN key generation

## Common Workflows

### 1. New User Registration Flow
```
User sends /start →
  userUsernameMiddleware() checks username →
  userDatabaseMiddleware() registers user →
  Checks if user was invited →
  Grants appropriate access level
```

### 2. VPN Key Request Flow
```
User sends /get_key →
  Display server list (getServerButtons) →
  User selects server (callback) →
  handleGetKeyCallback() triggered →
  generateKeyProcess() executes:
    - Animated loading message
    - Check/create X3UI client
    - Generate VLESS URI
    - Display key with copy button
```

### 3. Server Addition Flow (Admin)
```
Admin: /add_server <params> →
  Validate admin privileges →
  Parse server parameters →
  Connect via SSH (serverHandler.AddClient) →
  Create inbound if needed →
  Save to database →
  Confirm success to admin
```

## Performance Considerations

### 1. Connection Pooling
- SSH connections maintained per server
- X3UI clients reused across requests
- Automatic reconnection on failure

### 2. Database Optimization
- GORM automigration on startup
- Indexed fields: Username (unique), TelegramID (unique)
- Connection pooling via GORM

### 3. Rate Limiting
- Telegram Bot API: Batch message sending
- Retry logic with exponential backoff (`sendWithRetry`)
- Delays between bulk operations

## Extension Points

### Adding New Commands
1. Create handler function in appropriate file
2. Register in `registerCommands()` or `registerAdminCommands()`
3. Add help text in [`internal/telegram/help.go`](internal/telegram/help.go:1)

### Adding New Server Types
1. Extend [`Server`](internal/database/models.go:20) model if needed
2. Implement protocol-specific client in [`internal/x3ui/`](internal/x3ui/)
3. Update key generation logic in [`resources.go`](internal/x3ui/resources.go:1)

### Adding Platform Instructions
1. Add constants in [`internal/telegram/instructions.go`](internal/telegram/instructions.go:1)
2. Update [`vpnOSKeyboard`](internal/telegram/help.go:39) with new button
3. Handle callback in [`handleHelpCallback`](internal/telegram/help.go:86)

## Known Limitations

1. **Single-threaded key generation**: Keys generated sequentially per server
2. **Plain text credentials**: X3UI passwords stored unencrypted (API limitation)
3. **No key revocation**: Manual X3UI panel access required for key removal
4. **Limited error recovery**: SSH connection failures require manual intervention
5. **No usage analytics**: No tracking of VPN usage per user

## Future Improvements

1. **Key expiration**: Automatic key rotation and expiration
2. **Usage monitoring**: Track bandwidth and connection stats
3. **Multi-language support**: Internationalization for messages
4. **User quotas**: Bandwidth limits and fair usage policies
5. **Health dashboard**: Admin panel for system monitoring
6. **Backup servers**: Automatic failover to backup servers
7. **User feedback**: Collect and display server performance ratings

## References

- [X3UI Panel Documentation](https://github.com/MHSanaei/3x-ui)
- [Telego Bot Framework](https://github.com/mymmrac/telego)
- [GORM Documentation](https://gorm.io/)
- [Xray Protocol Documentation](https://xtls.github.io/)