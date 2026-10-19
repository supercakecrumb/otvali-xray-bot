package config

import (
	"os"

	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"github.com/supercakecrumb/otvali-xray-bot/internal/serverpolicy"
)

type Config struct {
	TelegramToken string
	LogLevel      string
	DatabaseURL   string
	SSHKeyPath    string
	ServerPolicy  serverpolicy.Policy
	Stockholm     remnawave.Config
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
	return Config{
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		LogLevel:      os.Getenv("LOG_LEVEL"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		SSHKeyPath:    os.Getenv("SSH_KEY_PATH"),
		ServerPolicy:  policy,
		Stockholm:     stockholm,
	}, nil
}
