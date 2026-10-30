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
	b.bh.Handle(b.handleSupport, th.CommandEqual("help"))
	b.bh.Handle(b.handleHelpSetup, th.CommandEqual("help_setup"))
	b.bh.Handle(b.handleBeta, th.CommandEqual("beta"))
	b.bh.Handle(b.handleStatus, th.CommandEqual("status"))
	b.bh.Handle(b.handleAccountStatus, th.CommandEqual("account"))
	b.bh.Handle(b.handleInvite, th.CommandEqual("invite"))

	b.bh.Handle(b.handleGetKey, th.CommandEqual("get_key"))
	b.bh.Handle(b.handleQR, th.CommandEqual("qr"))
	b.bh.Handle(b.handleQRSubscription, th.CommandEqual("qr_subscription"))
	b.bh.Handle(b.handleQRKey, th.CommandEqual("qr_key"))
	b.bh.Handle(b.handleQRCallback, th.CallbackDataPrefix("qr_"))

	// Handle callback queries from inline keyboards
	b.bh.Handle(b.handleHelpCallback, th.CallbackDataContains("help_"))

	b.bh.Handle(b.handleGetKeyCallback, th.CallbackDataContains("getkey_"))
	b.bh.Handle(b.handleIndividualKeys, th.CallbackDataEqual(CallbackIndividualKeys))
	b.bh.Handle(b.handleAccountStatusCallback, th.CallbackDataEqual(CallbackAccountStatus))
	b.bh.Handle(b.handleNetworkStatusCallback, th.CallbackDataPrefix(CallbackNetworkStatus))
	b.bh.Handle(b.handleManagedKey, th.CallbackDataPrefix(CallbackManagedKeyPrefix))
	b.bh.Handle(b.handleStockholmSubscription, th.CallbackDataEqual(CallbackStockholmSubscription))
	b.bh.Handle(b.handleStockholmGuide, th.CallbackDataEqual(CallbackStockholmGuide))
	b.bh.Handle(b.handleStockholmRoutingGuide, th.CallbackDataEqual(CallbackStockholmRouting))
	b.bh.Handle(b.handleStockholmHappRouting, th.CallbackDataEqual(CallbackStockholmHappRouting))
	b.bh.Handle(b.handleStockholmIncyRouting, th.CallbackDataEqual(CallbackStockholmIncyRouting))
	b.bh.Handle(b.handleStockholmHappRoutingLink, th.CallbackDataEqual(CallbackStockholmHappRoutingLink))
	b.bh.Handle(b.handleStockholmIncyRoutingLink, th.CallbackDataEqual(CallbackStockholmIncyRoutingLink))
}

// Handle /start command
func (b *Bot) handleStart(bot *telego.Bot, update telego.Update) {
	if !isPrivateActorUpdate(update) || update.Message == nil {
		return
	}
	chatID := update.Message.Chat.ID
	username := update.Message.From.Username

	// Notify admins about command usage
	b.NotifyAdminsOfCommand(username, chatID, "/start", "")

	if args := strings.Fields(update.Message.Text); len(args) == 2 {
		switch args[1] {
		case "beta":
			b.handleBeta(bot, update)
			return
		case "status":
			b.handleStatus(bot, update)
			return
		}
	}
	b.handleHelp(bot, update)
}

// Handle /invite command
func (b *Bot) handleInvite(bot *telego.Bot, update telego.Update) {
	chatID := update.Message.Chat.ID
	message := update.Message
	username := message.From.Username
	args := strings.Fields(message.Text)

	// Notify admins about command usage
	argsStr := ""
	if len(args) > 1 {
		argsStr = strings.Join(args[1:], " ")
	}
	b.NotifyAdminsOfCommand(username, chatID, "/invite", argsStr)

	if len(args) < 2 {
		msg := tu.Message(
			tu.ID(chatID),
			"Использование: /invite <username>",
		)
		_, _ = bot.SendMessage(msg)
		b.NotifyAdminsOfError(username, chatID, "/invite", "Неверные аргументы", "Пользователь не указал username для приглашения")
		return
	}

	invitedUsername := strings.TrimPrefix(strings.ToLower(args[1]), "@")

	// Check if the user already exists
	_, err := b.db.GetUserByUsername(invitedUsername)
	if err == nil {
		msg := tu.Message(
			tu.ID(chatID),
			"Этот пользователь уже зарегистрирован.",
		)
		_, _ = bot.SendMessage(msg)
		b.NotifyAdminsOfAction(username, chatID, "/invite", "Попытка пригласить уже существующего пользователя: @"+invitedUsername)
		return
	}

	// Add the new user as invited
	invitedUser := &database.User{
		Username:          invitedUsername,
		InvitedByID:       &chatID,
		InvitedByUsername: message.From.Username,
		Invited:           true,
	}

	if err := b.db.AddUser(invitedUser); err != nil {
		b.logger.Error("Failed to invite user", "error", err)
		msg := tu.Message(
			tu.ID(chatID),
			"Не удалось пригласить пользователя.",
		)
		_, _ = bot.SendMessage(msg)
		b.NotifyAdminsOfError(username, chatID, "/invite", err.Error(), "Не удалось добавить пользователя @"+invitedUsername+" в базу данных")
		return
	}

	// Notify admins about successful invitation
	b.NotifyAdminsOfAction(username, chatID, "/invite", "Успешно пригласил пользователя: @"+invitedUsername)

	msg := tu.Message(
		tu.ID(chatID),
		"Пользователь @"+invitedUsername+" приглашён и теперь может получить доступ к базовым серверам.",
	)
	_, err = bot.SendMessage(msg)
	if err != nil {
		b.logger.Error("Failed to send invite message", "error", err)
		b.NotifyAdminsOfError(username, chatID, "/invite", err.Error(), "Не удалось отправить подтверждающее сообщение")
	}
}
