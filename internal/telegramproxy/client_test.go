package telegramproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testRemoteSecret = "0123456789abcdef0123456789abcdef"
const testRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testNextRevision = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func writeEnvelope(w http.ResponseWriter, status int, data any, revision string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data, "revision": revision})
}

func writeFailure(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": code, "message": "sensitive-provider-details"}})
}

func testUser(name, secret string, enabled, runtime bool) map[string]any {
	return map[string]any{
		"username": name, "enabled": enabled, "in_runtime": runtime,
		"links": map[string]any{"classic": []string{}, "secure": []string{}, "tls": []string{
			"tg://proxy?server=proxy.example.test&port=8444&secret=ee" + secret + "6578616d706c652e636f6d",
		}, "tls_domains": []any{}},
	}
}

func newTestClient(t *testing.T, handler http.HandlerFunc, timeout time.Duration) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "Bearer test-only", &http.Client{Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}

func serveVersion(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/v1/system/info" {
		return false
	}
	writeEnvelope(w, 200, map[string]any{"version": telemtVersion, "config_hash": testRevision}, testRevision)
	return true
}

func TestClientCreateIdempotentWithRuntimeReadback(t *testing.T) {
	var mu sync.Mutex
	exists := false
	posts := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("missing authorization")
		}
		if serveVersion(w, r) {
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/users" {
			posts++
			if r.Header.Get("If-Match") != testRevision {
				t.Error("missing revision precondition")
			}
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["secret"] != testRemoteSecret || body["username"] != "otvali_test" || body["enabled"] != true {
				t.Error("unexpected create request")
			}
			exists = true
			writeEnvelope(w, 202, map[string]any{"user": testUser("otvali_test", testRemoteSecret, true, false), "secret": testRemoteSecret}, testNextRevision)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/users/otvali_test" {
			if !exists {
				writeFailure(w, 404, "not_found")
				return
			}
			writeEnvelope(w, 200, testUser("otvali_test", testRemoteSecret, true, true), testNextRevision)
			return
		}
		t.Error("unexpected API request")
		writeFailure(w, 404, "not_found")
	}, 0)
	for i := 0; i < 2; i++ {
		user, err := client.CreateUser(context.Background(), "otvali_test", testRemoteSecret)
		if err != nil || user.Secret != testRemoteSecret || !user.InRuntime {
			t.Fatalf("create failed: %v", err)
		}
	}
	if posts != 1 {
		t.Fatalf("created %d times", posts)
	}
}

func TestClientTimeoutReconcilesWithoutSecondWrite(t *testing.T) {
	var mu sync.Mutex
	exists := false
	posts := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if serveVersion(w, r) {
			return
		}
		if r.Method == http.MethodPost {
			mu.Lock()
			exists = true
			posts++
			mu.Unlock()
			time.Sleep(60 * time.Millisecond)
			writeEnvelope(w, 201, map[string]any{"user": testUser("otvali_test", testRemoteSecret, true, true), "secret": testRemoteSecret}, testNextRevision)
			return
		}
		mu.Lock()
		found := exists
		mu.Unlock()
		if !found {
			writeFailure(w, 404, "not_found")
			return
		}
		writeEnvelope(w, 200, testUser("otvali_test", testRemoteSecret, true, true), testNextRevision)
	}, 25*time.Millisecond)
	user, err := client.CreateUser(context.Background(), "otvali_test", testRemoteSecret)
	if err != nil || !user.Enabled {
		t.Fatalf("timeout reconciliation failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 {
		t.Fatalf("wrote %d times", posts)
	}
}

func TestClientExistingCredentialNeverReenabledOrRotated(t *testing.T) {
	for _, tt := range []struct {
		name             string
		enabled, runtime bool
		secret           string
		want             error
	}{
		{"disabled", false, true, testRemoteSecret, ErrInactive},
		{"mismatch", true, true, "ffffffffffffffffffffffffffffffff", ErrConflict},
		{"pending", true, false, testRemoteSecret, ErrUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("existing credential mutated")
				}
				if serveVersion(w, r) {
					return
				}
				writeEnvelope(w, 200, testUser("otvali_test", tt.secret, tt.enabled, tt.runtime), testRevision)
			}, 0)
			_, err := client.CreateUser(context.Background(), "otvali_test", testRemoteSecret)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
		})
	}
}

func TestClientDisableKeepsDisabledRuntimePresence(t *testing.T) {
	enabled := true
	posts := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if serveVersion(w, r) {
			return
		}
		if r.Method == http.MethodPost {
			if r.URL.Path != "/v1/users/otvali_test/disable" || r.Header.Get("If-Match") != testRevision {
				t.Error("incorrect disable request")
			}
			posts++
			enabled = false
		}
		writeEnvelope(w, 200, testUser("otvali_test", testRemoteSecret, enabled, true), testRevision)
	}, 0)
	for i := 0; i < 2; i++ {
		user, err := client.DisableUser(context.Background(), "otvali_test")
		if err != nil || user.Enabled || !user.InRuntime {
			t.Fatalf("disable failed: %v", err)
		}
	}
	if posts != 2 {
		t.Fatal("each disable must positively acknowledge the admission fence")
	}
}

func TestClientUncertainDisableDoesNotTrustDisabledReadback(t *testing.T) {
	for _, mode := range []string{"timeout", "conflict", "failure"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			posts, reads := 0, 0
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if serveVersion(w, r) {
					return
				}
				if r.Method == http.MethodPost {
					mu.Lock()
					posts++
					mu.Unlock()
					if r.URL.Path != "/v1/users/otvali_test/disable" || r.Header.Get("If-Match") != testRevision {
						t.Error("incorrect disable request")
					}
					switch mode {
					case "timeout":
						time.Sleep(60 * time.Millisecond)
						writeEnvelope(w, 200, testUser("otvali_test", testRemoteSecret, false, true), testRevision)
					case "conflict":
						writeFailure(w, 409, "revision_conflict")
					default:
						writeFailure(w, 500, "internal_error")
					}
					return
				}
				mu.Lock()
				reads++
				mu.Unlock()
				writeEnvelope(w, 200, testUser("otvali_test", testRemoteSecret, false, true), testRevision)
			}, 25*time.Millisecond)
			_, err := client.DisableUser(context.Background(), "otvali_test")
			want := ErrUnavailable
			if mode == "conflict" {
				want = ErrConflict
			}
			if !errors.Is(err, want) {
				t.Fatalf("unacknowledged disable accepted: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if posts != 1 || reads != 2 {
				t.Fatalf("expected one mutation and one reconciliation: posts=%d reads=%d", posts, reads)
			}
		})
	}
}

func TestClientRejectsVersionBeforeCredentialMutation(t *testing.T) {
	requests := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeEnvelope(w, 200, map[string]any{"version": "3.5.5", "config_hash": testRevision}, testRevision)
	}, 0)
	_, err := client.CreateUser(context.Background(), "otvali_test", testRemoteSecret)
	if !errors.Is(err, ErrUnavailable) || requests != 1 {
		t.Fatalf("version mismatch not fenced: %v", err)
	}
}

func TestClientRejectsRedirectWithoutCredentialLeak(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"?secret=do-not-leak", http.StatusTemporaryRedirect)
	}, 0)
	_, err := client.GetUser(context.Background(), "otvali_test")
	if !errors.Is(err, ErrUnavailable) || reached {
		t.Fatalf("redirect was followed or accepted: %v", err)
	}
	if strings.Contains(fmt.Sprint(err), "secret") || strings.Contains(fmt.Sprint(err), "127.0.0.1") {
		t.Fatal("sensitive error exposed")
	}
}

func TestClientRejectsUnsafeBaseURL(t *testing.T) {
	for _, address := range []string{"https://127.0.0.1:9091", "http://localhost:9091", "http://192.0.2.1:9091", "http://127.0.0.1", "http://127.0.0.1:0", "http://user:pass@127.0.0.1:9091", "http://127.0.0.1:9091/", "http://127.0.0.1:9091?x=1", "http://127.0.0.1:9091#x"} {
		if _, err := NewClient(address, "Bearer test-only", nil); err == nil {
			t.Errorf("accepted unsafe address: %s", address)
		}
	}
	if _, err := NewClient("http://[::1]:9091", "Bearer test-only", nil); err != nil {
		t.Fatal("IPv6 loopback rejected")
	}
	if _, err := NewClient("http://127.0.0.1:9091", "Bearer test\r\nx-header: leak", nil); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestClientFailsClosedForMalformedProviderData(t *testing.T) {
	valid := testUser("otvali_test", testRemoteSecret, true, true)
	encoded, _ := json.Marshal(map[string]any{"ok": true, "data": valid, "revision": testRevision})
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"missing flags", 200, `{"ok":true,"revision":"` + testRevision + `","data":{"username":"otvali_test","links":{"tls":[]}}}`},
		{"wrong name", 200, strings.Replace(string(encoded), "otvali_test", "someone_else", 1)},
		{"wrong revision", 200, strings.Replace(string(encoded), testRevision, "unknown", 1)},
		{"extra document", 200, string(encoded) + `{}`},
		{"error contains secret", 500, `{"ok":false,"error":{"code":"internal_error","message":"` + testRemoteSecret + `"}}`},
		{"oversized", 200, strings.Repeat(" ", maxResponseSize+1)},
		{"unknown success", 203, string(encoded)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if serveVersion(w, r) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}, 0)
			_, err := client.GetUser(context.Background(), "otvali_test")
			if !errors.Is(err, ErrUnavailable) || strings.Contains(fmt.Sprint(err), testRemoteSecret) {
				t.Fatalf("unsafe result: %v", err)
			}
		})
	}
}

func TestClientRevisionConflictNeverRetriesMutation(t *testing.T) {
	posts := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if serveVersion(w, r) {
			return
		}
		if r.Method == http.MethodPost {
			posts++
			writeFailure(w, 409, "revision_conflict")
			return
		}
		writeFailure(w, 404, "not_found")
	}, 0)
	_, err := client.CreateUser(context.Background(), "otvali_test", testRemoteSecret)
	if !errors.Is(err, ErrConflict) || posts != 1 {
		t.Fatalf("conflict retry: posts=%d err=%v", posts, err)
	}
}
