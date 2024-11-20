package telegram

import (
	"fmt"
	"strings"

	"github.com/mymmrac/telego"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

// Handle /invite command
func (b *Bot) handleInviteCommand(message *telego.Message) {
	chatID := message.Chat.ID
	args := strings.Fields(message.Text)

	if len(args) < 2 {
		_ = b.sendMessage(chatID, "Usage: /invite <username>")
		return
	}

	invitedUsername := args[1]

	// Check if the user already exists
	var invitedUser database.User
	if err := b.db.Conn.First(&invitedUser, "username = ?", invitedUsername).Error; err == nil {
		_ = b.sendMessage(chatID, "This user is already registered.")
		return
	}

	// Add the new user as invited
	invitedUser = database.User{
		ID:        0, // This will be updated when the user interacts with the bot
		Username:  invitedUsername,
		InvitedBy: &chatID,
	}
	if err := b.db.AddUser(&invitedUser); err != nil {
		_ = b.sendMessage(chatID, "Failed to invite user.")
		return
	}

	_ = b.sendMessage(chatID, fmt.Sprintf("User %s has been invited and can now access basic servers.", invitedUsername))
}
