package telegram

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

func (b *Bot) registerAdminCommands() {
	b.bh.Handle(b.handleAddServer, th.CommandEqual("add_server"))
	b.bh.Handle(b.handleListServers, th.CommandEqual("list_servers"))
	b.bh.Handle(b.handleServerExclusivity, th.CommandEqual("server_exclusivity"))
	b.bh.Handle(b.handleSendToAll, th.CommandEqual("send_to_all"))
	b.bh.Handle(b.handleUsers, th.CommandEqual("users"))
}

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

	// Connect to the server and set up the x3ui client
	_, err = b.sh.AddClient(server)
	if err != nil {
		b.logger.Error("Failed to connect to server", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось подключиться к серверу.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// If InboundID is nil, create an inbound
	if server.InboundID == nil {
		inbound, err := b.sh.CreateInbound(server)
		if err != nil {
			b.logger.Error("Failed to create inbound", slog.String("error", err.Error()))
			msg := tu.Message(tu.ID(chatID), "Не удалось создать исходящий прокси.")
			_, _ = bot.SendMessage(msg)
			return
		}
		// Update server with new InboundID
		server.InboundID = &inbound.ID
	}

	// Save server to database
	if err := b.db.AddServer(server); err != nil {
		b.logger.Error("Failed to add server", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось добавить сервер в базу данных.")
		_, _ = bot.SendMessage(msg)
		return
	}

	msg := tu.Message(tu.ID(chatID), "Сервер успешно добавлен и настроен.")
	_, _ = bot.SendMessage(msg)
}

func (b *Bot) handleListServers(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Error handling list servers command", slog.String("error", "update.Message == nil"))
		return
	}

	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	// Check if user is an admin
	isAdmin, err := b.db.IsUserAdmin(userID)
	if err != nil || !isAdmin {
		msg := tu.Message(tu.ID(chatID), "У вас нет прав для выполнения этой команды.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Fetch servers from the database
	servers, err := b.db.GetAllServers()
	if err != nil {
		b.logger.Error("Failed to fetch servers", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось получить список серверов.")
		_, _ = bot.SendMessage(msg)
		return
	}

	if len(servers) == 0 {
		msg := tu.Message(tu.ID(chatID), "Нет добавленных серверов.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Create a message listing all servers
	var sb strings.Builder
	sb.WriteString("Список серверов:\n\n")
	for _, server := range servers {
		sb.WriteString(fmt.Sprintf(
			"ID: %d\nИмя: %s\nСтрана: %s\nГород: %s\nIP: %s\nИсключительный: %t\n\n",
			server.ID, server.Name, server.Country, server.City, server.IP, server.IsExclusive,
		))
	}

	msg := tu.Message(tu.ID(chatID), sb.String())
	_, _ = bot.SendMessage(msg)
}

func (b *Bot) handleServerExclusivity(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Error handling server_exclusivity command", slog.String("error", "update.Message == nil"))
		return
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
	if len(args) < 2 {
		msg := tu.Message(tu.ID(chatID), "Использование: /server_exclusivity <ServerID> <true/false>")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Parse ServerID and exclusivity
	serverID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		b.logger.Error("Invalid ServerID", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "ID сервера должен быть числом.")
		_, _ = bot.SendMessage(msg)
		return
	}

	isExclusive, err := strconv.ParseBool(args[2])
	if err != nil {
		b.logger.Error("Invalid exclusivity value", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Значение эксклюзивности должно быть 'true' или 'false'.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Fetch the server
	server, err := b.db.GetServerByID(serverID)
	if err != nil {
		if errors.Is(err, database.ErrServerNotFound) {
			msg := tu.Message(tu.ID(chatID), fmt.Sprintf("Сервер с ID %d не найден.", serverID))
			_, _ = bot.SendMessage(msg)
			return
		}
		b.logger.Error("Failed to fetch server", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Ошибка при получении данных сервера.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Update the server exclusivity
	if err := b.db.UpdateServerExclusivity(server.ID, isExclusive); err != nil {
		b.logger.Error("Failed to update server exclusivity", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Ошибка при обновлении эксклюзивности сервера.")
		_, _ = bot.SendMessage(msg)
		return
	}

	msg := tu.Message(tu.ID(chatID), fmt.Sprintf("Эксклюзивность сервера '%s' установлена в '%t'.", server.Name, isExclusive))
	_, _ = bot.SendMessage(msg)
}

func (b *Bot) handleSendToAll(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Ошибка обработки send_to_all", slog.String("error", "update.Message == nil"))
		return
	}

	message := update.Message
	chatID := message.Chat.ID
	userID := message.From.ID

	isAdmin, err := b.db.IsUserAdmin(userID)
	if err != nil || !isAdmin {
		msg := tu.Message(tu.ID(chatID), "У вас нет прав для выполнения этой команды.")
		_, _ = bot.SendMessage(msg)
		return
	}

	args := strings.Fields(message.Text)
	if len(args) < 2 {
		msg := tu.Message(tu.ID(chatID), "Использование: /send_to_all <текст>")
		_, _ = bot.SendMessage(msg)
		return
	}

	text := strings.Join(args[1:], " ")

	users, err := b.db.GetAllUsers()
	if err != nil {
		b.logger.Error("Не удалось получить пользователей", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Ошибка при получении списка пользователей.")
		_, _ = bot.SendMessage(msg)
		return
	}

	for _, user := range users {
		msg := tu.Message(tu.ID(*user.TelegramID), text)
		_, _ = bot.SendMessage(msg)
	}

	msg := tu.Message(tu.ID(chatID), fmt.Sprintf("Сообщение отправлено %d пользователям.", len(users)))
	_, _ = bot.SendMessage(msg)
}

func (b *Bot) handleUsers(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Ошибка обработки users", slog.String("error", "update.Message == nil"))
		return
	}

	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID

	isAdmin, err := b.db.IsUserAdmin(userID)
	if err != nil || !isAdmin {
		msg := tu.Message(tu.ID(chatID), "У вас нет прав для выполнения этой команды.")
		_, _ = bot.SendMessage(msg)
		return
	}

	users, err := b.db.GetAllUsers()
	if err != nil {
		b.logger.Error("Ошибка получения пользователей", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Ошибка при получении количества пользователей.")
		_, _ = bot.SendMessage(msg)
		return
	}

	msgText := []string{fmt.Sprintf("Количество пользователей: %d", len(users))}

	for _, user := range users {
		msgText = append(msgText, fmt.Sprintf("@%v invited by: @%v", user.Username, user.InvitedByUsername))
	}

	msg := tu.Message(tu.ID(chatID), strings.Join(msgText, "\n"))
	_, _ = bot.SendMessage(msg)
}
