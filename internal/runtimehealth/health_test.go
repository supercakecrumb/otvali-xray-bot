package runtimehealth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testPinger struct{ err error }

func (p testPinger) PingContext(context.Context) error { return p.err }

func TestHeartbeatFreshMissingAndExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heartbeat")
	now := time.Now()
	if Check(path, now) == nil {
		t.Fatal("missing heartbeat passed")
	}
	if err := Refresh(context.Background(), testPinger{}, path, now); err != nil {
		t.Fatal(err)
	}
	if err := Check(path, now.Add(Interval)); err != nil {
		t.Fatal("fresh heartbeat failed")
	}
	if Check(path, now.Add(MaxAge+time.Nanosecond)) == nil {
		t.Fatal("stale heartbeat passed")
	}
	if Check(path, now.Add(-time.Second)) == nil {
		t.Fatal("future heartbeat passed")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("heartbeat must have mode 0600")
	}
}

func TestFailedDatabaseProbeDoesNotPublishOrRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heartbeat")
	now := time.Now()
	failed := testPinger{err: errors.New("private database error")}
	if Refresh(context.Background(), failed, path, now) == nil {
		t.Fatal("failed database probe passed")
	}
	if Check(path, now) == nil {
		t.Fatal("failed database probe created health")
	}
	if err := Refresh(context.Background(), testPinger{}, path, now); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if Refresh(context.Background(), failed, path, now.Add(Interval)) == nil {
		t.Fatal("failed database probe refreshed health")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed database probe changed health")
	}
}

func TestMalformedHeartbeatAndCancelledProbeFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heartbeat")
	if err := os.WriteFile(path, []byte("not-a-heartbeat"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Check(path, time.Now()) == nil {
		t.Fatal("malformed heartbeat passed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Refresh(ctx, testPinger{}, path, time.Now()) == nil {
		t.Fatal("cancelled probe passed")
	}
}
