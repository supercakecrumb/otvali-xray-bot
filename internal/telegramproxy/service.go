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

type Service struct {
	config    Config
	store     Store
	providers map[string]API
	endpoints map[string]EndpointConfig
	aead      cipher.AEAD
	locks     [64]sync.Mutex
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
	s := &Service{config: c, store: store, providers: map[string]API{}, endpoints: map[string]EndpointConfig{}}
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
		remote, err := s.providers[ep.ProviderID].GetUser(ctx, g.ExternalID)
		if errors.Is(err, ErrNotFound) {
			if g.State == StateActive {
				// A disk-backed provider 404 does not prove runtime admission
				// has stopped. Quarantine without recreating or clearing the
				// deletion guard; an operator must reconcile the provider.
				g.State, g.Generation = StateRevoking, g.Generation+1
				outcome = ErrInactive
				return nil
			}
			remote, err = s.providers[ep.ProviderID].CreateUser(ctx, g.ExternalID, secret)
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
