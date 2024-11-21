package x3ui

import (
	"fmt"
	"io"
	"net"
	"os"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"golang.org/x/crypto/ssh"
)

// StartSSHPortForward sets up an SSH connection with port forwarding using SSH key authentication
func StartSSHPortForward(sshKeyPath string, server *database.Server) (*ssh.Client, int, error) {
	// Read private key file
	key, err := os.ReadFile(sshKeyPath)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to read private key: %w", err)
	}

	// Create signer for key
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to parse private key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: server.SSHUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Use proper host key verification in production
	}

	// Establish SSH connection
	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:%d", server.IP, server.SSHPort), config)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to dial SSH: %w", err)
	}

	// Set up port forwarding from localPort to server.APIPort
	// Use ":0" to let the OS choose an available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to listen on local port: %w", err)
	}

	localPort := listener.Addr().(*net.TCPAddr).Port
	remoteAddr := fmt.Sprintf("localhost:%d", server.APIPort)

	go func() {
		for {
			localConn, err := listener.Accept()
			if err != nil {
				continue
			}
			remoteConn, err := conn.Dial("tcp", remoteAddr)
			if err != nil {
				localConn.Close()
				continue
			}
			go func() {
				defer localConn.Close()
				defer remoteConn.Close()
				go func() {
					_, _ = io.Copy(localConn, remoteConn)
				}()
				_, _ = io.Copy(remoteConn, localConn)
			}()
		}
	}()

	return conn, localPort, nil
}
