package database

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// TelegramProxyGrant deliberately has no foreign key to users: revocation
// tombstones must survive deletion and a later invitation of the same account.
type TelegramProxyGrant struct {
	TelegramID      int64     `gorm:"primaryKey;autoIncrement:false"`
	EndpointID      string    `gorm:"primaryKey;size:32"`
	UserID          int64     `gorm:"not null;index"`
	ProviderID      string    `gorm:"not null;size:32;uniqueIndex:telegram_proxy_external"`
	BindingHash     string    `gorm:"not null;size:64"`
	ExternalID      string    `gorm:"not null;size:64;uniqueIndex:telegram_proxy_external"`
	EncryptedSecret []byte    `gorm:"not null;type:bytea"`
	Generation      int64     `gorm:"not null"`
	State           string    `gorm:"not null;size:16;index"`
	CreatedAt       time.Time `gorm:"autoCreateTime"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime"`
}

var _ telegramproxy.Store = (*DB)(nil)
var proxyHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var proxyExternalPattern = regexp.MustCompile(`^otv_[0-9a-f]{32}$`)
var proxyProviderPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func (db *DB) proxyDB(ctx context.Context) *gorm.DB {
	return db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)
}

func (db *DB) InitTelegramProxy(ctx context.Context) error {
	if err := db.proxyDB(ctx).AutoMigrate(&TelegramProxyGrant{}); err != nil {
		return telegramproxy.ErrUnavailable
	}
	return nil
}

func (db *DB) ProxyUserID(ctx context.Context, telegramID int64) (int64, error) {
	if telegramID <= 0 {
		return 0, telegramproxy.ErrNotEligible
	}
	var user User
	err := db.proxyDB(ctx).Select("id").Where("telegram_id = ?", telegramID).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, telegramproxy.ErrNotEligible
	}
	if err != nil {
		return 0, telegramproxy.ErrUnavailable
	}
	return user.ID, nil
}

func proxyGrant(model TelegramProxyGrant) telegramproxy.Grant {
	return telegramproxy.Grant{UserID: model.UserID, TelegramID: model.TelegramID, EndpointID: model.EndpointID, ProviderID: model.ProviderID, BindingHash: model.BindingHash, ExternalID: model.ExternalID, EncryptedSecret: append([]byte(nil), model.EncryptedSecret...), Generation: model.Generation, State: model.State, CreatedAt: model.CreatedAt, UpdatedAt: model.UpdatedAt}
}

func proxyModel(g telegramproxy.Grant) TelegramProxyGrant {
	return TelegramProxyGrant{UserID: g.UserID, TelegramID: g.TelegramID, EndpointID: g.EndpointID, ProviderID: g.ProviderID, BindingHash: g.BindingHash, ExternalID: g.ExternalID, EncryptedSecret: append([]byte(nil), g.EncryptedSecret...), Generation: g.Generation, State: g.State, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}

func validProxyEndpoint(id string) bool {
	return id == "finland" || id == "stockholm" || id == "msk-finland" || id == "msk-stockholm"
}

func (db *DB) GetProxyGrant(ctx context.Context, telegramID int64, endpointID string) (telegramproxy.Grant, bool, error) {
	if telegramID <= 0 || !validProxyEndpoint(endpointID) {
		return telegramproxy.Grant{}, false, telegramproxy.ErrNotFound
	}
	var model TelegramProxyGrant
	err := db.proxyDB(ctx).Where("telegram_id = ? AND endpoint_id = ?", telegramID, endpointID).First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return telegramproxy.Grant{}, false, nil
	}
	if err != nil {
		return telegramproxy.Grant{}, false, telegramproxy.ErrUnavailable
	}
	return proxyGrant(model), true, nil
}

func proxyError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{telegramproxy.ErrNotEligible, telegramproxy.ErrNotFound, telegramproxy.ErrInactive, telegramproxy.ErrConflict, telegramproxy.ErrUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return telegramproxy.ErrUnavailable
}

// PrepareProxyGrant holds the account lock also used by guarded deletion.
// Its transaction commits before the caller can issue an external credential.
func (db *DB) PrepareProxyGrant(ctx context.Context, g telegramproxy.Grant) (telegramproxy.Grant, error) {
	if g.UserID <= 0 || g.TelegramID <= 0 || !validProxyEndpoint(g.EndpointID) || !proxyProviderPattern.MatchString(g.ProviderID) || !proxyHashPattern.MatchString(g.BindingHash) || !proxyExternalPattern.MatchString(g.ExternalID) || len(g.EncryptedSecret) != 45 || g.EncryptedSecret[0] != 1 || g.Generation != 1 || g.State != telegramproxy.StatePending {
		return telegramproxy.Grant{}, telegramproxy.ErrConflict
	}
	var result TelegramProxyGrant
	err := db.proxyDB(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND telegram_id = ?", g.UserID, g.TelegramID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return telegramproxy.ErrNotEligible
			}
			return err
		}
		model := proxyModel(g)
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model).Error; err != nil {
			return err
		}
		if err := tx.Where("telegram_id = ? AND endpoint_id = ?", g.TelegramID, g.EndpointID).First(&result).Error; err != nil {
			return err
		}
		if result.UserID != g.UserID {
			return telegramproxy.ErrInactive
		}
		if result.ProviderID != g.ProviderID || result.ExternalID != g.ExternalID || result.BindingHash != g.BindingHash {
			return telegramproxy.ErrConflict
		}
		return nil
	})
	return proxyGrant(result), proxyError(err)
}

func proxyImmutableEqual(a, b telegramproxy.Grant) bool {
	return a.UserID == b.UserID && a.TelegramID == b.TelegramID && a.EndpointID == b.EndpointID && a.ProviderID == b.ProviderID && a.BindingHash == b.BindingHash && a.ExternalID == b.ExternalID && bytes.Equal(a.EncryptedSecret, b.EncryptedSecret) && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

func proxyTransition(before, after telegramproxy.Grant) bool {
	if !proxyImmutableEqual(before, after) {
		return false
	}
	if before.State == after.State {
		return before.Generation == after.Generation
	}
	if after.Generation != before.Generation+1 {
		return false
	}
	switch before.State {
	case telegramproxy.StatePending:
		return after.State == telegramproxy.StateActive || after.State == telegramproxy.StateRevoking || after.State == telegramproxy.StateRevoked
	case telegramproxy.StateActive:
		return after.State == telegramproxy.StateRevoking || after.State == telegramproxy.StateRevoked
	case telegramproxy.StateRevoking:
		return after.State == telegramproxy.StateRevoked
	default:
		return false
	}
}

// WithProxyGrant locks account then grant in the same order as preparation and
// deletion. The external call is bounded by Service's deadline. Revoked rows
// cannot be reactivated; CAS rejects stale generations even after waiting.
func (db *DB) WithProxyGrant(ctx context.Context, telegramID int64, endpointID string, fn func(*telegramproxy.Grant) error) error {
	return db.withProxyGrant(ctx, telegramID, endpointID, true, fn)
}

func (db *DB) withProxyGrant(ctx context.Context, telegramID int64, endpointID string, requireBound bool, fn func(*telegramproxy.Grant) error) error {
	if telegramID <= 0 || !validProxyEndpoint(endpointID) || fn == nil {
		return telegramproxy.ErrNotFound
	}
	err := db.proxyDB(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		uerr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("telegram_id = ?", telegramID).First(&user).Error
		if uerr != nil && !errors.Is(uerr, gorm.ErrRecordNotFound) {
			return uerr
		}
		var model TelegramProxyGrant
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("telegram_id = ? AND endpoint_id = ?", telegramID, endpointID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return telegramproxy.ErrNotFound
			}
			return err
		}
		if requireBound && (model.State == telegramproxy.StateActive || model.State == telegramproxy.StatePending) && (uerr != nil || user.ID != model.UserID) {
			return telegramproxy.ErrInactive
		}
		before := proxyGrant(model)
		after := proxyGrant(model)
		if err := fn(&after); err != nil {
			return err
		}
		if !proxyTransition(before, after) {
			return telegramproxy.ErrConflict
		}
		if before.State == after.State {
			return nil
		}
		result := tx.Model(&TelegramProxyGrant{}).Where("telegram_id = ? AND endpoint_id = ? AND generation = ? AND state = ?", telegramID, endpointID, before.Generation, before.State).Updates(map[string]any{"state": after.State, "generation": after.Generation, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return telegramproxy.ErrConflict
		}
		return nil
	})
	return proxyError(err)
}

func (db *DB) MarkProxyRevoking(ctx context.Context, telegramID int64, endpointID string) (telegramproxy.Grant, error) {
	var result telegramproxy.Grant
	err := db.withProxyGrant(ctx, telegramID, endpointID, false, func(g *telegramproxy.Grant) error {
		switch g.State {
		case telegramproxy.StatePending, telegramproxy.StateActive:
			g.State, g.Generation = telegramproxy.StateRevoking, g.Generation+1
		case telegramproxy.StateRevoking, telegramproxy.StateRevoked:
		default:
			return telegramproxy.ErrConflict
		}
		result = *g
		return nil
	})
	return result, err
}

func (db *DB) CheckTelegramProxyDisabled(ctx context.Context, telegramID int64) error {
	if telegramID <= 0 {
		return telegramproxy.ErrNotEligible
	}
	var count int64
	if err := db.proxyDB(ctx).Model(&TelegramProxyGrant{}).Where("telegram_id = ? AND state <> ?", telegramID, telegramproxy.StateRevoked).Count(&count).Error; err != nil {
		return telegramproxy.ErrUnavailable
	}
	if count != 0 {
		return telegramproxy.ErrInactive
	}
	return nil
}

// DeleteUserWithTelegramProxyGuard makes the final check atomic with deletion.
// A separate preflight is useful for UX, but cannot prevent a concurrent Issue.
func (db *DB) DeleteUserWithTelegramProxyGuard(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return telegramproxy.ErrNotEligible
	}
	err := db.proxyDB(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", userID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return telegramproxy.ErrNotFound
			}
			return err
		}
		query := tx.Model(&TelegramProxyGrant{}).Where("state <> ?", telegramproxy.StateRevoked)
		if user.TelegramID != nil {
			query = query.Where("(user_id = ? OR telegram_id = ?)", user.ID, *user.TelegramID)
		} else {
			query = query.Where("user_id = ?", user.ID)
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return telegramproxy.ErrInactive
		}
		return tx.Delete(&user).Error
	})
	return proxyError(err)
}
