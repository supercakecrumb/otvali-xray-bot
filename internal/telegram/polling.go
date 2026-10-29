package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

const pollingGrace = 90 * time.Second

// Each Bot owns one loop. The SDK's default loop retries permanent failures
// forever; explicit retries let runtime health observe every configured bot.
func (b *Bot) updatesViaPolling(ctx context.Context) <-chan telego.Update {
	updates := make(chan telego.Update, 100)
	b.pollingStartedAt.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if b.checkPollingDeadline(now) {
					return
				}
			}
		}
	}()
	go func() {
		defer close(updates)
		params := &telego.GetUpdatesParams{Timeout: 8}
		for ctx.Err() == nil {
			batch, err := b.bot.GetUpdates(params)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				delay := b.recordPollingError(err)
				if ctx.Err() != nil {
					return
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				continue
			}
			b.recordPollingSuccess(time.Now())
			for _, update := range batch {
				if update.UpdateID < params.Offset {
					continue
				}
				params.Offset = update.UpdateID + 1
				select {
				case <-ctx.Done():
					return
				case updates <- update.WithContext(ctx):
				}
			}
		}
	}()
	return updates
}

func (b *Bot) recordPollingSuccess(now time.Time) {
	if b.pollingLastOK.Swap(now.UnixNano()) == 0 {
		b.logger.Info("Telegram polling active", "bot_id", b.ID())
	}
	b.pollingFailures.Store(0)
	b.pollingRetryUntil.Store(0)
}

func (b *Bot) recordPollingError(err error) time.Duration {
	code, delay := 0, 8*time.Second
	var apiError *ta.Error
	if errors.As(err, &apiError) {
		code = apiError.ErrorCode
		if code == 429 && apiError.Parameters != nil && apiError.Parameters.RetryAfter > 0 {
			// Clamp only at the representable Unix-nanosecond limit, never at an
			// operational timeout that could cause an early retry.
			now := time.Now()
			maxSeconds := int((int64(^uint64(0)>>1) - now.UnixNano()) / int64(time.Second))
			delay = time.Duration(min(apiError.Parameters.RetryAfter, maxSeconds)) * time.Second
			b.pollingRetryUntil.Store(now.Add(delay).UnixNano())
		}
	}
	failures := b.pollingFailures.Add(1)
	b.logger.Warn("Telegram polling retry", "bot_id", b.ID(), "failure_streak", failures, "status", code)
	if code == 401 || code == 409 {
		b.failPolling("permanent_error")
	}
	return delay
}

func (b *Bot) failPolling(reason string) {
	if b.pollingFailed.CompareAndSwap(false, true) {
		b.logger.Error("Telegram polling stopped", "bot_id", b.ID(), "reason", reason)
		b.Stop()
	}
}

func (b *Bot) checkPollingDeadline(now time.Time) bool {
	last := b.pollingLastOK.Load()
	if last == 0 {
		last = b.pollingStartedAt.Load()
	}
	if last > 0 && !now.Before(b.pollingDeadline(last)) {
		b.failPolling("polling_timeout")
		return true
	}
	return false
}

func (b *Bot) PollingHealthy() bool {
	last := b.pollingLastOK.Load()
	return !b.pollingFailed.Load() && last > 0 && time.Now().Before(b.pollingDeadline(last))
}

// Only an explicit Telegram 429 can extend the deadline. Give the worker a new
// grace interval after that mandatory cooldown; plain network errors never do.
func (b *Bot) pollingDeadline(last int64) time.Time {
	if until := b.pollingRetryUntil.Load(); until > last {
		last = until
	}
	return time.Unix(0, last).Add(pollingGrace)
}
