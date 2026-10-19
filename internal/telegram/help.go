package telegram

import (
	"context"
	"time"

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
)

var (
	helpMessage = "Available commands:\n" +
		"/start - start the bot\n" +
		"/help - show help\n" +
		"/invite - invite a trusted user\n" +
		"/get_key - choose a VPN server key or subscription\n\n" +
		"A Finland vless:// key is a single server. An Otvali HTTPS URL is a subscription: add it as a subscription and refresh its profiles. Use Happ or INCY; official links and import instructions are under VPN setup.\n\n" +
		"💬 Send a message without a command to contact the administrators. Keep keys and subscription URLs private.\n\n" +
		"Choose an option below:"

	helpKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔑 Get VPN access").WithCallbackData(CallbackGetKey),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⚙️ VPN setup").WithCallbackData(CallbackHelpVPNSetup),
			tu.InlineKeyboardButton("ℹ️ How it works").WithCallbackData(CallbackHelpHowItWorks),
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
			tu.InlineKeyboardButton("⬅️ Back").WithCallbackData(CallbackHelpBack),
		),
	)

	helpBackKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⬅️ Back").WithCallbackData(CallbackHelpBack),
		),
	)

	helpVpnBackKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⬅️ Back").WithCallbackData(CallbackHelpVPNSetup),
		),
	)
)

// Handle /help command
func (b *Bot) handleHelp(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID

	// Create inline keyboard
	keyboard := b.withSubscriptionGuides(update, helpKeyboard)

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
		text = "Choose your platform for Happ / INCY setup instructions:"
		keyboard = vpnOSKeyboard
	case "help_vpn_linux":
		text = InstructionLinux
		keyboard = helpVpnBackKeyboard
	case "help_vpn_windows":
		text = InstructionWindows
		keyboard = helpVpnBackKeyboard
	case "help_vpn_android":
		text = InstructionAndroid
		keyboard = helpVpnBackKeyboard
	case "help_vpn_ios":
		text = InstructionIOS
		keyboard = helpVpnBackKeyboard
	case "help_vpn_macos":
		text = InstructionMacOS
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
		ChatID:             tu.ID(chatID),
		MessageID:          messageID,
		Text:               text,
		ParseMode:          telego.ModeHTML,
		ReplyMarkup:        b.withSubscriptionGuides(update, keyboard),
		LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
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

// Add existing subscription guides without changing help access or issuing access.
func (b *Bot) withSubscriptionGuides(update telego.Update, keyboard *telego.InlineKeyboardMarkup) *telego.InlineKeyboardMarkup {
	if !isPrivateActorUpdate(update) || b.stockholm == nil {
		return keyboard
	}
	var actorID int64
	if update.Message != nil {
		actorID = update.Message.From.ID
	} else {
		actorID = update.CallbackQuery.From.ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !b.stockholm.Eligible(ctx, actorID) {
		return keyboard
	}
	rows := make([][]telego.InlineKeyboardButton, 0, len(keyboard.InlineKeyboard)+2)
	last := len(keyboard.InlineKeyboard)
	if last > 0 {
		last--
		rows = append(rows, keyboard.InlineKeyboard[:last]...)
	}
	rows = append(rows,
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("📖 Subscription setup guide").WithCallbackData(CallbackStockholmGuide)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("🧭 Routing guide").WithCallbackData(CallbackStockholmRouting)),
	)
	rows = append(rows, keyboard.InlineKeyboard[last:]...)
	return tu.InlineKeyboard(rows...)
}
