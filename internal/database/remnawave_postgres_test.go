package database

import (
	"context"
	"io"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This opt-in test must only run in a newly created disposable database. It
// refuses a production database name before opening any connection and never
// creates or drops a database itself.
func TestRemnawaveStorePostgres(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set TEST_DATABASE_URL to a new isolated otvali_remna_ci_ database")
	}
	parsed, err := pgx.ParseConfig(raw)
	if err != nil || !regexp.MustCompile(`^otvali_remna_ci_[a-z0-9_]+$`).MatchString(parsed.Database) {
		t.Fatal("refusing a database without the isolated integration-test prefix")
	}
	legacy, err := gorm.Open(postgres.Open(raw), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("could not connect to the isolated integration-test database")
	}
	legacySQL, err := legacy.DB()
	if err != nil {
		t.Fatal("could not obtain the isolated database connection")
	}
	t.Cleanup(func() { _ = legacySQL.Close() })
	var existingTables int64
	if legacy.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&existingTables).Error != nil || existingTables != 0 {
		t.Fatal("integration test requires a new empty database")
	}
	if legacy.AutoMigrate(&User{}, &Server{}, &UserMessage{}) != nil {
		t.Fatal("could not seed the legacy schema")
	}
	boundID := int64(900001)
	users := []User{
		{TelegramID: &boundID, Username: "bound_fixture", Invited: false, ExclusiveAccess: true},
		{Username: "unbound_fixture", Invited: true},
	}
	inbound := 2
	server := Server{Name: "legacy_fixture", Country: "Finland", City: "Helsinki", IP: "192.0.2.1", SSHPort: 41922, SSHUser: "fixture", APIPort: 10000, Username: "fixture", Password: "synthetic-fixture", RealityCover: "example.com", InboundID: &inbound}
	if legacy.Create(&users).Error != nil || legacy.Create(&server).Error != nil {
		t.Fatal("could not seed legacy rows")
	}
	var usersBefore []User
	var serversBefore []Server
	if legacy.Order("id").Find(&usersBefore).Error != nil || legacy.Order("id").Find(&serversBefore).Error != nil {
		t.Fatal("could not read legacy fixtures")
	}
	db, err := NewDB(raw, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("additive startup migration failed")
	}
	sqlDB, err := db.Conn.DB()
	if err != nil {
		t.Fatal("could not obtain the mapping database connection")
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if !db.Conn.Migrator().HasTable(&RemnawaveGrant{}) {
		t.Fatal("startup did not create the separate mapping table")
	}
	ctx := context.Background()
	if bound, err := db.IsBoundUser(ctx, boundID); err != nil || !bound {
		t.Fatal("a bound Telegram ID must be eligible independently of the invitation flag")
	}
	if bound, err := db.IsBoundUser(ctx, 900002); err != nil || bound {
		t.Fatal("an invited username without a Telegram binding must not be eligible")
	}
	if _, found, err := db.FindGrant(ctx, boundID); err != nil || found {
		t.Fatal("a new bound account must have no preexisting mapping")
	}
	first := remnawave.Mapping{TelegramID: boundID, RemoteID: 11, Username: "remote_fixture_one"}
	if db.SaveGrant(ctx, first) != nil || db.SaveGrant(ctx, first) != nil {
		t.Fatal("saving an identical mapping must be idempotent")
	}
	for _, candidate := range []remnawave.Mapping{
		{TelegramID: boundID, RemoteID: 12, Username: "remote_fixture_two"},
		{TelegramID: 900002, RemoteID: first.RemoteID, Username: "remote_fixture_two"},
		{TelegramID: 900003, RemoteID: 13, Username: first.Username},
	} {
		if err := db.SaveGrant(ctx, candidate); err == nil {
			t.Fatal("a conflicting Telegram ID, remote ID or username must be rejected")
		} else if strings.Contains(err.Error(), candidate.Username) || strings.Contains(err.Error(), "90000") {
			t.Fatal("mapping errors must not expose identities")
		}
	}
	stored, found, err := db.FindGrant(ctx, boundID)
	if err != nil || !found || stored != first {
		t.Fatal("conflicts must preserve the original mapping")
	}
	for _, id := range []int64{900002, 900003} {
		if _, found, err := db.FindGrant(ctx, id); err != nil || found {
			t.Fatal("a remote-identity conflict must not create another mapping")
		}
	}
	concurrent := remnawave.Mapping{TelegramID: 900004, RemoteID: 14, Username: "remote_fixture_concurrent"}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 16)
	for i := 0; i < cap(errorsSeen); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsSeen <- db.SaveGrant(ctx, concurrent)
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal("concurrent identical saves must all succeed")
		}
	}
	// Competing writes may choose either identity, but successful callers must
	// all observe the same winner; the losing identity must never replace it.
	candidates := []remnawave.Mapping{
		{TelegramID: 900005, RemoteID: 15, Username: "remote_fixture_race_a"},
		{TelegramID: 900005, RemoteID: 16, Username: "remote_fixture_race_b"},
	}
	successes := make(chan remnawave.Mapping, 16)
	start := make(chan struct{})
	for i := 0; i < cap(successes); i++ {
		candidate := candidates[i%len(candidates)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if db.SaveGrant(ctx, candidate) == nil {
				successes <- candidate
			}
		}()
	}
	close(start)
	wg.Wait()
	close(successes)
	winner, found, err := db.FindGrant(ctx, candidates[0].TelegramID)
	if err != nil || !found {
		t.Fatal("a concurrent mapping winner must be stored")
	}
	var successful int
	for saved := range successes {
		successful++
		if saved != winner {
			t.Fatal("competing writes must never successfully reassign an identity")
		}
	}
	if successful == 0 {
		t.Fatal("one concurrent mapping candidate must succeed")
	}
	var count int64
	if db.Conn.Model(&RemnawaveGrant{}).Count(&count).Error != nil || count != 3 {
		t.Fatal("mapping saves must create exactly one row per accepted identity")
	}
	var usersAfter []User
	var serversAfter []Server
	if legacy.Order("id").Find(&usersAfter).Error != nil || legacy.Order("id").Find(&serversAfter).Error != nil ||
		!reflect.DeepEqual(usersBefore, usersAfter) || !reflect.DeepEqual(serversBefore, serversAfter) {
		t.Fatal("migration and mapping operations must preserve legacy user and server values")
	}
}
