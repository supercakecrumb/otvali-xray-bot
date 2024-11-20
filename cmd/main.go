package main

import (
	"log/slog"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegram"
	"github.com/supercakecrumb/otvali-xray-bot/pkg/config"
	"github.com/supercakecrumb/otvali-xray-bot/pkg/logger"
)

func main() {
	// Load configuration
	cfg := config.LoadConfig()

	// Set up logger
	log := logger.New(cfg.LogLevel)

	// Initialize the database
	db, err := database.NewDB(cfg.DatabaseURL, log)
	if err != nil {
		log.Error("Failed to connect to database", slog.String("error", err.Error()))
		return
	}

	// Start the bot
	bot, err := telegram.NewBot(cfg.TelegramToken, log, db)
	if err != nil {
		log.Error("Failed to initialize bot", slog.String("error", err.Error()))
		return
	}

	log.Info("Bot started")
	bot.Start()
}
