package database

import (
	"context"
	"errors"
	"regexp"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// ActivityEvent stores fixed event names, never message contents, callback data,
// credential identifiers, URLs, device data or IP addresses. UserID is the local
// bound-user row; EventID is an opaque server-generated deduplication digest.
type ActivityEvent struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"not null;index:activity_user_created,priority:1"`
	Source    string    `gorm:"type:varchar(12);not null;uniqueIndex:activity_event,priority:1"`
	Kind      string    `gorm:"type:varchar(40);not null;uniqueIndex:activity_event,priority:3"`
	EventID   string    `gorm:"type:char(64);not null;uniqueIndex:activity_event,priority:2"`
	CreatedAt time.Time `gorm:"not null;index:activity_user_created,priority:2"`
}

// The first deployment timestamp is retained across restarts. Historical
// unobserved actions must not be reported as zero or reconstructed from keys.
type ActivityCollection struct {
	ID                  int       `gorm:"primaryKey;autoIncrement:false"`
	StartedAt           time.Time `gorm:"not null"`
	LegacyTelegramBotID *int64    // Immutable owner of pre-migration Telegram event digests.
}

// BindLegacyTelegramActivityBot assigns the pre-migration digest namespace only
// once. Changing notification primary or rotating a token must not reassign it.
// Existing events and the original collection timestamp are never rewritten.
func (db *DB) BindLegacyTelegramActivityBot(ctx context.Context, proposedID int64) (int64, error) {
	if db == nil || db.Conn == nil || proposedID <= 0 {
		return 0, ErrActivityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx := db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)
	if err := tx.Model(&ActivityCollection{}).Where("id = 1 AND legacy_telegram_bot_id IS NULL").Update("legacy_telegram_bot_id", proposedID).Error; err != nil {
		return 0, ErrActivityUnavailable
	}
	var collection ActivityCollection
	if err := tx.First(&collection, 1).Error; err != nil || collection.LegacyTelegramBotID == nil || *collection.LegacyTelegramBotID <= 0 {
		return 0, ErrActivityUnavailable
	}
	return *collection.LegacyTelegramBotID, nil
}

var ErrActivityUnavailable = errors.New("activity unavailable")
var activityDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func ValidActivity(source, kind, eventID string) bool {
	if !activityDigest.MatchString(eventID) {
		return false
	}
	switch source {
	case "telegram":
		switch kind {
		case "message", "command", "callback", "key_delivered", "subscription_delivered":
			return true
		}
	case "miniapp":
		switch kind {
		case "miniapp_session", "miniapp_keys", "miniapp_key_response", "miniapp_subscription_response", "miniapp_beta_plan", "miniapp_beta_reports", "miniapp_beta_detail", "miniapp_beta_submit", "miniapp_beta_update":
			return true
		}
	}
	return false
}

func (db *DB) InitActivity(ctx context.Context, now time.Time) error {
	if db == nil || db.Conn == nil || now.IsZero() {
		return ErrActivityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx := db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)
	if err := tx.AutoMigrate(&ActivityEvent{}, &ActivityCollection{}); err != nil {
		return ErrActivityUnavailable
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ActivityCollection{ID: 1, StartedAt: now.UTC()}).Error; err != nil {
		return ErrActivityUnavailable
	}
	return nil
}

func (db *DB) RecordActivity(ctx context.Context, telegramID int64, source, kind, eventID string, now time.Time) error {
	if db == nil || db.Conn == nil || telegramID <= 0 || now.IsZero() || !ValidActivity(source, kind, eventID) {
		return ErrActivityUnavailable
	}
	tx := db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)
	// INSERT..SELECT binds identity at write time and never creates users. Unique
	// source/update/kind makes Telegram redelivery safe, including goroutines.
	result := tx.Exec(`INSERT INTO activity_events (user_id, source, kind, event_id, created_at)
		SELECT id, ?, ?, ?, ? FROM users WHERE telegram_id = ?
		ON CONFLICT (source, event_id, kind) DO NOTHING`, source, kind, eventID, now.UTC(), telegramID)
	if result.Error != nil {
		return ErrActivityUnavailable
	}
	return nil
}
