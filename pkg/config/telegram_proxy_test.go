package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proxyConfigFixture() map[string]any {
	return map[string]any{
		"secret_key": strings.Repeat("ab", 32), "allow_all_bound": true,
		"endpoints": []any{
			map[string]any{"id": "finland", "name": "Finland, Helsinki", "provider_id": "fi", "public_host": "fi.example.com", "public_port": 10443, "tls_domain": "cover.example.com", "primary": true},
			map[string]any{"id": "stockholm", "name": "Sweden, Stockholm", "provider_id": "sto", "public_host": "sto.example.com", "public_port": 8443, "tls_domain": "cover.example.com"},
			map[string]any{"id": "msk-finland", "name": "Moscow → Finland", "provider_id": "fi", "public_host": "msk.example.com", "public_port": 10443, "tls_domain": "cover.example.com"},
			map[string]any{"id": "msk-stockholm", "name": "Moscow → Stockholm", "provider_id": "sto", "public_host": "msk.example.com", "public_port": 11443, "tls_domain": "cover.example.com"},
		},
		"providers": []any{
			map[string]any{"id": "fi", "base_url": "http://127.0.0.1:9093", "authorization": "Bearer test-only-private-token", "ssh": map[string]any{"address": "fi.example.com:22", "user": "otvali-mtproto-api", "key_path": "/run/proxy/fi_key", "known_hosts_path": "/run/proxy/known_hosts"}},
			map[string]any{"id": "sto", "base_url": "http://127.0.0.1:9093", "authorization": "Bearer test-only-private-token", "ssh": map[string]any{"address": "sto.example.com:22", "user": "otvali-mtproto-api", "key_path": "/run/proxy/sto_key", "known_hosts_path": "/run/proxy/known_hosts"}},
		},
	}
}

func TestPrivateTelegramProxyConfig(t *testing.T) {
	data, _ := json.Marshal(proxyConfigFixture())
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "TELEGRAM_PROXY_ENABLED" {
			return "true"
		}
		if key == "TELEGRAM_PROXY_CONFIG_PATH" {
			return path
		}
		return ""
	}
	c, transports, err := loadTelegramProxy(getenv)
	if err != nil || len(c.Endpoints) != 4 || len(transports) != 2 || transports["fi"].TargetAddress != "127.0.0.1:9093" || !c.AllowAllBound {
		t.Fatalf("configuration not loaded: %v", err)
	}
	// A world-readable secret config must never be accepted at startup.
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadTelegramProxy(getenv); err == nil {
		t.Fatal("accepted world-readable secret configuration")
	}
}

func TestTelegramProxyRejectsUnsafeConfigWithoutLeakingSecrets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"public management", func(v map[string]any) {
			v["providers"].([]any)[0].(map[string]any)["base_url"] = "http://evil.example.com:9093"
		}},
		{"root ssh", func(v map[string]any) {
			v["providers"].([]any)[0].(map[string]any)["ssh"].(map[string]any)["user"] = "root"
		}},
		{"extra field", func(v map[string]any) { v["unexpected"] = "sensitive-value" }},
		{"missing key", func(v map[string]any) { v["secret_key"] = "" }},
		{"invalid allowed actor", func(v map[string]any) { v["allowed_telegram_ids"] = []int64{-1} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := proxyConfigFixture()
			tc.mutate(v)
			data, _ := json.Marshal(v)
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadTelegramProxy(func(key string) string {
				if key == "TELEGRAM_PROXY_ENABLED" {
					return "true"
				}
				return path
			})
			if err != errTelegramProxyConfig {
				t.Fatal("unsafe input must return only a fixed error")
			}
		})
	}
}

func TestDisabledTelegramProxyDoesNotReadSecrets(t *testing.T) {
	c, ssh, err := loadTelegramProxy(func(key string) string {
		if key == "TELEGRAM_PROXY_ENABLED" {
			return "false"
		}
		t.Fatal("disabled service read a secret path")
		return ""
	})
	if err != nil || c.Enabled || len(ssh) != 0 {
		t.Fatal("disabled configuration is not inert")
	}
}
