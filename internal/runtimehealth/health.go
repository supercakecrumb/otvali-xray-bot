package runtimehealth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	Path     = "/tmp/otvali-xray-bot.health"
	Interval = 20 * time.Second
	MaxAge   = 90 * time.Second
)

var ErrUnhealthy = errors.New("runtime health unavailable")

type Pinger interface {
	PingContext(context.Context) error
}

// Check reads only the nonsecret heartbeat; it never loads configuration.
func Check(path string, now time.Time) error {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 64 {
		return ErrUnhealthy
	}
	stamp, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return ErrUnhealthy
	}
	age := now.Sub(time.Unix(0, stamp))
	if age < 0 || age > MaxAge {
		return ErrUnhealthy
	}
	return nil
}

// Refresh publishes health only after a successful database round trip.
func Refresh(ctx context.Context, pinger Pinger, path string, now time.Time) error {
	if err := pinger.PingContext(ctx); err != nil || ctx.Err() != nil {
		return ErrUnhealthy
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".otvali-health-*")
	if err != nil {
		return ErrUnhealthy
	}
	defer os.Remove(file.Name())
	if _, err = fmt.Fprintf(file, "%d\n", now.UnixNano()); err != nil {
		file.Close()
		return ErrUnhealthy
	}
	if err = file.Close(); err != nil {
		return ErrUnhealthy
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return ErrUnhealthy
	}
	return nil
}

// Monitor is independent of optional VPN issuers. Failed probes let health age
// out; stopping the process removes its heartbeat immediately.
func Monitor(ctx context.Context, pinger Pinger, path string, logger *slog.Logger) {
	_ = os.Remove(path)
	defer os.Remove(path)
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := Refresh(probeCtx, pinger, path, time.Now())
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Warn("Database runtime health probe failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
