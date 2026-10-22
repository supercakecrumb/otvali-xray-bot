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
	return b.userDatabaseMiddlewareWithStore(b.db)
}

type userMiddlewareStore interface {
	GetUserByTelegramID(int64) (*database.User, error)
	GetUserByUsername(string) (*database.User, error)
	UpdateUserUsername(int64, string) error
	UpdateUserTelegramID(int64, int64) error
}

func (b *Bot) userDatabaseMiddlewareWithStore(store userMiddlewareStore) th.Middleware {
	return func(bot *telego.Bot, update telego.Update, next th.Handler) {
		var fromUser *telego.User

		if update.Message != nil {
			fromUser = update.Message.From
		} else if update.CallbackQuery != nil {
			fromUser = &update.CallbackQuery.From
		} else {
			// Unsupported update, skip
			next(bot, update)
			return
		}

		if fromUser == nil {
			// No user info, cannot proceed
			b.logger.Warn("No user info in update")
			return
		}

		telegramID := fromUser.ID
		username := strings.ToLower(fromUser.Username)
		b.logger.Debug("Ensuring that user is in database", slog.Int64("telegram ID", telegramID), slog.String("username", username))

		// Try to get user by TelegramID
		user, err := store.GetUserByTelegramID(telegramID)
		if err == nil {
			// User found by TelegramID
			// Update username if changed
			if user.Username != username {
				if err := store.UpdateUserUsername(user.ID, username); err != nil {
					b.logger.Error("Failed to update username", slog.String("error", err.Error()))
				}
			}
			// Proceed to next handler
			next(bot, update)
			return
		}

		if !errors.Is(err, database.ErrUserNotFound) {
			b.logger.Warn("User lookup unavailable")
			b.rejectUserUpdate(bot, update, "Сервис временно недоступен. Попробуйте позже.")
			return
		}

		// User not found by TelegramID, try by Username
		user, err = store.GetUserByUsername(username)
		if err == nil {
			// User found by Username
			if user.TelegramID != nil && *user.TelegramID != telegramID {
				b.rejectUserUpdate(bot, update, "Для этого аккаунта доступа нет. Напишите администратору.")
				return
			}
			// Update TelegramID
			if user.TelegramID == nil {
				if err := store.UpdateUserTelegramID(user.ID, telegramID); err != nil {
					b.logger.Warn("User binding unavailable")
					b.rejectUserUpdate(bot, update, "Сервис временно недоступен. Попробуйте позже.")
					return
				}
			}
			// Proceed to next handler
			next(bot, update)
			return
		}

		if !errors.Is(err, database.ErrUserNotFound) {
			b.logger.Warn("User lookup unavailable")
			b.rejectUserUpdate(bot, update, "Сервис временно недоступен. Попробуйте позже.")
			return
		}

		// User not found, send message that they must be invited first
		b.rejectUserUpdate(bot, update, youMustBeInvitedResponse)
	}
}

// Only rejection paths acknowledge here; accepted callbacks are acknowledged by
// their handler. This keeps exactly one answer without widening authorization.
func (b *Bot) rejectUserUpdate(bot *telego.Bot, update telego.Update, text string) {
	if !isPrivateActorUpdate(update) {
		return
	}
	if update.CallbackQuery != nil {
		if err := bot.AnswerCallbackQuery(&telego.AnswerCallbackQueryParams{CallbackQueryID: update.CallbackQuery.ID, Text: text, ShowAlert: true}); err != nil {
			b.logger.Warn("Failed to acknowledge unavailable account")
		}
		return
	}
	if _, err := bot.SendMessage(tu.Message(tu.ID(update.Message.From.ID), text)); err != nil {
		b.logger.Warn("Failed to deliver account response")
	}
}

// privateChatMiddleware keeps account and credential operations in the actor's DM.
func (b *Bot) privateChatMiddleware() th.Middleware {
	return func(bot *telego.Bot, update telego.Update, next th.Handler) {
		if !isPrivateActorUpdate(update) {
			return
		}
		next(bot, update)
	}
}

func isPrivateActorUpdate(update telego.Update) bool {
	if update.Message != nil {
		return update.Message.From != nil && update.Message.Chat.Type == "private" && update.Message.Chat.ID == update.Message.From.ID
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
		chat := update.CallbackQuery.Message.GetChat()
		return chat.Type == "private" && chat.ID == update.CallbackQuery.From.ID
	}
	return false
}
