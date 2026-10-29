package database

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// UserMessage represents a message from user to admin or admin reply
type UserMessage struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	BotID          int64     `gorm:"index"`               // Origin bot; NULL/zero only for historical messages.
	TelegramChatID int64     `gorm:"index"`               // Origin chat scopes TelegramMsgID together with BotID.
	UserID         int64     `gorm:"not null;index"`      // Telegram ID of the user who sent the message
	Username       string    `gorm:"not null"`            // Username of the sender
	AdminID        *int64    `gorm:"index"`               // Telegram ID of admin who replied (null if user message)
	MessageText    string    `gorm:"type:text;not null"`  // Message content
	IsAdminReply   bool      `gorm:"default:false;index"` // True if this is an admin reply
	ReplyToID      *int64    `gorm:"index"`               // ID of message being replied to
	TelegramMsgID  int       `gorm:"not null"`            // Telegram message ID for reference
	AdminChatMsgID *int      `gorm:""`                    // Legacy first-admin reference; new forwards use UserMessageForward.
	CreatedAt      time.Time `gorm:"autoCreateTime;index"`
}

// UserMessageForward binds each delivered admin copy to its original message.
// Telegram message IDs are local to a bot/chat; no part of this key is optional.
// This separate table preserves historical messages without guessing their chat.
type UserMessageForward struct {
	BotID         int64     `gorm:"primaryKey;autoIncrement:false"`
	AdminChatID   int64     `gorm:"primaryKey;autoIncrement:false"`
	TelegramMsgID int       `gorm:"primaryKey;autoIncrement:false"`
	UserMessageID int64     `gorm:"not null;index"`
	CreatedAt     time.Time `gorm:"autoCreateTime"`
}

var ErrMessageUnavailable = errors.New("message unavailable")

// Queries may include private text. Disable SQL logging, including failed writes.
func (db *DB) messageDB() *gorm.DB {
	return db.Conn.Session(&gorm.Session{Logger: logger.Discard})
}

// AddUserMessage saves a user message to the database
func (db *DB) AddUserMessage(msg *UserMessage) error {
	if msg == nil || msg.BotID <= 0 || msg.TelegramChatID <= 0 || msg.TelegramMsgID <= 0 {
		return ErrMessageUnavailable
	}
	if err := db.messageDB().Create(msg).Error; err != nil {
		return ErrMessageUnavailable
	}
	return nil
}

// GetUserMessageByID retrieves a message by its ID
func (db *DB) GetUserMessageByID(id int64) (*UserMessage, error) {
	var msg UserMessage
	if err := db.messageDB().First(&msg, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrUserNotFound
		}
		return nil, ErrMessageUnavailable
	}
	return &msg, nil
}

// AddUserMessageForward is idempotent only for an identical immutable mapping.
func (db *DB) AddUserMessageForward(forward *UserMessageForward) error {
	if forward == nil || forward.BotID <= 0 || forward.AdminChatID <= 0 || forward.TelegramMsgID <= 0 || forward.UserMessageID <= 0 {
		return ErrMessageUnavailable
	}
	if err := db.messageDB().Clauses(clause.OnConflict{DoNothing: true}).Create(forward).Error; err != nil {
		return ErrMessageUnavailable
	}
	var stored UserMessageForward
	if err := db.messageDB().Where("bot_id = ? AND admin_chat_id = ? AND telegram_msg_id = ?", forward.BotID, forward.AdminChatID, forward.TelegramMsgID).First(&stored).Error; err != nil || stored.UserMessageID != forward.UserMessageID {
		return ErrMessageUnavailable
	}
	return nil
}

// GetUserMessageByAdminForward never matches a message from another bot or chat.
func (db *DB) GetUserMessageByAdminForward(botID, adminChatID int64, messageID int) (*UserMessage, error) {
	if botID <= 0 || adminChatID <= 0 || messageID <= 0 {
		return nil, ErrUserNotFound
	}
	var msg UserMessage
	err := db.messageDB().Table("user_messages").Select("user_messages.*").
		Joins("JOIN user_message_forwards AS forward ON forward.user_message_id = user_messages.id").
		Where("forward.bot_id = ? AND forward.admin_chat_id = ? AND forward.telegram_msg_id = ? AND user_messages.is_admin_reply = false AND user_messages.bot_id > 0", botID, adminChatID, messageID).
		First(&msg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, ErrMessageUnavailable
	}
	return &msg, nil
}

// GetLegacyUserMessageByAdminReply is only for the primary bot, after the caller
// validates a primary-bot-authored legacy forward and extracts its recipient ID.
// Old rows lack chat identity. Reject ambiguous candidates instead of guessing.
func (db *DB) GetLegacyUserMessageByAdminReply(messageID int, verifiedUserID int64) (*UserMessage, error) {
	if messageID <= 0 || verifiedUserID <= 0 {
		return nil, ErrUserNotFound
	}
	var messages []UserMessage
	if err := db.messageDB().Where("admin_chat_msg_id = ? AND user_id = ? AND is_admin_reply = false AND (bot_id IS NULL OR bot_id = 0)", messageID, verifiedUserID).
		Limit(2).Find(&messages).Error; err != nil {
		return nil, ErrMessageUnavailable
	}
	if len(messages) != 1 {
		return nil, ErrUserNotFound
	}
	return &messages[0], nil
}

// GetUserMessages retrieves all messages for a specific user
func (db *DB) GetUserMessages(userID int64, limit int) ([]UserMessage, error) {
	var messages []UserMessage
	query := db.messageDB().Where("user_id = ?", userID).Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&messages).Error; err != nil {
		return nil, ErrMessageUnavailable
	}
	return messages, nil
}
