package main

import (
	"context"
	"errors"

	"github.com/supercakecrumb/otvali-xray-bot/internal/runtimehealth"
)

type pollingBot interface {
	ID() int64
	StartContext(context.Context)
	Stop()
	PollingHealthy() bool
}

type botRuntimeHealth struct {
	database runtimehealth.Pinger
	bots     []pollingBot
}

func (h botRuntimeHealth) PingContext(ctx context.Context) error {
	if err := h.database.PingContext(ctx); err != nil {
		return errors.New("database runtime unavailable")
	}
	for _, bot := range h.bots {
		if !bot.PollingHealthy() {
			return errors.New("Telegram runtime unavailable")
		}
	}
	return ctx.Err()
}

func startBotPollers(ctx context.Context, bots []pollingBot) <-chan int64 {
	stopped := make(chan int64, len(bots))
	for _, bot := range bots {
		go func(bot pollingBot) {
			bot.StartContext(ctx)
			stopped <- bot.ID()
		}(bot)
	}
	return stopped
}

func stopBotPollers(bots []pollingBot, stopped <-chan int64, alreadyStopped int) {
	for _, bot := range bots {
		bot.Stop()
	}
	for remaining := len(bots) - alreadyStopped; remaining > 0; remaining-- {
		<-stopped
	}
}
