package telegram

import (
	"errors"
	"net/url"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const (
	CallbackGetKey           = "getkey_"
	CallbackIndividualKeys   = "individual_keys"
	CallbackManagedKeyPrefix = "managed_key_"
	CallbackHelpVPNSetup     = "help_vpn_setup"
	CallbackHelpVPNLinux     = "help_vpn_linux"
	CallbackHelpVPNWindows   = "help_vpn_windows"
	CallbackHelpVPNAndroid   = "help_vpn_android"
	CallbackHelpVPNIOS       = "help_vpn_ios"
	CallbackHelpVPNMacOS     = "help_vpn_macos"
	CallbackHelpHowItWorks   = "help_how_it_works"
	CallbackHelpBack         = "help_back"
)

const helpMessage = "Otvali · Aurora\n\nПолучите VPN или откройте инструкцию.\nДля связи с администраторами напишите в этот чат."
const installationText = "<b>Установка VPN — 3 шага</b>\n\n1. Установите Happ или INCY.\n2. Получите подписку в «Получить VPN» и добавьте её в приложение.\n3. Обновите подписку, выберите маршрут и подключитесь.\n\nОтдельный ключ добавляется как сервер. Подробности — на сайте."

var (
	helpKeyboard = tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("🔑 Получить VPN").WithCallbackData(CallbackGetKey)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("⚙️ Установка VPN").WithCallbackData(CallbackHelpVPNSetup)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("ℹ️ Как это работает").WithCallbackData(CallbackHelpHowItWorks)),
	)
	helpBackKeyboard    = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack)))
	helpVpnBackKeyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ К установке VPN").WithCallbackData(CallbackHelpVPNSetup)))
)

// WithVPNHelpURL accepts a public guide origin/path, never a credential URL.
func WithVPNHelpURL(raw string) (Option, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return func(b *Bot) { b.vpnHelpURL = "" }, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("VPN_HELP_URL must be a public HTTPS URL without credentials, query or fragment")
	}
	return func(b *Bot) { b.vpnHelpURL = u.String() }, nil
}

func (b *Bot) guideURL(anchor string) string {
	if b.vpnHelpURL == "" {
		return ""
	}
	u, err := url.Parse(b.vpnHelpURL)
	if err != nil {
		return ""
	}
	u.Fragment = anchor
	return u.String()
}

// WithMiniAppURL accepts only an explicitly allowed public origin. Telegram
// sends signed launch data to this URL, so a general help link is insufficient.
func WithMiniAppURL(raw string, allowedOrigins []string) (Option, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return func(b *Bot) { b.miniAppURL = "" }, nil
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("Mini App launch URL must use an allowed public HTTPS origin")
	}
	for _, origin := range allowedOrigins {
		if origin == u.Scheme+"://"+u.Host {
			return func(b *Bot) { b.miniAppURL = u.String() }, nil
		}
	}
	return nil, errors.New("Mini App launch origin is not allowed")
}

func (b *Bot) mainMenuKeyboard() *telego.InlineKeyboardMarkup {
	var rows [][]telego.InlineKeyboardButton
	if b.miniAppURL != "" {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Открыть приложение").WithWebApp(&telego.WebAppInfo{URL: b.miniAppURL})))
	}
	return tu.InlineKeyboard(append(rows, helpKeyboard.InlineKeyboard...)...)
}

func (b *Bot) installationKeyboard() *telego.InlineKeyboardMarkup {
	rows := [][]telego.InlineKeyboardButton{
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Windows").WithCallbackData(CallbackHelpVPNWindows), tu.InlineKeyboardButton("macOS").WithCallbackData(CallbackHelpVPNMacOS)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Android").WithCallbackData(CallbackHelpVPNAndroid), tu.InlineKeyboardButton("iOS").WithCallbackData(CallbackHelpVPNIOS)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Linux").WithCallbackData(CallbackHelpVPNLinux)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("🧭 Маршрутизация").WithCallbackData(CallbackStockholmRouting)),
	}
	if website := b.guideURL(""); website != "" {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("📖 Подробная инструкция").WithURL(website)))
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack)))
	return tu.InlineKeyboard(rows...)
}

func (b *Bot) platformKeyboard(anchor string) *telego.InlineKeyboardMarkup {
	var rows [][]telego.InlineKeyboardButton
	if website := b.guideURL(anchor); website != "" {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("📖 Инструкция на сайте").WithURL(website)))
	} else {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Скачать Happ").WithURL("https://happ.info/")), tu.InlineKeyboardRow(tu.InlineKeyboardButton("Скачать INCY").WithURL("https://github.com/INCY-DEV/incy-platforms")))
	}
	rows = append(rows, helpVpnBackKeyboard.InlineKeyboard...)
	return tu.InlineKeyboard(rows...)
}

func (b *Bot) handleHelp(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil {
		return
	}
	_, err := bot.SendMessage(tu.Message(tu.ID(update.Message.Chat.ID), helpMessage).WithReplyMarkup(b.mainMenuKeyboard()))
	if err != nil {
		b.logger.Warn("Failed to deliver help")
	}
}

func (b *Bot) handleHelpSetup(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil {
		return
	}
	_, err := bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(update.Message.From.ID), Text: installationText, ParseMode: telego.ModeHTML, ReplyMarkup: b.installationKeyboard(), LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		b.logger.Warn("Failed to deliver installation guide")
	}
}

func (b *Bot) acknowledgePrivateCallback(bot *telego.Bot, update telego.Update) bool {
	if update.CallbackQuery == nil {
		return false
	}
	if err := bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID}); err != nil {
		b.logger.Warn("Failed to acknowledge navigation")
		return false
	}
	return isPrivateActorUpdate(update)
}

func (b *Bot) handleHelpCallback(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) {
		return
	}
	text, keyboard := "", helpBackKeyboard
	switch update.CallbackQuery.Data {
	case CallbackHelpVPNSetup, CallbackStockholmGuide:
		text, keyboard = installationText, b.installationKeyboard()
	case CallbackHelpVPNLinux:
		text, keyboard = InstructionLinux, b.platformKeyboard("linux")
	case CallbackHelpVPNWindows:
		text, keyboard = InstructionWindows, b.platformKeyboard("windows")
	case CallbackHelpVPNAndroid:
		text, keyboard = InstructionAndroid, b.platformKeyboard("android")
	case CallbackHelpVPNIOS:
		text, keyboard = InstructionIOS, b.platformKeyboard("ios")
	case CallbackHelpVPNMacOS:
		text, keyboard = InstructionMacOS, b.platformKeyboard("macos")
	case CallbackHelpHowItWorks:
		text = howItWorksText
		keyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("Проверить доступ").WithCallbackData(CallbackAccountStatus)), helpBackKeyboard.InlineKeyboard[0])
	case CallbackHelpBack:
		text, keyboard = helpMessage, b.mainMenuKeyboard()
	default:
		return
	}
	b.editStockholmMessage(bot, update, text, keyboard)
}
