package telegram

import (
	"net/url"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

const betaMessage = "<b>Бета-отзыв</b>\n\nПроверьте один маршрут и расскажите, что работает. Ответы видите только вы и администраторы.\n\nКлючи и ссылки подписки не присылайте."

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
	text, keyboard := b.betaEntry()
	_, err := bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(update.Message.From.ID), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		b.logger.Warn("Failed to deliver beta entry")
	}
}

func (b *Bot) betaEntry() (string, *telego.InlineKeyboardMarkup) {
	text := betaMessage
	var rows [][]telego.InlineKeyboardButton
	if link := b.betaURL(); link != "" {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Оставить отзыв").WithWebApp(&telego.WebAppInfo{URL: link})))
	} else {
		text += "\n\nФорма сейчас недоступна. Напишите о проблеме в этот чат."
	}
	return text, tu.InlineKeyboard(append(rows, navigationRows(CallbackHelpSupport)...)...)
}
