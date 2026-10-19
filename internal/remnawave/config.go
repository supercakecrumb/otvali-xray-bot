package remnawave

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type Config struct {
	Enabled            bool
	BaseURL            string
	APIToken           string
	SquadUUID          string
	SubscriptionOrigin string
	AllowedTelegramIDs map[int64]struct{}
	AccessDays         int
}

func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{}
	switch getenv("REMNA_ENABLED") {
	case "", "false":
		return c, nil
	case "true":
		c.Enabled = true
	default:
		return c, errors.New("REMNA_ENABLED must be true or false")
	}
	c.BaseURL = strings.TrimRight(getenv("REMNA_BASE_URL"), "/")
	c.SubscriptionOrigin = strings.TrimRight(getenv("REMNA_SUBSCRIPTION_ORIGIN"), "/")
	if !validOrigin(c.BaseURL) || !validOrigin(c.SubscriptionOrigin) {
		return Config{}, errors.New("Remnawave API and subscription origins must be HTTPS origins without credentials or paths")
	}
	c.APIToken = strings.TrimSpace(getenv("REMNA_API_TOKEN"))
	if c.APIToken == "" || strings.ContainsAny(c.APIToken, "\r\n") {
		return Config{}, errors.New("REMNA_API_TOKEN is required")
	}
	squad, err := uuid.Parse(getenv("REMNA_SQUAD_UUID"))
	if err != nil || squad == uuid.Nil {
		return Config{}, errors.New("REMNA_SQUAD_UUID must be a nonzero UUID")
	}
	c.SquadUUID = squad.String()
	// Zero uses Remnawave's documented 2099 no-expiry sentinel.
	if raw := getenv("REMNA_ACCESS_DAYS"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 || days > 3660 {
			return Config{}, errors.New("REMNA_ACCESS_DAYS must be 0 for no automatic expiry or between 1 and 3660")
		}
		c.AccessDays = days
	}
	c.AllowedTelegramIDs = make(map[int64]struct{})
	for _, raw := range strings.Split(getenv("REMNA_ALLOWED_TELEGRAM_IDS"), ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			return Config{}, errors.New("REMNA_ALLOWED_TELEGRAM_IDS must list positive numeric Telegram IDs")
		}
		c.AllowedTelegramIDs[id] = struct{}{}
	}
	return c, nil
}

func validOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}
