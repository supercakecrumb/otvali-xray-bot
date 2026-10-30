package telegram

import (
	"context"
	"errors"
	"html"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

const (
	CallbackTelegramProxy      = "telegram_proxy_menu"
	CallbackTelegramProxyIssue = "telegram_proxy_issue:"
)

type TelegramProxyIssuer interface {
	List(context.Context, int64) ([]telegramproxy.Endpoint, error)
	Issue(context.Context, int64, string) (telegramproxy.Credential, error)
}

func WithTelegramProxy(issuer TelegramProxyIssuer) Option {
	return func(b *Bot) { b.telegramProxy = issuer }
}

const telegramProxyMenuText = "<b>Прокси для Telegram</b>\n\nРаботает только внутри Telegram. VPN для остальных приложений включается отдельно.\n\nВыберите маршрут. Затем нажмите «Подключить в Telegram» и подтвердите подключение."

func telegramProxyID(id string) bool {
	switch id {
	case "finland", "stockholm", "msk-finland", "msk-stockholm":
		return true
	default:
		return false
	}
}

func (b *Bot) handleTelegramProxy(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil || update.Message.From.ID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	text, keyboard := b.telegramProxyMenu(ctx, update.Message.From.ID)
	b.sendTelegramProxyResponse(bot, update, text, keyboard)
}

func (b *Bot) telegramProxyMenu(ctx context.Context, actor int64) (string, *telego.InlineKeyboardMarkup) {
	if b.telegramProxy == nil {
		return telegramProxyFailure(telegramproxy.ErrUnavailable)
	}
	endpoints, err := b.telegramProxy.List(ctx, actor)
	if err != nil {
		return telegramProxyFailure(err)
	}
	var rows [][]telego.InlineKeyboardButton
	for _, endpoint := range endpoints {
		if !telegramProxyID(endpoint.ID) || endpoint.Protocol != "mtproto" {
			return telegramProxyFailure(telegramproxy.ErrUnavailable)
		}
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(endpoint.Name).WithCallbackData(CallbackTelegramProxyIssue+endpoint.ID)))
	}
	if len(rows) == 0 {
		return telegramProxyFailure(telegramproxy.ErrUnavailable)
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Назад").WithCallbackData(CallbackHelpBack)))
	return telegramProxyMenuText, tu.InlineKeyboard(rows...)
}

func (b *Bot) handleTelegramProxyCallback(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.From.ID <= 0 {
		return
	}
	data, actor := update.CallbackQuery.Data, update.CallbackQuery.From.ID
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if data == CallbackTelegramProxy {
		text, keyboard := b.telegramProxyMenu(ctx, actor)
		b.sendTelegramProxyResponse(bot, update, text, keyboard)
		return
	}
	if !strings.HasPrefix(data, CallbackTelegramProxyIssue) {
		return
	}
	id := strings.TrimPrefix(data, CallbackTelegramProxyIssue)
	if !telegramProxyID(id) {
		return
	}
	if b.telegramProxy == nil {
		text, keyboard := telegramProxyFailure(telegramproxy.ErrUnavailable)
		b.sendTelegramProxyResponse(bot, update, text, keyboard)
		return
	}
	credential, err := b.telegramProxy.Issue(ctx, actor, id)
	if err != nil || credential.EndpointID != id || !telegramproxy.ValidCredentialURL(credential.URL) {
		// The provider may already have accepted an operation. Do not surface
		// raw errors, revoke a possibly valid grant, or claim the route connected.
		b.logger.Warn("Telegram proxy request unavailable")
		text, keyboard := telegramProxyFailure(err)
		b.sendTelegramProxyResponse(bot, update, text, keyboard)
		return
	}
	text := "<b>" + html.EscapeString(credential.Name) + "</b>\n\nПрокси подготовлен. Нажмите кнопку и подтвердите подключение в Telegram.\n\nЭто ещё не подтверждение соединения. Его состояние смотрите в настройках прокси Telegram. Ссылку никому не передавайте.\n\nДругой маршрут: /telegram."
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Подключить в Telegram").WithURL(credential.URL)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Другой маршрут").WithCallbackData(CallbackTelegramProxy)),
	)
	if b.sendTelegramProxyResponse(bot, update, text, keyboard) {
		b.recordTelegramActivity(update, "telegram_proxy_delivered")
	}
}

func telegramProxyFailure(err error) (string, *telego.InlineKeyboardMarkup) {
	text := "Не удалось подтвердить готовность прокси. Откройте список маршрутов позже. Если ссылка у вас уже есть, не удаляйте её."
	if errors.Is(err, telegramproxy.ErrNotEligible) || errors.Is(err, telegramproxy.ErrInactive) || errors.Is(err, telegramproxy.ErrConflict) {
		text = "Доступ к прокси пока недоступен. Напишите администратору в этот чат."
	}
	return text, tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Маршруты Telegram").WithCallbackData(CallbackTelegramProxy)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Назад").WithCallbackData(CallbackHelpBack)),
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
