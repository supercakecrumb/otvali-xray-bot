package miniapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

const allProxiesPath = "/api/miniapp/telegram/proxies/all"

func syntheticProxyURLWithPort(port string) string {
	return strings.Replace(syntheticProxyURL, "port=8443", "port="+port, 1)
}

func syntheticProxyAll() []telegramproxy.Credential {
	return []telegramproxy.Credential{
		{EndpointID: "finland", Name: "Финляндия", URL: syntheticProxyURLWithPort("8443")},
		{EndpointID: "stockholm", Name: "Швеция", URL: syntheticProxyURLWithPort("8444")},
		{EndpointID: "msk-finland", Name: "Москва → Финляндия", URL: syntheticProxyURLWithPort("10445")},
		{EndpointID: "msk-stockholm", Name: "Москва → Швеция", URL: syntheticProxyURLWithPort("11445")},
	}
}

type allProxiesResponse struct {
	Proxies     []telegramproxy.Credential `json:"proxies"`
	Unavailable []string                   `json:"unavailable"`
}

func allProxiesHandler(t *testing.T, provider *proxyIssuerFake) (*handler, *fakeStore) {
	t.Helper()
	s, store, _, _ := fixtureService()
	s.SetTelegramProxy(provider)
	value, err := NewHandler(Config{Enabled: true, AllowedOrigins: []string{testOrigin}}, testToken, s, nil, secondTestToken)
	if err != nil {
		t.Fatal(err)
	}
	h := value.(*handler)
	h.now = func() time.Time { return testNow }
	return h, store
}

func endpointIDs(credentials []telegramproxy.Credential) []string {
	ids := []string{}
	for _, c := range credentials {
		ids = append(ids, c.EndpointID)
	}
	return ids
}

func TestTelegramProxyAllServiceChecksBoundNumericActorBeforeProvider(t *testing.T) {
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
			credentials, unavailable, err := s.TelegramProxyAll(context.Background(), 42)
			if state == "bound" {
				if err != nil || len(credentials) != 4 || unavailable == nil || !reflect.DeepEqual(provider.allIssued, []int64{42}) {
					t.Fatal("bound actor must receive every own route")
				}
			} else if err == nil || credentials != nil || len(provider.allIssued) != 0 {
				t.Fatal("unverified binding reached proxy provider")
			}
		})
	}
	s, _, _, _ := fixtureService()
	if _, _, err := s.TelegramProxyAll(context.Background(), 42); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disabled provider must fail closed", err)
	}
}

func TestTelegramProxyAllHTTPIssuesEveryRouteFromBothBots(t *testing.T) {
	provider := &proxyIssuerFake{}
	h, _ := allProxiesHandler(t, provider)
	for _, token := range []string{testToken, secondTestToken} {
		w := request(h, allProxiesPath, "{}", func(r *http.Request) { r.Header.Set("Authorization", "tma "+initDataWithToken(42, token)) })
		var result allProxiesResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("all routes must issue: %d", w.Code)
		}
		if !reflect.DeepEqual(result.Proxies, syntheticProxyAll()) || !strings.Contains(w.Body.String(), `"unavailable":[]`) {
			t.Fatal("response must contain every route in config order and an empty unavailable list")
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("Pragma") != "no-cache" || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Access-Control-Allow-Origin") != testOrigin || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatal("credentials need a private, uncached, cookie-free exact-origin response")
		}
		if strings.Contains(w.Body.String(), "connected") || strings.Contains(w.Body.String(), `"state"`) {
			t.Fatal("issuance must not claim a connection")
		}
	}
	if !reflect.DeepEqual(provider.allIssued, []int64{42, 42}) || len(provider.issued)+len(provider.listed) != 0 {
		t.Fatal("signing bot changed account identity or used single-route issuance")
	}
	if provider.allBudget <= 25*time.Second || provider.allBudget > 30*time.Second {
		t.Fatal("sequential issuance needs a bounded budget below the write timeout", provider.allBudget)
	}
}

func TestTelegramProxyAllHTTPPartialResult(t *testing.T) {
	all := syntheticProxyAll()
	for _, item := range []struct {
		name            string
		credentials     []telegramproxy.Credential
		unavailable     []string
		wantIDs, wantNA []string
	}{
		{"reported", []telegramproxy.Credential{all[0], all[2]}, []string{"stockholm", "msk-stockholm"}, []string{"finland", "msk-finland"}, []string{"stockholm", "msk-stockholm"}},
		{"invalid_link", []telegramproxy.Credential{all[0], {EndpointID: "stockholm", Name: "x", URL: "https://evil.example/proxy?secret=private"}, all[2], all[3]}, []string{}, []string{"finland", "msk-finland", "msk-stockholm"}, []string{"stockholm"}},
		{"unknown_and_duplicate", []telegramproxy.Credential{all[0], {EndpointID: "private-route", URL: all[1].URL}, {EndpointID: "finland", URL: all[1].URL}}, []string{"evil", "finland", "msk-finland"}, []string{"finland"}, []string{"msk-finland"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			provider := &proxyIssuerFake{all: item.credentials, unavailable: item.unavailable}
			h, _ := allProxiesHandler(t, provider)
			w := request(h, allProxiesPath, "{}", nil)
			var result allProxiesResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
				t.Fatalf("partial issuance must still succeed: %d", w.Code)
			}
			if !reflect.DeepEqual(endpointIDs(result.Proxies), item.wantIDs) || !reflect.DeepEqual(result.Unavailable, item.wantNA) {
				t.Fatalf("partial result mismatch: %v %v", endpointIDs(result.Proxies), result.Unavailable)
			}
			for _, credential := range result.Proxies {
				if !telegramproxy.ValidCredentialURL(credential.URL) {
					t.Fatal("malformed link escaped")
				}
			}
			if strings.Contains(w.Body.String(), "evil") || strings.Contains(w.Body.String(), "private") {
				t.Fatal("untrusted issuer data leaked")
			}
		})
	}
}

func TestTelegramProxyAllHTTPFailures(t *testing.T) {
	for _, item := range []struct {
		name   string
		mutate func(*proxyIssuerFake, *fakeStore)
		status int
		code   string
	}{
		{"none_available", func(p *proxyIssuerFake, _ *fakeStore) { p.err = telegramproxy.ErrUnavailable }, 503, "unavailable"},
		{"raw_error", func(p *proxyIssuerFake, _ *fakeStore) { p.err = errors.New("secret-token-and-private-response") }, 503, "unavailable"},
		{"empty", func(p *proxyIssuerFake, _ *fakeStore) { p.all = []telegramproxy.Credential{} }, 503, "unavailable"},
		{"all_invalid", func(p *proxyIssuerFake, _ *fakeStore) {
			p.all = []telegramproxy.Credential{{EndpointID: "finland", URL: "https://evil.example/proxy?secret=private"}}
		}, 503, "unavailable"},
		{"ineligible", func(p *proxyIssuerFake, _ *fakeStore) { p.err = telegramproxy.ErrNotEligible }, 403, "forbidden"},
		{"revoked", func(p *proxyIssuerFake, _ *fakeStore) { p.err = telegramproxy.ErrInactive }, 403, "access_disabled"},
		{"conflict", func(p *proxyIssuerFake, _ *fakeStore) { p.err = telegramproxy.ErrConflict }, 403, "access_disabled"},
		{"unbound", func(_ *proxyIssuerFake, s *fakeStore) { s.err = database.ErrUserNotFound }, 403, "forbidden"},
	} {
		t.Run(item.name, func(t *testing.T) {
			provider := &proxyIssuerFake{}
			h, store := allProxiesHandler(t, provider)
			item.mutate(provider, store)
			w := request(h, allProxiesPath, "{}", nil)
			if w.Code != item.status || w.Body.String() != `{"error":{"code":"`+item.code+`"}}`+"\n" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if item.name == "unbound" && len(provider.allIssued) != 0 {
				t.Fatal("unbound actor reached the provider")
			}
		})
	}
}

func TestTelegramProxyAllHTTPRejectsInvalidOrUnauthenticatedRequests(t *testing.T) {
	for _, mode := range []string{"no_auth", "duplicate_auth", "forged", "tampered", "bad_origin", "missing_origin", "get", "id_field", "user_field", "oversized", "extra_json", "non_json", "query"} {
		t.Run(mode, func(t *testing.T) {
			provider := &proxyIssuerFake{}
			h, _ := allProxiesHandler(t, provider)
			path, body := allProxiesPath, "{}"
			switch mode {
			case "id_field":
				body = `{"id":"finland"}`
			case "user_field":
				body = `{"user_id":7}`
			case "oversized":
				// Valid empty JSON padded past the 1 KiB limit: only the
				// body-size guard can reject it.
				body = "{" + strings.Repeat(" ", 2000) + "}"
			case "extra_json":
				body = "{}{}"
			case "query":
				path += "?initData=secret"
			}
			w := request(h, path, body, func(r *http.Request) {
				switch mode {
				case "no_auth":
					r.Header.Del("Authorization")
				case "duplicate_auth":
					r.Header.Add("Authorization", "tma other")
				case "forged":
					r.Header.Set("Authorization", "tma forged")
				case "tampered":
					r.Header.Set("Authorization", "tma "+strings.Replace(initData(42), "42", "43", 1))
				case "bad_origin":
					r.Header.Set("Origin", "https://evil.example")
				case "missing_origin":
					r.Header.Del("Origin")
				case "get":
					r.Method = "GET"
				case "non_json":
					r.Header.Set("Content-Type", "text/plain")
				}
			})
			want := map[string]int{"no_auth": 401, "duplicate_auth": 401, "forged": 401, "tampered": 401, "bad_origin": 403, "missing_origin": 403, "get": 405, "oversized": 400}[mode]
			if (want != 0 && w.Code != want) || w.Code < 400 || len(provider.allIssued)+len(provider.issued)+len(provider.listed) != 0 {
				t.Fatalf("invalid request reached provider or used the wrong status: %d", w.Code)
			}
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || strings.Contains(w.Body.String(), "t.me") {
				t.Fatal("rejection must stay private and contain no credential")
			}
		})
	}
	h, _ := allProxiesHandler(t, &proxyIssuerFake{})
	req := httptest.NewRequest("OPTIONS", allProxiesPath, nil)
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != testOrigin || w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("bounded cookie-free CORS preflight failed")
	}
	// A backend without the optional feature fails closed.
	plain, backend := newTestHandler(t)
	if w := request(plain, allProxiesPath, "{}", nil); w.Code != 503 || len(backend.calls) != 0 {
		t.Fatal("optional feature must not fall through to VPN issuance")
	}
}
