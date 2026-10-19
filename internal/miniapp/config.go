package miniapp

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type Config struct {
	Enabled        bool
	Port           int
	AllowedOrigins []string
}

func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{Port: 8080}
	switch getenv("MINIAPP_ENABLED") {
	case "", "false":
		return c, nil
	case "true":
		c.Enabled = true
	default:
		return Config{}, errors.New("MINIAPP_ENABLED must be true or false")
	}
	if raw := getenv("MINIAPP_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1024 || port > 65535 {
			return Config{}, errors.New("MINIAPP_PORT must be between 1024 and 65535")
		}
		c.Port = port
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(getenv("MINIAPP_ALLOWED_ORIGINS"), ",") {
		raw = strings.TrimSpace(raw)
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return Config{}, errors.New("MINIAPP_ALLOWED_ORIGINS must contain exact HTTPS origins")
		}
		if !seen[raw] {
			c.AllowedOrigins = append(c.AllowedOrigins, raw)
			seen[raw] = true
		}
	}
	if len(c.AllowedOrigins) == 0 || len(c.AllowedOrigins) > 8 {
		return Config{}, errors.New("MINIAPP_ALLOWED_ORIGINS must contain 1 to 8 origins")
	}
	return c, nil
}
