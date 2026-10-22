package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/miniapp"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"github.com/supercakecrumb/otvali-xray-bot/internal/runtimehealth"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegram"
	"github.com/supercakecrumb/otvali-xray-bot/internal/x3ui"
	"github.com/supercakecrumb/otvali-xray-bot/pkg/config"
	"github.com/supercakecrumb/otvali-xray-bot/pkg/logger"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		if runtimehealth.Check(runtimehealth.Path, time.Now()) != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(run())
}

func run() int {
	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	guideOption, err := telegram.WithVPNHelpURL(cfg.VPNHelpURL)
	if err != nil {
		slog.Error("Invalid VPN help URL")
		return 1
	}
	var miniAppOption telegram.Option
	if cfg.MiniApp.Enabled {
		miniAppOption, err = telegram.WithMiniAppURL(cfg.VPNHelpURL, cfg.MiniApp.AllowedOrigins)
		if err != nil {
			slog.Error("Invalid Mini App launch URL")
			return 1
		}
	}

	// Set up logger
	log := logger.New(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the database
	db, err := database.NewDB(cfg.DatabaseURL, log)
	if err != nil {
		log.Error("Failed to connect to database", slog.String("error", err.Error()))
		return 1
	}
	pool, err := db.Conn.DB()
	if err != nil {
		log.Error("Failed to initialize database runtime health")
		return 1
	}
	defer pool.Close()

	servers, err := db.GetAllServers()
	if err != nil {
		log.Error("Error getting servers", slog.String("error", err.Error()))
		return 1
	}
	// Initialize server Handler
	serverHandler := x3ui.NewServerHandler(cfg.SSHKeyPath, servers, log, cfg.ServerPolicy)
	if serverHandler == nil {
		log.Error("Failed to init serverHandler")
		return 1
	}
	defer func() {
		serverHandler.Close()
		log.Info("Shutdown complete")
	}()

	// Start the bot
	botOptions := []telegram.Option{guideOption, miniAppOption}
	var managed miniapp.ManagedIssuer
	if cfg.Stockholm.Enabled {
		client, err := remnawave.NewClient(cfg.Stockholm)
		if err != nil {
			log.Error("Invalid Stockholm configuration")
			return 1
		}
		service := remnawave.NewService(cfg.Stockholm, client, db)
		managed = service
		botOptions = append(botOptions, telegram.WithStockholm(service))
		log.Info("Stockholm subscriptions enabled", "eligible_ids", len(cfg.Stockholm.AllowedTelegramIDs))
	}
	accountService := miniapp.NewService(db, managed, serverHandler)
	botOptions = append(botOptions, telegram.WithAccountStatus(accountService))
	bot, err := telegram.NewBot(cfg.TelegramToken, log, db, serverHandler, botOptions...)
	if err != nil {
		log.Error("Failed to initialize bot", slog.String("error", err.Error()))
		return 1
	}
	defer bot.Stop()

	var apiServer *http.Server
	var apiErrors <-chan error
	if cfg.MiniApp.Enabled {
		handler, err := miniapp.NewHandler(cfg.MiniApp, cfg.TelegramToken, accountService, pool)
		if err != nil {
			log.Error("Invalid Mini App configuration")
			return 1
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(cfg.MiniApp.Port)))
		if err != nil {
			log.Error("Mini App listener unavailable")
			return 1
		}
		apiServer = miniapp.NewHTTPServer(listener.Addr().String(), handler)
		apiServer.BaseContext = func(net.Listener) context.Context { return ctx }
		failures := make(chan error, 1)
		apiErrors = failures
		go func() {
			err := apiServer.Serve(listener)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			failures <- err
		}()
		log.Info("Mini App API enabled")
	}

	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		runtimehealth.Monitor(ctx, pool, runtimehealth.Path, log)
	}()

	// Start the bot in a separate goroutine
	botDone := make(chan struct{})
	go func() {
		defer close(botDone)
		log.Info("Bot is starting...")
		bot.Start()
	}()

	exitCode := 0
	select {
	case <-ctx.Done():
	case <-apiErrors:
		if ctx.Err() == nil {
			log.Error("Mini App API stopped unexpectedly")
			exitCode = 1
		}
	case <-botDone:
		if ctx.Err() == nil {
			log.Error("Bot stopped unexpectedly")
			exitCode = 1
		}
	}
	stop()
	if apiServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if apiServer.Shutdown(shutdownCtx) != nil {
			_ = apiServer.Close()
		}
		cancel()
	}
	<-healthDone
	log.Info("Shutting down gracefully...")
	return exitCode
}
