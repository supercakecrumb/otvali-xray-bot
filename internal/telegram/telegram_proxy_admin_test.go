package telegram

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

type proxyAdminStoreFake struct {
	allowed     map[int64]bool
	user        *database.User
	err         error
	lookups     []int64
	grantStates map[string]string
}

func (f *proxyAdminStoreFake) IsUserAdmin(actor int64) (bool, error) { return f.allowed[actor], f.err }
func (f *proxyAdminStoreFake) GetUserByID(id int64) (*database.User, error) {
	f.lookups = append(f.lookups, id)
	return f.user, f.err
}
func (f *proxyAdminStoreFake) GetProxyGrant(_ context.Context, actor int64, id string) (telegramproxy.Grant, bool, error) {
	state, found := f.grantStates[id]
	return telegramproxy.Grant{TelegramID: actor, EndpointID: id, State: state}, found, f.err
}

type proxyRevokerFake struct {
	*fakeTelegramProxy
	revocations []struct {
		actor int64
		route string
	}
	errors  map[string]error
	bounded bool
}

func (f *proxyRevokerFake) Revoke(ctx context.Context, actor int64, route string) error {
	f.revocations = append(f.revocations, struct {
		actor int64
		route string
	}{actor, route})
	deadline, ok := ctx.Deadline()
	f.bounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 8*time.Second
	return f.errors[route]
}

func proxyAdminFixture(t *testing.T) (*Bot, *stockholmTelegramCaller, *proxyAdminStoreFake, *proxyRevokerFake) {
	t.Helper()
	b, caller, _ := stockholmTestBot(t, nil)
	target := int64(4242)
	store := &proxyAdminStoreFake{allowed: map[int64]bool{42: true}, user: &database.User{ID: 7, Username: "target<escaped>", TelegramID: &target}, grantStates: map[string]string{}}
	provider := &proxyRevokerFake{fakeTelegramProxy: proxyFixture(), errors: map[string]error{}}
	b.proxyAdminStore = store
	b.telegramProxy = provider
	return b, caller, store, provider
}

func proxyRevokeCommand(raw string) telego.Update {
	return telego.Update{Message: &telego.Message{MessageID: 17, From: &telego.User{ID: 42}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: raw}}
}

func proxyRevokeConfirmation(t *testing.T, b *Bot, caller *stockholmTelegramCaller, command string) string {
	t.Helper()
	b.handleRevokeTelegram(b.bot, proxyRevokeCommand(command))
	keyboard := callKeyboard(t, caller.calls[len(caller.calls)-1])
	return keyboard.InlineKeyboard[0][0].CallbackData
}

func TestRevokeTelegramRequiresBoundAdminPrivateChatAndExplicitConfirmation(t *testing.T) {
	for _, botID := range []int64{123456789, 222222222} {
		b, caller, store, provider := proxyAdminFixture(t)
		b.botID = botID
		data := proxyRevokeConfirmation(t, b, caller, "/revoke_telegram 7 all")
		text := callText(t, caller.calls[0])
		if len(provider.revocations) != 0 || len(provider.issueIDs)+len(provider.listIDs) != 0 || !strings.Contains(text, "@target&lt;escaped&gt;") || !strings.Contains(text, "4242") || !strings.Contains(text, "5 минут") || len(data) > 64 {
			t.Fatal("preview must show escaped exact account and require explicit confirmation")
		}
		for _, route := range []string{"finland", "stockholm", "msk-finland", "msk-stockholm"} {
			if !strings.Contains(text, "<code>"+route+"</code>") {
				t.Fatal("missing route in confirmation")
			}
		}
		b.handleRevokeTelegramCallback(b.bot, proxyUpdate(data))
		var routes []string
		for _, revoke := range provider.revocations {
			if revoke.actor != 4242 {
				t.Fatal("revocation used admin identity instead of reviewed target")
			}
			routes = append(routes, revoke.route)
		}
		if !reflect.DeepEqual(routes, []string{"finland", "stockholm", "msk-finland", "msk-stockholm"}) || !provider.bounded || !reflect.DeepEqual(store.lookups, []int64{7, 7}) || len(provider.issueIDs)+len(provider.listIDs) != 0 {
			t.Fatal("confirmation must check target again and only revoke exact selected routes once")
		}
		b.handleRevokeTelegramCallback(b.bot, proxyUpdate(data))
		if len(provider.revocations) != 4 {
			t.Fatal("confirmation replay retried provider side effect")
		}
	}
}

func TestRevokeTelegramDeniesNonAdminAndMalformedCommandsBeforeLookup(t *testing.T) {
	for _, mode := range []string{"nonadmin", "admin_lookup_error", "group", "other_actor", "no_message", "bad_user", "negative_user", "bad_route", "extra_argument"} {
		t.Run(mode, func(t *testing.T) {
			b, caller, store, provider := proxyAdminFixture(t)
			update := proxyRevokeCommand("/revoke_telegram 7 all")
			switch mode {
			case "nonadmin":
				store.allowed[42] = false
			case "admin_lookup_error":
				store.err = errors.New("private-db-error")
			case "group":
				update.Message.Chat.Type = "group"
			case "other_actor":
				update.Message.From.ID = 99
			case "no_message":
				update.Message = nil
			case "bad_user":
				update.Message.Text = "/revoke_telegram @target all"
			case "negative_user":
				update.Message.Text = "/revoke_telegram -7 all"
			case "bad_route":
				update.Message.Text = "/revoke_telegram 7 arbitrary-route"
			case "extra_argument":
				update.Message.Text = "/revoke_telegram 7 all extra"
			}
			b.handleRevokeTelegram(b.bot, update)
			if len(store.lookups)+len(provider.revocations)+len(b.proxyRevokePreviews) != 0 {
				t.Fatal("unauthorized/invalid command reached target or side effect")
			}
			for _, call := range caller.calls {
				if strings.Contains(string(call.body["text"]), "private-db-error") {
					t.Fatal("raw error leaked")
				}
			}
		})
	}
}

func TestRevokeTelegramConfirmationCannotBeForgedTransferredOrRepeated(t *testing.T) {
	for _, mode := range []string{"nonadmin", "other_admin", "other_bot", "group", "forged", "expired", "target_changed", "target_deleted", "ack_failed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			b, caller, store, provider := proxyAdminFixture(t)
			data := proxyRevokeConfirmation(t, b, caller, "/revoke_telegram 7 msk-finland")
			update := proxyUpdate(data)
			switch mode {
			case "nonadmin":
				store.allowed[42] = false
			case "other_admin":
				store.allowed[99] = true
				update.CallbackQuery.From.ID = 99
				update.CallbackQuery.Message = &telego.Message{MessageID: 17, Chat: telego.Chat{ID: 99, Type: "private"}}
			case "other_bot":
				peer, _, _, _ := proxyAdminFixture(t)
				peer.telegramProxy = provider
				b = peer
			case "group":
				update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
			case "forged":
				update.CallbackQuery.Data = callbackRevokeTelegramPrefix + "yes:" + strings.Repeat("f", 32)
			case "expired":
				for token, preview := range b.proxyRevokePreviews {
					preview.createdAt = time.Now().Add(-6 * time.Minute)
					b.proxyRevokePreviews[token] = preview
				}
			case "target_changed":
				other := int64(5555)
				store.user.TelegramID = &other
			case "target_deleted":
				store.user = nil
			case "ack_failed":
				caller.failMethod = "answerCallbackQuery"
			case "cancel":
				update.CallbackQuery.Data = strings.Replace(data, ":yes:", ":no:", 1)
			}
			b.handleRevokeTelegramCallback(b.bot, update)
			if len(provider.revocations) != 0 {
				t.Fatal("invalid or cancelled confirmation changed provider")
			}
			if mode == "cancel" {
				b.handleRevokeTelegramCallback(b.bot, proxyUpdate(data))
				if len(provider.revocations) != 0 {
					t.Fatal("cancelled confirmation reused")
				}
			}
		})
	}
}

func TestRevokeTelegramPartialResultsRemainUnconfirmedAndDoNotRetry(t *testing.T) {
	b, caller, store, provider := proxyAdminFixture(t)
	provider.errors["stockholm"] = telegramproxy.ErrNotFound
	provider.errors["msk-finland"] = errors.New("private-provider-secret")
	provider.errors["msk-stockholm"] = telegramproxy.ErrConflict
	store.grantStates["msk-finland"] = telegramproxy.StateRevoking
	data := proxyRevokeConfirmation(t, b, caller, "/revoke_telegram 7 all")
	b.handleRevokeTelegramCallback(b.bot, proxyUpdate(data))
	text := callText(t, caller.calls[len(caller.calls)-1])
	for _, expected := range []string{"Финляндия: отключён", "Швеция: доступ не выдавался", "Москва → Финляндия: отзывается; подтверждения сервера нет", "Москва → Швеция: отключение не подтверждено", "автоматически не повторяются"} {
		if !strings.Contains(text, expected) {
			t.Fatal("partial outcome was hidden or misreported")
		}
	}
	if strings.Contains(text, "private-provider-secret") || strings.Contains(text, "secret=") || len(provider.revocations) != 4 {
		t.Fatal("provider error leaked or operation retried")
	}
	b.handleRevokeTelegramCallback(b.bot, proxyUpdate(data))
	if len(provider.revocations) != 4 {
		t.Fatal("unknown outcome confirmation replay retried revocation")
	}
	// A fresh explicit command may reconcile only the selected route.
	data = proxyRevokeConfirmation(t, b, caller, "/revoke_telegram 7 msk-finland")
	b.handleRevokeTelegramCallback(b.bot, proxyUpdate(data))
	if len(provider.revocations) != 5 || provider.revocations[4].route != "msk-finland" {
		t.Fatal("fresh reconciliation widened scope")
	}
}
