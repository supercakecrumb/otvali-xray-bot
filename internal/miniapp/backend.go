package miniapp

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"github.com/supercakecrumb/otvali-xray-bot/internal/x3ui"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Store interface {
	GetUserByTelegramID(int64) (*database.User, error)
	GetServerByID(int64) (*database.Server, error)
}
type ManagedIssuer interface {
	Eligible(context.Context, int64) bool
	Status(context.Context, int64) (remnawave.AccessState, error)
	Issue(context.Context, int64) (remnawave.Subscription, error)
	Keys(context.Context, int64) ([]remnawave.ServerKey, error)
}
type LegacyIssuer interface {
	IsServerDisabled(int64) bool
	CheckUserKeyForTelegramID(context.Context, *database.Server, string, int64) error
	GetUserKeyForTelegramID(context.Context, *database.Server, string, int64) (string, error)
}
type Service struct {
	store               Store
	managed             ManagedIssuer
	legacy              LegacyIssuer
	telegramProxy       TelegramProxyIssuer
	accountCheckTimeout time.Duration
	now                 func() time.Time
}

func NewService(store Store, managed ManagedIssuer, legacy LegacyIssuer) *Service {
	return &Service{store: store, managed: managed, legacy: legacy, accountCheckTimeout: 8 * time.Second, now: time.Now}
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
	if id <= 0 {
		return nil, ErrForbidden
	}
	if s.store == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	user, err := s.requestStore(ctx).GetUserByTelegramID(id)
	if ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if errors.Is(err, database.ErrUserNotFound) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if user == nil || user.TelegramID == nil || *user.TelegramID != id {
		return nil, ErrForbidden
	}
	return user, nil
}
func (s *Service) legacyServer(ctx context.Context, user *database.User) (*database.Server, bool) {
	server, err := s.legacyAccessServer(ctx, user)
	return server, err == nil
}
func (s *Service) legacyAccessServer(ctx context.Context, user *database.User) (*database.Server, error) {
	if s.legacy == nil || s.legacy.IsServerDisabled(1) || strings.TrimSpace(user.Username) == "" {
		return nil, ErrForbidden
	}
	server, err := s.requestStore(ctx).GetServerByID(1)
	if err != nil || ctx.Err() != nil || server == nil || server.ID != 1 || server.InboundID == nil {
		return nil, ErrUnavailable
	}
	if server.IsExclusive && !user.ExclusiveAccess && !user.IsAdmin {
		return nil, ErrForbidden
	}
	return server, nil
}
func managedError(err error) error {
	switch {
	case errors.Is(err, remnawave.ErrInactive), errors.Is(err, remnawave.ErrConflict):
		return ErrAccessDisabled
	case errors.Is(err, remnawave.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, remnawave.ErrNotEligible):
		return ErrForbidden
	default:
		return ErrUnavailable
	}
}
func legacyError(err error) error {
	switch {
	case errors.Is(err, x3ui.ErrKeyNotFound):
		return ErrNotFound
	case errors.Is(err, x3ui.ErrKeyDisabled), errors.Is(err, x3ui.ErrKeyConflict):
		return ErrAccessDisabled
	default:
		return ErrUnavailable
	}
}
func stateForError(err error) string {
	switch {
	case errors.Is(err, ErrAccessDisabled):
		return "access_disabled"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrForbidden):
		return "not_eligible"
	default:
		return "unavailable"
	}
}
func (s *Service) legacyReady(ctx context.Context, user *database.User) bool {
	server, ok := s.legacyServer(ctx, user)
	return ok && s.legacy.CheckUserKeyForTelegramID(ctx, server, user.Username, *user.TelegramID) == nil
}
func (s *Service) Account(ctx context.Context, id int64) (Account, error) {
	user, err := s.bound(ctx, id)
	if err != nil {
		return Account{}, err
	}
	result := Account{ManagedState: "unavailable", LegacyFinlandState: "unavailable", CheckScope: "account_access"}
	managedCtx, cancelManaged := context.WithTimeout(ctx, s.accountCheckTimeout)
	defer cancelManaged()
	legacyCtx, cancelLegacy := context.WithTimeout(ctx, s.accountCheckTimeout)
	defer cancelLegacy()
	managedDone, legacyDone := managedCtx.Done(), legacyCtx.Done()
	managedResult, legacyResult := make(chan accountCheck, 1), make(chan accountCheck, 1)
	go func(result chan<- accountCheck) { result <- s.managedAccount(managedCtx, id) }(managedResult)
	go func(result chan<- accountCheck) { result <- s.legacyAccount(legacyCtx, user, id) }(legacyResult)
	// Each provider has its own deadline and result. An outage must not erase a
	// successful check of the other provider or cause any issuance action.
	for managedResult != nil || legacyResult != nil {
		select {
		case part := <-managedResult:
			result.Managed, result.ManagedState = part.ready, part.state
			managedResult, managedDone = nil, nil
		case <-managedDone:
			select {
			case part := <-managedResult:
				result.Managed, result.ManagedState = part.ready, part.state
			default:
			}
			managedResult, managedDone = nil, nil
		case part := <-legacyResult:
			result.LegacyFinland, result.LegacyFinlandState = part.ready, part.state
			legacyResult, legacyDone = nil, nil
		case <-legacyDone:
			select {
			case part := <-legacyResult:
				result.LegacyFinland, result.LegacyFinlandState = part.ready, part.state
			default:
			}
			legacyResult, legacyDone = nil, nil
		}
	}
	result.CheckedAt = s.now().UTC()
	return result, nil
}

type accountCheck struct {
	ready bool
	state string
}

func (s *Service) managedAccount(ctx context.Context, id int64) accountCheck {
	result := accountCheck{state: "not_eligible"}
	if s.managed != nil && s.managed.Eligible(ctx, id) {
		state, err := s.managed.Status(ctx, id)
		if err != nil {
			result.state = stateForError(managedError(err))
		} else {
			result.ready, result.state = state == remnawave.AccessReady || state == remnawave.AccessUnprovisioned, string(state)
		}
	}
	if ctx.Err() != nil {
		return accountCheck{state: "unavailable"}
	}
	return result
}

func (s *Service) legacyAccount(ctx context.Context, user *database.User, id int64) accountCheck {
	server, err := s.legacyAccessServer(ctx, user)
	if err != nil {
		return accountCheck{state: stateForError(err)}
	}
	if err := s.legacy.CheckUserKeyForTelegramID(ctx, server, user.Username, id); err != nil {
		return accountCheck{state: stateForError(legacyError(err))}
	}
	if ctx.Err() != nil {
		return accountCheck{state: "unavailable"}
	}
	return accountCheck{ready: true, state: "ready"}
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
		return Subscription{}, managedError(err)
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
	if s.legacyReady(ctx, user) {
		result = append(result, Key{ID: "legacy_fi", Name: "Finland, Helsinki"})
	}
	if s.managed != nil && s.managed.Eligible(ctx, id) {
		keys, err := s.managed.Keys(ctx, id)
		if err != nil {
			if len(result) > 0 {
				return result, nil
			}
			return nil, managedError(err)
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
		server, err := s.legacyAccessServer(ctx, user)
		if err != nil {
			return Key{}, err
		}
		raw, err := s.legacy.GetUserKeyForTelegramID(ctx, server, user.Username, id)
		if err != nil {
			return Key{}, legacyError(err)
		}
		if !validKey(raw) {
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
		return Key{}, managedError(err)
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
