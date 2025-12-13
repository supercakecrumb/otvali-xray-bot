package database

import (
	"time"

	"gorm.io/gorm"
)

// UserMessage represents a message from user to admin or admin reply
type UserMessage struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	UserID         int64     `gorm:"not null;index"`      // Telegram ID of the user who sent the message
	Username       string    `gorm:"not null"`            // Username of the sender
	AdminID        *int64    `gorm:"index"`               // Telegram ID of admin who replied (null if user message)
	MessageText    string    `gorm:"type:text;not null"`  // Message content
	IsAdminReply   bool      `gorm:"default:false;index"` // True if this is an admin reply
	ReplyToID      *int64    `gorm:"index"`               // ID of message being replied to
	TelegramMsgID  int       `gorm:"not null"`            // Telegram message ID for reference
	AdminChatMsgID *int      `gorm:""`                    // Message ID in admin chat (for forwarded messages)
	CreatedAt      time.Time `gorm:"autoCreateTime;index"`
}

// AddUserMessage saves a user message to the database
func (db *DB) AddUserMessage(msg *UserMessage) error {
	return db.Conn.Create(msg).Error
}

// GetUserMessageByID retrieves a message by its ID
func (db *DB) GetUserMessageByID(id int64) (*UserMessage, error) {
	var msg UserMessage
	if err := db.Conn.First(&msg, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &msg, nil
}

// GetUserMessageByAdminChatMsgID retrieves the original user message by admin chat message ID
func (db *DB) GetUserMessageByAdminChatMsgID(adminChatMsgID int) (*UserMessage, error) {
	var msg UserMessage
	if err := db.Conn.Where("admin_chat_msg_id = ?", adminChatMsgID).First(&msg).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &msg, nil
}

// GetUserMessages retrieves all messages for a specific user
func (db *DB) GetUserMessages(userID int64, limit int) ([]UserMessage, error) {
	var messages []UserMessage
	query := db.Conn.Where("user_id = ?", userID).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}
