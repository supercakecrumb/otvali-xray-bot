package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

const fakeTelegramProxyURL = "https://t.me/proxy?server=proxy.example&port=8443&secret=ee00112233445566778899aabbccddeeff6578616d706c652e636f6d"

// fakeProxyURL returns a distinct, valid fictional link per route.
func fakeProxyURL(port int) string {
	return strings.Replace(fakeTelegramProxyURL, "port=8443", "port="+strconv.Itoa(port), 1)
}

type fakeTelegramProxy struct {
	issueIDs    []int64
	credentials []telegramproxy.Credential
	unavailable []string
	err         error
	bounded     bool
}

func (f *fakeTelegramProxy) IssueAll(ctx context.Context, actor int64) ([]telegramproxy.Credential, []string, error) {
	f.issueIDs = append(f.issueIDs, actor)
	deadline, ok := ctx.Deadline()
	f.bounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= telegramProxyTimeout
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.credentials, f.unavailable, nil
}

func proxyFixture() *fakeTelegramProxy {
	return &fakeTelegramProxy{credentials: []telegramproxy.Credential{
		{EndpointID: "finland", Name: "Финляндия", URL: fakeProxyURL(8443)},
		{EndpointID: "stockholm", Name: "Швеция", URL: fakeProxyURL(8444)},
		{EndpointID: "msk-finland", Name: "Москва → Финляндия", URL: fakeProxyURL(10445)},
		{EndpointID: "msk-stockholm", Name: "Москва → Швеция", URL: fakeProxyURL(11445)},
	}, unavailable: []string{}}
}

func proxyUpdate(data string) telego.Update {
	update := stockholmUpdate()
	update.UpdateID = 123
	update.CallbackQuery.Data = data
	return update
}

func proxyCommand(text string) telego.Update {
	return telego.Update{UpdateID: 124, Message: &telego.Message{MessageID: 9, From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: text}}
}

type proxyButton struct{ text, url, callback string }

func proxyButtons(t *testing.T, call recordedTelegramCall) [][]proxyButton {
	t.Helper()
	var rows [][]proxyButton
	for _, row := range callKeyboard(t, call).InlineKeyboard {
		var out []proxyButton
		for _, button := range row {
			out = append(out, proxyButton{button.Text, button.URL, button.CallbackData})
		}
		rows = append(rows, out)
	}
	return rows
}

func linkPreviewDisabled(t *testing.T, call recordedTelegramCall) bool {
	t.Helper()
	var options telego.LinkPreviewOptions
	return json.Unmarshal(call.body["link_preview_options"], &options) == nil && options.IsDisabled
}

// assertLoading checks the interim state: no buttons, no link, no preview.
func assertLoading(t *testing.T, call recordedTelegramCall, method string) {
	t.Helper()
	if call.method != method || callText(t, call) != telegramProxyLoadingText || len(call.body["reply_markup"]) != 0 || !linkPreviewDisabled(t, call) {
		t.Fatalf("loading state must be a button-free %s", method)
	}
}

func assertAllLinks(t *testing.T, call recordedTelegramCall, provider *fakeTelegramProxy) {
	t.Helper()
	if call.method != "editMessageText" || callText(t, call) != telegramProxyLinksText || !linkPreviewDisabled(t, call) {
		t.Fatal("final view must replace the loading message with the all-routes text")
	}
	want := [][]proxyButton{}
	for _, credential := range provider.credentials {
		want = append(want, []proxyButton{{credential.Name, credential.URL, ""}})
	}
	want = append(want, []proxyButton{{"Главная", "", CallbackHelpBack}})
	if got := proxyButtons(t, call); !reflect.DeepEqual(got, want) {
		t.Fatalf("one launch button per route in order, then home: %v", got)
	}
}

func TestTelegramProxyCommandIssuesEveryRouteInOneMessage(t *testing.T) {
	for _, command := range []string{"/telegram", "/start telegram", "/start@rkn_otvali_bot telegram"} {
		t.Run(command, func(t *testing.T) {
			provider := proxyFixture()
			b, caller, logs := stockholmTestBot(t, nil)
			WithTelegramProxy(provider)(b)
			recorder := &fakeActivityRecorder{}
			b.activity = recorder
			update := proxyCommand(command)
			if command == "/telegram" {
				b.handleTelegramProxy(b.bot, update)
			} else {
				b.handleStart(b.bot, update)
			}
			if len(caller.calls) != 2 || !provider.bounded || !reflect.DeepEqual(provider.issueIDs, []int64{42}) {
				t.Fatal("opening Telegram must issue every route for the actor once")
			}
			assertLoading(t, caller.calls[0], "sendMessage")
			assertAllLinks(t, caller.calls[1], provider)
			var messageID int
			if json.Unmarshal(caller.calls[1].body["message_id"], &messageID) != nil || messageID != 17 {
				t.Fatal("links must replace the loading message instead of adding another one")
			}
			if strings.Contains(callText(t, caller.calls[1]), "ee001122") || strings.Contains(logs.String(), "ee001122") || strings.Contains(logs.String(), "t.me/proxy") {
				t.Fatal("links belong only in launch buttons")
			}
			if len(recorder.calls) != 1 || recorder.calls[0].kind != "telegram_proxy_delivered" || !database.ValidActivity(recorder.calls[0].source, recorder.calls[0].kind, recorder.calls[0].id) {
				t.Fatal("delivery needs one safe outcome counter without credentials")
			}
		})
	}
}

func TestTelegramProxyCallbacksAndLegacyRouteButtonsShowAllLinks(t *testing.T) {
	entries := []string{CallbackTelegramProxy, CallbackHelpTelegram}
	for _, id := range []string{"finland", "stockholm", "msk-finland", "msk-stockholm"} {
		entries = append(entries, CallbackTelegramProxyIssue+id)
	}
	for _, data := range entries {
		t.Run(data, func(t *testing.T) {
			provider := proxyFixture()
			b, caller, _ := stockholmTestBot(t, nil)
			WithTelegramProxy(provider)(b)
			update := proxyUpdate(data)
			if data == CallbackHelpTelegram {
				b.handleHelpCallback(b.bot, update)
			} else {
				b.handleTelegramProxyCallback(b.bot, update)
			}
			if len(caller.calls) != 3 || caller.calls[0].method != "answerCallbackQuery" || !reflect.DeepEqual(provider.issueIDs, []int64{42}) || !provider.bounded {
				t.Fatal("callback must acknowledge once and issue all routes for the actor")
			}
			assertLoading(t, caller.calls[1], "editMessageText")
			assertAllLinks(t, caller.calls[2], provider)
		})
	}
}

func TestTelegramProxyFromPhotoSendsOneNewMessage(t *testing.T) {
	provider := proxyFixture()
	b, caller, _ := stockholmTestBot(t, nil)
	WithTelegramProxy(provider)(b)
	update := proxyUpdate(CallbackHelpTelegram)
	update.CallbackQuery.Message = &telego.Message{MessageID: 5, Chat: telego.Chat{ID: 42, Type: "private"}, Photo: []telego.PhotoSize{{FileID: "fictional-photo"}}}
	b.handleHelpCallback(b.bot, update)
	if len(caller.calls) != 3 || caller.calls[0].method != "answerCallbackQuery" {
		t.Fatal("photo navigation must acknowledge and render one text message")
	}
	assertLoading(t, caller.calls[1], "sendMessage")
	assertAllLinks(t, caller.calls[2], provider)
}

func TestTelegramProxyPartialResultOffersRefresh(t *testing.T) {
	for _, mode := range []string{"unavailable", "invalid_url", "unknown_route", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			provider := proxyFixture()
			switch mode {
			case "unavailable":
				provider.credentials = append(provider.credentials[:1], provider.credentials[2:]...)
				provider.unavailable = []string{"stockholm"}
			case "invalid_url":
				provider.credentials[1].URL = "https://evil.example/proxy?secret=private-secret"
			case "unknown_route":
				provider.credentials[1].EndpointID = "secret:evil"
			case "duplicate":
				provider.credentials[1].EndpointID = "finland"
			}
			b, caller, logs := stockholmTestBot(t, nil)
			WithTelegramProxy(provider)(b)
			recorder := &fakeActivityRecorder{}
			b.activity = recorder
			b.handleTelegramProxyCallback(b.bot, proxyUpdate(CallbackTelegramProxy))
			final := caller.calls[len(caller.calls)-1]
			text, rows := callText(t, final), proxyButtons(t, final)
			if !strings.HasPrefix(text, telegramProxyLinksText) || !strings.Contains(text, "временно недоступна") || !strings.Contains(text, "«Обновить»") {
				t.Fatal("partial result must explain that some routes are temporarily unavailable")
			}
			if len(rows) != 4 || !reflect.DeepEqual(rows[3], []proxyButton{{"Обновить", "", CallbackTelegramProxy}, {"Главная", "", CallbackHelpBack}}) {
				t.Fatal("partial result needs three launch buttons, refresh and home")
			}
			for _, row := range rows[:3] {
				if row[0].callback != "" || !telegramproxy.ValidCredentialURL(row[0].url) || row[0].url == provider.credentials[1].URL && mode != "unavailable" {
					t.Fatal("only valid issued routes may become launch buttons")
				}
			}
			if strings.Contains(text, "private-") || strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "t.me/proxy") || len(recorder.calls) != 1 {
				t.Fatal("partial delivery leaked data or was not counted once")
			}
		})
	}
}

func TestTelegramProxyRejectsForgedCallbacksAndCommands(t *testing.T) {
	for _, mode := range []string{"group", "other_actor", "no_message", "unknown_route", "wrong_callback", "ack_failed"} {
		t.Run(mode, func(t *testing.T) {
			provider := proxyFixture()
			b, caller, _ := stockholmTestBot(t, nil)
			WithTelegramProxy(provider)(b)
			update := proxyUpdate(CallbackTelegramProxyIssue + "msk-stockholm")
			switch mode {
			case "group":
				update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
			case "other_actor":
				update.CallbackQuery.From.ID = 7
			case "no_message":
				update.CallbackQuery.Message = nil
			case "unknown_route":
				update.CallbackQuery.Data = CallbackTelegramProxyIssue + "secret:evil"
			case "wrong_callback":
				update.CallbackQuery.Data = "getkey_1"
			case "ack_failed":
				caller.failMethod = "answerCallbackQuery"
			}
			b.handleTelegramProxyCallback(b.bot, update)
			if len(caller.calls) != 1 || caller.calls[0].method != "answerCallbackQuery" || len(provider.issueIDs) != 0 {
				t.Fatal("forged callback reached provider or disclosed a grant")
			}
		})
	}
	provider := proxyFixture()
	b, caller, _ := stockholmTestBot(t, nil)
	WithTelegramProxy(provider)(b)
	for _, update := range []telego.Update{{}, {Message: &telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: -42, Type: "group"}}}, {Message: &telego.Message{From: &telego.User{ID: 7}, Chat: telego.Chat{ID: 42, Type: "private"}}}} {
		b.handleTelegramProxy(b.bot, update)
	}
	if len(provider.issueIDs)+len(caller.calls) != 0 {
		t.Fatal("non-private command must not issue or expose links")
	}
}

func TestTelegramProxyFailuresNeverLeakOrRecordDelivery(t *testing.T) {
	for _, mode := range []string{"unavailable", "revoked", "ineligible", "invalid_url", "wrong_route", "empty", "send_failure", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			provider := proxyFixture()
			b, caller, logs := stockholmTestBot(t, nil)
			WithTelegramProxy(provider)(b)
			recorder := &fakeActivityRecorder{}
			b.activity = recorder
			switch mode {
			case "unavailable":
				provider.err = errors.New("private-api-token private-secret")
			case "revoked":
				provider.err = telegramproxy.ErrInactive
			case "ineligible":
				provider.err = telegramproxy.ErrNotEligible
			case "invalid_url":
				for i := range provider.credentials {
					provider.credentials[i].URL = "https://evil.example/proxy?secret=private-secret"
				}
			case "wrong_route":
				for i := range provider.credentials {
					provider.credentials[i].EndpointID = "private-route"
				}
			case "empty":
				provider.credentials = nil
			case "send_failure":
				caller.failMethod = "editMessageText"
			case "disabled":
				b.telegramProxy = nil
			}
			b.handleTelegramProxyCallback(b.bot, proxyUpdate(CallbackTelegramProxy))
			if len(recorder.calls) != 0 || strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "ee001122") {
				t.Fatal("failed delivery recorded success or leaked secrets")
			}
			if mode == "send_failure" {
				return
			}
			final := caller.calls[len(caller.calls)-1]
			text := callText(t, final)
			if strings.Contains(text, "private-") || strings.Contains(text, "ee001122") || strings.Contains(string(final.body["reply_markup"]), `"url"`) {
				t.Fatal("failed/uncertain provision must expose no credential")
			}
			if !reflect.DeepEqual(callbacks(callKeyboard(t, final)), []string{CallbackTelegramProxy, CallbackHelpBack}) {
				t.Fatal("no issued route keeps the existing failure view")
			}
			wantAdmin := mode == "revoked" || mode == "ineligible"
			if strings.Contains(text, "Напишите администратору") != wantAdmin {
				t.Fatal("access errors and temporary outages need their existing explanations")
			}
		})
	}
}
