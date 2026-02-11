package telegram

import (
	"strings"

	"github.com/mymmrac/telego"
)

func markdownMessage(id telego.ChatID, text string) *telego.SendMessageParams {
	return &telego.SendMessageParams{
		ChatID:    id,
		Text:      text,
		ParseMode: "Markdown",
	}
}

// escapeMarkdown escapes special characters for Telegram's Markdown format
// In Telegram Markdown, these characters have special meaning: _ * [ ] ( ) ~ ` > # + - = | { } . !
// However, for legacy Markdown mode (not MarkdownV2), we mainly need to escape: _ * ` [
func escapeMarkdown(text string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"`", "\\`",
		"[", "\\[",
	)
	return replacer.Replace(text)
}

// escapeMarkdownV2 escapes special characters for Telegram's MarkdownV2 format
// In MarkdownV2, ALL these characters must be escaped when outside code blocks:
// _ * [ ] ( ) ~ ` > # + - = | { } . !
func escapeMarkdownV2(text string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(text)
}
