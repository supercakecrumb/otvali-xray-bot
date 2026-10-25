package vpnmonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// State is a singleton row: observations, debounce and outbox commit together.
// No user credentials, private URLs or Telegram payloads are persisted here.
type State struct {
	ID               uint `gorm:"primaryKey"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CheckedAt        time.Time
	ReceivedAt       time.Time
	Digest           string
	ReportJSON       string `gorm:"type:text"`
	TrackerJSON      string `gorm:"type:text"`
	OutboxJSON       string `gorm:"type:text"`
	LastAttemptAt    time.Time
	DeliveryFailures int
}

func (State) TableName() string { return "vpn_monitor_states" }

type Incident struct {
	Failures  int       `json:"failures"`
	Successes int       `json:"successes"`
	Open      bool      `json:"open"`
	Since     time.Time `json:"since"`
}
type Event struct {
	Key      string    `json:"key"`
	Recovery bool      `json:"recovery"`
	At       time.Time `json:"at"`
	Since    time.Time `json:"since"`
}
type Service struct {
	db     *gorm.DB
	config Config
	now    func() time.Time
}

func NewService(db *gorm.DB, config Config) (*Service, error) {
	if db == nil || !config.Enabled || config.AlertChatID <= 0 || len(config.Secret) < 32 {
		return nil, ErrInvalid
	}
	if err := db.AutoMigrate(&State{}); err != nil {
		return nil, errors.New("monitor state migration failed")
	}
	s := &Service{db: db, config: config, now: time.Now}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&State{ID: 1, CreatedAt: s.now(), TrackerJSON: "{}", OutboxJSON: "[]"}).Error; err != nil {
		return nil, errors.New("monitor state initialization failed")
	}
	return s, nil
}

func decodeState(st State) (map[string]Incident, []Event, error) {
	tracker := map[string]Incident{}
	var events []Event
	if json.Unmarshal([]byte(st.TrackerJSON), &tracker) != nil || json.Unmarshal([]byte(st.OutboxJSON), &events) != nil {
		return nil, nil, errors.New("invalid monitor state")
	}
	return tracker, events, nil
}
func encodeState(st *State, tracker map[string]Incident, events []Event) {
	b, _ := json.Marshal(tracker)
	st.TrackerJSON = string(b)
	b, _ = json.Marshal(events)
	st.OutboxJSON = string(b)
}

func step(tracker map[string]Incident, key, observation string, at time.Time) *Event {
	i := tracker[key]
	if observation == "unknown" {
		i.Failures = 0
		i.Successes = 0
		tracker[key] = i
		return nil
	}
	var result *Event
	if observation == "ok" {
		i.Failures = 0
		i.Successes++
		if i.Successes >= 2 && i.Open {
			result = &Event{Key: key, Recovery: true, At: at, Since: i.Since}
			i.Open = false
			i.Since = time.Time{}
		}
	} else {
		i.Successes = 0
		i.Failures++
		if i.Failures == 1 && !i.Open {
			i.Since = at
		}
		if i.Failures >= 2 && !i.Open {
			i.Open = true
			result = &Event{Key: key, At: at, Since: i.Since}
		}
	}
	if i.Failures > 2 {
		i.Failures = 2
	}
	if i.Successes > 2 {
		i.Successes = 2
	}
	tracker[key] = i
	return result
}

func applyReport(st *State, r Report, raw []byte, now time.Time) (bool, error) {
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	// PostgreSQL timestamps have microsecond precision; byte-identical retries
	// remain idempotent even when the original RFC3339 value used nanoseconds.
	if digest == st.Digest && st.ReportJSON != "" {
		return false, nil
	}
	if !st.CheckedAt.IsZero() && !r.CheckedAt.After(st.CheckedAt) {
		return false, ErrReplay
	}
	// Distinct authenticated requests are not necessarily distinct probe cycles.
	// Refuse accelerated reports so retries cannot confirm an incident early.
	if !st.CheckedAt.IsZero() && r.CheckedAt.Sub(st.CheckedAt) < 4*time.Minute {
		return false, ErrReplay
	}
	tracker, events, err := decodeState(*st)
	if err != nil {
		return false, err
	}
	// A long collection gap interrupts consecutive observations.
	if !st.CheckedAt.IsZero() && r.CheckedAt.Sub(st.CheckedAt) > 2*Interval*time.Second {
		for key, i := range tracker {
			i.Failures = 0
			i.Successes = 0
			tracker[key] = i
		}
	}
	allInternetDown := len(r.Routes) == len(RouteOrder)
	positiveInternet := false
	for _, route := range r.Routes {
		allInternetDown = allInternetDown && NetworkState(route) == "down"
		for _, c := range route.Checks {
			if (c.Category == "ru" || c.Category == "world") && c.State == "ok" {
				positiveInternet = true
			}
		}
	}
	homeState := "unknown"
	if positiveInternet {
		homeState = "ok"
	}
	if allInternetDown {
		homeState = "down"
	}
	if event := step(tracker, "home-network", homeState, r.CheckedAt); event != nil {
		events = append(events, *event)
	}
	for _, route := range r.Routes {
		known := false
		for _, c := range route.Checks {
			if c.State != "unknown" {
				known = true
				break
			}
		}
		probeState := "fail"
		if known {
			probeState = "ok"
		}
		if event := step(tracker, route.ID+"/probe", probeState, r.CheckedAt); event != nil {
			events = append(events, *event)
		}
		for _, category := range Categories {
			observation := CategoryState(route, category)
			unknown, failed := false, false
			for _, c := range route.Checks {
				if c.Category != category {
					continue
				}
				unknown = unknown || c.State == "unknown"
				failed = failed || c.State == "fail"
			}
			// Missing observations alone are not a failure, but cannot hide a
			// conclusive failed target in the same category.
			if unknown && !failed {
				observation = "unknown"
			}
			if allInternetDown && category != "own" {
				observation = "unknown"
			}
			if event := step(tracker, route.ID+"/"+category, observation, r.CheckedAt); event != nil {
				events = append(events, *event)
			}
		}
	}
	if event := step(tracker, "source", "ok", r.CheckedAt); event != nil {
		events = append(events, *event)
	}
	// Bound storage if Telegram has been unavailable for a long period. Recent
	// transitions carry the current state; old undelivered pairs are superseded.
	if len(events) > 128 {
		events = events[len(events)-128:]
	}
	st.CheckedAt = r.CheckedAt
	st.ReceivedAt = now
	st.Digest = digest
	st.ReportJSON = string(raw)
	encodeState(st, tracker, events)
	return true, nil
}

func (s *Service) Ingest(ctx context.Context, r Report, raw []byte) error {
	if err := Validate(r, s.now()); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var st State
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&st, 1).Error; err != nil {
			return err
		}
		changed, err := applyReport(&st, r, raw, s.now())
		if err != nil || !changed {
			return err
		}
		return tx.Save(&st).Error
	})
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	var st State
	if err := s.db.WithContext(ctx).First(&st, 1).Error; err != nil {
		return Snapshot{}, err
	}
	if st.ReportJSON == "" {
		return Snapshot{}, nil
	}
	var r Report
	if json.Unmarshal([]byte(st.ReportJSON), &r) != nil {
		return Snapshot{}, errors.New("invalid monitor snapshot")
	}
	return Snapshot{Report: r, ReceivedAt: st.ReceivedAt, Available: true, Stale: s.now().Sub(r.CheckedAt) > StaleAfter}, nil
}

func markStale(st *State, now time.Time) error {
	last := st.CheckedAt
	if last.IsZero() {
		last = st.CreatedAt
	}
	if now.Sub(last) <= StaleAfter {
		return nil
	}
	tracker, events, err := decodeState(*st)
	if err != nil {
		return err
	}
	i := tracker["source"]
	if !i.Open {
		i.Open = true
		i.Since = last
		i.Failures = 2
		i.Successes = 0
		tracker["source"] = i
		events = append(events, Event{Key: "source", At: now, Since: last})
		encodeState(st, tracker, events)
	}
	return nil
}

type Deliver func(context.Context, int64, string) error

func retryDelay(failures int) time.Duration {
	if failures > 3 {
		return 15 * time.Minute
	}
	if failures < 0 {
		failures = 0
	}
	return time.Minute * time.Duration(1<<failures)
}

func (s *Service) Tick(ctx context.Context, deliver Deliver) error {
	var pending []Event
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var st State
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&st, 1).Error; err != nil {
			return err
		}
		if err := markStale(&st, s.now()); err != nil {
			return err
		}
		_, events, err := decodeState(st)
		if err != nil {
			return err
		}
		if len(events) > 0 && s.now().Sub(st.LastAttemptAt) >= retryDelay(st.DeliveryFailures) {
			pending = append(pending, events...)
			st.LastAttemptAt = s.now()
		}
		return tx.Save(&st).Error
	})
	if err != nil || len(pending) == 0 || deliver == nil {
		return err
	}
	if err = deliver(ctx, s.config.AlertChatID, RenderAlert(pending)); err != nil {
		// Persist the backoff across restarts without exposing Telegram errors.
		_ = s.db.WithContext(ctx).Model(&State{}).Where("id = ?", 1).UpdateColumn("delivery_failures", gorm.Expr("LEAST(delivery_failures + 1, 5)")).Error
		return errors.New("monitor notification delivery failed")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var st State
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&st, 1).Error; err != nil {
			return err
		}
		tracker, events, err := decodeState(st)
		if err != nil {
			return err
		}
		if len(events) >= len(pending) {
			same := true
			for i := range pending {
				if events[i] != pending[i] {
					same = false
					break
				}
			}
			if same {
				events = events[len(pending):]
			}
		}
		encodeState(&st, tracker, events)
		st.DeliveryFailures = 0
		return tx.Save(&st).Error
	})
}

func (s *Service) Run(ctx context.Context, deliver Deliver, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		iteration, cancel := context.WithTimeout(ctx, 25*time.Second)
		if err := s.Tick(iteration, deliver); err != nil && ctx.Err() == nil {
			logger.Warn("VPN monitor notification unavailable")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func RenderAlert(events []Event) string {
	latest := map[string]Event{}
	for _, e := range events {
		latest[e.Key] = e
	}
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("<b>Otvali · изменение доступности</b>\n\n")
	for _, key := range keys {
		e := latest[key]
		icon, word := "🔴", "сбой подтверждён"
		if e.Recovery {
			icon, word = "🟢", "восстановилось"
		}
		if key == "source" {
			if e.Recovery {
				b.WriteString("🟢 Проверки с домашнего сервера возобновились.\n")
			} else {
				b.WriteString("⚪ Домашний сервер не присылает результаты более 15 минут. Состояние VPN неизвестно.\n")
			}
			continue
		}
		if key == "home-network" {
			if e.Recovery {
				b.WriteString("🟢 Интернет с проверочного сервера восстановился.\n")
			} else {
				b.WriteString("🔴 Интернет недоступен с проверочного сервера и напрямую, и через VPN. Возможна проблема домашнего подключения; отдельные VPN-маршруты пока оценить нельзя.\n")
			}
			continue
		}
		parts := strings.Split(key, "/")
		if len(parts) != 2 {
			continue
		}
		category := CategoryLabels[parts[1]]
		if parts[1] == "probe" {
			category = "нет результатов проверки"
			if e.Recovery {
				category = "проверка конфигурации"
			}
		}
		b.WriteString(fmt.Sprintf("%s %s · %s: %s.\n", icon, RouteLabels[parts[0]], category, word))
	}
	b.WriteString("\nДва последовательных результата подтверждают сбой или восстановление. Проверка с домашнего сервера; состояние телефона может отличаться.")
	return b.String()
}
