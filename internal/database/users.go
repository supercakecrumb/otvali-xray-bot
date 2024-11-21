package database

import (
	"errors"

	"gorm.io/gorm"
)

var ErrUserNotFound = errors.New("user not found")

// AddUser adds a new user to the database or updates an existing user
func (db *DB) AddUser(user *User) error {
	return db.Conn.Save(user).Error
}

// GetUserByID retrieves a user by their primary key ID
func (db *DB) GetUserByID(id int64) (*User, error) {
	var user User
	if err := db.Conn.First(&user, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByTelegramID retrieves a user by their Telegram ID
func (db *DB) GetUserByTelegramID(telegramID int64) (*User, error) {
	var user User
	if err := db.Conn.First(&user, "telegram_id = ?", telegramID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByUsername retrieves a user by their username
func (db *DB) GetUserByUsername(username string) (*User, error) {
	var user User
	if err := db.Conn.First(&user, "username = ?", username).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// UpdateUserTelegramID updates the user's TelegramID
func (db *DB) UpdateUserTelegramID(userID int64, telegramID int64) error {
	return db.Conn.Model(&User{}).Where("id = ?", userID).Update("telegram_id", telegramID).Error
}

// UpdateUserUsername updates the user's username
func (db *DB) UpdateUserUsername(userID int64, username string) error {
	return db.Conn.Model(&User{}).Where("id = ?", userID).Update("username", username).Error
}

// UpdateUserExclusiveAccess updates the user's exclusive access
func (db *DB) UpdateUserExclusiveAccess(userID int64, exclusiveAccess bool) error {
	return db.Conn.Model(&User{}).Where("id = ?", userID).Update("exclusive_access", exclusiveAccess).Error
}
