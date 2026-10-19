package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type fakeIndividualKeyIssuer struct {
	fakeStockholmIssuer
	keys        []remnawave.ServerKey
	keyIDs      []int64
	keysErr     error
	keyDeadline bool
}

func (f *fakeIndividualKeyIssuer) Keys(ctx context.Context, id int64) ([]remnawave.ServerKey, error) {
	f.keyIDs = append(f.keyIDs, id)
	deadline, ok := ctx.Deadline()
	f.keyDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 25*time.Second
	return f.keys, f.keysErr
}

func managedTestKeys() []remnawave.ServerKey {
	return []remnawave.ServerKey{
		{ID: "0123456789abcdef", Name: "Sweden, Stockholm", URL: "vless://00000000-0000-4000-8000-000000000001@sto.example:443?security=reality&flow=xtls-rprx-vision#Sweden"},
		{ID: "123456789abcdef0", Name: "Moscow → Stockholm", URL: "vless://00000000-0000-4000-8000-000000000001@msk.example:8443?security=reality#Moscow%20Stockholm"},
		{ID: "23456789abcdef01", Name: "Moscow → Finland", URL: "vless://00000000-0000-4000-8000-000000000001@msk.example:9443?security=reality#Moscow%20Finland"},
	}
}

func TestIndividualMenuPreservesLegacyFinlandAndUsesOpaqueIDs(t *testing.T) {
	b := testBotWithDisabledServer(t)
	keys := managedTestKeys()
	keys = append(keys, keys[0], remnawave.ServerKey{ID: "vless://secret", Name: "unsafe", URL: keys[0].URL})
	keyboard := b.individualKeysKeyboard([]database.Server{{ID: 7, Country: "Estonia", City: "Narva"}, {ID: 1, Country: "Finland", City: "Helsinki"}}, keys)
	got := callbacks(keyboard)
	want := []string{"getkey_1", CallbackManagedKeyPrefix + "0123456789abcdef", CallbackManagedKeyPrefix + "123456789abcdef0", CallbackManagedKeyPrefix + "23456789abcdef01", CallbackGetKey}
	if len(got) != len(want) {
		t.Fatal("one legacy key, three managed keys and back expected")
	}
	for i, cb := range got {
		if cb != want[i] || strings.Contains(cb, "vless") || strings.Contains(cb, "example") {
			t.Fatal("key callbacks must preserve legacy ID or use only opaque current ID")
		}
	}
}

func TestIndividualKeysUseSameActorAndOnlyKeysInterface(t *testing.T) {
	issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true}, keys: managedTestKeys()}
	b, caller, logs := stockholmTestBot(t, issuer)
	update := stockholmUpdate()
	update.CallbackQuery.Data = CallbackIndividualKeys
	b.handleIndividualKeys(b.bot, update)
	if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || len(issuer.keyIDs) != 1 || issuer.keyIDs[0] != 42 || !issuer.keyDeadline || len(issuer.issueIDs) != 0 {
		t.Fatal("listing must use same actor Keys API with no separate Issue or legacy writes")
	}
	if strings.Contains(callText(t, caller.calls[1]), "vless://") && strings.Contains(callText(t, caller.calls[1]), "00000000") {
		t.Fatal("list must not print key credentials")
	}
	update.CallbackQuery.Data = CallbackManagedKeyPrefix + issuer.keys[1].ID
	b.handleManagedKey(b.bot, update)
	if len(issuer.keyIDs) != 2 || issuer.keyIDs[1] != 42 || len(issuer.issueIDs) != 0 {
		t.Fatal("selection must reread same actor's current profiles")
	}
	text := callText(t, caller.calls[3])
	if !strings.Contains(text, "msk.example:8443") || strings.Contains(text, "msk.example:9443") || !strings.Contains(text, "/help_setup") || !strings.Contains(text, "как сервер") {
		t.Fatal("must deliver only selected current key plus concise setup")
	}
	if strings.Contains(logs.String(), "00000000") || strings.Contains(logs.String(), "msk.example") {
		t.Fatal("key leaked to logs")
	}
}

func TestManagedKeyRejectsStaleForgedAndUnauthorizedRequests(t *testing.T) {
	for _, name := range []string{"stale", "malformed", "unapproved", "group", "other_actor", "missing_message", "no_issuer", "ack_error", "backend_error", "bad_url"} {
		t.Run(name, func(t *testing.T) {
			issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true}, keys: managedTestKeys()}
			b, caller, logs := stockholmTestBot(t, issuer)
			update := stockholmUpdate()
			update.CallbackQuery.Data = CallbackManagedKeyPrefix + issuer.keys[0].ID
			expectedAPI := 1
			switch name {
			case "stale":
				update.CallbackQuery.Data = CallbackManagedKeyPrefix + "ffffffffffffffff"
			case "malformed":
				update.CallbackQuery.Data = CallbackManagedKeyPrefix + "vless://private"
				expectedAPI = 0
			case "unapproved":
				issuer.eligible = false
				expectedAPI = 0
			case "group":
				update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
				expectedAPI = 0
			case "other_actor":
				update.CallbackQuery.From.ID = 7
				expectedAPI = 0
			case "missing_message":
				update.CallbackQuery.Message = nil
				expectedAPI = 0
			case "no_issuer":
				b.stockholm = nil
				expectedAPI = 0
			case "ack_error":
				caller.failMethod = "answerCallbackQuery"
				expectedAPI = 0
			case "backend_error":
				issuer.keysErr = errors.New("private-api-token private-bearer raw-response")
			case "bad_url":
				issuer.keys[0].URL = "javascript:private-bearer"
			}
			b.handleManagedKey(b.bot, update)
			if len(caller.calls) == 0 || caller.calls[0].method != "answerCallbackQuery" || len(issuer.keyIDs) != expectedAPI || len(issuer.issueIDs) != 0 {
				t.Fatal("invalid request reached wrong API or skipped acknowledgement")
			}
			for _, call := range caller.calls {
				for _, value := range call.body {
					for _, secret := range []string{"private-api-token", "private-bearer", "raw-response", "00000000"} {
						if strings.Contains(string(value), secret) {
							t.Fatal("failure exposed credential")
						}
					}
				}
			}
			if strings.Contains(logs.String(), "private") && strings.Contains(logs.String(), "bearer") {
				t.Fatal("failure logged raw error")
			}
		})
	}
}

func TestIndividualListingRejectsForgedBeforeAnyBackend(t *testing.T) {
	issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true}, keys: managedTestKeys()}
	b, caller, _ := stockholmTestBot(t, issuer)
	update := stockholmUpdate()
	update.CallbackQuery.Data = CallbackIndividualKeys
	update.CallbackQuery.From.ID = 7
	b.handleIndividualKeys(b.bot, update)
	if len(caller.calls) != 1 || len(issuer.keyIDs)+len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
		t.Fatal("forged listing must not access any backend")
	}
}
