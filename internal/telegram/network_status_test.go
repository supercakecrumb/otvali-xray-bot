package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/vpnmonitor"
)

type fakeNetworkReader struct {
	calls    int
	deadline bool
	err      error
}

func (f *fakeNetworkReader) Snapshot(ctx context.Context) (vpnmonitor.Snapshot, error) {
	f.calls++
	d, ok := ctx.Deadline()
	f.deadline = ok && time.Until(d) <= 4*time.Second
	return vpnmonitor.Snapshot{}, f.err
}

func TestNetworkStatusCallbacksArePrivateCachedAndReadOnly(t *testing.T) {
	for _, mode := range []string{"overview", "details", "route", "group", "other_actor", "unknown_route", "ack_error", "snapshot_error"} {
		t.Run(mode, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true}
			b, caller, logs := stockholmTestBot(t, issuer)
			reader := &fakeNetworkReader{}
			WithNetworkStatus(reader, 42)(b)
			u := stockholmUpdate()
			u.CallbackQuery.Data = CallbackNetworkStatus
			wantReads, wantCalls := 1, 2
			switch mode {
			case "details":
				u.CallbackQuery.Data = CallbackNetworkDetails
			case "route":
				u.CallbackQuery.Data = CallbackNetworkRoute + "msk-finland"
			case "group":
				u.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
				wantReads, wantCalls = 0, 1
			case "other_actor":
				u.CallbackQuery.From.ID = 99
				wantReads, wantCalls = 0, 1
			case "unknown_route":
				u.CallbackQuery.Data = CallbackNetworkRoute + "private-hostname"
				wantReads, wantCalls = 0, 1
			case "ack_error":
				caller.failMethod = "answerCallbackQuery"
				wantReads, wantCalls = 0, 1
			case "snapshot_error":
				reader.err = errors.New("private database error")
			}
			b.handleNetworkStatusCallback(b.bot, u)
			if reader.calls != wantReads || len(caller.calls) != wantCalls || caller.calls[0].method != "answerCallbackQuery" || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
				t.Fatal("status callback escaped identity checks or issued access")
			}
			if reader.calls > 0 && !reader.deadline {
				t.Fatal("snapshot query has no bounded timeout")
			}
			if strings.Contains(logs.String(), "private database") {
				t.Fatal("snapshot error leaked")
			}
			if wantCalls == 2 {
				text := callText(t, caller.calls[1])
				if strings.Contains(text, "private database") {
					t.Fatal("snapshot error sent to user")
				}
				kb := callKeyboard(t, caller.calls[1])
				found := false
				for _, row := range kb.InlineKeyboard {
					for _, button := range row {
						if button.Text == "Обновить статус" {
							found = true
						}
					}
				}
				if !found {
					t.Fatal("refresh must be labelled as cached status")
				}
			}
		})
	}
}

func TestNetworkStatusCommandDoesNotProvisionOrNotify(t *testing.T) {
	issuer := &fakeStockholmIssuer{eligible: true}
	b, caller, _ := stockholmTestBot(t, issuer)
	reader := &fakeNetworkReader{}
	WithNetworkStatus(reader, 42)(b)
	u := telego.Update{Message: &telego.Message{Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}}}
	b.handleStatus(b.bot, u)
	if reader.calls != 1 || len(caller.calls) != 1 || caller.calls[0].method != "sendMessage" || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
		t.Fatal("status must read only existing health snapshot")
	}
	if !strings.Contains(callText(t, caller.calls[0]), "Результатов пока нет") {
		t.Fatal("empty snapshot should not claim success")
	}
	u.Message.Chat.Type = "group"
	b.handleStatus(b.bot, u)
	if reader.calls != 1 || len(caller.calls) != 1 {
		t.Fatal("group query escaped private chat guard")
	}
}

func TestMonitorAlertsOnlyReachConfiguredBoundAdmin(t *testing.T) {
	for _, mode := range []string{"owner", "other_admin", "owner_revoked", "no_owner", "no_transport", "cancelled", "telegram_error"} {
		t.Run(mode, func(t *testing.T) {
			b, caller, logs := stockholmTestBot(t, nil)
			b.monitorOwnerID = 42
			b.monitorBot = b.bot
			checks := 0
			b.monitorOwnerCheck = func(_ context.Context, id int64) bool { checks++; return id == 42 && mode != "owner_revoked" }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			id := int64(42)
			switch mode {
			case "other_admin":
				id = 99
			case "no_owner":
				b.monitorOwnerID = 0
			case "no_transport":
				b.monitorBot = nil
			case "cancelled":
				cancel()
			case "telegram_error":
				caller.failMethod = "sendMessage"
			}
			err := b.SendMonitorAlert(ctx, id, "test status")
			expected := mode == "owner" || mode == "telegram_error"
			if (len(caller.calls) == 1) != expected {
				t.Fatal("alert recipient or transport guard failed")
			}
			if mode == "owner" && err != nil || mode != "owner" && err == nil {
				t.Fatal("delivery outcome incorrect")
			}
			if mode == "other_admin" && checks != 0 {
				t.Fatal("foreign chat must be rejected before database check")
			}
			if err != nil && strings.Contains(err.Error(), "sensitive-telegram") || strings.Contains(logs.String(), "sensitive-telegram") {
				t.Fatal("Telegram error payload leaked")
			}
		})
	}
}
