package miniapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type fakeStore struct {
	user    *database.User
	server  *database.Server
	lookups []int64
	err     error
}

func (f *fakeStore) GetUserByTelegramID(id int64) (*database.User, error) {
	f.lookups = append(f.lookups, id)
	return f.user, f.err
}
func (f *fakeStore) GetServerByID(id int64) (*database.Server, error) {
	if id != 1 {
		return nil, errors.New("unexpected server")
	}
	return f.server, f.err
}

type fakeManaged struct {
	eligible     bool
	issued, read []int64
	err          error
	profiles     []remnawave.ServerKey
}

func (f *fakeManaged) Eligible(context.Context, int64) bool { return f.eligible }
func (f *fakeManaged) Issue(_ context.Context, id int64) (remnawave.Subscription, error) {
	f.issued = append(f.issued, id)
	return remnawave.Subscription{URL: "https://sub.example/api/sub/same-private-account", ExpiresAt: time.Time{}}, f.err
}
func (f *fakeManaged) Keys(_ context.Context, id int64) ([]remnawave.ServerKey, error) {
	f.read = append(f.read, id)
	return f.profiles, f.err
}

type fakeLegacy struct {
	disabled bool
	calls    int
	actor    int64
	email    string
	err      error
}

func (f *fakeLegacy) IsServerDisabled(int64) bool { return f.disabled }
func (f *fakeLegacy) GetUserKeyForTelegramID(_ context.Context, _ *database.Server, email string, id int64) (string, error) {
	f.calls++
	f.email = email
	f.actor = id
	return "vless://00000000-0000-4000-8000-000000000001@legacy.example:443?flow=xtls-rprx-vision", f.err
}
func fixtureService() (*Service, *fakeStore, *fakeManaged, *fakeLegacy) {
	id := int64(42)
	inbound := 2
	store := &fakeStore{user: &database.User{TelegramID: &id, Username: "stored_database_username"}, server: &database.Server{ID: 1, InboundID: &inbound}}
	managed := &fakeManaged{eligible: true, profiles: []remnawave.ServerKey{{ID: "0123456789abcdef", Name: "Current profile", URL: "vless://00000000-0000-4000-8000-000000000001@managed.example:443"}}}
	legacy := &fakeLegacy{}
	return NewService(store, managed, legacy), store, managed, legacy
}
func TestAccountReadNeverIssuesOrBinds(t *testing.T) {
	s, store, managed, legacy := fixtureService()
	account, err := s.Account(context.Background(), 42)
	if err != nil || !account.Managed || !account.LegacyFinland || len(managed.issued)+len(managed.read)+legacy.calls != 0 || len(store.lookups) != 1 || store.lookups[0] != 42 {
		t.Fatal("session must check existing numeric binding without issuance")
	}
	store.user.TelegramID = nil
	if _, err := s.Account(context.Background(), 42); !errors.Is(err, ErrForbidden) {
		t.Fatal("username-only invitation must never auto-bind through API")
	}
}
func TestUnboundOrMismatchedUserCannotIssue(t *testing.T) {
	for _, mode := range []string{"missing", "mismatched", "deleted"} {
		s, store, managed, legacy := fixtureService()
		switch mode {
		case "missing":
			store.user = nil
		case "mismatched":
			id := int64(99)
			store.user.TelegramID = &id
		case "deleted":
			store.err = database.ErrUserNotFound
		}
		if _, err := s.Subscription(context.Background(), 42); !errors.Is(err, ErrForbidden) {
			t.Fatal("wrong identity admitted")
		}
		if _, err := s.Keys(context.Background(), 42); !errors.Is(err, ErrForbidden) {
			t.Fatal("wrong identity listed")
		}
		if _, err := s.Key(context.Background(), 42, "legacy_fi"); !errors.Is(err, ErrForbidden) {
			t.Fatal("wrong identity obtained FI")
		}
		if len(managed.issued)+len(managed.read)+legacy.calls != 0 {
			t.Fatal("unauthorized request reached issuers")
		}
	}
}
func TestSameManagedIssuerAndCurrentAccountSelection(t *testing.T) {
	s, _, managed, legacy := fixtureService()
	sub, err := s.Subscription(context.Background(), 42)
	if err != nil || sub.ExpiresAt != nil || len(managed.issued) != 1 || managed.issued[0] != 42 {
		t.Fatal("subscription must use same numeric issuer and normalized no-expiry")
	}
	keys, err := s.Keys(context.Background(), 42)
	if err != nil || len(keys) != 2 || keys[1].URL != "" {
		t.Fatal("list must contain legacy plus managed metadata only")
	}
	key, err := s.Key(context.Background(), 42, managed.profiles[0].ID)
	if err != nil || key.URL != managed.profiles[0].URL || len(managed.read) != 2 || managed.read[0] != 42 || managed.read[1] != 42 || legacy.calls != 0 {
		t.Fatal("selection must retrieve same account's current profile")
	}
	if _, err := s.Key(context.Background(), 42, "ffffffffffffffff"); !errors.Is(err, ErrNotFound) {
		t.Fatal("another account or stale key ID must not be accepted")
	}
}
func TestLegacyWorksWithManagedProviderFailureAndUsesStoredIdentity(t *testing.T) {
	s, _, managed, legacy := fixtureService()
	managed.err = errors.New("raw-private-api-response")
	keys, err := s.Keys(context.Background(), 42)
	if err != nil || len(keys) != 1 || keys[0].ID != "legacy_fi" {
		t.Fatal("provider failure must leave Finland choice available")
	}
	key, err := s.Key(context.Background(), 42, "legacy_fi")
	if err != nil || key.URL == "" || legacy.actor != 42 || legacy.email != "stored_database_username" || len(managed.issued) != 0 {
		t.Fatal("FI must use stored bound identity independently of provider")
	}
}
func TestLegacyDisabledAndExclusivePolicy(t *testing.T) {
	s, store, _, legacy := fixtureService()
	legacy.disabled = true
	if _, err := s.Key(context.Background(), 42, "legacy_fi"); !errors.Is(err, ErrForbidden) || legacy.calls != 0 {
		t.Fatal("retired server must never issue")
	}
	legacy.disabled = false
	store.server.IsExclusive = true
	if _, err := s.Key(context.Background(), 42, "legacy_fi"); !errors.Is(err, ErrForbidden) {
		t.Fatal("exclusive server requires stored role")
	}
	store.user.ExclusiveAccess = true
	if _, err := s.Key(context.Background(), 42, "legacy_fi"); err != nil {
		t.Fatal("stored exclusive role should retain access")
	}
}
