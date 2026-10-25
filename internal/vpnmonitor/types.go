// Package vpnmonitor accepts bounded, signed observations from the home probe.
// It never receives or handles VPN credentials or subscription URLs.
package vpnmonitor

import (
	"errors"
	"regexp"
	"strconv"
	"time"
)

const Source = "home-assistant"
const Interval = 300
const StaleAfter = 15 * time.Minute

var ErrInvalid = errors.New("invalid monitor report")
var ErrReplay = errors.New("monitor report is not newer")

type Config struct {
	Enabled     bool
	Secret      string
	AlertChatID int64
}

func LoadConfig(get func(string) string) (Config, error) {
	raw := get("VPN_MONITOR_ENABLED")
	if raw == "" || raw == "false" {
		return Config{}, nil
	}
	if raw != "true" {
		return Config{}, errors.New("VPN_MONITOR_ENABLED must be true or false")
	}
	id, err := strconv.ParseInt(get("VPN_MONITOR_ALERT_CHAT_ID"), 10, 64)
	secret := get("VPN_MONITOR_SECRET")
	if err != nil || id <= 0 || len(secret) < 32 || len(secret) > 256 {
		return Config{}, errors.New("VPN monitor requires a strong secret and explicit owner chat ID")
	}
	return Config{Enabled: true, Secret: secret, AlertChatID: id}, nil
}

type Check struct {
	ID         string `json:"id"`
	Category   string `json:"category"`
	State      string `json:"state"`
	LatencyMS  int    `json:"latency_ms"`
	HTTPStatus int    `json:"http_status"`
	ErrorCode  string `json:"error_code"`
}
type Route struct {
	ID     string  `json:"id"`
	Label  string  `json:"label,omitempty"`
	Kind   string  `json:"kind"`
	State  string  `json:"state"`
	Checks []Check `json:"checks"`
}
type Report struct {
	Version         int       `json:"version"`
	Source          string    `json:"source"`
	CheckedAt       time.Time `json:"checked_at"`
	IntervalSeconds int       `json:"interval_seconds"`
	Routes          []Route   `json:"routes"`
}
type Snapshot struct {
	Report     Report
	ReceivedAt time.Time
	Available  bool
	Stale      bool
}

var RouteOrder = []string{"stockholm", "finland", "msk-stockholm", "msk-finland", "legacy-finland", "direct"}
var RouteLabels = map[string]string{
	"direct": "Без VPN", "stockholm": "Стокгольм", "finland": "Финляндия",
	"msk-stockholm": "Москва → Стокгольм", "msk-finland": "Москва → Финляндия", "legacy-finland": "Финляндия · старый ключ",
}
var Categories = []string{"ru", "world", "own"}
var CategoryLabels = map[string]string{"ru": "Россия", "world": "Зарубежные сайты", "own": "Наши сервисы"}
var CheckCategory = map[string]string{
	"ru-yandex": "ru", "ru-vk": "ru", "world-cloudflare": "world", "world-google": "world", "world-exit": "world",
	"own-guide": "own", "own-api": "own", "own-subscription": "own", "own-home-assistant": "own",
	"own-home-dokploy": "own", "own-jellyfin": "own", "own-qbit": "own",
}
var CheckLabels = map[string]string{
	"ru-yandex": "Яндекс", "ru-vk": "VK", "world-cloudflare": "Cloudflare", "world-google": "Google", "world-exit": "Выход в интернет",
	"own-guide": "Инструкция", "own-api": "API Otvali", "own-subscription": "Подписка", "own-home-assistant": "Home Assistant",
	"own-home-dokploy": "Домашняя панель", "own-jellyfin": "Медиатека", "own-qbit": "Загрузки",
}
var safeError = regexp.MustCompile(`^[a-z0-9_]{0,48}$`)

func Validate(r Report, now time.Time) error {
	if r.Version != 1 || r.Source != Source || r.IntervalSeconds != Interval || r.CheckedAt.IsZero() || r.CheckedAt.Before(now.Add(-10*time.Minute)) || r.CheckedAt.After(now.Add(time.Minute)) || len(r.Routes) != len(RouteOrder) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, route := range r.Routes {
		_, ok := RouteLabels[route.ID]
		kind := "vpn"
		if route.ID == "direct" {
			kind = "direct"
		}
		if !ok || seen[route.ID] || route.Kind != kind || len(route.Label) > 120 || len(route.Checks) != len(CheckCategory) {
			return ErrInvalid
		}
		if route.State != "ok" && route.State != "degraded" && route.State != "down" && route.State != "unknown" {
			return ErrInvalid
		}
		seen[route.ID] = true
		checks := map[string]bool{}
		for _, c := range route.Checks {
			category, ok := CheckCategory[c.ID]
			if !ok || checks[c.ID] || c.Category != category || (c.State != "ok" && c.State != "fail" && c.State != "unknown") || c.LatencyMS < 0 || c.LatencyMS > 120000 || c.HTTPStatus < 0 || c.HTTPStatus > 599 || !safeError.MatchString(c.ErrorCode) {
				return ErrInvalid
			}
			checks[c.ID] = true
		}
	}
	return nil
}

// CategoryState reflects all observed targets, not just a successful TCP socket.
// A partial target failure is degraded; missing observations never become green.
func CategoryState(route Route, category string) string {
	ok, fail, unknown := 0, 0, 0
	for _, c := range route.Checks {
		if c.Category == category {
			switch c.State {
			case "ok":
				ok++
			case "fail":
				fail++
			default:
				unknown++
			}
		}
	}
	if ok+fail == 0 {
		return "unknown"
	}
	if fail == 0 && unknown == 0 {
		return "ok"
	}
	if ok == 0 && unknown == 0 {
		return "down"
	}
	return "degraded"
}

func NetworkState(route Route) string {
	a, b := CategoryState(route, "ru"), CategoryState(route, "world")
	if a == "ok" && b == "ok" {
		return "ok"
	}
	if a == "down" && b == "down" {
		return "down"
	}
	if a == "unknown" || b == "unknown" {
		return "unknown"
	}
	return "degraded"
}
