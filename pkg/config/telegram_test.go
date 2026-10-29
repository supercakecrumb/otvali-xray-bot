package config

import (
	"strings"
	"testing"
)

func TestLoadConfigAdditionalTelegramTokenEnvironment(t *testing.T) {
	primary := "101:" + strings.Repeat("a", 35)
	secondary := "202:" + strings.Repeat("b", 35)
	t.Setenv("TELEGRAM_TOKEN", primary)
	t.Setenv("TELEGRAM_ADDITIONAL_TOKENS", secondary)
	t.Setenv("REMNA_ENABLED", "false")
	t.Setenv("MINIAPP_ENABLED", "false")
	t.Setenv("VPN_MONITOR_ENABLED", "false")
	t.Setenv("BOT_DISABLED_SERVER_IDS", "")
	cfg, err := LoadConfig()
	if err != nil || cfg.TelegramToken != primary || len(cfg.TelegramAdditionalTokens) != 1 || cfg.TelegramAdditionalTokens[0] != secondary {
		t.Fatal("additional-token environment must leave the legacy token intact")
	}
}

func TestAdditionalTelegramTokensKeepLegacyPrimary(t *testing.T) {
	primary := "101:" + strings.Repeat("a", 35)
	secondary := "202:" + strings.Repeat("b", 35)
	third := "303:" + strings.Repeat("c", 35)
	additional, err := parseAdditionalTelegramTokens(primary, "  "+secondary+",\n"+third+"\t")
	if err != nil || len(additional) != 2 || additional[0] != secondary || additional[1] != third {
		t.Fatal("optional bots must parse without replacing the legacy primary")
	}
	cfg := Config{TelegramToken: primary, TelegramAdditionalTokens: additional}
	tokens := cfg.TelegramTokens()
	if len(tokens) != 3 || tokens[0] != primary || tokens[1] != secondary {
		t.Fatal("legacy bot must remain the first sender")
	}
	tokens[1] = "changed"
	if cfg.TelegramAdditionalTokens[0] != secondary {
		t.Fatal("returned bot list must not expose mutable config storage")
	}
	if got, err := parseAdditionalTelegramTokens(primary, ""); err != nil || len(got) != 0 {
		t.Fatal("single-bot deployment must remain supported")
	}
}

func TestAdditionalTelegramTokensRejectDuplicateIdentityAndMalformedConfig(t *testing.T) {
	primary := "101:" + strings.Repeat("a", 35)
	secondary := "202:" + strings.Repeat("b", 35)
	for _, raw := range []string{
		primary, "101:" + strings.Repeat("z", 35), secondary + "," + secondary,
		"private-invalid-token", "0:" + strings.Repeat("c", 35),
		"9223372036854775808:" + strings.Repeat("c", 35),
		strings.Repeat(secondary+",", 8),
	} {
		if _, err := parseAdditionalTelegramTokens(primary, raw); err == nil || strings.Contains(err.Error(), raw) {
			t.Fatal("invalid or duplicate bot configuration must fail without exposing tokens")
		}
	}
}
