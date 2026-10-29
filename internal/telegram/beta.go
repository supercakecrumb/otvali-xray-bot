package telegram

import (
	"net/url"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const betaMessage = "<b>Бета-тест VPN</b>\n\nВыберите один маршрут и проверьте видео, Telegram и сайты. В форме можно отметить, что работает, а что нет, и оставить комментарий. Не нужно проверять всё сразу.\n\nОтветы видите только вы и администраторы. Не присылайте ключи и ссылки подписки."

func (b *Bot) betaURL() string {
	if b.miniAppURL == "" {
		return ""
	}
	u, err := url.Parse(b.miniAppURL)
	if err != nil {
		return ""
	}
	u.Fragment = "beta"
	return u.String()
}

func (b *Bot) handleBeta(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil {
		return
	}
	text := betaMessage
	var keyboard *telego.InlineKeyboardMarkup
	if link := b.betaURL(); link != "" {
		keyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("Начать проверку / оставить отзыв").WithWebApp(&telego.WebAppInfo{URL: link})))
	} else {
		text += "\n\nФорма сейчас недоступна. Напишите о проблеме в этот чат."
	}
	_, err := bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(update.Message.From.ID), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		b.logger.Warn("Failed to deliver beta entry")
	}
}
