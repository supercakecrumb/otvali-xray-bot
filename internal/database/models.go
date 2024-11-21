package database

import (
	"time"
)

// User represents a Telegram user in the database
type User struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`
	TelegramID      *int64    `gorm:"unique;"`
	Username        string    `gorm:"unique;not null"`
	IsAdmin         bool      `gorm:"default:false"`
	InvitedBy       *int64    `gorm:""`
	ExclusiveAccess bool      `gorm:"default:false"`
	CreatedAt       time.Time `gorm:"autoCreateTime"`
}
