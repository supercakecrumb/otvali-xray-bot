package remnawave

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type AccessState string

const (
	AccessReady         AccessState = "ready"
	AccessUnprovisioned AccessState = "eligible_unprovisioned"
)

// Status reads provider state without creating an account, saving a mapping,
// renewing access or reactivating an account. A deleted mapped account stays
// revoked; an eligible person with no account is explicitly unprovisioned.
func (s *Service) Status(ctx context.Context, id int64) (AccessState, error) {
	if !s.Eligible(ctx, id) {
		return "", ErrNotEligible
	}
	mapping, exists, err := s.store.FindGrant(ctx, id)
	if err != nil {
		return "", ErrUnavailable
	}
	username := "otvali_tg_" + strconv.FormatInt(id, 10)
	if exists && (mapping.TelegramID != id || mapping.Username != username || mapping.RemoteID <= 0) {
		return "", ErrConflict
	}
	user, err := s.api.FindUser(ctx, username)
	if errors.Is(err, ErrNotFound) {
		if exists {
			return "", ErrInactive
		}
		return AccessUnprovisioned, nil
	}
	if err != nil {
		return "", ErrUnavailable
	}
	if user.ID <= 0 || user.Username != username || user.TelegramID == nil || *user.TelegramID != id || user.Tag != accountTag || (exists && mapping.RemoteID != user.ID) {
		return "", ErrConflict
	}
	if user.Status != "ACTIVE" || (user.ExpireAt.UTC().Year() != 2099 && !user.ExpireAt.After(s.now())) {
		return "", ErrInactive
	}
	assigned := false
	for _, squad := range user.ActiveInternalSquads {
		assigned = assigned || squad.UUID == s.config.SquadUUID
	}
	if !assigned {
		return "", ErrInactive
	}
	u, err := url.Parse(user.SubscriptionURL)
	origin, _ := url.Parse(s.config.SubscriptionOrigin)
	if err != nil || origin == nil || u.Scheme != "https" || !strings.EqualFold(u.Host, origin.Host) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/api/sub/") || len(u.Path) <= len("/api/sub/") {
		return "", ErrUnavailable
	}
	return AccessReady, nil
}
