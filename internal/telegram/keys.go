package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

var backHomeKeyboard = tu.InlineKeyboard(
	tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("⚙️ Установка VPN").WithCallbackData(CallbackHelpVPNSetup),
	),
	tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("⬅️ Отдельные ключи").WithCallbackData(CallbackIndividualKeys),
	),
)

// Handle /get_key command
func (b *Bot) handleGetKey(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID
	username := update.Message.From.Username

	// Notify admins about command usage
	b.NotifyAdminsOfCommand(username, chatID, "/get_key", "")

	// Fetch the list of servers and their user counts
	serverButtons, err := b.getServerButtons(chatID)
	if err != nil {
		b.logger.Error("Failed to get server buttons", "error", err)
		_, _ = bot.SendMessage(tu.Message(
			tu.ID(chatID),
			"Произошла ошибка при получении списка серверов. Пожалуйста, попробуйте позже.",
		))
		b.NotifyAdminsOfError(username, chatID, "/get_key", err.Error(), "Не удалось получить список серверов")
		return
	}

	// Create inline keyboard with server buttons
	keyboard := tu.InlineKeyboard(serverButtons...)

	msg := tu.Message(
		tu.ID(chatID),
		accessText,
	).WithReplyMarkup(keyboard)

	_, err = bot.SendMessage(msg)
	if err != nil {
		b.logger.Error("Failed to send get_key message", "error", err)
		b.NotifyAdminsOfError(username, chatID, "/get_key", err.Error(), "Не удалось отправить список серверов")
	}
}

// The access screen is independent of legacy panel availability.
func (b *Bot) getServerButtons(chatID int64) ([][]telego.InlineKeyboardButton, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return b.accessButtons(ctx, chatID), nil
}

func (b *Bot) buildServerButtons(servers []database.Server) [][]telego.InlineKeyboardButton {
	var buttons [][]telego.InlineKeyboardButton
	for _, server := range servers {
		if b.sh.IsServerDisabled(server.ID) {
			continue
		}
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

	backRow := tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack),
	)

	buttons = append(buttons, backRow)

	return buttons
}

// Handle callback queries from /get_key command
func (b *Bot) handleGetKeyCallback(bot *telego.Bot, update telego.Update) {
	callbackQuery := update.CallbackQuery
	if callbackQuery == nil || callbackQuery.Message == nil {
		return
	}
	data := callbackQuery.Data
	chatID := callbackQuery.Message.GetChat().ID
	msgID := callbackQuery.Message.GetMessageID()
	username := callbackQuery.Message.GetChat().Username

	if !strings.HasPrefix(data, "getkey_") {
		// Unknown callback data
		return
	}

	// Check if callback data matches "getkey_" exactly
	if data == CallbackGetKey {
		if !b.acknowledgePrivateCallback(bot, update) {
			return
		}
		serverButtons, err := b.getServerButtons(chatID)
		if err != nil {
			b.logger.Error("Failed to get server buttons", "error", err)
			_, _ = bot.SendMessage(tu.Message(
				tu.ID(chatID),
				"Произошла ошибка при получении списка серверов. Пожалуйста, попробуйте позже.",
			))
			b.NotifyAdminsOfError(username, chatID, "get_key_callback", err.Error(), "Не удалось получить список серверов для повторного выбора")
			return
		}

		// Create inline keyboard with server buttons
		keyboard := tu.InlineKeyboard(serverButtons...)

		editMsg := &telego.EditMessageTextParams{
			ChatID:      tu.ID(chatID),
			MessageID:   msgID,
			Text:        accessText,
			ReplyMarkup: keyboard,
		}

		b.bot.EditMessageText(editMsg)

		return
	}

	// Extract inbound ID from callback data
	var serverID int
	_, err := fmt.Sscanf(data, "getkey_%d", &serverID)
	if err != nil {
		b.logger.Error("Failed to parse inbound ID", "error", err)
		b.NotifyAdminsOfError(username, chatID, "get_key_callback", err.Error(), fmt.Sprintf("Не удалось распарсить server ID из callback data: %s", data))
		return
	}

	// Old Telegram messages retain their callbacks after a server is retired.
	// Reject them before key generation or error notifications to administrators.
	if b.sh.IsServerDisabled(int64(serverID)) {
		b.rejectDisabledServer(bot, update)
		return
	}

	// Notify admins about server selection
	b.NotifyAdminsOfAction(username, chatID, "server_selected", fmt.Sprintf("Пользователь выбрал сервер ID: %d для получения ключа", serverID))

	// Start generating the key
	go b.generateKeyProcess(serverID, update)
}

func (b *Bot) rejectDisabledServer(bot *telego.Bot, update telego.Update) {
	const text = "Этот сервер больше недоступен. Выберите другой сервер."
	callback := update.CallbackQuery
	if err := bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{
		CallbackQueryID: callback.ID,
		Text:            text,
		ShowAlert:       true,
	}); err != nil {
		b.logger.Error("Failed to acknowledge disabled server selection", "error", err)
	}
	_, err := bot.EditMessageText(&telego.EditMessageTextParams{
		ChatID:    tu.ID(callback.Message.GetChat().ID),
		MessageID: callback.Message.GetMessageID(),
		Text:      text,
		ReplyMarkup: tu.InlineKeyboard(
			tu.InlineKeyboardRow(tu.InlineKeyboardButton("Выбрать сервер").WithCallbackData(CallbackGetKey)),
		),
	})
	if err != nil {
		b.logger.Error("Failed to update disabled server selection", "error", err)
	}
}

// Generate the existing legacy key with one loading edit and one final edit.
func (b *Bot) generateKeyProcess(serverID int, update telego.Update) {
	chatID := update.CallbackQuery.Message.GetChat().ID
	messageID := update.CallbackQuery.Message.GetMessageID()
	username := update.CallbackQuery.Message.GetChat().Username

	// Acknowledge the callback query to remove the loading animation
	err := b.bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})
	if err != nil {
		b.logger.Warn("Failed to acknowledge legacy key request")
		b.NotifyAdminsOfError(username, chatID, "key_generation", "callback_ack_failed", "Не удалось ответить на callback query")
	}

	keyMsg, err := b.sendKeyLoadingMessage(chatID, messageID)
	if err != nil {
		keyMsg.Text = "Не удалось получить ключ. Попробуйте ещё раз или напишите администратору."
		_, _ = b.bot.EditMessageText(keyMsg)
		b.NotifyAdminsOfError(username, chatID, "key_generation", "loading_message_failed", fmt.Sprintf("Не удалось показать сообщение загрузки ключа для сервера ID: %d", serverID))
		return
	}

	server, err := b.db.GetServerByID(int64(serverID))
	if err != nil {
		b.logger.Warn("Legacy server lookup unavailable")
		keyMsg.Text = "Не удалось получить ключ. Попробуйте ещё раз или напишите администратору."
		_, _ = b.bot.EditMessageText(keyMsg)
		b.NotifyAdminsOfError(username, chatID, "key_generation", "server_lookup_failed", fmt.Sprintf("Не удалось получить сервер из БД, ID: %d", serverID))
		return
	}

	serverName := fmt.Sprintf("%s %s, %s", countryToFlag(server.Country), server.Country, server.City)

	// Proceed to generate the key
	key, err := b.sh.GetUserKey(server, username, chatID)
	if err != nil {
		keyMsg.Text = "Не удалось получить ключ. Попробуйте ещё раз или напишите администратору."
		_, _ = b.bot.EditMessageText(keyMsg)
		b.NotifyAdminsOfKeyRequest(username, chatID, serverName, false, "legacy_key_unavailable")
		return
	}

	// Edit the message to show the generated key in monospace
	keyText := legacyKeyCaption(serverName, key)
	keyMsg.Text = keyText
	keyMsg.ParseMode = telego.ModeMarkdownV2
	keyMsg.ReplyMarkup = qrKeyDeliveryKeyboard(CallbackQRLegacyPrefix + strconv.Itoa(serverID))

	// Notify admins about successful key generation
	b.NotifyAdminsOfKeyRequest(username, chatID, serverName, true, "")

	_, err = b.bot.EditMessageText(keyMsg)
	if err != nil {
		// Transport errors can contain the credential-bearing request body.
		b.logger.Error("Failed to edit message with key")
		b.NotifyAdminsOfError(username, chatID, "key_generation", "delivery_failed", "Не удалось отправить ключ пользователю (ключ сгенерирован успешно)")
		return
	}
	b.recordTelegramActivity(update, "key_delivered")
}

func legacyKeyCaption(serverName, key string) string {
	return "Ключ для " + escapeMarkdownV2(serverName) + ":\n\n```" + key + "```\n\n" +
		escapeMarkdownV2("Добавьте полный vless:// ключ в Happ или INCY как сервер. Установка: /help_setup. Ключ никому не передавайте.")
}

// A single synchronous edit keeps loading and final delivery in one goroutine.
func (b *Bot) sendKeyLoadingMessage(chatID int64, messageID int) (*telego.EditMessageTextParams, error) {
	editMsg := &telego.EditMessageTextParams{
		ChatID: tu.ID(chatID), MessageID: messageID, Text: "Генерирую ключ…",
	}
	_, err := b.bot.EditMessageText(editMsg)
	if err != nil {
		b.logger.Warn("Failed to deliver key loading message")
	}
	return editMsg, err
}
