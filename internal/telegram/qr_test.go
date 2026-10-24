package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	th "github.com/mymmrac/telego/telegohandler"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/qrcode"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"github.com/supercakecrumb/otvali-xray-bot/internal/x3ui"
)

type qrTelegramCaller struct {
	t          *testing.T
	calls      []recordedTelegramCall
	images     [][]byte
	failMethod string
}

func (r *qrTelegramCaller) Call(url string, data *ta.RequestData) (*ta.Response, error) {
	r.t.Helper()
	body := make(map[string]json.RawMessage)
	media, params, err := mime.ParseMediaType(data.ContentType)
	if err != nil {
		r.t.Fatal(err)
	}
	if media == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(data.Buffer.Bytes()), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				r.t.Fatal(err)
			}
			value, err := io.ReadAll(part)
			if err != nil {
				r.t.Fatal(err)
			}
			if part.FileName() != "" {
				r.images = append(r.images, value)
				continue
			}
			if part.FormName() == "chat_id" || part.FormName() == "reply_markup" {
				body[part.FormName()] = value
			} else {
				body[part.FormName()], _ = json.Marshal(string(value))
			}
		}
	} else if err := json.Unmarshal(data.Buffer.Bytes(), &body); err != nil {
		r.t.Fatal(err)
	}
	method := url[strings.LastIndex(url, "/")+1:]
	r.calls = append(r.calls, recordedTelegramCall{method: method, body: body})
	if method == r.failMethod {
		return nil, errors.New("sensitive-private-bearer transport error")
	}
	if method == "sendPhoto" || method == "sendMessage" || method == "editMessageText" {
		return &ta.Response{Ok: true, Result: []byte(`{"message_id":17,"chat":{"id":42,"type":"private"}}`)}, nil
	}
	return &ta.Response{Ok: true, Result: []byte(`true`)}, nil
}

func qrTestBot(t *testing.T, issuer StockholmIssuer) (*Bot, *qrTelegramCaller, *bytes.Buffer) {
	t.Helper()
	caller := &qrTelegramCaller{t: t}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	api, err := telego.NewBot("123456789:"+strings.Repeat("x", 35), telego.WithAPICaller(caller), telego.WithLogger(telegramAPILogger{logger: logger}))
	if err != nil {
		t.Fatal(err)
	}
	return &Bot{bot: api, logger: logger, stockholm: issuer}, caller, logs
}

func qrCommand(text string) telego.Update {
	return telego.Update{Message: &telego.Message{MessageID: 19, Chat: telego.Chat{ID: 42, Type: "private"}, From: &telego.User{ID: 42, Username: "current_actor"}, Text: text, Entities: []telego.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(text)}}}}
}

func assertQRPhoto(t *testing.T, caller *qrTelegramCaller, raw, kind string) {
	t.Helper()
	if len(caller.images) != 1 {
		t.Fatal("one uploaded PNG expected")
	}
	want, err := qrcode.PNG(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, caller.images[0]) {
		t.Fatal("image must encode exact selected credential")
	}
	image, err := png.Decode(bytes.NewReader(caller.images[0]))
	if err != nil || image.Bounds().Dx() < 300 {
		t.Fatal("readable PNG required")
	}
	var photo *recordedTelegramCall
	for i := range caller.calls {
		if caller.calls[i].method == "sendPhoto" {
			photo = &caller.calls[i]
		}
	}
	if photo == nil {
		t.Fatal("photo upload missing")
	}
	var id int64
	if json.Unmarshal(photo.body["chat_id"], &id) != nil || id != 42 {
		t.Fatal("photo must go to current actor private chat")
	}
	var caption string
	json.Unmarshal(photo.body["caption"], &caption)
	if !strings.Contains(caption, kind) || !strings.Contains(caption, "Никому") || strings.Contains(caption, raw) {
		t.Fatal("caption must distinguish import format without duplicating bearer")
	}
	for _, call := range caller.calls {
		for _, value := range call.body {
			if strings.Contains(string(value), raw) {
				t.Fatal("raw credential leaked into callback/caption")
			}
		}
	}
}

func TestQRCommandsAreRegisteredAndNeedNoArguments(t *testing.T) {
	for _, command := range []string{"/qr", "/qr_key", "/qr_subscription"} {
		t.Run(command, func(t *testing.T) {
			issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://subscriptions.example/private-bearer"}}, keys: managedTestKeys()}
			b, caller, _ := qrTestBot(t, issuer)
			updates := make(chan telego.Update, 1)
			var err error
			b.bh, err = th.NewBotHandler(b.bot, updates)
			if err != nil {
				t.Fatal(err)
			}
			b.registerCommands()
			done := make(chan struct{})
			b.bh.Use(func(bot *telego.Bot, update telego.Update, next th.Handler) { next(bot, update); close(done) })
			stopped := make(chan struct{})
			go func() { b.bh.Start(); close(stopped) }()
			updates <- qrCommand(command)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("QR command did not complete")
			}
			close(updates)
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not stop")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			b.bh.StopWithContext(ctx)
			if len(caller.calls) != 1 {
				t.Fatal("command must deliver its requested screen directly")
			}
			switch command {
			case "/qr":
				if len(issuer.issueIDs)+len(issuer.keyIDs) != 0 {
					t.Fatal("QR menu must not issue credentials")
				}
				got := callbacks(callKeyboard(t, caller.calls[0]))
				if len(got) != 3 || got[0] != CallbackQRSubscription || got[1] != CallbackQRKeys {
					t.Fatal("QR type choices missing")
				}
			case "/qr_key":
				if len(issuer.keyIDs) != 1 || len(issuer.issueIDs) != 0 {
					t.Fatal("key menu must use actor keys only")
				}
			case "/qr_subscription":
				assertQRPhoto(t, caller, issuer.subscription.URL, "подписки")
			}
		})
	}
}

func TestQRSubscriptionAndManagedKeyUseBoundedCurrentActor(t *testing.T) {
	for _, kind := range []string{"subscription", "key"} {
		t.Run(kind, func(t *testing.T) {
			issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true, subscription: remnawave.Subscription{URL: "https://subscriptions.example/private-bearer?x=1&y=2"}}, keys: managedTestKeys()}
			b, caller, logs := qrTestBot(t, issuer)
			update := stockholmUpdate()
			update.CallbackQuery.Data = CallbackQRSubscription
			raw, caption := issuer.subscription.URL, "подписки"
			if kind == "key" {
				update.CallbackQuery.Data = CallbackQRManagedPrefix + issuer.keys[1].ID
				raw, caption = issuer.keys[1].URL, "ключа"
			}
			b.handleQRCallback(b.bot, update)
			if len(caller.calls) != 2 || caller.calls[0].method != "answerCallbackQuery" {
				t.Fatal("callback must acknowledge then upload once")
			}
			if kind == "key" {
				if len(issuer.keyIDs) != 1 || issuer.keyIDs[0] != 42 || !issuer.keyDeadline || len(issuer.issueIDs) != 0 {
					t.Fatal("QR must reread selected current actor key")
				}
			} else if len(issuer.issueIDs) != 1 || issuer.issueIDs[0] != 42 || !issuer.hasDeadline {
				t.Fatal("subscription must use bounded actor request")
			}
			assertQRPhoto(t, caller, raw, caption)
			if strings.Contains(logs.String(), "private-bearer") || strings.Contains(logs.String(), "00000000") {
				t.Fatal("credential logged")
			}
		})
	}
}

func TestQRRejectsForgedCallbacksBeforeBackend(t *testing.T) {
	for _, callback := range []string{CallbackQRMenu, CallbackQRKeys, CallbackQRSubscription, CallbackQRManagedPrefix + "0123456789abcdef", CallbackQRLegacyPrefix + "8"} {
		for _, reason := range []string{"group", "actor", "missing_message", "ack_error"} {
			t.Run(callback+"/"+reason, func(t *testing.T) {
				issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true}, keys: managedTestKeys()}
				b, caller, _ := qrTestBot(t, issuer)
				update := stockholmUpdate()
				update.CallbackQuery.Data = callback
				switch reason {
				case "group":
					update.CallbackQuery.Message = &telego.Message{Chat: telego.Chat{ID: -42, Type: "group"}}
				case "actor":
					update.CallbackQuery.From.ID = 7
				case "missing_message":
					update.CallbackQuery.Message = nil
				case "ack_error":
					caller.failMethod = "answerCallbackQuery"
				}
				b.handleQRCallback(b.bot, update)
				if len(caller.calls) != 1 || caller.calls[0].method != "answerCallbackQuery" || len(issuer.keyIDs)+len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
					t.Fatal("forged callback reached backend or skipped ack")
				}
			})
		}
	}
	issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true}, keys: managedTestKeys()}
	b, caller, _ := qrTestBot(t, issuer)
	for _, handler := range []func(*telego.Bot, telego.Update){b.handleQR, b.handleQRSubscription, b.handleQRKey} {
		update := qrCommand("/qr")
		update.Message.From.ID = 7
		handler(b.bot, update)
	}
	if len(caller.calls)+len(issuer.keyIDs)+len(issuer.issueIDs)+len(issuer.eligibleIDs) != 0 {
		t.Fatal("forged command reached backend")
	}
}

func TestQRManagedFailuresDoNotRevealCredentials(t *testing.T) {
	for _, reason := range []string{"malformed", "stale", "ineligible", "inactive", "backend_error", "bad_url", "oversize", "delivery_error"} {
		t.Run(reason, func(t *testing.T) {
			issuer := &fakeIndividualKeyIssuer{fakeStockholmIssuer: fakeStockholmIssuer{eligible: true}, keys: managedTestKeys()}
			b, caller, logs := qrTestBot(t, issuer)
			update := stockholmUpdate()
			update.CallbackQuery.Data = CallbackQRManagedPrefix + issuer.keys[0].ID
			switch reason {
			case "malformed":
				update.CallbackQuery.Data = CallbackQRManagedPrefix + "vless://private-bearer"
			case "stale":
				update.CallbackQuery.Data = CallbackQRManagedPrefix + "ffffffffffffffff"
			case "ineligible":
				issuer.eligible = false
			case "inactive":
				issuer.keysErr = remnawave.ErrInactive
			case "backend_error":
				issuer.keysErr = errors.New("sensitive-private-bearer provider error")
			case "bad_url":
				issuer.keys[0].URL = "javascript:private-bearer"
			case "oversize":
				issuer.keys[0].URL = "vless://private-bearer@example.org:443#" + strings.Repeat("x", 2800)
			case "delivery_error":
				caller.failMethod = "sendPhoto"
			}
			b.handleQRCallback(b.bot, update)
			if reason != "delivery_error" && len(caller.images) != 0 {
				t.Fatal("invalid/unavailable key generated image")
			}
			last := caller.calls[len(caller.calls)-1]
			if last.method != "sendMessage" {
				t.Fatal("failure must have usable text fallback")
			}
			if reason == "oversize" && !strings.Contains(callText(t, last), "/get_key") {
				t.Fatal("oversize must preserve copy fallback")
			}
			if reason == "backend_error" && strings.Contains(callText(t, last), "Повторить") {
				t.Fatal("failure must not refer to a missing retry button")
			}
			for _, call := range caller.calls {
				for _, value := range call.body {
					if strings.Contains(string(value), "private-bearer") || strings.Contains(string(value), "00000000") {
						t.Fatal("failure leaked bearer")
					}
				}
			}
			if strings.Contains(logs.String(), "sensitive") || strings.Contains(logs.String(), "private-bearer") {
				t.Fatal("transport error logged raw")
			}
		})
	}
}

type qrLegacyStoreFake struct {
	user   *database.User
	server *database.Server
	calls  int
}

func (f *qrLegacyStoreFake) GetUserByTelegramID(int64) (*database.User, error) {
	f.calls++
	return f.user, nil
}
func (f *qrLegacyStoreFake) GetAllServers() ([]database.Server, error) {
	f.calls++
	return []database.Server{*f.server}, nil
}
func (f *qrLegacyStoreFake) GetServerByID(int64) (*database.Server, error) {
	f.calls++
	return f.server, nil
}

type qrLegacyIssuerFake struct {
	disabled bool
	key      string
	err      error
	ids      []int64
	bounded  bool
}

func (f *qrLegacyIssuerFake) IsServerDisabled(int64) bool { return f.disabled }
func (f *qrLegacyIssuerFake) GetUserKeyForTelegramID(ctx context.Context, _ *database.Server, _ string, id int64) (string, error) {
	f.ids = append(f.ids, id)
	d, ok := ctx.Deadline()
	f.bounded = ok && time.Until(d) > 0 && time.Until(d) <= 25*time.Second
	return f.key, f.err
}

func TestQRLegacyReadsExistingNumericOwnedKeyAndHonorsAccess(t *testing.T) {
	for _, reason := range []string{"success", "disabled", "exclusive", "wrong_actor", "wrong_server", "inactive", "missing", "invalid_id"} {
		t.Run(reason, func(t *testing.T) {
			b, caller, _ := qrTestBot(t, nil)
			id := int64(42)
			inbound := 1
			store := &qrLegacyStoreFake{user: &database.User{TelegramID: &id, Username: "current_actor"}, server: &database.Server{ID: 8, Country: "Finland", City: "Helsinki", InboundID: &inbound}}
			legacy := &qrLegacyIssuerFake{key: managedTestKeys()[0].URL}
			b.qrStore, b.qrLegacy = store, legacy
			update := stockholmUpdate()
			update.CallbackQuery.Data = CallbackQRLegacyPrefix + "8"
			switch reason {
			case "disabled":
				legacy.disabled = true
			case "exclusive":
				store.server.IsExclusive = true
			case "wrong_actor":
				other := int64(7)
				store.user.TelegramID = &other
			case "wrong_server":
				store.server.ID = 9
			case "inactive":
				legacy.err = x3ui.ErrKeyDisabled
			case "missing":
				legacy.err = x3ui.ErrKeyNotFound
			case "invalid_id":
				update.CallbackQuery.Data = CallbackQRLegacyPrefix + "8junk"
			}
			b.handleQRCallback(b.bot, update)
			if caller.calls[0].method != "answerCallbackQuery" {
				t.Fatal("legacy callback not acknowledged")
			}
			if reason == "success" {
				assertQRPhoto(t, caller, legacy.key, "ключа")
				if len(legacy.ids) != 1 || legacy.ids[0] != 42 || !legacy.bounded {
					t.Fatal("legacy must use bounded numeric-owned retrieval")
				}
			} else if len(caller.images) != 0 {
				t.Fatal("invalid legacy request revealed key")
			}
			if (reason == "disabled" || reason == "invalid_id") && store.calls != 0 {
				t.Fatal("disabled/malformed legacy reached database")
			}
			if (reason == "exclusive" || reason == "wrong_actor" || reason == "wrong_server") && len(legacy.ids) != 0 {
				t.Fatal("unauthorized legacy request reached provider")
			}
		})
	}
}

func TestQRDeliveryButtonsUseOnlyOpaqueSelection(t *testing.T) {
	for _, keyboard := range []*telego.InlineKeyboardMarkup{subscriptionDeliveryKeyboard(), qrKeyDeliveryKeyboard(CallbackQRManagedPrefix + "0123456789abcdef"), qrKeyDeliveryKeyboard(CallbackQRLegacyPrefix + "8")} {
		got := callbacks(keyboard)
		if len(got) != 3 || !strings.HasPrefix(got[0], "qr_") {
			t.Fatal("delivery must offer QR plus existing navigation")
		}
		for _, data := range got {
			if len(data) > 64 || strings.Contains(data, "://") {
				t.Fatal("callbacks must not carry credentials")
			}
		}
	}
}
