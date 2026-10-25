package vpnmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
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

// The operator must create a separate empty test database. This test never
// accepts DATABASE_URL or production names, and never drops any database.
func TestMonitorPostgresRestartOutboxAndShutdown(t *testing.T) {
	raw := os.Getenv("VPN_MONITOR_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set VPN_MONITOR_TEST_DATABASE_URL to a new isolated otvali_monitor_ci_ database")
	}
	parsed, err := pgx.ParseConfig(raw)
	if err != nil || !regexp.MustCompile(`^otvali_monitor_ci_[a-z0-9_]+$`).MatchString(parsed.Database) {
		t.Fatal("refusing a database without the isolated monitoring test prefix")
	}
	db, err := gorm.Open(postgres.Open(raw), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("isolated connection unavailable")
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var tables int64
	if db.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&tables).Error != nil || tables != 0 {
		t.Fatal("test requires a new empty database")
	}
	config := Config{Enabled: true, Secret: strings.Repeat("test-only-", 4), AlertChatID: 42}
	service, err := NewService(db, config)
	if err != nil {
		t.Fatal(err)
	}
	now := testTime.Add(123456789 * time.Nanosecond)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	report := testReport(now)
	setCheck(&report, "msk-stockholm", "world-google", "fail")
	ingest := func() {
		t.Helper()
		body, _ := json.Marshal(report)
		if service.Ingest(ctx, report, body) != nil {
			t.Fatal("report persistence failed")
		}
	}
	ingest()
	// Concurrent duplicate delivery must not increment the first failed cycle.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	body, _ := json.Marshal(report)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- service.Ingest(ctx, report, body) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("identical retry failed")
		}
	}
	sent := 0
	deliver := func(_ context.Context, id int64, text string) error {
		sent++
		if id != 42 || !strings.Contains(text, "Москва → Стокгольм") {
			t.Fatal("unexpected notification")
		}
		return nil
	}
	if service.Tick(ctx, deliver) != nil || sent != 0 {
		t.Fatal("duplicate report created alert")
	}
	// Reopen service against the persisted first failure, then confirm it.
	service, err = NewService(db, config)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	now = now.Add(5 * time.Minute)
	report.CheckedAt = now
	ingest()
	failures := 0
	failed := func(context.Context, int64, string) error { failures++; return errors.New("private telegram error") }
	if service.Tick(ctx, failed) == nil || failures != 1 {
		t.Fatal("failed delivery must remain queued")
	}
	service, err = NewService(db, config)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	if service.Tick(ctx, deliver) != nil || sent != 0 {
		t.Fatal("restart bypassed delivery backoff")
	}
	now = now.Add(2 * time.Minute)
	if service.Tick(ctx, deliver) != nil || sent != 1 {
		t.Fatal("pending notification not retried")
	}
	if service.Tick(ctx, deliver) != nil || sent != 1 {
		t.Fatal("delivered alert repeated")
	}
	// An ongoing outage still produces no second alert after another restart.
	service, err = NewService(db, config)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	now = now.Add(3 * time.Minute)
	report.CheckedAt = now
	ingest()
	if service.Tick(ctx, deliver) != nil || sent != 1 {
		t.Fatal("restart forgot open incident")
	}
	setCheck(&report, "msk-stockholm", "world-google", "ok")
	now = now.Add(5 * time.Minute)
	report.CheckedAt = now
	ingest()
	if service.Tick(ctx, deliver) != nil || sent != 1 {
		t.Fatal("recovered after one pass")
	}
	now = now.Add(5 * time.Minute)
	report.CheckedAt = now
	ingest()
	if service.Tick(ctx, deliver) != nil || sent != 2 {
		t.Fatal("two passes did not recover")
	}
	snap, err := service.Snapshot(ctx)
	if err != nil || !snap.Available || snap.Stale {
		t.Fatal("fresh snapshot unavailable")
	}
	now = now.Add(16 * time.Minute)
	snap, err = service.Snapshot(ctx)
	if err != nil || !snap.Stale {
		t.Fatal("stale snapshot remained green")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { service.Run(runCtx, deliver, slog.New(slog.NewTextHandler(io.Discard, nil))); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not shut down promptly")
	}
}
