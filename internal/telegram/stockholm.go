package telegram

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

const (
	CallbackStockholmSubscription    = "stockholm_subscription"
	CallbackStockholmGuide           = "stockholm_guide"
	CallbackStockholmRouting         = "stockholm_routing"
	CallbackStockholmHappRouting     = "stockholm_route_happ"
	CallbackStockholmIncyRouting     = "stockholm_route_incy"
	CallbackStockholmHappRoutingLink = "stockholm_route_happ_link"
	CallbackStockholmIncyRoutingLink = "stockholm_route_incy_link"
)

const accessText = "Выберите подписку со всеми маршрутами или отдельный ключ."

type StockholmIssuer interface {
	Eligible(context.Context, int64) bool
	Issue(context.Context, int64) (remnawave.Subscription, error)
}

func (b *Bot) accessButtons(ctx context.Context, actorID int64) [][]telego.InlineKeyboardButton {
	var rows [][]telego.InlineKeyboardButton
	if actorID > 0 && b.stockholm != nil && b.stockholm.Eligible(ctx, actorID) {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("🌐 Подписка Otvali — все маршруты").WithCallbackData(CallbackStockholmSubscription)))
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("🔑 Отдельные ключи").WithCallbackData(CallbackIndividualKeys)), tu.InlineKeyboardRow(tu.InlineKeyboardButton("Главная").WithCallbackData(CallbackHelpBack)))
	return rows
}

func (b *Bot) handleStockholmSubscription(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.Data != CallbackStockholmSubscription {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if b.stockholm == nil || !b.stockholm.Eligible(ctx, update.CallbackQuery.From.ID) {
		b.editStockholmMessage(bot, update, "Подписка сейчас недоступна. Можно открыть отдельные ключи, в том числе прежний ключ Финляндии.", subscriptionFallbackKeyboard(false))
		return
	}
	subscription, err := b.stockholm.Issue(ctx, update.CallbackQuery.From.ID)
	if err != nil || !validSubscriptionURL(subscription.URL) {
		b.logger.Warn("VPN subscription request failed")
		text, retryable := managedAccessError(err)
		b.editStockholmMessage(bot, update, text, subscriptionFallbackKeyboard(retryable))
		return
	}
	text := "<b>Подписка Otvali</b>\n\n<code>" + html.EscapeString(subscription.URL) + "</code>\n\nДобавьте как подписку в Happ или INCY и включите VPN."
	if subscription.TrafficLimitBytes == 0 {
		text += "\n\nБезлимитный трафик"
	}
	if subscription.ExpiresAt.IsZero() {
		if subscription.TrafficLimitBytes == 0 {
			text += " • Без автоматического окончания"
		} else {
			text += "\n\nБез автоматического окончания"
		}
	} else {
		text += fmt.Sprintf("\n\nДействует до %s МСК.", subscription.ExpiresAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04"))
	}
	text += "\n\nНе пересылайте ссылку."
	if b.editStockholmMessage(bot, update, text, b.subscriptionDeliveryKeyboard()) {
		b.recordTelegramActivity(update, "subscription_delivered")
	}
}

func subscriptionDeliveryKeyboard() *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Установка").WithCallbackData(CallbackHelpVPNSetup)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("QR-код").WithCallbackData(CallbackQRSubscription), tu.InlineKeyboardButton("Отдельные ключи").WithCallbackData(CallbackIndividualKeys)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Главная").WithCallbackData(CallbackHelpBack)),
	)
}

func (b *Bot) subscriptionDeliveryKeyboard() *telego.InlineKeyboardMarkup {
	keyboard := subscriptionDeliveryKeyboard()
	if b.miniAppURL != "" {
		keyboard.InlineKeyboard[0] = append(keyboard.InlineKeyboard[0], tu.InlineKeyboardButton("Mini App").WithWebApp(&telego.WebAppInfo{URL: b.miniAppURL}))
	}
	return keyboard
}

func subscriptionFallbackKeyboard(retryable bool) *telego.InlineKeyboardMarkup {
	var rows [][]telego.InlineKeyboardButton
	if retryable {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("🔄 Повторить").WithCallbackData(CallbackStockholmSubscription)))
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Отдельные ключи").WithCallbackData(CallbackIndividualKeys)))
	return tu.InlineKeyboard(append(rows, navigationRows(CallbackHelpBack)...)...)
}

// Saved messages from earlier releases lead to the same installation screen.
func (b *Bot) handleStockholmGuide(bot *telego.Bot, update telego.Update) {
	if update.CallbackQuery != nil && update.CallbackQuery.Data != CallbackStockholmGuide {
		b.acknowledgePrivateCallback(bot, update)
		return
	}
	b.handleHelpCallback(bot, update)
}

func (b *Bot) showStockholmGuide(bot *telego.Bot, update telego.Update, expectedCallback, text string, keyboard *telego.InlineKeyboardMarkup) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.Data != expectedCallback {
		return
	}
	b.editStockholmMessage(bot, update, text, keyboard)
}

func validSubscriptionURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func (b *Bot) editStockholmMessage(bot *telego.Bot, update telego.Update, text string, keyboard *telego.InlineKeyboardMarkup) bool {
	if keyboard == nil {
		keyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Получить VPN").WithCallbackData(CallbackGetKey)))
	}
	var err error
	if message, ok := update.CallbackQuery.Message.(*telego.Message); ok && len(message.Photo) > 0 {
		// A QR photo cannot be edited into text. Keep its navigation usable by
		// sending the selected text screen into the same private conversation.
		_, err = bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(update.CallbackQuery.From.ID), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	} else {
		_, err = bot.EditMessageText(&telego.EditMessageTextParams{ChatID: tu.ID(update.CallbackQuery.From.ID), MessageID: update.CallbackQuery.Message.GetMessageID(), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	}
	if err != nil {
		b.logger.Warn("Failed to deliver private VPN response")
	}
	return err == nil
}
