package x3ui

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

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

func NewServerHandler(sshKeyPath string, logger *slog.Logger) *ServerHandler {
	return &ServerHandler{
		SSHKeyPath: sshKeyPath,
		x3Clients:  make(map[int64]*x3client.Client),
		sshClients: make(map[int64]*ssh.Client),
		localPorts: make(map[int64]int),
		listeners:  make(map[int64]net.Listener),
		logger:     logger,
	}
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

	// Optionally, clear the maps
	sh.sshClients = make(map[int64]*ssh.Client)
	sh.listeners = make(map[int64]net.Listener)
	sh.x3Clients = make(map[int64]*x3client.Client)
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
	sshClient, localPort, err := sh.StartSSHPortForward(server)
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
	sh.localPorts[server.ID] = localPort

	return x3Client, nil
}

func (sh *ServerHandler) CreateInbound(server *database.Server) (*x3client.Inbound, error) {
	// Define inbound configuration
	inboundPayload, err := sh.x3Clients[server.ID].GenerateDefaultInboundConfig(defaultInboundRemark, server.RealityCover, server.IP, defaultInboundPort)
	if err != nil {
		sh.logger.Error("Failed to create inbound payload", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to create inbound payload: %w", err)
	}

	// Create inbound
	inbound, err := sh.x3Clients[server.ID].AddInbound(inboundPayload)
	if err != nil {
		sh.logger.Error("Failed to create inbound", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to create inbound: %w", err)
	}

	return inbound, nil
}
