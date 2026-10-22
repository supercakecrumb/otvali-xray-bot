package miniapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

func TestLookupFailureIsRecoverableWithoutGrantingAccess(t *testing.T) {
	for _, mode := range []string{"database_failure", "not_found", "missing_user", "wrong_owner", "cancelled", "cancelled_during_lookup"} {
		for _, action := range []string{"session", "subscription", "keys", "key"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				s, store, managed, legacy := fixtureService()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				want := http.StatusForbidden
				switch mode {
				case "database_failure":
					store.err, want = errors.New("private-database-details"), http.StatusServiceUnavailable
				case "not_found":
					store.err = fmt.Errorf("lookup: %w", database.ErrUserNotFound)
				case "missing_user":
					store.user = nil
				case "wrong_owner":
					other := int64(99)
					store.user.TelegramID = &other
				case "cancelled":
					cancel()
					want = http.StatusServiceUnavailable
				case "cancelled_during_lookup":
					store.afterLookup, want = cancel, http.StatusServiceUnavailable
				}
				h, _ := newTestHandler(t)
				h.backend = s
				body := "{}"
				if action == "key" {
					body = `{"id":"legacy_fi"}`
				}
				response := request(h, "/api/miniapp/"+action, body, func(r *http.Request) { *r = *r.WithContext(ctx) })
				code := "forbidden"
				if want == http.StatusServiceUnavailable {
					code = "unavailable"
				}
				if response.Code != want || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) || strings.Contains(response.Body.String(), "private-database-details") || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("lookup error lost its safe, recoverable HTTP classification")
				}
				if len(managed.issued)+len(managed.read)+legacy.calls != 0 {
					t.Fatal("a failed numeric ownership lookup reached credential issuance")
				}
			})
		}
	}
}
