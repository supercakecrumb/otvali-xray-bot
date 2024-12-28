package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

type serverOutput struct {
	ID            int64
	Country       string
	City          string
	OnlineClients int
	AllClients    int
}

// Handle /get_key command
func (b *Bot) handleGetKey(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID

	// Fetch the list of servers and their user counts
	serverButtons, err := b.getServerButtons(chatID)
	if err != nil {
		b.logger.Error("Failed to get server buttons", "error", err)
		_, _ = bot.SendMessage(tu.Message(
			tu.ID(chatID),
			"Произошла ошибка при получении списка серверов. Пожалуйста, попробуйте позже.",
		))
		return
	}

	// Create inline keyboard with server buttons
	keyboard := tu.InlineKeyboard(serverButtons...)

	msg := tu.Message(
		tu.ID(chatID),
		"Выберите сервер для получения ключа:",
	).WithReplyMarkup(keyboard)

	_, err = bot.SendMessage(msg)
	if err != nil {
		b.logger.Error("Failed to send get_key message", "error", err)
	}
}

// Fetch server buttons with online and total user counts
func (b *Bot) getServerButtons(chatID int64) ([][]telego.InlineKeyboardButton, error) {
	// Build buttons
	var buttons [][]telego.InlineKeyboardButton
	// Fetch servers from the database
	servers, err := b.db.GetAllServers()
	if err != nil {
		b.logger.Error("Failed to fetch servers", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось получить список серверов.")
		_, _ = b.bot.SendMessage(msg)
		return nil, err
	}
	for _, server := range servers {
		// TODO: Parse the inbound settings to get total users

		// TODO: Count online users for this inbound

		// Create button text and callback data
		buttonText := fmt.Sprintf("%s %s, %s online:(%s/%s)", countryToFlag(server.Country), server.Country, server.City, "tbd", "tbd")
		callbackData := fmt.Sprintf("getkey_%d", server.ID)

		// Create the button
		button := tu.InlineKeyboardButton(buttonText).WithCallbackData(callbackData)

		// Add to buttons
		buttons = append(buttons, tu.InlineKeyboardRow(button))
	}

	return buttons, nil
}

func (b *Bot) getServerOutput(serverID int64) (*serverOutput, error) {
	so := serverOutput{
		ID:            serverID,
		Country:       "",
		City:          "",
		OnlineClients: 0,
		AllClients:    0,
	}

	return &so, nil
}

// Handle callback queries from /get_key command
func (b *Bot) handleGetKeyCallback(bot *telego.Bot, update telego.Update) {
	callbackQuery := update.CallbackQuery
	data := callbackQuery.Data
	chatID := callbackQuery.Message.GetChat().ID
	messageID := callbackQuery.Message.GetMessageID()

	if !strings.HasPrefix(data, "getkey_") {
		// Unknown callback data
		return
	}

	// Extract inbound ID from callback data
	var inboundID int
	_, err := fmt.Sscanf(data, "getkey_%d", &inboundID)
	if err != nil {
		b.logger.Error("Failed to parse inbound ID", "error", err)
		return
	}

	// Start generating the key
	go b.generateKeyProcess(bot, chatID, messageID, inboundID, callbackQuery)
}

// Generate key process with animated dots and message updates
func (b *Bot) generateKeyProcess(bot *telego.Bot, chatID int64, messageID int, inboundID int, callbackQuery *telego.CallbackQuery) {
	// Acknowledge the callback query to remove the loading animation
	err := bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQuery.ID,
	})
	if err != nil {
		b.logger.Error("Failed to answer callback query", "error", err)
	}

	// Initialize the message
	loadingText := "Генерирую ключ"
	msgText := loadingText
	editMsg := &telego.EditMessageTextParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
		Text:      msgText,
	}

	_, err = bot.EditMessageText(editMsg)
	if err != nil {
		b.logger.Error("Failed to edit message", "error", err)
		return
	}

	// Start the loading animation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.animateDots(bot, ctx, editMsg, loadingText)

	// Proceed to generate the key
	key, err := b.generateVLESSKey(chatID, inboundID)
	if err != nil {
		cancel() // Stop the animation
		errorMsg := fmt.Sprintf("Произошла ошибка при генерации ключа: %v", err)
		editMsg.Text = errorMsg
		_, _ = bot.EditMessageText(editMsg)
		return
	}

	cancel() // Stop the animation

	// Edit the message to show the generated key in monospace
	keyMsg := fmt.Sprintf("`%s`", key)
	editMsg.Text = keyMsg
	editMsg.ParseMode = telego.ModeMarkdownV2

	_, err = bot.EditMessageText(editMsg)
	if err != nil {
		b.logger.Error("Failed to edit message with key", "error", err)
		return
	}
}

// Animate dots in the loading message
func (b *Bot) animateDots(bot *telego.Bot, ctx context.Context, editMsg *telego.EditMessageTextParams, baseText string) {
	dots := []string{".", "..", "...", ""}
	i := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
			editMsg.Text = baseText + dots[i%len(dots)]
			_, err := bot.EditMessageText(editMsg)
			if err != nil {
				b.logger.Error("Failed to edit message during dots animation", "error", err)
				return
			}
			time.Sleep(300 * time.Millisecond)
			i++
		}
	}
}

// Generate VLESS key for the user
func (b *Bot) generateVLESSKey(chatID int64, inboundID int) (string, error) {
	time.Sleep(5 * time.Second)
	return "", fmt.Errorf("Хехехехех, я ещё не реализовала этот функционал подовитесь капиталисты")
}
