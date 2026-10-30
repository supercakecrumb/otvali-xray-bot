package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

const fakeTelegramProxyURL = "https://t.me/proxy?server=proxy.example&port=8443&secret=ee00112233445566778899aabbccddeeff6578616d706c652e636f6d"

type fakeTelegramProxy struct {
	listIDs, issueIDs []int64
	endpoints         []telegramproxy.Endpoint
	credential        telegramproxy.Credential
	err               error
	bounded           bool
}

func (f *fakeTelegramProxy) List(_ context.Context, actor int64) ([]telegramproxy.Endpoint, error) {
	f.listIDs = append(f.listIDs, actor)
	return f.endpoints, f.err
}
func (f *fakeTelegramProxy) Issue(ctx context.Context, actor int64, endpoint string) (telegramproxy.Credential, error) {
	f.issueIDs = append(f.issueIDs, actor)
	deadline, ok := ctx.Deadline()
	f.bounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 25*time.Second
	value := f.credential
	if value.URL == "" {
		value = telegramproxy.Credential{EndpointID: endpoint, Name: "Москва → Финляндия", URL: fakeTelegramProxyURL}
	}
	return value, f.err
}
func proxyFixture() *fakeTelegramProxy {
	return &fakeTelegramProxy{endpoints: []telegramproxy.Endpoint{
		{ID: "finland", Name: "Финляндия", Protocol: "mtproto", Primary: true},
		{ID: "stockholm", Name: "Швеция", Protocol: "mtproto"},
		{ID: "msk-finland", Name: "Москва → Финляндия", Protocol: "mtproto"},
		{ID: "msk-stockholm", Name: "Москва → Швеция", Protocol: "mtproto"},
	}}
}
func proxyUpdate(data string) telego.Update {
	update := stockholmUpdate()
	update.UpdateID = 123
	update.CallbackQuery.Data = data
	return update
}

func TestTelegramProxyNavigationListsFourRoutesWithoutIssuing(t *testing.T) {
	provider := proxyFixture()
	b, caller, _ := stockholmTestBot(t, nil)
	WithTelegramProxy(provider)(b)
	update := telego.Update{Message: &telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "/telegram"}}
	b.handleTelegramProxy(b.bot, update)
	if len(caller.calls) != 1 || caller.calls[0].method != "sendMessage" || len(provider.issueIDs) != 0 || len(provider.listIDs) != 1 || provider.listIDs[0] != 42 {
		t.Fatal("navigation must only read own metadata")
	}
	buttons := callbacks(callKeyboard(t, caller.calls[0]))
	for i, id := range []string{"finland", "stockholm", "msk-finland", "msk-stockholm"} {
		if len(buttons) != 5 || buttons[i] != CallbackTelegramProxyIssue+id {
			t.Fatal("all four direct/relay routes must be selectable")
		}
	}
	if !strings.Contains(callText(t, caller.calls[0]), "только внутри Telegram") {
		t.Fatal("scope must distinguish Telegram proxy from VPN")
	}
	b.handleTelegramProxyCallback(b.bot, proxyUpdate(CallbackTelegramProxy))
	if len(caller.calls) != 3 || caller.calls[1].method != "answerCallbackQuery" || len(provider.issueIDs) != 0 {
		t.Fatal("callback navigation must acknowledge without issuance")
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
			if len(caller.calls) != 1 || caller.calls[0].method != "answerCallbackQuery" || len(provider.listIDs)+len(provider.issueIDs) != 0 {
				t.Fatal("forged callback reached provider or disclosed a grant")
			}
		})
	}
	provider := proxyFixture()
	b, caller, _ := stockholmTestBot(t, nil)
	WithTelegramProxy(provider)(b)
	for _, update := range []telego.Update{{}, {Message: &telego.Message{From: &telego.User{ID: 42}, Chat: telego.Chat{ID: -42, Type: "group"}}}} {
		b.handleTelegramProxy(b.bot, update)
	}
	if len(provider.listIDs)+len(caller.calls) != 0 {
		t.Fatal("non-private command must not expose metadata")
	}
}

func TestTelegramProxyDeliveryUsesPersonalURLAndNeverClaimsConnection(t *testing.T) {
	for _, botID := range []int64{123456789, 222222222} {
		provider := proxyFixture()
		b, caller, logs := stockholmTestBot(t, nil)
		b.botID = botID
		WithTelegramProxy(provider)(b)
		recorder := &fakeActivityRecorder{}
		b.activity = recorder
		b.handleTelegramProxyCallback(b.bot, proxyUpdate(CallbackTelegramProxyIssue+"msk-finland"))
		if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || !provider.bounded || len(provider.issueIDs) != 1 || provider.issueIDs[0] != 42 {
			t.Fatal("explicit action must acknowledge and issue only actor grant")
		}
		text, keyboard := callText(t, caller.calls[1]), callKeyboard(t, caller.calls[1])
		if keyboard.InlineKeyboard[0][0].URL != fakeTelegramProxyURL || keyboard.InlineKeyboard[0][0].CallbackData != "" || !strings.Contains(text, "ещё не подтверждение соединения") || strings.Contains(text, "ee001122") {
			t.Fatal("credential belongs only in launch button, issued is not connected")
		}
		if strings.Contains(logs.String(), "ee001122") || len(recorder.calls) != 1 || recorder.calls[0].kind != "telegram_proxy_delivered" || !database.ValidActivity(recorder.calls[0].source, recorder.calls[0].kind, recorder.calls[0].id) {
			t.Fatal("delivery needs safe outcome counter without credentials")
		}
	}
}

func TestTelegramProxyFailuresNeverLeakOrRecordDelivery(t *testing.T) {
	for _, mode := range []string{"unavailable", "revoked", "invalid_url", "wrong_route", "send_failure", "disabled"} {
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
			case "invalid_url":
				provider.credential = telegramproxy.Credential{EndpointID: "finland", URL: "https://evil.example/proxy?secret=private-secret"}
			case "wrong_route":
				provider.credential = telegramproxy.Credential{EndpointID: "stockholm", URL: fakeTelegramProxyURL}
			case "send_failure":
				caller.failMethod = "editMessageText"
			case "disabled":
				b.telegramProxy = nil
			}
			b.handleTelegramProxyCallback(b.bot, proxyUpdate(CallbackTelegramProxyIssue+"finland"))
			if len(recorder.calls) != 0 || strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "ee001122") {
				t.Fatal("failed delivery recorded success or leaked secrets")
			}
			if mode != "send_failure" {
				text := callText(t, caller.calls[1])
				if strings.Contains(text, "private-") || strings.Contains(text, "ee001122") || strings.Contains(string(caller.calls[1].body["reply_markup"]), `"url"`) {
					t.Fatal("failed/uncertain provision must expose no credential")
				}
			}
		})
	}
}
