package telegram

import (
	"errors"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

// Only known access states affect the explanation. Raw provider/transport
// errors can contain credentials and must never reach Telegram or logs.
func managedAccessError(err error) (text string, retryable bool) {
	switch {
	case errors.Is(err, remnawave.ErrInactive):
		return "Доступ отключён или его срок закончился. Напишите администратору.", false
	case errors.Is(err, remnawave.ErrConflict):
		return "Не удалось подтвердить доступ. Напишите администратору.", false
	case errors.Is(err, remnawave.ErrNotEligible):
		return "Для этого аккаунта подписка недоступна. Напишите администратору.", false
	case errors.Is(err, remnawave.ErrNotFound):
		return "Доступ не найден. Откройте список заново или напишите администратору.", false
	default:
		return "Сервис временно недоступен. Нажмите «Повторить» или попробуйте позже.", true
	}
}

func managedFailureKeyboard(retryable bool, retryCallback, backCallback string) *telego.InlineKeyboardMarkup {
	var rows [][]telego.InlineKeyboardButton
	if retryable {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("🔄 Повторить").WithCallbackData(retryCallback)))
	}
	return tu.InlineKeyboard(append(rows, navigationRows(backCallback)...)...)
}
