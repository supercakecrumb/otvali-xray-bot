package config

import "testing"

func TestLoadConfigDisabledServerIDs(t *testing.T) {
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
	t.Setenv("BOT_DISABLED_SERVER_IDS", "7,not-an-id")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("invalid deployment configuration must fail startup")
	}
}
