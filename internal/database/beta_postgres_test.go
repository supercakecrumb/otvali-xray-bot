package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Only an empty disposable database with this prefix can be used. No database
// creation/drop and no production connection is performed by the test itself.
func TestBetaStorePostgres(t *testing.T) {
	raw := os.Getenv("BETA_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set BETA_TEST_DATABASE_URL to a new isolated otvali_beta_ci_ database")
	}
	parsed, err := pgx.ParseConfig(raw)
	if err != nil || !regexp.MustCompile(`^otvali_beta_ci_[a-z0-9_]+$`).MatchString(parsed.Database) {
		t.Fatal("refusing database outside disposable beta prefix")
	}
	seed, err := gorm.Open(postgres.Open(raw), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("isolated database unavailable")
	}
	pool, err := seed.DB()
	if err != nil {
		t.Fatal("isolated pool unavailable")
	}
	t.Cleanup(func() { _ = pool.Close() })
	var count int64
	if seed.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&count).Error != nil || count != 0 {
		t.Fatal("isolated database must be empty")
	}
	if seed.AutoMigrate(&User{}) != nil {
		t.Fatal("could not seed old user schema")
	}
	first, second, admin, quota, daily := int64(42001), int64(42002), int64(42003), int64(42004), int64(42005)
	users := []User{{TelegramID: &first, Username: "beta_first"}, {TelegramID: &second, Username: "beta_second"}, {TelegramID: &admin, Username: "beta_admin", IsAdmin: true}, {TelegramID: &quota, Username: "beta_quota"}, {TelegramID: &daily, Username: "beta_daily"}}
	if seed.Create(&users).Error != nil {
		t.Fatal("seed users failed")
	}
	var before []User
	if seed.Order("id").Find(&before).Error != nil {
		t.Fatal("read users failed")
	}
	db, err := NewDB(raw, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("additive migration failed")
	}
	newPool, err := db.Conn.DB()
	if err != nil {
		t.Fatal("migrated pool unavailable")
	}
	newPool.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = newPool.Close() })
	if !db.Conn.Migrator().HasTable(&BetaReport{}) || !db.Conn.Migrator().HasIndex(&BetaReport{}, "beta_request") {
		t.Fatal("missing beta table or dedup constraint")
	}
	if db.Conn.Migrator().HasColumn(&BetaReport{}, "author_username") || db.Conn.Migrator().HasColumn(&BetaReport{}, "author_telegram_id") {
		t.Fatal("read-only author aliases must not persist PII copies")
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := BetaReport{RequestID: "00000000-0000-4000-8000-000000000001", PayloadHash: fmt.Sprintf("%064d", 1), PayloadJSON: `{"route_id":"sto","comments":"synthetic test"}`}
	firstRow, duplicate, err := db.CreateBetaReport(ctx, first, candidate, now)
	if err != nil || duplicate || firstRow.ID <= 0 {
		t.Fatal("first report failed")
	}
	retry, duplicate, err := db.CreateBetaReport(ctx, first, candidate, now.Add(time.Minute))
	if err != nil || !duplicate || retry.ID != firstRow.ID || !retry.CreatedAt.Equal(firstRow.CreatedAt) {
		t.Fatal("idempotency failed")
	}
	changed := candidate
	changed.PayloadHash = fmt.Sprintf("%064d", 2)
	if _, _, err = db.CreateBetaReport(ctx, first, changed, now); !errors.Is(err, ErrBetaConflict) {
		t.Fatal("changed retry accepted")
	}
	if _, dup, err := db.CreateBetaReport(ctx, second, candidate, now); err != nil || dup {
		t.Fatal("request ID not scoped per user")
	}
	if _, err := db.GetBetaReport(ctx, second, firstRow.ID); !errors.Is(err, ErrBetaNotFound) {
		t.Fatal("cross-user read succeeded")
	}
	if _, err := db.ListBetaReports(ctx, first, true, 0); !errors.Is(err, ErrBetaForbidden) {
		t.Fatal("nonadmin list succeeded")
	}
	if _, err := db.UpdateBetaReport(ctx, first, firstRow.ID, "resolved", "not allowed", now); !errors.Is(err, ErrBetaForbidden) {
		t.Fatal("nonadmin update succeeded")
	}
	rows, err := db.ListBetaReports(ctx, first, false, 0)
	if err != nil || len(rows) != 1 || rows[0].UserID != users[0].ID {
		t.Fatal("own list scope failed")
	}
	rows, err = db.ListBetaReports(ctx, admin, true, 0)
	if err != nil || len(rows) != 2 || rows[0].AuthorUsername != "beta_second" || rows[0].AuthorTelegramID != second {
		t.Fatal("admin list author join failed")
	}
	updated, err := db.UpdateBetaReport(ctx, admin, firstRow.ID, "need_info", "Please retest mobile", now.Add(time.Minute))
	if err != nil || updated.Status != "need_info" || updated.Note != "Please retest mobile" {
		t.Fatal("admin triage failed")
	}
	own, err := db.GetBetaReport(ctx, first, firstRow.ID)
	if err != nil || own.Status != "need_info" || own.Note != updated.Note {
		t.Fatal("author cannot see reply")
	}
	var after []User
	if seed.Order("id").Find(&after).Error != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("beta migration/operations changed existing users")
	}
	if db.Conn.Model(&User{}).Where("telegram_id = ?", admin).Update("is_admin", false).Error != nil {
		t.Fatal("fixture role change failed")
	}
	if _, err := db.GetBetaReport(ctx, admin, firstRow.ID); !errors.Is(err, ErrBetaNotFound) {
		t.Fatal("revoked admin read succeeded")
	}
	if _, err := db.UpdateBetaReport(ctx, admin, firstRow.ID, "resolved", "", now); !errors.Is(err, ErrBetaForbidden) {
		t.Fatal("revoked admin update succeeded")
	}
	if _, _, err := db.CreateBetaReport(ctx, 999999, candidate, now); !errors.Is(err, ErrBetaForbidden) {
		t.Fatal("unbound actor submitted")
	}
	// Concurrent retries deduplicate under the bound user lock, including callers
	// which started before the first transaction committed.
	var wg sync.WaitGroup
	start := make(chan struct{})
	seen := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row, _, err := db.CreateBetaReport(ctx, first, candidate, now)
			if err == nil && row.ID != firstRow.ID {
				err = errors.New("wrong identity")
			}
			seen <- err
		}()
	}
	close(start)
	wg.Wait()
	close(seen)
	for err := range seen {
		if err != nil {
			t.Fatal("concurrent identical retry failed")
		}
	}
	// Distinct concurrent submissions must not race the rolling quota.
	start = make(chan struct{})
	accepted := make(chan BetaReport, 20)
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		body := candidate
		body.RequestID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row, _, err := db.CreateBetaReport(ctx, quota, body, now)
			if err == nil {
				accepted <- row
			} else {
				failures <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(accepted)
	close(failures)
	if len(accepted) != 12 || len(failures) != 8 {
		t.Fatal("parallel hourly quota was exceeded")
	}
	for err := range failures {
		if !errors.Is(err, ErrBetaRateLimited) {
			t.Fatal("quota returned unexpected error")
		}
	}
	acceptedRow := <-accepted
	existing := BetaReport{RequestID: acceptedRow.RequestID, PayloadHash: acceptedRow.PayloadHash, PayloadJSON: acceptedRow.PayloadJSON}
	if row, dup, err := db.CreateBetaReport(ctx, quota, existing, now); err != nil || !dup || row.ID != acceptedRow.ID {
		t.Fatal("full quota blocked harmless idempotent retry")
	}
	var dailyRows []BetaReport
	for i := 0; i < 60; i++ {
		dailyRows = append(dailyRows, BetaReport{UserID: users[4].ID, RequestID: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+100), PayloadHash: candidate.PayloadHash, PayloadJSON: candidate.PayloadJSON, Status: "new", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)})
	}
	if db.betaDB(ctx).Create(&dailyRows).Error != nil {
		t.Fatal("daily quota seed failed")
	}
	if _, _, err := db.CreateBetaReport(ctx, daily, candidate, now); !errors.Is(err, ErrBetaRateLimited) {
		t.Fatal("daily quota ignored")
	}
	page, err := db.ListBetaReports(ctx, daily, false, 0)
	if err != nil || len(page) != 26 {
		t.Fatal("storage page must be bounded to 26 rows")
	}
}
