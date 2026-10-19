package remnawave

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientAuthenticatesAndNeverExposesProviderErrors(t *testing.T) {
	const token = "synthetic-private-token"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.UserAgent() == "" {
			t.Error("missing authentication or user agent")
		}
		switch r.URL.Path {
		case "/api/users/by-username/otvali_tg_7":
			fmt.Fprint(w, `{"response":{"id":12,"username":"otvali_tg_7"}}`)
		case "/api/users/by-username/missing":
			w.WriteHeader(404)
		default:
			w.WriteHeader(500)
			fmt.Fprint(w, token+" https://sub.example/api/sub/private-grant")
		}
	}))
	defer server.Close()
	c, err := NewClient(Config{Enabled: true, BaseURL: server.URL, APIToken: token})
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = server.Client().Transport
	if u, err := c.FindUser(context.Background(), "otvali_tg_7"); err != nil || u.ID != 12 {
		t.Fatal("numeric user ID was not decoded")
	}
	if _, err := c.FindUser(context.Background(), "missing"); err != ErrNotFound {
		t.Fatal("404 must be distinguishable")
	}
	if _, err := c.CreateUser(context.Background(), CreateUserRequest{}); err != ErrUnavailable || strings.Contains(err.Error(), token) {
		t.Fatal("provider error leaked")
	}
}

func TestClientDoesNotFollowCredentialBearingRedirects(t *testing.T) {
	hits := 0
	dest := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer dest.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c, _ := NewClient(Config{Enabled: true, BaseURL: source.URL, APIToken: "synthetic"})
	c.http.Transport = source.Client().Transport
	if _, err := c.FindUser(context.Background(), "test"); err != ErrUnavailable || hits != 0 {
		t.Fatal("credential request followed a redirect")
	}
}

func TestConfigDisabledAndInvalidInputsFailClosed(t *testing.T) {
	env := map[string]string{"REMNA_ENABLED": "true", "REMNA_BASE_URL": "https://panel.example", "REMNA_API_TOKEN": "synthetic", "REMNA_SQUAD_UUID": "0306677b-2434-4918-bf55-2d5147e87c17", "REMNA_SUBSCRIPTION_ORIGIN": "https://sub.example", "REMNA_ALLOWED_TELEGRAM_IDS": "7,8"}
	get := func(k string) string { return env[k] }
	if c, err := LoadConfig(get); err != nil || !c.Enabled || len(c.AllowedTelegramIDs) != 2 || c.AccessDays != 30 {
		t.Fatal("valid config rejected")
	}
	for key, value := range map[string]string{"REMNA_ENABLED": "yes", "REMNA_BASE_URL": "http://panel.example", "REMNA_API_TOKEN": "", "REMNA_SQUAD_UUID": "bad", "REMNA_SUBSCRIPTION_ORIGIN": "https://user:secret@sub.example", "REMNA_ALLOWED_TELEGRAM_IDS": "all", "REMNA_ACCESS_DAYS": "-1"} {
		old := env[key]
		env[key] = value
		if _, err := LoadConfig(get); err == nil {
			t.Errorf("invalid %s accepted", key)
		}
		env[key] = old
	}
	env["REMNA_ENABLED"] = "false"
	env["REMNA_BASE_URL"] = "malformed"
	if c, err := LoadConfig(get); err != nil || c.Enabled {
		t.Fatal("disabled feature must not require credentials")
	}
}
