package telegram

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

func (b *Bot) handleAddServer(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Error handling add server command", slog.String("error", "update.Message == nil"))
	}
	message := update.Message
	chatID := message.Chat.ID
	userID := message.From.ID

	// Check if user is admin
	isAdmin, err := b.db.IsUserAdmin(userID)
	if err != nil || !isAdmin {
		msg := tu.Message(tu.ID(chatID), "У вас нет прав для выполнения этой команды.")
		_, _ = bot.SendMessage(msg)
		return
	}

	args := strings.Fields(message.Text)
	if len(args) < 10 {
		msg := tu.Message(tu.ID(chatID), "Использование: /add_server <Name> <Country> <City> <IP> <SSHPort> <SSHUser> <APIPort> <Username> <Password> [InboundID] [IsExclusive]")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Parse arguments
	name := args[1]
	country := args[2]
	city := args[3]
	ip := args[4]
	sshPort, err := strconv.Atoi(args[5])
	if err != nil {
		b.logger.Error("Invalid SSHPort", slog.String("error", err.Error()))
		return
	}
	sshUser := args[6]
	apiPort, err := strconv.Atoi(args[7])
	if err != nil {
		b.logger.Error("Invalid APIPort", slog.String("error", err.Error()))
		return
	}
	username := args[8]
	password := args[9]
	var inboundID *int
	if len(args) >= 11 {
		id, err := strconv.Atoi(args[10])
		if err != nil {
			b.logger.Error("Invalid InboundID", slog.String("error", err.Error()))
			return
		}
		inboundID = &id
	}
	isExclusive := false
	if len(args) >= 12 {
		isExclusiveArg := args[11]
		isExclusive = (isExclusiveArg == "true")
	}

	// Create Server object
	server := &database.Server{
		Name:        name,
		Country:     country,
		City:        city,
		IP:          ip,
		SSHPort:     sshPort,
		SSHUser:     sshUser,
		APIPort:     apiPort,
		Username:    username,
		Password:    password,
		InboundID:   inboundID,
		IsExclusive: isExclusive,
	}

	// Save server to database
	if err := b.db.AddServer(server); err != nil {
		b.logger.Error("Failed to add server", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось добавить сервер.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Connect to the server and set up the x3ui client
	_, err = b.serverHandler.GetClient(server)
	if err != nil {
		b.logger.Error("Failed to connect to server", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось подключиться к серверу.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// If InboundID is nil, create an inbound
	if server.InboundID == nil {
		inbound, err := b.serverHandler.CreateInbound(server)
		if err != nil {
			b.logger.Error("Failed to create inbound", slog.String("error", err.Error()))
			msg := tu.Message(tu.ID(chatID), "Не удалось создать исходящий прокси.")
			_, _ = bot.SendMessage(msg)
			return
		}
		// Update server with new InboundID
		if err := b.db.UpdateServerInboundID(server.ID, inbound.ID); err != nil {
			b.logger.Error("Failed to update server inbound ID", slog.String("error", err.Error()))
		}
	}

	msg := tu.Message(tu.ID(chatID), "Сервер успешно добавлен и настроен.")
	_, _ = bot.SendMessage(msg)
}
