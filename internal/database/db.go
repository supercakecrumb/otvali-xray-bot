package database

import (
	"log/slog"

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

	return &DB{Conn: db}, nil
}
