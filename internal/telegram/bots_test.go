package telegram

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
)

func configuredTestBot(t *testing.T, id string) *Bot {
	t.Helper()
	b, err := NewBot(id+":"+strings.Repeat("x", 35), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPeerBotsPreservePrimaryNotificationsAndOriginSenders(t *testing.T) {
	primary, secondary := configuredTestBot(t, "101"), configuredTestBot(t, "202")
	for _, bot := range []*Bot{primary, secondary} {
		bot.ConfigurePeers(primary, []*Bot{primary, secondary})
		if bot.notificationSender() != primary.bot || bot.senderForBotID(0) != primary.bot || bot.senderForBotID(101) != primary.bot || bot.senderForBotID(202) != secondary.bot || bot.senderForBotID(999) != nil {
			t.Fatal("notifications must retain the primary while replies use only their known origin")
		}
	}
	if !primary.legacyMessageFallback || secondary.legacyMessageFallback || primary.ID() != 101 || secondary.ID() != 202 {
		t.Fatal("historical reply lookup must be restricted to the legacy bot")
	}
}

type pollingTestCaller struct {
	requested chan struct{}
	release   chan struct{}
	calls     atomic.Int32
}

func (c *pollingTestCaller) Call(_ string, _ *ta.RequestData) (*ta.Response, error) {
	if c.calls.Add(1) == 1 {
		close(c.requested)
	}
	<-c.release
	return &ta.Response{Ok: true, Result: []byte(`[]`)}, nil
}

func TestBotStopCancelsPollingAndDoesNotRestart(t *testing.T) {
	b := configuredTestBot(t, "101")
	caller := &pollingTestCaller{requested: make(chan struct{}), release: make(chan struct{})}
	var err error
	b.bot, err = telego.NewBot("101:"+strings.Repeat("x", 35), telego.WithAPICaller(caller))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { b.StartContext(context.Background()); close(done) }()
	select {
	case <-caller.requested:
	case <-time.After(time.Second):
		t.Fatal("polling did not start")
	}
	b.Stop()
	close(caller.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopping a bot must drain its poller")
	}
	b.StartContext(context.Background())
	if caller.calls.Load() != 1 {
		t.Fatal("the same bot instance must never create another poller")
	}
}

func TestBotStoppedBeforeStartDoesNotPoll(t *testing.T) {
	b := configuredTestBot(t, "101")
	b.Stop()
	b.StartContext(context.Background())
	if b.started {
		t.Fatal("an early shutdown must not start a poller")
	}
}
