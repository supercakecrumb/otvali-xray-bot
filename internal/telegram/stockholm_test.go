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
	if method == "editMessageText" || method == "sendMessage" {
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
	b := &Bot{bot: api, botID: 123456789, primaryBotID: 123456789, logger: logger}
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

func callText(t *testing.T, call recordedTelegramCall) string {
	t.Helper()
	var text string
	if err := json.Unmarshal(call.body["text"], &text); err != nil {
		t.Fatal(err)
	}
	return text
}

func callKeyboard(t *testing.T, call recordedTelegramCall) *telego.InlineKeyboardMarkup {
	t.Helper()
	var keyboard telego.InlineKeyboardMarkup
	if err := json.Unmarshal(call.body["reply_markup"], &keyboard); err != nil {
		t.Fatal(err)
	}
	return &keyboard
}

func callbacks(keyboard *telego.InlineKeyboardMarkup) []string {
	var result []string
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData != "" {
				result = append(result, button.CallbackData)
			}
		}
	}
	return result
}

func TestSingleSubscriptionAccessMenu(t *testing.T) {
	issuer := &fakeStockholmIssuer{eligible: true}
	b, _, _ := stockholmTestBot(t, issuer)
	rows, err := b.getServerButtons(42)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][0].Text != "🌐 Подписка Otvali — все маршруты" || rows[0][0].CallbackData != CallbackStockholmSubscription || rows[1][0].CallbackData != CallbackIndividualKeys || rows[2][0].CallbackData != CallbackHelpBack {
		t.Fatal("access must contain one subscription, individual keys and back")
	}
	for _, row := range rows {
		if len(row) != 1 {
			t.Fatal("simple one-action rows expected")
		}
	}
	if len(issuer.issueIDs) != 0 {
		t.Fatal("menu must never issue credentials")
	}
	issuer.eligible = false
	rows, _ = b.getServerButtons(42)
	if len(rows) != 2 || rows[0][0].CallbackData != CallbackIndividualKeys {
		t.Fatal("subscription must remain eligibility gated")
	}
	b.stockholm = nil
	rows, _ = b.getServerButtons(42)
	if len(rows) != 2 {
		t.Fatal("legacy access must remain without the managed issuer")
	}
}

func TestStockholmRejectsUnapprovedAndForgedCallbacks(t *testing.T) {
	for _, name := range []string{"disabled", "unapproved", "group", "other_actor", "missing_message", "wrong_callback", "ack_error"} {
		t.Run(name, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true}
			b, caller, _ := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			expected := 1
			switch name {
			case "disabled":
				b.stockholm = nil
				expected = 2
			case "unapproved":
				issuer.eligible = false
				expected = 2
			case "group":
				update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
			case "other_actor":
				update.CallbackQuery.From.ID = 7
			case "missing_message":
				update.CallbackQuery.Message = nil
			case "wrong_callback":
				update.CallbackQuery.Data = "getkey_1"
			case "ack_error":
				caller.failMethod = "answerCallbackQuery"
			}
			b.handleStockholmSubscription(b.bot, update)
			if len(caller.calls) != expected || caller.calls[0].method != "answerCallbackQuery" || len(issuer.issueIDs) != 0 {
				t.Fatal("invalid callback must acknowledge and never issue")
			}
			if name != "unapproved" && len(issuer.eligibleIDs) != 0 {
				t.Fatal("forged callback reached backend")
			}
		})
	}
}

func TestSubscriptionIsOnePrivateConciseMessage(t *testing.T) {
	const privateURL = "https://subscriptions.example/api/sub/private-bearer?x=1&y=2"
	issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: privateURL, ExpiresAt: time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)}}
	b, caller, logs := stockholmTestBot(t, issuer)
	b.handleStockholmSubscription(b.bot, stockholmUpdate())
	if len(issuer.issueIDs) != 1 || issuer.issueIDs[0] != 42 || !issuer.hasDeadline || len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "editMessageText" {
		t.Fatal("bounded actor request must deliver once in place")
	}
	text := callText(t, caller.calls[1])
	if !strings.Contains(text, "x=1&amp;y=2") || !strings.Contains(text, "02.01.2030 15:00") || !strings.Contains(text, "/help_setup") || strings.Contains(text, "2099") {
		t.Fatal("escaped URL, date or setup shortcut missing")
	}
	var chatID int64
	json.Unmarshal(caller.calls[1].body["chat_id"], &chatID)
	var mode string
	json.Unmarshal(caller.calls[1].body["parse_mode"], &mode)
	if chatID != 42 || mode != telego.ModeHTML || len([]rune(text)) > 650 {
		t.Fatal("private concise HTML response required")
	}
	got := callbacks(callKeyboard(t, caller.calls[1]))
	if len(got) != 3 || got[0] != CallbackQRSubscription || got[1] != CallbackHelpVPNSetup || got[2] != CallbackGetKey {
		t.Fatal("delivery must offer QR, setup and access")
	}
	if strings.Contains(logs.String(), "private-bearer") || !strings.Contains(string(caller.calls[1].body["link_preview_options"]), `"is_disabled":true`) {
		t.Fatal("credential logging or preview leak")
	}
}

func TestSubscriptionShowsActualUnlimitedPolicy(t *testing.T) {
	for _, limit := range []int64{0, 1234567} {
		issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://subscriptions.example/api/sub/private-bearer", TrafficLimitBytes: limit}}
		b, caller, _ := stockholmTestBot(t, issuer)
		b.handleStockholmSubscription(b.bot, stockholmUpdate())
		text := callText(t, caller.calls[1])
		if !strings.Contains(text, "Без автоматического окончания") || strings.Contains(text, "2099") || strings.Contains(text, "Действует до") || strings.Contains(text, "Безлимитный трафик") != (limit == 0) {
			t.Fatal("display must reflect actual provider policy")
		}
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
					t.Fatal("unexpected external side effect")
				}
			}
		})
	}
}
