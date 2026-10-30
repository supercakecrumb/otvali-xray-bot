// Package sshapi transports a private HTTP API over a pinned SSH connection.
// It never opens a listening socket, reads proxy environment variables, or
// permits requests to destinations other than the configured remote loopback.
package sshapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	ErrConfig  = errors.New("invalid private management transport configuration")
	ErrTarget  = errors.New("private management destination is not allowed")
	ErrSSH     = errors.New("private management SSH connection failed")
	ErrHostKey = errors.New("private management SSH host key verification failed")
	ErrClosed  = errors.New("private management transport is closed")
	ErrHTTP    = errors.New("private management HTTP request failed")
)

// Config uses a dedicated SSH account restricted server-side to local TCP
// forwarding to TargetAddress. Both file paths refer to private runtime mounts.
// SSHUser must not be root. TargetAddress must be a numeric loopback address.
type Config struct {
	SSHAddr        string
	SSHUser        string
	KeyPath        string
	KnownHostsPath string
	TargetAddress  string
	DialTimeout    time.Duration
	RequestTimeout time.Duration
}

var usernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// NewClient performs only local configuration checks. Network establishment is
// lazy. Close stops pending SSH handshakes and closes both idle and active HTTP
// connections. Each HTTP connection owns one SSH connection and one channel;
// closing a timed-out HTTP request therefore cannot leave an orphaned tunnel.
func NewClient(cfg Config) (*http.Client, io.Closer, error) {
	if !validAddress(cfg.SSHAddr, false) || !validAddress(cfg.TargetAddress, true) ||
		!usernamePattern.MatchString(cfg.SSHUser) || cfg.SSHUser == "root" ||
		cfg.KeyPath == "" || cfg.KnownHostsPath == "" ||
		cfg.DialTimeout < 0 || cfg.RequestTimeout < 0 {
		return nil, nil, ErrConfig
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 20 * time.Second
	}
	key, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return nil, nil, ErrConfig
	}
	signer, err := ssh.ParsePrivateKey(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return nil, nil, ErrConfig
	}
	verify, err := knownhosts.New(cfg.KnownHostsPath)
	if err != nil {
		return nil, nil, ErrConfig
	}
	lifetime, cancel := context.WithCancel(context.Background())
	t := &transport{
		cfg: cfg, lifetime: lifetime, cancel: cancel,
		connections: make(map[*connection]struct{}),
		sshConfig: &ssh.ClientConfig{
			User: cfg.SSHUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			// The dedicated provisioner pins only the verified Ed25519 host
			// key. Do not negotiate an unpinned ECDSA/RSA key merely because
			// OpenSSH also offers it and the SSH library prefers that type.
			HostKeyAlgorithms: []string{ssh.KeyAlgoED25519},
			HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
				if verify(hostname, remote, key) != nil {
					return ErrHostKey
				}
				return nil
			},
			Timeout: cfg.DialTimeout,
		},
	}
	t.http = &http.Transport{
		DialContext: t.dialContext,
		// Proxy is intentionally nil: ALL_PROXY / HTTP_PROXY must never expose
		// the management bearer token to an environment-configured proxy.
		MaxIdleConns: 2, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 4,
		IdleConnTimeout:        30 * time.Second,
		ResponseHeaderTimeout:  cfg.RequestTimeout,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 32 << 10,
	}
	client := &http.Client{
		Transport: t, Timeout: cfg.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return client, t, nil
}

func validAddress(address string, loopback bool) bool {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != rawPort {
		return false
	}
	if loopback {
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback() && net.JoinHostPort(host, rawPort) == address
	}
	return true
}

type transport struct {
	cfg         Config
	sshConfig   *ssh.ClientConfig
	http        *http.Transport
	lifetime    context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	connections map[*connection]struct{}
}

type requestContextKey struct{}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || req.URL.Scheme != "http" ||
		req.URL.Host != t.cfg.TargetAddress || req.URL.User != nil ||
		(req.Host != "" && req.Host != t.cfg.TargetAddress) {
		if req != nil && req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, ErrTarget
	}
	if t.lifetime.Err() != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, ErrClosed
	}
	// net/http may detach dial cancellation to let another request reuse a
	// pending connection. Preserve the actual caller context explicitly: an
	// abandoned management request must not keep an SSH handshake running.
	request := req.Clone(context.WithValue(req.Context(), requestContextKey{}, req.Context()))
	response, err := t.http.RoundTrip(request)
	if err == nil {
		return response, nil
	}
	if t.lifetime.Err() != nil {
		return nil, ErrClosed
	}
	for _, allowed := range []error{ErrTarget, ErrHostKey, ErrSSH, ErrClosed, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, allowed) {
			return nil, allowed
		}
	}
	return nil, ErrHTTP
}

func (t *transport) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" || address != t.cfg.TargetAddress {
		return nil, ErrTarget
	}
	if t.lifetime.Err() != nil {
		return nil, ErrClosed
	}
	dialCtx, cancel := context.WithTimeout(ctx, t.cfg.DialTimeout)
	defer cancel()
	stopLifetime := context.AfterFunc(t.lifetime, cancel)
	defer stopLifetime()
	if caller, ok := ctx.Value(requestContextKey{}).(context.Context); ok {
		if err := caller.Err(); err != nil {
			return nil, err
		}
		stopCaller := context.AfterFunc(caller, cancel)
		defer stopCaller()
	}
	raw, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", t.cfg.SSHAddr)
	if err != nil {
		return nil, t.dialError(dialCtx, err)
	}
	deadline, _ := dialCtx.Deadline()
	if raw.SetDeadline(deadline) != nil {
		raw.Close()
		return nil, ErrSSH
	}
	stopCancel := context.AfterFunc(dialCtx, func() { _ = raw.Close() })
	defer stopCancel()
	sshConn, channels, requests, err := ssh.NewClientConn(raw, t.cfg.SSHAddr, t.sshConfig)
	if err != nil {
		raw.Close()
		return nil, t.dialError(dialCtx, err)
	}
	client := ssh.NewClient(sshConn, channels, requests)
	forwarded, err := client.DialContext(dialCtx, "tcp", t.cfg.TargetAddress)
	if err != nil {
		client.Close()
		return nil, t.dialError(dialCtx, err)
	}
	// Stop the handshake cancellation before returning a reusable connection.
	// A raced cancellation closes the raw connection; never pool that socket.
	if !stopCancel() || dialCtx.Err() != nil || raw.SetDeadline(time.Time{}) != nil {
		forwarded.Close()
		client.Close()
		return nil, t.dialError(dialCtx, ErrSSH)
	}
	conn := &connection{Conn: forwarded, client: client, raw: raw, owner: t}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		forwarded.Close()
		client.Close()
		return nil, ErrClosed
	}
	t.connections[conn] = struct{}{}
	t.mu.Unlock()
	return conn, nil
}

func (t *transport) dialError(ctx context.Context, err error) error {
	if t.lifetime.Err() != nil {
		return ErrClosed
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrHostKey) {
		return ErrHostKey
	}
	// SSH failures can include server-controlled text. No raw SSH, HTTP URL,
	// key path, or authentication error is allowed into application logs.
	return ErrSSH
}

func (t *transport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	connections := make([]*connection, 0, len(t.connections))
	for conn := range t.connections {
		connections = append(connections, conn)
	}
	t.mu.Unlock()
	t.cancel()
	t.http.CloseIdleConnections()
	for _, conn := range connections {
		_ = conn.Close()
	}
	return nil
}

type connection struct {
	net.Conn
	client *ssh.Client
	raw    net.Conn
	owner  *transport
	once   sync.Once
}

func (c *connection) Close() error {
	c.once.Do(func() {
		_ = c.client.Close()
		_ = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.connections, c)
		c.owner.mu.Unlock()
	})
	return nil
}

// SSH channels do not implement deadlines. One channel owns this complete SSH
// connection, so deadlines on its underlying socket have the expected scope.
func (c *connection) SetDeadline(deadline time.Time) error { return c.raw.SetDeadline(deadline) }
func (c *connection) SetReadDeadline(deadline time.Time) error {
	return c.raw.SetReadDeadline(deadline)
}
func (c *connection) SetWriteDeadline(deadline time.Time) error {
	return c.raw.SetWriteDeadline(deadline)
}
