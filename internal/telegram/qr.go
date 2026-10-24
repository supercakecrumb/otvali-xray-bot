package telegram

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/qrcode"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

const (
	CallbackQRMenu          = "qr_menu"
	CallbackQRSubscription  = "qr_subscription"
	CallbackQRKeys          = "qr_keys"
	CallbackQRManagedPrefix = "qr_managed_"
	CallbackQRLegacyPrefix  = "qr_legacy_"
)

type legacyQRStore interface {
	GetUserByTelegramID(int64) (*database.User, error)
	GetAllServers() ([]database.Server, error)
	GetServerByID(int64) (*database.Server, error)
}

type legacyQRIssuer interface {
	IsServerDisabled(int64) bool
	GetUserKeyForTelegramID(context.Context, *database.Server, string, int64) (string, error)
}

func (b *Bot) qrRequestStore(ctx context.Context) legacyQRStore {
	if db, ok := b.qrStore.(*database.DB); ok && db != nil && db.Conn != nil {
		return &database.DB{Conn: db.Conn.Session(&gorm.Session{Logger: gormlog.Discard}).WithContext(ctx)}
	}
	return b.qrStore
}

func qrKeyDeliveryKeyboard(callback string) *telego.InlineKeyboardMarkup {
	rows := [][]telego.InlineKeyboardButton{tu.InlineKeyboardRow(tu.InlineKeyboardButton("QR-код ключа").WithCallbackData(callback))}
	return tu.InlineKeyboard(append(rows, backHomeKeyboard.InlineKeyboard...)...)
}

func (b *Bot) handleQR(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil || update.Message.From.ID <= 0 {
		return
	}
	b.showQRMenu(bot, update.Message.From.ID)
}

func (b *Bot) handleQRSubscription(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil || update.Message.From.ID <= 0 {
		return
	}
	b.sendSubscriptionQR(bot, update.Message.From.ID)
}

func (b *Bot) handleQRKey(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil || update.Message.From.ID <= 0 {
		return
	}
	b.showQRKeys(bot, update.Message.From.ID)
}

func (b *Bot) handleQRCallback(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) || update.CallbackQuery.From.ID <= 0 {
		return
	}
	callback := update.CallbackQuery
	switch {
	case callback.Data == CallbackQRMenu:
		b.showQRMenu(bot, callback.From.ID)
	case callback.Data == CallbackQRSubscription:
		b.sendSubscriptionQR(bot, callback.From.ID)
	case callback.Data == CallbackQRKeys:
		b.showQRKeys(bot, callback.From.ID)
	case strings.HasPrefix(callback.Data, CallbackQRManagedPrefix):
		b.sendManagedQR(bot, callback.From.ID, strings.TrimPrefix(callback.Data, CallbackQRManagedPrefix))
	case strings.HasPrefix(callback.Data, CallbackQRLegacyPrefix):
		raw := strings.TrimPrefix(callback.Data, CallbackQRLegacyPrefix)
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
			b.qrUnavailable(bot, callback.From.ID)
			return
		}
		b.sendLegacyQR(bot, callback.From.ID, id)
	}
}

func (b *Bot) qrReply(bot *telego.Bot, id int64, text string, keyboard *telego.InlineKeyboardMarkup) {
	if keyboard == nil {
		keyboard = tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("Другой QR-код").WithCallbackData(CallbackQRMenu)))
	}
	_, err := bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(id), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		b.logger.Warn("Failed to deliver QR response")
	}
}

func (b *Bot) showQRMenu(bot *telego.Bot, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var rows [][]telego.InlineKeyboardButton
	if b.stockholm != nil && b.stockholm.Eligible(ctx, id) {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("QR-код подписки").WithCallbackData(CallbackQRSubscription)))
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("QR-код отдельного ключа").WithCallbackData(CallbackQRKeys)), tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Получить VPN").WithCallbackData(CallbackGetKey)))
	b.qrReply(bot, id, "<b>QR-коды VPN</b>\n\nВыберите подписку или отдельный ключ. Ничего вводить или вставлять не нужно.", tu.InlineKeyboard(rows...))
}

func (b *Bot) showQRKeys(bot *telego.Bot, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var rows [][]telego.InlineKeyboardButton
	text := "<b>QR-код отдельного ключа</b>\n\nВыберите сервер."
	store := b.qrRequestStore(ctx)
	if store != nil && b.qrLegacy != nil {
		user, err := store.GetUserByTelegramID(id)
		if err == nil && user != nil && user.TelegramID != nil && *user.TelegramID == id {
			servers, err := store.GetAllServers()
			if err != nil {
				text += "\n\nСписок старых серверов временно недоступен."
			}
			for _, server := range servers {
				if !qrLegacyAllowed(user, &server, b.qrLegacy) {
					continue
				}
				name := fmt.Sprintf("%s %s, %s", countryToFlag(server.Country), server.Country, server.City)
				rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(qrShortName(name)).WithCallbackData(CallbackQRLegacyPrefix+strconv.FormatInt(server.ID, 10))))
			}
		}
	}
	issuer, ok := b.stockholm.(individualKeyIssuer)
	if ok && b.stockholm.Eligible(ctx, id) {
		keys, err := issuer.Keys(ctx, id)
		if err != nil {
			message, _ := managedAccessError(err)
			text += "\n\n" + message
		} else {
			seen := make(map[string]bool)
			for _, key := range keys {
				if !managedKeyID.MatchString(key.ID) || !validServerKey(key.URL) || seen[key.ID] {
					continue
				}
				seen[key.ID] = true
				rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(qrShortName(key.Name)).WithCallbackData(CallbackQRManagedPrefix+key.ID)))
			}
		}
	}
	if len(rows) == 0 {
		text += "\n\nДоступных ключей пока нет. Получите ключ через /get_key или попробуйте позже."
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ QR-коды").WithCallbackData(CallbackQRMenu)))
	b.qrReply(bot, id, text, tu.InlineKeyboard(rows...))
}

func (b *Bot) sendSubscriptionQR(bot *telego.Bot, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if b.stockholm == nil || !b.stockholm.Eligible(ctx, id) {
		b.qrUnavailable(bot, id)
		return
	}
	sub, err := b.stockholm.Issue(ctx, id)
	if err != nil || !validSubscriptionURL(sub.URL) || ctx.Err() != nil {
		b.qrAccessError(bot, id, err)
		return
	}
	b.sendQRImage(bot, id, sub.URL, "Подписка Otvali — все маршруты", true)
}

func (b *Bot) sendManagedQR(bot *telego.Bot, id int64, selected string) {
	if !managedKeyID.MatchString(selected) {
		b.qrUnavailable(bot, id)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	issuer, ok := b.stockholm.(individualKeyIssuer)
	if !ok || !b.stockholm.Eligible(ctx, id) {
		b.qrUnavailable(bot, id)
		return
	}
	keys, err := issuer.Keys(ctx, id)
	if err != nil || ctx.Err() != nil {
		b.qrAccessError(bot, id, err)
		return
	}
	for _, key := range keys {
		if key.ID == selected && validServerKey(key.URL) {
			b.sendQRImage(bot, id, key.URL, key.Name, false)
			return
		}
	}
	b.qrUnavailable(bot, id)
}

func qrLegacyAllowed(user *database.User, server *database.Server, issuer legacyQRIssuer) bool {
	return user != nil && server != nil && server.ID > 0 && server.InboundID != nil && issuer != nil && !issuer.IsServerDisabled(server.ID) && (!server.IsExclusive || user.ExclusiveAccess || user.IsAdmin)
}

func (b *Bot) sendLegacyQR(bot *telego.Bot, id, serverID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	store := b.qrRequestStore(ctx)
	if store == nil || b.qrLegacy == nil || b.qrLegacy.IsServerDisabled(serverID) {
		b.qrUnavailable(bot, id)
		return
	}
	user, err := store.GetUserByTelegramID(id)
	if err != nil || user == nil || user.TelegramID == nil || *user.TelegramID != id || strings.TrimSpace(user.Username) == "" {
		b.qrUnavailable(bot, id)
		return
	}
	server, err := store.GetServerByID(serverID)
	if err != nil || server == nil || server.ID != serverID || !qrLegacyAllowed(user, server, b.qrLegacy) || ctx.Err() != nil {
		b.qrUnavailable(bot, id)
		return
	}
	key, err := b.qrLegacy.GetUserKeyForTelegramID(ctx, server, user.Username, id)
	if err != nil || !validServerKey(key) || ctx.Err() != nil {
		b.qrUnavailable(bot, id)
		return
	}
	b.sendQRImage(bot, id, key, server.Country+", "+server.City, false)
}

func (b *Bot) qrAccessError(bot *telego.Bot, id int64, err error) {
	text, retryable := managedAccessError(err)
	if retryable {
		text = "Сервис временно недоступен. Попробуйте запросить QR-код позже."
	}
	b.qrReply(bot, id, text+"\n\nПолучить ссылку текстом: /get_key.", nil)
}

func (b *Bot) qrUnavailable(bot *telego.Bot, id int64) {
	b.qrReply(bot, id, "Этот QR-код сейчас недоступен. Выберите ключ заново или напишите администратору. Получить ссылку текстом: /get_key.", nil)
}

func qrShortName(name string) string {
	runes := []rune(name)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	if len(runes) == 0 {
		return "Профиль VPN"
	}
	return string(runes)
}

type qrPNGFile struct{ *bytes.Reader }

func (qrPNGFile) Name() string { return "otvali-qr.png" }

func (b *Bot) sendQRImage(bot *telego.Bot, id int64, raw, name string, subscription bool) {
	png, err := qrcode.PNG(raw)
	if err != nil {
		b.qrReply(bot, id, "Не удалось создать QR-код: ссылка слишком длинная или недоступна. Скопируйте её через /get_key и добавьте в VPN-приложение.", nil)
		return
	}
	kind := "ключа"
	instruction := "Добавьте как отдельный сервер или профиль."
	if subscription {
		kind = "подписки"
		instruction = "Добавьте как подписку, чтобы получить все маршруты."
	}
	caption := "<b>QR-код " + kind + " · " + html.EscapeString(qrShortName(name)) + "</b>\n\nОтсканируйте в Happ или INCY с другого устройства либо импортируйте изображение из галереи, если приложение поддерживает это. " + instruction + "\n\nQR-код даёт доступ к VPN. Никому его не передавайте."
	_, err = bot.SendPhoto(&telego.SendPhotoParams{ChatID: tu.ID(id), Photo: tu.File(qrPNGFile{bytes.NewReader(png)}), Caption: caption, ParseMode: telego.ModeHTML, ReplyMarkup: tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("Другой QR-код").WithCallbackData(CallbackQRMenu)))})
	if err != nil {
		b.logger.Warn("Failed to deliver QR image")
		b.qrReply(bot, id, "Не удалось отправить QR-код. Повторите запрос или скопируйте ссылку через /get_key.", nil)
	}
}
