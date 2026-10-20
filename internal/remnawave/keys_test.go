package remnawave

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testServerKey = "vless://00000000-0000-4000-8000-000000000001@exit.example:443?security=reality&flow=xtls-rprx-vision&type=tcp#Sweden%2C%20Stockholm"

func TestParseServerKeysPreservesCredentialAndOpaqueSelection(t *testing.T) {
	for _, body := range []string{testServerKey, base64.StdEncoding.EncodeToString([]byte(testServerKey + "\n" + testServerKey))} {
		keys, err := parseServerKeys([]byte(body))
		if err != nil || len(keys) != 1 || keys[0].URL != testServerKey || keys[0].Name != "Sweden, Stockholm" || len(keys[0].ID) != 16 || strings.Contains(keys[0].ID, "vless") {
			t.Fatal("single-key export must preserve the exact share link and use an opaque callback ID")
		}
		again, err := parseServerKeys([]byte(body))
		if err != nil || again[0] != keys[0] {
			t.Fatal("unchanged profiles must retain their selection identifier")
		}
	}
	for _, bad := range []string{"", "{}", "<html>login</html>", "not-base64", "vless://password@example.org:443", "vless://00000000-0000-0000-0000-000000000000@exit.example:443", "vless://00000000-0000-4000-8000-000000000001:secret@exit.example:443", strings.Replace(testServerKey, ":443", ":0", 1), testServerKey + "\nhttps://other.example/private", testServerKey + "%00"} {
		if keys, err := parseServerKeys([]byte(bad)); !errors.Is(err, ErrUnavailable) || len(keys) != 0 {
			t.Fatal("malformed or mixed subscription output must fail without returning credentials")
		}
	}
}

func TestReadSubscriptionRejectsRedirectsAndNeverSendsPanelCredential(t *testing.T) {
	requests := 0
	mode := "good"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "" || r.Header.Get("User-Agent") != "OtvaliBot/1.0" {
			t.Error("subscription requests must not carry the panel API credential")
		}
		switch mode {
		case "good":
			fmt.Fprint(w, base64.StdEncoding.EncodeToString([]byte(testServerKey)))
		case "redirect":
			w.Header().Set("Location", "/api/sub/other-private-link")
			w.WriteHeader(http.StatusFound)
		case "oversized":
			fmt.Fprint(w, strings.Repeat("x", 256*1024+1))
		case "error":
			http.Error(w, "private provider diagnostic", http.StatusForbidden)
		}
	}))
	defer server.Close()
	c, err := NewClient(Config{Enabled: true, BaseURL: "https://panel.example", SubscriptionOrigin: server.URL, APIToken: "synthetic-panel-secret"})
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = server.Client().Transport
	privateURL := server.URL + "/api/sub/private-link"
	keys, err := c.ReadSubscription(context.Background(), privateURL)
	if err != nil || len(keys) != 1 || keys[0].URL != testServerKey {
		t.Fatal("base64 profile download failed")
	}
	for _, modeName := range []string{"redirect", "oversized", "error"} {
		mode = modeName
		before := requests
		keys, err = c.ReadSubscription(context.Background(), privateURL)
		if !errors.Is(err, ErrUnavailable) || len(keys) != 0 || requests != before+1 || strings.Contains(err.Error(), "private") {
			t.Fatal("unsafe HTTP output must fail without following redirects or exposing URL/body")
		}
	}
	before := requests
	for _, unsafe := range []string{"http://wrong.example/api/sub/link", "https://wrong.example/api/sub/link", server.URL + "/api/users", privateURL + "?token=value", privateURL + "#fragment"} {
		if _, err := c.ReadSubscription(context.Background(), unsafe); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unapproved URL must fail before HTTP")
		}
	}
	if requests != before {
		t.Fatal("invalid subscription URL reached the network")
	}
}

type memoryKeyAPI struct {
	*memoryAPI
	reads int
}

func (m *memoryKeyAPI) ReadSubscription(_ context.Context, raw string) ([]ServerKey, error) {
	m.reads++
	if m.user == nil || raw != m.user.SubscriptionURL {
		return nil, ErrUnavailable
	}
	return parseServerKeys([]byte(testServerKey))
}

func TestKeyListingIssuesSameAccountAndRespectsRevocation(t *testing.T) {
	s, api, store := serviceFixture()
	reader := &memoryKeyAPI{memoryAPI: api}
	s.api = reader
	keys, err := s.Keys(context.Background(), 7)
	if err != nil || len(keys) != 1 || api.creates != 1 || !store.exists {
		t.Fatal("initial individual-key access must use the normal immutable grant")
	}
	mapping, expiry, url := store.mapping, api.user.ExpireAt, api.user.SubscriptionURL
	sub, err := s.Issue(context.Background(), 7)
	if err != nil || sub.URL != url {
		t.Fatal("subscription retrieval did not reuse the exported-key account")
	}
	again, err := s.Keys(context.Background(), 7)
	if err != nil || again[0] != keys[0] || api.creates != 1 || store.mapping != mapping || !api.user.ExpireAt.Equal(expiry) {
		t.Fatal("key export changed account, URL, expiry, mapping or profile")
	}
	api.user.Status = "DISABLED"
	before := reader.reads
	if keys, err := s.Keys(context.Background(), 7); !errors.Is(err, ErrInactive) || len(keys) != 0 || reader.reads != before {
		t.Fatal("disabled access must not download or expose saved profiles")
	}
	if _, err := s.Keys(context.Background(), 8); !errors.Is(err, ErrNotEligible) || api.creates != 1 || reader.reads != before {
		t.Fatal("another Telegram actor must not obtain keys or create access")
	}
}
