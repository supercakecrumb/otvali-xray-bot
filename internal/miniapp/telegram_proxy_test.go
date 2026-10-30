package miniapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

const syntheticProxyURL = "https://t.me/proxy?server=proxy.example&port=8443&secret=ee00112233445566778899aabbccddeeff6578616d706c652e636f6d"

type proxyIssuerFake struct {
	listed, issued []int64
	endpointID     string
	err            error
	credential     telegramproxy.Credential
	// IssueAll results; nil all returns every synthetic route.
	allIssued   []int64
	all         []telegramproxy.Credential
	unavailable []string
	allBudget   time.Duration
}

func (f *proxyIssuerFake) IssueAll(ctx context.Context, actor int64) ([]telegramproxy.Credential, []string, error) {
	f.allIssued = append(f.allIssued, actor)
	if deadline, ok := ctx.Deadline(); ok {
		f.allBudget = time.Until(deadline)
	}
	if f.err != nil {
		return nil, nil, f.err
	}
	if f.all == nil {
		return syntheticProxyAll(), []string{}, nil
	}
	return f.all, f.unavailable, nil
}

func (f *proxyIssuerFake) List(_ context.Context, actor int64) ([]telegramproxy.Endpoint, error) {
	f.listed = append(f.listed, actor)
	return []telegramproxy.Endpoint{
		{ID: "finland", Name: "Финляндия", Protocol: "mtproto", Primary: true},
		{ID: "stockholm", Name: "Швеция", Protocol: "mtproto"},
		{ID: "msk-finland", Name: "Москва → Финляндия", Protocol: "mtproto"},
		{ID: "msk-stockholm", Name: "Москва → Швеция", Protocol: "mtproto"},
	}, f.err
}

func (f *proxyIssuerFake) Issue(_ context.Context, actor int64, id string) (telegramproxy.Credential, error) {
	f.issued = append(f.issued, actor)
	f.endpointID = id
	value := f.credential
	if value.URL == "" {
		value = telegramproxy.Credential{EndpointID: id, Name: "Выбранный маршрут", URL: syntheticProxyURL}
	}
	return value, f.err
}

func TestTelegramProxyServiceChecksBoundNumericActorBeforeProvider(t *testing.T) {
	for _, state := range []string{"bound", "missing", "mismatch", "unbound", "lookup_error"} {
		t.Run(state, func(t *testing.T) {
			s, store, _, _ := fixtureService()
			provider := &proxyIssuerFake{}
			s.SetTelegramProxy(provider)
			switch state {
			case "missing":
				store.err = database.ErrUserNotFound
			case "mismatch":
				other := int64(43)
				store.user.TelegramID = &other
			case "unbound":
				store.user.TelegramID = nil
			case "lookup_error":
				store.err = errors.New("private-query")
			}
			_, listErr := s.TelegramProxies(context.Background(), 42)
			_, issueErr := s.TelegramProxy(context.Background(), 42, "msk-finland")
			if state == "bound" {
				if listErr != nil || issueErr != nil || len(provider.listed) != 1 || len(provider.issued) != 1 || provider.issued[0] != 42 || provider.endpointID != "msk-finland" {
					t.Fatal("bound actor must use own selected route")
				}
			} else if listErr == nil || issueErr == nil || len(provider.listed)+len(provider.issued) != 0 {
				t.Fatal("unverified binding reached proxy provider")
			}
		})
	}
}

func TestTelegramProxyHTTPMetadataAndExplicitIssueFromBothBots(t *testing.T) {
	s, _, _, _ := fixtureService()
	provider := &proxyIssuerFake{}
	s.SetTelegramProxy(provider)
	value, err := NewHandler(Config{Enabled: true, AllowedOrigins: []string{testOrigin}}, testToken, s, nil, secondTestToken)
	if err != nil {
		t.Fatal(err)
	}
	h := value.(*handler)
	h.now = func() time.Time { return testNow }
	for _, token := range []string{testToken, secondTestToken} {
		auth := func(r *http.Request) { r.Header.Set("Authorization", "tma "+initDataWithToken(42, token)) }
		w := request(h, "/api/miniapp/telegram/proxies", "{}", auth)
		if w.Code != 200 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "url") || strings.Contains(w.Body.String(), "connected") || strings.Count(w.Body.String(), `"protocol":"mtproto"`) != 4 || len(provider.issued) != len(provider.listed)-1 {
			t.Fatal("metadata must list exactly four routes without issuing or reporting connection")
		}
		w = request(h, "/api/miniapp/telegram/proxy", `{"id":"msk-stockholm"}`, auth)
		var result struct {
			Proxy telegramproxy.Credential `json:"proxy"`
			State string                   `json:"state"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Proxy.URL != syntheticProxyURL || result.Proxy.EndpointID != "msk-stockholm" || result.State != "issued" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("explicit credential needs bounded authenticated private response with issued-only state")
		}
	}
	if len(provider.issued) != 2 || provider.issued[0] != 42 || provider.issued[1] != 42 {
		t.Fatal("signing bot changed account identity")
	}
}

func TestTelegramProxyHTTPRejectsInvalidOrUnauthenticatedRequests(t *testing.T) {
	for _, item := range []struct{ path, body, mode string }{
		{"proxy", `{"id":"finland","user_id":7}`, ""},
		{"proxy", `{"id":"unknown"}`, ""},
		{"proxy", `{}`, ""},
		{"proxy", `{"id":"finland"}`, "no_auth"},
		{"proxy", `{"id":"finland"}`, "bad_origin"},
		{"proxy", `{"id":"finland"}`, "get"},
		{"proxies", `{"id":"finland"}`, ""},
	} {
		t.Run(item.path+item.mode+item.body, func(t *testing.T) {
			s, _, _, _ := fixtureService()
			provider := &proxyIssuerFake{}
			s.SetTelegramProxy(provider)
			h, _ := newTestHandler(t)
			h.backend = s
			w := request(h, "/api/miniapp/telegram/"+item.path, item.body, func(r *http.Request) {
				switch item.mode {
				case "no_auth":
					r.Header.Del("Authorization")
				case "bad_origin":
					r.Header.Set("Origin", "https://evil.example")
				case "get":
					r.Method = "GET"
				}
			})
			if w.Code < 400 || len(provider.listed)+len(provider.issued) != 0 {
				t.Fatal("invalid request reached provider")
			}
		})
	}
	// A backend without the optional feature remains usable and fails closed.
	h, backend := newTestHandler(t)
	w := request(h, "/api/miniapp/telegram/proxies", `{}`, nil)
	if w.Code != 503 || len(backend.calls) != 0 {
		t.Fatal("optional feature must not fall through to VPN issuance")
	}
}

func TestTelegramProxyFailuresAreSanitizedAndCannotReturnAnotherRoute(t *testing.T) {
	for _, item := range []struct {
		credential telegramproxy.Credential
		err        error
	}{
		{err: errors.New("secret-token-and-private-response")},
		{credential: telegramproxy.Credential{EndpointID: "stockholm", URL: syntheticProxyURL}},
		{credential: telegramproxy.Credential{EndpointID: "finland", URL: "https://evil.example/proxy?secret=private"}},
	} {
		s, _, _, _ := fixtureService()
		provider := &proxyIssuerFake{credential: item.credential, err: item.err}
		s.SetTelegramProxy(provider)
		h, _ := newTestHandler(t)
		h.backend = s
		w := request(h, "/api/miniapp/telegram/proxy", `{"id":"finland"}`, nil)
		if w.Code != 503 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "evil") || strings.Contains(w.Body.String(), "proxy") {
			t.Fatal("failed issuance leaked credentials or provider errors")
		}
	}
}
