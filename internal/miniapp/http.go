package miniapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrForbidden      = errors.New("forbidden")
	ErrAccessDisabled = errors.New("access disabled")
	ErrUnavailable    = errors.New("unavailable")
	ErrNotFound       = errors.New("not found")
)

type Account struct {
	Managed            bool   `json:"managed"`
	LegacyFinland      bool   `json:"legacy_finland"`
	ManagedState       string `json:"managed_state,omitempty"`
	LegacyFinlandState string `json:"legacy_finland_state,omitempty"`
}
type Subscription struct {
	URL               string     `json:"url"`
	ExpiresAt         *time.Time `json:"expires_at"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
}
type Key struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}
type Backend interface {
	Account(context.Context, int64) (Account, error)
	Subscription(context.Context, int64) (Subscription, error)
	Keys(context.Context, int64) ([]Key, error)
	Key(context.Context, int64, string) (Key, error)
}
type Pinger interface{ PingContext(context.Context) error }

type bucket struct {
	start time.Time
	count int
}
type handler struct {
	token    string
	origins  map[string]bool
	backend  Backend
	health   Pinger
	now      func() time.Time
	mutex    sync.Mutex
	global   bucket
	users    map[int64]bucket
	inFlight chan struct{}
}

func NewHandler(c Config, token string, backend Backend, health Pinger) (http.Handler, error) {
	if !c.Enabled || token == "" || backend == nil || len(c.AllowedOrigins) == 0 {
		return nil, errors.New("invalid Mini App configuration")
	}
	h := &handler{token: token, backend: backend, health: health, now: time.Now, origins: map[string]bool{}, users: map[int64]bucket{}, inFlight: make(chan struct{}, 8)}
	for _, origin := range c.AllowedOrigins {
		h.origins[origin] = true
	}
	return h, nil
}

func NewHTTPServer(address string, h http.Handler) *http.Server {
	return &http.Server{Addr: address, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 24 * 1024, ErrorLog: log.New(io.Discard, "", 0)}
}

var keyID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Vary", "Origin")
	// No access/error logger receives Authorization, request bodies or responses.
	defer func() {
		if recover() != nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable")
		}
	}()
	if r.URL.RawQuery != "" {
		writeError(w, 400, "invalid_request")
		return
	}
	if r.URL.Path == "/health" {
		if r.Method != http.MethodGet {
			writeError(w, 405, "method_not_allowed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if h.health == nil || h.health.PingContext(ctx) != nil {
			writeError(w, 503, "unavailable")
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	known := r.URL.Path == "/api/miniapp/session" || r.URL.Path == "/api/miniapp/subscription" || r.URL.Path == "/api/miniapp/keys" || r.URL.Path == "/api/miniapp/key"
	if !known {
		writeError(w, 404, "not_found")
		return
	}
	origin := r.Header.Get("Origin")
	if !h.origins[origin] {
		writeError(w, 403, "forbidden")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	if r.Method == http.MethodOptions {
		if r.Header.Get("Access-Control-Request-Method") != "POST" {
			writeError(w, 405, "method_not_allowed")
			return
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			header = strings.ToLower(strings.TrimSpace(header))
			if header != "" && header != "authorization" && header != "content-type" {
				writeError(w, 400, "invalid_request")
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed")
		return
	}
	authorization := r.Header.Values("Authorization")
	if len(authorization) != 1 || !strings.HasPrefix(authorization[0], "tma ") {
		writeError(w, 401, "unauthorized")
		return
	}
	actorID, err := ValidateInitData(strings.TrimPrefix(authorization[0], "tma "), h.token, h.now())
	if err != nil {
		writeError(w, 401, "unauthorized")
		return
	}
	if !h.allow(actorID) {
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "rate_limited")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, 400, "invalid_request")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	var payload any = &struct{}{}
	if r.URL.Path == "/api/miniapp/key" {
		payload = &body
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(payload) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, 400, "invalid_request")
		return
	}
	if (r.URL.Path != "/api/miniapp/key" && body.ID != "") || (r.URL.Path == "/api/miniapp/key" && body.ID != "legacy_fi" && !keyID.MatchString(body.ID)) {
		writeError(w, 400, "invalid_request")
		return
	}
	select {
	case h.inFlight <- struct{}{}:
		defer func() { <-h.inFlight }()
	default:
		writeError(w, 429, "rate_limited")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	switch r.URL.Path {
	case "/api/miniapp/session":
		account, err := h.backend.Account(ctx, actorID)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		writeJSON(w, 200, map[string]Account{"account": account})
	case "/api/miniapp/subscription":
		sub, err := h.backend.Subscription(ctx, actorID)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		writeJSON(w, 200, map[string]Subscription{"subscription": sub})
	case "/api/miniapp/keys":
		keys, err := h.backend.Keys(ctx, actorID)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		// Enforce metadata-only output even if an implementation returns a URL.
		for i := range keys {
			keys[i].URL = ""
		}
		if keys == nil {
			keys = []Key{}
		}
		writeJSON(w, 200, map[string][]Key{"keys": keys})
	case "/api/miniapp/key":
		key, err := h.backend.Key(ctx, actorID, body.ID)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		writeJSON(w, 200, map[string]Key{"key": key})
	}
}

func (h *handler) allow(id int64) bool {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	now := h.now()
	if now.Sub(h.global.start) >= time.Minute {
		h.global = bucket{start: now}
		for key, value := range h.users {
			if now.Sub(value.start) >= time.Minute {
				delete(h.users, key)
			}
		}
	}
	user := h.users[id]
	if now.Sub(user.start) >= time.Minute {
		user = bucket{start: now}
	}
	if h.global.count >= 300 || user.count >= 20 || (len(h.users) >= 1024 && user.count == 0) {
		return false
	}
	h.global.count++
	user.count++
	h.users[id] = user
	return true
}

func writeBackendError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrAccessDisabled):
		writeError(w, 403, "access_disabled")
	case errors.Is(err, ErrForbidden):
		writeError(w, 403, "forbidden")
	case errors.Is(err, ErrNotFound):
		writeError(w, 404, "not_found")
	default:
		writeError(w, 503, "unavailable")
	}
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
