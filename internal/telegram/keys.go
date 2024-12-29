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
		buttonText := fmt.Sprintf("%s %s, %s", countryToFlag(server.Country), server.Country, server.City)
		callbackData := fmt.Sprintf("getkey_%d", server.ID)

		// Create the button
		button := tu.InlineKeyboardButton(buttonText).WithCallbackData(callbackData)

		// Add to buttons
		buttons = append(buttons, tu.InlineKeyboardRow(button))
	}

	return buttons, nil
}

// Handle callback queries from /get_key command
func (b *Bot) handleGetKeyCallback(bot *telego.Bot, update telego.Update) {
	callbackQuery := update.CallbackQuery
	data := callbackQuery.Data

	if !strings.HasPrefix(data, "getkey_") {
		// Unknown callback data
		return
	}

	// Extract inbound ID from callback data
	var serverID int
	_, err := fmt.Sscanf(data, "getkey_%d", &serverID)
	if err != nil {
		b.logger.Error("Failed to parse inbound ID", "error", err)
		return
	}

	// Start generating the key
	go b.generateKeyProcess(serverID, callbackQuery)
}

// Generate key process with animated dots and message updates
func (b *Bot) generateKeyProcess(serverID int, callbackQuery *telego.CallbackQuery) {
	chatID := callbackQuery.Message.GetChat().ID
	messageID := callbackQuery.Message.GetMessageID()
	username := callbackQuery.Message.GetChat().Username

	// Acknowledge the callback query to remove the loading animation
	err := b.bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQuery.ID,
	})
	if err != nil {
		b.logger.Error("Failed to answer callback query", "error", err)
	}

	keyMsg, cancel, err := b.sendMessageWithAnimatedDots(chatID, messageID, "Генерирую ключ")
	if err != nil {
		errorMsg := fmt.Sprintf("Произошла ошибка при генерации ключа: %v", err)
		keyMsg.Text = errorMsg
		_, _ = b.bot.EditMessageText(keyMsg)
		return
	}

	defer cancel()

	server, err := b.db.GetServerByID(int64(serverID))
	if err != nil {
		b.logger.Error("error getting server from db", slog.String("error", err.Error()))
	}
	// Proceed to generate the key
	key, err := b.sh.GetClientKey(server, username)
	if err != nil {
		cancel() // Stop the animation
		errorMsg := fmt.Sprintf("Произошла ошибка при генерации ключа: %v", err)
		keyMsg.Text = errorMsg
		_, _ = b.bot.EditMessageText(keyMsg)
		return
	}

	cancel() // Stop the animation

	// Edit the message to show the generated key in monospace
	keyText := fmt.Sprintf("`%s`", key)
	keyMsg.Text = keyText
	keyMsg.ParseMode = telego.ModeMarkdownV2

	_, err = b.bot.EditMessageText(keyMsg)
	if err != nil {
		b.logger.Error("Failed to edit message with key", "error", err)
		return
	}
}

func (b *Bot) sendMessageWithAnimatedDots(chatID int64, messageID int, loadingText string) (*telego.EditMessageTextParams, context.CancelFunc, error) {
	editMsg := &telego.EditMessageTextParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
		Text:      loadingText,
	}

	_, err := b.bot.EditMessageText(editMsg)
	if err != nil {
		b.logger.Error("Failed to edit message", "error", err)
		return editMsg, nil, err
	}

	// Start the loading animation
	ctx, cancel := context.WithCancel(context.Background())
	go b.animateDots(ctx, editMsg, loadingText)

	return editMsg, cancel, nil
}

// Animate dots in the loading message
func (b *Bot) animateDots(ctx context.Context, editMsg *telego.EditMessageTextParams, baseText string) {
	dots := []string{".", "..", "...", ""}
	i := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
			editMsg.Text = baseText + dots[i%len(dots)]
			_, err := b.bot.EditMessageText(editMsg)
			if err != nil {
				b.logger.Error("Failed to edit message during dots animation", "error", err)
				return
			}
			time.Sleep(300 * time.Millisecond)
			i++
		}
	}
}
