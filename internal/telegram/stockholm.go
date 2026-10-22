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
	CallbackStockholmSubscription = "stockholm_subscription"
	CallbackStockholmGuide        = "stockholm_guide"
	CallbackStockholmRouting      = "stockholm_routing"
	CallbackStockholmHappRouting  = "stockholm_route_happ"
	CallbackStockholmIncyRouting  = "stockholm_route_incy"
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
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("🔑 Отдельные ключи").WithCallbackData(CallbackIndividualKeys)), tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack)))
	return rows
}

func (b *Bot) handleStockholmSubscription(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.Data != CallbackStockholmSubscription {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if b.stockholm == nil || !b.stockholm.Eligible(ctx, update.CallbackQuery.From.ID) {
		b.editStockholmMessage(bot, update, "Подписка сейчас недоступна. Можно выбрать отдельный ключ.", nil)
		return
	}
	subscription, err := b.stockholm.Issue(ctx, update.CallbackQuery.From.ID)
	if err != nil || !validSubscriptionURL(subscription.URL) {
		b.logger.Warn("VPN subscription request failed")
		text, retryable := managedAccessError(err)
		b.editStockholmMessage(bot, update, text, managedFailureKeyboard(retryable, CallbackStockholmSubscription, CallbackGetKey, "⬅️ Получить VPN"))
		return
	}
	text := "<b>Подписка Otvali — все маршруты</b>\n\n<code>" + html.EscapeString(subscription.URL) + "</code>\n\nСкопируйте ссылку и добавьте её в Happ или INCY как подписку."
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
	text += "\n\nУстановка: /help_setup. Ссылку никому не передавайте."
	b.editStockholmMessage(bot, update, text, subscriptionDeliveryKeyboard())
}

func subscriptionDeliveryKeyboard() *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("⚙️ Установка VPN").WithCallbackData(CallbackHelpVPNSetup)), tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Получить VPN").WithCallbackData(CallbackGetKey)))
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

func (b *Bot) editStockholmMessage(bot *telego.Bot, update telego.Update, text string, keyboard *telego.InlineKeyboardMarkup) {
	if keyboard == nil {
		keyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Получить VPN").WithCallbackData(CallbackGetKey)))
	}
	_, err := bot.EditMessageText(&telego.EditMessageTextParams{ChatID: tu.ID(update.CallbackQuery.From.ID), MessageID: update.CallbackQuery.Message.GetMessageID(), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		b.logger.Warn("Failed to deliver private VPN response")
	}
}
