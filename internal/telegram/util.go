package telegram

import "github.com/mymmrac/telego"

func markdownMessage(id telego.ChatID, text string) *telego.SendMessageParams {
	return &telego.SendMessageParams{
		ChatID:    id,
		Text:      text,
		ParseMode: "Markdown",
	}
}
