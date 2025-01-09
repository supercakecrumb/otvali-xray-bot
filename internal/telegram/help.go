package telegram

import (
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const (
	CallbackGetKey         = "getkey_"
	CallbackHelpVPNSetup   = "help_vpn_setup"
	CallbackHelpVPNLinux   = "help_vpn_linux"
	CallbackHelpVPNWindows = "help_vpn_windows"
	CallbackHelpVPNAndroid = "help_vpn_android"
	CallbackHelpVPNIOS     = "help_vpn_ios"
	CallbackHelpVPNMacOS   = "help_vpn_macos"
	CallbackHelpHowItWorks = "help_how_it_works"
	CallbackHelpBack       = "help_back"

	howItWorksText = `Ну вот так вот работает чо нос суеш куда не поподя`
)

// Russian messages
var (
	helpMessage = "Доступные команды:\n" +
		"/start - начать работу с ботом\n" +
		"/help - получить помощь\n" +
		"/invite - пригласить пользователя\n" +
		"/get_key - получить ключ для доступа к VPN\n\n" +
		"Выберите один из вариантов ниже:"

	helpKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔑 Получить ключ 🔑").WithCallbackData(CallbackGetKey),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⚙️ Настройка VPN").WithCallbackData(CallbackHelpVPNSetup),
			tu.InlineKeyboardButton("ℹ️ Как это работает").WithCallbackData(CallbackHelpHowItWorks),
		),
	)

	vpnOSKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🪟 Windows").WithCallbackData(CallbackHelpVPNWindows),
			tu.InlineKeyboardButton("🍏 macOS").WithCallbackData(CallbackHelpVPNMacOS),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📱 Android").WithCallbackData(CallbackHelpVPNAndroid),
			tu.InlineKeyboardButton("🍎 iOS").WithCallbackData(CallbackHelpVPNIOS),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🐧 Linux").WithCallbackData(CallbackHelpVPNLinux),
			tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack),
		),
	)

	helpBackKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack),
		),
	)

	helpVpnBackKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpVPNSetup),
		),
	)
)

// Handle /help command
func (b *Bot) handleHelp(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID

	// Create inline keyboard
	keyboard := helpKeyboard

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
	var keyboard *telego.InlineKeyboardMarkup = helpBackKeyboard

	switch data {
	case "help_vpn_setup":
		text = "Выберите вашу платформу для получения инструкции:"
		keyboard = vpnOSKeyboard
	case "help_vpn_linux":
		text = InstructionHiddifyLinux
		keyboard = helpVpnBackKeyboard
	case "help_vpn_windows":
		text = InstructionHiddifyWindows
		keyboard = helpVpnBackKeyboard
	case "help_vpn_android":
		text = InstructionHiddifyAndroid
		keyboard = helpVpnBackKeyboard
	case "help_vpn_ios":
		text = InstructionHiddifyIOS
		keyboard = helpVpnBackKeyboard
	case "help_vpn_macos":
		text = InstructionHiddifyMacOS
		keyboard = helpVpnBackKeyboard
	case "help_how_it_works":
		text = howItWorksText
		keyboard = helpBackKeyboard
	case "help_back":
		text = helpMessage
		keyboard = helpKeyboard
	default:
		return
	}

	editMsg := &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ParseMode:   telego.ModeHTML,
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
