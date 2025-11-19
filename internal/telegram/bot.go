package telegram

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/x3ui"
)

type Bot struct {
	bot    *telego.Bot
	logger *slog.Logger
	db     *database.DB
	bh     *th.BotHandler
	sh     *x3ui.ServerHandler
}

func NewBot(token string, logger *slog.Logger, db *database.DB, serverHandler *x3ui.ServerHandler) (*Bot, error) {
	bot, err := telego.NewBot(token)
	if err != nil {
		return nil, err
	}

	return &Bot{
		bot:    bot,
		logger: logger,
		db:     db,
		sh:     serverHandler,
	}, nil
}

func (b *Bot) Start() {
	b.logger.Info("Starting bot...")

	// Notify admins about the shutdown
	b.NotifyAdmins("⚠️ The bot is starting.")

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

	b.registerAdminCommands()

	b.bh.Start()
}

func (b *Bot) Stop() {
	b.logger.Info("Stopping bot...")

	// Notify admins about the shutdown
	b.NotifyAdmins("⚠️ The bot is stopping. Please check the server for details.")

	// Stop the bot handler
	b.bh.Stop()
}

// NotifyAdmins sends a message to all admins
func (b *Bot) NotifyAdmins(message string) {
	admins, err := b.db.GetAdminUsers() // Fetch admin users from the database
	if err != nil {
		b.logger.Error("Failed to fetch admin users", slog.String("error", err.Error()))
		return
	}

	for _, admin := range admins {
		_, err := b.bot.SendMessage(tu.Message(
			tu.ID(*admin.TelegramID),
			message,
		))
		if err != nil {
			b.logger.Error("Failed to notify admin", slog.String("username", admin.Username), slog.String("error", err.Error()))
		} else {
			b.logger.Info("Notified admin", slog.String("username", admin.Username))
		}
	}
}

// NotifyAdminsOfAction sends a structured notification about a user action
func (b *Bot) NotifyAdminsOfAction(username string, chatID int64, action string, details string) {
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	message := fmt.Sprintf(
		"✅ *Действие пользователя*\n\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Chat ID: `%d`\n"+
			"⚡ Действие: %s\n"+
			"📝 Детали: %s\n"+
			"🕐 Время: %s",
		username,
		chatID,
		action,
		details,
		timestamp,
	)

	b.logger.Info("User action",
		slog.String("username", username),
		slog.Int64("chat_id", chatID),
		slog.String("action", action),
		slog.String("details", details),
	)

	b.sendFormattedNotification(message)
}

// NotifyAdminsOfError sends a structured notification about an error
func (b *Bot) NotifyAdminsOfError(username string, chatID int64, action string, errorMsg string, context string) {
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	message := fmt.Sprintf(
		"❌ *Ошибка пользователя*\n\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Chat ID: `%d`\n"+
			"⚡ Действие: %s\n"+
			"📝 Контекст: %s\n"+
			"🚨 Ошибка: `%s`\n"+
			"🕐 Время: %s",
		username,
		chatID,
		action,
		context,
		errorMsg,
		timestamp,
	)

	b.logger.Error("User error",
		slog.String("username", username),
		slog.Int64("chat_id", chatID),
		slog.String("action", action),
		slog.String("context", context),
		slog.String("error", errorMsg),
	)

	b.sendFormattedNotification(message)
}

// NotifyAdminsOfCommand sends a notification about a command execution
func (b *Bot) NotifyAdminsOfCommand(username string, chatID int64, command string, args string) {
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	argsText := "нет"
	if args != "" {
		argsText = args
	}

	message := fmt.Sprintf(
		"⚡ *Команда выполнена*\n\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Chat ID: `%d`\n"+
			"💬 Команда: `%s`\n"+
			"📋 Аргументы: %s\n"+
			"🕐 Время: %s",
		username,
		chatID,
		command,
		argsText,
		timestamp,
	)

	b.logger.Info("Command executed",
		slog.String("username", username),
		slog.Int64("chat_id", chatID),
		slog.String("command", command),
		slog.String("args", args),
	)

	b.sendFormattedNotification(message)
}

// NotifyAdminsOfKeyRequest sends a notification about a key request
func (b *Bot) NotifyAdminsOfKeyRequest(username string, chatID int64, serverName string, success bool, errorMsg string) {
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	var message string
	if success {
		message = fmt.Sprintf(
			"🔑 *Ключ выдан*\n\n"+
				"👤 Пользователь: @%s\n"+
				"🆔 Chat ID: `%d`\n"+
				"🖥 Сервер: %s\n"+
				"✅ Статус: Успешно\n"+
				"🕐 Время: %s",
			username,
			chatID,
			serverName,
			timestamp,
		)

		b.logger.Info("Key generated successfully",
			slog.String("username", username),
			slog.Int64("chat_id", chatID),
			slog.String("server", serverName),
		)
	} else {
		message = fmt.Sprintf(
			"🔑 *Ошибка выдачи ключа*\n\n"+
				"👤 Пользователь: @%s\n"+
				"🆔 Chat ID: `%d`\n"+
				"🖥 Сервер: %s\n"+
				"❌ Статус: Ошибка\n"+
				"🚨 Ошибка: `%s`\n"+
				"🕐 Время: %s",
			username,
			chatID,
			serverName,
			errorMsg,
			timestamp,
		)

		b.logger.Error("Key generation failed",
			slog.String("username", username),
			slog.Int64("chat_id", chatID),
			slog.String("server", serverName),
			slog.String("error", errorMsg),
		)
	}

	b.sendFormattedNotification(message)
}

// sendFormattedNotification sends a formatted notification to all admins with Markdown parsing
func (b *Bot) sendFormattedNotification(message string) {
	admins, err := b.db.GetAdminUsers()
	if err != nil {
		b.logger.Error("Failed to fetch admin users", slog.String("error", err.Error()))
		return
	}

	for _, admin := range admins {
		msg := tu.Message(
			tu.ID(*admin.TelegramID),
			message,
		).WithParseMode(telego.ModeMarkdown)

		_, err := b.bot.SendMessage(msg)
		if err != nil {
			b.logger.Error("Failed to notify admin",
				slog.String("username", admin.Username),
				slog.String("error", err.Error()))
		}
	}
}
