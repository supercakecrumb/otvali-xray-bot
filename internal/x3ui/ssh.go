package x3ui

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/skeema/knownhosts"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"golang.org/x/crypto/ssh"
)

var localPortSearchStart = 10000

// StartSSHPortForward sets up an SSH connection with port forwarding using SSH key authentication
func (sh *ServerHandler) StartSSHPortForward(server *database.Server) (*ssh.Client, int, error) {
	defer func() {
		if r := recover(); r != nil {
			sh.logger.Error("Recovered from panic", slog.Any("panic", r))
		}
	}()

	sh.logger.Info("Starting SSH port forwarding",
		slog.String("server_ip", server.IP),
		slog.Int("ssh_port", server.SSHPort),
		slog.String("ssh_user", server.SSHUser),
		slog.Int("api_port", server.APIPort),
	)

	// Create SSH client configuration
	config, err := sh.createSshConfig(server.SSHUser, sh.SSHKeyPath)
	if err != nil {
		sh.logger.Error("Failed to create SSH config", slog.String("error", err.Error()))
		return nil, 0, err
	}

	// Establish SSH connection
	addr := fmt.Sprintf("%s:%d", server.IP, server.SSHPort)
	sh.logger.Info("Connecting to SSH server", slog.String("address", addr))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		sh.logger.Error("Failed to dial SSH server", slog.String("error", err.Error()))
		return nil, 0, fmt.Errorf("failed to dial SSH: %w", err)
	}
	sh.logger.Info("SSH connection established")

	// Find an available local port
	localPort, err := findLocalPort(localPortSearchStart)
	if err != nil {
		sh.logger.Error("Error finding local port", slog.String("error", err.Error()))
		return nil, 0, fmt.Errorf("error finding local port: %v", err)
	}
	sh.logger.Info("Found available local port", slog.Int("local_port", localPort))

	// Start local listener
	localAddr := fmt.Sprintf("localhost:%d", localPort)
	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		sh.logger.Error("Failed to start local listener", slog.String("error", err.Error()))
		return nil, 0, fmt.Errorf("failed to start local listener: %v", err)
	}
	sh.logger.Info("Local listener started", slog.String("address", localAddr))

	// Store the listener
	sh.mutex.Lock()
	sh.listeners[server.ID] = listener
	sh.mutex.Unlock()

	remoteAddr := fmt.Sprintf("localhost:%v", server.APIPort)

	// Start forwarding connections
	go func() {
		for {
			localConn, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					sh.logger.Info("Listener closed, stopping accept loop")
					return // Exit the goroutine
				}

				if _, ok := err.(net.Error); ok {
					sh.logger.Warn("Temporary error accepting local connection", slog.String("error", err.Error()))
					continue
				}

				sh.logger.Error("Unexpected error accepting local connection", slog.String("error", err.Error()))
				continue
			}
			sh.logger.Debug("Accepted local connection", slog.String("local_addr", localConn.RemoteAddr().String()))

			remoteConn, err := client.Dial("tcp", remoteAddr)
			if err != nil {
				sh.logger.Error("Error dialing remote address",
					slog.String("remote_address", remoteAddr),
					slog.String("error", err.Error()),
				)
				localConn.Close()
				continue
			}
			sh.logger.Debug("Remote connection established",
				slog.String("remote_address", remoteAddr),
			)

			go sh.runTunnel(localConn, remoteConn)
		}
	}()

	sh.logger.Info("SSH port forwarding started successfully",
		slog.Int("local_port", localPort),
		slog.String("remote_address", remoteAddr),
	)
	return client, localPort, nil
}

func (sh *ServerHandler) runTunnel(localConn, remoteConn net.Conn) {
	defer localConn.Close()
	defer remoteConn.Close()

	// Channel to signal completion
	done := make(chan struct{}, 2)

	// Copy data from local to remote
	go func() {
		_, err := io.Copy(remoteConn, localConn)
		if err != nil && !isClosedNetworkError(err) {
			sh.logger.Error("Error copying data from local to remote", slog.String("error", err.Error()))
		}
		done <- struct{}{}
	}()

	// Copy data from remote to local
	go func() {
		_, err := io.Copy(localConn, remoteConn)
		if err != nil && !isClosedNetworkError(err) {
			sh.logger.Error("Error copying data from remote to local", slog.String("error", err.Error()))
		}
		done <- struct{}{}
	}()

	// Wait for both copy operations to finish
	<-done
	<-done

	sh.logger.Debug("Tunnel closed")
}

func isClosedNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if opErr, ok := err.(*net.OpError); ok {
		if opErr.Err.Error() == "use of closed network connection" {
			return true
		}
	}
	return false
}

// createSshConfig creates the SSH client configuration
func (sh *ServerHandler) createSshConfig(username, keyFile string) (*ssh.ClientConfig, error) {
	knownHostsPath, err := sh.sshConfigPath("known_hosts")
	if err != nil {
		sh.logger.Error("Failed to setup known_hosts file", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to setup known_hosts file: %w", err)
	}
	knownHostsCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		sh.logger.Error("Error loading known_hosts", slog.String("path", knownHostsPath), slog.String("error", err.Error()))
		return nil, fmt.Errorf("error loading known_hosts: %w", err)
	}

	// Wrap the callback to handle unknown keys
	hostKeyCallback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := knownHostsCallback(hostname, remote, key)
		if err != nil {
			sh.logger.Warn("Unknown host key, adding to known_hosts", slog.String("host", hostname))

			// Append the new key to the known_hosts file
			entry := knownhosts.Line([]string{hostname}, key)
			file, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				sh.logger.Error("Failed to open known_hosts for writing", slog.String("path", knownHostsPath), slog.String("error", err.Error()))
				return err
			}
			defer file.Close()

			if _, err := file.WriteString(entry + "\n"); err != nil {
				sh.logger.Error("Failed to write new key to known_hosts", slog.String("path", knownHostsPath), slog.String("error", err.Error()))
				return err
			}

			sh.logger.Info("Added new host key to known_hosts", slog.String("host", hostname))
			return nil
		}
		return err
	}

	key, err := os.ReadFile(keyFile)
	if err != nil {
		sh.logger.Error("Unable to read private key", slog.String("error", err.Error()))
		return nil, fmt.Errorf("unable to read private key: %w", err)
	}

	// Create the Signer for this private key.
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		sh.logger.Error("Unable to parse private key", slog.String("error", err.Error()))
		return nil, fmt.Errorf("unable to parse private key: %w", err)
	}
	sh.logger.Info("SSH private key successfully parsed")

	return &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
		// HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, // Uncomment if needed
	}, nil
}

// sshConfigPath constructs the path to the SSH configuration file
func (sh *ServerHandler) sshConfigPath(filename string) (string, error) {
	// Get the current directory
	currentDir, err := os.Getwd()
	if err != nil {
		sh.logger.Error("Failed to get current directory", slog.String("error", err.Error()))
		return "", fmt.Errorf("unable to get current directory: %w", err)
	}

	// Full path to the file
	filePath := filepath.Join(currentDir, filename)

	// Check if the file exists
	_, err = os.Stat(filePath)
	if os.IsNotExist(err) {
		// Create the file if it does not exist
		file, err := os.Create(filePath)
		if err != nil {
			sh.logger.Error("Failed to create file", slog.String("filename", filename), slog.String("error", err.Error()))
			return "", fmt.Errorf("unable to create %s file: %w", filename, err)
		}
		defer file.Close()

		// Add a comment to indicate it's a known_hosts file
		if _, err := file.WriteString("# SSH known_hosts file\n"); err != nil {
			sh.logger.Error("Failed to write initial content to file", slog.String("filename", filename), slog.String("error", err.Error()))
			return "", fmt.Errorf("failed to write initial content to %s: %w", filename, err)
		}
		sh.logger.Info("Created new known_hosts file", slog.String("path", filePath))
	}

	return filePath, nil
}

// findLocalPort finds the first available local port starting from the given start port.
func findLocalPort(start int) (int, error) {
	for port := start; port <= 65535; port++ {
		address := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", address)
		if err == nil {
			_ = ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports found starting from %d", start)
}

func (sh *ServerHandler) monitorSSHConnections(server *database.Server) {
	for {
		time.Sleep(10 * time.Second) // Check every 10 seconds

		sh.mutex.Lock()
		client, exists := sh.sshClients[server.ID]
		sh.mutex.Unlock()

		if !exists || !isSSHConnectionAlive(client) {
			sh.logger.Warn("SSH connection lost, reconnecting", slog.String("server", server.Name))

			sh.mutex.Lock()
			delete(sh.sshClients, server.ID)
			sh.mutex.Unlock()

			newClient, _, err := sh.StartSSHPortForward(server)
			if err != nil {
				sh.logger.Error("Failed to reconnect SSH", slog.String("error", err.Error()))
				continue
			}

			sh.mutex.Lock()
			sh.sshClients[server.ID] = newClient
			sh.mutex.Unlock()
		}
	}
}

func isSSHConnectionAlive(client *ssh.Client) bool {
	session, err := client.NewSession()
	if err != nil {
		return false
	}
	defer session.Close()
	return true
}
