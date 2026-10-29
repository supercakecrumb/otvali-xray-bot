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
	row := b.mainMenuKeyboard().InlineKeyboard[0]
	if len(row) != 2 || row[0].WebApp.URL != "https://app.example/vpn" || row[1].WebApp.URL != "https://app.example/vpn#beta" {
		t.Fatal("app and beta must be separate read-only navigation actions")
	}
}
