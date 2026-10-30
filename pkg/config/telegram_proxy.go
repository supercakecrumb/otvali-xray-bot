package config

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/supercakecrumb/otvali-xray-bot/internal/sshapi"
	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

var errTelegramProxyConfig = errors.New("invalid private Telegram proxy configuration")

// Secrets and restricted SSH keys are supplied together in a private read-only
// runtime directory. Nothing in this file is safe for diagnostic logging.
type telegramProxyFile struct {
	SecretKey          string                         `json:"secret_key"`
	AllowAllBound      bool                           `json:"allow_all_bound"`
	AllowedTelegramIDs []int64                        `json:"allowed_telegram_ids"`
	Endpoints          []telegramproxy.EndpointConfig `json:"endpoints"`
	Providers          []struct {
		ID            string `json:"id"`
		BaseURL       string `json:"base_url"`
		Authorization string `json:"authorization"`
		SSH           struct {
			Address        string `json:"address"`
			User           string `json:"user"`
			KeyPath        string `json:"key_path"`
			KnownHostsPath string `json:"known_hosts_path"`
		} `json:"ssh"`
	} `json:"providers"`
}

func loadTelegramProxy(getenv func(string) string) (telegramproxy.Config, map[string]sshapi.Config, error) {
	c := telegramproxy.Config{}
	switch getenv("TELEGRAM_PROXY_ENABLED") {
	case "", "false":
		return c, nil, nil
	case "true":
		c.Enabled = true
	default:
		return c, nil, errTelegramProxyConfig
	}
	path := getenv("TELEGRAM_PROXY_CONFIG_PATH")
	if !filepath.IsAbs(path) {
		return c, nil, errTelegramProxyConfig
	}
	f, err := os.Open(path)
	if err != nil {
		return c, nil, errTelegramProxyConfig
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 || info.Mode().Perm()&0027 != 0 {
		return c, nil, errTelegramProxyConfig
	}
	decoder := json.NewDecoder(io.LimitReader(f, 64<<10))
	decoder.DisallowUnknownFields()
	var raw telegramProxyFile
	if decoder.Decode(&raw) != nil || decoder.Decode(new(any)) != io.EOF {
		return c, nil, errTelegramProxyConfig
	}
	c.SecretKey, err = hex.DecodeString(raw.SecretKey)
	if err != nil || len(c.SecretKey) != 32 {
		return c, nil, errTelegramProxyConfig
	}
	c.AllowAllBound, c.Endpoints = raw.AllowAllBound, raw.Endpoints
	c.AllowedTelegramIDs = make(map[int64]struct{})
	for _, id := range raw.AllowedTelegramIDs {
		if id <= 0 {
			return c, nil, errTelegramProxyConfig
		}
		c.AllowedTelegramIDs[id] = struct{}{}
	}
	sshConfigs := make(map[string]sshapi.Config)
	for _, p := range raw.Providers {
		// The API destination is fixed on each host. It is never an input from
		// a Telegram callback or a browser request.
		if p.BaseURL != "http://127.0.0.1:9093" || !strings.HasPrefix(p.Authorization, "Bearer ") ||
			!filepath.IsAbs(p.SSH.KeyPath) || !filepath.IsAbs(p.SSH.KnownHostsPath) || p.SSH.User != "otvali-mtproto-api" {
			return c, nil, errTelegramProxyConfig
		}
		c.Providers = append(c.Providers, telegramproxy.ProviderConfig{ID: p.ID, BaseURL: p.BaseURL, Authorization: p.Authorization})
		sshConfigs[p.ID] = sshapi.Config{SSHAddr: p.SSH.Address, SSHUser: p.SSH.User,
			KeyPath: p.SSH.KeyPath, KnownHostsPath: p.SSH.KnownHostsPath, TargetAddress: "127.0.0.1:9093",
			DialTimeout: 4 * time.Second, RequestTimeout: 5 * time.Second}
	}
	if telegramproxy.ValidateConfig(c) != nil {
		return c, nil, errTelegramProxyConfig
	}
	return c, sshConfigs, nil
}
