package database

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestActivityPostgresDedupAndPreservation(t *testing.T) {
	raw := os.Getenv("ACTIVITY_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set ACTIVITY_TEST_DATABASE_URL to a new isolated otvali_activity_ci_ database")
	}
	parsed, err := pgx.ParseConfig(raw)
	if err != nil || !regexp.MustCompile(`^otvali_activity_ci_[a-z0-9_]+$`).MatchString(parsed.Database) {
		t.Fatal("refusing database without isolated activity test prefix")
	}
	conn, err := gorm.Open(postgres.Open(raw), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("isolated activity database unavailable")
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal("isolated activity pool unavailable")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var count int64
	if conn.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&count).Error != nil || count != 0 {
		t.Fatal("requires new empty database")
	}
	if conn.AutoMigrate(&User{}) != nil {
		t.Fatal("fixture migration failed")
	}
	id := int64(900001)
	users := []User{{Username: "activity_fixture", TelegramID: &id}, {Username: "unbound_fixture"}}
	if conn.Create(&users).Error != nil {
		t.Fatal("fixture write failed")
	}
	var before []User
	if conn.Order("id").Find(&before).Error != nil {
		t.Fatal("fixture read failed")
	}
	db := &DB{Conn: conn}
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	if db.InitActivity(ctx, now) != nil || db.InitActivity(ctx, now.Add(time.Hour)) != nil {
		t.Fatal("additive activity initialization failed")
	}
	var collection ActivityCollection
	if conn.First(&collection, 1).Error != nil || !collection.StartedAt.Equal(now) {
		t.Fatal("collection start changed across restart")
	}
	event := strings.Repeat("a", 64)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- db.RecordActivity(ctx, id, "telegram", "callback", event, now) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal("idempotent concurrent event write failed")
		}
	}
	if conn.Model(&ActivityEvent{}).Count(&count).Error != nil || count != 1 {
		t.Fatal("Telegram redelivery double counted")
	}
	if db.RecordActivity(ctx, id, "telegram", "key_delivered", event, now) != nil {
		t.Fatal("separate outcome not recorded")
	}
	if db.RecordActivity(ctx, 900002, "telegram", "callback", strings.Repeat("b", 64), now) != nil {
		t.Fatal("unbound identity must be harmless")
	}
	if db.RecordActivity(ctx, id, "telegram", "secret-payload", strings.Repeat("c", 64), now) == nil {
		t.Fatal("arbitrary event contents accepted")
	}
	var events []ActivityEvent
	if conn.Order("id").Find(&events).Error != nil || len(events) != 2 || events[0].UserID != users[0].ID || events[1].UserID != users[0].ID {
		t.Fatal("identity or dedup boundary changed")
	}
	var after []User
	if conn.Order("id").Find(&after).Error != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("activity mutated user records")
	}
}
