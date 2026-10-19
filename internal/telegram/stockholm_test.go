package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
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
	if len(got) != 5 || got[0][0].CallbackData != "getkey_8" || got[1][0].CallbackData != CallbackStockholmSubscription || got[2][0].CallbackData != CallbackStockholmGuide || got[3][0].CallbackData != CallbackStockholmRouting || got[4][0].CallbackData != CallbackHelpBack {
		t.Fatalf("unexpected server navigation: %+v", got)
	}
	if got[1][0].Text != "🇸🇪 Sweden, Stockholm (subscription)" {
		t.Fatal("Stockholm subscription button must use the English location label")
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
	if chatID != 42 || mode != telego.ModeHTML || !strings.Contains(text, "x=1&amp;y=2") || !strings.Contains(text, "subscription") || !strings.Contains(text, "02 Jan 2030, 15:00 MSK") || strings.Contains(text, "Hiddify") {
		t.Fatalf("unsafe/incomplete subscription rendering: %q", text)
	}
	var keyboard telego.InlineKeyboardMarkup
	if err := json.Unmarshal(caller.calls[1].body["reply_markup"], &keyboard); err != nil {
		t.Fatal(err)
	}
	if len(keyboard.InlineKeyboard) != 4 || keyboard.InlineKeyboard[0][0].URL != url || keyboard.InlineKeyboard[1][0].CallbackData != CallbackStockholmGuide || keyboard.InlineKeyboard[2][0].CallbackData != CallbackStockholmRouting || !strings.Contains(string(caller.calls[1].body["link_preview_options"]), `"is_disabled":true`) {
		t.Fatal("subscription button or disabled link preview missing")
	}
	if strings.Contains(logs.String(), "private-bearer") {
		t.Fatal("subscription URL leaked to logs")
	}
}

func TestStockholmGuideAcknowledgesPrivateActorWithoutIssuingCredentials(t *testing.T) {
	issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://subscriptions.example/api/sub/private-bearer"}}
	b, caller, logs := stockholmTestBot(t, issuer)
	update := stockholmUpdate()
	update.CallbackQuery.Data = CallbackStockholmGuide
	b.handleStockholmGuide(b.bot, update)
	if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "editMessageText" {
		t.Fatal("guide must acknowledge the callback and privately render instructions")
	}
	if len(issuer.eligibleIDs) != 1 || issuer.eligibleIDs[0] != 42 || len(issuer.issueIDs) != 0 {
		t.Fatal("guide must authorize the actor without issuing credentials")
	}
	var text string
	var chatID int64
	json.Unmarshal(caller.calls[1].body["text"], &text)
	json.Unmarshal(caller.calls[1].body["chat_id"], &chatID)
	for _, platform := range []string{"iOS", "Android", "Windows", "macOS", "Linux", "Finland"} {
		if !strings.Contains(text, platform) {
			t.Fatalf("guide is missing %s guidance", platform)
		}
	}
	if chatID != 42 || strings.Contains(text+logs.String(), "private-bearer") || strings.Contains(text, "Hiddify") {
		t.Fatal("guide must be private, contain no subscription credential and use supported client instructions")
	}
	var keyboard telego.InlineKeyboardMarkup
	if err := json.Unmarshal(caller.calls[1].body["reply_markup"], &keyboard); err != nil {
		t.Fatal(err)
	}
	foundSubscription := false
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			foundSubscription = foundSubscription || button.CallbackData == CallbackStockholmSubscription
		}
	}
	if !foundSubscription {
		t.Fatal("guide must provide an explicit route back to the subscription")
	}
}

func TestStockholmRoutingProfilesAreOptInAndUseVerifiedRules(t *testing.T) {
	for _, tc := range []struct {
		name, callback, scheme string
		handler                func(*Bot, *telego.Bot, telego.Update)
	}{
		{"overview", CallbackStockholmRouting, "", (*Bot).handleStockholmRoutingGuide},
		{"Happ", CallbackStockholmHappRouting, "happ", (*Bot).handleStockholmHappRouting},
		{"INCY", CallbackStockholmIncyRouting, "incy", (*Bot).handleStockholmIncyRouting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://subscriptions.example/api/sub/private-bearer"}}
			b, caller, logs := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			update.CallbackQuery.Data = tc.callback
			tc.handler(b, b.bot, update)
			if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "editMessageText" || len(issuer.issueIDs) != 0 {
				t.Fatal("routing guide must acknowledge and display instructions without issuing credentials")
			}
			if len(issuer.eligibleIDs) != 1 || issuer.eligibleIDs[0] != 42 {
				t.Fatal("routing guide did not recheck the actor")
			}
			var text string
			json.Unmarshal(caller.calls[1].body["text"], &text)
			if len(text) > 4096 || strings.Contains(text+logs.String(), "private-bearer") {
				t.Fatal("guide is too long or contains a subscription credential")
			}
			if !strings.Contains(text, "direct foreign server") || !strings.Contains(text, "Proxy exception") || !strings.Contains(text, "IP-check pages") || !strings.Contains(text, "foreign exit IP") || !strings.Contains(text, "ISP IP") {
				t.Fatal("routing guide must explain foreign-IP exceptions and verify both egress paths")
			}
			if tc.scheme == "" {
				if !strings.Contains(text, "ISP IP") || !strings.Contains(text, "Moscow") {
					t.Fatal("guide must distinguish client-direct and server-direct routing")
				}
				return
			}
			start := strings.Index(text, "<code>")
			end := strings.Index(text, "</code>")
			if start < 0 || end <= start {
				t.Fatal("copyable routing profile missing")
			}
			link := html.UnescapeString(text[start+len("<code>") : end])
			prefix := tc.scheme + "://routing/add/"
			if !strings.HasPrefix(link, prefix) || strings.Contains(link, "/onadd/") {
				t.Fatal("routing import must use the non-forcing add contract")
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, prefix))
			if err != nil {
				t.Fatal(err)
			}
			var profile struct {
				GlobalProxy                                                     string
				DirectSites, DirectIp, ProxySites, ProxyIp, BlockSites, BlockIp []string
				DomainStrategy, FakeDNS                                         string
			}
			if err := json.Unmarshal(data, &profile); err != nil {
				t.Fatal(err)
			}
			if profile.GlobalProxy != "true" || profile.DomainStrategy != "IPOnDemand" || profile.FakeDNS != "false" || len(profile.ProxySites)+len(profile.ProxyIp)+len(profile.BlockSites)+len(profile.BlockIp) != 0 {
				t.Fatal("routing defaults must keep unmatched traffic on VPN and avoid extra overrides")
			}
			if !strings.Contains(string(data), `"geosite:category-ru"`) || strings.Contains(string(data), `"geosite:ru"`) || !strings.Contains(string(data), `"geoip:ru"`) || !strings.Contains(string(data), `"192.168.0.0/16"`) {
				t.Fatal("routing profile needs valid RU categories and private-network bypass")
			}
			var raw map[string]json.RawMessage
			json.Unmarshal(data, &raw)
			if _, exists := raw["RemoteDNSIP"]; exists {
				t.Fatal("routing guide must leave DNS resolvers at app defaults")
			}
			var keyboard telego.InlineKeyboardMarkup
			json.Unmarshal(caller.calls[1].body["reply_markup"], &keyboard)
			for _, row := range keyboard.InlineKeyboard {
				for _, button := range row {
					if strings.HasPrefix(button.URL, tc.scheme+":") {
						t.Fatal("routing import must be an explicit copy action, not a Telegram URL button")
					}
				}
			}
		})
	}
}

func TestStockholmRoutingGuidesRejectForgedAndRemovedAccess(t *testing.T) {
	for _, tc := range []struct {
		callback string
		handler  func(*Bot, *telego.Bot, telego.Update)
	}{
		{CallbackStockholmRouting, (*Bot).handleStockholmRoutingGuide},
		{CallbackStockholmHappRouting, (*Bot).handleStockholmHappRouting},
		{CallbackStockholmIncyRouting, (*Bot).handleStockholmIncyRouting},
	} {
		for _, forged := range []bool{false, true} {
			issuer := &fakeStockholmIssuer{eligible: false}
			b, caller, _ := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			update.CallbackQuery.Data = tc.callback
			if forged {
				update.CallbackQuery.From.ID = 7
			}
			tc.handler(b, b.bot, update)
			if len(caller.calls) == 0 || caller.calls[0].method != "answerCallbackQuery" || len(issuer.issueIDs) != 0 {
				t.Fatal("routing request must be acknowledged without issuance")
			}
			if forged && (len(caller.calls) != 1 || len(issuer.eligibleIDs) != 0) {
				t.Fatal("forged request reached private guide/backend")
			}
			if !forged && len(caller.calls) != 2 {
				t.Fatal("removed access must show generic unavailability")
			}
		}
	}
}

func TestStockholmGuideRejectsDisabledUnapprovedAndForgedCallbacks(t *testing.T) {
	for _, name := range []string{"disabled", "unapproved", "group", "other_actor", "missing_message", "wrong_callback", "ack_error"} {
		t.Run(name, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true}
			b, caller, _ := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			update.CallbackQuery.Data = CallbackStockholmGuide
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
				update.CallbackQuery.Data = CallbackStockholmSubscription
			case "ack_error":
				caller.failMethod = "answerCallbackQuery"
			}
			b.handleStockholmGuide(b.bot, update)
			if len(caller.calls) != expectedCalls || caller.calls[0].method != "answerCallbackQuery" || len(issuer.issueIDs) != 0 {
				t.Fatal("rejected guide request must only acknowledge or display unavailability")
			}
			if name != "unapproved" && len(issuer.eligibleIDs) != 0 {
				t.Fatal("forged or disabled request reached the authorization backend")
			}
		})
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
