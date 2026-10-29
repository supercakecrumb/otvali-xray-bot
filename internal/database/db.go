package database

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DB struct {
	Conn *gorm.DB
}

// NewDB initializes a GORM database connection
func NewDB(connectionString string, logger *slog.Logger) (*DB, error) {
	db, err := gorm.Open(postgres.Open(connectionString), &gorm.Config{})
	if err != nil {
		logger.Error("Failed to connect to database", slog.String("error", err.Error()))
		return nil, err
	}

	// Automigrate schema
	if err := db.AutoMigrate(&User{}); err != nil {
		logger.Error("Failed to migrate database schema", slog.String("error", err.Error()))
		return nil, err
	}
	if err := db.AutoMigrate(&Server{}); err != nil {
		logger.Error("Failed to migrate database schema", slog.String("error", err.Error()))
		return nil, err
	}
	if err := db.AutoMigrate(&UserMessage{}, &UserMessageForward{}); err != nil {
		logger.Error("Failed to migrate database schema", slog.String("error", err.Error()))
		return nil, err
	}
	if err := db.AutoMigrate(&RemnawaveGrant{}); err != nil {
		logger.Error("Failed to migrate database schema", slog.String("error", err.Error()))
		return nil, err
	}

	if err := db.AutoMigrate(&BetaReport{}); err != nil {
		logger.Error("Failed to migrate beta feedback schema")
		return nil, err
	}

	wrapped := &DB{Conn: db}
	if err := wrapped.InitActivity(context.Background(), time.Now()); err != nil {
		logger.Error("Failed to initialize activity schema")
		return nil, err
	}
	return wrapped, nil
}
