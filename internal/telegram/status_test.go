package telegram

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	"github.com/supercakecrumb/otvali-xray-bot/internal/miniapp"
)

type fakeAccountReader struct {
	account  miniapp.Account
	err      error
	ids      []int64
	deadline bool
}

func (f *fakeAccountReader) Account(ctx context.Context, id int64) (miniapp.Account, error) {
	f.ids = append(f.ids, id)
	deadline, ok := ctx.Deadline()
	f.deadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 20*time.Second
	return f.account, f.err
}

func TestMiniAppEntryUsesOnlyAllowlistedOriginAndRetainsChatMenu(t *testing.T) {
	for _, raw := range []string{"http://app.example", "https://other.example", "https://app.example?secret=x", "https://user:secret@app.example", "https://app.example#access", "https://app.example/?"} {
		option, err := WithMiniAppURL(raw, []string{"https://app.example"})
		if err == nil || option != nil || strings.Contains(err.Error(), raw) {
			t.Fatal("invalid launch URL must fail closed without reflecting private inputs")
		}
	}
	b, caller, _ := stockholmTestBot(t, nil)
	option, err := WithMiniAppURL("https://app.example/vpn", []string{"https://app.example"})
	if err != nil {
		t.Fatal(err)
	}
	option(b)
	update := telego.Update{Message: &telego.Message{Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}}}
	b.handleHelp(b.bot, update)
	keyboard := callKeyboard(t, caller.calls[0])
	if len(keyboard.InlineKeyboard) != 4 || keyboard.InlineKeyboard[0][0].Text != "Открыть приложение" || keyboard.InlineKeyboard[0][0].WebApp == nil || keyboard.InlineKeyboard[0][0].WebApp.URL != "https://app.example/vpn" || keyboard.InlineKeyboard[0][0].URL != "" || keyboard.InlineKeyboard[0][0].CallbackData != "" || !reflect.DeepEqual(keyboard.InlineKeyboard[1:], helpKeyboard.InlineKeyboard) {
		t.Fatal("Mini App must launch with signed Telegram data while retaining all three chat actions")
	}
	clear, err := WithMiniAppURL("", nil)
	if err != nil {
		t.Fatal(err)
	}
	clear(b)
	if !reflect.DeepEqual(b.mainMenuKeyboard(), helpKeyboard) {
		t.Fatal("disabled/absent Mini App option must preserve the original menu")
	}
}

func TestAccountStatusCallbacksArePrivateBoundedAndReadOnly(t *testing.T) {
	for _, mode := range []string{"ready", "provider_error", "no_reader", "group", "other_actor", "wrong_callback", "ack_error"} {
		t.Run(mode, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true}
			b, caller, logs := stockholmTestBot(t, issuer)
			reader := &fakeAccountReader{account: miniapp.Account{Managed: true, ManagedState: "ready", LegacyFinlandState: "unavailable", CheckedAt: time.Date(2026, 10, 22, 18, 30, 0, 0, time.UTC), CheckScope: "account_access"}}
			WithAccountStatus(reader)(b)
			update := stockholmUpdate()
			update.CallbackQuery.Data = CallbackAccountStatus
			wantReads, wantCalls := 1, 2
			switch mode {
			case "provider_error":
				reader.err = errors.New("private-token private-bearer raw-response")
			case "no_reader":
				b.accountStatus, wantReads = nil, 0
			case "group":
				update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
				wantReads, wantCalls = 0, 1
			case "other_actor":
				update.CallbackQuery.From.ID = 99
				wantReads, wantCalls = 0, 1
			case "wrong_callback":
				update.CallbackQuery.Data = CallbackGetKey
				wantReads, wantCalls = 0, 1
			case "ack_error":
				caller.failMethod = "answerCallbackQuery"
				wantReads, wantCalls = 0, 1
			}
			b.handleAccountStatusCallback(b.bot, update)
			if len(reader.ids) != wantReads || len(caller.calls) != wantCalls || caller.calls[0].method != "answerCallbackQuery" || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
				t.Fatal("status callback escaped private actor checks or reached issuance")
			}
			if wantReads == 1 && (reader.ids[0] != 42 || !reader.deadline) {
				t.Fatal("status must use only the actor's bounded account check")
			}
			if mode == "ready" {
				text := callText(t, caller.calls[1])
				if !strings.Contains(text, "Доступ по подписке: активен") || !strings.Contains(text, "временно не удалось проверить") || !strings.Contains(text, "21:30 МСК") || !strings.Contains(text, "на устройстве здесь не проверяется") || len([]rune(text)) > 600 {
					t.Fatal("status must show independent account states without claiming traffic connectivity")
				}
				if !reflect.DeepEqual(callbacks(callKeyboard(t, caller.calls[1])), []string{CallbackAccountStatus, CallbackHelpVPNSetup, CallbackGetKey}) {
					t.Fatal("status must offer refresh, installation and access")
				}
			}
			for _, secret := range []string{"private-token", "private-bearer", "raw-response"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("readiness error leaked")
				}
				for _, call := range caller.calls {
					for _, value := range call.body {
						if strings.Contains(string(value), secret) {
							t.Fatal("readiness payload leaked")
						}
					}
				}
			}
		})
	}
}

func TestStatusCommandIsRegisteredAndNeverIssues(t *testing.T) {
	b, caller, _ := stockholmTestBot(t, nil)
	reader := &fakeAccountReader{account: miniapp.Account{ManagedState: "eligible_unprovisioned", LegacyFinlandState: "not_found", CheckScope: "account_access"}}
	WithAccountStatus(reader)(b)
	updates := make(chan telego.Update, 1)
	var err error
	b.bh, err = th.NewBotHandler(b.bot, updates)
	if err != nil {
		t.Fatal(err)
	}
	b.registerCommands()
	done, stopped := make(chan struct{}), make(chan struct{})
	b.bh.Use(func(bot *telego.Bot, update telego.Update, next th.Handler) { next(bot, update); close(done) })
	go func() { b.bh.Start(); close(stopped) }()
	updates <- telego.Update{Message: &telego.Message{Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}, Text: "/status", Entities: []telego.MessageEntity{{Type: "bot_command", Offset: 0, Length: 7}}}}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("registered status command did not finish")
	}
	close(updates)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("status handler did not stop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b.bh.StopWithContext(ctx)
	if len(reader.ids) != 1 || reader.ids[0] != 42 || len(caller.calls) != 1 || caller.calls[0].method != "sendMessage" || !strings.Contains(callText(t, caller.calls[0]), "подписка ещё не получена") {
		t.Fatal("/status must only read existing account state and never provision")
	}
}
