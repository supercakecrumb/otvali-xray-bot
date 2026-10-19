package remnawave

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu                                 sync.Mutex
	mapping                            Mapping
	exists, failSave, failBound, bound bool
}

func (m *memoryStore) IsBoundUser(context.Context, int64) (bool, error) {
	if m.failBound {
		return false, errors.New("private database diagnostic")
	}
	return m.bound, nil
}
func (m *memoryStore) FindGrant(context.Context, int64) (Mapping, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mapping, m.exists, nil
}
func (m *memoryStore) SaveGrant(_ context.Context, mapping Mapping) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSave {
		m.failSave = false
		return errors.New("private mapping diagnostic")
	}
	m.mapping, m.exists = mapping, true
	return nil
}

type memoryAPI struct {
	mu             sync.Mutex
	user           *User
	creates, finds int
	ambiguous      bool
}

func (m *memoryAPI) FindUser(context.Context, string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finds++
	if m.user == nil {
		return User{}, ErrNotFound
	}
	return *m.user, nil
}
func (m *memoryAPI) CreateUser(_ context.Context, r CreateUserRequest) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates++
	id := r.TelegramID
	m.user = &User{ID: 17, Username: r.Username, TelegramID: &id, Tag: r.Tag, Status: r.Status,
		ExpireAt: r.ExpireAt, SubscriptionURL: "https://sub.example/api/sub/private-grant",
		ActiveInternalSquads: []Squad{{UUID: r.ActiveInternalSquads[0]}}}
	if m.ambiguous {
		return User{}, ErrUnavailable
	}
	return *m.user, nil
}

func serviceFixture() (*Service, *memoryAPI, *memoryStore) {
	a, store := &memoryAPI{}, &memoryStore{bound: true}
	c := Config{Enabled: true, AllowedTelegramIDs: map[int64]struct{}{7: {}}, SquadUUID: "squad", AccessDays: 30, SubscriptionOrigin: "https://sub.example"}
	s := NewService(c, a, store)
	s.now = func() time.Time { return time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC) }
	return s, a, store
}

func TestConcurrentRepeatedIssuanceCreatesOneRemoteAccount(t *testing.T) {
	s, a, store := serviceFixture()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, err := s.Issue(context.Background(), 7)
			if err != nil || sub.URL != "https://sub.example/api/sub/private-grant" {
				t.Errorf("issuance failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if a.creates != 1 || !store.exists || store.mapping.RemoteID != 17 {
		t.Fatalf("duplicate or missing grant: creates=%d", a.creates)
	}
	expiry := a.user.ExpireAt
	if _, err := s.Issue(context.Background(), 7); err != nil || !a.user.ExpireAt.Equal(expiry) {
		t.Fatal("retrieval changed expiration")
	}
}

func TestAmbiguousCreateAndMappingWriteFailureRecoverWithoutRecreation(t *testing.T) {
	for _, mode := range []string{"ambiguous-create", "mapping-failure"} {
		t.Run(mode, func(t *testing.T) {
			s, a, store := serviceFixture()
			if mode == "ambiguous-create" {
				a.ambiguous = true
			} else {
				store.failSave = true
			}
			_, err := s.Issue(context.Background(), 7)
			if mode == "mapping-failure" && !errors.Is(err, ErrUnavailable) {
				t.Fatal("mapping failure must withhold delivery")
			}
			if _, err := s.Issue(context.Background(), 7); err != nil {
				t.Fatal(err)
			}
			if a.creates != 1 || !store.exists {
				t.Fatalf("recovery recreated account: %d", a.creates)
			}
		})
	}
}

func TestEligibilityFailsClosedBeforeAnyProviderRequest(t *testing.T) {
	for _, mode := range []string{"disabled", "not-allowlisted", "not-bound", "database-error"} {
		t.Run(mode, func(t *testing.T) {
			s, a, store := serviceFixture()
			switch mode {
			case "disabled":
				s.config.Enabled = false
			case "not-allowlisted":
				delete(s.config.AllowedTelegramIDs, 7)
			case "not-bound":
				store.bound = false
			case "database-error":
				store.failBound = true
			}
			if s.Eligible(context.Background(), 7) {
				t.Fatal("ineligible account accepted")
			}
			if _, err := s.Issue(context.Background(), 7); !errors.Is(err, ErrNotEligible) {
				t.Fatal("expected safe access denial")
			}
			if a.finds != 0 || a.creates != 0 {
				t.Fatal("ineligible account contacted provider")
			}
		})
	}
}

func TestInactiveConflictingAndMalformedGrantsAreNotDelivered(t *testing.T) {
	for _, mode := range []string{"disabled", "expired", "deleted", "owner", "tag", "mapping", "squad", "subscription-host"} {
		t.Run(mode, func(t *testing.T) {
			s, a, store := serviceFixture()
			if _, err := s.Issue(context.Background(), 7); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "disabled":
				a.user.Status = "DISABLED"
			case "expired":
				a.user.ExpireAt = s.now().Add(-time.Hour)
			case "deleted":
				a.user = nil
			case "owner":
				id := int64(8)
				a.user.TelegramID = &id
			case "tag":
				a.user.Tag = "OTHER"
			case "mapping":
				store.mapping.RemoteID = 18
			case "squad":
				a.user.ActiveInternalSquads = nil
			case "subscription-host":
				a.user.SubscriptionURL = "https://untrusted.example/api/sub/private-grant"
			}
			if sub, err := s.Issue(context.Background(), 7); err == nil || sub.URL != "" {
				t.Fatal("unsafe grant delivered")
			}
			if a.creates != 1 {
				t.Fatal("retrieval recreated or reactivated account")
			}
		})
	}
}

func TestDeletionCheckRequiresExplicitProviderDisable(t *testing.T) {
	s, a, _ := serviceFixture()
	if err := s.CheckDisabled(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDisabled(context.Background(), 7); err == nil {
		t.Fatal("active provider access must block bot-row deletion")
	}
	a.user.Status = "DISABLED"
	if err := s.CheckDisabled(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	a.user = nil
	if err := s.CheckDisabled(context.Background(), 7); err != nil {
		t.Fatal("already deleted provider account should allow local cleanup")
	}
	if a.creates != 1 {
		t.Fatal("deletion check changed provider access")
	}
}
