package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type fakeStockholmIssuer struct {
	eligible     bool
	eligibleIDs  []int64
	issueIDs     []int64
	subscription remnawave.Subscription
	err          error
	hasDeadline  bool
}

func (f *fakeStockholmIssuer) Eligible(_ context.Context, id int64) bool {
	f.eligibleIDs = append(f.eligibleIDs, id)
	return f.eligible
}

func (f *fakeStockholmIssuer) Issue(ctx context.Context, id int64) (remnawave.Subscription, error) {
	f.issueIDs = append(f.issueIDs, id)
	deadline, ok := ctx.Deadline()
	f.hasDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 25*time.Second
	return f.subscription, f.err
}

type stockholmTelegramCaller struct {
	t          *testing.T
	calls      []recordedTelegramCall
	failMethod string
}

func (r *stockholmTelegramCaller) Call(url string, data *ta.RequestData) (*ta.Response, error) {
	r.t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data.Buffer.Bytes(), &body); err != nil {
		r.t.Fatal(err)
	}
	method := url[strings.LastIndex(url, "/")+1:]
	r.calls = append(r.calls, recordedTelegramCall{method: method, body: body})
	if method == r.failMethod {
		return nil, errors.New("sensitive-telegram-request-content")
	}
	if method == "editMessageText" {
		return &ta.Response{Ok: true, Result: []byte(`{"message_id":17,"chat":{"id":42,"type":"private"}}`)}, nil
	}
	return &ta.Response{Ok: true, Result: []byte(`true`)}, nil
}

func stockholmTestBot(t *testing.T, issuer StockholmIssuer) (*Bot, *stockholmTelegramCaller, *bytes.Buffer) {
	t.Helper()
	caller := &stockholmTelegramCaller{t: t}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	api, err := telego.NewBot("123456789:"+strings.Repeat("x", 35), telego.WithAPICaller(caller), telego.WithLogger(telegramAPILogger{logger: logger}))
	if err != nil {
		t.Fatal(err)
	}
	// Nil legacy handler/database make any accidental FI access or admin notification fail.
	b := &Bot{bot: api, logger: logger}
	WithStockholm(issuer)(b)
	return b, caller, logs
}

func stockholmUpdate() telego.Update {
	return telego.Update{CallbackQuery: &telego.CallbackQuery{
		ID: "stockholm-callback", Data: CallbackStockholmSubscription,
		From:    telego.User{ID: 42, Username: "renamed_actor"},
		Message: &telego.Message{MessageID: 17, Chat: telego.Chat{ID: 42, Type: "private", Username: "old_username"}},
	}}
}

func TestStockholmMenuPreservesLegacyNavigation(t *testing.T) {
	b := testBotWithDisabledServer(t)
	legacy := b.buildServerButtons([]database.Server{{ID: 7, Country: "Estonia", City: "Narva"}, {ID: 8, Country: "Finland", City: "Helsinki"}})
	if got := b.appendStockholmButton(context.Background(), 42, legacy); len(got) != 2 {
		t.Fatal("Stockholm must be hidden by default")
	}
	issuer := &fakeStockholmIssuer{eligible: true}
	WithStockholm(issuer)(b)
	got := b.appendStockholmButton(context.Background(), 42, legacy)
	if len(got) != 3 || got[0][0].CallbackData != "getkey_8" || got[1][0].CallbackData != CallbackStockholmSubscription || got[2][0].CallbackData != CallbackHelpBack {
		t.Fatalf("unexpected server navigation: %+v", got)
	}
	if len(legacy) != 2 || legacy[1][0].CallbackData != CallbackHelpBack {
		t.Fatal("legacy navigation was mutated")
	}
	issuer.eligible = false
	if got := b.appendStockholmButton(context.Background(), 42, legacy); len(got) != 2 {
		t.Fatal("unauthorized actor saw Stockholm")
	}
	if len(issuer.issueIDs) != 0 {
		t.Fatal("menu must never issue credentials")
	}
}

func TestStockholmRejectsUnapprovedAndForgedCallbacks(t *testing.T) {
	for _, name := range []string{"disabled", "unapproved", "group", "other_actor", "missing_message", "wrong_callback"} {
		t.Run(name, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true}
			b, caller, _ := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			expectedCalls := 1
			switch name {
			case "disabled":
				b.stockholm = nil
				expectedCalls = 2
			case "unapproved":
				issuer.eligible = false
				expectedCalls = 2
			case "group":
				update.CallbackQuery.Message = &telego.Message{MessageID: 17, Chat: telego.Chat{ID: -42, Type: "group"}}
			case "other_actor":
				update.CallbackQuery.From.ID = 7
			case "missing_message":
				update.CallbackQuery.Message = nil
			case "wrong_callback":
				update.CallbackQuery.Data = "getkey_8"
			}
			b.handleStockholmSubscription(b.bot, update)
			if len(caller.calls) != expectedCalls || caller.calls[0].method != "answerCallbackQuery" {
				t.Fatalf("callback not safely acknowledged: %+v", caller.calls)
			}
			if len(issuer.issueIDs) != 0 {
				t.Fatal("unauthorized callback issued a subscription")
			}
			if name != "unapproved" && len(issuer.eligibleIDs) != 0 {
				t.Fatal("forged/disabled callback reached authorization backend")
			}
		})
	}
}

func TestStockholmSubscriptionUsesActorIDAndPrivateDelivery(t *testing.T) {
	url := "https://subscriptions.example/api/sub/private-bearer?x=1&y=2"
	issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: url, ExpiresAt: time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)}}
	b, caller, logs := stockholmTestBot(t, issuer)
	b.handleStockholmSubscription(b.bot, stockholmUpdate())
	if len(issuer.eligibleIDs) != 1 || issuer.eligibleIDs[0] != 42 || len(issuer.issueIDs) != 1 || issuer.issueIDs[0] != 42 || !issuer.hasDeadline {
		t.Fatal("issuer did not receive the bounded authenticated actor request")
	}
	if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "editMessageText" {
		t.Fatalf("unexpected Telegram delivery: %+v", caller.calls)
	}
	var text, mode string
	var chatID int64
	json.Unmarshal(caller.calls[1].body["text"], &text)
	json.Unmarshal(caller.calls[1].body["parse_mode"], &mode)
	json.Unmarshal(caller.calls[1].body["chat_id"], &chatID)
	if chatID != 42 || mode != telego.ModeHTML || !strings.Contains(text, "x=1&amp;y=2") || !strings.Contains(text, "Hiddify") || !strings.Contains(text, "02.01.2030 15:00 МСК") {
		t.Fatalf("unsafe/incomplete subscription rendering: %q", text)
	}
	var keyboard telego.InlineKeyboardMarkup
	if err := json.Unmarshal(caller.calls[1].body["reply_markup"], &keyboard); err != nil {
		t.Fatal(err)
	}
	if len(keyboard.InlineKeyboard) != 2 || keyboard.InlineKeyboard[0][0].URL != url || !strings.Contains(string(caller.calls[1].body["link_preview_options"]), `"is_disabled":true`) {
		t.Fatal("subscription button or disabled link preview missing")
	}
	if strings.Contains(logs.String(), "private-bearer") {
		t.Fatal("subscription URL leaked to logs")
	}
}

func TestStockholmFailuresDoNotExposeSecretsOrTouchLegacy(t *testing.T) {
	for _, name := range []string{"issuer_error", "invalid_url", "delivery_error", "ack_error"} {
		t.Run(name, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://subscriptions.example/api/sub/private-bearer"}}
			b, caller, logs := stockholmTestBot(t, issuer)
			switch name {
			case "issuer_error":
				issuer.err = errors.New("private-api-token private-bearer raw-response")
			case "invalid_url":
				issuer.subscription.URL = "javascript:private-bearer"
			case "delivery_error":
				caller.failMethod = "editMessageText"
			case "ack_error":
				caller.failMethod = "answerCallbackQuery"
			}
			b.handleStockholmSubscription(b.bot, stockholmUpdate())
			if name == "ack_error" && len(issuer.issueIDs) != 0 {
				t.Fatal("issuance continued after failed acknowledgement")
			}
			for _, secret := range []string{"private-api-token", "private-bearer", "raw-response", "sensitive-telegram-request-content"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("secret leaked to logs")
				}
				if name == "issuer_error" || name == "invalid_url" {
					for _, call := range caller.calls {
						for _, value := range call.body {
							if strings.Contains(string(value), secret) {
								t.Fatal("secret leaked in failure response")
							}
						}
					}
				}
			}
			for _, call := range caller.calls {
				if call.method != "answerCallbackQuery" && call.method != "editMessageText" {
					t.Fatal("failure triggered an unexpected Telegram side effect")
				}
			}
		})
	}
}
