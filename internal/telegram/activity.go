package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
)

type activityRecorder interface {
	RecordActivity(context.Context, int64, string, string, string, time.Time) error
}

func telegramActivity(update telego.Update, botID int64) (actor int64, kind, digest string) {
	if botID <= 0 || update.UpdateID <= 0 || !isPrivateActorUpdate(update) {
		return
	}
	if update.Message != nil {
		actor, kind = update.Message.From.ID, "message"
		for _, entity := range update.Message.Entities {
			if entity.Type == "bot_command" && entity.Offset == 0 {
				kind = "command"
				break
			}
		}
		// Commands without Telegram entities are still commands, never stored text.
		if strings.HasPrefix(update.Message.Text, "/") {
			kind = "command"
		}
	} else if update.CallbackQuery != nil {
		actor, kind = update.CallbackQuery.From.ID, "callback"
	}
	if actor <= 0 {
		return 0, "", ""
	}
	// Telegram update IDs are unique only within one bot's update stream.
	sum := sha256.Sum256([]byte("telegram:" + strconv.FormatInt(botID, 10) + ":" + strconv.Itoa(update.UpdateID)))
	return actor, kind, hex.EncodeToString(sum[:])
}

func (b *Bot) recordTelegramActivity(update telego.Update, override string) {
	actor, kind, eventID := telegramActivity(update, b.ID())
	if actor <= 0 || b.activity == nil {
		return
	}
	if b.ID() == b.legacyActivityBotID {
		// The first deployment persisted these unscoped digests. Their immutable
		// owner keeps that namespace; all other bots use their numeric bot ID.
		// This preserves replay deduplication without rewriting historical rows.
		sum := sha256.Sum256([]byte("telegram:" + strconv.Itoa(update.UpdateID)))
		eventID = hex.EncodeToString(sum[:])
	}
	if override != "" {
		kind = override
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if b.activity.RecordActivity(ctx, actor, "telegram", kind, eventID, time.Now()) != nil {
		b.logger.Warn("Activity counter unavailable")
	}
}

// Installed after binding middleware. One update is one incoming action:
// messages and commands are disjoint; callbacks are button actions. Delivery
// outcomes are separate events and must not be added to incoming-action totals.
func (b *Bot) activityMiddleware() th.Middleware {
	return func(bot *telego.Bot, update telego.Update, next th.Handler) {
		b.recordTelegramActivity(update, "")
		next(bot, update)
	}
}
