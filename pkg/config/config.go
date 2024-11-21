package config

import (
	"os"
)

type Config struct {
	TelegramToken string
	LogLevel      string
	DatabaseURL   string
	SSHKeyPath    string
}

func LoadConfig() Config {
	return Config{
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		LogLevel:      os.Getenv("LOG_LEVEL"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		SSHKeyPath:    os.Getenv("SSH_KEY_PATH"),
	}
}
