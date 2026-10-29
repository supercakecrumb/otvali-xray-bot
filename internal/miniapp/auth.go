package miniapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

const (
	maxInitData    = 16 * 1024
	maxInitDataAge = time.Hour
)

// ValidateInitData implements Telegram's bot-token HMAC contract. The modern
// signature field remains in the check string; only hash is excluded.
// https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app
func ValidateInitData(raw, botToken string, now time.Time) (int64, error) {
	if raw == "" || len(raw) > maxInitData || botToken == "" {
		return 0, ErrUnauthorized
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return 0, ErrUnauthorized
	}
	fields := make([]string, 0, len(values))
	for key, entries := range values {
		if len(entries) != 1 || key == "" || (strings.ContainsAny(key, "\r\n=") || strings.ContainsAny(entries[0], "\r\n")) {
			return 0, ErrUnauthorized
		}
		if key != "hash" {
			fields = append(fields, key+"="+entries[0])
		}
	}
	hash, err := hex.DecodeString(values.Get("hash"))
	if err != nil || len(hash) != sha256.Size {
		return 0, ErrUnauthorized
	}
	sort.Strings(fields)
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	check := hmac.New(sha256.New, secret.Sum(nil))
	check.Write([]byte(strings.Join(fields, "\n")))
	if !hmac.Equal(hash, check.Sum(nil)) {
		return 0, ErrUnauthorized
	}
	seconds, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil || seconds <= 0 {
		return 0, ErrUnauthorized
	}
	// Launch data is fixed when Telegram opens the Mini App. One hour covers
	// importing the subscription in another app and returning for a key; the
	// data only travels in the Authorization header to this API.
	age := now.Sub(time.Unix(seconds, 0))
	if age < -30*time.Second || age > maxInitDataAge {
		return 0, ErrUnauthorized
	}
	var user struct {
		ID    int64 `json:"id"`
		IsBot bool  `json:"is_bot"`
	}
	if json.Unmarshal([]byte(values.Get("user")), &user) != nil || user.ID <= 0 || user.IsBot {
		return 0, ErrUnauthorized
	}
	return user.ID, nil
}

// Each accepted token belongs to a bot serving the same account store. The
// verified Telegram user ID stays the account identity across those bots.
func validateInitDataWithTokens(raw string, botTokens []string, now time.Time) (int64, error) {
	for _, token := range botTokens {
		if id, err := ValidateInitData(raw, token, now); err == nil {
			return id, nil
		}
	}
	return 0, ErrUnauthorized
}
