package database

import (
	"time"
)

// User represents a Telegram user in the database
type User struct {
	ID        int64     `gorm:"primaryKey"`        // Telegram user ID
	Username  string    `gorm:"unique;not null"`   // Telegram username
	IsAdmin   bool      `gorm:"default:false"`     // Admin status
	InvitedBy *int64    `gorm:"null"`              // ID of the user who invited them
	CreatedAt time.Time `gorm:"autoCreateTime"`    // Timestamp
}

