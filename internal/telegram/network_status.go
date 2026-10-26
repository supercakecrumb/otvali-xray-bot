package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/vpnmonitor"
)

const CallbackNetworkStatus = "vpn_health"
const CallbackNetworkDetails = "vpn_health_details"
const CallbackNetworkRoute = "vpn_health_route:"

type NetworkStatusReader interface {
	Snapshot(context.Context) (vpnmonitor.Snapshot, error)
}

func WithNetworkStatus(reader NetworkStatusReader, owner int64) Option {
	return func(b *Bot) { b.networkStatus = reader; b.monitorOwnerID = owner }
}

func (b *Bot) isMonitorOwner(ctx context.Context, id int64) bool {
	if id <= 0 || id != b.monitorOwnerID {
		return false
	}
	if b.monitorOwnerCheck != nil {
		return b.monitorOwnerCheck(ctx, id)
	}
	return b.isMonitorAdmin(ctx, id)
}

// Diagnostics may be read by bound administrators. Notification delivery still
// requires the separately configured owner ID in isMonitorOwner.
func (b *Bot) isMonitorAdmin(ctx context.Context, id int64) bool {
	if id <= 0 || ctx.Err() != nil {
		return false
	}
	if b.monitorAdminCheck != nil {
		return b.monitorAdminCheck(ctx, id)
	}
	if b.db == nil || b.db.Conn == nil {
		return false
	}
	var count int64
	return b.db.Conn.WithContext(ctx).Model(&database.User{}).Where("telegram_id = ? AND is_admin = ?", id, true).Count(&count).Error == nil && count == 1
}

func (b *Bot) networkStatusText(actor int64, route string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if b.networkStatus == nil {
		return "<b>Состояние VPN</b>\n\n⚪ Проверки соединения ещё не включены."
	}
	s, err := b.networkStatus.Snapshot(ctx)
	if err != nil {
		return "<b>Состояние VPN</b>\n\n⚪ Сейчас не удалось получить результаты. Доступность неизвестна; попробуйте позже."
	}
	admin := b.isMonitorAdmin(ctx, actor)
	if route != "" {
		return vpnmonitor.RenderRoute(s, route, admin)
	}
	return vpnmonitor.RenderOverview(s, admin)
}

func networkStatusKeyboard(details bool, route string) *telego.InlineKeyboardMarkup {
	var rows [][]telego.InlineKeyboardButton
	if details {
		for _, ids := range [][]string{{"stockholm", "finland"}, {"msk-stockholm", "msk-finland"}, {"legacy-finland", "direct"}} {
			rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(vpnmonitor.RouteLabels[ids[0]]).WithCallbackData(CallbackNetworkRoute+ids[0]), tu.InlineKeyboardButton(vpnmonitor.RouteLabels[ids[1]]).WithCallbackData(CallbackNetworkRoute+ids[1])))
		}
	} else {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Подробнее по маршрутам").WithCallbackData(CallbackNetworkDetails)))
	}
	refresh := CallbackNetworkStatus
	if route != "" {
		refresh = CallbackNetworkRoute + route
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Обновить статус").WithCallbackData(refresh)))
	if route != "" {
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Все маршруты").WithCallbackData(CallbackNetworkStatus)))
	}
	rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Мой доступ").WithCallbackData(CallbackAccountStatus), tu.InlineKeyboardButton("Установка VPN").WithCallbackData(CallbackHelpVPNSetup)), tu.InlineKeyboardRow(tu.InlineKeyboardButton("⬅️ Назад").WithCallbackData(CallbackHelpBack)))
	return tu.InlineKeyboard(rows...)
}

func (b *Bot) handleStatus(bot *telego.Bot, update telego.Update) {
	if b.networkStatus == nil {
		b.handleAccountStatus(bot, update)
		return
	}
	if !isPrivateActorUpdate(update) || update.Message == nil {
		return
	}
	_, err := bot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(update.Message.From.ID), Text: b.networkStatusText(update.Message.From.ID, ""), ParseMode: telego.ModeHTML, ReplyMarkup: networkStatusKeyboard(false, ""), LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		b.logger.Warn("Failed to deliver VPN status")
	}
}
func (b *Bot) handleNetworkStatusCallback(bot *telego.Bot, update telego.Update) {
	if !b.acknowledgePrivateCallback(bot, update) {
		return
	}
	data := update.CallbackQuery.Data
	route := ""
	details := false
	switch {
	case data == CallbackNetworkStatus:
	case data == CallbackNetworkDetails:
		details = true
	case strings.HasPrefix(data, CallbackNetworkRoute):
		route = strings.TrimPrefix(data, CallbackNetworkRoute)
		if _, ok := vpnmonitor.RouteLabels[route]; !ok {
			return
		}
	default:
		return
	}
	b.editStockholmMessage(bot, update, b.networkStatusText(update.CallbackQuery.From.ID, route), networkStatusKeyboard(details, route))
}

// SendMonitorAlert has no fallback to all admins or to user-provided chat IDs.
// The dedicated HTTP client has a fixed timeout and is never used for polling.
func (b *Bot) SendMonitorAlert(ctx context.Context, chatID int64, text string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !b.isMonitorOwner(ctx, chatID) || b.monitorBot == nil {
		return errors.New("monitor recipient unavailable")
	}
	_, err := b.monitorBot.SendMessage(&telego.SendMessageParams{ChatID: tu.ID(chatID), Text: text, ParseMode: telego.ModeHTML, ReplyMarkup: tu.InlineKeyboard(tu.InlineKeyboardRow(tu.InlineKeyboardButton("Состояние VPN").WithCallbackData(CallbackNetworkStatus))), LinkPreviewOptions: &telego.LinkPreviewOptions{IsDisabled: true}})
	if err != nil {
		return errors.New("monitor notification unavailable")
	}
	return nil
}
