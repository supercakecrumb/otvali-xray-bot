package config

import (
	"errors"
	"os"

	"github.com/supercakecrumb/otvali-xray-bot/internal/miniapp"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"github.com/supercakecrumb/otvali-xray-bot/internal/serverpolicy"
	"github.com/supercakecrumb/otvali-xray-bot/internal/vpnmonitor"
)

type Config struct {
	TelegramToken string
	LogLevel      string
	DatabaseURL   string
	SSHKeyPath    string
	VPNHelpURL    string
	ServerPolicy  serverpolicy.Policy
	Stockholm     remnawave.Config
	MiniApp       miniapp.Config
	VPNMonitor    vpnmonitor.Config
}

func LoadConfig() (Config, error) {
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
		VPNMonitor:    monitor,
		MiniApp:       mini,
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		LogLevel:      os.Getenv("LOG_LEVEL"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		SSHKeyPath:    os.Getenv("SSH_KEY_PATH"),
		VPNHelpURL:    os.Getenv("VPN_HELP_URL"),
		ServerPolicy:  policy,
		Stockholm:     stockholm,
	}, nil
}
