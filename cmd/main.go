package main

import (
	"log/slog"

	"github.com/supercakecrumb/otvali-xray-bot/internal/telegram"
	"github.com/supercakecrumb/otvali-xray-bot/pkg/config"
	"github.com/supercakecrumb/otvali-xray-bot/pkg/logger"
)

func main() {
	// Load configuration
	cfg := config.LoadConfig()

	// Set up logger
	log := logger.New(cfg.LogLevel)

	// Start the bot
	bot, err := telegram.NewBot(cfg.TelegramToken, log)
	if err != nil {
		slog.Error("Failed to initialize bot", slog.String("error", err.Error()))
		return
	}

	log.Info("Bot started")
	bot.Start()
}
