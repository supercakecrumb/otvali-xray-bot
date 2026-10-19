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

const stockholmGuideText = "<b>Set up your subscription in Happ or INCY</b>\n\n" +
	"1. <b>Choose your app.</b> Use the official Happ or INCY links below to find a supported build for your device.\n\n" +
	"2. <b>Copy your subscription URL.</b> Choose ‘Get my subscription’ below and copy the URL from the message. It is a subscription link, not a single server key.\n\n" +
	"3. <b>Import the subscription.</b> In Happ or INCY, use the option to import from the clipboard or add a subscription URL. Control names vary by app.\n" +
	"• <b>iOS / Android:</b> approve the system VPN permission when prompted.\n" +
	"• <b>Windows / macOS / Linux:</b> import the URL into the app’s subscription/profile list and approve any required system permission.\n\n" +
	"4. <b>Update and connect.</b> Refresh the subscription, choose Sweden, Stockholm, Moscow → Stockholm or Moscow → Finland, and connect. Moscow routes send Russian traffic directly from Moscow and other traffic through the selected foreign exit. Refresh this same subscription to receive new server profiles.\n\n" +
	"5. <b>Choose routing separately.</b> The routing guide offers an optional ‘RU direct, other traffic VPN’ profile. This Otvali subscription supplies standard VLESS/base64 to Happ and INCY for custom routing. Other providers’ JSON subscriptions can lock manual routing.\n\n" +
	"<b>Fallback:</b> keep your existing Finland profile. If the new connection fails, select Finland and reconnect.\n\n" +
	"<b>Keep your subscription URL private.</b> It grants VPN access. Do not forward or post it. You do not need to share the URL to get help."

type StockholmIssuer interface {
	Eligible(context.Context, int64) bool
	Issue(context.Context, int64) (remnawave.Subscription, error)
}

func (b *Bot) appendStockholmButton(ctx context.Context, actorID int64, buttons [][]telego.InlineKeyboardButton) [][]telego.InlineKeyboardButton {
	if actorID <= 0 || b.stockholm == nil || !b.stockholm.Eligible(ctx, actorID) {
		return buttons
	}
	row := tu.InlineKeyboardRow(tu.InlineKeyboardButton("🇸🇪 Sweden, Stockholm (subscription)").WithCallbackData(CallbackStockholmSubscription))
	moscow := tu.InlineKeyboardRow(tu.InlineKeyboardButton("🇷🇺 Moscow routes (subscription)").WithCallbackData(CallbackStockholmSubscription))
	guide := tu.InlineKeyboardRow(tu.InlineKeyboardButton("📖 Setup guide").WithCallbackData(CallbackStockholmGuide))
	routing := tu.InlineKeyboardRow(tu.InlineKeyboardButton("🧭 Routing guide").WithCallbackData(CallbackStockholmRouting))
	// The legacy builder always ends with navigation; preserve its callbacks.
	if len(buttons) == 0 {
		return [][]telego.InlineKeyboardButton{row, moscow, guide, routing}
	}
	result := make([][]telego.InlineKeyboardButton, 0, len(buttons)+4)
	result = append(result, buttons[:len(buttons)-1]...)
	result = append(result, row, moscow, guide, routing, buttons[len(buttons)-1])
	return result
}

func (b *Bot) handleStockholmSubscription(bot *telego.Bot, update telego.Update) {
	callback := update.CallbackQuery
	if callback == nil {
		return
	}
	if err := bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{CallbackQueryID: callback.ID}); err != nil {
		// Telegram errors can include request content, including subscription URLs.
		b.logger.Warn("Failed to acknowledge Stockholm subscription request")
		return
	}
	if callback.Data != CallbackStockholmSubscription || !isPrivateActorUpdate(update) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if b.stockholm == nil || !b.stockholm.Eligible(ctx, callback.From.ID) {
		b.editStockholmMessage(bot, update, "VPN subscriptions are currently unavailable. Please choose another server.", nil)
		return
	}
	subscription, err := b.stockholm.Issue(ctx, callback.From.ID)
	if err != nil || !validSubscriptionURL(subscription.URL) {
		// Never forward API errors or response bodies to logs or administrators.
		b.logger.Warn("Stockholm subscription request failed")
		b.editStockholmMessage(bot, update, "Could not get your VPN subscription. Please try again later or contact the administrator.", nil)
		return
	}

	text := "<b>Otvali VPN subscription</b>\n\n<code>" + html.EscapeString(subscription.URL) + "</code>\n\n" +
		"Copy this URL and add it as a subscription in Happ or INCY. Refresh it, then choose Sweden, Stockholm, Moscow → Stockholm or Moscow → Finland, and connect.\n" +
		"Moscow routes send Russian traffic directly from Moscow and other traffic through the selected foreign exit.\n" +
		"Keep your existing Finland profile as a fallback. The guides below cover setup and optional client-side routing."
	if !subscription.ExpiresAt.IsZero() {
		text += fmt.Sprintf("\n\nExpires: %s MSK.", subscription.ExpiresAt.In(time.FixedZone("MSK", 3*60*60)).Format("02 Jan 2006, 15:04"))
	}
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Open subscription URL").WithURL(subscription.URL)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("📖 Setup guide").WithCallbackData(CallbackStockholmGuide)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("🧭 Routing guide").WithCallbackData(CallbackStockholmRouting)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Choose server").WithCallbackData(CallbackGetKey)),
	)
	b.editStockholmMessage(bot, update, text, keyboard)
}

func (b *Bot) handleStockholmGuide(bot *telego.Bot, update telego.Update) {
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Happ: official downloads").WithURL("https://happ.info/")),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("INCY: official downloads").WithURL("https://github.com/INCY-DEV/incy-platforms")),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("🧭 Routing guide").WithCallbackData(CallbackStockholmRouting)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Get my subscription").WithCallbackData(CallbackStockholmSubscription)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Choose server").WithCallbackData(CallbackGetKey)),
	)
	b.showStockholmGuide(bot, update, CallbackStockholmGuide, stockholmGuideText, keyboard)
}

func (b *Bot) showStockholmGuide(bot *telego.Bot, update telego.Update, expectedCallback, text string, keyboard *telego.InlineKeyboardMarkup) {
	callback := update.CallbackQuery
	if callback == nil {
		return
	}
	if err := bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{CallbackQueryID: callback.ID}); err != nil {
		b.logger.Warn("Failed to acknowledge Stockholm guide request")
		return
	}
	if callback.Data != expectedCallback || !isPrivateActorUpdate(update) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if b.stockholm == nil || !b.stockholm.Eligible(ctx, callback.From.ID) {
		b.editStockholmMessage(bot, update, "VPN subscriptions are currently unavailable. Please choose another server.", nil)
		return
	}
	b.editStockholmMessage(bot, update, text, keyboard)
}

func validSubscriptionURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func (b *Bot) editStockholmMessage(bot *telego.Bot, update telego.Update, text string, keyboard *telego.InlineKeyboardMarkup) {
	if keyboard == nil {
		keyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Choose server").WithCallbackData(CallbackGetKey)))
	}
	_, err := bot.EditMessageText(&telego.EditMessageTextParams{
		ChatID: tu.ID(update.CallbackQuery.From.ID), MessageID: update.CallbackQuery.Message.GetMessageID(),
		Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard,
		LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		b.logger.Warn("Failed to deliver Stockholm subscription response")
	}
}
