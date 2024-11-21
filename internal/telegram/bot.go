package telegram

import (
	"log/slog"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

type Bot struct {
	bot    *telego.Bot
	logger *slog.Logger
	db     *database.DB
	bh     *th.BotHandler
}

func NewBot(token string, logger *slog.Logger, db *database.DB) (*Bot, error) {
	bot, err := telego.NewBot(token)
	if err != nil {
		return nil, err
	}

	return &Bot{
		bot:    bot,
		logger: logger,
		db:     db,
	}, nil
}

func (b *Bot) Start() {
	// Use UpdatesViaLongPolling to handle updates
	updates, err := b.bot.UpdatesViaLongPolling(nil)
	if err != nil {
		b.logger.Error("Failed to start long polling", slog.String("error", err.Error()))
		return
	}

	// Create bot handler and specify from where to get updates
	b.bh, err = th.NewBotHandler(b.bot, updates)
	if err != nil {
		b.logger.Error("Failed to create new bot handler", slog.String("error", err.Error()))
		return
	}

	defer b.bh.Stop()
	defer b.bot.StopLongPolling()

	// Middleware in case of panic and no username
	b.bh.Use(
		th.PanicRecovery(),
		b.userUsernameMiddleware(),
		b.userDatabaseMiddleware(),
	)

	b.registerCommands()

	b.bh.Start()
}
