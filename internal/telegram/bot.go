package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
	"github.com/supercakecrumb/otvali-xray-bot/internal/x3ui"
)

type Bot struct {
	bot                   *telego.Bot
	botID                 int64
	legacyActivityBotID   int64
	pollingLastOK         atomic.Int64
	pollingStartedAt      atomic.Int64
	pollingFailures       atomic.Int64
	pollingRetryUntil     atomic.Int64
	pollingFailed         atomic.Bool
	primaryBotID          int64
	legacyMessageFallback bool
	notificationBot       *telego.Bot
	peerBots              map[int64]*telego.Bot
	lifecycleMu           sync.Mutex
	cancel                context.CancelFunc
	started               bool
	stopRequested         bool
	logger                *slog.Logger
	db                    *database.DB
	bh                    *th.BotHandler
	sh                    *x3ui.ServerHandler
	stockholm             StockholmIssuer
	telegramProxy         TelegramProxyIssuer
	proxyAdminStore       telegramProxyAdminStore
	proxyRevokeMu         sync.Mutex
	proxyRevokePreviews   map[string]telegramProxyRevokePreview
	vpnHelpURL            string
	miniAppURL            string
	accountStatus         AccountStatusReader
	qrStore               legacyQRStore
	qrLegacy              legacyQRIssuer
	activity              activityRecorder
	messages              messageStore
	networkStatus         NetworkStatusReader
	monitorOwnerID        int64
	monitorOwnerCheck     func(context.Context, int64) bool
	monitorAdminCheck     func(context.Context, int64) bool
	monitorBot            *telego.Bot
}

type Option func(*Bot)

// Telegram errors may echo credential-bearing request or response content.
// Keep the transport logger independent from handler logs and payload-free.
type telegramAPILogger struct{ logger *slog.Logger }

func (telegramAPILogger) Debugf(string, ...any) {}
func (l telegramAPILogger) Errorf(string, ...any) {
	l.logger.Warn("Telegram API request failed")
}

func WithStockholm(issuer StockholmIssuer) Option {
	return func(b *Bot) { b.stockholm = issuer }
}

func NewBot(token string, logger *slog.Logger, db *database.DB, serverHandler *x3ui.ServerHandler, options ...Option) (*Bot, error) {
	bot, err := telego.NewBot(token, telego.WithLogger(telegramAPILogger{logger: logger}), telego.WithHTTPClient(&http.Client{Timeout: 15 * time.Second}))
	if err != nil {
		return nil, err
	}
	botID, err := strconv.ParseInt(strings.SplitN(token, ":", 2)[0], 10, 64)
	if err != nil || botID <= 0 {
		return nil, fmt.Errorf("invalid Telegram bot identity")
	}

	b := &Bot{
		bot:                   bot,
		botID:                 botID,
		primaryBotID:          botID,
		legacyMessageFallback: true,
		logger:                logger,
		db:                    db,
		sh:                    serverHandler,
	}
	if db != nil {
		b.qrStore = db
		b.activity = db
	}
	if serverHandler != nil {
		b.qrLegacy = serverHandler
	}
	for _, option := range options {
		if option != nil {
			option(b)
		}
	}
	if b.networkStatus != nil {
		b.monitorBot, err = telego.NewBot(token, telego.WithHTTPClient(&http.Client{Timeout: 15 * time.Second}), telego.WithLogger(telegramAPILogger{logger: logger}))
		if err != nil {
			return nil, err
		}
	}
	return b, nil
}

// ID is the public numeric bot identity; it does not expose its credential and
// remains stable when a token is rotated.
func (b *Bot) ID() int64 { return b.botID }

func (b *Bot) SetLegacyActivityBotID(id int64) { b.legacyActivityBotID = id }

// ConfigurePeers is called once, before polling starts. Administrative messages
// keep using the legacy primary bot; replies can return through their origin bot.
func (b *Bot) ConfigurePeers(primary *Bot, peers []*Bot) {
	b.primaryBotID = primary.ID()
	b.notificationBot = primary.bot
	b.legacyMessageFallback = b.ID() == primary.ID()
	b.peerBots = make(map[int64]*telego.Bot, len(peers))
	for _, peer := range peers {
		b.peerBots[peer.ID()] = peer.bot
	}
}

func (b *Bot) notificationSender() *telego.Bot {
	if b.notificationBot != nil {
		return b.notificationBot
	}
	return b.bot
}

func (b *Bot) senderForBotID(id int64) *telego.Bot {
	if id == 0 {
		return b.notificationSender()
	}
	if id == b.ID() {
		return b.bot
	}
	return b.peerBots[id]
}

func (b *Bot) Start() { b.StartContext(context.Background()) }

func (b *Bot) StartContext(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	b.lifecycleMu.Lock()
	if b.started || b.stopRequested {
		b.lifecycleMu.Unlock()
		cancel()
		return
	}
	b.started, b.cancel = true, cancel
	b.lifecycleMu.Unlock()
	defer cancel()
	defer b.pollingLastOK.Store(0)
	if ctx.Err() != nil {
		return
	}
	b.logger.Info("Starting bot", "bot_id", b.ID())

	if b.ID() == b.primaryBotID {
		b.NotifyAdmins("⚠️ The bot is starting.")
		defer func() {
			// Do not delay a failed poller's exit with more requests through the
			// same unavailable identity. Main must stop all peer pollers promptly.
			if !b.pollingFailed.Load() {
				b.NotifyAdmins("⚠️ The bot is stopping. Please check the server for details.")
			}
		}()
	}

	updates := b.updatesViaPolling(ctx)

	// Create bot handler and specify from where to get updates
	var err error
	b.bh, err = th.NewBotHandler(b.bot, updates)
	if err != nil {
		b.logger.Error("Failed to create new bot handler", "bot_id", b.ID())
		return
	}

	defer func() {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		b.bh.StopWithContext(shutdownCtx)
	}()

	// Middleware in case of panic and no username
	b.bh.Use(
		th.PanicRecovery(),
		b.privateChatMiddleware(),
		b.userUsernameMiddleware(),
		b.userDatabaseMiddleware(),
		b.activityMiddleware(),
	)

	b.registerCommands()

	b.registerAdminCommands()

	b.registerMessagingHandlers()

	b.bh.Start()
}

func (b *Bot) Stop() {
	b.lifecycleMu.Lock()
	b.stopRequested = true
	cancel := b.cancel
	b.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// NotifyAdmins sends a message to all admins
func (b *Bot) NotifyAdmins(message string) {
	if b.db == nil {
		return
	}
	admins, err := b.db.GetAdminUsers() // Fetch admin users from the database
	if err != nil {
		b.logger.Error("Failed to fetch admin users", slog.String("error", err.Error()))
		return
	}

	for _, admin := range admins {
		if admin.TelegramID == nil {
			b.logger.Warn("Admin missing TelegramID, skipping notification", slog.String("username", admin.Username))
			continue
		}
		_, err := b.notificationSender().SendMessage(tu.Message(
			tu.ID(*admin.TelegramID),
			message,
		))
		if err != nil {
			b.logger.Error("Failed to notify admin", slog.String("username", admin.Username), slog.String("error", err.Error()))
		} else {
			b.logger.Info("Notified admin", slog.String("username", admin.Username))
		}
	}
}

// NotifyAdminsOfAction sends a structured notification about a user action
func (b *Bot) NotifyAdminsOfAction(username string, chatID int64, action string, details string) {
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	message := fmt.Sprintf(
		"✅ *Действие пользователя*\n\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Chat ID: `%d`\n"+
			"⚡ Действие: %s\n"+
			"📝 Детали: %s\n"+
			"🕐 Время: %s",
		escapeMarkdown(username),
		chatID,
		escapeMarkdown(action),
		escapeMarkdown(details),
		timestamp,
	)

	b.logger.Info("User action",
		slog.String("username", username),
		slog.Int64("chat_id", chatID),
		slog.String("action", action),
		slog.String("details", details),
	)

	b.sendFormattedNotification(message)
}

// NotifyAdminsOfError sends a structured notification about an error
func (b *Bot) NotifyAdminsOfError(username string, chatID int64, action string, errorMsg string, context string) {
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	message := fmt.Sprintf(
		"❌ *Ошибка пользователя*\n\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Chat ID: `%d`\n"+
			"⚡ Действие: %s\n"+
			"📝 Контекст: %s\n"+
			"🚨 Ошибка: `%s`\n"+
			"🕐 Время: %s",
		escapeMarkdown(username),
		chatID,
		escapeMarkdown(action),
		escapeMarkdown(context),
		errorMsg,
		timestamp,
	)

	b.logger.Error("User error",
		slog.String("username", username),
		slog.Int64("chat_id", chatID),
		slog.String("action", action),
		slog.String("context", context),
		slog.String("error", errorMsg),
	)

	b.sendFormattedNotification(message)
}

// NotifyAdminsOfCommand sends a notification about a command execution
func (b *Bot) NotifyAdminsOfCommand(username string, chatID int64, command string, args string) {
	// Command arguments can contain panel passwords and private messages.
	args = "[redacted]"
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	argsText := "нет"
	if args != "" {
		argsText = args
	}

	message := fmt.Sprintf(
		"⚡ *Команда выполнена*\n\n"+
			"👤 Пользователь: @%s\n"+
			"🆔 Chat ID: `%d`\n"+
			"💬 Команда: `%s`\n"+
			"📋 Аргументы: %s\n"+
			"🕐 Время: %s",
		escapeMarkdown(username),
		chatID,
		command,
		escapeMarkdown(argsText),
		timestamp,
	)

	b.logger.Info("Command executed",
		slog.String("username", username),
		slog.Int64("chat_id", chatID),
		slog.String("command", command),
		slog.String("args", args),
	)

	b.sendFormattedNotification(message)
}

// NotifyAdminsOfKeyRequest sends a notification about a key request
func (b *Bot) NotifyAdminsOfKeyRequest(username string, chatID int64, serverName string, success bool, errorMsg string) {
	timestamp := time.Now().In(time.FixedZone("MSK", 3*60*60)).Format("2006-01-02 15:04:05 MSK")

	var message string
	if success {
		message = fmt.Sprintf(
			"🔑 *Ключ выдан*\n\n"+
				"👤 Пользователь: @%s\n"+
				"🆔 Chat ID: `%d`\n"+
				"🖥 Сервер: %s\n"+
				"✅ Статус: Успешно\n"+
				"🕐 Время: %s",
			escapeMarkdown(username),
			chatID,
			escapeMarkdown(serverName),
			timestamp,
		)

		b.logger.Info("Key generated successfully",
			slog.String("username", username),
			slog.Int64("chat_id", chatID),
			slog.String("server", serverName),
		)
	} else {
		message = fmt.Sprintf(
			"🔑 *Ошибка выдачи ключа*\n\n"+
				"👤 Пользователь: @%s\n"+
				"🆔 Chat ID: `%d`\n"+
				"🖥 Сервер: %s\n"+
				"❌ Статус: Ошибка\n"+
				"🚨 Ошибка: `%s`\n"+
				"🕐 Время: %s",
			escapeMarkdown(username),
			chatID,
			escapeMarkdown(serverName),
			errorMsg,
			timestamp,
		)

		b.logger.Error("Key generation failed",
			slog.String("username", username),
			slog.Int64("chat_id", chatID),
			slog.String("server", serverName),
			slog.String("error", errorMsg),
		)
	}

	b.sendFormattedNotification(message)
}

// sendFormattedNotification sends a formatted notification to all admins with Markdown parsing
func (b *Bot) sendFormattedNotification(message string) {
	if b.db == nil {
		return
	}
	admins, err := b.db.GetAdminUsers()
	if err != nil {
		b.logger.Error("Failed to fetch admin users", slog.String("error", err.Error()))
		return
	}

	for _, admin := range admins {
		if admin.TelegramID == nil {
			b.logger.Warn("Admin missing TelegramID, skipping notification", slog.String("username", admin.Username))
			continue
		}
		msg := tu.Message(
			tu.ID(*admin.TelegramID),
			message,
		).WithParseMode(telego.ModeMarkdown)

		_, err := b.notificationSender().SendMessage(msg)
		if err != nil {
			b.logger.Error("Failed to notify admin",
				slog.String("username", admin.Username),
				slog.String("error", err.Error()))
		}
	}
}
