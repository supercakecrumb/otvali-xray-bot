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
	"net/http"
	"sync"
	"time"
)

// Telemt persists a new user first and admits it to runtime asynchronously,
// so a just-created user reports in_runtime=false for a few seconds. Issue
// waits for admission with a short read-only backoff inside its deadline.
const (
	runtimeWait      = 8 * time.Second
	runtimePollStart = 250 * time.Millisecond
	runtimePollMax   = time.Second
)

type Service struct {
	config    Config
	store     Store
	providers map[string]API
	endpoints map[string]EndpointConfig
	aead      cipher.AEAD
	locks     [64]sync.Mutex

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

func (s *Service) Issue(ctx context.Context, telegramID int64, endpointID string) (Credential, error) {
	if _, err := s.userID(ctx, telegramID); err != nil {
		return Credential{}, err
	}
	ep, ok := s.endpoints[endpointID]
	if !ok {
		return Credential{}, ErrNotFound
	}
	lock := &s.locks[uint64(telegramID)%uint64(len(s.locks))]
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	userID, err := s.userID(ctx, telegramID)
	if err != nil {
		return Credential{}, err
	}
	g, found, err := s.store.GetProxyGrant(ctx, telegramID, endpointID)
	if err != nil {
		return Credential{}, err
	}
	if !found {
		g, err = s.newGrant(userID, telegramID, ep)
		if err != nil {
			return Credential{}, err
		}
		g, err = s.store.PrepareProxyGrant(ctx, g)
		if err != nil {
			return Credential{}, err
		}
	}
	if g.UserID != userID || g.TelegramID != telegramID {
		return Credential{}, ErrInactive
	}
	var result Credential
	var outcome error
	err = s.store.WithProxyGrant(ctx, telegramID, endpointID, func(g *Grant) error {
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
		remote, err = s.awaitRuntime(ctx, api, *g, secret, remote)
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
			return ErrUnavailable
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
		return Credential{}, err
	}
	if outcome != nil {
		return Credential{}, outcome
	}
	return result, nil
}

// awaitRuntime polls a matching, enabled user that Telemt has not yet admitted
// to runtime. It only reads: every observation returns to Issue's existing
// checks, so a mismatch stays a conflict and a disabled user is fenced. The
// grant row stays locked, and the wait is bounded by runtimeWait and ctx.
func (s *Service) awaitRuntime(ctx context.Context, api API, g Grant, secret string, remote RemoteUser) (RemoteUser, error) {
	deadline := time.Now().Add(s.runtimeWait)
	delay := s.runtimePollStart
	for remoteMatches(remote, g, secret) && remote.Enabled && !remote.InRuntime {
		wait := time.Until(deadline)
		if wait <= 0 {
			break
		}
		if delay < wait {
			wait = delay
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return RemoteUser{}, ErrUnavailable
		case <-timer.C:
		}
		next, err := api.GetUser(ctx, g.ExternalID)
		if errors.Is(err, ErrNotFound) {
			// Leave the grant unchanged; the next Issue applies the existing
			// pending/active 404 rules instead of acting on a mid-wait read.
			return RemoteUser{}, ErrUnavailable
		}
		if err != nil {
			return RemoteUser{}, err
		}
		remote = next
		if delay *= 2; delay > s.runtimePollMax {
			delay = s.runtimePollMax
		}
	}
	return remote, nil
}

// IssueAll issues every configured route for one user in configuration order.
// Each route goes through Issue, its per-user lock and all grant guards; the
// calls are sequential and IssueAll itself holds no lock. Revoked or disabled
// routes are omitted. Other route failures are reported by endpoint ID as
// unavailable. It fails when the user is not eligible, when every route is
// inactive (ErrInactive), or when no route could be issued (ErrUnavailable).
func (s *Service) IssueAll(ctx context.Context, telegramID int64) ([]Credential, []string, error) {
	if _, err := s.userID(ctx, telegramID); err != nil {
		return nil, nil, err
	}
	issued := make([]Credential, 0, len(s.config.Endpoints))
	unavailable := []string{}
	for _, ep := range s.config.Endpoints {
		if ctx.Err() != nil {
			unavailable = append(unavailable, ep.ID)
			continue
		}
		credential, err := s.Issue(ctx, telegramID, ep.ID)
		switch {
		case err == nil:
			issued = append(issued, credential)
		case errors.Is(err, ErrNotEligible):
			return nil, nil, err
		case errors.Is(err, ErrInactive):
			// Revoked or disabled routes are omitted, never offered for retry.
		default:
			unavailable = append(unavailable, ep.ID)
		}
	}
	if len(issued) == 0 {
		if len(unavailable) == 0 {
			return nil, nil, ErrInactive
		}
		return nil, nil, ErrUnavailable
	}
	return issued, unavailable, nil
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
	lock := &s.locks[uint64(telegramID)%uint64(len(s.locks))]
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
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
