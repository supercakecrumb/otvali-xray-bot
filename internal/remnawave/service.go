package remnawave

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const accountTag = "OTVALI_STO"

// Mapping stores identity only. Subscription links remain in Remnawave.
type Mapping struct {
	TelegramID int64
	RemoteID   int64
	Username   string
}

type Store interface {
	IsBoundUser(context.Context, int64) (bool, error)
	FindGrant(context.Context, int64) (Mapping, bool, error)
	SaveGrant(context.Context, Mapping) error
}

type Subscription struct {
	URL               string
	ExpiresAt         time.Time
	TrafficLimitBytes int64
}

type Service struct {
	config Config
	api    API
	store  Store
	locks  [64]sync.Mutex
	now    func() time.Time
}

func NewService(c Config, api API, store Store) *Service {
	allowed := make(map[int64]struct{}, len(c.AllowedTelegramIDs))
	for id := range c.AllowedTelegramIDs {
		allowed[id] = struct{}{}
	}
	c.AllowedTelegramIDs = allowed
	return &Service{config: c, api: api, store: store, now: time.Now}
}

func (s *Service) Eligible(ctx context.Context, telegramID int64) bool {
	if s == nil || !s.config.Enabled || s.api == nil || s.store == nil || telegramID <= 0 {
		return false
	}
	if _, ok := s.config.AllowedTelegramIDs[telegramID]; !ok {
		return false
	}
	bound, err := s.store.IsBoundUser(ctx, telegramID)
	return err == nil && bound
}

func (s *Service) Issue(ctx context.Context, telegramID int64) (Subscription, error) {
	if !s.Eligible(ctx, telegramID) {
		return Subscription{}, ErrNotEligible
	}
	lock := &s.locks[uint64(telegramID)%uint64(len(s.locks))]
	lock.Lock()
	defer lock.Unlock()
	if !s.Eligible(ctx, telegramID) || ctx.Err() != nil {
		return Subscription{}, ErrNotEligible
	}
	mapping, exists, err := s.store.FindGrant(ctx, telegramID)
	if err != nil {
		return Subscription{}, ErrUnavailable
	}
	username := "otvali_tg_" + strconv.FormatInt(telegramID, 10)
	if exists && (mapping.TelegramID != telegramID || mapping.Username != username || mapping.RemoteID <= 0) {
		return Subscription{}, ErrConflict
	}
	user, err := s.api.FindUser(ctx, username)
	if errors.Is(err, ErrNotFound) {
		if exists {
			// A mapped account that an operator deleted must not be recreated.
			return Subscription{}, ErrInactive
		}
		expiresAt := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
		if s.config.AccessDays > 0 {
			expiresAt = s.now().UTC().AddDate(0, 0, s.config.AccessDays)
		}
		_, createErr := s.api.CreateUser(ctx, CreateUserRequest{
			Username: username, TelegramID: telegramID, Tag: accountTag, Status: "ACTIVE",
			ExpireAt:             expiresAt,
			TrafficLimitStrategy: "NO_RESET", ActiveInternalSquads: []string{s.config.SquadUUID},
		})
		// Read back even after a successful POST. A timeout or conflict is
		// reconciled by deterministic username; never blindly repeat the POST.
		user, err = s.api.FindUser(ctx, username)
		if err != nil || (createErr != nil && user.ID <= 0) {
			return Subscription{}, ErrUnavailable
		}
	} else if err != nil {
		return Subscription{}, ErrUnavailable
	}
	if user.ID <= 0 || user.Username != username || user.TelegramID == nil || *user.TelegramID != telegramID || user.Tag != accountTag {
		return Subscription{}, ErrConflict
	}
	if exists && mapping.RemoteID != user.ID {
		return Subscription{}, ErrConflict
	}
	if !exists {
		if err := s.store.SaveGrant(ctx, Mapping{TelegramID: telegramID, RemoteID: user.ID, Username: username}); err != nil {
			return Subscription{}, ErrUnavailable
		}
	}
	// Retrieval never extends access, changes squads or reactivates accounts.
	// Backend 3.4.5's get-user-info.headers.ts emits expire=0 for year 2099.
	noAutomaticExpiry := user.ExpireAt.UTC().Year() == 2099
	if user.Status != "ACTIVE" || (!noAutomaticExpiry && !user.ExpireAt.After(s.now())) {
		return Subscription{}, ErrInactive
	}
	assigned := false
	for _, squad := range user.ActiveInternalSquads {
		assigned = assigned || squad.UUID == s.config.SquadUUID
	}
	if !assigned {
		return Subscription{}, ErrInactive
	}
	u, err := url.Parse(user.SubscriptionURL)
	origin, _ := url.Parse(s.config.SubscriptionOrigin)
	if err != nil || origin == nil || u.Scheme != "https" || !strings.EqualFold(u.Host, origin.Host) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/api/sub/") || len(u.Path) <= len("/api/sub/") {
		return Subscription{}, ErrUnavailable
	}
	expiresAt := user.ExpireAt
	if noAutomaticExpiry {
		expiresAt = time.Time{}
	}
	return Subscription{URL: user.SubscriptionURL, ExpiresAt: expiresAt, TrafficLimitBytes: user.TrafficLimitBytes}, nil
}

// CheckDisabled lets an authenticated administrator verify revocation before
// deleting the bot's account row. It never edits or re-enables provider access.
func (s *Service) CheckDisabled(ctx context.Context, telegramID int64) error {
	mapping, exists, err := s.store.FindGrant(ctx, telegramID)
	if err != nil {
		return ErrUnavailable
	}
	if !exists {
		return nil
	}
	user, err := s.api.FindUser(ctx, mapping.Username)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	if user.ID != mapping.RemoteID || user.TelegramID == nil || *user.TelegramID != telegramID || user.Tag != accountTag {
		return ErrConflict
	}
	if user.Status != "DISABLED" {
		return ErrInactive
	}
	return nil
}
