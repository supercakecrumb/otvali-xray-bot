package remnawave

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ServerKey is one current profile from the same account as the subscription.
// ID is an opaque callback identifier, never the credential itself.
type ServerKey struct {
	ID   string
	Name string
	URL  string
}

type subscriptionReader interface {
	ReadSubscription(context.Context, string) ([]ServerKey, error)
}

// Keys exports the current profiles of the subscription account. Like Issue it
// is an explicit issuance action: the first call creates the account and its
// immutable grant. Later calls never change the account, expiry, revocations
// or mapping, and never create a second account.
func (s *Service) Keys(ctx context.Context, telegramID int64) ([]ServerKey, error) {
	if !s.Eligible(ctx, telegramID) {
		return nil, ErrNotEligible
	}
	reader, ok := s.api.(subscriptionReader)
	if !ok {
		return nil, ErrUnavailable
	}
	subscription, err := s.Issue(ctx, telegramID)
	if err != nil {
		return nil, err
	}
	return reader.ReadSubscription(ctx, subscription.URL)
}

// ReadSubscription sends no panel API credential. The only accepted origin is
// the configured public subscription origin; redirects and oversized responses
// are rejected. Transport errors can contain bearer URLs and must stay private.
func (c *Client) ReadSubscription(ctx context.Context, raw string) ([]ServerKey, error) {
	u, err := url.Parse(raw)
	origin, originErr := url.Parse(c.subscriptionOrigin)
	if err != nil || originErr != nil || origin == nil || origin.Host == "" ||
		u.Scheme != "https" || origin.Scheme != "https" || !strings.EqualFold(u.Host, origin.Host) ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!strings.HasPrefix(u.Path, "/api/sub/") || len(u.Path) <= len("/api/sub/") {
		return nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("User-Agent", "OtvaliBot/1.0")
	req.Header.Set("Accept", "text/plain")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	const maxBody = 256 * 1024
	if response.StatusCode != http.StatusOK || response.ContentLength > maxBody {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return nil, ErrUnavailable
	}
	return parseServerKeys(body)
}

func parseServerKeys(body []byte) ([]ServerKey, error) {
	text := strings.TrimSpace(string(body))
	if !strings.HasPrefix(text, "vless://") {
		encoded := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, text)
		var decoded []byte
		var err error
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			decoded, err = encoding.DecodeString(encoded)
			if err == nil {
				break
			}
		}
		if err != nil {
			return nil, ErrUnavailable
		}
		text = strings.TrimSpace(string(decoded))
	}
	keys := make([]ServerKey, 0, 3)
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "vless" || u.Opaque != "" || u.Hostname() == "" || u.User == nil || len(line) > 4096 || strings.ContainsAny(line, "\r\n\x00") {
			return nil, ErrUnavailable
		}
		if _, password := u.User.Password(); password {
			return nil, ErrUnavailable
		}
		id, err := uuid.Parse(u.User.Username())
		port, portErr := strconv.Atoi(u.Port())
		if err != nil || id == uuid.Nil || portErr != nil || port < 1 || port > 65535 {
			return nil, ErrUnavailable
		}
		name := strings.TrimSpace(u.Fragment)
		if name == "" {
			name = u.Host
		}
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return nil, ErrUnavailable
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		digest := sha256.Sum256([]byte(line))
		keys = append(keys, ServerKey{ID: hex.EncodeToString(digest[:8]), Name: name, URL: line})
		if len(keys) > 32 {
			return nil, ErrUnavailable
		}
	}
	if len(keys) == 0 {
		return nil, ErrUnavailable
	}
	return keys, nil
}
