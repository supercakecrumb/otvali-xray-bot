package sshapi

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func testKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer, key
}

type sshTestServer struct {
	listener    net.Listener
	config      *ssh.ServerConfig
	connections sync.Map
	accepted    atomic.Int32
	forwarded   atomic.Int32
	targets     chan string
	closed      chan struct{}
	hangChannel bool
}

func newSSHServer(t *testing.T, clientKey ssh.PublicKey, hostKey ssh.Signer, hangChannel bool, additionalHostKeys ...ssh.Signer) *sshTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &sshTestServer{listener: listener, targets: make(chan string, 8), closed: make(chan struct{}, 8), hangChannel: hangChannel}
	server.config = &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if !bytes.Equal(key.Marshal(), clientKey.Marshal()) {
			return nil, errors.New("wrong test key")
		}
		return nil, nil
	}}
	server.config.AddHostKey(hostKey)
	for _, key := range additionalHostKeys {
		server.config.AddHostKey(key)
	}
	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			server.accepted.Add(1)
			server.connections.Store(raw, struct{}{})
			go server.handle(raw)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		server.connections.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true })
	})
	return server
}

func (s *sshTestServer) handle(raw net.Conn) {
	defer func() { s.closed <- struct{}{} }()
	defer raw.Close()
	defer s.connections.Delete(raw)
	conn, channels, requests, err := ssh.NewServerConn(raw, s.config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(requests)
	for pending := range channels {
		if pending.ChannelType() != "direct-tcpip" {
			pending.Reject(ssh.UnknownChannelType, "test only permits forwarding")
			continue
		}
		var destination struct {
			Host       string
			Port       uint32
			OriginHost string
			OriginPort uint32
		}
		if ssh.Unmarshal(pending.ExtraData(), &destination) != nil {
			pending.Reject(ssh.Prohibited, "invalid target")
			continue
		}
		target := net.JoinHostPort(destination.Host, strconv.FormatUint(uint64(destination.Port), 10))
		s.forwarded.Add(1)
		s.targets <- target
		if s.hangChannel {
			continue
		}
		remote, err := net.Dial("tcp", target)
		if err != nil {
			pending.Reject(ssh.ConnectionFailed, "test destination unavailable")
			continue
		}
		channel, reqs, err := pending.Accept()
		if err != nil {
			remote.Close()
			continue
		}
		go ssh.DiscardRequests(reqs)
		go func() {
			defer remote.Close()
			defer channel.Close()
			go func() { _, _ = io.Copy(remote, channel); remote.Close() }()
			_, _ = io.Copy(channel, remote)
		}()
	}
}

func testConfig(t *testing.T, server *sshTestServer, keyPath string, pinned ssh.PublicKey, target string) Config {
	t.Helper()
	known := filepath.Join(t.TempDir(), "known_hosts")
	entry := knownhosts.Line([]string{knownhosts.Normalize(server.listener.Addr().String())}, pinned)
	if err := os.WriteFile(known, []byte(entry+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{SSHAddr: server.listener.Addr().String(), SSHUser: "otvali-mtproto-api", KeyPath: keyPath,
		KnownHostsPath: known, TargetAddress: target, DialTimeout: time.Second, RequestTimeout: 2 * time.Second}
}

func TestPinnedSSHForwardsOnlyConfiguredLoopbackAndDoesNotUseEnvironmentProxy(t *testing.T) {
	key, keyPath := testKey(t)
	hostKey, _ := testKey(t)
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("authorization was not forwarded")
		}
		_, _ = io.WriteString(w, `{"ready":true}`)
	}))
	defer api.Close()
	s := newSSHServer(t, key.PublicKey(), hostKey, false)
	cfg := testConfig(t, s, keyPath, hostKey.PublicKey(), strings.TrimPrefix(api.URL, "http://"))
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	client, closer, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("GET", api.URL+"/v1/users", nil)
		req.Header.Set("Authorization", "Bearer test-only")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != `{"ready":true}` {
			t.Fatalf("unexpected API response: %s", body)
		}
	}
	if calls.Load() != 2 || s.forwarded.Load() != 1 {
		t.Fatalf("requests=%d channels=%d; expected one reused SSH connection", calls.Load(), s.forwarded.Load())
	}
	if got := <-s.targets; got != cfg.TargetAddress {
		t.Fatalf("unexpected forwarding target: %s", got)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(api.URL); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed client accepted request: %v", err)
	}
}

func TestRejectsOtherTargetsBeforeConnectingAndNeverFollowsRedirects(t *testing.T) {
	key, keyPath := testKey(t)
	hostKey, _ := testKey(t)
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer api.Close()
	s := newSSHServer(t, key.PublicKey(), hostKey, false)
	cfg := testConfig(t, s, keyPath, hostKey.PublicKey(), strings.TrimPrefix(api.URL, "http://"))
	client, closer, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	for _, endpoint := range []string{destination.URL, "https://" + cfg.TargetAddress, "http://localhost:9093", "http://test:password@" + cfg.TargetAddress} {
		if _, err := client.Get(endpoint); !errors.Is(err, ErrTarget) {
			t.Fatalf("endpoint was not rejected: %v", err)
		}
	}
	req, _ := http.NewRequest("GET", api.URL, nil)
	req.Host = "different-host.test"
	if _, err := client.Do(req); !errors.Is(err, ErrTarget) {
		t.Fatal("Host override was allowed")
	}
	if s.accepted.Load() != 0 {
		t.Fatal("invalid destination attempted SSH connection")
	}
	resp, err := client.Get(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || destinationCalls.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestChangedOrUnknownHostKeyCannotForward(t *testing.T) {
	key, keyPath := testKey(t)
	hostKey, _ := testKey(t)
	wrongKey, _ := testKey(t)
	s := newSSHServer(t, key.PublicKey(), hostKey, false)
	cfg := testConfig(t, s, keyPath, wrongKey.PublicKey(), "127.0.0.1:9093")
	client, closer, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if _, err := client.Get("http://" + cfg.TargetAddress); !errors.Is(err, ErrHostKey) {
		t.Fatalf("changed host key not rejected safely: %v", err)
	}
	if s.forwarded.Load() != 0 {
		t.Fatal("unverified host accepted forwarding")
	}
	if err := os.WriteFile(cfg.KnownHostsPath, []byte("# no trusted hosts\n"), 0600); err != nil {
		t.Fatal(err)
	}
	other, closer2, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer2.Close()
	if _, err := other.Get("http://" + cfg.TargetAddress); !errors.Is(err, ErrHostKey) {
		t.Fatalf("unknown host key was accepted: %v", err)
	}
}

func TestMixedHostKeysNegotiatePinnedEd25519(t *testing.T) {
	key, keyPath := testKey(t)
	ed25519Host, _ := testKey(t)
	ecdsaPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaHost, err := ssh.NewSignerFromKey(ecdsaPrivate)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer api.Close()
	// A normal OpenSSH host offers multiple host-key types. Only its verified
	// Ed25519 key is provisioned into this service's known_hosts mount.
	s := newSSHServer(t, key.PublicKey(), ecdsaHost, false, ed25519Host)
	cfg := testConfig(t, s, keyPath, ed25519Host.PublicKey(), strings.TrimPrefix(api.URL, "http://"))
	client, closer, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	response, err := client.Get(api.URL)
	if err != nil {
		t.Fatalf("server offers the pinned Ed25519 key but negotiation rejected it: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || s.forwarded.Load() != 1 {
		t.Fatal("request did not reach the private API through the pinned SSH host")
	}
}

func TestCancellationStopsWaitingForSSHChannel(t *testing.T) {
	key, keyPath := testKey(t)
	hostKey, _ := testKey(t)
	s := newSSHServer(t, key.PublicKey(), hostKey, true)
	cfg := testConfig(t, s, keyPath, hostKey.PublicKey(), "127.0.0.1:9093")
	cfg.DialTimeout = 5 * time.Second
	client, closer, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+cfg.TargetAddress, nil)
	done := make(chan error, 1)
	go func() { _, err := client.Do(req); done <- err }()
	select {
	case <-s.targets:
	case <-time.After(time.Second):
		t.Fatal("SSH channel request never arrived")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled request returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not stop channel creation")
	}
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("cancelled HTTP request left an SSH handshake running")
	}
}

func TestDeadlineAndCloseStopSilentSSHHandshake(t *testing.T) {
	key, keyPath := testKey(t)
	hostKey, _ := testKey(t)
	s := newSSHServer(t, key.PublicKey(), hostKey, true)
	// Use a different TCP listener which accepts but never speaks SSH.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	cfg := testConfig(t, s, keyPath, hostKey.PublicKey(), "127.0.0.1:9093")
	cfg.SSHAddr = listener.Addr().String()
	cfg.DialTimeout = 100 * time.Millisecond
	client, closer, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := client.Get("http://" + cfg.TargetAddress); err == nil {
		t.Fatal("silent SSH handshake succeeded")
	}
	if time.Since(started) > time.Second {
		t.Fatal("SSH handshake exceeded dial timeout")
	}
	(<-accepted).Close()
	closer.Close()
	cfg.DialTimeout = 5 * time.Second
	client, closer, err = NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	done := make(chan error, 1)
	go func() { _, err := client.Get("http://" + cfg.TargetAddress); done <- err }()
	var raw net.Conn
	select {
	case raw = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("connection not started")
	}
	defer raw.Close()
	closer.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("closed handshake returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not stop pending handshake")
	}
}

func TestRejectsUnsafeConfiguration(t *testing.T) {
	for _, target := range []string{"localhost:9093", "0.0.0.0:9093", "10.0.0.1:9093", "127.0.0.1:0", "127.0.0.1:09093", "127.0.0.1"} {
		if validAddress(target, true) {
			t.Fatalf("unsafe target accepted: %s", target)
		}
	}
	for _, target := range []string{"127.0.0.1:9093", "[::1]:9093"} {
		if !validAddress(target, true) {
			t.Fatalf("loopback rejected: %s", target)
		}
	}
	if _, _, err := NewClient(Config{SSHAddr: "host.test:22", SSHUser: "root", TargetAddress: "127.0.0.1:9093", KeyPath: "key", KnownHostsPath: "known"}); !errors.Is(err, ErrConfig) {
		t.Fatal("root account accepted")
	}
}

func TestHTTPDeadlineAndShutdownCloseActiveTunnel(t *testing.T) {
	for _, mode := range []string{"deadline", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			key, keyPath := testKey(t)
			hostKey, _ := testKey(t)
			started := make(chan struct{})
			release := make(chan struct{})
			api := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer api.Close()
			defer close(release)
			s := newSSHServer(t, key.PublicKey(), hostKey, false)
			cfg := testConfig(t, s, keyPath, hostKey.PublicKey(), strings.TrimPrefix(api.URL, "http://"))
			if mode == "deadline" {
				cfg.RequestTimeout = 100 * time.Millisecond
			}
			client, closer, err := NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer closer.Close()
			done := make(chan error, 1)
			go func() { _, err := client.Get(api.URL); done <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("HTTP request did not reach private API")
			}
			if mode == "shutdown" {
				closer.Close()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("hanging request completed without an error")
				}
				if mode == "shutdown" && !errors.Is(err, ErrClosed) {
					t.Fatalf("shutdown returned unsanitized error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("active HTTP request did not stop")
			}
			select {
			case <-s.closed:
			case <-time.After(time.Second):
				t.Fatal("completed request left an SSH connection open")
			}
		})
	}
}
