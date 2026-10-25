package vpnmonitor

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
)

type Ingester interface {
	Ingest(context.Context, Report, []byte) error
}
type receiver struct {
	secret []byte
	ingest Ingester
	now    func() time.Time
	slots  chan struct{}
}

func NewHandler(secret string, ingest Ingester) http.Handler {
	return &receiver{secret: []byte(secret), ingest: ingest, now: time.Now, slots: make(chan struct{}, 2)}
}
func (s *Service) Handler() http.Handler { return NewHandler(s.config.Secret, s) }

func (h *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != "/api/monitor/report" {
		w.WriteHeader(404)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if r.URL.RawQuery != "" {
		w.WriteHeader(400)
		return
	}
	if len(r.Header.Values("X-Otvali-Timestamp")) != 1 || len(r.Header.Values("X-Otvali-Signature")) != 1 {
		w.WriteHeader(401)
		return
	}
	stamp := r.Header.Get("X-Otvali-Timestamp")
	epoch, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || strconv.FormatInt(epoch, 10) != stamp || time.Unix(epoch, 0).Before(h.now().Add(-5*time.Minute)) || time.Unix(epoch, 0).After(h.now().Add(time.Minute)) {
		w.WriteHeader(401)
		return
	}
	sig, err := hex.DecodeString(r.Header.Get("X-Otvali-Signature"))
	if err != nil || len(sig) != sha256.Size || len(h.secret) < 32 {
		w.WriteHeader(401)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		w.WriteHeader(429)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 96*1024))
	if err != nil {
		w.WriteHeader(413)
		return
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(stamp + "\n"))
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		w.WriteHeader(401)
		return
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/json" {
		w.WriteHeader(400)
		return
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || decoder.Decode(&struct{}{}) != io.EOF || Validate(report, h.now()) != nil {
		w.WriteHeader(400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.ingest.Ingest(ctx, report, body); err != nil {
		if errors.Is(err, ErrReplay) {
			w.WriteHeader(409)
		} else {
			w.WriteHeader(503)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
