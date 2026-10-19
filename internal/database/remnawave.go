package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// RemnawaveGrant records the immutable link between an existing bound account
// and its separately provisioned Remnawave identity.
type RemnawaveGrant struct {
	TelegramID int64     `gorm:"primaryKey;autoIncrement:false"`
	RemoteID   int64     `gorm:"unique;not null"`
	Username   string    `gorm:"unique;not null"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
}

var _ remnawave.Store = (*DB)(nil)

// remnawaveDB prevents query logging from exposing Telegram IDs or remote
// identities, including when PostgreSQL reports a uniqueness conflict.
func (db *DB) remnawaveDB(ctx context.Context) *gorm.DB {
	return db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)
}

// IsBoundUser checks the stored Telegram binding, independently of invitation
// flags or a mutable Telegram username.
func (db *DB) IsBoundUser(ctx context.Context, telegramID int64) (bool, error) {
	if telegramID <= 0 {
		return false, nil
	}
	var count int64
	if err := db.remnawaveDB(ctx).Model(&User{}).
		Where("telegram_id = ?", telegramID).Count(&count).Error; err != nil {
		return false, errors.New("could not check bound account")
	}
	return count > 0, nil
}

func (db *DB) FindGrant(ctx context.Context, telegramID int64) (remnawave.Mapping, bool, error) {
	if telegramID <= 0 {
		return remnawave.Mapping{}, false, nil
	}
	var grant RemnawaveGrant
	err := db.remnawaveDB(ctx).Where("telegram_id = ?", telegramID).First(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return remnawave.Mapping{}, false, nil
	}
	if err != nil {
		return remnawave.Mapping{}, false, errors.New("could not read access mapping")
	}
	return remnawave.Mapping{
		TelegramID: grant.TelegramID,
		RemoteID:   grant.RemoteID,
		Username:   grant.Username,
	}, true, nil
}

// SaveGrant is idempotent only for the identical mapping. A conflict must never
// reassign an existing Telegram or remote identity to another account.
func (db *DB) SaveGrant(ctx context.Context, mapping remnawave.Mapping) error {
	if mapping.TelegramID <= 0 || mapping.RemoteID <= 0 || strings.TrimSpace(mapping.Username) == "" {
		return errors.New("invalid access mapping")
	}
	grant := RemnawaveGrant{
		TelegramID: mapping.TelegramID,
		RemoteID:   mapping.RemoteID,
		Username:   mapping.Username,
	}
	if err := db.remnawaveDB(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&grant).Error; err != nil {
		return errors.New("could not save access mapping")
	}
	stored, found, err := db.FindGrant(ctx, mapping.TelegramID)
	if err != nil {
		return err
	}
	if !found || stored != mapping {
		return errors.New("access mapping conflict")
	}
	return nil
}
