package telegram

import (
	"strings"
	"testing"

	"github.com/mymmrac/telego"
)

func TestBetaEntryIsPrivateAndDoesNotIssueCredentials(t *testing.T) {
	issuer := &fakeStockholmIssuer{eligible: true}
	b, caller, _ := stockholmTestBot(t, issuer)
	option, err := WithMiniAppURL("https://app.example/vpn", []string{"https://app.example"})
	if err != nil {
		t.Fatal(err)
	}
	option(b)
	update := telego.Update{Message: &telego.Message{Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}}}
	b.handleBeta(b.bot, update)
	if len(caller.calls) != 1 || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
		t.Fatal("beta navigation must not issue or inspect credentials")
	}
	button := callKeyboard(t, caller.calls[0]).InlineKeyboard[0][0]
	if button.WebApp == nil || button.WebApp.URL != "https://app.example/vpn#beta" || button.URL != "" {
		t.Fatal("beta must open signed Telegram Mini App at the approved origin")
	}
	if !strings.Contains(callText(t, caller.calls[0]), "только вы и администраторы") {
		t.Fatal("privacy scope missing")
	}
	caller.calls = nil
	update.Message.Chat.Type = "group"
	b.handleBeta(b.bot, update)
	update.Message.Chat.Type = "private"
	update.Message.From.ID = 7
	b.handleBeta(b.bot, update)
	if len(caller.calls) != 0 {
		t.Fatal("beta must not send into groups or another actor's chat")
	}
	clear, _ := WithMiniAppURL("", nil)
	clear(b)
	update.Message.From.ID = 42
	b.handleBeta(b.bot, update)
	if len(caller.calls) != 1 || !strings.Contains(callText(t, caller.calls[0]), "Форма сейчас недоступна") {
		t.Fatal("missing Mini App must keep the chat fallback")
	}
}

func TestBetaMenuDoesNotChangeCredentialActions(t *testing.T) {
	b, _, _ := stockholmTestBot(t, nil)
	option, _ := WithMiniAppURL("https://app.example/vpn", []string{"https://app.example"})
	option(b)
	keyboard := b.mainMenuKeyboard()
	if keyboard.InlineKeyboard[0][0].CallbackData != CallbackStockholmSubscription {
		t.Fatal("feedback navigation must preserve the direct primary subscription action")
	}
	support := b.supportKeyboard()
	app := support.InlineKeyboard[1][1]
	feedback := support.InlineKeyboard[1][0]
	if app.WebApp == nil || app.WebApp.URL != "https://app.example/vpn" || feedback.WebApp == nil || feedback.WebApp.URL != "https://app.example/vpn#beta" {
		t.Fatal("app and feedback must be separate read-only navigation actions")
	}

}

func TestStartBetaPayloadOpensBetaWithoutIssuing(t *testing.T) {
	for _, command := range []string{"/start beta", "/start@rkn_otvali_bot beta"} {
		issuer := &fakeStockholmIssuer{eligible: true}
		b, caller, _ := stockholmTestBot(t, issuer)
		option, _ := WithMiniAppURL("https://app.example/vpn", []string{"https://app.example"})
		option(b)
		b.handleStart(b.bot, telego.Update{Message: &telego.Message{Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}, Text: command}})
		if len(caller.calls) != 1 || callText(t, caller.calls[0]) != betaMessage || len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
			t.Fatal("the beta deep link must open the beta entry without issuing credentials")
		}
		if callKeyboard(t, caller.calls[0]).InlineKeyboard[0][0].WebApp.URL != "https://app.example/vpn#beta" {
			t.Fatal("the beta payload must retain signed Mini App launch navigation")
		}
	}
}
