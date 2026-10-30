package telegram

import (
	"context"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/miniapp"
)

const CallbackAccountStatus = "vpn_status"

// AccountStatusReader is the same read-only service used by Mini App /session.
// This interface deliberately offers no issuance or key retrieval methods.
type AccountStatusReader interface {
	Account(context.Context, int64) (miniapp.Account, error)
}

func WithAccountStatus(reader AccountStatusReader) Option {
	return func(b *Bot) { b.accountStatus = reader }
}

func accountStateText(state string) string {
	switch state {
	case "ready":
		return "активен"
	case "eligible_unprovisioned":
		return "разрешён, подписка ещё не получена"
	case "access_disabled":
		return "отключён или закончился"
	case "not_found":
		return "не найден"
	case "not_eligible":
		return "не выдан"
	default:
		return "временно не удалось проверить"
	}
}

func (b *Bot) accountStatusText(id int64) string {
	const unavailable = "<b>Проверка доступа</b>\n\nСейчас не удалось проверить доступ. Попробуйте обновить позже."
	if b.accountStatus == nil {
		return unavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	account, err := b.accountStatus.Account(ctx, id)
	if err != nil {
		b.logger.Warn("Account access check unavailable")
		return unavailable
	}
	var text strings.Builder
	text.WriteString("<b>Проверка доступа</b>\n\nДоступ по подписке: " + accountStateText(account.ManagedState) + ".\nСтарый ключ Финляндии: " + accountStateText(account.LegacyFinlandState) + ".")
	if !account.CheckedAt.IsZero() {
		text.WriteString("\n\nПроверено " + account.CheckedAt.In(time.FixedZone("MSK", 3*60*60)).Format("02.01 15:04") + " МСК.")
	}
	text.WriteString("\n\nЭто проверка учётной записи. Соединение на устройстве здесь не проверяется.")
	return text.String()
}

func accountStatusKeyboard() *telego.InlineKeyboardMarkup {
	rows := [][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(tu.InlineKeyboardButton("Обновить").WithCallbackData(CallbackAccountStatus))}
	return tu.InlineKeyboard(append(rows, navigationRows(CallbackHelpSupport)...)...)
}

func (b *Bot) handleAccountStatus(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil {
		return
	}
	_, err := bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(update.Message.From.ID), Text: b.accountStatusText(update.Message.From.ID), ParseMode: telego.ModeHTML, ReplyMarkup: accountStatusKeyboard(), LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		b.logger.Warn("Failed to deliver account check")
	}
}

func (b *Bot) handleAccountStatusCallback(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.Data != CallbackAccountStatus {
		return
	}
	b.editStockholmMessage(bot, update, b.accountStatusText(update.CallbackQuery.From.ID), accountStatusKeyboard())
}
