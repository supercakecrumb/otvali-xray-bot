package telegram

import (
	"log/slog"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

type Bot struct {
	client *telego.Bot
	logger *slog.Logger
	db     *database.DB
}

func NewBot(token string, logger *slog.Logger, db *database.DB) (*Bot, error) {
	bot, err := telego.NewBot(token)
	if err != nil {
		return nil, err
	}

	return &Bot{
		client: bot,
		logger: logger,
		db:     db,
	}, nil
}

func (b *Bot) Start() {
	// Use UpdatesViaLongPolling to handle updates
	updates, err := b.client.UpdatesViaLongPolling(nil)
	if err != nil {
		b.logger.Error("Failed to start long polling", slog.String("error", err.Error()))
		return
	}
	defer b.client.StopLongPolling() // Ensure proper cleanup

	b.logger.Info("Bot is running and waiting for updates...")

	for update := range updates {
		if update.Message != nil {
			b.logger.Info("Received message", slog.String("text", update.Message.Text))
			// Handle commands here
			b.handleCommand(update.Message)
		}
	}
}

func (b *Bot) handleCommand(message *telego.Message) {
	chatID := message.Chat.ID
	username := message.From.Username

	// Enforce username
	if username == "" {
		err := b.sendMessage(chatID, "You must set a username to use this bot.\n\nTo set a username:\n1. Go to Telegram Settings.\n2. Tap 'Username'.\n3. Choose a unique username.")
		if err != nil {
			b.logger.Error("Failed to send username enforcement message", slog.String("error", err.Error()))
		}
		return
	}

	// Add or update the user in the database
	user := &database.User{
		ID:       chatID,
		Username: username,
	}
	if err := b.db.AddUser(user); err != nil {
		b.logger.Error("Failed to add user to database", slog.String("error", err.Error()))
		return
	}

	// Handle commands
	switch message.Text {
	case "/start":
		b.logger.Info("Processing /start command", slog.Int64("chat_id", chatID))
		err := b.sendMessage(chatID, "Welcome to the bot! Use /help to see available commands.")
		if err != nil {
			b.logger.Error("Failed to send /start response", slog.String("error", err.Error()))
		}
	case "/invite":
		b.handleInviteCommand(message)
	default:
		err := b.sendMessage(chatID, "Unknown command. Use /help to see available commands.")
		if err != nil {
			b.logger.Error("Failed to send unknown command response", slog.String("error", err.Error()))
		}
	}
}

// Helper function to send a message to a user
func (b *Bot) sendMessage(chatID int64, text string) error {
	msg := tu.Message(tu.ID(chatID), text)
	_, err := b.client.SendMessage(msg)
	return err
}
