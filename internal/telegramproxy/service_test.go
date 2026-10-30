package telegramproxy

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type memoryStore struct {
	mu     sync.Mutex
	users  map[int64]int64
	grants map[string]Grant
}

func newMemoryStore() *memoryStore {
	return &memoryStore{users: map[int64]int64{101: 1, 102: 2}, grants: map[string]Grant{}}
}
func grantKey(tid int64, ep string) string { return strconv.FormatInt(tid, 10) + ":" + ep }
func cloneGrant(g Grant) Grant {
	g.EncryptedSecret = append([]byte(nil), g.EncryptedSecret...)
	return g
}
func (m *memoryStore) ProxyUserID(_ context.Context, tid int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.users[tid]
	if id == 0 {
		return 0, ErrNotEligible
	}
	return id, nil
}
func (m *memoryStore) GetProxyGrant(_ context.Context, tid int64, ep string) (Grant, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.grants[grantKey(tid, ep)]
	return cloneGrant(g), ok, nil
}
func (m *memoryStore) PrepareProxyGrant(_ context.Context, g Grant) (Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.users[g.TelegramID] != g.UserID {
		return Grant{}, ErrNotEligible
	}
	key := grantKey(g.TelegramID, g.EndpointID)
	if old, ok := m.grants[key]; ok {
		return cloneGrant(old), nil
	}
	m.grants[key] = cloneGrant(g)
	return cloneGrant(g), nil
}
func (m *memoryStore) WithProxyGrant(_ context.Context, tid int64, ep string, fn func(*Grant) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := grantKey(tid, ep)
	g, ok := m.grants[key]
	if !ok {
		return ErrNotFound
	}
	g = cloneGrant(g)
	if err := fn(&g); err != nil {
		return err
	}
	m.grants[key] = cloneGrant(g)
	return nil
}
func (m *memoryStore) MarkProxyRevoking(ctx context.Context, tid int64, ep string) (Grant, error) {
	var result Grant
	err := m.WithProxyGrant(ctx, tid, ep, func(g *Grant) error {
		if g.State == StateActive || g.State == StatePending {
			g.State = StateRevoking
			g.Generation++
		}
		result = cloneGrant(*g)
		return nil
	})
	return result, err
}
func (m *memoryStore) CheckTelegramProxyDisabled(_ context.Context, tid int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.grants {
		if g.TelegramID == tid && g.State != StateRevoked {
			return ErrInactive
		}
	}
	return nil
}

type memoryAPI struct {
	mu                           sync.Mutex
	users                        map[string]RemoteUser
	creates, disables, gets      int
	uncertainCreate, failDisable bool
}

func newMemoryAPI() *memoryAPI { return &memoryAPI{users: map[string]RemoteUser{}} }
func (m *memoryAPI) GetUser(_ context.Context, name string) (RemoteUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	u, ok := m.users[name]
	if !ok {
		return RemoteUser{}, ErrNotFound
	}
	return u, nil
}
func (m *memoryAPI) CreateUser(_ context.Context, name, secret string) (RemoteUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates++
	if old, ok := m.users[name]; ok {
		if old.Secret != secret {
			return RemoteUser{}, ErrConflict
		}
		return old, nil
	}
	u := RemoteUser{Name: name, Secret: secret, Enabled: true, InRuntime: true}
	m.users[name] = u
	if m.uncertainCreate {
		m.uncertainCreate = false
		return RemoteUser{}, ErrUnavailable
	}
	return u, nil
}
func (m *memoryAPI) DisableUser(_ context.Context, name string) (RemoteUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disables++
	if m.failDisable {
		return RemoteUser{}, ErrUnavailable
	}
	u := m.users[name]
	u.Enabled = false
	m.users[name] = u
	return u, nil
}

func fixtureConfig() Config {
	return Config{Enabled: true, SecretKey: []byte("0123456789abcdef0123456789abcdef"), AllowAllBound: true,
		Providers: []ProviderConfig{{ID: "fi", BaseURL: "http://127.0.0.1:9093", Authorization: "Bearer synthetic-fi"}, {ID: "se", BaseURL: "http://127.0.0.1:9093", Authorization: "Bearer synthetic-se"}},
		Endpoints: []EndpointConfig{{ID: "finland", Name: "Finland, Helsinki", ProviderID: "fi", PublicHost: "192.0.2.1", PublicPort: 8445, TLSDomain: "example.org", Primary: true}, {ID: "stockholm", Name: "Sweden, Stockholm", ProviderID: "se", PublicHost: "192.0.2.2", PublicPort: 8445, TLSDomain: "example.org"}, {ID: "msk-finland", Name: "Moscow → Finland", ProviderID: "fi", PublicHost: "192.0.2.3", PublicPort: 10445, TLSDomain: "example.org"}, {ID: "msk-stockholm", Name: "Moscow → Stockholm", ProviderID: "se", PublicHost: "192.0.2.3", PublicPort: 11445, TLSDomain: "example.org"}}}
}
func fixtureService(t *testing.T, store *memoryStore) (*Service, map[string]*memoryAPI) {
	t.Helper()
	s, err := NewService(fixtureConfig(), store, nil)
	if err != nil {
		t.Fatal(err)
	}
	apis := map[string]*memoryAPI{"fi": newMemoryAPI(), "se": newMemoryAPI()}
	for id, api := range apis {
		s.providers[id] = api
	}
	return s, apis
}

func TestServiceStablePrivatePerRouteCredentials(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	list, err := s.List(ctx, 101)
	if err != nil || len(list) != 4 {
		t.Fatalf("catalog: %v %v", list, err)
	}
	secrets := map[string]bool{}
	for _, ep := range list {
		one, err := s.Issue(ctx, 101, ep.ID)
		if err != nil {
			t.Fatal(err)
		}
		two, err := s.Issue(ctx, 101, ep.ID)
		if err != nil || two != one {
			t.Fatal("repeat issue changed credential", err)
		}
		if !ValidCredentialURL(one.URL) || one.EndpointID != ep.ID {
			t.Fatal("invalid credential")
		}
		u, _ := url.Parse(one.URL)
		secret := u.Query().Get("secret")[2:34]
		if secrets[secret] {
			t.Fatal("routes share secret")
		}
		secrets[secret] = true
		g, _, _ := store.GetProxyGrant(ctx, 101, ep.ID)
		if !externalIDPattern.MatchString(g.ExternalID) || g.State != StateActive || g.Generation != 2 {
			t.Fatal("identity or lifecycle invalid")
		}
		if strings.Contains(hex.EncodeToString(g.EncryptedSecret), secret) {
			t.Fatal("plaintext persisted")
		}
	}
	if apis["fi"].creates != 2 || apis["se"].creates != 2 {
		t.Fatal("unexpected remote creation")
	}
	other, err := s.Issue(ctx, 102, "finland")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(other.URL)
	if secrets[u.Query().Get("secret")[2:34]] {
		t.Fatal("users share credentials")
	}
}

func TestServiceAuthorizationAndAllowlist(t *testing.T) {
	s, apis := fixtureService(t, newMemoryStore())
	ctx := context.Background()
	for _, id := range []int64{-1, 0, 999} {
		if _, err := s.Issue(ctx, id, "finland"); !errors.Is(err, ErrNotEligible) {
			t.Fatal(err)
		}
	}
	if _, err := s.Issue(ctx, 101, "https://example.org"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	s.config.AllowAllBound = false
	s.config.AllowedTelegramIDs = map[int64]struct{}{102: {}}
	if s.Eligible(ctx, 101) {
		t.Fatal("cohort bypass")
	}
	if _, err := s.List(ctx, 101); !errors.Is(err, ErrNotEligible) {
		t.Fatal(err)
	}
	if apis["fi"].creates+apis["fi"].gets != 0 {
		t.Fatal("unauthorized provider request")
	}
}

func TestServiceUncertainCreateReconcilesDurablePending(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	apis["fi"].uncertainCreate = true
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	pending, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if pending.State != StatePending {
		t.Fatal(pending.State)
	}
	// A fresh process shares the committed pending identity and ciphertext.
	restarted, _ := fixtureService(t, store)
	restarted.providers = s.providers
	if _, err := restarted.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	if apis["fi"].creates != 1 {
		t.Fatal("uncertain mutation repeated")
	}
	active, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if active.ExternalID != pending.ExternalID || string(active.EncryptedSecret) != string(pending.EncryptedSecret) {
		t.Fatal("pending credential replaced")
	}
}

func TestServiceConcurrentInstancesIssueOnce(t *testing.T) {
	store := newMemoryStore()
	a, apis := fixtureService(t, store)
	b, _ := fixtureService(t, store)
	b.providers = a.providers
	var wg sync.WaitGroup
	results := make(chan Credential, 20)
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			c, e := s.Issue(context.Background(), 101, "finland")
			if e != nil {
				failures <- e
			} else {
				results <- c
			}
		}(i)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var first Credential
	for c := range results {
		if first.URL == "" {
			first = c
		}
		if c != first {
			t.Fatal("racing issue changed credential")
		}
	}
	if apis["fi"].creates != 1 {
		t.Fatal("duplicate remote account")
	}
}

func TestServiceRevocationFenceAndImmutableTombstone(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	if _, err := s.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	apis["fi"].failDisable = true
	if err := s.Revoke(ctx, 101, "finland"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StateRevoking {
		t.Fatal("revocation fence not committed")
	}
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
	if err := s.CheckDisabled(ctx, 101); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
	apis["fi"].failDisable = false
	if err := s.Revoke(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDisabled(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	// Reinviting the same numeric Telegram account cannot resurrect old access.
	store.users[101] = 17
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
	if apis["fi"].creates != 1 {
		t.Fatal("revoked access recreated")
	}
}

func TestServiceRemoteRemovalNeverRecreatesActiveGrant(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	if _, err := s.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	delete(apis["fi"].users, g.ExternalID)
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
	g, _, _ = store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StateRevoking {
		t.Fatal("remote deletion not fenced")
	}
	if err := s.Revoke(ctx, 101, "finland"); !errors.Is(err, ErrConflict) {
		t.Fatal("disk404 incorrectly proved runtime revocation", err)
	}
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
	if apis["fi"].creates != 1 {
		t.Fatal("remote deletion recreated")
	}
}

func TestServiceDiskDisabledRequiresAcknowledgedRuntimeFence(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	if _, err := s.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	g, _, _ := store.GetProxyGrant(ctx, 101, "finland")
	remote := apis["fi"].users[g.ExternalID]
	remote.Enabled = false // Simulate config changed without runtime staging.
	apis["fi"].users[g.ExternalID] = remote
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrInactive) {
		t.Fatal(err)
	}
	g, _, _ = store.GetProxyGrant(ctx, 101, "finland")
	if g.State != StateRevoking {
		t.Fatal("disk flag incorrectly cleared revocation guard")
	}
	apis["fi"].failDisable = true
	if err := s.Revoke(ctx, 101, "finland"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := s.CheckDisabled(ctx, 101); !errors.Is(err, ErrInactive) {
		t.Fatal("failed fence allowed deletion", err)
	}
	apis["fi"].failDisable = false
	if err := s.Revoke(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	if apis["fi"].disables != 2 {
		t.Fatal("disabled config skipped runtime fencing")
	}
	if err := s.CheckDisabled(ctx, 101); err != nil {
		t.Fatal(err)
	}
}

func TestServiceTamperAndConfigDriftFailClosed(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	s, apis := fixtureService(t, store)
	if _, err := s.Issue(ctx, 101, "finland"); err != nil {
		t.Fatal(err)
	}
	key := grantKey(101, "finland")
	old := cloneGrant(store.grants[key])
	g := cloneGrant(old)
	g.EncryptedSecret[4] ^= 1
	store.grants[key] = g
	gets := apis["fi"].gets
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if apis["fi"].gets != gets {
		t.Fatal("tampered secret reached provider")
	}
	store.grants[key] = old
	ep := s.endpoints["finland"]
	ep.PublicPort++
	s.endpoints["finland"] = ep
	if _, err := s.Issue(ctx, 101, "finland"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if apis["fi"].gets != gets {
		t.Fatal("config drift reached provider")
	}
}

func TestCredentialURLStrictValidation(t *testing.T) {
	ep := fixtureConfig().Endpoints[0]
	good, err := credentialURL(ep, "00112233445566778899aabbccddeeff")
	if err != nil || !ValidCredentialURL(good) {
		t.Fatal(err)
	}
	mutations := []func(*url.URL){func(u *url.URL) { u.Host = "t.me.evil.example" }, func(u *url.URL) { u.Scheme = "javascript" }, func(u *url.URL) { u.User = url.User("me") }, func(u *url.URL) { u.Fragment = "x" }, func(u *url.URL) { u.Path = "/socks" }, func(u *url.URL) { q := u.Query(); q.Add("server", "192.0.2.5"); u.RawQuery = q.Encode() }, func(u *url.URL) { q := u.Query(); q.Set("server", "127.0.0.1"); u.RawQuery = q.Encode() }, func(u *url.URL) { q := u.Query(); q.Set("port", "0"); u.RawQuery = q.Encode() }, func(u *url.URL) { q := u.Query(); q.Set("port", "08445"); u.RawQuery = q.Encode() }, func(u *url.URL) {
		q := u.Query()
		q.Set("secret", "dd00112233445566778899aabbccddeeff")
		u.RawQuery = q.Encode()
	}, func(u *url.URL) { q := u.Query(); q.Set("extra", "1"); u.RawQuery = q.Encode() }}
	for i, mutate := range mutations {
		u, _ := url.Parse(good)
		mutate(u)
		if ValidCredentialURL(u.String()) {
			t.Fatalf("accepted mutation %d", i)
		}
	}
}

func TestConfigOnlyApprovedEndpointBindings(t *testing.T) {
	if err := ValidateConfig(fixtureConfig()); err != nil {
		t.Fatal("separate SSH clients may use identical loopback URLs", err)
	}
	changes := []func(*Config){func(c *Config) { c.Endpoints[2].ProviderID = "se"; c.Endpoints[3].ProviderID = "fi" }, func(c *Config) { c.Endpoints[1].ID = "finland" }, func(c *Config) { c.Endpoints[1].Primary = true }, func(c *Config) { c.Providers[0].BaseURL = "http://provider.example:9093" }, func(c *Config) { c.Providers[0].BaseURL += "/" }, func(c *Config) { c.SecretKey = c.SecretKey[:16] }, func(c *Config) { c.Endpoints[0].PublicHost = "127.0.0.1" }}
	for i, change := range changes {
		c := fixtureConfig()
		change(&c)
		if ValidateConfig(c) == nil {
			t.Fatalf("accepted invalid config %d", i)
		}
	}
}
