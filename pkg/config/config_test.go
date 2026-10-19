package config

import "testing"

func TestLoadConfigDisabledServerIDs(t *testing.T) {
	t.Setenv("REMNA_ENABLED", "false")
	t.Setenv("BOT_DISABLED_SERVER_IDS", "7,12")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ServerPolicy.IsDisabled(7) || !cfg.ServerPolicy.IsDisabled(12) || cfg.ServerPolicy.IsDisabled(8) {
		t.Fatal("environment policy must disable only the configured IDs")
	}
}

func TestLoadConfigRejectsMalformedDisabledServerIDs(t *testing.T) {
	t.Setenv("REMNA_ENABLED", "false")
	t.Setenv("BOT_DISABLED_SERVER_IDS", "7,not-an-id")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("invalid deployment configuration must fail startup")
	}
}

func TestLoadConfigRejectsIncompleteStockholmPilot(t *testing.T) {
	t.Setenv("BOT_DISABLED_SERVER_IDS", "3")
	t.Setenv("REMNA_ENABLED", "true")
	t.Setenv("REMNA_BASE_URL", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("an enabled pilot with incomplete configuration must fail startup")
	}
}

func TestLoadConfigStockholmPilot(t *testing.T) {
	t.Setenv("BOT_DISABLED_SERVER_IDS", "3")
	t.Setenv("REMNA_ENABLED", "true")
	t.Setenv("REMNA_BASE_URL", "https://panel.example.com")
	t.Setenv("REMNA_API_TOKEN", "test-api-token")
	t.Setenv("REMNA_SQUAD_UUID", "8d6dc453-818e-41f4-bb2f-f90c042a4dfd")
	t.Setenv("REMNA_SUBSCRIPTION_ORIGIN", "https://sub.example.com")
	t.Setenv("REMNA_ALLOWED_TELEGRAM_IDS", "1234")
	t.Setenv("REMNA_ACCESS_DAYS", "30")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Stockholm.Enabled || cfg.Stockholm.AccessDays != 30 || !cfg.ServerPolicy.IsDisabled(3) {
		t.Fatal("pilot configuration and the retired-server policy must both be loaded")
	}
	if _, allowed := cfg.Stockholm.AllowedTelegramIDs[1234]; !allowed {
		t.Fatal("the explicit pilot allowlist must be passed to the service")
	}
}
