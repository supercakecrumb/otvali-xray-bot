package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

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
	b.bh.Handle(b.handleDeleteUser, th.CommandEqual("delete_user"))
}

func (b *Bot) handleAddServer(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Error handling add server command", slog.String("error", "update.Message == nil"))
		return
	}
	message := update.Message
	chatID := message.Chat.ID
	userID := message.From.ID
	username := message.From.Username

	// Notify admins about command usage
	argsStr := strings.Join(strings.Fields(message.Text)[1:], " ")
	b.NotifyAdminsOfCommand(username, chatID, "/add_server", argsStr)

	// Check if user is admin
	isAdmin, err := b.db.IsUserAdmin(userID)
	if err != nil || !isAdmin {
		msg := tu.Message(tu.ID(chatID), "У вас нет прав для выполнения этой команды.")
		_, _ = bot.SendMessage(msg)
		b.NotifyAdminsOfError(username, chatID, "/add_server", "Нет прав администратора", "Попытка выполнить команду без прав")
		return
	}

	args := strings.Fields(message.Text)
	if len(args) < 10 {
		msg := tu.Message(tu.ID(chatID), "Использование: /add_server <Name> <Country> <City> <IP> <SSHPort> <SSHUser> <APIPort> <Username> <Password> [InboundID] [IsExclusive]")
		_, _ = bot.SendMessage(msg)
		b.NotifyAdminsOfError(username, chatID, "/add_server", "Недостаточно аргументов", fmt.Sprintf("Передано аргументов: %d, требуется минимум: 10", len(args)))
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
	x3uiUsername := args[8]
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
		Username:    x3uiUsername,
		Password:    password,
		InboundID:   inboundID,
		IsExclusive: isExclusive,
	}

	// Save server to database
	if err := b.db.AddServer(server); err != nil {
		b.logger.Error("Failed to add server", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось добавить сервер в базу данных.")
		_, _ = bot.SendMessage(msg)
		b.NotifyAdminsOfError(username, chatID, "/add_server", err.Error(), fmt.Sprintf("Не удалось добавить сервер %s в БД", name))
		return
	}

	// Connect to the server and set up the x3ui client
	_, err = b.sh.AddClient(server)
	if err != nil {
		b.logger.Error("Failed to connect to server", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Не удалось подключиться к серверу.")
		_, _ = bot.SendMessage(msg)
		b.NotifyAdminsOfError(username, chatID, "/add_server", err.Error(), fmt.Sprintf("Не удалось подключиться к серверу: %s (%s)", name, ip))
		return
	}

	// If InboundID is nil, create an inbound
	if server.InboundID == nil {
		inbound, err := b.sh.CreateInbound(server)
		if err != nil {
			b.logger.Error("Failed to create inbound", slog.String("error", err.Error()))
			msg := tu.Message(tu.ID(chatID), "Не удалось создать исходящий прокси.")
			_, _ = bot.SendMessage(msg)
			b.NotifyAdminsOfError(username, chatID, "/add_server", err.Error(), fmt.Sprintf("Не удалось создать inbound для сервера: %s", name))
			return
		}
		// Update server with new InboundID
		server.InboundID = &inbound.ID
		if err := b.db.UpdateServerInboundID(server.ID, inbound.ID); err != nil {
			b.logger.Error("Failed to update server inbound ID", slog.String("error", err.Error()))
		}
	}

	// Notify admins about successful server addition
	serverInfo := fmt.Sprintf("%s (%s, %s) - IP: %s, Exclusive: %t", name, country, city, ip, isExclusive)
	b.NotifyAdminsOfAction(username, chatID, "/add_server", "Успешно добавлен сервер: "+serverInfo)

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
	username := update.Message.From.Username

	// Notify admins about command usage
	b.NotifyAdminsOfCommand(username, chatID, "/list_servers", "")

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
		b.NotifyAdminsOfError(username, chatID, "/list_servers", err.Error(), "Не удалось получить список серверов из БД")
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
	username := message.From.Username

	// Notify admins about command usage
	argsStr := strings.Join(strings.Fields(message.Text)[1:], " ")
	b.NotifyAdminsOfCommand(username, chatID, "/server_exclusivity", argsStr)

	// Check if user is admin
	isAdmin, err := b.db.IsUserAdmin(userID)
	if err != nil || !isAdmin {
		msg := tu.Message(tu.ID(chatID), "У вас нет прав для выполнения этой команды.")
		_, _ = bot.SendMessage(msg)
		return
	}

	args := strings.Fields(message.Text)
	if len(args) < 3 {
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
		b.NotifyAdminsOfError(username, chatID, "/server_exclusivity", err.Error(), fmt.Sprintf("Не удалось обновить эксклюзивность сервера ID: %d", serverID))
		return
	}

	// Notify admins about the change
	b.NotifyAdminsOfAction(username, chatID, "/server_exclusivity", fmt.Sprintf("Изменена эксклюзивность сервера '%s' (ID: %d) на: %t", server.Name, serverID, isExclusive))

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
	username := message.From.Username

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

	// Notify admins about broadcast
	b.NotifyAdminsOfCommand(username, chatID, "/send_to_all", fmt.Sprintf("Текст: '%s'", text))

	users, err := b.db.GetAllUsers()
	if err != nil {
		b.logger.Error("Не удалось получить пользователей", slog.String("error", err.Error()))
		msg := tu.Message(tu.ID(chatID), "Ошибка при получении списка пользователей.")
		_, _ = bot.SendMessage(msg)
		return
	}

	if len(users) == 0 {
		msg := tu.Message(tu.ID(chatID), "Нет пользователей для отправки сообщений.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Function to send message with retry
	sendWithRetry := func(chatID int64, text string, maxRetries int) (*telego.Message, error) {
		var lastErr error
		for i := 0; i < maxRetries; i++ {
			msg := tu.Message(tu.ID(chatID), text)
			resp, err := bot.SendMessage(msg)
			if err == nil {
				return resp, nil
			}
			lastErr = err
			b.logger.Warn("Retry sending message",
				slog.Int("attempt", i+1),
				slog.String("error", err.Error()))
			time.Sleep(2 * time.Second) // Wait before retry
		}
		return nil, lastErr
	}

	// Send initial status message with retry
	_, err = sendWithRetry(chatID, "Начинаю отправку сообщений...", 3)
	if err != nil {
		b.logger.Error("Failed to send initial status message after retries",
			slog.String("error", err.Error()))
		// Continue anyway
	}

	successCount := 0
	failCount := 0
	statusUpdates := []string{}

	// Process users in smaller batches with longer delays
	batchSize := 5 // Reduced batch size
	currentBatch := 0

	for i, user := range users {
		// Skip users without TelegramID
		if user.TelegramID == nil {
			b.logger.Warn("Skipping user without TelegramID", slog.String("username", user.Username))
			continue
		}

		// Ensure username has @ prefix
		username := user.Username
		if !strings.HasPrefix(username, "@") {
			username = "@" + username
		}

		// Try to send message to user with retry
		_, err := sendWithRetry(*user.TelegramID, text, 2)

		statusText := ""
		if err != nil {
			// Failed to send message
			failCount++
			errorMsg := err.Error()
			statusText = fmt.Sprintf("🔴 %s: Ошибка отправки (%s)", username, errorMsg)
			b.logger.Error("Failed to send message to user after retries",
				slog.String("username", username),
				slog.String("error", errorMsg))
		} else {
			// Successfully sent message
			successCount++
			statusText = fmt.Sprintf("🟢 %s: Сообщение успешно отправлено", username)
		}

		// Add status to the batch
		statusUpdates = append(statusUpdates, statusText)
		currentBatch++

		// Send batch update when we reach batch size or at the end
		if currentBatch >= batchSize || i == len(users)-1 {
			if len(statusUpdates) > 0 {
				batchText := strings.Join(statusUpdates, "\n")
				_, err = sendWithRetry(chatID, batchText, 3)
				if err != nil {
					b.logger.Error("Failed to send batch status update after retries",
						slog.String("error", err.Error()))
				}

				// Reset for next batch
				statusUpdates = []string{}
				currentBatch = 0

				// Add a longer delay to avoid rate limiting
				time.Sleep(3 * time.Second)
			}
		}
	}

	// Send summary message with retry
	summaryText := fmt.Sprintf("Отправка завершена. Успешно: %d 🟢, Ошибок: %d 🔴, Всего: %d",
		successCount, failCount, len(users))
	_, err = sendWithRetry(chatID, summaryText, 3)
	if err != nil {
		b.logger.Error("Failed to send summary message after retries",
			slog.String("error", err.Error()))
	}

	// Notify admins about broadcast completion
	b.NotifyAdminsOfAction(username, chatID, "/send_to_all", fmt.Sprintf("Рассылка завершена. Успешно: %d, Ошибок: %d, Всего: %d", successCount, failCount, len(users)))
}

func (b *Bot) handleUsers(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Ошибка обработки users", slog.String("error", "update.Message == nil"))
		return
	}

	chatID := update.Message.Chat.ID
	userID := update.Message.From.ID
	username := update.Message.From.Username

	// Notify admins about command usage
	b.NotifyAdminsOfCommand(username, chatID, "/users", "")

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
		msgText = append(msgText, fmt.Sprintf("%v: @%v invited by: @%v", user.ID, user.Username, user.InvitedByUsername))
	}

	msg := tu.Message(tu.ID(chatID), strings.Join(msgText, "\n"))
	_, _ = bot.SendMessage(msg)
}

func (b *Bot) handleDeleteUser(bot *telego.Bot, update telego.Update) {
	if update.Message == nil {
		b.logger.Error("Error handling delete_user command", slog.String("error", "update.Message == nil"))
		return
	}

	message := update.Message
	chatID := message.Chat.ID
	userID := message.From.ID
	username := message.From.Username

	// Notify admins about command usage
	argsStr := strings.Join(strings.Fields(message.Text)[1:], " ")
	b.NotifyAdminsOfCommand(username, chatID, "/delete_user", argsStr)

	// Check if the user is an admin
	isAdmin, err := b.db.IsUserAdmin(userID)
	if err != nil || !isAdmin {
		msg := tu.Message(tu.ID(chatID), "You do not have permission to execute this command.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// Parse user ID
	args := strings.Fields(message.Text)
	if len(args) < 2 {
		msg := tu.Message(tu.ID(chatID), "Usage: /delete_user <user_id>")
		_, _ = bot.SendMessage(msg)
		return
	}

	deleteUserID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		msg := tu.Message(tu.ID(chatID), "Invalid user ID. It must be a number.")
		_, _ = bot.SendMessage(msg)
		return
	}

	// A bot-row deletion alone does not revoke a downloaded VPN credential.
	// Preserve the account until its separate Stockholm grant is disabled.
	target, lookupErr := b.db.GetUserByID(deleteUserID)
	if lookupErr == nil && target.TelegramID != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		checkErr := checkStockholmDeletion(ctx, *target.TelegramID, b.db, b.stockholm)
		cancel()
		if checkErr != nil {
			_, _ = bot.SendMessage(tu.Message(tu.ID(chatID), "Данные пользователя не удалены: сначала отключите его доступ к Стокгольму в Remnawave. Если доступ уже отключён, проверьте соединение с панелью и повторите команду."))
			return
		}
	}
	if lookupErr != nil && !errors.Is(lookupErr, database.ErrUserNotFound) {
		_, _ = bot.SendMessage(tu.Message(tu.ID(chatID), "Не удалось проверить пользователя. Данные не удалены."))
		return
	}

	// Delete user from database
	err = b.db.DeleteUserByID(deleteUserID)
	if err != nil {
		msg := ""
		if errors.Is(err, database.ErrUserNotFound) {
			msg = fmt.Sprintf("User with ID %d not found.", deleteUserID)
			b.NotifyAdminsOfError(username, chatID, "/delete_user", err.Error(), fmt.Sprintf("Пользователь с ID %d не найден", deleteUserID))
		} else {
			b.logger.Error("Error deleting user", slog.String("error", err.Error()))
			msg = fmt.Sprintf("Error while deleting user from the database: %v.", err.Error())
			b.NotifyAdminsOfError(username, chatID, "/delete_user", err.Error(), fmt.Sprintf("Не удалось удалить пользователя с ID %d", deleteUserID))
		}
		_, _ = bot.SendMessage(tu.Message(message.Chat.ChatID(), msg))
		return
	}

	// Notify admins about user deletion
	b.NotifyAdminsOfAction(username, chatID, "/delete_user", fmt.Sprintf("Удалён пользователь с ID: %d", deleteUserID))

	// Send success message
	msg := tu.Message(tu.ID(chatID), fmt.Sprintf("User with ID %d has been successfully deleted.", deleteUserID))
	_, _ = bot.SendMessage(msg)
}
