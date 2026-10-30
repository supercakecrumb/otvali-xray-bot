package telegram

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type middlewareUserStore struct {
	bound, invited       *database.User
	boundErr, invitedErr error
	bindErr              error
	boundID              int64
}

func (s *middlewareUserStore) GetUserByTelegramID(int64) (*database.User, error) {
	return s.bound, s.boundErr
}
func (s *middlewareUserStore) GetUserByUsername(string) (*database.User, error) {
	return s.invited, s.invitedErr
}
func (*middlewareUserStore) UpdateUserUsername(int64, string) error { return nil }
func (s *middlewareUserStore) UpdateUserTelegramID(_, id int64) error {
	s.boundID = id
	return s.bindErr
}

func TestDatabaseMiddlewareAcknowledgesRejectedCallbacksExactlyOnce(t *testing.T) {
	for _, mode := range []string{"bound_lookup_failure", "username_lookup_failure", "binding_conflict", "binding_failure", "uninvited", "accepted_bound", "accepted_invited"} {
		t.Run(mode, func(t *testing.T) {
			b, caller, logs := stockholmTestBot(t, nil)
			id := int64(42)
			store := &middlewareUserStore{boundErr: database.ErrUserNotFound, invitedErr: database.ErrUserNotFound}
			wantNext := false
			switch mode {
			case "bound_lookup_failure":
				store.boundErr = errors.New("private-database-details")
			case "username_lookup_failure":
				store.invitedErr = errors.New("private-database-details")
			case "binding_conflict":
				other := int64(99)
				store.invited, store.invitedErr = &database.User{TelegramID: &other}, nil
			case "binding_failure":
				store.invited, store.invitedErr, store.bindErr = &database.User{ID: 7}, nil, errors.New("private-database-details")
			case "accepted_bound":
				store.bound, store.boundErr = &database.User{TelegramID: &id, Username: "renamed_actor"}, nil
				wantNext = true
			case "accepted_invited":
				store.invited, store.invitedErr = &database.User{ID: 7}, nil
				wantNext = true
			}
			next := false
			b.userDatabaseMiddlewareWithStore(store)(b.bot, stockholmUpdate(), func(bot *telego.Bot, update telego.Update) {
				next = true
				b.acknowledgePrivateCallback(bot, update)
			})
			if next != wantNext || len(caller.calls) != 1 || caller.calls[0].method != "answerCallbackQuery" {
				t.Fatal("rejected callbacks must stop and answer; accepted callbacks must leave the answer to the handler")
			}
			if mode == "accepted_invited" && store.boundID != 42 {
				t.Fatal("existing invite binding behavior changed")
			}
			if strings.Contains(logs.String(), "private-database-details") || strings.Contains(string(caller.calls[0].body["text"]), "private-database-details") {
				t.Fatal("database details escaped the failure boundary")
			}
		})
	}
}

func TestStartOpensMainMenuWithoutIssuingCredentials(t *testing.T) {
	issuer := &fakeStockholmIssuer{eligible: true}
	b, caller, logs := stockholmTestBot(t, issuer)
	// DryRun avoids all SQL/network activity while preserving the existing empty
	// admin-notification lookup used by /start.
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	b.db = &database.DB{Conn: db}
	update := telego.Update{Message: &telego.Message{Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42}, Text: "/start"}}
	b.handleStart(b.bot, update)
	if len(caller.calls) != 1 || caller.calls[0].method != "sendMessage" || callText(t, caller.calls[0]) != helpMessage || !reflect.DeepEqual(callbacks(callKeyboard(t, caller.calls[0])), callbacks(helpKeyboard)) {
		t.Fatal("/start must immediately show the same short main menu as /help")
	}
	if len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
		t.Fatal("opening the main menu must not contact the issuer")
	}
	if strings.Count(logs.String(), "Command executed") != 1 {
		t.Fatal("/start must retain exactly one command-usage event")
	}
	caller.calls = nil
	update.Message.Chat.Type = "group"
	b.handleStart(b.bot, update)
	if len(caller.calls) != 0 {
		t.Fatal("main menu must stay in the actor's private chat")
	}
}

func TestLegacyKeyLoadingAndFinalDeliveryAreSequential(t *testing.T) {
	b, caller, _ := stockholmTestBot(t, nil)
	message, err := b.sendKeyLoadingMessage(42, 17)
	if err != nil || len(caller.calls) != 1 || caller.calls[0].method != "editMessageText" {
		t.Fatal("loading must finish one synchronous edit")
	}
	message.Text = legacyKeyCaption("Finland, Helsinki", "vless://test-only@fi.example:443")
	message.ParseMode, message.ReplyMarkup = telego.ModeMarkdownV2, backHomeKeyboard
	if _, err := b.bot.EditMessageText(message); err != nil {
		t.Fatal(err)
	}
	if len(caller.calls) != 2 || !strings.Contains(callText(t, caller.calls[1]), "vless://test-only@fi.example:443") || strings.Contains(callText(t, caller.calls[1]), "Генерирую") {
		t.Fatal("the final edit must preserve the full original key and replace loading")
	}
}

func TestLegacyKeyFailuresKeepTransportAndDatabaseDetailsPrivate(t *testing.T) {
	for _, mode := range []string{"loading_transport", "database", "ack_transport"} {
		t.Run(mode, func(t *testing.T) {
			b, caller, logs := stockholmTestBot(t, nil)
			db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: logger.Discard})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Query().Before("gorm:query").Register("polish:server_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == "servers" {
					tx.AddError(errors.New("private-database-request-content"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			b.db = &database.DB{Conn: db}
			wantCode := "server_lookup_failed"
			if mode == "loading_transport" {
				caller.failMethod, wantCode = "editMessageText", "loading_message_failed"
			} else if mode == "ack_transport" {
				caller.failMethod = "answerCallbackQuery"
			}
			b.generateKeyProcess(1, stockholmUpdate())
			if !strings.Contains(logs.String(), wantCode) || (mode == "ack_transport" && !strings.Contains(logs.String(), "callback_ack_failed")) {
				t.Fatal("administrator diagnostics must retain a stable safe reason code")
			}
			for _, sentinel := range []string{"sensitive-telegram-request-content", "private-database-request-content"} {
				if strings.Contains(logs.String(), sentinel) {
					t.Fatal("raw failure details leaked to diagnostics")
				}
				for _, call := range caller.calls {
					for _, value := range call.body {
						if strings.Contains(string(value), sentinel) {
							t.Fatal("raw failure details leaked to Telegram")
						}
					}
				}
			}
		})
	}
}

func TestManagedAccessFailuresOfferOnlyExplicitSafeRetries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		retryable bool
	}{
		{"inactive", remnawave.ErrInactive, false},
		{"conflict", remnawave.ErrConflict, false},
		{"not_eligible", remnawave.ErrNotEligible, false},
		{"not_found", remnawave.ErrNotFound, false},
		{"unavailable", remnawave.ErrUnavailable, true},
	} {
		for _, action := range []string{"subscription", "key_list", "selected_key"} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				providerErr := fmt.Errorf("private-bearer private-token raw-response: %w", tc.err)
				issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true, err: providerErr}, keysErr: providerErr}
				b, caller, logs := stockholmTestBot(t, issuer)
				update := stockholmUpdate()
				switch action {
				case "subscription":
					b.handleStockholmSubscription(b.bot, update)
				case "key_list":
					update.CallbackQuery.Data = CallbackIndividualKeys
					b.handleIndividualKeys(b.bot, update)
				case "selected_key":
					update.CallbackQuery.Data = CallbackManagedKeyPrefix + "0123456789abcdef"
					b.handleManagedKey(b.bot, update)
				}
				if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "editMessageText" || len(issuer.issueIDs)+len(issuer.keyIDs) != 1 {
					t.Fatal("each explicit action must acknowledge once and call only its own issuer once")
				}
				retry := false
				for _, row := range callKeyboard(t, caller.calls[1]).InlineKeyboard {
					for _, button := range row {
						if button.Text == "🔄 Повторить" {
							retry = true
							if button.CallbackData != update.CallbackQuery.Data {
								t.Fatal("retry must repeat exactly the user's explicit current action")
							}
						}
					}
				}
				if retry != tc.retryable || (!tc.retryable && strings.Contains(callText(t, caller.calls[1]), "позже")) {
					t.Fatal("disabled/missing access must not be presented as a transient retry")
				}
				for _, secret := range []string{"private-bearer", "private-token", "raw-response"} {
					if strings.Contains(logs.String(), secret) || strings.Contains(callText(t, caller.calls[1]), secret) {
						t.Fatal("provider details escaped the safe error presenter")
					}
				}
			})
		}
	}
}
