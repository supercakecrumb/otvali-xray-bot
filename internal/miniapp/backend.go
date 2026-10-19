package miniapp

import (
	"context"
	"net/url"
	"strings"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store interface {
	GetUserByTelegramID(int64) (*database.User, error)
	GetServerByID(int64) (*database.Server, error)
}
type ManagedIssuer interface {
	Eligible(context.Context, int64) bool
	Issue(context.Context, int64) (remnawave.Subscription, error)
	Keys(context.Context, int64) ([]remnawave.ServerKey, error)
}
type LegacyIssuer interface {
	IsServerDisabled(int64) bool
	GetUserKeyForTelegramID(context.Context, *database.Server, string, int64) (string, error)
}
type Service struct {
	store   Store
	managed ManagedIssuer
	legacy  LegacyIssuer
}

func NewService(store Store, managed ManagedIssuer, legacy LegacyIssuer) *Service {
	return &Service{store: store, managed: managed, legacy: legacy}
}

// Reuse the existing lookup methods and pool with the request deadline. SQL
// failures must not echo stored identities or query parameters to the log.
func (s *Service) requestStore(ctx context.Context) Store {
	if db, ok := s.store.(*database.DB); ok && db.Conn != nil {
		return &database.DB{Conn: db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)}
	}
	return s.store
}

func (s *Service) bound(ctx context.Context, id int64) (*database.User, error) {
	if id <= 0 || s.store == nil || ctx.Err() != nil {
		return nil, ErrForbidden
	}
	user, err := s.requestStore(ctx).GetUserByTelegramID(id)
	if err != nil || user == nil || user.TelegramID == nil || *user.TelegramID != id || ctx.Err() != nil {
		return nil, ErrForbidden
	}
	return user, nil
}
func (s *Service) legacyServer(ctx context.Context, user *database.User) (*database.Server, bool) {
	if s.legacy == nil || s.legacy.IsServerDisabled(1) || strings.TrimSpace(user.Username) == "" {
		return nil, false
	}
	server, err := s.requestStore(ctx).GetServerByID(1)
	if err != nil || server == nil || server.ID != 1 || server.InboundID == nil || (server.IsExclusive && !user.ExclusiveAccess && !user.IsAdmin) {
		return nil, false
	}
	return server, true
}
func (s *Service) Account(ctx context.Context, id int64) (Account, error) {
	user, err := s.bound(ctx, id)
	if err != nil {
		return Account{}, err
	}
	_, legacy := s.legacyServer(ctx, user)
	return Account{Managed: s.managed != nil && s.managed.Eligible(ctx, id), LegacyFinland: legacy}, nil
}
func (s *Service) Subscription(ctx context.Context, id int64) (Subscription, error) {
	if _, err := s.bound(ctx, id); err != nil {
		return Subscription{}, err
	}
	if s.managed == nil || !s.managed.Eligible(ctx, id) {
		return Subscription{}, ErrForbidden
	}
	value, err := s.managed.Issue(ctx, id)
	if err != nil {
		return Subscription{}, ErrUnavailable
	}
	u, err := url.Parse(value.URL)
	if err != nil || len(value.URL) > 4096 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return Subscription{}, ErrUnavailable
	}
	result := Subscription{URL: value.URL, TrafficLimitBytes: value.TrafficLimitBytes}
	if !value.ExpiresAt.IsZero() {
		expiry := value.ExpiresAt
		result.ExpiresAt = &expiry
	}
	return result, nil
}
func (s *Service) Keys(ctx context.Context, id int64) ([]Key, error) {
	user, err := s.bound(ctx, id)
	if err != nil {
		return nil, err
	}
	result := []Key{}
	if _, ok := s.legacyServer(ctx, user); ok {
		result = append(result, Key{ID: "legacy_fi", Name: "Finland, Helsinki"})
	}
	if s.managed != nil && s.managed.Eligible(ctx, id) {
		keys, err := s.managed.Keys(ctx, id)
		if err != nil {
			if len(result) > 0 {
				return result, nil
			}
			return nil, ErrUnavailable
		}
		for _, key := range keys {
			if keyID.MatchString(key.ID) {
				result = append(result, Key{ID: key.ID, Name: key.Name})
			}
		}
	}
	return result, nil
}
func (s *Service) Key(ctx context.Context, id int64, selected string) (Key, error) {
	user, err := s.bound(ctx, id)
	if err != nil {
		return Key{}, err
	}
	if selected == "legacy_fi" {
		server, ok := s.legacyServer(ctx, user)
		if !ok {
			return Key{}, ErrForbidden
		}
		raw, err := s.legacy.GetUserKeyForTelegramID(ctx, server, user.Username, id)
		if err != nil || !validKey(raw) {
			return Key{}, ErrUnavailable
		}
		return Key{ID: selected, Name: "Finland, Helsinki", URL: raw}, nil
	}
	if !keyID.MatchString(selected) {
		return Key{}, ErrNotFound
	}
	if s.managed == nil || !s.managed.Eligible(ctx, id) {
		return Key{}, ErrForbidden
	}
	keys, err := s.managed.Keys(ctx, id)
	if err != nil {
		return Key{}, ErrUnavailable
	}
	for _, key := range keys {
		if key.ID == selected && validKey(key.URL) {
			return Key{ID: key.ID, Name: key.Name, URL: key.URL}, nil
		}
	}
	return Key{}, ErrNotFound
}
func validKey(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 4096 && u.Scheme == "vless" && u.User != nil && u.Hostname() != "" && u.Port() != "" && !strings.ContainsAny(raw, "\r\n\x00")
}
