package telegram

import (
	"log/slog"

	"github.com/mymmrac/telego"
)

type Bot struct {
	client *telego.Bot
	logger *slog.Logger
}

func NewBot(token string, logger *slog.Logger) (*Bot, error) {
	bot, err := telego.NewBot(token, telego.WithDefaultDebugLogger())
	if err != nil {
		return nil, err
	}

	return &Bot{
		client: bot,
		logger: logger,
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

// Command handler for the bot
func (b *Bot) handleCommand(message *telego.Message) {
	command := message.Text
	chatID := message.Chat.ID

	switch command {
	case "/start":
		b.logger.Info("Processing /start command", slog.Int64("chat_id", chatID))
		err := b.sendMessage(chatID, "Welcome to the bot! Use /help to see available commands.")
		if err != nil {
			b.logger.Error("Failed to send /start response", slog.String("error", err.Error()))
		}
	case "/help":
		b.logger.Info("Processing /help command", slog.Int64("chat_id", chatID))
		err := b.sendMessage(chatID, "Here are the available commands:\n/start - Start the bot\n/help - Show this help message")
		if err != nil {
			b.logger.Error("Failed to send /help response", slog.String("error", err.Error()))
		}
	default:
		b.logger.Info("Unknown command received", slog.Int64("chat_id", chatID), slog.String("command", command))
		err := b.sendMessage(chatID, "Sorry, I didn't understand that command. Use /help to see available commands.")
		if err != nil {
			b.logger.Error("Failed to send unknown command response", slog.String("error", err.Error()))
		}
	}
}

// Helper function to send a message to a user
func (b *Bot) sendMessage(chatID int64, text string) error {
	_, err := b.client.SendMessage(&telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text:   text,
	})
	return err
}
