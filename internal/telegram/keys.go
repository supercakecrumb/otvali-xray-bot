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

var backHomeKeyboard = tu.InlineKeyboard(
	tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("🏠 Домой").WithCallbackData("help_back"),
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
		"Выберите сервер для получения ключа:",
	).WithReplyMarkup(keyboard)

	_, err = bot.SendMessage(msg)
	if err != nil {
		b.logger.Error("Failed to send get_key message", "error", err)
		b.NotifyAdminsOfError(username, chatID, "/get_key", err.Error(), "Не удалось отправить список серверов")
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
		return nil, fmt.Errorf("failed to fetch servers: %w", err)
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

	backRow := tu.InlineKeyboardRow(
		tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack),
	)

	buttons = append(buttons, backRow)

	return buttons, nil
}

// Handle callback queries from /get_key command
func (b *Bot) handleGetKeyCallback(bot *telego.Bot, update telego.Update) {
	callbackQuery := update.CallbackQuery
	data := callbackQuery.Data
	chatID := callbackQuery.Message.GetChat().ID
	msgID := callbackQuery.Message.GetMessageID()
	username := callbackQuery.Message.GetChat().Username

	if !strings.HasPrefix(data, "getkey_") {
		// Unknown callback data
		return
	}

	// Check if callback data matches "getkey_" exactly
	if data == "getkey_" {
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
			Text:        "Выберите сервер для получения ключа:",
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

	// Notify admins about server selection
	b.NotifyAdminsOfAction(username, chatID, "server_selected", fmt.Sprintf("Пользователь выбрал сервер ID: %d для получения ключа", serverID))

	// Start generating the key
	go b.generateKeyProcess(serverID, update)
}

// Generate key process with animated dots and message updates
func (b *Bot) generateKeyProcess(serverID int, update telego.Update) {
	chatID := update.CallbackQuery.Message.GetChat().ID
	messageID := update.CallbackQuery.Message.GetMessageID()
	username := update.CallbackQuery.Message.GetChat().Username

	// Acknowledge the callback query to remove the loading animation
	err := b.bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
	})
	if err != nil {
		b.logger.Error("Failed to answer callback query", "error", err)
		b.NotifyAdminsOfError(username, chatID, "key_generation", err.Error(), "Не удалось ответить на callback query")
	}

	keyMsg, cancel, err := b.sendMessageWithAnimatedDots(chatID, messageID, "Генерирую ключ")
	if err != nil {
		errorMsg := fmt.Sprintf("Произошла ошибка при генерации ключа: %v", err)
		keyMsg.Text = errorMsg
		_, _ = b.bot.EditMessageText(keyMsg)
		b.NotifyAdminsOfError(username, chatID, "key_generation", err.Error(), fmt.Sprintf("Не удалось начать анимацию генерации ключа для сервера ID: %d", serverID))
		return
	}

	defer cancel()

	server, err := b.db.GetServerByID(int64(serverID))
	if err != nil {
		b.logger.Error("error getting server from db", slog.String("error", err.Error()))
		cancel() // Stop the animation
		errorMsg := fmt.Sprintf("Произошла ошибка при генерации ключа: %v", err)
		keyMsg.Text = errorMsg
		_, _ = b.bot.EditMessageText(keyMsg)
		b.NotifyAdminsOfError(username, chatID, "key_generation", err.Error(), fmt.Sprintf("Не удалось получить сервер из БД, ID: %d", serverID))
		return
	}

	serverName := fmt.Sprintf("%s %s, %s", countryToFlag(server.Country), server.Country, server.City)

	// Proceed to generate the key
	key, err := b.sh.GetUserKey(server, username, chatID)
	if err != nil {
		cancel() // Stop the animation
		errorMsg := fmt.Sprintf("Произошла ошибка при генерации ключа: %v", err)
		keyMsg.Text = errorMsg
		_, _ = b.bot.EditMessageText(keyMsg)
		b.NotifyAdminsOfKeyRequest(username, chatID, serverName, false, err.Error())
		return
	}

	cancel() // Stop the animation

	// Edit the message to show the generated key in monospace
	keyText := fmt.Sprintf("Твой ключ от сервера %v:```%s```Скопируй его и вставь в Hiddify чтобы начать пользоваться", serverName, key)
	keyMsg.Text = keyText
	keyMsg.ParseMode = telego.ModeMarkdownV2
	keyMsg.ReplyMarkup = backHomeKeyboard

	// Notify admins about successful key generation
	b.NotifyAdminsOfKeyRequest(username, chatID, serverName, true, "")

	_, err = b.bot.EditMessageText(keyMsg)
	if err != nil {
		b.logger.Error("Failed to edit message with key", "error", err)
		b.NotifyAdminsOfError(username, chatID, "key_generation", err.Error(), "Не удалось отправить ключ пользователю (ключ сгенерирован успешно)")
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
