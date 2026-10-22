package miniapp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type waitingManagedStatus struct {
	*fakeManaged
	started, finished chan struct{}
}

func (f *waitingManagedStatus) Status(ctx context.Context, _ int64) (remnawave.AccessState, error) {
	close(f.started)
	<-ctx.Done()
	close(f.finished)
	return "", ctx.Err()
}

type waitingLegacyStatus struct {
	*fakeLegacy
	finished chan struct{}
}

type lateManagedStatus struct {
	*fakeManaged
	release, finished chan struct{}
}

func (f *lateManagedStatus) Status(context.Context, int64) (remnawave.AccessState, error) {
	<-f.release
	defer close(f.finished)
	return remnawave.AccessReady, nil
}

func TestLateProviderCannotBlockCallerOrOverwriteOtherProvider(t *testing.T) {
	s, _, managed, _ := fixtureService()
	late := &lateManagedStatus{fakeManaged: managed, release: make(chan struct{}), finished: make(chan struct{})}
	s.managed, s.accountCheckTimeout = late, 50*time.Millisecond
	released := false
	defer func() {
		if !released {
			close(late.release)
		}
	}()
	start := time.Now()
	account, err := s.Account(context.Background(), 42)
	if err != nil || time.Since(start) > time.Second || !account.LegacyFinland || account.LegacyFinlandState != "ready" || account.Managed || account.ManagedState != "unavailable" {
		t.Fatal("a late provider must not block or erase a successful independent check")
	}
	close(late.release)
	released = true
	select {
	case <-late.finished:
	case <-time.After(time.Second):
		t.Fatal("released provider did not finish")
	}
	if account.Managed || account.ManagedState != "unavailable" {
		t.Fatal("late readiness must not change the completed response")
	}
}

func (f *waitingLegacyStatus) CheckUserKeyForTelegramID(ctx context.Context, _ *database.Server, _ string, _ int64) error {
	<-ctx.Done()
	close(f.finished)
	return ctx.Err()
}

func TestAccountProviderDeadlinesPreserveOtherReadyAccess(t *testing.T) {
	for _, slow := range []string{"managed", "legacy"} {
		t.Run(slow, func(t *testing.T) {
			s, _, managed, legacy := fixtureService()
			s.accountCheckTimeout = 100 * time.Millisecond
			finished := make(chan struct{})
			if slow == "managed" {
				s.managed = &waitingManagedStatus{fakeManaged: managed, started: make(chan struct{}), finished: finished}
			} else {
				s.legacy = &waitingLegacyStatus{fakeLegacy: legacy, finished: finished}
			}
			start := time.Now()
			account, err := s.Account(context.Background(), 42)
			if err != nil || time.Since(start) > time.Second {
				t.Fatal("account check did not respect its independent bounded checks")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("provider work survived its request deadline")
			}
			if slow == "managed" && (account.Managed || account.ManagedState != "unavailable" || !account.LegacyFinland || account.LegacyFinlandState != "ready") {
				t.Fatal("managed timeout erased current legacy Finland readiness")
			}
			if slow == "legacy" && (!account.Managed || account.ManagedState != "ready" || account.LegacyFinland || account.LegacyFinlandState != "unavailable") {
				t.Fatal("legacy timeout erased current managed readiness")
			}
			if len(managed.issued)+len(managed.read)+legacy.calls != 0 {
				t.Fatal("readiness checks must not issue or retrieve credentials")
			}
		})
	}
}

func TestAccountCancellationStopsProviderReads(t *testing.T) {
	s, _, managed, legacy := fixtureService()
	waiting := &waitingManagedStatus{fakeManaged: managed, started: make(chan struct{}), finished: make(chan struct{})}
	s.managed = waiting
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Account, 1)
	go func() { account, _ := s.Account(ctx, 42); done <- account }()
	select {
	case <-waiting.started:
	case <-time.After(time.Second):
		t.Fatal("provider check did not start")
	}
	cancel()
	select {
	case account := <-done:
		if account.Managed || account.ManagedState != "unavailable" {
			t.Fatal("cancelled check must not claim ready or deny eligibility")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled account check did not stop")
	}
	select {
	case <-waiting.finished:
	case <-time.After(time.Second):
		t.Fatal("provider read did not observe parent cancellation")
	}
	if len(managed.issued)+len(managed.read)+legacy.calls != 0 {
		t.Fatal("cancellation must not trigger issuance or fallback provisioning")
	}
}

func TestSessionMetadataIsUTCAndContainsNoCredentials(t *testing.T) {
	s, _, managed, legacy := fixtureService()
	s.now = func() time.Time { return time.Date(2026, 10, 22, 21, 30, 0, 0, time.FixedZone("MSK", 3*60*60)) }
	h, _ := newTestHandler(t)
	h.backend = s
	response := request(h, "/api/miniapp/session", "{}", nil)
	var body struct {
		Account struct {
			CheckedAt  string `json:"checked_at"`
			CheckScope string `json:"check_scope"`
		} `json:"account"`
	}
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != 200 || body.Account.CheckedAt != "2026-10-22T18:30:00Z" || body.Account.CheckScope != "account_access" {
		t.Fatal("session must expose a current UTC account-access check")
	}
	var data map[string]map[string]json.RawMessage
	json.Unmarshal(response.Body.Bytes(), &data)
	for _, forbidden := range []string{"url", "uuid", "telegram_id", "token", "username"} {
		if _, exists := data["account"][forbidden]; exists {
			t.Fatal("readiness response contains identity or credential material")
		}
	}
	if len(managed.issued)+len(managed.read)+legacy.calls != 0 {
		t.Fatal("metadata check must not provision")
	}
}

func TestLegacyConfigurationFailureIsUnavailableNotDenied(t *testing.T) {
	s, store, _, legacy := fixtureService()
	store.serverErr = errors.New("private-database-details")
	account, err := s.Account(context.Background(), 42)
	if err != nil || !account.Managed || account.LegacyFinlandState != "unavailable" || account.LegacyFinland {
		t.Fatal("legacy configuration failure must preserve managed readiness and remain recoverable")
	}
	if _, err := s.Key(context.Background(), 42, "legacy_fi"); !errors.Is(err, ErrUnavailable) || legacy.calls != 0 {
		t.Fatal("legacy lookup failure must not issue or masquerade as denied access")
	}
}
