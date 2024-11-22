package telegram

import (
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

func (b *Bot) registerCommands() {
	// Register command handlers
	b.bh.Handle(b.handleStart, th.CommandEqual("start"))
	b.bh.Handle(b.handleHelp, th.CommandEqual("help"))
	b.bh.Handle(b.handleInvite, th.CommandEqual("invite"))

	// Handle callback queries from inline keyboards
	b.bh.Handle(b.handleHelpCallback, th.CallbackDataContains("help_"))
}

// Handle /start command
func (b *Bot) handleStart(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID

	welcomeMessage := "Добро пожаловать! Используйте /help, чтобы узнать доступные команды."

	msg := tu.Message(
		tu.ID(chatID),
		welcomeMessage,
	)

	_, err := bot.SendMessage(msg)
	if err != nil {
		b.logger.Error("Failed to send start message", "error", err)
	}
}

// Handle /invite command
func (b *Bot) handleInvite(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID
	message := update.Message
	args := strings.Fields(message.Text)

	if len(args) < 2 {
		msg := tu.Message(
			tu.ID(chatID),
			"Использование: /invite <username>",
		)
		_, _ = bot.SendMessage(msg)
		return
	}

	invitedUsername := args[1]

	// Check if the user already exists
	_, err := b.db.GetUserByUsername(invitedUsername)
	if err == nil {
		msg := tu.Message(
			tu.ID(chatID),
			"Этот пользователь уже зарегистрирован.",
		)
		_, _ = bot.SendMessage(msg)
		return
	}

	// Add the new user as invited
	invitedUser := &database.User{
		Username:  invitedUsername,
		InvitedBy: &chatID,
	}

	if err := b.db.AddUser(invitedUser); err != nil {
		b.logger.Error("Failed to invite user", "error", err)
		msg := tu.Message(
			tu.ID(chatID),
			"Не удалось пригласить пользователя.",
		)
		_, _ = bot.SendMessage(msg)
		return
	}

	msg := tu.Message(
		tu.ID(chatID),
		"Пользователь @"+invitedUsername+" приглашён и теперь может получить доступ к базовым серверам.",
	)
	_, err = bot.SendMessage(msg)
	if err != nil {
		b.logger.Error("Failed to send invite message", "error", err)
	}
}
