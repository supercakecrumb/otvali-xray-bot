package database

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// BetaReport is separate from access credentials. Payload contains only the
// validated user-entered diagnostic form; it is never written to query logs.
type BetaReport struct {
	ID               int64     `gorm:"primaryKey;autoIncrement"`
	UserID           int64     `gorm:"not null;uniqueIndex:beta_request;index:beta_user_created,priority:1"`
	RequestID        string    `gorm:"type:varchar(36);not null;uniqueIndex:beta_request"`
	PayloadHash      string    `gorm:"type:char(64);not null"`
	PayloadJSON      string    `gorm:"type:text;not null"`
	Status           string    `gorm:"type:varchar(20);not null;default:new;index"`
	Note             string    `gorm:"type:text;not null;default:''"`
	CreatedAt        time.Time `gorm:"not null;index:beta_user_created,priority:2"`
	UpdatedAt        time.Time `gorm:"not null"`
	AuthorUsername   string    `gorm:"->;-:migration"`
	AuthorTelegramID int64     `gorm:"->;-:migration"`
}

var (
	ErrBetaForbidden   = errors.New("beta forbidden")
	ErrBetaNotFound    = errors.New("beta report not found")
	ErrBetaConflict    = errors.New("beta request conflict")
	ErrBetaRateLimited = errors.New("beta report rate limited")
	ErrBetaUnavailable = errors.New("beta storage unavailable")
)

func (db *DB) betaDB(ctx context.Context) *gorm.DB {
	return db.Conn.Session(&gorm.Session{Logger: logger.Discard}).WithContext(ctx)
}

func betaActor(tx *gorm.DB, telegramID int64, lock bool) (User, error) {
	var user User
	if telegramID <= 0 {
		return user, ErrBetaForbidden
	}
	query := tx.Select("id", "telegram_id", "username", "is_admin").Where("telegram_id = ?", telegramID)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.First(&user).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return user, ErrBetaForbidden
	} else if err != nil {
		return user, ErrBetaUnavailable
	}
	return user, nil
}

func betaStorageError(err error) error {
	if err == nil || errors.Is(err, ErrBetaForbidden) || errors.Is(err, ErrBetaNotFound) || errors.Is(err, ErrBetaConflict) || errors.Is(err, ErrBetaRateLimited) {
		return err
	}
	return ErrBetaUnavailable
}

// The bound user row serializes quota and idempotency decisions across all
// processes. Identical retries return the original report even after the quota
// fills; a changed payload under the same request ID never overwrites it.
func (db *DB) CreateBetaReport(ctx context.Context, telegramID int64, candidate BetaReport, now time.Time) (BetaReport, bool, error) {
	var result BetaReport
	duplicate := false
	err := db.betaDB(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := betaActor(tx, telegramID, true)
		if err != nil {
			return err
		}
		found := tx.Where("user_id = ? AND request_id = ?", user.ID, candidate.RequestID).First(&result).Error
		if found == nil {
			if result.PayloadHash != candidate.PayloadHash {
				return ErrBetaConflict
			}
			duplicate = true
		} else {
			if !errors.Is(found, gorm.ErrRecordNotFound) {
				return found
			}
			for _, quota := range []struct {
				window time.Duration
				max    int64
			}{{time.Hour, 12}, {24 * time.Hour, 60}} {
				var count int64
				if err := tx.Model(&BetaReport{}).Where("user_id = ? AND created_at >= ?", user.ID, now.Add(-quota.window)).Count(&count).Error; err != nil {
					return err
				}
				if count >= quota.max {
					return ErrBetaRateLimited
				}
			}
			result = BetaReport{UserID: user.ID, RequestID: candidate.RequestID, PayloadHash: candidate.PayloadHash, PayloadJSON: candidate.PayloadJSON, Status: "new", CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
		}
		result.AuthorUsername, result.AuthorTelegramID = user.Username, telegramID
		return nil
	})
	return result, duplicate, betaStorageError(err)
}

func betaReadQuery(tx *gorm.DB) *gorm.DB {
	return tx.Model(&BetaReport{}).Select("beta_reports.*, users.username AS author_username, users.telegram_id AS author_telegram_id").Joins("LEFT JOIN users ON users.id = beta_reports.user_id")
}

func (db *DB) ListBetaReports(ctx context.Context, telegramID int64, admin bool, before int64) ([]BetaReport, error) {
	tx := db.betaDB(ctx)
	user, err := betaActor(tx, telegramID, false)
	if err != nil {
		return nil, err
	}
	if admin && !user.IsAdmin {
		return nil, ErrBetaForbidden
	}
	query := betaReadQuery(tx)
	if !admin {
		query = query.Where("beta_reports.user_id = ?", user.ID)
	}
	if before > 0 {
		query = query.Where("beta_reports.id < ?", before)
	}
	var result []BetaReport
	err = query.Order("beta_reports.id DESC").Limit(26).Find(&result).Error
	return result, betaStorageError(err)
}

func (db *DB) GetBetaReport(ctx context.Context, telegramID, id int64) (BetaReport, error) {
	tx := db.betaDB(ctx)
	user, err := betaActor(tx, telegramID, false)
	if err != nil {
		return BetaReport{}, err
	}
	query := betaReadQuery(tx).Where("beta_reports.id = ?", id)
	if !user.IsAdmin {
		query = query.Where("beta_reports.user_id = ?", user.ID)
	}
	var result BetaReport
	err = query.First(&result).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, ErrBetaNotFound
	}
	return result, betaStorageError(err)
}

func (db *DB) UpdateBetaReport(ctx context.Context, telegramID, id int64, status, note string, now time.Time) (BetaReport, error) {
	var result BetaReport
	err := db.betaDB(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := betaActor(tx, telegramID, true)
		if err != nil {
			return err
		}
		if !user.IsAdmin {
			return ErrBetaForbidden
		}
		if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&result, id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBetaNotFound
		} else if err != nil {
			return err
		}
		if result.Status == status && result.Note == note {
			return nil
		}
		return tx.Model(&result).Updates(map[string]any{"status": status, "note": note, "updated_at": now.UTC()}).Error
	})
	if err != nil {
		return BetaReport{}, betaStorageError(err)
	}
	// Re-read through current authorization; a revoked admin never receives data.
	return db.GetBetaReport(ctx, telegramID, id)
}
