package telegram

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

func (b *Bot) userUsernameMiddleware() th.Middleware {
	return func(bot *telego.Bot, update telego.Update, next th.Handler) {
		b.logger.Debug("Ensuring that user has username")
		if update.Message != nil && update.Message.From != nil && update.Message.From.Username == "" {
			bot.SendMessage(markdownMessage(update.Message.Chat.ChatID(), noUsernameResponse))
			return
		}
		next(bot, update)
	}
}

// Middleware to ensure user is registered and update user info
func (b *Bot) userDatabaseMiddleware() th.Middleware {
	return func(bot *telego.Bot, update telego.Update, next th.Handler) {
		var chatID int64
		var fromUser *telego.User

		if update.Message != nil {
			chatID = update.Message.Chat.ID
			fromUser = update.Message.From
		} else if update.CallbackQuery != nil {
			chatID = update.CallbackQuery.From.ID
			fromUser = &update.CallbackQuery.From
		} else {
			// Unsupported update, skip
			next(bot, update)
			return
		}

		if fromUser == nil {
			// No user info, cannot proceed
			b.logger.Warn("No user info in update")
			next(bot, update)
			return
		}

		telegramID := fromUser.ID
		username := strings.ToLower(fromUser.Username)
		b.logger.Debug("Ensuring that user is in database", slog.Int64("telegram ID", telegramID), slog.String("username", username))

		// Try to get user by TelegramID
		user, err := b.db.GetUserByTelegramID(telegramID)
		if err == nil {
			// User found by TelegramID
			// Update username if changed
			if user.Username != username {
				if err := b.db.UpdateUserUsername(user.ID, username); err != nil {
					b.logger.Error("Failed to update username", slog.String("error", err.Error()))
				}
			}
			// Proceed to next handler
			next(bot, update)
			return
		}

		if !errors.Is(err, database.ErrUserNotFound) {
			b.logger.Error("Error retrieving user by TelegramID", slog.String("error", err.Error()))
			next(bot, update)
			return
		}

		// User not found by TelegramID, try by Username
		user, err = b.db.GetUserByUsername(username)
		if err == nil {
			// User found by Username
			// Update TelegramID
			if user.TelegramID == nil {
				if err := b.db.UpdateUserTelegramID(user.ID, telegramID); err != nil {
					b.logger.Error("Failed to update TelegramID", slog.String("error", err.Error()))
				}
			}
			// Proceed to next handler
			next(bot, update)
			return
		}

		if !errors.Is(err, database.ErrUserNotFound) {
			b.logger.Error("Error retrieving user by Username", slog.String("error", err.Error()))
			next(bot, update)
			return
		}

		// User not found, send message that they must be invited first
		msg := tu.Message(
			tu.ID(chatID),
			youMustBeInvitedResponse,
		)
		_, _ = bot.SendMessage(msg)
	}
}
