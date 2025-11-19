# AI Agent Meta Files Guide

This document contains templates and guidelines for AI coding agent meta files used in the otvali-xray-bot project.

## Overview

Meta files provide AI coding agents with project-specific context, conventions, and guidelines. They help agents:
- Understand project architecture and patterns
- Follow consistent coding standards
- Make informed decisions without repeated explanations
- Avoid common pitfalls and anti-patterns

## Files to Create

1. **`.cursorrules`** - Instructions for Cursor IDE's AI
2. **`.clinerules`** - Configuration for Cline (Code assistant)
3. **`.aicontext`** - Generic AI context file for any agent

---

## 1. .cursorrules Template

This file should be created in the project root.

```
# otvali-xray-bot - Telegram VPN Management Bot

## Project Overview
You are working on otvali-xray-bot, a Go-based Telegram bot that manages VPN (Xray/X3UI) access keys for users. The bot connects to X3UI panel servers via SSH tunnels and provides a user-friendly interface for VPN key distribution and server management.

### Core Technologies
- Go 1.23.3
- PostgreSQL 15 with GORM ORM
- Telego (Telegram Bot framework)
- Docker & Docker Compose
- SSH tunneling for secure X3UI API access

## Architecture Principles

### 1. Layered Architecture
Follow the existing three-layer structure:
- **Telegram Layer** (internal/telegram/): Command handlers, inline keyboards, message formatting
- **Business Logic Layer** (internal/x3ui/): VPN key generation, SSH tunnel management, server health monitoring
- **Data Access Layer** (internal/database/): User management, server configuration storage, GORM models

### 2. Package Organization
- `internal/`: Private application code (not importable by external projects)
- `pkg/`: Reusable public packages (config, logger)
- `cmd/`: Application entry points

### 3. Dependency Direction
Dependencies should flow downward:
```
telegram/ → x3ui/ → database/
    ↓         ↓         ↓
        pkg/ (config, logger)
```

## Code Style & Conventions

### Go-Specific Guidelines

1. **Error Handling**
   ```go
   // Always check errors immediately
   if err != nil {
       logger.Error("Operation failed", slog.String("error", err.Error()))
       // Return user-friendly message
       bot.SendMessage(tu.Message(chatID, "Произошла ошибка. Попробуйте позже."))
       return
   }
   ```

2. **Logging**
   - Use structured logging with `slog`
   - Include relevant context in log messages
   ```go
   logger.Info("Bot is starting...")
   logger.Error("Failed to connect", slog.String("server", name), slog.String("error", err.Error()))
   logger.Debug("Initializing x3ui client", slog.String("server", name))
   ```

3. **Function Naming**
   - Public functions: PascalCase (exported)
   - Private functions: camelCase (unexported)
   - Methods: Receiver type + verb (e.g., `(b *Bot) handleStart`)

4. **File Organization**
   - One major type or feature per file
   - Group related handlers together (command.go, admincommand.go, keys.go)
   - Keep files under 500 lines when possible

### Telegram Bot Patterns

1. **Command Registration**
   ```go
   // User commands in registerCommands()
   b.bh.Handle(b.handleStart, th.CommandEqual("start"))
   
   // Admin commands in registerAdminCommands()
   b.bh.Handle(b.handleAddServer, th.CommandEqual("add_server"))
   
   // Callback queries for inline keyboards
   b.bh.Handle(b.handleHelpCallback, th.CallbackDataContains("help_"))
   ```

2. **Message Formatting**
   - User messages: Russian language
   - Admin notifications: Russian or English as appropriate
   - Use inline keyboards for better UX
   - Include back navigation in multi-step flows

3. **Callback Data Convention**
   - Format: `{action}_{id}` (e.g., `getkey_123`, `help_vpn_setup`)
   - Keep callback data under 64 bytes (Telegram limit)

### Database Patterns

1. **GORM Models**
   - Use pointer types for nullable fields (`*int64`, `*int`)
   - Include `gorm` tags for constraints
   - Always include `CreatedAt` timestamp

2. **Database Operations**
   ```go
   // Pattern: Check existence before creating
   _, err := b.db.GetUserByUsername(username)
   if err == nil {
       // User exists, handle accordingly
       return
   }
   
   // Create new record
   if err := b.db.AddUser(user); err != nil {
       // Handle error
   }
   ```

### SSH & X3UI Patterns

1. **Connection Management**
   - One SSH client per server (stored in ServerHandler)
   - Reuse X3UI clients across requests
   - Monitor connection health in background goroutine
   - Clean up connections on shutdown

2. **Key Generation**
   - One client per user per server
   - Email format: `{username}_{chatID}@domain.com`
   - Reuse existing clients when possible
   - Generate VLESS URIs with Reality protocol

## File-Specific Guidelines

### When editing internal/telegram/
- Validate admin access for admin commands: `b.db.IsUserAdmin(userID)`
- Always answer callback queries to remove loading animation
- Use `tu.Message()` helper for consistent message creation
- Include error handling for all bot API calls

### When editing internal/x3ui/
- Lock mutex when accessing connection maps
- Always close connections on errors
- Monitor SSH connection health continuously
- Log all connection state changes

### When editing internal/database/
- Use GORM's built-in methods (Create, First, Find, etc.)
- Return custom errors (ErrUserNotFound, ErrServerNotFound)
- Use transactions for complex operations
- Add indexes for frequently queried fields

## Common Tasks

### Adding a New Command

1. Create handler function in appropriate file (command.go for users, admincommand.go for admins)
2. Register handler in `registerCommands()` or `registerAdminCommands()`
3. Add help text in help.go if needed
4. Add constants for callback data if using inline keyboards

### Adding a New Server Field

1. Update Server model in internal/database/models.go
2. Update database migration (handled by GORM AutoMigrate)
3. Update /add_server command parsing in admincommand.go
4. Update server display in /list_servers

### Adding Platform Instructions

1. Add instruction constant in internal/telegram/instructions.go
2. Add button to vpnOSKeyboard in help.go
3. Handle callback case in handleHelpCallback

## Security Considerations

1. **Never commit these files to the repository:**
   - `.env` (environment variables)
   - SSH private keys
   - Database credentials
   
2. **Access Control:**
   - All admin commands must verify `IsUserAdmin()`
   - Invite system enforces user registration
   - Exclusive servers restricted to `ExclusiveAccess` users

3. **SSH Security:**
   - Use key-based authentication only
   - Verify known hosts
   - Close connections properly to prevent leaks

4. **API Security:**
   - X3UI APIs accessed via SSH tunnels only
   - No public exposure of panel ports
   - Use TLS for bot-to-Telegram communication

## Testing Guidelines

1. **Test Environment Setup:**
   - Use cmd/testenv/ for test utilities
   - Separate test database from production
   - Mock external services when possible

2. **Areas to Test:**
   - User CRUD operations
   - Server connection lifecycle
   - Command handler logic
   - SSH tunnel reliability
   - Key generation and formatting

## Environment Variables

Required in .env file:
```bash
TELEGRAM_TOKEN=<bot_token>
DATABASE_URL=postgresql://user:password@host:port/dbname
SSH_KEY_PATH=/path/to/ssh/private/key
LOG_LEVEL=debug|info
```

## Dependencies Management

- Use `go mod tidy` before committing
- Pin major versions in go.mod
- Document why dependencies were added
- Prefer standard library when possible

## Performance Considerations

1. **Connection Pooling:**
   - Maintain SSH connection pool per server
   - Reuse X3UI clients across requests
   - Implement automatic reconnection

2. **Rate Limiting:**
   - Respect Telegram Bot API limits
   - Batch message sending with delays
   - Implement exponential backoff for retries

3. **Database:**
   - Use connection pooling via GORM
   - Add indexes on frequently queried fields
   - Optimize N+1 queries with eager loading

## Error Messages

- User-facing: Russian language, friendly, actionable
- Logs: English, detailed, include error context
- Admin notifications: Clear, actionable, include user context

## Localization Notes

- All user-facing messages currently in Russian
- Commands and callback data in English
- Flag emojis used for country identification
- Consider i18n package for future multi-language support

## Git Workflow

1. Feature branches from main
2. Descriptive commit messages
3. PR review required for main branch
4. Keep commits atomic and focused
5. Document breaking changes in CHANGELOG.md

## Documentation Updates

When making significant changes:
1. Update ARCHITECTURE.md
2. Update README.md if setup changes
3. Add entry to CHANGELOG.md
4. Update inline comments for complex logic

## Anti-Patterns to Avoid

1. ❌ Don't store passwords in logs
2. ❌ Don't expose X3UI panel ports publicly
3. ❌ Don't block main thread with long operations (use goroutines)
4. ❌ Don't ignore error returns
5. ❌ Don't hardcode configuration values
6. ❌ Don't create global mutable state
7. ❌ Don't use panic for regular error handling

## Useful Commands

```bash
# Build and run locally
go run cmd/main.go

# Run with Docker Compose
docker-compose up --build

# Run tests
go test ./...

# Format code
go fmt ./...

# Lint code
golangci-lint run

# Update dependencies
go mod tidy
go mod vendor

# Check for security vulnerabilities
go list -m all | nancy sleuth
```

## Resources

- [X3UI Panel Documentation](https://github.com/MHSanaei/3x-ui)
- [Telego Bot Framework](https://github.com/mymmrac/telego)
- [GORM Documentation](https://gorm.io/)
- [Go SSH Package](https://pkg.go.dev/golang.org/x/crypto/ssh)
- [Xray Protocol](https://xtls.github.io/)

## Questions?

Refer to:
1. ARCHITECTURE.md for system design details
2. README.md for setup instructions
3. Existing code patterns in the codebase
4. Git history for examples of similar changes
```

---

## 2. .clinerules Template

This file should be created in the project root.

```
# Cline Rules for otvali-xray-bot

## Project Context
Go-based Telegram bot for VPN (Xray/X3UI) key management. Uses PostgreSQL, SSH tunneling, and Docker deployment.

## Code Standards

### Go Style
- Follow Go standard conventions (gofmt, golint)
- Use structured logging (slog package)
- Error handling: check immediately, log, return user-friendly message
- Naming: PascalCase for exports, camelCase for private

### File Organization
- internal/: private application code
- pkg/: reusable public packages
- cmd/: entry points
- Keep files under 500 lines

### Key Patterns
1. Layered architecture: telegram → x3ui → database
2. SSH connection pooling in ServerHandler
3. GORM for database operations
4. Middleware for auth and validation
5. Inline keyboards for user interaction

## Important Constraints

### Security
- Never commit: .env, SSH keys, credentials
- Admin commands: verify IsUserAdmin()
- X3UI APIs: access via SSH tunnel only
- Use key-based SSH auth only

### Telegram Bot
- Messages: Russian language for users
- Callback data: format as `{action}_{id}`
- Always answer callback queries
- Use structured error messages

### Database
- Nullable fields: use pointer types (*int64, *int)
- Include CreatedAt timestamps
- Check existence before creating
- Return custom errors (ErrUserNotFound, etc.)

### SSH & X3UI
- One SSH client per server
- Reuse X3UI clients
- Monitor connection health
- Clean up connections on shutdown

## Common Anti-Patterns to Avoid
- ❌ Blocking main thread (use goroutines)
- ❌ Ignoring error returns
- ❌ Hardcoded config values
- ❌ Global mutable state
- ❌ Passwords in logs
- ❌ Public X3UI panel ports

## Environment Variables
Required in .env:
- TELEGRAM_TOKEN
- DATABASE_URL
- SSH_KEY_PATH
- LOG_LEVEL

## Quick References
- Architecture: ARCHITECTURE.md
- Setup: README.md
- X3UI: github.com/MHSanaei/3x-ui
- Bot Framework: github.com/mymmrac/telego
```

---

## 3. .aicontext Template

This file should be created in the project root.

```
{
  "projectName": "otvali-xray-bot",
  "description": "Telegram bot for managing VPN (Xray/X3UI) access keys with SSH-tunneled server connections",
  "language": "Go",
  "version": "1.23.3",
  "frameworks": [
    "Telego (Telegram Bot API)",
    "GORM (PostgreSQL ORM)",
    "Docker & Docker Compose"
  ],
  "architecture": {
    "style": "Layered Architecture",
    "layers": [
      {
        "name": "Telegram Interface",
        "path": "internal/telegram/",
        "responsibilities": ["Command handlers", "Inline keyboards", "Message formatting"]
      },
      {
        "name": "Business Logic",
        "path": "internal/x3ui/",
        "responsibilities": ["VPN key generation", "SSH tunnel management", "Server monitoring"]
      },
      {
        "name": "Data Access",
        "path": "internal/database/",
        "responsibilities": ["User management", "Server storage", "GORM models"]
      }
    ]
  },
  "keyPatterns": [
    "SSH connection pooling per server",
    "Middleware for authentication and validation",
    "Command handler registration pattern",
    "Graceful shutdown with context cancellation",
    "Structured logging with slog"
  ],
  "dataModels": [
    {
      "name": "User",
      "fields": ["ID", "TelegramID", "Username", "IsAdmin", "InvitedByID", "Invited", "ExclusiveAccess", "CreatedAt"]
    },
    {
      "name": "Server",
      "fields": ["ID", "Name", "Country", "City", "IP", "SSHPort", "SSHUser", "APIPort", "Username", "Password", "InboundID", "IsExclusive", "CreatedAt"]
    }
  ],
  "codeStyle": {
    "naming": {
      "exported": "PascalCase",
      "unexported": "camelCase",
      "constants": "PascalCase or ALL_CAPS"
    },
    "errorHandling": "Check immediately, log, return user-friendly message",
    "logging": "Structured logging with slog package",
    "fileSize": "Keep under 500 lines when possible"
  },
  "security": {
    "sensitiveFiles": [".env", "SSH private keys", "Database credentials"],
    "accessControl": "Admin commands must verify IsUserAdmin()",
    "apiAccess": "X3UI APIs accessed via SSH tunnels only",
    "authentication": "Key-based SSH authentication only"
  },
  "environment": {
    "required": [
      "TELEGRAM_TOKEN",
      "DATABASE_URL",
      "SSH_KEY_PATH",
      "LOG_LEVEL"
    ]
  },
  "deployment": {
    "containerization": "Docker with docker-compose.yaml",
    "database": "PostgreSQL 15 in separate container",
    "volumes": ["SSH key mounted read-only", "Database persistent volume"]
  },
  "testing": {
    "testUtilities": "cmd/testenv/",
    "focusAreas": ["User CRUD", "Server connections", "Command handlers", "SSH tunnels", "Key generation"]
  },
  "commonTasks": [
    {
      "task": "Add new command",
      "steps": [
        "Create handler in command.go or admincommand.go",
        "Register in registerCommands() or registerAdminCommands()",
        "Add help text in help.go",
        "Add callback data constants if needed"
      ]
    },
    {
      "task": "Add new server field",
      "steps": [
        "Update Server model in models.go",
        "Update /add_server parsing",
        "Update /list_servers display"
      ]
    }
  ],
  "antiPatterns": [
    "Blocking main thread with long operations",
    "Ignoring error returns",
    "Hardcoding configuration values",
    "Creating global mutable state",
    "Using panic for regular errors",
    "Storing passwords in logs",
    "Exposing X3UI panels publicly"
  ],
  "resources": [
    {
      "name": "X3UI Documentation",
      "url": "https://github.com/MHSanaei/3x-ui"
    },
    {
      "name": "Telego Framework",
      "url": "https://github.com/mymmrac/telego"
    },
    {
      "name": "GORM ORM",
      "url": "https://gorm.io/"
    },
    {
      "name": "Go SSH Package",
      "url": "https://pkg.go.dev/golang.org/x/crypto/ssh"
    }
  ],
  "documentation": [
    "ARCHITECTURE.md - System design and patterns",
    "README.md - Setup and deployment",
    "CHANGELOG.md - Version history"
  ]
}
```

---

## 4. .aiderignore Template (Optional)

For Aider AI coding assistant - excludes files from context.

```
# Dependencies and build artifacts
*.sum
vendor/
*.exe
*.dll
*.so
*.dylib
*.test
*.out
main

# Environment and secrets
.env
*.key
*.pem
known_hosts

# Version control
.git/
.github/

# Documentation (include selectively)
.changes/
CHANGELOG.md

# IDE and editor files
.vscode/
.idea/
*.swp
*.swo
*~

# Docker artifacts
Dockerfile
docker-compose.yaml

# Logs
*.log

# Database
*.db
*.sqlite
```

---

## Implementation Instructions

### Step 1: Review the Templates
Read through all templates to understand the content and structure.

### Step 2: Create the Meta Files

In Code mode, create these files in the project root:

1. **`.cursorrules`** - Copy content from section 1
2. **`.clinerules`** - Copy content from section 2  
3. **`.aicontext`** - Copy content from section 3
4. **`.aiderignore`** (optional) - Copy content from section 4

### Step 3: Verify and Test

After creation:
1. Ensure files are in project root directory
2. Check that .gitignore excludes sensitive patterns
3. Test with your AI coding assistant
4. Adjust based on assistant behavior

### Step 4: Maintain

Keep meta files updated when:
- Adding new architectural patterns
- Changing coding conventions
- Adding new common tasks
- Discovering new anti-patterns
- Updating dependencies

## Best Practices

1. **Be Specific**: Provide concrete examples, not just abstract rules
2. **Be Concise**: AI agents have context limits
3. **Be Actionable**: Include commands and code snippets
4. **Be Current**: Update when project evolves
5. **Be Consistent**: Use same terminology across all files

## Customization Guide

### Adding Project-Specific Rules

Add rules that are unique to your workflow:
```
## Custom Workflow
- Branch naming: feature/TICKET-123-description
- PR format: Must include tests and docs
- Review process: At least 2 approvals required
```

### Adding Technology-Specific Guidelines

Include specific guidelines for your tech stack:
```
## PostgreSQL Specific
- Use UUID for distributed systems
- Include proper indexes for foreign keys
- Use JSONB for flexible schemas
- Avoid SELECT * in production code
```

### Adding Team Conventions

Document team-specific practices:
```
## Team Conventions
- Code reviews within 24 hours
- Daily standups at 10 AM
- Deploy to staging before production
- Use semantic versioning for releases
```

## Troubleshooting

### AI Not Following Rules
- Check file is in correct location
- Verify content is properly formatted
- Reduce complexity (too many rules)
- Provide more concrete examples

### AI Missing Context
- Add more background in project overview
- Include architecture diagrams
- Reference relevant documentation
- Provide code examples for patterns

### Rules Conflict
- Prioritize more specific over general rules
- Document precedence explicitly
- Resolve contradictions
- Keep single source of truth

## Version Control

**Important**: Commit meta files to version control!

These files should be:
- ✅ Committed to repository
- ✅ Shared with team
- ✅ Updated with code changes
- ✅ Reviewed in PRs

Do not confuse with:
- ❌ .env (secrets, never commit)
- ❌ SSH keys (secrets, never commit)
- ❌ Local IDE settings (personal preference)

## Additional Resources

- [Cursor Rules Community](https://github.com/PatrickJS/awesome-cursorrules)
- [Aider Documentation](https://aider.chat/docs/config.html)
- [AI Coding Best Practices](https://github.com/openai/openai-cookbook)

---

## Summary

You now have comprehensive meta files for your otvali-xray-bot project:

1. **`.cursorrules`** - Detailed guide for Cursor AI (most comprehensive)
2. **`.clinerules`** - Concise rules for Cline (quick reference)
3. **`.aicontext`** - Structured JSON for generic AI agents
4. **`.aiderignore`** - File exclusions for Aider (optional)

Next steps:
1. Switch to Code mode
2. Create each file using the templates above
3. Test with your AI coding assistant
4. Adjust based on results
5. Keep updated as project evolves

These meta files will significantly improve AI agent performance on your codebase by providing consistent context, conventions, and guidelines.