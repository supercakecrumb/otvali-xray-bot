package vpnmonitor

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const transferDownload = "/api/monitor/transfer/download"
const transferUpload = "/api/monitor/transfer/upload"

// transferReceiver checks bounded synthetic data only. It is not an arbitrary
// proxy, storage endpoint or speed test, and does not return request content.
type transferReceiver struct {
	secret []byte
	now    func() time.Time
	slots  chan struct{}
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func NewTransferHandler(secret string) http.Handler {
	return &transferReceiver{secret: []byte(secret), now: time.Now, slots: make(chan struct{}, 6), tokens: 12}
}

func (h *transferReceiver) allow(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.last.IsZero() {
		h.tokens += now.Sub(h.last).Seconds()
		if h.tokens > 12 {
			h.tokens = 12
		}
	}
	h.last = now
	if h.tokens < 1 {
		return false
	}
	h.tokens--
	return true
}

func (h *transferReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	length := 32
	switch r.URL.Path {
	case transferDownload:
	case transferUpload:
		length = 65536
	default:
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
	now := h.now()
	if err != nil || strconv.FormatInt(epoch, 10) != stamp || time.Unix(epoch, 0).Before(now.Add(-5*time.Minute)) || time.Unix(epoch, 0).After(now.Add(time.Minute)) {
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(length)))
	if err != nil {
		w.WriteHeader(413)
		return
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(stamp + "\n" + r.URL.Path + "\n"))
	mac.Write(body)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		w.WriteHeader(401)
		return
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/octet-stream" || len(body) != length {
		w.WriteHeader(400)
		return
	}
	if !h.allow(now) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
		return
	}
	if r.URL.Path == transferDownload {
		digest := sha256.Sum256(append([]byte("OtvaliProbe/2.0\n"), body...))
		payload := bytes.Repeat(digest[:], 8192)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
		return
	}
	digest := sha256.Sum256(body)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Size   int    `json:"size"`
		SHA256 string `json:"sha256"`
	}{len(body), hex.EncodeToString(digest[:])})
}
