// Package telegramproxy issues private, individually revocable Telegram proxy
// credentials. It never provisions VPN accounts or changes existing VPN keys.
package telegramproxy

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotEligible = errors.New("telegram proxy access unavailable")
	ErrNotFound    = errors.New("telegram proxy not found")
	ErrInactive    = errors.New("telegram proxy access disabled")
	ErrConflict    = errors.New("telegram proxy state conflict")
	ErrUnavailable = errors.New("telegram proxy service unavailable")
)

const (
	StatePending  = "pending"
	StateActive   = "active"
	StateRevoking = "revoking"
	StateRevoked  = "revoked"
)

// Endpoint is deliberately metadata only. API addresses and proxy credentials
// never belong in catalog responses, logs, callback data, or activity records.
type Endpoint struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Primary  bool   `json:"primary"`
}

type Credential struct {
	EndpointID string `json:"endpoint_id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
}

// Grant contains encrypted key material, not plaintext credentials. Identity,
// provider binding and ciphertext are immutable after PrepareProxyGrant.
// TelegramID remains on a revoked tombstone if the users row is later deleted.
type Grant struct {
	UserID          int64
	TelegramID      int64
	EndpointID      string
	ProviderID      string
	BindingHash     string
	ExternalID      string
	EncryptedSecret []byte
	Generation      int64
	State           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Store serializes mutations across processes. Prepare commits a durable
// pending grant before any external request. WithProxyGrant holds its row lock
// while the callback reconciles the provider; successful updates use CAS.
// MarkProxyRevoking commits the revocation fence before remote disable.
type Store interface {
	ProxyUserID(context.Context, int64) (int64, error)
	GetProxyGrant(context.Context, int64, string) (Grant, bool, error)
	PrepareProxyGrant(context.Context, Grant) (Grant, error)
	WithProxyGrant(context.Context, int64, string, func(*Grant) error) error
	MarkProxyRevoking(context.Context, int64, string) (Grant, error)
	CheckTelegramProxyDisabled(context.Context, int64) error
}

type EndpointConfig struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProviderID string `json:"provider_id"`
	PublicHost string `json:"public_host"`
	PublicPort int    `json:"public_port"`
	TLSDomain  string `json:"tls_domain"`
	Primary    bool   `json:"primary"`
}

type ProviderConfig struct {
	ID            string `json:"id"`
	BaseURL       string `json:"base_url"`
	Authorization string `json:"authorization"`
}

type Config struct {
	Enabled            bool
	SecretKey          []byte
	Endpoints          []EndpointConfig
	Providers          []ProviderConfig
	AllowedTelegramIDs map[int64]struct{}
	AllowAllBound      bool
}
