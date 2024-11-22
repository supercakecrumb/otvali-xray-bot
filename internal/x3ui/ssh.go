package x3ui

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/skeema/knownhosts"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"golang.org/x/crypto/ssh"
)

var localPortSearchStart = 10000

// StartSSHPortForward sets up an SSH connection with port forwarding using SSH key authentication
func (sh *ServerHandler) StartSSHPortForward(server *database.Server) (*ssh.Client, int, error) {
	addr := fmt.Sprintf("%s:%v", server.IP, server.SSHPort)
	username := server.Username
	remoteURL := fmt.Sprintf("localhost:%v", server.APIPort)

	localPort, err := findLocalPort(localPortSearchStart)
	if err != nil {
		sh.logger.Error("Error finding local port", slog.String("err", err.Error()))
		return nil, 0, fmt.Errorf("error finding local port: %v", err)
	}

	config := sh.createSshConfig(username, sh.SSHKeyPath)

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		sh.logger.Error("Error creating ssh client", slog.String("addr", addr), slog.String("err", err.Error()))
		return nil, 0, fmt.Errorf("error creating ssh client: %v", err)
	}
	defer client.Close()

	listener, err := client.Listen("tcp", remoteURL)
	if err != nil {
		sh.logger.Error("Error listening api", slog.String("remoteURL", remoteURL), slog.String("err", err.Error()))
		return nil, 0, fmt.Errorf("error listening api: %v", err)
	}
	defer listener.Close()

	for {
		remote, err := listener.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go func() {
			local, err := net.Dial("tcp", fmt.Sprintf("localhost:%v", localPort))
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println("tunnel established with", local.LocalAddr())
			sh.runTunnel(local, remote)
		}()
	}
}

// runTunnel runs a tunnel between two connections; as soon as one connection
// reaches EOF or reports an error, both connections are closed and this
// function returns.
func (sh *ServerHandler) runTunnel(local, remote net.Conn) {
	defer local.Close()
	defer remote.Close()
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(local, remote)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(remote, local)
		done <- struct{}{}
	}()

	<-done
}

func (sh *ServerHandler) createSshConfig(username, keyFile string) *ssh.ClientConfig {
	knownHostsCallback, err := knownhosts.New(sshConfigPath("known_hosts"))
	if err != nil {
		log.Fatal(err)
	}

	key, err := os.ReadFile(keyFile)
	if err != nil {
		log.Fatalf("unable to read private key: %v", err)
	}

	// Create the Signer for this private key.
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		log.Fatalf("unable to parse private key: %v", err)
	}

	// An SSH client is represented with a ClientConn.
	//
	// To authenticate with the remote server you must pass at least one
	// implementation of AuthMethod via the Auth field in ClientConfig,
	// and provide a HostKeyCallback.
	return &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback:   ssh.HostKeyCallback(knownHostsCallback),
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
	}
}

func sshConfigPath(filename string) string {
	return filepath.Join(os.Getenv("HOME"), ".ssh", filename)
}

// findLocalPort finds the first available local port starting from the given start port.
func findLocalPort(start int) (int, error) {
	for port := start; port <= 65535; port++ {
		address := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", address)
		if err == nil {
			// Successfully bound to the port, it is available
			_ = ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available ports found starting from %d", start)
}
