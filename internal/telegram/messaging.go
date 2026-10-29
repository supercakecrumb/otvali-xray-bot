package telegram

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

type messageStore interface {
	GetUserByTelegramID(int64) (*database.User, error)
	GetAdminUsers() ([]database.User, error)
	IsUserAdmin(int64) (bool, error)
	AddUserMessage(*database.UserMessage) error
	AddUserMessageForward(*database.UserMessageForward) error
	GetUserMessageByAdminForward(int64, int64, int) (*database.UserMessage, error)
	GetLegacyUserMessageByAdminReply(int, int64) (*database.UserMessage, error)
}

func (b *Bot) messageStore() messageStore {
	if b.messages != nil {
		return b.messages
	}
	return b.db
}

// A legacy row has no admin chat ID. The old bot's canonical forward header is
// additional evidence of its recipient; a bare coinciding message ID is unsafe.
func (b *Bot) legacyReplyRecipient(message *telego.Message) int64 {
	if !b.legacyMessageFallback || b.ID() <= 0 || b.ID() != b.primaryBotID || message == nil || message.ReplyToMessage == nil {
		return 0
	}
	replied := message.ReplyToMessage
	if replied.From == nil || !replied.From.IsBot || replied.From.ID != b.primaryBotID || replied.Chat.ID != message.Chat.ID || replied.Chat.Type != "private" {
		return 0
	}
	lines := strings.SplitN(replied.Text, "\n", 8)
	if len(lines) != 8 || lines[0] != "💬 Сообщение от пользователя" || lines[1] != "" ||
		!strings.HasPrefix(lines[2], "👤 От: ") || !strings.HasPrefix(lines[3], "🆔 ID: ") ||
		!strings.HasPrefix(lines[4], "🕐 Время: ") || lines[5] != "" || lines[6] != "📨 Сообщение:" {
		return 0
	}
	encoded := strings.TrimPrefix(lines[3], "🆔 ID: ")
	id, err := strconv.ParseInt(encoded, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != encoded {
		return 0
	}
	// Names are user-controlled and may contain newlines. The actual recipient
	// was formatted as code; an apparent ID injected into a name is plain text.
	prefix := strings.Join(lines[:3], "\n") + "\n🆔 ID: "
	offset := len(utf16.Encode([]rune(prefix)))
	for _, entity := range replied.Entities {
		if entity.Type == "code" && entity.Offset == offset && entity.Length == len(encoded) {
			return id
		}
	}
	return 0
}

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
	if update.Message == nil || !isPrivateActorUpdate(update) || b.ID() <= 0 {
		return
	}

	message := update.Message
	userID := message.From.ID
	username := message.From.Username

	// Check if user exists in database
	store := b.messageStore()
	user, err := store.GetUserByTelegramID(userID)
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
		BotID:          b.ID(),
		TelegramChatID: message.Chat.ID,
		UserID:         userID,
		Username:       username,
		MessageText:    message.Text,
		IsAdminReply:   false,
		TelegramMsgID:  message.MessageID,
		CreatedAt:      time.Now(),
	}

	if err := store.AddUserMessage(userMsg); err != nil {
		b.logger.Error("Failed to save user message")
		return
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
	displayName = escapeMarkdown(displayName)
	safeMessageText := escapeMarkdown(message.Text)

	forwardMessage := fmt.Sprintf(
		// Keep this heading distinct from legacy copies: if saving a new
		// forward reference fails, it must never use the historical fallback.
		"💬 *Сообщение пользователя*\n\n"+
			"👤 От: %s\n"+
			"🆔 ID: `%d`\n"+
			"🕐 Время: %s\n\n"+
			"📨 *Сообщение:*\n%s\n\n",
		displayName,
		userID,
		timestamp,
		safeMessageText,
	)

	b.logger.Info("User message received",
		slog.String("username", username),
		slog.Int64("user_id", userID))

	// Send message to all admins
	admins, err := store.GetAdminUsers()
	if err != nil {
		b.logger.Error("Failed to fetch admin users")
		return
	}

	for _, admin := range admins {
		if admin.TelegramID == nil {
			b.logger.Warn("Admin missing TelegramID, skipping message forward", slog.String("username", admin.Username))
			continue
		}
		msg := tu.Message(
			tu.ID(*admin.TelegramID),
			forwardMessage,
		).WithParseMode(telego.ModeMarkdown)

		sentMsg, err := b.notificationSender().SendMessage(msg)
		if err != nil {
			b.logger.Error("Failed to forward message to admin",
				slog.String("admin", admin.Username))
		} else {
			forward := &database.UserMessageForward{BotID: b.primaryBotID, AdminChatID: *admin.TelegramID, TelegramMsgID: sentMsg.MessageID, UserMessageID: userMsg.ID}
			if err := store.AddUserMessageForward(forward); err != nil {
				b.logger.Error("Failed to save admin message reference")
			}
		}
	}
}

// handleAdminReply handles admin replies to user messages
func (b *Bot) handleAdminReply(bot *telego.Bot, update telego.Update) {
	if update.Message == nil || update.Message.ReplyToMessage == nil || !isPrivateActorUpdate(update) || b.ID() <= 0 {
		return
	}

	message := update.Message
	chatID := message.Chat.ID
	adminID := message.From.ID
	adminUsername := message.From.Username

	// Check if sender is admin
	store := b.messageStore()
	isAdmin, err := store.IsUserAdmin(adminID)
	if err != nil || !isAdmin {
		// Not an admin, ignore
		return
	}

	// Get the original message from database using the replied-to message ID
	repliedMsgID := update.Message.ReplyToMessage.MessageID
	originalMsg, err := store.GetUserMessageByAdminForward(b.ID(), chatID, repliedMsgID)
	if errors.Is(err, database.ErrUserNotFound) {
		if recipient := b.legacyReplyRecipient(message); recipient > 0 {
			originalMsg, err = store.GetLegacyUserMessageByAdminReply(repliedMsgID, recipient)
		}
	}
	if err != nil {
		b.logger.Warn("Could not find original user message",
			slog.Int("replied_msg_id", repliedMsgID))

		// Send error to admin
		errorMsg := tu.Message(
			tu.ID(chatID),
			"⚠️ Не удалось найти оригинальное сообщение пользователя. Возможно, оно было отправлено до обновления бота.",
		)
		_, _ = bot.SendMessage(errorMsg)
		return
	}
	originBot := b.senderForBotID(originalMsg.BotID)
	if originBot == nil {
		b.logger.Warn("Original message bot is unavailable")
		_, _ = bot.SendMessage(tu.Message(tu.ID(chatID), "⚠️ Бот, через который пришло сообщение, сейчас недоступен. Ответ не отправлен."))
		return
	}

	// Save admin reply to database
	adminReply := &database.UserMessage{
		BotID:          b.ID(),
		TelegramChatID: chatID,
		UserID:         originalMsg.UserID,
		Username:       originalMsg.Username,
		AdminID:        &adminID,
		MessageText:    message.Text,
		IsAdminReply:   true,
		ReplyToID:      &originalMsg.ID,
		TelegramMsgID:  message.MessageID,
		CreatedAt:      time.Now(),
	}

	if err := store.AddUserMessage(adminReply); err != nil {
		b.logger.Error("Failed to save admin reply")
		_, _ = bot.SendMessage(tu.Message(tu.ID(chatID), "⚠️ Не удалось сохранить ответ. Попробуйте ещё раз."))
		return
	}

	// Format reply message for user
	replyText := fmt.Sprintf(
		"📥 *Ответ от администратора:*\n\n%s",
		escapeMarkdown(message.Text),
	)

	// Send reply to user
	userMsg := tu.Message(
		tu.ID(originalMsg.UserID),
		replyText,
	).WithParseMode(telego.ModeMarkdown)
	if originalMsg.TelegramMsgID > 0 {
		replyParams := (&telego.ReplyParameters{}).
			WithMessageID(originalMsg.TelegramMsgID).
			WithAllowSendingWithoutReply()
		userMsg = userMsg.WithReplyParameters(replyParams)
	}

	_, err = originBot.SendMessage(userMsg)
	if err != nil {
		b.logger.Error("Failed to send reply to user",
			slog.Int64("user_id", originalMsg.UserID),
			slog.String("username", originalMsg.Username))

		// Notify admin about failure
		errorMsg := tu.Message(
			tu.ID(chatID),
			fmt.Sprintf("❌ Не удалось отправить ответ пользователю @%s. Попробуйте ещё раз.", originalMsg.Username),
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
		b.logger.Error("Failed to send confirmation to admin")
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

	admins, err := store.GetAdminUsers()
	if err == nil {
		for _, admin := range admins {
			if admin.TelegramID == nil {
				b.logger.Warn("Admin missing TelegramID, skipping reply notification", slog.String("username", admin.Username))
				continue
			}
			// Don't notify the admin who sent the reply
			if *admin.TelegramID == adminID {
				continue
			}

			notifMsg := tu.Message(
				tu.ID(*admin.TelegramID),
				notificationMsg,
			).WithParseMode(telego.ModeMarkdown)

			_, err := b.notificationSender().SendMessage(notifMsg)
			if err != nil {
				b.logger.Error("Failed to notify other admin",
					slog.String("admin", admin.Username))
			}
		}
	}
}
