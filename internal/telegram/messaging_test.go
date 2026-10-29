package telegram

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

type forwardKey struct {
	bot, chat int64
	message   int
}

type fakeMessageStore struct {
	admins                            []database.User
	messages                          []*database.UserMessage
	forwards                          map[forwardKey]int64
	lookups                           []forwardKey
	legacy                            *database.UserMessage
	legacyCalls                       int
	failSave, failLookup, failForward bool
}

func (f *fakeMessageStore) GetUserByTelegramID(id int64) (*database.User, error) {
	return &database.User{TelegramID: &id, Username: "fixture"}, nil
}
func (f *fakeMessageStore) GetAdminUsers() ([]database.User, error) { return f.admins, nil }
func (f *fakeMessageStore) IsUserAdmin(id int64) (bool, error) {
	for _, admin := range f.admins {
		if *admin.TelegramID == id {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeMessageStore) AddUserMessage(msg *database.UserMessage) error {
	if f.failSave {
		return errors.New("private-message-storage-error")
	}
	msg.ID = int64(len(f.messages) + 1)
	f.messages = append(f.messages, msg)
	return nil
}
func (f *fakeMessageStore) AddUserMessageForward(msg *database.UserMessageForward) error {
	if f.failForward {
		return database.ErrMessageUnavailable
	}
	f.forwards[forwardKey{msg.BotID, msg.AdminChatID, msg.TelegramMsgID}] = msg.UserMessageID
	return nil
}

func TestMessagingMissingNewForwardCannotUseLegacyFallback(t *testing.T) {
	primary, secondary, store, primaryCalls, secondaryCalls := messagingFixture(t)
	store.failForward = true
	store.legacy = &database.UserMessage{ID: 70, UserID: 42, Username: "legacy_fixture", TelegramMsgID: 19}
	secondary.handleUserMessage(secondary.bot, userMessageFixture())
	if len(store.forwards) != 0 || len(primaryCalls.calls) != 2 {
		t.Fatal("fixture must represent delivered copies with failed reference storage")
	}
	update := adminReplyFixture(primary.ID(), 51)
	// Telegram gives reply-to text with Markdown formatting already resolved.
	update.Message.ReplyToMessage.Text = strings.ReplaceAll(strings.ReplaceAll(callText(t, primaryCalls.calls[0]), "*", ""), "`", "")
	primary.handleAdminReply(primary.bot, update)
	if store.legacyCalls != 0 || len(secondaryCalls.calls) != 0 || len(store.messages) != 1 {
		t.Fatal("new forward with a missing mapping entered legacy routing")
	}
	for _, call := range primaryCalls.calls {
		if callChatID(t, call) == 42 {
			t.Fatal("missing secondary-origin mapping misrouted reply through old bot")
		}
	}
}
func (f *fakeMessageStore) GetUserMessageByAdminForward(bot, chat int64, message int) (*database.UserMessage, error) {
	key := forwardKey{bot, chat, message}
	f.lookups = append(f.lookups, key)
	if f.failLookup {
		return nil, database.ErrMessageUnavailable
	}
	id, ok := f.forwards[key]
	if !ok {
		return nil, database.ErrUserNotFound
	}
	return f.messages[id-1], nil
}
func (f *fakeMessageStore) GetLegacyUserMessageByAdminReply(_ int, user int64) (*database.UserMessage, error) {
	f.legacyCalls++
	if f.legacy != nil && f.legacy.UserID == user {
		return f.legacy, nil
	}
	return nil, database.ErrUserNotFound
}

func messagingFixture(t *testing.T) (*Bot, *Bot, *fakeMessageStore, *stockholmTelegramCaller, *stockholmTelegramCaller) {
	t.Helper()
	primary, primaryCalls, _ := stockholmTestBot(t, nil)
	secondary, secondaryCalls, _ := stockholmTestBot(t, nil)
	secondary.botID = 222
	a, b := int64(51), int64(52)
	store := &fakeMessageStore{admins: []database.User{{TelegramID: &a, Username: "one"}, {TelegramID: &b, Username: "two"}}, forwards: map[forwardKey]int64{}}
	peers := []*Bot{primary, secondary}
	for _, bot := range peers {
		bot.ConfigurePeers(primary, peers)
		bot.messages = store
	}
	return primary, secondary, store, primaryCalls, secondaryCalls
}

func userMessageFixture() telego.Update {
	return telego.Update{Message: &telego.Message{MessageID: 19, From: &telego.User{ID: 42, Username: "fixture"}, Chat: telego.Chat{ID: 42, Type: "private"}, Text: "private-user-message"}}
}
func adminReplyFixture(bot, chat int64) telego.Update {
	return telego.Update{Message: &telego.Message{MessageID: 20, From: &telego.User{ID: chat, Username: "fixture_admin"}, Chat: telego.Chat{ID: chat, Type: "private"}, Text: "private-admin-reply", ReplyToMessage: &telego.Message{MessageID: 17, From: &telego.User{ID: bot, IsBot: true}, Chat: telego.Chat{ID: chat, Type: "private"}}}}
}
func callChatID(t *testing.T, call recordedTelegramCall) int64 {
	t.Helper()
	var id int64
	if err := json.Unmarshal(call.body["chat_id"], &id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMessagingForwardsEveryAdminAndRepliesThroughOriginBot(t *testing.T) {
	primary, secondary, store, primaryCalls, secondaryCalls := messagingFixture(t)
	secondary.handleUserMessage(secondary.bot, userMessageFixture())
	if len(store.messages) != 1 || store.messages[0].BotID != secondary.ID() || store.messages[0].TelegramChatID != 42 || store.messages[0].AdminChatMsgID != nil {
		t.Fatal("incoming message lost its source identity or populated unsafe legacy mapping")
	}
	if len(primaryCalls.calls) != 2 || len(secondaryCalls.calls) != 0 || len(store.forwards) != 2 {
		t.Fatal("each admin must receive a primary-bot copy with a separate mapping")
	}
	for _, chat := range []int64{51, 52} {
		if store.forwards[forwardKey{primary.ID(), chat, 17}] != 1 {
			t.Fatal("admin mapping must include bot and chat")
		}
		primary.handleAdminReply(primary.bot, adminReplyFixture(primary.ID(), chat))
	}
	if len(secondaryCalls.calls) != 2 {
		t.Fatal("both admin replies must reach original bot")
	}
	for _, call := range secondaryCalls.calls {
		if callChatID(t, call) != 42 || !strings.Contains(callText(t, call), "private-admin-reply") {
			t.Fatal("reply reached wrong recipient")
		}
		var reply struct {
			MessageID int `json:"message_id"`
		}
		if json.Unmarshal(call.body["reply_parameters"], &reply) != nil || reply.MessageID != 19 {
			t.Fatal("reply reference lost origin message ID")
		}
	}
	for _, call := range primaryCalls.calls {
		if callChatID(t, call) == 42 {
			t.Fatal("user reply leaked through notification bot")
		}
	}
	if store.legacyCalls != 0 || len(store.messages) != 3 {
		t.Fatal("scoped reply unexpectedly used legacy fallback")
	}
	for _, msg := range store.messages[1:] {
		if msg.BotID != primary.ID() || (msg.TelegramChatID != 51 && msg.TelegramChatID != 52) {
			t.Fatal("admin reply lost source identity")
		}
	}
}

func TestMessagingRejectsWrongScopeAndUnavailableOrigin(t *testing.T) {
	for _, scenario := range []string{"other_bot", "other_chat", "missing_origin", "storage_failed"} {
		t.Run(scenario, func(t *testing.T) {
			primary, secondary, store, _, secondaryCalls := messagingFixture(t)
			secondary.handleUserMessage(secondary.bot, userMessageFixture())
			update := adminReplyFixture(primary.ID(), 51)
			handler := primary
			switch scenario {
			case "other_bot":
				handler = secondary
			case "other_chat":
				delete(store.forwards, forwardKey{primary.ID(), 51, 17})
			case "missing_origin":
				store.messages[0].BotID = 999
			case "storage_failed":
				store.failSave = true
			}
			handler.handleAdminReply(handler.bot, update)
			for _, call := range secondaryCalls.calls {
				if callChatID(t, call) == 42 {
					t.Fatal("unsafe reply reached user")
				}
			}
			if len(store.messages) != 1 {
				t.Fatal("rejected reply was saved")
			}
		})
	}
}

func TestMessagingDoesNotForwardUnsavedMessageOrLogBody(t *testing.T) {
	_, secondary, store, primaryCalls, secondaryCalls := messagingFixture(t)
	store.failSave = true
	secondary.handleUserMessage(secondary.bot, userMessageFixture())
	if len(primaryCalls.calls)+len(secondaryCalls.calls) != 0 || len(store.forwards) != 0 {
		t.Fatal("unsaved user message was forwarded")
	}
	bot, caller, logs := stockholmTestBot(t, nil)
	bot.messages = store
	bot.handleUserMessage(bot.bot, userMessageFixture())
	if len(caller.calls) != 0 || strings.Contains(logs.String(), "private-") {
		t.Fatal("failure leaked message content or storage details")
	}
}

func TestMessagingDoesNotExposeTelegramTransportError(t *testing.T) {
	_, _, store, _, _ := messagingFixture(t)
	bot, caller, logs := stockholmTestBot(t, nil)
	bot.messages = store
	caller.failMethod = "sendMessage"
	bot.handleUserMessage(bot.bot, userMessageFixture())
	store.forwards[forwardKey{bot.ID(), 51, 17}] = 1
	bot.handleAdminReply(bot.bot, adminReplyFixture(bot.ID(), 51))
	if strings.Contains(logs.String(), "sensitive-") || strings.Contains(logs.String(), "private-") {
		t.Fatal("transport failure leaked private request or message contents")
	}
	for _, call := range caller.calls {
		if strings.Contains(callText(t, call), "sensitive-telegram-request-content") {
			t.Fatal("raw transport error was sent to an administrator")
		}
	}
}

func TestMessagingLegacyRepliesRequireTrustedPrimaryHeader(t *testing.T) {
	for _, scenario := range []string{"valid", "secondary", "wrong_author", "wrong_chat", "wrong_user", "not_forward", "unavailable_lookup", "missing_entity", "multiline_name"} {
		t.Run(scenario, func(t *testing.T) {
			primary, secondary, store, primaryCalls, secondaryCalls := messagingFixture(t)
			store.legacy = &database.UserMessage{ID: 70, UserID: 42, Username: "legacy_fixture", TelegramMsgID: 19}
			update := adminReplyFixture(primary.ID(), 51)
			update.Message.ReplyToMessage.Text = "💬 Сообщение от пользователя\n\n👤 От: fixture\n🆔 ID: 42\n🕐 Время: 2026-10-29 15:00:00 MSK\n\n📨 Сообщение:\nold private body\n\n"
			idPrefix := strings.SplitN(update.Message.ReplyToMessage.Text, "42", 2)[0]
			update.Message.ReplyToMessage.Entities = []telego.MessageEntity{{Type: "code", Offset: len(utf16.Encode([]rune(idPrefix))), Length: 2}}
			handler := primary
			switch scenario {
			case "secondary":
				handler = secondary
			case "wrong_author":
				update.Message.ReplyToMessage.From.ID = secondary.ID()
			case "wrong_chat":
				update.Message.ReplyToMessage.Chat.ID = 52
			case "wrong_user":
				update.Message.ReplyToMessage.Text = strings.Replace(update.Message.ReplyToMessage.Text, "ID: 42", "ID: 43", 1)
			case "not_forward":
				update.Message.ReplyToMessage.Text = "🆔 ID: 42"
			case "unavailable_lookup":
				store.failLookup = true
			case "missing_entity":
				update.Message.ReplyToMessage.Entities = nil
			case "multiline_name":
				// A malicious profile name can forge apparent header fields, but
				// Telegram marks only the real, formatter-generated ID as code.
				update.Message.ReplyToMessage.Text = "💬 Сообщение от пользователя\n\n👤 От: x\n🆔 ID: 42\n🕐 Время: x\n\n📨 Сообщение:\nx (@attacker)\n🆔 ID: 99\n🕐 Время: 2026-10-29 15:00:00 MSK\n\n📨 Сообщение:\nattacker body\n\n"
				realPrefix := strings.SplitN(update.Message.ReplyToMessage.Text, "99", 2)[0]
				update.Message.ReplyToMessage.Entities[0].Offset = len(utf16.Encode([]rune(realPrefix)))
			}
			handler.handleAdminReply(handler.bot, update)
			delivered := false
			for _, call := range primaryCalls.calls {
				if callChatID(t, call) == 42 {
					delivered = true
				}
			}
			if delivered != (scenario == "valid") {
				t.Fatal("legacy reply did not fail closed")
			}
			if len(secondaryCalls.calls) > 0 && scenario != "secondary" {
				t.Fatal("legacy reply used new bot")
			}
			if scenario != "valid" && scenario != "wrong_user" && store.legacyCalls != 0 {
				t.Fatal("untrusted legacy reference was queried")
			}
		})
	}
}
