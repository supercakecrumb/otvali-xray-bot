package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakePollingBot struct {
	id      int64
	started chan struct{}
	starts  atomic.Int32
	stops   atomic.Int32
	healthy bool
}

func (b *fakePollingBot) ID() int64 { return b.id }
func (b *fakePollingBot) StartContext(ctx context.Context) {
	b.starts.Add(1)
	close(b.started)
	<-ctx.Done()
}
func (b *fakePollingBot) Stop()                { b.stops.Add(1) }
func (b *fakePollingBot) PollingHealthy() bool { return b.healthy }

type healthyDatabase struct{}

func (healthyDatabase) PingContext(context.Context) error { return nil }

func TestRuntimeHealthRequiresEveryBot(t *testing.T) {
	primary := &fakePollingBot{id: 101, healthy: true}
	secondary := &fakePollingBot{id: 202}
	health := botRuntimeHealth{database: healthyDatabase{}, bots: []pollingBot{primary, secondary}}
	if health.PingContext(context.Background()) == nil {
		t.Fatal("healthy legacy poller must not hide an unavailable additional poller")
	}
	secondary.healthy = true
	if health.PingContext(context.Background()) != nil {
		t.Fatal("database and all working pollers must be healthy")
	}
	primary.healthy = false
	if health.PingContext(context.Background()) == nil {
		t.Fatal("each bot must independently contribute to readiness")
	}
}

func TestBotPollersStartOncePerIdentityAndAllDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &fakePollingBot{id: 101, started: make(chan struct{})}
	newBot := &fakePollingBot{id: 202, started: make(chan struct{})}
	bots := []pollingBot{old, newBot}
	stopped := startBotPollers(ctx, bots)
	for _, bot := range []*fakePollingBot{old, newBot} {
		select {
		case <-bot.started:
		case <-time.After(time.Second):
			t.Fatal("each configured bot must start its own poller")
		}
	}
	cancel()
	// Model the main select already having consumed the first exit notification.
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cancelled poller did not exit")
	}
	done := make(chan struct{})
	go func() { stopBotPollers(bots, stopped, 1); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("all pollers must drain without waiting twice for the first")
	}
	for _, bot := range []*fakePollingBot{old, newBot} {
		if bot.starts.Load() != 1 || bot.stops.Load() != 1 {
			t.Fatal("each bot must start and stop exactly once")
		}
	}
}
