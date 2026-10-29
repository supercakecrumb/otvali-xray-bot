package miniapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type activityBackend interface {
	RecordActivity(context.Context, int64, string)
}
type activityStore interface {
	RecordActivity(context.Context, int64, string, string, string, time.Time) error
}

func (h *handler) recordActivity(ctx context.Context, actor int64, kind string) {
	if backend, ok := h.backend.(activityBackend); ok {
		backend.RecordActivity(ctx, actor, kind)
	}
}

// A successful HTTP operation is counted, not proof that the browser received
// or imported the credential. Each HTTP request has a fresh server-generated
// event identity; unlike Telegram update redelivery, client retries are new
// HTTP operations. No initData, URL, key ID, body or IP enters the record.
func (s *Service) RecordActivity(parent context.Context, actor int64, kind string) {
	ctx, cancel := context.WithTimeout(parent, 250*time.Millisecond)
	defer cancel()
	store, ok := s.requestStore(ctx).(activityStore)
	if !ok {
		return
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return
	}
	_ = store.RecordActivity(ctx, actor, "miniapp", kind, hex.EncodeToString(nonce[:]), s.now())
}
