package x3ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	x3client "github.com/supercakecrumb/go-x3ui/client"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/serverpolicy"
)

// Synthetic panel encoding; production Mini App never writes clients.
type numericPanelClient struct {
	x3client.InboundClient
	TelegramID *int64 `json:"tgId"`
}

func numericInbound(t *testing.T, clients []x3client.InboundClient) *x3client.Inbound {
	t.Helper()
	wire := make([]numericPanelClient, 0, len(clients))
	for _, client := range clients {
		wire = append(wire, numericPanelClient{InboundClient: client, TelegramID: client.TgID.Value})
	}
	raw, err := json.Marshal(map[string]any{"clients": wire})
	if err != nil {
		t.Fatal(err)
	}
	return &x3client.Inbound{ID: 2, Enable: true, Port: 443, Protocol: "vless", Settings: string(raw), StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["cover.example"],"shortIds":["0001"],"settings":{"publicKey":"synthetic-public","fingerprint":"chrome"}}}`}
}
func TestNumericOwnerNeverClaimsUsernameAndPreservesExistingEmail(t *testing.T) {
	id := int64(42)
	other := int64(99)
	client := x3client.InboundClient{ID: "00000000-0000-4000-8000-000000000001", Email: "original_email", Enable: true, TgID: x3client.FlexibleInt64{Value: &id}}
	inbound := numericInbound(t, []x3client.InboundClient{client})
	email, exists, err := ownedEmail(inbound, "new_username", id, time.Now())
	if err != nil || !exists || email != "original_email" {
		t.Fatal("numeric owner must retain existing credential despite rename")
	}
	for _, tg := range []*int64{nil, &other} {
		client.Email = "reused_name"
		client.TgID.Value = tg
		inbound = numericInbound(t, []x3client.InboundClient{client})
		if _, _, err := ownedEmail(inbound, "reused_name", id, time.Now()); err == nil {
			t.Fatal("unbound/other-account email cannot be claimed")
		}
	}
}
func TestNumericOwnerRejectsDuplicateDisabledExpiredAndQuota(t *testing.T) {
	id := int64(42)
	for _, mode := range []string{"duplicate_id", "duplicate_email", "disabled", "expired", "quota"} {
		client := x3client.InboundClient{ID: "00000000-0000-4000-8000-000000000001", Email: "owned", Enable: true, TgID: x3client.FlexibleInt64{Value: &id}}
		clients := []x3client.InboundClient{client}
		switch mode {
		case "duplicate_id":
			clients = append(clients, client)
			clients[1].Email = "another"
		case "duplicate_email":
			clients = append(clients, client)
			clients[1].TgID.Value = nil
		case "disabled":
			clients[0].Enable = false
		case "expired":
			clients[0].ExpiryTime = time.Now().Add(-time.Second).UnixMilli()
		}
		inbound := numericInbound(t, clients)
		if mode == "quota" {
			inbound.ClientStats = []x3client.ClientStats{{Email: "owned", Enable: true, Total: 100, Up: 50, Down: 50}}
		}
		if _, _, err := ownedEmail(inbound, "new_name", id, time.Now()); err == nil {
			t.Fatalf("%s accepted", mode)
		}
	}
}
func TestNumericHelperPreservesUUIDAndSharesBotLock(t *testing.T) {
	id := int64(42)
	inboundID := 2
	uuid := "00000000-0000-4000-8000-000000000001"
	inbound := numericInbound(t, []x3client.InboundClient{{ID: uuid, Email: "old_name", Flow: "xtls-rprx-vision", Enable: true, TgID: x3client.FlexibleInt64{Value: &id}}})
	reads, writes := 0, 0
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/panel/inbound/list" {
			reads++
			json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []x3client.Inbound{*inbound}})
			return
		}
		writes++
		w.WriteHeader(500)
	}))
	defer httpServer.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sh := NewServerHandler("unused", nil, logger, serverpolicy.Policy{})
	defer sh.Close()
	sh.x3Clients[1] = &x3client.Client{Resty: resty.New().SetBaseURL(httpServer.URL), Logger: logger}
	server := &database.Server{ID: 1, IP: "fi.example", InboundID: &inboundID}
	key, err := sh.GetUserKeyForTelegramID(context.Background(), server, "new_name", id)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(key)
	if err != nil || u.User.Username() != uuid || u.Host != "fi.example:443" || u.Query().Get("flow") != "xtls-rprx-vision" || reads != 1 || writes != 0 {
		t.Fatal("owned FI credential changed or unexpected write")
	}
	lock := &sh.keyLocks[uint64(id)%uint64(len(sh.keyLocks))]
	lock.Lock()
	_, err = sh.GetUserKeyForTelegramID(context.Background(), server, "new_name", id)
	lock.Unlock()
	if err == nil || reads != 1 {
		t.Fatal("API must reject while same shared bot issuance lock is held")
	}
	if strings.Contains(key, "new_name") {
		t.Fatal("rename replaced original client identity")
	}
}

func TestNumericHelperNeverCreatesForNewOrDeletedClient(t *testing.T) {
	for _, mode := range []string{"never_provisioned", "deleted_after_retrieval"} {
		t.Run(mode, func(t *testing.T) {
			id := int64(42)
			inboundID := 2
			clients := []x3client.InboundClient{}
			if mode == "deleted_after_retrieval" {
				clients = append(clients, x3client.InboundClient{ID: "00000000-0000-4000-8000-000000000001", Email: "owned", Enable: true, TgID: x3client.FlexibleInt64{Value: &id}})
			}
			inbound := numericInbound(t, clients)
			writes := 0
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/panel/inbound/list" {
					writes++
					w.WriteHeader(500)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []x3client.Inbound{*inbound}})
			}))
			defer httpServer.Close()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			sh := NewServerHandler("unused", nil, logger, serverpolicy.Policy{})
			defer sh.Close()
			sh.x3Clients[1] = &x3client.Client{Resty: resty.New().SetBaseURL(httpServer.URL), Logger: logger}
			server := &database.Server{ID: 1, IP: "fi.example", InboundID: &inboundID}
			if mode == "deleted_after_retrieval" {
				key, err := sh.GetUserKeyForTelegramID(context.Background(), server, "owned", id)
				if err != nil || key == "" {
					t.Fatal("existing retrieval failed")
				}
				inbound = numericInbound(t, nil)
			}
			if _, err := sh.GetUserKeyForTelegramID(context.Background(), server, "owned", id); !errors.Is(err, ErrKeyNotFound) {
				t.Fatal("absent/deleted credential must remain not_found")
			}
			if err := sh.CheckUserKeyForTelegramID(context.Background(), server, "owned", id); !errors.Is(err, ErrKeyNotFound) {
				t.Fatal("status must not claim an absent credential is ready")
			}
			if writes != 0 {
				t.Fatal("Mini App recreated a deleted or never-provisioned client")
			}
		})
	}
}
