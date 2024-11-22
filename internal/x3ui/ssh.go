package x3ui

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"log/slog"

	"github.com/skeema/knownhosts"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"golang.org/x/crypto/ssh"
)

var localPortSearchStart = 10000

// StartSSHPortForward sets up an SSH connection with port forwarding using SSH key authentication
func (sh *ServerHandler) StartSSHPortForward(server *database.Server) (*ssh.Client, int, error) {
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

	remoteAddr := fmt.Sprintf("localhost:%v", server.APIPort)

	// Start forwarding connections
	go func() {
		for {
			localConn, err := listener.Accept()
			if err != nil {
				sh.logger.Error("Error accepting local connection", slog.String("error", err.Error()))
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

// runTunnel forwards data between local and remote connections
func (sh *ServerHandler) runTunnel(local, remote net.Conn) {
	defer local.Close()
	defer remote.Close()
	done := make(chan struct{}, 2)

	go func() {
		_, err := io.Copy(local, remote)
		if err != nil {
			sh.logger.Error("Error copying data from remote to local", slog.String("error", err.Error()))
		}
		done <- struct{}{}
	}()

	go func() {
		_, err := io.Copy(remote, local)
		if err != nil {
			sh.logger.Error("Error copying data from local to remote", slog.String("error", err.Error()))
		}
		done <- struct{}{}
	}()

	<-done
	sh.logger.Debug("Tunnel closed")
}

// createSshConfig creates the SSH client configuration
func (sh *ServerHandler) createSshConfig(username, keyFile string) (*ssh.ClientConfig, error) {
	knownHostsPath := sshConfigPath("known_hosts")
	knownHostsCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		sh.logger.Error("Error loading known_hosts", slog.String("path", knownHostsPath), slog.String("error", err.Error()))
		return nil, fmt.Errorf("error loading known_hosts: %w", err)
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
		HostKeyCallback: ssh.HostKeyCallback(knownHostsCallback),
		// HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, // Uncomment if needed
	}, nil
}

// sshConfigPath constructs the path to the SSH configuration file
func sshConfigPath(filename string) string {
	return filepath.Join(os.Getenv("HOME"), ".ssh", filename)
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
