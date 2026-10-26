package vpnmonitor

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func testReport(at time.Time) Report {
	r := Report{Version: 1, Source: Source, CheckedAt: at, IntervalSeconds: Interval}
	for _, id := range RouteOrder {
		route := Route{ID: id, Kind: "vpn", State: "ok"}
		if id == "direct" {
			route.Kind = "direct"
		}
		// Stable order also makes byte-identical retries reproducible.
		for _, checkID := range []string{"ru-yandex", "ru-vk", "world-cloudflare", "world-google", "world-exit", "own-guide", "own-api", "own-subscription", "own-home-assistant", "own-home-dokploy", "own-jellyfin", "own-qbit"} {
			route.Checks = append(route.Checks, Check{ID: checkID, Category: CheckCategory[checkID], State: "ok", LatencyMS: 45, HTTPStatus: 200})
		}
		r.Routes = append(r.Routes, route)
	}
	return r
}
func setCheck(r *Report, routeID, checkID, state string) {
	for i := range r.Routes {
		if r.Routes[i].ID == routeID {
			for j := range r.Routes[i].Checks {
				if r.Routes[i].Checks[j].ID == checkID {
					r.Routes[i].Checks[j].State = state
				}
			}
		}
	}
}
func newState() State { return State{ID: 1, CreatedAt: testTime, TrackerJSON: "{}", OutboxJSON: "[]"} }
func apply(t *testing.T, st *State, r Report) {
	t.Helper()
	raw, _ := json.Marshal(r)
	changed, err := applyReport(st, r, raw, r.CheckedAt)
	if err != nil || !changed {
		t.Fatalf("apply report: changed=%v err=%v", changed, err)
	}
}
func eventsOf(t *testing.T, st State) []Event {
	t.Helper()
	_, events, err := decodeState(st)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestValidateFixedCompleteMatrix(t *testing.T) {
	mutations := map[string]func(*Report){
		"missing route":   func(r *Report) { r.Routes = r.Routes[:5] },
		"duplicate route": func(r *Report) { r.Routes[1] = r.Routes[0] },
		"unknown route":   func(r *Report) { r.Routes[0].ID = "other" },
		"wrong kind":      func(r *Report) { r.Routes[0].Kind = "direct" },
		"missing check":   func(r *Report) { r.Routes[0].Checks = r.Routes[0].Checks[:10] },
		"duplicate check": func(r *Report) { r.Routes[0].Checks[1] = r.Routes[0].Checks[0] },
		"wrong category":  func(r *Report) { r.Routes[0].Checks[0].Category = "own" },
		"unsafe error":    func(r *Report) { r.Routes[0].Checks[0].ErrorCode = "https://private.invalid/token" },
		"old report":      func(r *Report) { r.CheckedAt = r.CheckedAt.Add(-11 * time.Minute) },
		"future report":   func(r *Report) { r.CheckedAt = r.CheckedAt.Add(2 * time.Minute) },
		"wrong source":    func(r *Report) { r.Source = "other-home" },
		"excess latency":  func(r *Report) { r.Routes[0].Checks[0].LatencyMS = 120001 },
	}
	if err := Validate(testReport(testTime), testTime); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r := testReport(testTime)
			mutate(&r)
			if Validate(r, testTime) == nil {
				t.Fatal("invalid matrix accepted")
			}
		})
	}
	r := testReport(testTime)
	for i := range r.Routes {
		for j := range r.Routes[i].Checks {
			r.Routes[i].Checks[j].State = "unknown"
		}
	}
	if Validate(r, testTime) != nil {
		t.Fatal("unknown observations must be representable without missing fields")
	}
}

type memoryIngester struct {
	calls    int
	err      error
	deadline bool
}

func (m *memoryIngester) Ingest(ctx context.Context, _ Report, _ []byte) error {
	m.calls++
	d, ok := ctx.Deadline()
	m.deadline = ok && time.Until(d) <= 5*time.Second
	return m.err
}
func signedRequest(body []byte, at time.Time, secret string) *http.Request {
	r := httptest.NewRequest("POST", "https://api.example/api/monitor/report", strings.NewReader(string(body)))
	stamp := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "\n"))
	mac.Write(body)
	r.Header.Set("X-Otvali-Timestamp", stamp)
	r.Header.Set("X-Otvali-Signature", hex.EncodeToString(mac.Sum(nil)))
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestReportReceiverRejectsUnsignedMalformedAndReplay(t *testing.T) {
	secret := strings.Repeat("test-only-key-", 4)
	for _, name := range []string{"valid", "bad-signature", "old-signature", "future-signature", "duplicate-header", "missing-header", "oversize", "unknown-field", "extra-json", "wrong-content-type", "query", "replay", "database-error"} {
		t.Run(name, func(t *testing.T) {
			sink := &memoryIngester{}
			h := NewHandler(secret, sink).(*receiver)
			h.now = func() time.Time { return testTime }
			raw, _ := json.Marshal(testReport(testTime))
			at := testTime
			want := 200
			switch name {
			case "old-signature":
				at = at.Add(-6 * time.Minute)
				want = 401
			case "future-signature":
				at = at.Add(2 * time.Minute)
				want = 401
			case "oversize":
				raw = []byte(strings.Repeat("x", 96*1024+1))
				want = 413
			case "unknown-field":
				raw = append([]byte(`{"secret":"never-reflect",`), raw[1:]...)
				want = 400
			case "extra-json":
				raw = append(raw, []byte(`{}`)...)
				want = 400
			case "replay":
				sink.err = ErrReplay
				want = 409
			case "database-error":
				sink.err = errors.New("private database credentials")
				want = 503
			}
			req := signedRequest(raw, at, secret)
			switch name {
			case "bad-signature":
				req.Header.Set("X-Otvali-Signature", strings.Repeat("0", 64))
				want = 401
			case "duplicate-header":
				req.Header.Add("X-Otvali-Timestamp", req.Header.Get("X-Otvali-Timestamp"))
				want = 401
			case "missing-header":
				req.Header.Del("X-Otvali-Signature")
				want = 401
			case "wrong-content-type":
				req.Header.Set("Content-Type", "text/plain")
				want = 400
			case "query":
				req.URL.RawQuery = "secret=hidden"
				want = 400
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != want {
				t.Fatalf("got %d, want %d", response.Code, want)
			}
			wantCalls := 0
			if name == "valid" || name == "replay" || name == "database-error" {
				wantCalls = 1
			}
			if sink.calls != wantCalls || sink.calls > 0 && !sink.deadline {
				t.Fatal("authentication must precede bounded persistence")
			}
			if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "never-reflect") {
				t.Fatal("response leaked input/error")
			}
		})
	}
}

func TestDebounceSurvivesSerializedRestartAndRejectsReplays(t *testing.T) {
	st := newState()
	r := testReport(testTime)
	setCheck(&r, "msk-stockholm", "world-google", "fail")
	apply(t, &st, r)
	raw, _ := json.Marshal(r)
	if changed, err := applyReport(&st, r, raw, testTime); changed || err != nil {
		t.Fatal("identical retry should be idempotent")
	}
	fast := testReport(testTime.Add(time.Minute))
	fastRaw, _ := json.Marshal(fast)
	if _, err := applyReport(&st, fast, fastRaw, fast.CheckedAt); !errors.Is(err, ErrReplay) {
		t.Fatal("an accelerated retry must not count as another probe cycle")
	}
	if len(eventsOf(t, st)) != 0 {
		t.Fatal("one failed observation must not alert")
	}
	// Full process state is persisted as JSON; recreate it before the next cycle.
	saved, _ := json.Marshal(st)
	st = State{}
	if json.Unmarshal(saved, &st) != nil {
		t.Fatal("restore failed")
	}
	r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
	apply(t, &st, r)
	events := eventsOf(t, st)
	if len(events) != 1 || events[0].Key != "msk-stockholm/world" || events[0].Recovery {
		t.Fatal("two cycles must open exactly one incident")
	}
	r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
	apply(t, &st, r)
	if len(eventsOf(t, st)) != 1 {
		t.Fatal("steady failure spammed")
	}
	setCheck(&r, "msk-stockholm", "world-google", "ok")
	r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
	apply(t, &st, r)
	if len(eventsOf(t, st)) != 1 {
		t.Fatal("one success recovered early")
	}
	r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
	apply(t, &st, r)
	events = eventsOf(t, st)
	if len(events) != 2 || !events[1].Recovery {
		t.Fatal("two successes must recover")
	}
}

func TestIncompleteCyclesAndLongGapDoNotConfirmFailure(t *testing.T) {
	for _, mode := range []string{"unknown", "gap"} {
		t.Run(mode, func(t *testing.T) {
			st := newState()
			r := testReport(testTime)
			setCheck(&r, "finland", "world-google", "fail")
			apply(t, &st, r)
			if mode == "unknown" {
				r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
				setCheck(&r, "finland", "world-google", "ok")
				setCheck(&r, "finland", "world-cloudflare", "unknown")
				apply(t, &st, r)
				setCheck(&r, "finland", "world-cloudflare", "ok")
				setCheck(&r, "finland", "world-google", "fail")
				r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
			} else {
				r.CheckedAt = r.CheckedAt.Add(15 * time.Minute)
			}
			apply(t, &st, r)
			if len(eventsOf(t, st)) != 0 {
				t.Fatal("incomplete/discontinuous observations confirmed an incident")
			}
		})
	}
}

func TestKnownFailureIsNotHiddenByUnknownExitCheck(t *testing.T) {
	st := newState()
	r := testReport(testTime)
	setCheck(&r, "msk-stockholm", "world-google", "fail")
	setCheck(&r, "msk-stockholm", "world-exit", "unknown")
	apply(t, &st, r)
	r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
	apply(t, &st, r)
	events := eventsOf(t, st)
	if len(events) != 1 || events[0].Key != "msk-stockholm/world" {
		t.Fatal("a missing exit observation hid a known connection failure")
	}
}

func TestHomeOutageCannotRecoverFromUnknownObservations(t *testing.T) {
	st := newState()
	r := testReport(testTime)
	for i := range r.Routes {
		for j := range r.Routes[i].Checks {
			if r.Routes[i].Checks[j].Category != "own" {
				r.Routes[i].Checks[j].State = "fail"
			}
		}
	}
	apply(t, &st, r)
	r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
	apply(t, &st, r)
	for i := range r.Routes {
		for j := range r.Routes[i].Checks {
			r.Routes[i].Checks[j].State = "unknown"
		}
	}
	for i := 0; i < 2; i++ {
		r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
		apply(t, &st, r)
	}
	for _, event := range eventsOf(t, st) {
		if event.Key == "home-network" && event.Recovery {
			t.Fatal("unknown observations falsely recovered home connectivity")
		}
	}
	for i := 0; i < 2; i++ {
		r = testReport(r.CheckedAt.Add(5 * time.Minute))
		apply(t, &st, r)
	}
	recovered := false
	for _, event := range eventsOf(t, st) {
		if event.Key == "home-network" && event.Recovery {
			recovered = true
		}
	}
	if !recovered {
		t.Fatal("two positive cycles did not recover home connectivity")
	}
}

func TestStaleSourceAlertsOnceAndRecoversAfterTwoReports(t *testing.T) {
	st := newState()
	apply(t, &st, testReport(testTime))
	if err := markStale(&st, testTime.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := markStale(&st, testTime.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	events := eventsOf(t, st)
	if len(events) != 1 || events[0].Key != "source" {
		t.Fatal("missing source must produce one separate alert")
	}
	apply(t, &st, testReport(testTime.Add(20*time.Minute)))
	if len(eventsOf(t, st)) != 1 {
		t.Fatal("source recovered after one report")
	}
	apply(t, &st, testReport(testTime.Add(25*time.Minute)))
	events = eventsOf(t, st)
	if len(events) != 2 || !events[1].Recovery {
		t.Fatal("source did not recover after two fresh reports")
	}
}

func TestUnknownConfigurationAndHomeOutageRemainDistinct(t *testing.T) {
	for _, mode := range []string{"unknown-config", "home-outage", "foreign-direct-only"} {
		t.Run(mode, func(t *testing.T) {
			r := testReport(testTime)
			for i := range r.Routes {
				for j := range r.Routes[i].Checks {
					c := &r.Routes[i].Checks[j]
					if mode == "unknown-config" && r.Routes[i].ID == "stockholm" {
						c.State = "unknown"
					}
					if mode == "home-outage" && c.Category != "own" {
						c.State = "fail"
					}
					if mode == "foreign-direct-only" && (r.Routes[i].ID == "direct" || r.Routes[i].ID == "msk-finland") && c.Category == "world" {
						c.State = "fail"
					}
				}
			}
			st := newState()
			apply(t, &st, r)
			r.CheckedAt = r.CheckedAt.Add(5 * time.Minute)
			apply(t, &st, r)
			events := eventsOf(t, st)
			if mode == "unknown-config" && (len(events) != 1 || events[0].Key != "stockholm/probe") {
				t.Fatal("unknown config misreported as internet outage")
			}
			if mode == "home-outage" && (len(events) != 1 || events[0].Key != "home-network") {
				t.Fatal("home outage blamed all VPN routes")
			}
			if mode == "foreign-direct-only" && (len(events) != 2) {
				t.Fatal("direct failure must not suppress a VPN failure")
			}
		})
	}
}

func TestPublicStatusHidesPrivateChecksAndDoesNotConfuseServicesWithInternet(t *testing.T) {
	r := testReport(testTime)
	r.Routes[0].Label = "<script>private-home-name</script>"
	setCheck(&r, "stockholm", "own-home-assistant", "fail")
	s := Snapshot{Available: true, Report: r}
	text := RenderOverview(s, false) + RenderRoute(s, "stockholm", false)
	for _, private := range []string{"Home Assistant", "Домашняя панель", "Медиатека", "Загрузки", "домашнего", "private-home-name", "<script>", "Проблема отдельного", "Сервисы", "сервисов", "Наши сервисы", "API Otvali", "Инструкция", "Подписка"} {
		if strings.Contains(text, private) {
			t.Fatalf("public status exposed private signal %s", private)
		}
	}
	if !strings.Contains(text, "Интернет работает через все маршруты") || !strings.Contains(text, "Проверка из России") {
		t.Fatal("public overview lost internet scope/source")
	}
	if !strings.Contains(RenderRoute(s, "stockholm", true), "Home Assistant") {
		t.Fatal("owner lost private diagnostics")
	}
	for _, id := range RouteOrder {
		setCheck(&r, id, "own-guide", "fail")
	}
	s.Report = r
	text = RenderOverview(s, false) + RenderRoute(s, "stockholm", false)
	if !strings.Contains(text, "Интернет работает через все маршруты") || strings.Contains(text, "сервисов") || strings.Contains(text, "Инструкция") {
		t.Fatal("private service failure must not alter the public view")
	}
	s.Stale = true
	text = RenderOverview(s, false) + RenderRoute(s, "stockholm", false)
	if strings.Contains(text, "🟢") || !strings.Contains(text, "доступность неизвестна") {
		t.Fatal("stale data presented as current green")
	}
}

func TestMonitorConfigDefaultsOffAndRequiresExplicitOwner(t *testing.T) {
	values := map[string]string{}
	get := func(k string) string { return values[k] }
	if c, err := LoadConfig(get); err != nil || c.Enabled {
		t.Fatal("monitor must default off")
	}
	values["VPN_MONITOR_ENABLED"] = "true"
	values["VPN_MONITOR_SECRET"] = strings.Repeat("x", 32)
	for _, id := range []string{"", "0", "-42", "owner"} {
		values["VPN_MONITOR_ALERT_CHAT_ID"] = id
		if _, err := LoadConfig(get); err == nil {
			t.Fatal("invalid owner accepted")
		}
	}
	values["VPN_MONITOR_ALERT_CHAT_ID"] = "42"
	if c, err := LoadConfig(get); err != nil || !c.Enabled || c.AlertChatID != 42 {
		t.Fatal("valid config rejected")
	}
}
