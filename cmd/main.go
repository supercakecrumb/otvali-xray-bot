package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegram"
	"github.com/supercakecrumb/otvali-xray-bot/internal/x3ui"
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

	servers, err := db.GetAllServers()
	if err != nil {
		log.Error("Error getting servers", slog.String("error", err.Error()))
		os.Exit(1)
	}
	// Initialize server Handler
	serverHandler := x3ui.NewServerHandler(cfg.SSHKeyPath, servers, log)
	if serverHandler == nil {
		log.Error("Failed to init serverHandler")
		os.Exit(1)
	}

	// Start the bot
	bot, err := telegram.NewBot(cfg.TelegramToken, log, db, serverHandler)
	if err != nil {
		log.Error("Failed to initialize bot", slog.String("error", err.Error()))
		return
	}

	// Create a context that is cancelled on OS interrupt or terminate signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start the bot in a separate goroutine
	go func() {
		log.Info("Bot is starting...")
		bot.Start()
	}()

	// Wait for the context to be cancelled (signal received)
	<-ctx.Done()
	log.Info("Shutting down gracefully...")

	// Stop the bot handler
	bot.Stop()

	// Close the ServerHandler to clean up SSH connections
	serverHandler.Close()

	log.Info("Shutdown complete")
}
