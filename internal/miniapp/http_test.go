package miniapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/qrcode"
)

const testOrigin = "https://otvali.aurorass.art"

type fakeBackend struct {
	calls           []string
	ids             []int64
	err             error
	deadline        bool
	subscriptionURL string
	keyURL          string
}

func (f *fakeBackend) record(ctx context.Context, id int64, method string) {
	f.calls = append(f.calls, method)
	f.ids = append(f.ids, id)
	end, ok := ctx.Deadline()
	f.deadline = ok && time.Until(end) > 0 && time.Until(end) <= 25*time.Second
}
func (f *fakeBackend) Account(ctx context.Context, id int64) (Account, error) {
	f.record(ctx, id, "session")
	return Account{Managed: true, LegacyFinland: true}, f.err
}
func (f *fakeBackend) Subscription(ctx context.Context, id int64) (Subscription, error) {
	f.record(ctx, id, "subscription")
	url := f.subscriptionURL
	if url == "" {
		url = "https://sub.example/private-bearer"
	}
	return Subscription{URL: url, QRCode: "unexpected-upstream-qr"}, f.err
}
func (f *fakeBackend) Keys(ctx context.Context, id int64) ([]Key, error) {
	f.record(ctx, id, "keys")
	return []Key{{ID: "0123456789abcdef", Name: "Profile", URL: "vless://private-key@host:443", QRCode: "unexpected-upstream-qr"}}, f.err
}
func (f *fakeBackend) Key(ctx context.Context, id int64, key string) (Key, error) {
	f.record(ctx, id, "key")
	url := f.keyURL
	if url == "" {
		url = "vless://private-key@host:443"
	}
	return Key{ID: key, Name: "Profile", URL: url, QRCode: "unexpected-upstream-qr"}, f.err
}

type fakeHealth struct{ err error }

func (f fakeHealth) PingContext(context.Context) error { return f.err }
func newTestHandler(t *testing.T) (*handler, *fakeBackend) {
	t.Helper()
	backend := &fakeBackend{}
	value, err := NewHandler(Config{Enabled: true, AllowedOrigins: []string{testOrigin}}, testToken, backend, fakeHealth{})
	if err != nil {
		t.Fatal(err)
	}
	h := value.(*handler)
	h.now = func() time.Time { return testNow }
	return h, backend
}
func request(h http.Handler, path, body string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Authorization", "tma "+initData(42))
	req.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func TestHTTPReadAndIssueAreExplicitAndMetadataContainsNoKeys(t *testing.T) {
	h, backend := newTestHandler(t)
	for _, path := range []string{"session", "subscription", "keys", "key"} {
		body := "{}"
		if path == "key" {
			body = `{"id":"0123456789abcdef"}`
		}
		w := request(h, "/api/miniapp/"+path, body, nil)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Access-Control-Allow-Origin") != testOrigin {
			t.Fatal("private response/CORS policy missing")
		}
		if path == "session" && (strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "qr_code")) {
			t.Fatal("session issued/disclosed credentials")
		}
		if path == "keys" && (strings.Contains(w.Body.String(), "vless") || strings.Contains(w.Body.String(), "url") || strings.Contains(w.Body.String(), "qr_code") || strings.Contains(w.Body.String(), "unexpected-upstream-qr")) {
			t.Fatal("key metadata leaked credentials")
		}
	}
	if len(backend.calls) != 4 || backend.calls[0] != "session" || !backend.deadline {
		t.Fatal("requests must dispatch only requested bounded action")
	}
	for _, id := range backend.ids {
		if id != 42 {
			t.Fatal("body/username selected actor")
		}
	}
}

func TestHTTPExplicitCredentialIncludesPNGForItsExactURL(t *testing.T) {
	for _, action := range []string{"subscription", "key"} {
		t.Run(action, func(t *testing.T) {
			h, backend := newTestHandler(t)
			backend.subscriptionURL = "https://subscriptions.example/fictional?x=1&y=2"
			backend.keyURL = "vless://00000000-0000-4000-8000-000000000001@example.invalid:443?security=reality#Москва%20→%20Стокгольм"
			body, want := "{}", backend.subscriptionURL
			if action == "key" {
				body, want = `{"id":"0123456789abcdef"}`, backend.keyURL
			}
			response := request(h, "/api/miniapp/"+action, body, nil)
			var payload map[string]struct {
				URL    string `json:"url"`
				QRCode string `json:"qr_code"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload[action].URL != want {
				t.Fatal("explicit credential response changed the original URL")
			}
			data, err := base64.StdEncoding.DecodeString(payload[action].QRCode)
			if err != nil {
				t.Fatal("QR must be plain base64 without a data URL prefix")
			}
			if _, err := png.Decode(bytes.NewReader(data)); err != nil {
				t.Fatal("QR response is not a valid PNG")
			}
			expected, err := qrcode.PNG(want)
			if err != nil || !bytes.Equal(data, expected) {
				t.Fatal("QR did not encode the exact credential selected by this request")
			}
			if len(backend.calls) != 1 || backend.calls[0] != action || backend.ids[0] != 42 || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("QR must share the existing authenticated explicit credential response")
			}
		})
	}
}

func TestHTTPUnencodableQROmittedWhileOriginalLinkRemainsUsable(t *testing.T) {
	for _, action := range []string{"subscription", "key"} {
		t.Run(action, func(t *testing.T) {
			h, backend := newTestHandler(t)
			backend.subscriptionURL = "https://subscriptions.example/" + strings.Repeat("x", 3000)
			backend.keyURL = "vless://fictional@example.invalid:443?pbk=" + strings.Repeat("x", 3000)
			body, want := "{}", backend.subscriptionURL
			if action == "key" {
				body, want = `{"id":"0123456789abcdef"}`, backend.keyURL
			}
			response := request(h, "/api/miniapp/"+action, body, nil)
			var payload map[string]struct {
				URL    string  `json:"url"`
				QRCode *string `json:"qr_code"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload[action].URL != want {
				t.Fatal("QR capacity failure must not truncate or remove the original credential")
			}
			if payload[action].QRCode != nil || strings.Contains(response.Body.String(), `"qr_code"`) || strings.Contains(response.Body.String(), "unexpected-upstream-qr") {
				t.Fatal("failed QR generation must not return partial or unrelated upstream data")
			}
		})
	}
}
func TestHTTPRejectsInvalidRequestsBeforeAnyBackend(t *testing.T) {
	for _, name := range []string{"no_auth", "duplicate_auth", "forged", "bad_origin", "missing_origin", "get", "unknown_id_field", "unknown_user_field", "unexpected_key_field", "oversized_body", "extra_json", "bad_key", "query_secret", "non_json", "malformed"} {
		t.Run(name, func(t *testing.T) {
			h, backend := newTestHandler(t)
			path := "/api/miniapp/session"
			body := "{}"
			if name == "bad_key" {
				path = "/api/miniapp/key"
				body = `{"id":"some-other-user"}`
			}
			if name == "unknown_id_field" {
				body = `{"telegram_id":99}`
			}
			if name == "unexpected_key_field" {
				body = `{"id":""}`
			}
			if name == "unknown_user_field" {
				body = `{"user":{"id":99}}`
			}
			if name == "oversized_body" {
				body = `{"id":"` + strings.Repeat("x", 2000) + `"}`
			}
			if name == "extra_json" {
				body = "{}{}"
			}
			if name == "query_secret" {
				path += "?initData=secret"
			}
			if name == "malformed" {
				body = "{"
			}
			w := request(h, path, body, func(r *http.Request) {
				switch name {
				case "no_auth":
					r.Header.Del("Authorization")
				case "duplicate_auth":
					r.Header.Add("Authorization", "tma other")
				case "forged":
					r.Header.Set("Authorization", "tma forged")
				case "bad_origin":
					r.Header.Set("Origin", "https://attacker.example")
				case "missing_origin":
					r.Header.Del("Origin")
				case "get":
					r.Method = "GET"
				case "non_json":
					r.Header.Set("Content-Type", "text/plain")
				}
			})
			if w.Code < 400 || len(backend.calls) != 0 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("invalid request reached issuance or became cacheable")
			}
		})
	}
}
func TestHTTPPreflightAndHealthDoNotAuthenticateOrIssue(t *testing.T) {
	h, backend := newTestHandler(t)
	req := httptest.NewRequest("OPTIONS", "/api/miniapp/key", nil)
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Credentials") != "" || len(backend.calls) != 0 {
		t.Fatal("bounded cookie-free CORS preflight failed")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 || len(backend.calls) != 0 {
		t.Fatal("health must not issue or depend on provider")
	}
}
func TestHTTPGenericErrorsAndRateLimit(t *testing.T) {
	h, backend := newTestHandler(t)
	backend.err = errors.New("private-token https://sub.example/private-bearer raw-response")
	w := request(h, "/api/miniapp/subscription", "{}", nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private-") {
		t.Fatal("raw provider error leaked")
	}
	backend.err = nil
	for i := 1; i < 20; i++ {
		request(h, "/api/miniapp/session", "{}", nil)
	}
	w = request(h, "/api/miniapp/subscription", "{}", nil)
	if w.Code != 429 || len(backend.calls) != 20 {
		t.Fatal("per-ID request cap did not prevent issuance")
	}
	var data map[string]any
	if json.Unmarshal(w.Body.Bytes(), &data) != nil {
		t.Fatal("errors must be JSON")
	}
}
func TestMiniAppConfigDefaultsOffAndRequiresExactOrigins(t *testing.T) {
	get := func(string) string { return "" }
	c, err := LoadConfig(get)
	if err != nil || c.Enabled || c.Port != 8080 {
		t.Fatal("default must not open listener")
	}
	for _, origin := range []string{"*", "http://otvali.aurorass.art", "https://user:secret@otvali.aurorass.art", "https://otvali.aurorass.art/path", "https://otvali.aurorass.art?secret=x"} {
		_, err := LoadConfig(func(key string) string {
			if key == "MINIAPP_ENABLED" {
				return "true"
			}
			if key == "MINIAPP_ALLOWED_ORIGINS" {
				return origin
			}
			return ""
		})
		if err == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
}

func TestHTTPAccessDisabledAndNotFoundAreNotTransientErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{ErrAccessDisabled, 403, "access_disabled"}, {ErrNotFound, 404, "not_found"}, {ErrUnavailable, 503, "unavailable"}} {
		h, backend := newTestHandler(t)
		backend.err = tc.err
		response := request(h, "/api/miniapp/subscription", "{}", nil)
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != tc.status || body.Error.Code != tc.code {
			t.Fatal("access state lost its stable HTTP classification")
		}
		if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("access failure must remain private")
		}
	}
}
