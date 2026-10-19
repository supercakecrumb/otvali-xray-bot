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

const CallbackStockholmSubscription = "stockholm_subscription"

type StockholmIssuer interface {
	Eligible(context.Context, int64) bool
	Issue(context.Context, int64) (remnawave.Subscription, error)
}

func (b *Bot) appendStockholmButton(ctx context.Context, actorID int64, buttons [][]telego.InlineKeyboardButton) [][]telego.InlineKeyboardButton {
	if actorID <= 0 || b.stockholm == nil || !b.stockholm.Eligible(ctx, actorID) {
		return buttons
	}
	row := tu.InlineKeyboardRow(tu.InlineKeyboardButton("🇸🇪 Швеция, Стокгольм · подписка").WithCallbackData(CallbackStockholmSubscription))
	// The legacy builder always ends with navigation; preserve its callbacks.
	if len(buttons) == 0 {
		return [][]telego.InlineKeyboardButton{row}
	}
	result := make([][]telego.InlineKeyboardButton, 0, len(buttons)+1)
	result = append(result, buttons[:len(buttons)-1]...)
	result = append(result, row, buttons[len(buttons)-1])
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
		b.editStockholmMessage(bot, update, "Подписка на Стокгольм сейчас недоступна. Выберите другой сервер.", nil)
		return
	}
	subscription, err := b.stockholm.Issue(ctx, callback.From.ID)
	if err != nil || !validSubscriptionURL(subscription.URL) {
		// Never forward API errors or response bodies to logs or administrators.
		b.logger.Warn("Stockholm subscription request failed")
		b.editStockholmMessage(bot, update, "Не удалось получить подписку на Стокгольм. Попробуйте позже или обратитесь к администратору.", nil)
		return
	}

	text := "🇸🇪 Подписка на Стокгольм\n\n<code>" + html.EscapeString(subscription.URL) + "</code>\n\n" +
		"Скопируйте ссылку и добавьте её в Hiddify как подписку. В приложении появится сервер в Стокгольме.\n" +
		"Ваш текущий ключ Финляндии продолжает работать."
	if !subscription.ExpiresAt.IsZero() {
		text += fmt.Sprintf("\n\nДействует до: %s МСК.", subscription.ExpiresAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01.2006 15:04"))
	}
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Открыть подписку").WithURL(subscription.URL)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Выбрать сервер").WithCallbackData(CallbackGetKey)),
	)
	b.editStockholmMessage(bot, update, text, keyboard)
}

func validSubscriptionURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func (b *Bot) editStockholmMessage(bot *telego.Bot, update telego.Update, text string, keyboard *telego.InlineKeyboardMarkup) {
	if keyboard == nil {
		keyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Выбрать сервер").WithCallbackData(CallbackGetKey)))
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
