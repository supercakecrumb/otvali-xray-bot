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

// Server represents a VPN server configuration
type Server struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	Name        string    `gorm:"unique;not null"`
	Country     string    `gorm:"not null"`
	City        string    `gorm:"not null"`
	IP          string    `gorm:"not null"`
	SSHPort     int       `gorm:"not null"`
	SSHUser     string    `gorm:"not null"`
	APIPort     int       `gorm:"not null"`
	Username    string    `gorm:"not null"`
	Password    string    `gorm:"not null"`
	OutboundID  *int      `gorm:""`              // Nullable if outbound ID is not provided
	IsExclusive bool      `gorm:"default:false"` // Indicates if the server is exclusive
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}
