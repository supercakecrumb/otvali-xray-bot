package config

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/supercakecrumb/otvali-xray-bot/internal/miniapp"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"github.com/supercakecrumb/otvali-xray-bot/internal/serverpolicy"
	"github.com/supercakecrumb/otvali-xray-bot/internal/vpnmonitor"
)

type Config struct {
	TelegramToken            string
	TelegramAdditionalTokens []string
	LogLevel                 string
	DatabaseURL              string
	SSHKeyPath               string
	VPNHelpURL               string
	ServerPolicy             serverpolicy.Policy
	Stockholm                remnawave.Config
	MiniApp                  miniapp.Config
	VPNMonitor               vpnmonitor.Config
}

func LoadConfig() (Config, error) {
	additionalTokens, err := parseAdditionalTelegramTokens(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_ADDITIONAL_TOKENS"))
	if err != nil {
		return Config{}, err
	}
	policy, err := serverpolicy.ParseDisabledServerIDs(os.Getenv("BOT_DISABLED_SERVER_IDS"))
	if err != nil {
		return Config{}, err
	}
	stockholm, err := remnawave.LoadConfig(os.Getenv)
	if err != nil {
		return Config{}, err
	}
	mini, err := miniapp.LoadConfig(os.Getenv)
	if err != nil {
		return Config{}, err
	}
	monitor, err := vpnmonitor.LoadConfig(os.Getenv)
	if err != nil {
		return Config{}, err
	}
	if monitor.Enabled && !mini.Enabled {
		return Config{}, errors.New("VPN monitor requires the Mini App HTTP listener")
	}
	return Config{
		VPNMonitor:               monitor,
		MiniApp:                  mini,
		TelegramToken:            os.Getenv("TELEGRAM_TOKEN"),
		TelegramAdditionalTokens: additionalTokens,
		LogLevel:                 os.Getenv("LOG_LEVEL"),
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		SSHKeyPath:               os.Getenv("SSH_KEY_PATH"),
		VPNHelpURL:               os.Getenv("VPN_HELP_URL"),
		ServerPolicy:             policy,
		Stockholm:                stockholm,
	}, nil
}

// TelegramTokens keeps the legacy bot first: it remains the sender for monitoring
// and administrative notifications while all bots share the same accounts.
func (c Config) TelegramTokens() []string {
	return append([]string{c.TelegramToken}, c.TelegramAdditionalTokens...)
}

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{35}$`)

func parseAdditionalTelegramTokens(primary, raw string) ([]string, error) {
	tokens := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' })
	if len(tokens) == 0 {
		return nil, nil
	}
	if len(tokens) > 7 {
		return nil, errors.New("at most eight Telegram bots may share one runtime")
	}
	seen := map[int64]bool{}
	for _, token := range append([]string{primary}, tokens...) {
		if !telegramTokenPattern.MatchString(token) {
			return nil, errors.New("invalid Telegram bot configuration")
		}
		id, err := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
		if err != nil || id <= 0 || seen[id] {
			return nil, errors.New("Telegram bots must have distinct positive identities")
		}
		seen[id] = true
	}
	return tokens, nil
}
