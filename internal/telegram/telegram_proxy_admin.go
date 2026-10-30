package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const callbackRevokeTelegramPrefix = "tgproxy_revoke:"
const telegramProxyRevokeTTL = 5 * time.Minute

type telegramProxyRevoker interface {
	Revoke(context.Context, int64, string) error
}

type telegramProxyAdminStore interface {
	IsUserAdmin(int64) (bool, error)
	GetUserByID(int64) (*database.User, error)
	GetProxyGrant(context.Context, int64, string) (telegramproxy.Grant, bool, error)
}

type telegramProxyRevokePreview struct {
	actor, userID, telegramID int64
	route                     string
	createdAt                 time.Time
}

func (b *Bot) proxyAdminDB(ctx context.Context) telegramProxyAdminStore {
	if b.proxyAdminStore != nil {
		return b.proxyAdminStore
	}
	if b.db == nil || b.db.Conn == nil {
		return nil
	}
	return &database.DB{Conn: b.db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)}
}

func proxyAdminAllowed(store telegramProxyAdminStore, actor int64) bool {
	if store == nil || actor <= 0 {
		return false
	}
	allowed, err := store.IsUserAdmin(actor)
	return err == nil && allowed
}

func proxyRevokeRoutes(route string) []string {
	if route == "all" {
		return []string{"finland", "stockholm", "msk-finland", "msk-stockholm"}
	}
	if telegramProxyID(route) {
		return []string{route}
	}
	return nil
}

func proxyRouteLabel(route string) string {
	switch route {
	case "finland":
		return "Финляндия"
	case "stockholm":
		return "Швеция"
	case "msk-finland":
		return "Москва → Финляндия"
	case "msk-stockholm":
		return "Москва → Швеция"
	default:
		return "Неизвестный маршрут"
	}
}

func (b *Bot) handleRevokeTelegram(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	store, actor := b.proxyAdminDB(ctx), update.Message.From.ID
	if !proxyAdminAllowed(store, actor) {
		b.sendTelegramProxyResponse(bot, update, "Команда доступна только администраторам.", nil)
		return
	}
	args := strings.Fields(update.Message.Text)
	if len(args) < 2 || len(args) > 3 {
		b.proxyRevokeUsage(bot, update)
		return
	}
	userID, err := strconv.ParseInt(args[1], 10, 64)
	route := "all"
	if len(args) == 3 {
		route = args[2]
	}
	if err != nil || userID <= 0 || strconv.FormatInt(userID, 10) != args[1] || proxyRevokeRoutes(route) == nil {
		b.proxyRevokeUsage(bot, update)
		return
	}
	if _, ok := b.telegramProxy.(telegramProxyRevoker); !ok {
		b.sendTelegramProxyResponse(bot, update, "Отзыв прокси сейчас недоступен. Данные пользователя сохранены.", nil)
		return
	}
	user, err := store.GetUserByID(userID)
	if err != nil || user == nil || user.ID != userID || user.TelegramID == nil || *user.TelegramID <= 0 {
		b.sendTelegramProxyResponse(bot, update, "Не удалось найти подтверждённый Telegram-аккаунт по этому ID. Проверьте /users.", nil)
		return
	}
	preview := telegramProxyRevokePreview{actor: actor, userID: userID, telegramID: *user.TelegramID, route: route, createdAt: time.Now()}
	token, ok := b.saveProxyRevokePreview(preview)
	if !ok {
		b.sendTelegramProxyResponse(bot, update, "Не удалось подготовить подтверждение. Повторите команду позже.", nil)
		return
	}
	name := "без username"
	if user.Username != "" {
		name = "@" + user.Username
	}
	text := fmt.Sprintf("<b>Отозвать прокси Telegram?</b>\n\n%s\nID в боте: <code>%d</code>\nTelegram ID: <code>%d</code>\n\n", html.EscapeString(name), userID, *user.TelegramID)
	for _, id := range proxyRevokeRoutes(route) {
		text += "• " + proxyRouteLabel(id) + " (<code>" + id + "</code>)\n"
	}
	text += "\nСсылки этих маршрутов перестанут работать; повторная выдача для них будет заблокирована. VPN-подписка и отдельные VPN-ключи сохранятся. Подтверждение действует 5 минут."
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Отозвать указанные прокси").WithCallbackData(callbackRevokeTelegramPrefix+"yes:"+token)),
		tu.InlineKeyboardRow(tu.InlineKeyboardButton("Отмена").WithCallbackData(callbackRevokeTelegramPrefix+"no:"+token)),
	)
	if !b.sendTelegramProxyResponse(bot, update, text, keyboard) {
		b.takeProxyRevokePreview(token, actor, time.Now())
	}
}

func (b *Bot) proxyRevokeUsage(bot *telego.Bot, update telego.Update) {
	b.sendTelegramProxyResponse(bot, update, "Использование: <code>/revoke_telegram ID [маршрут|all]</code>\n\nID — номер пользователя из /users. Маршруты: <code>finland</code>, <code>stockholm</code>, <code>msk-finland</code>, <code>msk-stockholm</code>. Без маршрута выбираются все четыре. Команда сначала покажет подтверждение.", nil)
}

func (b *Bot) saveProxyRevokePreview(preview telegramProxyRevokePreview) (string, bool) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", false
	}
	token := hex.EncodeToString(nonce[:])
	b.proxyRevokeMu.Lock()
	defer b.proxyRevokeMu.Unlock()
	if b.proxyRevokePreviews == nil {
		b.proxyRevokePreviews = make(map[string]telegramProxyRevokePreview)
	}
	for key, value := range b.proxyRevokePreviews {
		if preview.createdAt.Sub(value.createdAt) >= telegramProxyRevokeTTL {
			delete(b.proxyRevokePreviews, key)
		}
	}
	if len(b.proxyRevokePreviews) >= 128 {
		return "", false
	}
	b.proxyRevokePreviews[token] = preview
	return token, true
}

// Confirmation is bound to this bot instance, the requesting admin and a short
// lifetime. Consume before provider I/O: redelivery never repeats a side effect.
func (b *Bot) takeProxyRevokePreview(token string, actor int64, now time.Time) (telegramProxyRevokePreview, bool) {
	b.proxyRevokeMu.Lock()
	defer b.proxyRevokeMu.Unlock()
	preview, ok := b.proxyRevokePreviews[token]
	if !ok || preview.actor != actor {
		return telegramProxyRevokePreview{}, false
	}
	delete(b.proxyRevokePreviews, token)
	if now.Before(preview.createdAt) || now.Sub(preview.createdAt) >= telegramProxyRevokeTTL {
		return telegramProxyRevokePreview{}, false
	}
	return preview, true
}

func (b *Bot) handleRevokeTelegramCallback(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	store, actor := b.proxyAdminDB(ctx), update.CallbackQuery.From.ID
	if !proxyAdminAllowed(store, actor) {
		b.sendTelegramProxyResponse(bot, update, "Команда доступна только администраторам.", nil)
		return
	}
	parts := strings.Split(strings.TrimPrefix(update.CallbackQuery.Data, callbackRevokeTelegramPrefix), ":")
	if !strings.HasPrefix(update.CallbackQuery.Data, callbackRevokeTelegramPrefix) || len(parts) != 2 || (parts[0] != "yes" && parts[0] != "no") || len(parts[1]) != 32 {
		return
	}
	preview, ok := b.takeProxyRevokePreview(parts[1], actor, time.Now())
	if !ok {
		b.sendTelegramProxyResponse(bot, update, "Подтверждение истекло или уже использовано. Для новой операции отправьте /revoke_telegram с ID пользователя.", nil)
		return
	}
	if parts[0] == "no" {
		b.sendTelegramProxyResponse(bot, update, "Отзыв отменён. Доступы не изменены.", nil)
		return
	}
	user, err := store.GetUserByID(preview.userID)
	if err != nil || user == nil || user.ID != preview.userID || user.TelegramID == nil || *user.TelegramID != preview.telegramID {
		b.sendTelegramProxyResponse(bot, update, "Привязка аккаунта изменилась или недоступна. Отзыв не выполнен. Проверьте /users и начните заново.", nil)
		return
	}
	revoker, ok := b.telegramProxy.(telegramProxyRevoker)
	if !ok {
		b.sendTelegramProxyResponse(bot, update, "Отзыв прокси сейчас недоступен. Данные пользователя сохранены.", nil)
		return
	}
	text := fmt.Sprintf("<b>Отзыв прокси Telegram</b>\nID в боте: <code>%d</code>\n\n", preview.userID)
	unconfirmed := false
	for _, id := range proxyRevokeRoutes(preview.route) {
		// A single route cannot consume the whole four-route operation budget.
		routeCtx, routeCancel := context.WithTimeout(ctx, 8*time.Second)
		err := revoker.Revoke(routeCtx, preview.telegramID, id)
		routeCancel()
		state := "отключён"
		switch {
		case errors.Is(err, telegramproxy.ErrNotFound):
			state = "доступ не выдавался"
		case err != nil:
			unconfirmed = true
			state = "отключение не подтверждено"
			if grant, found, readErr := store.GetProxyGrant(ctx, preview.telegramID, id); readErr == nil && found && grant.State == telegramproxy.StateRevoking {
				state = "отзывается; подтверждения сервера нет"
			}
		}
		text += "• " + proxyRouteLabel(id) + ": " + state + "\n"
	}
	if unconfirmed {
		text += "\nНеподтверждённые операции автоматически не повторяются. Доступы в состоянии отзыва блокируют выдачу новых ссылок и удаление аккаунта до сверки. Для отдельной сверки снова отправьте /revoke_telegram и подтвердите её."
	} else {
		text += "\nПроверка завершена. VPN-доступы этой командой не изменялись."
	}
	b.sendTelegramProxyResponse(bot, update, text, nil)
}
