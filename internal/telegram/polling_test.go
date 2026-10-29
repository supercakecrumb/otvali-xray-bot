package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

type controlledPollRequest struct {
	params   telego.GetUpdatesParams
	response chan *ta.Response
}

type controlledPollingCaller struct{ requests chan controlledPollRequest }

func (c controlledPollingCaller) Call(_ string, data *ta.RequestData) (*ta.Response, error) {
	request := controlledPollRequest{response: make(chan *ta.Response)}
	if err := json.Unmarshal(data.Buffer.Bytes(), &request.params); err != nil {
		return nil, err
	}
	c.requests <- request
	return <-request.response, nil
}

func TestPollingBatchOrderOffsetRetryAndCancellation(t *testing.T) {
	b := configuredTestBot(t, "202")
	requests := make(chan controlledPollRequest)
	var err error
	b.bot, err = telego.NewBot("202:"+strings.Repeat("x", 35), telego.WithAPICaller(controlledPollingCaller{requests}), telego.WithLogger(telegramAPILogger{logger: b.logger}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.cancel = cancel
	updates := b.updatesViaPolling(ctx)
	nextRequest := func() controlledPollRequest {
		t.Helper()
		select {
		case request := <-requests:
			return request
		case <-time.After(2 * time.Second):
			t.Fatal("poll request missing")
			return controlledPollRequest{}
		}
	}
	first := nextRequest()
	if first.params.Offset != 0 || first.params.Timeout != 8 {
		t.Fatal("initial polling parameters changed")
	}
	first.response <- &ta.Response{Ok: true, Result: []byte(`[{"update_id":7},{"update_id":8}]`)}
	for _, id := range []int{7, 8} {
		select {
		case update := <-updates:
			if update.UpdateID != id {
				t.Fatal("updates must retain batch order")
			}
		case <-time.After(time.Second):
			t.Fatal("batch update was lost")
		}
	}
	second := nextRequest()
	if second.params.Offset != 9 {
		t.Fatal("only a received batch may advance the acknowledged offset")
	}
	second.response <- &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: 429, Parameters: &ta.ResponseParameters{RetryAfter: 1}}}
	select {
	case <-requests:
		t.Fatal("rate-limited polling retried before its requested delay")
	case <-time.After(100 * time.Millisecond):
	}
	third := nextRequest()
	if third.params.Offset != 9 {
		t.Fatal("a failed poll must not advance the update offset")
	}
	cancel()
	third.response <- &ta.Response{Ok: true, Result: []byte(`[{"update_id":9}]`)}
	select {
	case _, open := <-updates:
		if open {
			t.Fatal("cancellation must stop delivery of a late response")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled polling did not close its output")
	}
}

func TestPollingTransientFailureAndRecovery(t *testing.T) {
	b := configuredTestBot(t, "101")
	logs := &bytes.Buffer{}
	b.logger = slog.New(slog.NewTextHandler(logs, nil))
	b.recordPollingSuccess(time.Now())
	if b.recordPollingError(errors.New("private-token-and-url")) != 8*time.Second || !b.PollingHealthy() || b.stopRequested {
		t.Fatal("one transient polling failure must retain healthy grace and not stop the runtime")
	}
	b.recordPollingSuccess(time.Now())
	if !b.PollingHealthy() || b.pollingFailures.Load() != 0 || strings.Contains(logs.String(), "private-token-and-url") {
		t.Fatal("recovery must reset failures without exposing transport errors")
	}
	if strings.Count(logs.String(), "Telegram polling active") != 1 {
		t.Fatal("initial polling proof must be emitted only once")
	}
}

func TestPollingRateLimitRespectsRetryAfter(t *testing.T) {
	b := configuredTestBot(t, "101")
	delay := b.recordPollingError(&ta.Error{ErrorCode: 429, Description: "private-error", Parameters: &ta.ResponseParameters{RetryAfter: 27}})
	if delay != 27*time.Second || b.stopRequested {
		t.Fatal("rate limiting must retain its server-directed retry delay")
	}
}

func TestLongPollingRateLimitDoesNotRestartBeforeAllowedRetry(t *testing.T) {
	b := configuredTestBot(t, "202")
	now := time.Now()
	b.pollingStartedAt.Store(now.UnixNano())
	b.recordPollingError(&ta.Error{ErrorCode: 429, Parameters: &ta.ResponseParameters{RetryAfter: 240}})
	if b.PollingHealthy() {
		t.Fatal("rate limiting cannot make a bot healthy before its first successful poll")
	}
	b.recordPollingSuccess(now.Add(-2 * time.Minute))
	delay := b.recordPollingError(&ta.Error{ErrorCode: 429, Parameters: &ta.ResponseParameters{RetryAfter: 240}})
	if delay != 4*time.Minute || !b.PollingHealthy() || b.checkPollingDeadline(now.Add(2*time.Minute)) {
		t.Fatal("an explicit long cooldown must not restart or mark a previously proven bot unhealthy before retry")
	}
	deadline := b.pollingDeadline(b.pollingLastOK.Load())
	b.recordPollingError(errors.New("transient-after-cooldown"))
	if b.pollingDeadline(b.pollingLastOK.Load()) != deadline {
		t.Fatal("ordinary transport errors must not extend the API-directed grace")
	}
	if !b.checkPollingDeadline(deadline) {
		t.Fatal("an API-directed cooldown must still have a bounded expiry")
	}
}

func TestPollingDeadlineCancelsStalledWorker(t *testing.T) {
	for _, firstSuccess := range []bool{false, true} {
		b := configuredTestBot(t, "202")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		b.cancel = cancel
		now := time.Now()
		b.pollingStartedAt.Store(now.UnixNano())
		if firstSuccess {
			b.recordPollingSuccess(now)
		}
		if b.checkPollingDeadline(now.Add(pollingGrace-time.Second)) || ctx.Err() != nil {
			t.Fatal("polling must retain its bounded grace")
		}
		if !b.checkPollingDeadline(now.Add(pollingGrace)) || ctx.Err() == nil || b.PollingHealthy() {
			t.Fatal("a stalled initial or previously-working secondary must cancel at the deadline")
		}
	}
}

type permanentPollingFailure struct{ code int }

func (c permanentPollingFailure) Call(string, *ta.RequestData) (*ta.Response, error) {
	return &ta.Response{Ok: false, Error: &ta.Error{ErrorCode: c.code, Description: "private-telegram-detail"}}, nil
}

func TestPermanentPollingFailureStopsActualWorker(t *testing.T) {
	for _, code := range []int{401, 409} {
		b := configuredTestBot(t, "202")
		logs := &bytes.Buffer{}
		b.logger = slog.New(slog.NewTextHandler(logs, nil))
		var err error
		b.bot, err = telego.NewBot("202:"+strings.Repeat("x", 35), telego.WithAPICaller(permanentPollingFailure{code}), telego.WithLogger(telegramAPILogger{logger: b.logger}))
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { b.StartContext(context.Background()); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("permanent authentication/conflict failure must exit promptly")
		}
		if !b.pollingFailed.Load() || b.PollingHealthy() || strings.Contains(logs.String(), "private-telegram-detail") {
			t.Fatal("permanent failure must fail health without logging API content")
		}
	}
}
