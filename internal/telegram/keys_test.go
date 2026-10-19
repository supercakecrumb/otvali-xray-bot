package telegram

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/serverpolicy"
	"github.com/supercakecrumb/otvali-xray-bot/internal/x3ui"
)

type recordedTelegramCall struct {
	method string
	body   map[string]json.RawMessage
}

type recordingTelegramCaller struct {
	t     *testing.T
	calls []recordedTelegramCall
}

func (r *recordingTelegramCaller) Call(url string, data *ta.RequestData) (*ta.Response, error) {
	r.t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data.Buffer.Bytes(), &body); err != nil {
		r.t.Fatal(err)
	}
	r.calls = append(r.calls, recordedTelegramCall{method: url[strings.LastIndex(url, "/")+1:], body: body})
	return &ta.Response{Ok: true, Result: []byte(`true`)}, nil
}

func testBotWithDisabledServer(t *testing.T) *Bot {
	t.Helper()
	policy, _ := serverpolicy.ParseDisabledServerIDs("7")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sh := x3ui.NewServerHandler("unused-missing-key", nil, logger, policy)
	t.Cleanup(sh.Close)
	return &Bot{sh: sh, logger: logger}
}

func TestServerMenuHidesDisabledServerAndPreservesExistingCallbacks(t *testing.T) {
	b := testBotWithDisabledServer(t)
	buttons := b.buildServerButtons([]database.Server{
		{ID: 7, Country: "Estonia", City: "Narva"},
		{ID: 8, Country: "Finland", City: "Helsinki"},
	})
	if len(buttons) != 2 || len(buttons[0]) != 1 || buttons[0][0].CallbackData != "getkey_8" {
		t.Fatalf("expected existing Finnish callback and back button, got %+v", buttons)
	}
	if !strings.Contains(buttons[0][0].Text, "Finland, Helsinki") || buttons[1][0].CallbackData != CallbackHelpBack {
		t.Fatalf("existing enabled-server navigation changed: %+v", buttons)
	}
}

func TestStaleDisabledCallbackDoesNotAccessDatabaseOrIssueKey(t *testing.T) {
	b := testBotWithDisabledServer(t)
	caller := &recordingTelegramCaller{t: t}
	api, err := telego.NewBot("123456789:"+strings.Repeat("x", 35), telego.WithAPICaller(caller))
	if err != nil {
		t.Fatal(err)
	}
	b.bot = api
	// Deliberately leave b.db nil: retirement must return before any DB access,
	// key generation, or administrator notification.
	b.handleGetKeyCallback(api, telego.Update{CallbackQuery: &telego.CallbackQuery{
		ID:      "stale-callback",
		Data:    "getkey_7",
		Message: &telego.Message{MessageID: 17, Chat: telego.Chat{ID: 42}},
	}})
	if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" || caller.calls[1].method != "editMessageText" {
		t.Fatalf("expected callback acknowledgement and retirement message only: %+v", caller.calls)
	}
	var callbackID, text string
	json.Unmarshal(caller.calls[0].body["callback_query_id"], &callbackID)
	json.Unmarshal(caller.calls[1].body["text"], &text)
	if callbackID != "stale-callback" || !strings.Contains(text, "Выберите другой сервер") {
		t.Fatalf("missing callback acknowledgement or localized retirement message: id=%q text=%q", callbackID, text)
	}
	if !strings.Contains(string(caller.calls[1].body["reply_markup"]), `"getkey_"`) {
		t.Fatal("retirement message must link back to the current server selection")
	}
}
