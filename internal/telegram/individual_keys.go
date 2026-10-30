package telegram

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type individualKeyIssuer interface {
	Keys(context.Context, int64) ([]remnawave.ServerKey, error)
}

var managedKeyID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (b *Bot) individualKeysKeyboard(servers []database.Server, keys []remnawave.ServerKey) *telego.InlineKeyboardMarkup {
	rows := b.buildServerButtons(servers)
	rows = rows[:len(rows)-1]
	seen := make(map[string]bool)
	for _, key := range keys {
		if !managedKeyID.MatchString(key.ID) || !validServerKey(key.URL) || seen[key.ID] {
			continue
		}
		seen[key.ID] = true
		name := []rune(key.Name)
		if len(name) > 80 {
			name = name[:80]
		}
		if len(name) == 0 {
			continue
		}
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(string(name)).WithCallbackData(CallbackManagedKeyPrefix+key.ID)))
	}
	rows = append(rows, navigationRows(CallbackGetKey)...)
	return tu.InlineKeyboard(rows...)
}

func (b *Bot) handleIndividualKeys(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.Data != CallbackIndividualKeys {
		return
	}
	var servers []database.Server
	legacyFailed := false
	if b.db != nil {
		var err error
		servers, err = b.db.GetAllServers()
		if err != nil {
			b.logger.Warn("Legacy server list unavailable")
			legacyFailed = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var keys []remnawave.ServerKey
	var managedErr error
	issuer, ok := b.stockholm.(individualKeyIssuer)
	if ok && b.stockholm.Eligible(ctx, update.CallbackQuery.From.ID) {
		var err error
		keys, err = issuer.Keys(ctx, update.CallbackQuery.From.ID)
		if err != nil {
			b.logger.Warn("Individual VPN key list unavailable")
			managedErr = err
		}
	}
	text, keyboard := b.individualKeysView(servers, keys, legacyFailed, managedErr)
	b.editStockholmMessage(bot, update, text, keyboard)
}

func (b *Bot) individualKeysView(servers []database.Server, keys []remnawave.ServerKey, legacyFailed bool, managedErr error) (string, *telego.InlineKeyboardMarkup) {
	text := "<b>Отдельные ключи</b>\n\nВыберите маршрут. Добавьте ключ как сервер в Happ или INCY."
	if legacyFailed {
		text += "\n\nСписок прежних ключей временно недоступен."
	}
	if managedErr != nil {
		message, _ := managedAccessError(managedErr)
		text += "\n\n" + message
	}
	keyboard := b.individualKeysKeyboard(servers, keys)
	if len(keyboard.InlineKeyboard) == 1 && !legacyFailed && managedErr == nil {
		text = "<b>Отдельные ключи</b>\n\nДоступных ключей пока нет. Напишите сюда — поможем."
	}
	if managedErr != nil {
		if _, retryable := managedAccessError(managedErr); retryable {
			keyboard.InlineKeyboard = append([][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(tu.InlineKeyboardButton("🔄 Повторить").WithCallbackData(CallbackIndividualKeys))}, keyboard.InlineKeyboard...)
		}
	}
	return text, keyboard
}

func (b *Bot) handleManagedKey(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) {
		return
	}
	callback := update.CallbackQuery
	if !strings.HasPrefix(callback.Data, CallbackManagedKeyPrefix) {
		return
	}
	id := strings.TrimPrefix(callback.Data, CallbackManagedKeyPrefix)
	if !managedKeyID.MatchString(id) {
		b.managedKeyUnavailable(bot, update)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	issuer, ok := b.stockholm.(individualKeyIssuer)
	if !ok || !b.stockholm.Eligible(ctx, callback.From.ID) {
		b.managedKeyUnavailable(bot, update)
		return
	}
	keys, err := issuer.Keys(ctx, callback.From.ID)
	if err != nil {
		b.logger.Warn("Individual VPN key request failed")
		text, retryable := managedAccessError(err)
		b.editStockholmMessage(bot, update, text, managedFailureKeyboard(retryable, callback.Data, CallbackIndividualKeys))
		return
	}
	for _, key := range keys {
		if key.ID != id || !validServerKey(key.URL) {
			continue
		}
		text := "<b>" + html.EscapeString(key.Name) + "</b>\n\n<code>" + html.EscapeString(key.URL) + "</code>\n\nДобавьте как сервер в Happ или INCY и включите VPN.\nКлюч не пересылайте."
		if b.editStockholmMessage(bot, update, text, qrKeyDeliveryKeyboard(CallbackQRManagedPrefix+id)) {
			b.recordTelegramActivity(update, "key_delivered")
		}
		return
	}
	b.managedKeyUnavailable(bot, update)
}

func (b *Bot) managedKeyUnavailable(bot *telego.Bot, update telego.Update) {
	b.editStockholmMessage(bot, update, "Ключ сейчас недоступен. Откройте список заново или напишите администратору.", tu.InlineKeyboard(navigationRows(CallbackIndividualKeys)...))
}

func validServerKey(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 3000 && u.Scheme == "vless" && u.User != nil && u.Hostname() != "" && u.Port() != "" && !strings.ContainsAny(raw, "\r\n\x00")
}
