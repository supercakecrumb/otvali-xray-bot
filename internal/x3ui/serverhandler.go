package x3ui

import (
	"fmt"
	"log/slog"
	"sync"

	x3client "github.com/supercakecrumb/go-x3ui/client"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"golang.org/x/crypto/ssh"
)

type ServerHandler struct {
	SSHKeyPath string
	x3Clients  map[int64]*x3client.Client // Map of server ID to x3ui client
	sshClients map[int64]*ssh.Client      // Map of server ID to SSH client
	mutex      sync.Mutex
	logger     *slog.Logger
}

func NewServerHandler(sshKeyPath string, logger *slog.Logger) *ServerHandler {
	return &ServerHandler{
		SSHKeyPath: sshKeyPath,
		x3Clients:  make(map[int64]*x3client.Client),
		sshClients: make(map[int64]*ssh.Client),
		logger:     logger,
	}
}

func (sh *ServerHandler) Close() {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	for id, sshClient := range sh.sshClients {
		sshClient.Close()
		delete(sh.sshClients, id)
		delete(sh.x3Clients, id)
	}
}

func (sh *ServerHandler) GetClient(server *database.Server) (*x3client.Client, error) {
	sh.mutex.Lock()
	defer sh.mutex.Unlock()

	// Check if client already exists
	if client, exists := sh.x3Clients[server.ID]; exists {
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
	sshClient, localPort, err := StartSSHPortForward(sh.SSHKeyPath, server)
	if err != nil {
		return nil, err
	}

	// Initialize x3ui client with server credentials
	x3Client, err := InitializeX3uiClient(localPort, server.Username, server.Password, sh.logger)
	if err != nil {
		// Close the SSH client if x3Client initialization failed
		sshClient.Close()
		return nil, err
	}

	// Store both sshClient and x3Client
	sh.sshClients[server.ID] = sshClient
	sh.x3Clients[server.ID] = x3Client

	return x3Client, nil
}

func (sh *ServerHandler) CreateOutbound(serverID int64) error {
	// Define outbound configuration
	outboundPayload := x3client.AddInboundPayload{
		// Fill in required fields
	}

	// Create outbound
	err := sh.x3Clients[serverID].AddInbound(outboundPayload)
	if err != nil {
		sh.logger.Error("Failed to create outbound", slog.String("error", err.Error()))
		return fmt.Errorf("failed to create outbound: %w", err)
	}

	return nil
}
