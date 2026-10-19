package config

import (
	"os"

	"github.com/supercakecrumb/otvali-xray-bot/internal/serverpolicy"
)

type Config struct {
	TelegramToken string
	LogLevel      string
	DatabaseURL   string
	SSHKeyPath    string
	ServerPolicy  serverpolicy.Policy
}

func LoadConfig() (Config, error) {
	policy, err := serverpolicy.ParseDisabledServerIDs(os.Getenv("BOT_DISABLED_SERVER_IDS"))
	if err != nil {
		return Config{}, err
	}
	return Config{
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		LogLevel:      os.Getenv("LOG_LEVEL"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		SSHKeyPath:    os.Getenv("SSH_KEY_PATH"),
		ServerPolicy:  policy,
	}, nil
}
