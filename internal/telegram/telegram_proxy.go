package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

const (
	CallbackTelegramProxy = "telegram_proxy_menu"
	// CallbackTelegramProxyIssue is retained for per-route buttons in messages
	// sent before v0.2.18; they now open the same all-routes view.
	CallbackTelegramProxyIssue = "telegram_proxy_issue:"
	// telegramProxyTimeout bounds sequential issuance of every route. Each
	// route also keeps the service's own per-route deadline.
	telegramProxyTimeout = 40 * time.Second
)

type TelegramProxyIssuer interface {
	IssueAll(context.Context, int64) ([]telegramproxy.Credential, []string, error)
}

func WithTelegramProxy(issuer TelegramProxyIssuer) Option {
	return func(b *Bot) { b.telegramProxy = issuer }
}

const telegramProxyLoadingText = "<b>Прокси для Telegram</b>\n\nГотовлю личные ссылки для всех маршрутов…"

const telegramProxyLinksText = "<b>Прокси для Telegram</b>\n\n" +
	"Работает только внутри Telegram. VPN для остальных приложений включается отдельно.\n\n" +
	"Нажмите на каждую кнопку ниже — Telegram предложит добавить прокси. Лучше добавить все: если один перестанет работать, можно переключиться на другой.\n\n" +
	"Добавленные прокси и их состояние — в настройках Telegram, раздел «Прокси». Там же можно включить ротацию прокси, если она есть в вашей версии.\n\n" +
	"Ссылки личные, никому их не пересылайте."

const telegramProxyPartialText = "Часть маршрутов временно недоступна. Нажмите «Обновить» чуть позже."

func telegramProxyID(id string) bool {
	switch id {
	case "finland", "stockholm", "msk-finland", "msk-stockholm":
		return true
	default:
		return false
	}
}

// handleTelegramProxy serves /telegram and /start telegram.
func (b *Bot) handleTelegramProxy(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil || update.Message.From.ID <= 0 {
		return
	}
	b.deliverTelegramProxies(bot, update, update.Message.From.ID)
}

func (b *Bot) handleTelegramProxyCallback(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.From.ID <= 0 {
		return
	}
	data := update.CallbackQuery.Data
	if data != CallbackTelegramProxy && (!strings.HasPrefix(data, CallbackTelegramProxyIssue) || !telegramProxyID(strings.TrimPrefix(data, CallbackTelegramProxyIssue))) {
		return
	}
	b.deliverTelegramProxies(bot, update, update.CallbackQuery.From.ID)
}

// telegramProxyScreen is the single private message that first shows the
// loading state and then the issued links.
type telegramProxyScreen struct {
	chatID    int64
	messageID int // zero sends a new message
}

// deliverTelegramProxies issues every route for the private actor and shows
// them in one message. Links appear only in launch buttons, never in logs.
func (b *Bot) deliverTelegramProxies(bot *telego.Bot, update telego.Update, actor int64) {
	if b.telegramProxy == nil {
		text, keyboard := telegramProxyFailure(telegramproxy.ErrUnavailable)
		b.sendTelegramProxyResponse(bot, update, text, keyboard)
		return
	}
	screen := telegramProxyScreen{chatID: actor}
	if update.CallbackQuery != nil {
		// A QR photo cannot become text; it gets a new message instead.
		if message, ok := update.CallbackQuery.Message.(*telego.Message); !ok || len(message.Photo) == 0 {
			screen.messageID = update.CallbackQuery.Message.GetMessageID()
		}
	}
	// The loading state has no buttons, so repeated taps cannot queue work.
	b.showTelegramProxyScreen(bot, &screen, telegramProxyLoadingText, nil)
	ctx, cancel := context.WithTimeout(context.Background(), telegramProxyTimeout)
	defer cancel()
	credentials, unavailable, err := b.telegramProxy.IssueAll(ctx, actor)
	text, keyboard, issued, partial := telegramProxyLinksView(credentials, unavailable, err)
	switch {
	case !issued:
		// The provider may already have accepted an operation. Do not surface
		// raw errors, revoke a possibly valid grant, or claim a connection.
		b.logger.Warn("Telegram proxy request unavailable")
	case partial:
		b.logger.Warn("Telegram proxy routes partially unavailable", slog.Int("unavailable", len(unavailable)))
	}
	if b.showTelegramProxyScreen(bot, &screen, text, keyboard) && issued {
		b.recordTelegramActivity(update, "telegram_proxy_delivered")
	}
}

func (b *Bot) showTelegramProxyScreen(bot *telego.Bot, screen *telegramProxyScreen, text string, keyboard *telego.InlineKeyboardMarkup) bool {
	preview := &telego.LinkPreviewOptions{IsDisabled: true}
	var err error
	if screen.messageID == 0 {
		params := &telego.SendMessageParams{ChatID: tu.ID(screen.chatID), Text: text, ParseMode: telego.ModeHTML, LinkPreviewOptions: preview}
		if keyboard != nil {
			params.ReplyMarkup = keyboard
		}
		var message *telego.Message
		message, err = bot.SendMessage(params)
		if err == nil && message != nil && message.MessageID > 0 {
			screen.messageID = message.MessageID
		}
	} else {
		_, err = bot.EditMessageText(&telego.EditMessageTextParams{ChatID: tu.ID(screen.chatID), MessageID: screen.messageID, Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: preview})
	}
	if err != nil {
		b.logger.Warn("Failed to deliver private Telegram proxy response")
	}
	return err == nil
}

// telegramProxyLinksView renders one launch button per issued route in the
// issuer's order. It does not trust the issuer: unknown, duplicate or
// malformed credentials are dropped and the view offers a refresh instead.
func telegramProxyLinksView(credentials []telegramproxy.Credential, unavailable []string, err error) (text string, keyboard *telego.InlineKeyboardMarkup, issued, partial bool) {
	if err != nil {
		text, keyboard = telegramProxyFailure(err)
		return text, keyboard, false, false
	}
	partial = len(unavailable) > 0
	seen := map[string]bool{}
	var rows [][]telego.InlineKeyboardButton
	for _, credential := range credentials {
		if !telegramProxyID(credential.EndpointID) || seen[credential.EndpointID] || !telegramproxy.ValidCredentialURL(credential.URL) {
			partial = true
			continue
		}
		seen[credential.EndpointID] = true
		label := strings.TrimSpace(credential.Name)
		if label == "" {
			label = proxyRouteLabel(credential.EndpointID)
		}
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(label).WithURL(credential.URL)))
	}
	if len(rows) == 0 {
		text, keyboard = telegramProxyFailure(telegramproxy.ErrUnavailable)
		return text, keyboard, false, false
	}
	text = telegramProxyLinksText
	navigation := tu.InlineKeyboardRow(tu.InlineKeyboardButton("Главная").WithCallbackData(CallbackHelpBack))
	if partial {
		text += "\n\n" + telegramProxyPartialText
		navigation = tu.InlineKeyboardRow(tu.InlineKeyboardButton("Обновить").WithCallbackData(CallbackTelegramProxy), tu.InlineKeyboardButton("Главная").WithCallbackData(CallbackHelpBack))
	}
	return text, tu.InlineKeyboard(append(rows, navigation)...), true, partial
}

func telegramProxyFailure(err error) (string, *telego.InlineKeyboardMarkup) {
	text := "Не удалось подтвердить готовность прокси. Откройте список маршрутов позже. Если ссылка у вас уже есть, не удаляйте её."
	if errors.Is(err, telegramproxy.ErrNotEligible) || errors.Is(err, telegramproxy.ErrInactive) || errors.Is(err, telegramproxy.ErrConflict) {
		text = "Доступ к прокси пока недоступен. Напишите администратору в этот чат."
	}
	return text, tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Маршруты Telegram").WithCallbackData(CallbackTelegramProxy)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Главная").WithCallbackData(CallbackHelpBack)),
	)
}

func (b *Bot) sendTelegramProxyResponse(bot *telego.Bot, update telego.Update, text string, keyboard *telego.InlineKeyboardMarkup) bool {
	var err error
	if update.CallbackQuery != nil {
		_, err = bot.EditMessageText(&telego.EditMessageTextParams{ChatID: tu.ID(update.CallbackQuery.From.ID), MessageID: update.CallbackQuery.Message.GetMessageID(), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	} else {
		_, err = bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(update.Message.From.ID), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	}
	if err != nil {
		b.logger.Warn("Failed to deliver private Telegram proxy response")
	}
	return err == nil
}
