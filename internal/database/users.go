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

// GetUser retrieves a user by their Telegram ID
func (db *DB) GetUser(userID int64) (*User, error) {
	var user User
	if err := db.Conn.First(&user, "id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// IsUserAdmin checks if a user is an admin
func (db *DB) IsUserAdmin(userID int64) (bool, error) {
	user, err := db.GetUser(userID)
	if err != nil {
		return false, err
	}
	return user.IsAdmin, nil
}

