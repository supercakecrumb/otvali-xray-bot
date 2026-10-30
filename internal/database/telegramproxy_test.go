package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func syntheticProxyGrant(uid, tid int64, ep string) telegramproxy.Grant {
	return telegramproxy.Grant{UserID: uid, TelegramID: tid, EndpointID: ep, ProviderID: "fi", BindingHash: strings.Repeat("a", 64), ExternalID: fmt.Sprintf("otv_%032x", uid), EncryptedSecret: append([]byte{1}, make([]byte, 44)...), Generation: 1, State: telegramproxy.StatePending}
}

func TestProxyTransitionCannotReenableOrAlterIdentity(t *testing.T) {
	before := syntheticProxyGrant(1, 101, "finland")
	after := before
	after.State = telegramproxy.StateActive
	after.Generation++
	if !proxyTransition(before, after) {
		t.Fatal("valid activation rejected")
	}
	after.Generation--
	if proxyTransition(before, after) {
		t.Fatal("generation was not checked")
	}
	after = before
	after.ExternalID = "otv_" + strings.Repeat("b", 32)
	if proxyTransition(before, after) {
		t.Fatal("identity mutation allowed")
	}
	before.State = telegramproxy.StateRevoked
	after = before
	after.State = telegramproxy.StateActive
	after.Generation++
	if proxyTransition(before, after) {
		t.Fatal("revoked grant reenabled")
	}
}

// This opt-in suite refuses non-disposable/nonempty databases. It exercises
// actual PostgreSQL row locks, transaction boundaries and compare-and-swap.
func TestPostgresTelegramProxyLifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_TELEGRAM_PROXY_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL URL not supplied")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !regexp.MustCompile(`^/otvali_telegramproxy_ci_[a-z0-9_]+$`).MatchString(u.Path) {
		t.Fatal("only a disposable otvali_telegramproxy_ci_* database is permitted")
	}
	conn, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal("could not open disposable PostgreSQL")
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal("could not obtain test connection")
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(24)
	var count int64
	if err := conn.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&count).Error; err != nil || count != 0 {
		t.Fatal("test requires an empty public schema")
	}
	db := &DB{Conn: conn}
	ctx := context.Background()
	if conn.AutoMigrate(&User{}) != nil || db.InitTelegramProxy(ctx) != nil {
		t.Fatal("test schema setup failed")
	}
	t.Cleanup(func() { _ = conn.Migrator().DropTable(&TelegramProxyGrant{}, &User{}) })
	seed := func(tid int64) User {
		t.Helper()
		user := User{TelegramID: &tid, Username: fmt.Sprintf("synthetic_%d", tid)}
		if conn.Create(&user).Error != nil {
			t.Fatal("synthetic user create failed")
		}
		return user
	}
	transition := func(tid int64, state string) error {
		return db.WithProxyGrant(ctx, tid, "finland", func(g *telegramproxy.Grant) error { g.State = state; g.Generation++; return nil })
	}

	t.Run("concurrent preparation and row locking", func(t *testing.T) {
		user := seed(1001)
		candidate := syntheticProxyGrant(user.ID, 1001, "finland")
		var wg sync.WaitGroup
		failures := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := db.PrepareProxyGrant(ctx, candidate)
				if e != nil {
					failures <- e
				}
			}()
		}
		wg.Wait()
		close(failures)
		for e := range failures {
			t.Fatal(e)
		}
		var records int64
		conn.Model(&TelegramProxyGrant{}).Where("telegram_id = ?", 1001).Count(&records)
		if records != 1 {
			t.Fatal("duplicate durable identity")
		}
		var inside, peak atomic.Int32
		failures = make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e := db.WithProxyGrant(ctx, 1001, "finland", func(g *telegramproxy.Grant) error {
					now := inside.Add(1)
					for {
						old := peak.Load()
						if old >= now || peak.CompareAndSwap(old, now) {
							break
						}
					}
					defer inside.Add(-1)
					time.Sleep(5 * time.Millisecond)
					if g.State == telegramproxy.StatePending {
						g.State = telegramproxy.StateActive
						g.Generation++
					}
					return nil
				})
				if e != nil {
					failures <- e
				}
			}()
		}
		wg.Wait()
		close(failures)
		for e := range failures {
			t.Fatal(e)
		}
		g, ok, e := db.GetProxyGrant(ctx, 1001, "finland")
		if e != nil || !ok || g.State != telegramproxy.StateActive || g.Generation != 2 || peak.Load() != 1 {
			t.Fatal("cross-process row-lock contract failed")
		}
		if e := db.WithProxyGrant(ctx, 1001, "finland", func(g *telegramproxy.Grant) error { g.State = telegramproxy.StateRevoked; return nil }); !errors.Is(e, telegramproxy.ErrConflict) {
			t.Fatal("missing generation accepted")
		}
		if e := db.WithProxyGrant(ctx, 1001, "finland", func(g *telegramproxy.Grant) error { g.EncryptedSecret[1]++; return nil }); !errors.Is(e, telegramproxy.ErrConflict) {
			t.Fatal("ciphertext changed")
		}
	})

	t.Run("durability and delete tombstone", func(t *testing.T) {
		user := seed(1002)
		candidate := syntheticProxyGrant(user.ID, 1002, "finland")
		if _, e := db.PrepareProxyGrant(ctx, candidate); e != nil {
			t.Fatal(e)
		}
		if e := db.DeleteUserWithTelegramProxyGuard(ctx, user.ID); !errors.Is(e, telegramproxy.ErrInactive) {
			t.Fatal("pending credential orphaned")
		}
		if e := transition(1002, telegramproxy.StateActive); e != nil {
			t.Fatal(e)
		}
		if _, e := db.MarkProxyRevoking(ctx, 1002, "finland"); e != nil {
			t.Fatal(e)
		}
		_ = db.WithProxyGrant(ctx, 1002, "finland", func(g *telegramproxy.Grant) error { return telegramproxy.ErrUnavailable })
		g, _, _ := db.GetProxyGrant(ctx, 1002, "finland")
		if g.State != telegramproxy.StateRevoking {
			t.Fatal("revocation fence rolled back with provider failure")
		}
		if e := db.CheckTelegramProxyDisabled(ctx, 1002); !errors.Is(e, telegramproxy.ErrInactive) {
			t.Fatal("revoking grant not guarded")
		}
		if e := transition(1002, telegramproxy.StateRevoked); e != nil {
			t.Fatal(e)
		}
		if e := db.DeleteUserWithTelegramProxyGuard(ctx, user.ID); e != nil {
			t.Fatal(e)
		}
		if e := db.CheckTelegramProxyDisabled(ctx, 1002); e != nil {
			t.Fatal(e)
		}
		newUser := seed(1002)
		if _, e := db.PrepareProxyGrant(ctx, syntheticProxyGrant(newUser.ID, 1002, "finland")); !errors.Is(e, telegramproxy.ErrInactive) {
			t.Fatal("reinvitation resurrected revoked grant")
		}
		g, ok, e := db.GetProxyGrant(ctx, 1002, "finland")
		if e != nil || !ok || g.UserID != user.ID || g.State != telegramproxy.StateRevoked {
			t.Fatal("immutable tombstone lost")
		}
		if e := transition(1002, telegramproxy.StateActive); !errors.Is(e, telegramproxy.ErrConflict) {
			t.Fatal("revoked row reenabled")
		}
	})

	t.Run("orphan can still be revoked", func(t *testing.T) {
		user := seed(1003)
		if _, e := db.PrepareProxyGrant(ctx, syntheticProxyGrant(user.ID, 1003, "finland")); e != nil {
			t.Fatal(e)
		}
		if e := transition(1003, telegramproxy.StateActive); e != nil {
			t.Fatal(e)
		}
		// Simulate an older operator removing the users row outside the guard.
		if conn.Delete(&user).Error != nil {
			t.Fatal("synthetic orphan setup failed")
		}
		if e := db.WithProxyGrant(ctx, 1003, "finland", func(g *telegramproxy.Grant) error { return nil }); !errors.Is(e, telegramproxy.ErrInactive) {
			t.Fatal("orphan issuance allowed")
		}
		if _, e := db.MarkProxyRevoking(ctx, 1003, "finland"); e != nil {
			t.Fatal("orphan could not be fenced", e)
		}
		if e := transition(1003, telegramproxy.StateRevoked); e != nil {
			t.Fatal("orphan could not reach revoked", e)
		}
	})

	t.Run("prepare versus delete race", func(t *testing.T) {
		for n := 0; n < 12; n++ {
			tid := int64(2000 + n)
			user := seed(tid)
			candidate := syntheticProxyGrant(user.ID, tid, "finland")
			start := make(chan struct{})
			prepared := make(chan error, 1)
			deleted := make(chan error, 1)
			go func() { <-start; _, e := db.PrepareProxyGrant(ctx, candidate); prepared <- e }()
			go func() { <-start; deleted <- db.DeleteUserWithTelegramProxyGuard(ctx, user.ID) }()
			close(start)
			pe, de := <-prepared, <-deleted
			if pe == nil {
				if !errors.Is(de, telegramproxy.ErrInactive) {
					t.Fatal("account deletion raced past pending grant")
				}
			} else if !errors.Is(pe, telegramproxy.ErrNotEligible) || de != nil {
				t.Fatal("unexpected race outcome")
			}
			_, found, e := db.GetProxyGrant(ctx, tid, "finland")
			if e != nil || (found && de == nil) {
				t.Fatal("issued grant orphaned by deletion")
			}
		}
	})
}
