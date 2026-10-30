package telegram

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

func TestTelegramEntryIsPrivateStaticAndHasNoFakeConnection(t *testing.T) {
	for _, mode := range []string{"private", "group", "other_actor", "ack_error"} {
		t.Run(mode, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: true}
			b, caller, _ := stockholmTestBot(t, issuer)
			network := &fakeNetworkReader{}
			account := &fakeAccountReader{}
			WithNetworkStatus(network, 42)(b)
			WithAccountStatus(account)(b)
			update := stockholmUpdate()
			update.CallbackQuery.Data = CallbackHelpTelegram
			wantCalls := 2
			switch mode {
			case "group":
				update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
				wantCalls = 1
			case "other_actor":
				update.CallbackQuery.From.ID = 7
				wantCalls = 1
			case "ack_error":
				caller.failMethod = "answerCallbackQuery"
				wantCalls = 1
			}
			b.handleHelpCallback(b.bot, update)
			if len(caller.calls) != wantCalls || caller.calls[0].method != "answerCallbackQuery" || len(issuer.issueIDs)+len(issuer.eligibleIDs)+len(account.ids)+network.calls != 0 {
				t.Fatal("Telegram navigation must acknowledge once, enforce the private actor and avoid all backends")
			}
			if mode == "private" {
				text := callText(t, caller.calls[1])
				if !strings.Contains(text, "Прокси-ссылка ещё не настроена") || !strings.Contains(text, "включение подтвердите вы") {
					t.Fatal("Telegram must explain unavailable setup and the user's confirmation")
				}
				keyboard := callKeyboard(t, caller.calls[1])
				if !reflect.DeepEqual(callbacks(keyboard), []string{CallbackHelpBack}) {
					t.Fatal("the unconfigured proxy must have no active connection action")
				}
				for _, row := range keyboard.InlineKeyboard {
					for _, button := range row {
						if button.URL != "" || button.WebApp != nil {
							t.Fatal("the unconfigured proxy must not expose a fabricated link")
						}
					}
				}
			}
		})
	}
}

func TestSubscriptionFallbackKeepsIndividualKeysReachable(t *testing.T) {
	for _, mode := range []string{"ineligible", "unconfigured", "outage"} {
		t.Run(mode, func(t *testing.T) {
			issuer := &fakeStockholmIssuer{eligible: mode == "outage", err: remnawave.ErrUnavailable}
			b, caller, _ := stockholmTestBot(t, issuer)
			if mode == "unconfigured" {
				b.stockholm = nil
			}
			b.handleStockholmSubscription(b.bot, stockholmUpdate())
			if len(caller.calls) != 2 {
				t.Fatal("subscription failure must acknowledge and explain once")
			}
			got := callbacks(callKeyboard(t, caller.calls[1]))
			want := []string{CallbackIndividualKeys, CallbackHelpBack}
			if mode == "outage" {
				want = append([]string{CallbackStockholmSubscription}, want...)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("failure must retain individual keys without issuing them or showing a server list")
			}
			if mode != "outage" && len(issuer.issueIDs) != 0 {
				t.Fatal("unavailable access must not provision a subscription")
			}
		})
	}
}

func TestStartStatusPayloadIsPrivateAndReadOnly(t *testing.T) {
	for _, command := range []string{"/start status", "/start@rkn_otvali_bot status", "/start@otvali_ss_proxy_bot status"} {
		for _, mode := range []string{"private", "group", "other_actor"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				issuer := &fakeStockholmIssuer{eligible: true}
				b, caller, _ := stockholmTestBot(t, issuer)
				reader := &fakeNetworkReader{}
				WithNetworkStatus(reader, 42)(b)
				update := telego.Update{Message: &telego.Message{Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}, Text: command}}
				want := 1
				if mode == "group" {
					update.Message.Chat = telego.Chat{ID: -42, Type: "group"}
					want = 0
				}
				if mode == "other_actor" {
					update.Message.From.ID = 7
					want = 0
				}
				b.handleStart(b.bot, update)
				if reader.calls != want || len(caller.calls) != want || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
					t.Fatal("status deep links must only read availability in the actor's private chat")
				}
				if want == 1 && (caller.calls[0].method != "sendMessage" || !strings.Contains(callText(t, caller.calls[0]), "Состояние VPN")) {
					t.Fatal("the status deep link must open existing network status")
				}
			})
		}
	}
}

func TestQRPhotoCanReturnToMainMenu(t *testing.T) {
	issuer := &fakeStockholmIssuer{eligible: true}
	b, caller, _ := stockholmTestBot(t, issuer)
	update := stockholmUpdate()
	update.CallbackQuery.Data = CallbackHelpBack
	update.CallbackQuery.Message = &telego.Message{MessageID: 17, Chat: telego.Chat{ID: 42, Type: "private"}, Photo: []telego.PhotoSize{{FileID: "fictional-photo"}}}
	b.handleHelpCallback(b.bot, update)
	if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "sendMessage" || callText(t, caller.calls[1]) != helpMessage {
		t.Fatal("a photo must navigate to a new text screen instead of an invalid editMessageText")
	}
	if len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
		t.Fatal("returning from a photo must not retrieve credentials")
	}
	if !reflect.DeepEqual(callbacks(callKeyboard(t, caller.calls[1])), callbacks(helpKeyboard)) {
		t.Fatal("photo navigation must show the same four-action home")
	}
}

func TestSupportAndDevicePickersNeverReadPrivateServices(t *testing.T) {
	for _, callback := range []string{CallbackHelpSupport, CallbackHelpVPNDevices} {
		issuer := &fakeStockholmIssuer{eligible: true}
		b, caller, _ := stockholmTestBot(t, issuer)
		network, account := &fakeNetworkReader{}, &fakeAccountReader{}
		WithNetworkStatus(network, 42)(b)
		WithAccountStatus(account)(b)
		update := stockholmUpdate()
		update.CallbackQuery.Data = callback
		b.handleHelpCallback(b.bot, update)
		if len(caller.calls) != 2 || len(issuer.issueIDs)+len(issuer.eligibleIDs)+network.calls+len(account.ids) != 0 {
			t.Fatal("opening a picker must only acknowledge and render navigation")
		}
	}
}
