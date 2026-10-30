package telegramproxy

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServiceRuntimeWaitDefaultsFitIssueDeadline(t *testing.T) {
	s, err := NewService(fixtureConfig(), newMemoryStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.runtimeWait != 8*time.Second || s.runtimePollStart != 250*time.Millisecond || s.runtimePollMax != time.Second {
		t.Fatal("runtime admission wait must use the short production backoff")
	}
}

func TestServiceWaitsForRuntimeAdmissionAfterCreate(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	apis["fi"].runtimeAfter = 3
	credential, err := s.Issue(ctx, 101, "finland")
	if err != nil || !ValidCredentialURL(credential.URL) || credential.EndpointID != "finland" {
		t.Fatalf("first issuance must wait for runtime admission: %v", err)
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StateActive || g.Generation != 2 {
		t.Fatal("admitted grant must become active exactly once")
	}
	// One read before create, two unlocked polls until admission, and one
	// locked re-check before activation.
	if apis["fi"].creates != 1 || apis["fi"].gets != 5 {
		t.Fatalf("expected one create and read-only polling: creates=%d gets=%d", apis["fi"].creates, apis["fi"].gets)
	}
	again, err := s.Issue(ctx, 101, "finland")
	if err != nil || again != credential || apis["fi"].creates != 1 {
		t.Fatal("repeat issuance changed or recreated the credential")
	}
}

func TestServiceRuntimeNeverAdmittedStaysPending(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	s.runtimeWait = 30 * time.Millisecond
	apis["fi"].runtimeAfter = -1
	start := time.Now()
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("runtime wait exceeded its bound")
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StatePending || g.Generation != 1 {
		t.Fatal("unadmitted user must not be activated")
	}
	if apis["fi"].creates != 1 || apis["fi"].gets < 3 {
		t.Fatalf("expected a single create followed by polling: creates=%d gets=%d", apis["fi"].creates, apis["fi"].gets)
	}
	// Telemt finishes applying its config later: the next request reuses the
	// same remote user without another create.
	apis["fi"].runtimeAfter = 0
	for name, u := range apis["fi"].users {
		u.InRuntime = true
		apis["fi"].users[name] = u
		delete(apis["fi"].pending, name)
	}
	if _, err := s.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	if apis["fi"].creates != 1 {
		t.Fatal("pending runtime admission caused a second create")
	}
}

func TestServiceRuntimeWaitAfterExistingUserRead(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	apis["fi"].runtimeAfter = -1
	s.runtimeWait = 20 * time.Millisecond
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	// The second tap finds the user via GetUser and waits there as well.
	for name := range apis["fi"].users {
		apis["fi"].pending[name] = 2
	}
	s.runtimeWait = 200 * time.Millisecond
	if _, err := s.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	if apis["fi"].creates != 1 {
		t.Fatal("existing user was recreated")
	}
}

func TestServiceDisabledDuringRuntimeWaitIsFenced(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	apis["fi"].runtimeAfter = -1
	apis["fi"].onRead = func(u *RemoteUser) { u.Enabled = false }
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StateRevoking || g.Generation != 2 {
		t.Fatal("disabled user during wait must keep the existing revoking path")
	}
	if apis["fi"].creates != 1 || apis["fi"].disables != 0 {
		t.Fatal("waiting must never mutate the provider")
	}
}

func TestServiceMismatchDuringRuntimeWaitConflicts(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	apis["fi"].runtimeAfter = -1
	apis["fi"].onRead = func(u *RemoteUser) {
		u.Secret = "ffffffffffffffffffffffffffffffff"
		u.InRuntime = true
	}
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StatePending {
		t.Fatal("a mismatched provider user must not activate the grant")
	}
}

func TestServiceRuntimeWaitRespectsContextCancel(t *testing.T) {
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	s.runtimeWait, s.runtimePollStart, s.runtimePollMax = 10*time.Second, 50*time.Millisecond, 50*time.Millisecond
	apis["fi"].runtimeAfter = -1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	apis["fi"].onRead = func(*RemoteUser) { cancel() }
	start := time.Now()
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("canceled request kept waiting for runtime admission")
	}
	g, _, _ := store.GetProxyGrant(context.Background(), 101, "finland")
	if g.State != StatePending || apis["fi"].gets > 3 {
		t.Fatal("canceled wait must leave the grant pending without further polling")
	}
}

func TestServiceRevokeIsNotBlockedByRuntimeWait(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	s.runtimeWait, s.runtimePollStart, s.runtimePollMax = 5*time.Second, 5*time.Millisecond, 20*time.Millisecond
	apis["fi"].runtimeAfter = -1
	polling := make(chan struct{}, 1)
	apis["fi"].onRead = func(*RemoteUser) {
		select {
		case polling <- struct{}{}:
		default:
		}
	}
	issued := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := s.Issue(ctx, 101, "finland")
		issued <- err
	}()
	<-polling
	// The operator's revoke gets a short budget: the waiting issuance must
	// hold neither the per-user lock nor the grant row while it polls.
	revokeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.Revoke(revokeCtx, 101, "finland"); err != nil {
		t.Fatal("revoke was blocked by a runtime wait", err)
	}
	if err := <-issued; !errors.Is(err, ErrInactive) {
		t.Fatal("a route revoked during the wait must not be activated", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("issuance kept waiting after the user was disabled")
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StateRevoked || apis["fi"].creates != 1 || apis["fi"].disables != 1 {
		t.Fatal("revocation during the wait must win exactly once")
	}
}

func TestServiceUserLockRespectsContext(t *testing.T) {
	ctx := context.Background()
	s, apis := fixtureService(t, newMemoryStore())
	slot := s.locks[101%len(s.locks)]
	slot <- struct{}{} // another request holds the user's lock
	for name, call := range map[string]func(context.Context) error{
		"issue":  func(c context.Context) error { _, err := s.Issue(c, 101, "finland"); return err },
		"revoke": func(c context.Context) error { return s.Revoke(c, 101, "finland") },
	} {
		short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		start := time.Now()
		err := call(short)
		cancel()
		if !errors.Is(err, ErrUnavailable) || time.Since(start) > time.Second {
			t.Fatalf("%s waited for the lock past its deadline: %v", name, err)
		}
	}
	if apis["fi"].gets+apis["fi"].creates+apis["fi"].disables != 0 {
		t.Fatal("a request without the lock reached the provider")
	}
	<-slot
	if _, err := s.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal("released lock must be usable again", err)
	}
}

func TestServiceIssueAllWaitsForAdmissionsTogether(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	const admission = 250 * time.Millisecond
	s.runtimeWait, s.runtimePollStart, s.runtimePollMax = 2*time.Second, 5*time.Millisecond, 20*time.Millisecond
	for _, api := range apis {
		api.runtimeAfter, api.admitAfter = -1, admission
	}
	start := time.Now()
	credentials, unavailable, err := s.IssueAll(ctx, 101)
	elapsed := time.Since(start)
	if err != nil || len(unavailable) != 0 || len(credentials) != 4 {
		t.Fatalf("every route must be issued on the first open: %v %v", unavailable, err)
	}
	// Waiting route by route would take at least 4*admission.
	if elapsed >= 3*admission {
		t.Fatalf("first open waited for admissions one after another: %v", elapsed)
	}
	var first, last time.Time
	for _, api := range apis {
		for _, created := range api.createdAt {
			if first.IsZero() || created.Before(first) {
				first = created
			}
			if created.After(last) {
				last = created
			}
		}
	}
	if last.Sub(first) >= admission {
		t.Fatal("every route must be created before waiting for any admission")
	}
	for _, c := range credentials {
		if g, _, _ := store.GetProxyGrant(ctx, 101, c.EndpointID); g.State != StateActive {
			t.Fatal("issued route was not activated")
		}
	}
}

func TestServiceIssueAllKeepsConflictsApartFromOutages(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	if _, _, err := s.IssueAll(ctx, 101); err != nil {
		t.Fatal(err)
	}
	original := map[string]map[string]RemoteUser{}
	for id, api := range apis {
		original[id] = map[string]RemoteUser{}
		for name, u := range api.users {
			original[id][name] = u
		}
	}
	mismatch := func(provider string) {
		for name, u := range apis[provider].users {
			u.Secret = "ffffffffffffffffffffffffffffffff"
			apis[provider].users[name] = u
		}
	}
	restore := func() {
		for id, users := range original {
			for name, u := range users {
				apis[id].users[name] = u
			}
			apis[id].failGet = false
		}
	}

	// Every route conflicts: same outcome as single-route issuance.
	mismatch("fi")
	mismatch("se")
	if credentials, unavailable, err := s.IssueAll(ctx, 101); !errors.Is(err, ErrConflict) || credentials != nil || unavailable != nil {
		t.Fatal("an all-conflict result must keep the administrator path", err)
	}
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}

	// Some routes conflict: they are not offered as temporarily unavailable.
	restore()
	mismatch("fi")
	credentials, unavailable, err := s.IssueAll(ctx, 101)
	if err != nil || !reflect.DeepEqual(issuedIDs(credentials), []string{"stockholm", "msk-stockholm"}) || len(unavailable) != 0 || unavailable == nil {
		t.Fatalf("conflicted routes must not get a retry: %v %v %v", issuedIDs(credentials), unavailable, err)
	}

	// Configuration drift is a conflict too.
	restore()
	ep := s.endpoints["finland"]
	ep.PublicPort++
	s.endpoints["finland"] = ep
	credentials, unavailable, err = s.IssueAll(ctx, 101)
	if err != nil || !reflect.DeepEqual(issuedIDs(credentials), []string{"stockholm", "msk-finland", "msk-stockholm"}) || len(unavailable) != 0 {
		t.Fatalf("drifted route must be omitted: %v %v %v", issuedIDs(credentials), unavailable, err)
	}
	ep.PublicPort--
	s.endpoints["finland"] = ep

	// Nothing issued, but an outage may clear: retry stays possible.
	mismatch("fi")
	apis["se"].failGet = true
	if _, _, err := s.IssueAll(ctx, 101); !errors.Is(err, ErrUnavailable) {
		t.Fatal("conflicts mixed with outages must stay retryable", err)
	}
	for _, id := range []string{"finland", "stockholm", "msk-finland", "msk-stockholm"} {
		if g, _, _ := store.GetProxyGrant(ctx, 101, id); g.State != StateActive {
			t.Fatal("a conflict or outage must not change an active grant")
		}
	}
}

func issuedIDs(credentials []Credential) []string {
	ids := []string{}
	for _, c := range credentials {
		ids = append(ids, c.EndpointID)
	}
	return ids
}

func TestServiceIssueAllReturnsEveryRouteInConfigOrder(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	// Production case: every new user is admitted to runtime asynchronously.
	apis["fi"].runtimeAfter, apis["se"].runtimeAfter = 2, 2
	credentials, unavailable, err := s.IssueAll(ctx, 101)
	if err != nil || len(unavailable) != 0 || unavailable == nil {
		t.Fatalf("all routes must issue in one call: %v %v", unavailable, err)
	}
	if !reflect.DeepEqual(issuedIDs(credentials), []string{"finland", "stockholm", "msk-finland", "msk-stockholm"}) {
		t.Fatal("credentials must follow configuration order")
	}
	seen := map[string]bool{}
	for _, c := range credentials {
		if !ValidCredentialURL(c.URL) || seen[c.URL] || c.Name == "" {
			t.Fatal("each route needs its own valid personal link")
		}
		seen[c.URL] = true
		g, _, _ := store.GetProxyGrant(ctx, 101, c.EndpointID)
		if g.State != StateActive {
			t.Fatal("issued route was not activated")
		}
	}
	again, unavailable, err := s.IssueAll(ctx, 101)
	if err != nil || len(unavailable) != 0 || !reflect.DeepEqual(again, credentials) {
		t.Fatal("repeat must return the same credentials")
	}
	if apis["fi"].creates != 2 || apis["se"].creates != 2 {
		t.Fatal("each route must be created exactly once")
	}
}

func TestServiceIssueAllReportsPartialFailure(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	apis["se"].failCreate = true
	credentials, unavailable, err := s.IssueAll(ctx, 101)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(issuedIDs(credentials), []string{"finland", "msk-finland"}) || !reflect.DeepEqual(unavailable, []string{"stockholm", "msk-stockholm"}) {
		t.Fatalf("partial result mismatch: %v %v", issuedIDs(credentials), unavailable)
	}
	for _, id := range unavailable {
		g, _, _ := store.GetProxyGrant(ctx, 101, id)
		if g.State != StatePending {
			t.Fatal("failed route must stay pending for a retry")
		}
	}
	apis["se"].failCreate = false
	credentials, unavailable, err = s.IssueAll(ctx, 101)
	if err != nil || len(credentials) != 4 || len(unavailable) != 0 {
		t.Fatal("refresh must complete the remaining routes")
	}
}

func TestServiceIssueAllOmitsRevokedRoutes(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	if _, _, err := s.IssueAll(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, 101, "stockholm"); err != nil {
		t.Fatal(err)
	}
	credentials, unavailable, err := s.IssueAll(ctx, 101)
	if err != nil || len(unavailable) != 0 || !reflect.DeepEqual(issuedIDs(credentials), []string{"finland", "msk-finland", "msk-stockholm"}) {
		t.Fatal("an individually revoked route must be omitted, not retried")
	}
	for _, id := range []string{"finland", "msk-finland", "msk-stockholm"} {
		if err := s.Revoke(ctx, 101, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.IssueAll(ctx, 101); !errors.Is(err, ErrInactive) {
		t.Fatal("a fully revoked user must receive the inactive error", err)
	}
	if apis["se"].creates != 2 || apis["fi"].creates != 2 {
		t.Fatal("revoked routes were recreated")
	}
}

func TestServiceIssueAllUserLevelFailures(t *testing.T) {
	ctx := context.Background()
	s, apis := fixtureService(t, newMemoryStore())
	for _, id := range []int64{-1, 0, 999} {
		if credentials, unavailable, err := s.IssueAll(ctx, id); !errors.Is(err, ErrNotEligible) || credentials != nil || unavailable != nil {
			t.Fatal("ineligible user must receive only the error", err)
		}
	}
	s.config.AllowAllBound = false
	s.config.AllowedTelegramIDs = map[int64]struct{}{102: {}}
	if _, _, err := s.IssueAll(ctx, 101); !errors.Is(err, ErrNotEligible) {
		t.Fatal(err)
	}
	if apis["fi"].gets+apis["fi"].creates+apis["se"].gets+apis["se"].creates != 0 {
		t.Fatal("ineligible user reached a provider")
	}
	s.config.AllowAllBound = true
	apis["fi"].failCreate, apis["se"].failCreate = true, true
	if credentials, _, err := s.IssueAll(ctx, 101); !errors.Is(err, ErrUnavailable) || credentials != nil {
		t.Fatal("no issued route must be unavailable", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	apis["fi"].failCreate, apis["se"].failCreate = false, false
	before := apis["fi"].gets + apis["se"].gets
	if _, _, err := s.IssueAll(canceled, 101); !errors.Is(err, ErrUnavailable) {
		t.Fatal("canceled request must not issue", err)
	}
	if apis["fi"].gets+apis["se"].gets != before {
		t.Fatal("canceled request reached a provider")
	}
}
