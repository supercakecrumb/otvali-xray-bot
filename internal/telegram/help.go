package telegram

import (
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Russian messages
var (
	helpMessage = "Доступные команды:\n" +
		"/start - начать работу с ботом\n" +
		"/help - получить помощь\n" +
		"/invite - пригласить пользователя\n" +
		"/get_key - получить ключ для доступа к VPN\n\n" +
		"Выберите один из вариантов ниже:"

	vpnSetupText    = "Инструкция по настройке VPN:\n\n1. Шаг первый...\n2. Шаг второй...\n3. Шаг третий..."
	invitationsText = "Чтобы поделиться доступом к боту, используйте команду /invite <username>."
	howItWorksText  = "Описание того, как это работает:\n\nНаш сервис позволяет вам получить доступ к VPN через бота..."
)

// Handle /help command
func (b *Bot) handleHelp(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID

	// Create inline keyboard
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("Настройка VPN").WithCallbackData("help_vpn_setup"),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("Приглашения").WithCallbackData("help_invitations"),
			tu.InlineKeyboardButton("Как это работает").WithCallbackData("help_how_it_works"),
		),
	)

	msg := tu.Message(
		tu.ID(chatID),
		helpMessage,
	).WithReplyMarkup(keyboard)

	_, err := bot.SendMessage(msg)
	if err != nil {
		b.logger.Error("Failed to send help message", "error", err)
	}
}

// Handle callback queries from /help command
func (b *Bot) handleHelpCallback(bot *telego.Bot, update telego.Update) {
	callbackQuery := update.CallbackQuery
	data := callbackQuery.Data
	chatID := callbackQuery.Message.GetChat().ID
	messageID := callbackQuery.Message.GetMessageID()

	var text string
	var keyboard *telego.InlineKeyboardMarkup

	switch data {
	case "help_vpn_setup":
		text = vpnSetupText
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("Назад").WithCallbackData("help_back"),
			),
		)
	case "help_invitations":
		text = invitationsText
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("Назад").WithCallbackData("help_back"),
			),
		)
	case "help_how_it_works":
		text = howItWorksText
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("Назад").WithCallbackData("help_back"),
			),
		)
	case "help_back":
		text = helpMessage
		keyboard = tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("Настройка VPN").WithCallbackData("help_vpn_setup"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("Приглашения").WithCallbackData("help_invitations"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("Как это работает").WithCallbackData("help_how_it_works"),
			),
		)
	default:
		// Unknown callback data
		return
	}

	// Edit the original message
	editMsg := &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: keyboard,
	}

	_, err := bot.EditMessageText(editMsg)
	if err != nil {
		b.logger.Error("Failed to edit message", "error", err)
	}

	// Answer the callback query to remove the loading animation
	err = bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{
		CallbackQueryID: callbackQuery.ID,
	})
	if err != nil {
		b.logger.Error("Failed to answer callback query", "error", err)
	}
}
