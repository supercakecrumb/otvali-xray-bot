package telegram

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

// registerMessagingHandlers registers handlers for bidirectional messaging
func (b *Bot) registerMessagingHandlers() {
	// Handle all text messages (except commands) from users
	b.bh.Handle(b.handleUserMessage, th.AnyMessage(), func(u telego.Update) bool {
		return u.Message != nil &&
			u.Message.Text != "" &&
			!strings.HasPrefix(u.Message.Text, "/") &&
			u.Message.ReplyToMessage == nil // Don't handle as user message if it's a reply
	})

	// Handle admin replies (reply-to messages)
	b.bh.Handle(b.handleAdminReply, th.AnyMessage(), func(u telego.Update) bool {
		return u.Message != nil &&
			u.Message.Text != "" &&
			u.Message.ReplyToMessage != nil
	})
}

// handleUserMessage forwards user messages to admins
func (b *Bot) handleUserMessage(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		return
	}

	message := update.Message
	chatID := message.Chat.ID
	userID := message.From.ID
	username := message.From.Username

	// Check if user exists in database
	user, err := b.db.GetUserByTelegramID(userID)
	if err != nil {
		b.logger.Warn("User not found in database, ignoring message",
			slog.Int64("user_id", userID),
			slog.String("username", username))
		return
	}

	// Don't forward messages from admins to avoid loops
	if user.IsAdmin {
		return
	}

	// Save user message to database
	userMsg := &database.UserMessage{
		UserID:        userID,
		Username:      username,
		MessageText:   message.Text,
		IsAdminReply:  false,
		TelegramMsgID: message.MessageID,
		CreatedAt:     time.Now(),
	}

	if err := b.db.AddUserMessage(userMsg); err != nil {
		b.logger.Error("Failed to save user message",
			slog.String("error", err.Error()),
			slog.String("username", username))
	}

	// Format message for admins
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	// Get user info for display
	displayName := username
	if message.From.FirstName != "" {
		displayName = message.From.FirstName
		if message.From.LastName != "" {
			displayName += " " + message.From.LastName
		}
		displayName += fmt.Sprintf(" (@%s)", username)
	}

	forwardMessage := fmt.Sprintf(
		"💬 *Сообщение от пользователя*\n\n"+
			"👤 От: %s\n"+
			"🆔 ID: `%d`\n"+
			"🕐 Время: %s\n\n"+
			"📨 *Сообщение:*\n%s\n\n",
		displayName,
		userID,
		timestamp,
		message.Text,
	)

	b.logger.Info("User message received",
		slog.String("username", username),
		slog.Int64("user_id", userID),
		slog.String("message", message.Text))

	// Send message to all admins
	admins, err := b.db.GetAdminUsers()
	if err != nil {
		b.logger.Error("Failed to fetch admin users", slog.String("error", err.Error()))
		return
	}

	for _, admin := range admins {
		msg := tu.Message(
			tu.ID(*admin.TelegramID),
			forwardMessage,
		).WithParseMode(telego.ModeMarkdown)

		sentMsg, err := bot.SendMessage(msg)
		if err != nil {
			b.logger.Error("Failed to forward message to admin",
				slog.String("admin", admin.Username),
				slog.String("error", err.Error()))
		} else {
			// Update the database with admin chat message ID for the first admin
			if userMsg.AdminChatMsgID == nil {
				adminMsgID := sentMsg.MessageID
				userMsg.AdminChatMsgID = &adminMsgID
				if err := b.db.Conn.Save(userMsg).Error; err != nil {
					b.logger.Error("Failed to update message with admin chat ID",
						slog.String("error", err.Error()))
				}
			}
		}
	}

	// Send acknowledgment to user
	ackMsg := tu.Message(
		tu.ID(chatID),
		"Скоро отвечу",
	)
	_, err = bot.SendMessage(ackMsg)
	if err != nil {
		b.logger.Error("Failed to send acknowledgment to user",
			slog.String("username", username),
			slog.String("error", err.Error()))
	}
}

// handleAdminReply handles admin replies to user messages
func (b *Bot) handleAdminReply(bot *telego.Bot, update telego.Update) {
	if update.Message == nil || update.Message.ReplyToMessage == nil {
		return
	}

	message := update.Message
	chatID := message.Chat.ID
	adminID := message.From.ID
	adminUsername := message.From.Username

	// Check if sender is admin
	isAdmin, err := b.db.IsUserAdmin(adminID)
	if err != nil || !isAdmin {
		// Not an admin, ignore
		return
	}

	// Get the original message from database using the replied-to message ID
	repliedMsgID := update.Message.ReplyToMessage.MessageID
	originalMsg, err := b.db.GetUserMessageByAdminChatMsgID(repliedMsgID)
	if err != nil {
		b.logger.Warn("Could not find original user message",
			slog.Int("replied_msg_id", repliedMsgID),
			slog.String("error", err.Error()))

		// Send error to admin
		errorMsg := tu.Message(
			tu.ID(chatID),
			"⚠️ Не удалось найти оригинальное сообщение пользователя. Возможно, оно было отправлено до обновления бота.",
		)
		_, _ = bot.SendMessage(errorMsg)
		return
	}

	// Save admin reply to database
	adminReply := &database.UserMessage{
		UserID:        originalMsg.UserID,
		Username:      originalMsg.Username,
		AdminID:       &adminID,
		MessageText:   message.Text,
		IsAdminReply:  true,
		ReplyToID:     &originalMsg.ID,
		TelegramMsgID: message.MessageID,
		CreatedAt:     time.Now(),
	}

	if err := b.db.AddUserMessage(adminReply); err != nil {
		b.logger.Error("Failed to save admin reply",
			slog.String("error", err.Error()))
	}

	// Format reply message for user
	replyText := fmt.Sprintf(
		"📥 *Ответ от администратора:*\n\n%s",
		message.Text,
	)

	// Send reply to user
	userMsg := tu.Message(
		tu.ID(originalMsg.UserID),
		replyText,
	).WithParseMode(telego.ModeMarkdown)

	_, err = bot.SendMessage(userMsg)
	if err != nil {
		b.logger.Error("Failed to send reply to user",
			slog.Int64("user_id", originalMsg.UserID),
			slog.String("username", originalMsg.Username),
			slog.String("error", err.Error()))

		// Notify admin about failure
		errorMsg := tu.Message(
			tu.ID(chatID),
			fmt.Sprintf("❌ Не удалось отправить ответ пользователю @%s. Ошибка: %s",
				originalMsg.Username, err.Error()),
		)
		_, _ = bot.SendMessage(errorMsg)
		return
	}

	// Send confirmation to admin
	confirmMsg := tu.Message(
		tu.ID(chatID),
		fmt.Sprintf("✅ Ответ успешно отправлен пользователю @%s", originalMsg.Username),
	)
	_, err = bot.SendMessage(confirmMsg)
	if err != nil {
		b.logger.Error("Failed to send confirmation to admin",
			slog.String("error", err.Error()))
	}

	b.logger.Info("Admin reply sent",
		slog.String("admin", adminUsername),
		slog.String("to_user", originalMsg.Username),
		slog.Int64("user_id", originalMsg.UserID))

	// Notify other admins about the reply
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")
	notificationMsg := fmt.Sprintf(
		"✅ *Ответ отправлен*\n\n"+
			"👨‍💼 Администратор: @%s\n"+
			"👤 Пользователю: @%s (ID: `%d`)\n"+
			"📨 Ответ: %s\n"+
			"🕐 Время: %s",
		escapeMarkdown(adminUsername),
		escapeMarkdown(originalMsg.Username),
		originalMsg.UserID,
		escapeMarkdown(message.Text),
		timestamp,
	)

	admins, err := b.db.GetAdminUsers()
	if err == nil {
		for _, admin := range admins {
			// Don't notify the admin who sent the reply
			if *admin.TelegramID == adminID {
				continue
			}

			notifMsg := tu.Message(
				tu.ID(*admin.TelegramID),
				notificationMsg,
			).WithParseMode(telego.ModeMarkdown)

			_, err := bot.SendMessage(notifMsg)
			if err != nil {
				b.logger.Error("Failed to notify other admin",
					slog.String("admin", admin.Username),
					slog.String("error", err.Error()))
			}
		}
	}
}
