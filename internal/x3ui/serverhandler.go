package x3ui

import (
	"log/slog"
	"net"
	"sync"
	"time"

	x3client "github.com/supercakecrumb/go-x3ui/client"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"golang.org/x/crypto/ssh"
)

var (
	defaultInboundRemark = "DefaultInbound"
	defaultInboundPort   = 443
)

type ServerHandler struct {
	SSHKeyPath string
	x3Clients  map[int64]*x3client.Client // Map of server ID to x3ui client
	sshClients map[int64]*ssh.Client      // Map of server ID to SSH client
	localPorts map[int64]int              // Map of server ID to local port
	listeners  map[int64]net.Listener     // Map of server ID to Listener
	mutex      sync.Mutex
	logger     *slog.Logger
}

func NewServerHandler(sshKeyPath string, servers []database.Server, logger *slog.Logger) *ServerHandler {
	sh := ServerHandler{
		SSHKeyPath: sshKeyPath,
		x3Clients:  make(map[int64]*x3client.Client),
		sshClients: make(map[int64]*ssh.Client),
		localPorts: make(map[int64]int),
		listeners:  make(map[int64]net.Listener),
		logger:     logger,
	}

	for i := range servers {
		server := servers[i]
		// Connect to the server and set up the x3ui client
		_, err := sh.AddClient(&server)
		if err != nil {
			logger.Error("Failed to connect to server, will retry in background",
				slog.String("server", server.Name),
				slog.Int64("server_id", server.ID),
				slog.String("error", err.Error()),
			)
			go sh.retryConnect(&server)
		}
	}

	return &sh
}

func (sh *ServerHandler) Close() {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	for id := range sh.sshClients {
		// Close the listener first
		if listener, exists := sh.listeners[id]; exists {
			err := listener.Close()
			if err != nil {
				sh.logger.Error("Failed to close listener", slog.Int64("server_id", id), slog.String("error", err.Error()))
			} else {
				sh.logger.Info("Listener closed", slog.Int64("server_id", id))
			}
			delete(sh.listeners, id)
		}

		// Close the SSH client
		sshClient := sh.sshClients[id]
		err := sshClient.Close()
		if err != nil {
			sh.logger.Error("Failed to close SSH client", slog.Int64("server_id", id), slog.String("error", err.Error()))
		} else {
			sh.logger.Info("SSH client closed", slog.Int64("server_id", id))
		}
		delete(sh.sshClients, id)

		// Remove the x3client.Client
		delete(sh.x3Clients, id)
	}
}

func (sh *ServerHandler) AddClient(server *database.Server) (*x3client.Client, error) {

	// Check if client already exists
	sh.mutex.Lock()
	client, exists := sh.x3Clients[server.ID]
	sh.mutex.Unlock()
	if exists {
		return client, nil
	}

	// Establish SSH connection and create x3ui client
	client, err := sh.connectToServer(server)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func (sh *ServerHandler) connectToServer(server *database.Server) (*x3client.Client, error) {
	// Use the SSHKeyPath from ServerHandler to connect via SSH
	sh.logger.Debug("Starting ssh port forwarding", slog.String("server", server.Name))
	sshClient, localPort, err := sh.StartSSHPortForward(server)
	if err != nil {
		return nil, err
	}

	// Initialize x3ui client with server credentials
	sh.logger.Debug("Initializing x3ui client", slog.String("server", server.Name))
	x3Client, err := InitializeX3uiClient(localPort, server.Username, server.Password, sh.logger)
	if err != nil {
		// Close the SSH client if x3Client initialization failed
		sshClient.Close()
		return nil, err
	}

	// Store both sshClient and x3Client
	sh.mutex.Lock()
	sh.sshClients[server.ID] = sshClient
	sh.x3Clients[server.ID] = x3Client
	sh.localPorts[server.ID] = localPort
	sh.mutex.Unlock()

	go sh.monitorSSHConnections(server)

	return x3Client, nil
}

func (sh *ServerHandler) retryConnect(server *database.Server) {
	backoff := 5 * time.Second
	maxBackoff := 2 * time.Minute

	for {
		time.Sleep(backoff)
		if _, err := sh.AddClient(server); err == nil {
			sh.logger.Info("Successfully connected to server after retry",
				slog.String("server", server.Name),
				slog.Int64("server_id", server.ID),
			)
			return
		}

		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}
