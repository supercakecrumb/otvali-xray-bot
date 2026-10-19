package x3ui

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	x3client "github.com/supercakecrumb/go-x3ui/client"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/serverpolicy"
)

func disabledTestHandler(t *testing.T) *ServerHandler {
	t.Helper()
	policy, err := serverpolicy.ParseDisabledServerIDs("7")
	if err != nil {
		t.Fatal(err)
	}
	return &ServerHandler{policy: policy, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestDisabledServerStartupDoesNotOpenConnections(t *testing.T) {
	policy, _ := serverpolicy.ParseDisabledServerIDs("7")
	server := database.Server{ID: 7, Name: "retired", IP: "invalid.example"}
	sh := NewServerHandler("unused-missing-key", []database.Server{server}, slog.New(slog.NewTextHandler(io.Discard, nil)), policy)
	defer sh.Close()
	if len(sh.sshClients) != 0 || len(sh.x3Clients) != 0 || len(sh.listeners) != 0 || len(sh.localPorts) != 0 {
		t.Fatal("disabled server must not create connection state")
	}
}

func TestDisabledServerCannotIssueOrReconnect(t *testing.T) {
	sh := disabledTestHandler(t)
	server := &database.Server{ID: 7}
	if _, err := sh.AddClient(server); !errors.Is(err, ErrServerDisabled) {
		t.Fatalf("AddClient must reject before accessing credentials: %v", err)
	}
	if key, err := sh.GetUserKey(server, "existing-user", 42); key != "" || !errors.Is(err, ErrServerDisabled) {
		t.Fatalf("GetUserKey must reject before accessing panels: key=%q err=%v", key, err)
	}
	if client, port, err := sh.StartSSHPortForward(server); client != nil || port != 0 || !errors.Is(err, ErrServerDisabled) {
		t.Fatalf("direct SSH forwarding must be blocked: port=%d err=%v", port, err)
	}
	for name, run := range map[string]func(*database.Server){"startup retry": sh.retryConnect, "connection monitor": sh.monitorSSHConnections} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sh.ctx = ctx
			done := make(chan struct{})
			go func() { run(server); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				cancel()
				<-done
				t.Fatal("disabled server must not enter a reconnect wait loop")
			}
		})
	}
}

func TestEnabledServerKeepsExistingClient(t *testing.T) {
	sh := disabledTestHandler(t)
	want := &x3client.Client{}
	sh.x3Clients = map[int64]*x3client.Client{8: want}
	got, err := sh.AddClient(&database.Server{ID: 8})
	if err != nil || got != want {
		t.Fatalf("unlisted server must retain its current client: %v", err)
	}
}
