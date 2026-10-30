package telegramproxy

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Telemt persists a new user first and admits it to runtime asynchronously,
// so a just-created user reports in_runtime=false for a few seconds. Issue
// waits for admission with a short read-only backoff inside its deadline.
const (
	runtimeWait      = 8 * time.Second
	runtimePollStart = 250 * time.Millisecond
	runtimePollMax   = time.Second
	issueTimeout     = 25 * time.Second
)

// errNotAdmitted marks a matching, enabled provider user that is not yet in
// runtime. It stays inside this package: Issue and IssueAll wait and re-check,
// then report ErrUnavailable if admission still has not happened.
var errNotAdmitted = fmt.Errorf("%w: awaiting runtime admission", ErrUnavailable)

type Service struct {
	config    Config
	store     Store
	providers map[string]API
	endpoints map[string]EndpointConfig
	aead      cipher.AEAD
	// locks serialize one user's issuance and revocation in this process.
	// Each is a one-slot semaphore so waiting respects the caller's context.
	locks [64]chan struct{}

	runtimeWait, runtimePollStart, runtimePollMax time.Duration
}

func NewService(c Config, store Store, client *http.Client) (*Service, error) {
	clients := map[string]*http.Client{}
	for _, p := range c.Providers {
		clients[p.ID] = client
	}
	return NewServiceWithClients(c, store, clients)
}

// NewServiceWithClients keeps each provider bound to its own SSH transport.
// Identical loopback API URLs are valid when they target different SSH hosts.
func NewServiceWithClients(c Config, store Store, clients map[string]*http.Client) (*Service, error) {
	if err := ValidateConfig(c); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, ErrUnavailable
	}
	s := &Service{config: c, store: store, providers: map[string]API{}, endpoints: map[string]EndpointConfig{}, runtimeWait: runtimeWait, runtimePollStart: runtimePollStart, runtimePollMax: runtimePollMax}
	for i := range s.locks {
		s.locks[i] = make(chan struct{}, 1)
	}
	s.config.SecretKey = append([]byte(nil), c.SecretKey...)
	s.config.Endpoints = append([]EndpointConfig(nil), c.Endpoints...)
	s.config.Providers = nil // API authentication remains solely in the client.
	s.config.AllowedTelegramIDs = map[int64]struct{}{}
	for id := range c.AllowedTelegramIDs {
		s.config.AllowedTelegramIDs[id] = struct{}{}
	}
	if !c.Enabled {
		return s, nil
	}
	block, err := aes.NewCipher(s.config.SecretKey)
	if err != nil {
		return nil, ErrUnavailable
	}
	s.aead, err = cipher.NewGCM(block)
	if err != nil {
		return nil, ErrUnavailable
	}
	for _, p := range c.Providers {
		client, present := clients[p.ID]
		if !present {
			return nil, ErrUnavailable
		}
		api, err := NewClient(p.BaseURL, p.Authorization, client)
		if err != nil {
			return nil, err
		}
		s.providers[p.ID] = api
	}
	for _, ep := range c.Endpoints {
		s.endpoints[ep.ID] = ep
	}
	return s, nil
}

func (s *Service) userID(ctx context.Context, telegramID int64) (int64, error) {
	if s == nil || !s.config.Enabled || telegramID <= 0 {
		return 0, ErrNotEligible
	}
	if _, allowed := s.config.AllowedTelegramIDs[telegramID]; !allowed && !s.config.AllowAllBound {
		return 0, ErrNotEligible
	}
	id, err := s.store.ProxyUserID(ctx, telegramID)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrNotEligible
	}
	return id, nil
}

func (s *Service) Eligible(ctx context.Context, telegramID int64) bool {
	_, err := s.userID(ctx, telegramID)
	return err == nil
}

func (s *Service) List(ctx context.Context, telegramID int64) ([]Endpoint, error) {
	if _, err := s.userID(ctx, telegramID); err != nil {
		return nil, err
	}
	result := make([]Endpoint, 0, len(s.config.Endpoints))
	for _, ep := range s.config.Endpoints {
		result = append(result, Endpoint{ID: ep.ID, Name: ep.Name, Protocol: "mtproto", Primary: ep.Primary})
	}
	return result, nil
}

func grantAAD(g Grant) []byte {
	b, _ := json.Marshal([]any{"telegram-proxy-secret-v1", g.UserID, g.TelegramID, g.EndpointID, g.ProviderID, g.BindingHash, g.ExternalID})
	return b
}

func (s *Service) newGrant(userID, telegramID int64, ep EndpointConfig) (Grant, error) {
	identity, _ := json.Marshal([]any{"telegram-proxy-name-v1", userID, ep.ID})
	h := hmac.New(sha256.New, s.config.SecretKey)
	_, _ = h.Write(identity)
	g := Grant{UserID: userID, TelegramID: telegramID, EndpointID: ep.ID, ProviderID: ep.ProviderID, BindingHash: bindingHash(ep), ExternalID: "otv_" + hex.EncodeToString(h.Sum(nil)[:16]), Generation: 1, State: StatePending}
	plain := make([]byte, 16)
	if _, err := rand.Read(plain); err != nil {
		return Grant{}, ErrUnavailable
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Grant{}, ErrUnavailable
	}
	g.EncryptedSecret = append([]byte{1}, nonce...)
	g.EncryptedSecret = s.aead.Seal(g.EncryptedSecret, nonce, plain, grantAAD(g))
	return g, nil
}

func (s *Service) secret(g Grant) (string, error) {
	n := s.aead.NonceSize()
	if len(g.EncryptedSecret) != 1+n+16+s.aead.Overhead() || g.EncryptedSecret[0] != 1 {
		return "", ErrUnavailable
	}
	plain, err := s.aead.Open(nil, g.EncryptedSecret[1:1+n], g.EncryptedSecret[1+n:], grantAAD(g))
	if err != nil || len(plain) != 16 {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(plain), nil
}

func (s *Service) binding(g Grant, ep EndpointConfig) error {
	if g.EndpointID != ep.ID || g.ProviderID != ep.ProviderID || g.BindingHash != bindingHash(ep) || !externalIDPattern.MatchString(g.ExternalID) || g.Generation < 1 {
		return ErrConflict
	}
	return nil
}

func remoteMatches(remote RemoteUser, g Grant, secret string) bool {
	return remote.Name == g.ExternalID && subtle.ConstantTimeCompare([]byte(remote.Secret), []byte(secret)) == 1
}

// lock acquires the per-user lock or fails when ctx ends first, so neither
// issuance nor an operator's revocation waits past its own deadline.
func (s *Service) lock(ctx context.Context, telegramID int64) (func(), error) {
	slot := s.locks[uint64(telegramID)%uint64(len(s.locks))]
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ErrUnavailable
	}
}

// Issue returns the user's personal link for one route. A user that Telemt
// has created but not yet admitted to runtime is awaited without holding any
// lock, then re-checked under the locks before the grant becomes active.
func (s *Service) Issue(ctx context.Context, telegramID int64, endpointID string) (Credential, error) {
	if _, err := s.userID(ctx, telegramID); err != nil {
		return Credential{}, err
	}
	ep, ok := s.endpoints[endpointID]
	if !ok {
		return Credential{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()
	credential, externalID, err := s.issueLocked(ctx, telegramID, ep)
	if !errors.Is(err, errNotAdmitted) {
		return credential, err
	}
	if err := s.awaitRuntime(ctx, s.providers[ep.ProviderID], externalID); err != nil {
		return Credential{}, err
	}
	credential, _, err = s.issueLocked(ctx, telegramID, ep)
	if errors.Is(err, errNotAdmitted) {
		return Credential{}, ErrUnavailable
	}
	return credential, err
}

// issueLocked performs one guarded issuance attempt under the per-user lock
// and the store's row locks. It never waits for runtime admission: a matching,
// enabled user that is not yet in runtime leaves the grant unchanged and
// returns errNotAdmitted with the grant's non-secret provider user name.
func (s *Service) issueLocked(ctx context.Context, telegramID int64, ep EndpointConfig) (Credential, string, error) {
	unlock, err := s.lock(ctx, telegramID)
	if err != nil {
		return Credential{}, "", err
	}
	defer unlock()
	userID, err := s.userID(ctx, telegramID)
	if err != nil {
		return Credential{}, "", err
	}
	g, found, err := s.store.GetProxyGrant(ctx, telegramID, ep.ID)
	if err != nil {
		return Credential{}, "", err
	}
	if !found {
		g, err = s.newGrant(userID, telegramID, ep)
		if err != nil {
			return Credential{}, "", err
		}
		g, err = s.store.PrepareProxyGrant(ctx, g)
		if err != nil {
			return Credential{}, "", err
		}
	}
	if g.UserID != userID || g.TelegramID != telegramID {
		return Credential{}, "", ErrInactive
	}
	var result Credential
	var outcome error
	var notAdmitted string
	err = s.store.WithProxyGrant(ctx, telegramID, ep.ID, func(g *Grant) error {
		if g.UserID != userID || g.TelegramID != telegramID {
			return ErrInactive
		}
		if err := s.binding(*g, ep); err != nil {
			return err
		}
		if g.State != StatePending && g.State != StateActive {
			return ErrInactive
		}
		secret, err := s.secret(*g)
		if err != nil {
			return err
		}
		api := s.providers[ep.ProviderID]
		remote, err := api.GetUser(ctx, g.ExternalID)
		if errors.Is(err, ErrNotFound) {
			if g.State == StateActive {
				// A disk-backed provider 404 does not prove runtime admission
				// has stopped. Quarantine without recreating or clearing the
				// deletion guard; an operator must reconcile the provider.
				g.State, g.Generation = StateRevoking, g.Generation+1
				outcome = ErrInactive
				return nil
			}
			remote, err = api.CreateUser(ctx, g.ExternalID, secret)
		}
		if err != nil {
			return err
		}
		if !remoteMatches(remote, *g, secret) {
			return ErrConflict
		}
		if !remote.Enabled {
			// Config is disk-backed. Only an acknowledged disable operation
			// proves that Telemt fenced runtime admission as well.
			g.State, g.Generation = StateRevoking, g.Generation+1
			outcome = ErrInactive
			return nil
		}
		if !remote.InRuntime {
			notAdmitted = g.ExternalID
			return errNotAdmitted
		}
		link, err := credentialURL(ep, secret)
		if err != nil {
			return err
		}
		if g.State != StateActive {
			g.State, g.Generation = StateActive, g.Generation+1
		}
		result = Credential{EndpointID: ep.ID, Name: ep.Name, URL: link}
		return nil
	})
	if err != nil {
		// A store may normalize the callback error to ErrUnavailable.
		if notAdmitted != "" && errors.Is(err, ErrUnavailable) {
			return Credential{}, notAdmitted, errNotAdmitted
		}
		return Credential{}, "", err
	}
	if outcome != nil {
		return Credential{}, "", outcome
	}
	return result, "", nil
}

// awaitRuntime polls a provider user that Telemt has not yet admitted to
// runtime. It holds no lock and only reads; it never decides the outcome.
// It returns nil on admission, on any change Issue's locked checks must see
// (disabled or renamed user), or when runtimeWait elapses, so the caller
// re-runs those checks. A canceled context or a failed read (including 404,
// left to the next request's existing rules) returns an error instead.
func (s *Service) awaitRuntime(ctx context.Context, api API, externalID string) error {
	deadline := time.Now().Add(s.runtimeWait)
	delay := s.runtimePollStart
	for {
		wait := time.Until(deadline)
		if wait <= 0 {
			return nil
		}
		if delay < wait {
			wait = delay
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ErrUnavailable
		case <-timer.C:
		}
		remote, err := api.GetUser(ctx, externalID)
		if errors.Is(err, ErrNotFound) {
			return ErrUnavailable
		}
		if err != nil {
			return err
		}
		if remote.InRuntime || !remote.Enabled || remote.Name != externalID {
			return nil
		}
		if delay *= 2; delay > s.runtimePollMax {
			delay = s.runtimePollMax
		}
	}
}

// IssueAll issues every configured route for one user in configuration order.
// Every route goes through the same guarded, locked attempt as Issue, one
// route at a time; IssueAll itself holds no lock. The first pass creates or
// confirms every route without waiting, so Telemt admits new users together.
// The second pass waits only for routes that were not yet in runtime, making
// the first-open delay roughly the slowest admission rather than their sum.
//
// Revoked or disabled routes and permanent conflicts are omitted; other route
// failures are reported by endpoint ID as unavailable for a retry. It fails
// when the user is not eligible; when nothing was issued it returns
// ErrUnavailable if any route may succeed on retry, otherwise ErrConflict if
// any route conflicts, otherwise ErrInactive.
func (s *Service) IssueAll(ctx context.Context, telegramID int64) ([]Credential, []string, error) {
	if _, err := s.userID(ctx, telegramID); err != nil {
		return nil, nil, err
	}
	credentials := make([]Credential, len(s.config.Endpoints))
	errs := make([]error, len(s.config.Endpoints))
	for i, ep := range s.config.Endpoints {
		credentials[i], errs[i] = s.issueWithoutWait(ctx, telegramID, ep.ID)
		if errors.Is(errs[i], ErrNotEligible) {
			return nil, nil, errs[i]
		}
	}
	for i, ep := range s.config.Endpoints {
		if !errors.Is(errs[i], errNotAdmitted) {
			continue
		}
		if ctx.Err() != nil {
			errs[i] = ErrUnavailable
			continue
		}
		credentials[i], errs[i] = s.Issue(ctx, telegramID, ep.ID)
		if errors.Is(errs[i], ErrNotEligible) {
			return nil, nil, errs[i]
		}
	}
	issued := make([]Credential, 0, len(s.config.Endpoints))
	unavailable := []string{}
	conflict := false
	for i, ep := range s.config.Endpoints {
		switch err := errs[i]; {
		case err == nil:
			issued = append(issued, credentials[i])
		case errors.Is(err, ErrInactive):
			// Revoked or disabled routes are omitted, never offered for retry.
		case errors.Is(err, ErrConflict):
			// A binding or provider mismatch needs an operator; a retry
			// cannot fix it, so the route is not offered as unavailable.
			conflict = true
		default:
			unavailable = append(unavailable, ep.ID)
		}
	}
	if len(issued) == 0 {
		switch {
		case len(unavailable) > 0:
			return nil, nil, ErrUnavailable
		case conflict:
			return nil, nil, ErrConflict
		default:
			return nil, nil, ErrInactive
		}
	}
	return issued, unavailable, nil
}

// issueWithoutWait is IssueAll's first pass: one locked attempt per route
// with Issue's per-route deadline and no runtime wait.
func (s *Service) issueWithoutWait(ctx context.Context, telegramID int64, endpointID string) (Credential, error) {
	ep, ok := s.endpoints[endpointID]
	if !ok {
		return Credential{}, ErrNotFound
	}
	if ctx.Err() != nil {
		return Credential{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()
	credential, _, err := s.issueLocked(ctx, telegramID, ep)
	return credential, err
}

// Revoke writes the durable fence before touching the provider. A lost reply
// leaves revoking state, which blocks Issue and user deletion until reconciled.
func (s *Service) Revoke(ctx context.Context, telegramID int64, endpointID string) error {
	if s == nil || !s.config.Enabled || telegramID <= 0 {
		return ErrUnavailable
	}
	ep, ok := s.endpoints[endpointID]
	if !ok {
		return ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, issueTimeout)
	defer cancel()
	unlock, err := s.lock(ctx, telegramID)
	if err != nil {
		return err
	}
	defer unlock()
	g, err := s.store.MarkProxyRevoking(ctx, telegramID, endpointID)
	if err != nil {
		return err
	}
	if g.State == StateRevoked {
		return nil
	}
	return s.store.WithProxyGrant(ctx, telegramID, endpointID, func(g *Grant) error {
		if g.State == StateRevoked {
			return nil
		}
		if g.State != StateRevoking {
			return ErrConflict
		}
		if err := s.binding(*g, ep); err != nil {
			return err
		}
		secret, err := s.secret(*g)
		if err != nil {
			return err
		}
		api := s.providers[ep.ProviderID]
		remote, err := api.GetUser(ctx, g.ExternalID)
		if errors.Is(err, ErrNotFound) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if !remoteMatches(remote, *g, secret) {
			return ErrConflict
		}
		remote, err = api.DisableUser(ctx, g.ExternalID)
		if err != nil {
			return err
		}
		if remote.Enabled || !remoteMatches(remote, *g, secret) {
			return ErrConflict
		}
		g.State, g.Generation = StateRevoked, g.Generation+1
		return nil
	})
}

func (s *Service) CheckDisabled(ctx context.Context, telegramID int64) error {
	if s == nil || s.store == nil {
		return ErrUnavailable
	}
	return s.store.CheckTelegramProxyDisabled(ctx, telegramID)
}
