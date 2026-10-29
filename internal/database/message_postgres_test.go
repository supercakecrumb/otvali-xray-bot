package database

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This opt-in test only accepts a new, disposable database. It seeds the
// pre-migration schema explicitly so preservation is tested against real NULL
// scope columns introduced by startup, rather than the current Go model.
func TestMessagePostgresScopeAndLegacyPreservation(t *testing.T) {
	raw := os.Getenv("MESSAGE_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set MESSAGE_TEST_DATABASE_URL to a new isolated otvali_message_ci_ database")
	}
	parsed, err := pgx.ParseConfig(raw)
	if err != nil || !regexp.MustCompile(`^otvali_message_ci_[a-z0-9_]+$`).MatchString(parsed.Database) {
		t.Fatal("refusing database without isolated message test prefix")
	}
	legacy, err := gorm.Open(postgres.Open(raw), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("isolated message database unavailable")
	}
	legacySQL, err := legacy.DB()
	if err != nil {
		t.Fatal("isolated message pool unavailable")
	}
	t.Cleanup(func() { _ = legacySQL.Close() })
	var count int64
	if legacy.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&count).Error != nil || count != 0 {
		t.Fatal("requires new empty database")
	}
	if legacy.Exec(`CREATE TABLE user_messages (
		id bigserial PRIMARY KEY,
		user_id bigint NOT NULL,
		username text NOT NULL,
		admin_id bigint,
		message_text text NOT NULL,
		is_admin_reply boolean DEFAULT false,
		reply_to_id bigint,
		telegram_msg_id bigint NOT NULL,
		admin_chat_msg_id bigint,
		created_at timestamptz
	)`).Error != nil {
		t.Fatal("legacy schema creation failed")
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if legacy.Exec(`INSERT INTO user_messages
		(user_id, username, admin_id, message_text, is_admin_reply, reply_to_id, telegram_msg_id, admin_chat_msg_id, created_at)
		VALUES
		(900001, 'legacy_one', NULL, 'synthetic first message', false, NULL, 11, 77, ?),
		(900002, 'legacy_two', NULL, 'synthetic other chat message', false, NULL, 11, 77, ?),
		(900001, 'legacy_one', 800001, 'synthetic admin reply', true, 1, 12, 79, ?),
		(900003, 'legacy_duplicate', NULL, 'synthetic duplicate one', false, NULL, 13, 88, ?),
		(900003, 'legacy_duplicate', NULL, 'synthetic duplicate two', false, NULL, 14, 88, ?)`,
		now, now.Add(time.Second), now.Add(2*time.Second), now.Add(3*time.Second), now.Add(4*time.Second)).Error != nil {
		t.Fatal("legacy fixture write failed")
	}

	// Select all historical columns, including nullable values and timestamps.
	// Reading through the new model before the migration would hide missing scope
	// columns, so the fixture deliberately has its own pre-migration representation.
	type historicalMessage struct {
		ID             int64
		UserID         int64
		Username       string
		AdminID        *int64
		MessageText    string
		IsAdminReply   bool
		ReplyToID      *int64
		TelegramMsgID  int
		AdminChatMsgID *int
		CreatedAt      time.Time
	}
	var before []historicalMessage
	if legacy.Table("user_messages").Order("id").Find(&before).Error != nil || len(before) != 5 {
		t.Fatal("legacy fixture read failed")
	}
	var db *DB
	for restart := 0; restart < 2; restart++ {
		db, err = NewDB(raw, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal("additive message migration failed")
		}
		pool, poolErr := db.Conn.DB()
		if poolErr != nil {
			t.Fatal("migrated message pool unavailable")
		}
		t.Cleanup(func() { _ = pool.Close() })
		var after []historicalMessage
		if db.Conn.Table("user_messages").Order("id").Find(&after).Error != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("startup migration changed historical message values")
		}
		if db.Conn.Model(&UserMessage{}).Where("bot_id IS NULL AND telegram_chat_id IS NULL").Count(&count).Error != nil || count != int64(len(before)) {
			t.Fatal("startup must retain unknown historical bot and chat as NULL")
		}
		if !db.Conn.Migrator().HasTable(&UserMessageForward{}) {
			t.Fatal("startup did not create scoped forward mappings")
		}
	}

	// The same Telegram message number in two admin chats and two bots must
	// identify three independent messages, including when origin message IDs match.
	const primaryBot, secondaryBot, firstAdmin, secondAdmin int64 = 700001, 700002, 800001, 800002
	messages := []UserMessage{
		{BotID: primaryBot, TelegramChatID: 900001, UserID: 900001, Username: "scoped_one", MessageText: "synthetic scoped one", TelegramMsgID: 20},
		{BotID: primaryBot, TelegramChatID: 900002, UserID: 900002, Username: "scoped_two", MessageText: "synthetic scoped two", TelegramMsgID: 20},
		{BotID: secondaryBot, TelegramChatID: 900001, UserID: 900001, Username: "scoped_three", MessageText: "synthetic scoped three", TelegramMsgID: 20},
	}
	for i := range messages {
		if db.AddUserMessage(&messages[i]) != nil {
			t.Fatal("scoped origin message write failed")
		}
	}
	forwards := []UserMessageForward{
		{BotID: primaryBot, AdminChatID: firstAdmin, TelegramMsgID: 77, UserMessageID: messages[0].ID, CreatedAt: now},
		{BotID: primaryBot, AdminChatID: secondAdmin, TelegramMsgID: 77, UserMessageID: messages[1].ID, CreatedAt: now},
		{BotID: secondaryBot, AdminChatID: firstAdmin, TelegramMsgID: 77, UserMessageID: messages[2].ID, CreatedAt: now},
	}
	for i := range forwards {
		forward := &forwards[i]
		if db.AddUserMessageForward(forward) != nil || db.AddUserMessageForward(forward) != nil {
			t.Fatal("identical forward mapping must be idempotent")
		}
		found, lookupErr := db.GetUserMessageByAdminForward(forward.BotID, forward.AdminChatID, forward.TelegramMsgID)
		if lookupErr != nil || found.ID != messages[i].ID {
			t.Fatal("forward lookup crossed bot or admin-chat scope")
		}
	}
	if db.Conn.Model(&UserMessageForward{}).Count(&count).Error != nil || count != 3 {
		t.Fatal("repeated forward save created duplicate mappings")
	}
	conflict := forwards[0]
	conflict.UserMessageID = messages[1].ID
	conflict.CreatedAt = now.Add(time.Hour)
	if db.AddUserMessageForward(&conflict) == nil {
		t.Fatal("a forward mapping must not be reassigned")
	}
	var stored UserMessageForward
	if db.Conn.Where("bot_id = ? AND admin_chat_id = ? AND telegram_msg_id = ?", primaryBot, firstAdmin, 77).First(&stored).Error != nil ||
		stored.BotID != forwards[0].BotID || stored.AdminChatID != forwards[0].AdminChatID ||
		stored.TelegramMsgID != forwards[0].TelegramMsgID || stored.UserMessageID != forwards[0].UserMessageID || !stored.CreatedAt.Equal(forwards[0].CreatedAt) {
		t.Fatal("conflicting forward write mutated original mapping")
	}
	for _, scope := range []UserMessageForward{
		{BotID: secondaryBot, AdminChatID: secondAdmin, TelegramMsgID: 77},
		{BotID: primaryBot, AdminChatID: firstAdmin, TelegramMsgID: 78},
		{BotID: 700003, AdminChatID: firstAdmin, TelegramMsgID: 77},
	} {
		if _, lookupErr := db.GetUserMessageByAdminForward(scope.BotID, scope.AdminChatID, scope.TelegramMsgID); !errors.Is(lookupErr, ErrUserNotFound) {
			t.Fatal("missing scope must not fall back to another bot, chat or legacy row")
		}
	}

	for _, candidate := range []struct {
		messageID int
		userID    int64
		wantID    int64
	}{
		{77, 900001, before[0].ID},
		{77, 900002, before[1].ID},
	} {
		found, lookupErr := db.GetLegacyUserMessageByAdminReply(candidate.messageID, candidate.userID)
		if lookupErr != nil || found.ID != candidate.wantID {
			t.Fatal("legacy lookup did not honor verified recipient identity")
		}
	}
	for _, candidate := range []struct {
		messageID int
		userID    int64
	}{
		{77, 900003}, // Correct number alone cannot establish the recipient.
		{88, 900003}, // Two old rows for one recipient are ambiguous.
		{79, 900001}, // Admin replies are never forward targets.
		{77, 0},
	} {
		if _, lookupErr := db.GetLegacyUserMessageByAdminReply(candidate.messageID, candidate.userID); !errors.Is(lookupErr, ErrUserNotFound) {
			t.Fatal("unsafe legacy candidate must be rejected")
		}
	}
	legacyNumber := 99
	explicit := UserMessage{BotID: primaryBot, TelegramChatID: 900004, UserID: 900004, Username: "explicit_scope", MessageText: "synthetic explicit scope", TelegramMsgID: 21, AdminChatMsgID: &legacyNumber}
	if db.AddUserMessage(&explicit) != nil {
		t.Fatal("explicitly scoped fixture write failed")
	}
	if _, lookupErr := db.GetLegacyUserMessageByAdminReply(legacyNumber, explicit.UserID); !errors.Is(lookupErr, ErrUserNotFound) {
		t.Fatal("legacy lookup must exclude explicitly scoped messages")
	}
	var historicalAfter []historicalMessage
	if db.Conn.Table("user_messages").Where("id <= ?", before[len(before)-1].ID).Order("id").Find(&historicalAfter).Error != nil || !reflect.DeepEqual(before, historicalAfter) {
		t.Fatal("scoped messaging operations changed historical rows")
	}
}
