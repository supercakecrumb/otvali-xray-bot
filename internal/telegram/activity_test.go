package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type recordedActivity struct {
	actor            int64
	source, kind, id string
}
type fakeActivityRecorder struct {
	calls   []recordedActivity
	err     error
	bounded bool
}

func (f *fakeActivityRecorder) RecordActivity(ctx context.Context, actor int64, source, kind, id string, _ time.Time) error {
	deadline, ok := ctx.Deadline()
	f.bounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 250*time.Millisecond
	f.calls = append(f.calls, recordedActivity{actor, source, kind, id})
	return f.err
}

func TestActivityCountsPrivateUpdatesWithoutContentsAndDoesNotBlockHandler(t *testing.T) {
	b, _, logs := stockholmTestBot(t, nil)
	recorder := &fakeActivityRecorder{err: errors.New("private-storage-error")}
	b.activity = recorder
	update := telego.Update{UpdateID: 123, Message: &telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "private-message-vless://secret"}}
	nextCalls := 0
	for i := 0; i < 2; i++ {
		b.activityMiddleware()(b.bot, update, func(*telego.Bot, telego.Update) { nextCalls++ })
	}
	if nextCalls != 2 || len(recorder.calls) != 2 || !recorder.bounded || recorder.calls[0] != recorder.calls[1] {
		t.Fatal("identical Telegram redelivery must retain event identity and storage failure must not stop handler")
	}
	first := recorder.calls[0]
	if first.actor != 42 || first.kind != "message" || !database.ValidActivity(first.source, first.kind, first.id) {
		t.Fatal("unexpected event identity or classification")
	}
	update.UpdateID++
	update.Message.Text = "/get_key secret-argument"
	b.recordTelegramActivity(update, "")
	if recorder.calls[2].kind != "command" || recorder.calls[2].id == first.id {
		t.Fatal("commands must be distinct actions without retaining command text")
	}
	update = stockholmUpdate()
	update.UpdateID = 125
	b.recordTelegramActivity(update, "")
	if recorder.calls[3].kind != "callback" {
		t.Fatal("button event missing")
	}
	update.CallbackQuery.From.ID = 7
	b.recordTelegramActivity(update, "")
	update.CallbackQuery.From.ID = 42
	update.UpdateID = 0
	b.recordTelegramActivity(update, "")
	if len(recorder.calls) != 4 {
		t.Fatal("forged actor or absent update identity was recorded")
	}
	if strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "secret") {
		t.Fatal("activity failure leaked source text or storage error")
	}
}

func TestActivityCountsOnlySuccessfulCredentialDelivery(t *testing.T) {
	for _, fail := range []bool{false, true} {
		issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://sub.example/private"}}
		b, caller, _ := stockholmTestBot(t, issuer)
		recorder := &fakeActivityRecorder{}
		b.activity = recorder
		if fail {
			caller.failMethod = "editMessageText"
		}
		update := stockholmUpdate()
		update.UpdateID = 345
		b.handleStockholmSubscription(b.bot, update)
		if fail && len(recorder.calls) != 0 {
			t.Fatal("failed Telegram delivery must not count as received key")
		}
		if !fail && (len(recorder.calls) != 1 || recorder.calls[0].kind != "subscription_delivered") {
			t.Fatal("successful subscription must have its own delivery category")
		}
		keyIssuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true}, keys: managedTestKeys()}
		b.stockholm = keyIssuer
		recorder.calls = nil
		update.CallbackQuery.Data = CallbackManagedKeyPrefix + keyIssuer.keys[0].ID
		b.handleManagedKey(b.bot, update)
		if fail && len(recorder.calls) != 0 {
			t.Fatal("failed standalone key delivery counted")
		}
		if !fail && (len(recorder.calls) != 1 || recorder.calls[0].kind != "key_delivered") {
			t.Fatal("successful standalone key delivery missing")
		}
	}
}

func TestActivityCountsQRDeliveryAndNotFailedUpload(t *testing.T) {
	for _, fail := range []bool{false, true} {
		issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://sub.example/private"}}
		b, caller, _ := qrTestBot(t, issuer)
		recorder := &fakeActivityRecorder{}
		b.activity = recorder
		if fail {
			caller.failMethod = "sendPhoto"
		}
		update := qrCommand("/qr_subscription")
		update.UpdateID = 789
		b.handleQRSubscription(b.bot, update)
		if fail && len(recorder.calls) != 0 {
			t.Fatal("failed QR delivery counted")
		}
		if !fail && (len(recorder.calls) != 1 || recorder.calls[0].kind != "subscription_delivered") {
			t.Fatal("successful QR delivery must use same subscription metric")
		}
	}
}
