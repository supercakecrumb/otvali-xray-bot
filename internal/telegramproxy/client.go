package telegramproxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// API is deliberately limited to individual credentials; it cannot rotate,
// enable, delete, or replace a provider's configuration.
type API interface {
	GetUser(context.Context, string) (RemoteUser, error)
	CreateUser(context.Context, string, string) (RemoteUser, error)
	DisableUser(context.Context, string) (RemoteUser, error)
}

// RemoteUser contains control-plane state, never an API authentication token.
// InRuntime indicates presence, not enablement: a disabled user may remain in it.
type RemoteUser struct {
	Name, Secret, Revision string
	Enabled, InRuntime     bool
}

const (
	telemtVersion   = "3.5.14"
	requestTimeout  = 5 * time.Second
	maxResponseSize = 256 << 10
)

var remoteNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type Client struct {
	base          string
	authorization string
	http          *http.Client
}

// NewClient accepts only a literal loopback HTTP endpoint: the runtime provides
// the authenticated SSH tunnel. It never follows redirects or environment proxy
// settings. A supplied client may customize transport but not redirect behavior.
func NewClient(baseURL, authorization string, client *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrUnavailable
	}
	ip, err := netip.ParseAddr(u.Hostname())
	port, portErr := strconv.Atoi(u.Port())
	if err != nil || !ip.IsLoopback() || ip.Zone() != "" || portErr != nil || port < 1 || port > 65535 || strings.TrimSpace(authorization) == "" || len(authorization) > 4096 || strings.ContainsAny(authorization, "\r\n") {
		return nil, ErrUnavailable
	}
	hc := http.Client{}
	if client != nil {
		hc = *client
	}
	if hc.Transport == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		hc.Transport = transport
	} else if transport, ok := hc.Transport.(*http.Transport); ok {
		transport = transport.Clone()
		transport.Proxy = nil
		hc.Transport = transport
	}
	hc.Jar = nil
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if hc.Timeout <= 0 || hc.Timeout > requestTimeout {
		hc.Timeout = requestTimeout
	}
	return &Client{base: u.String(), authorization: authorization, http: &hc}, nil
}

type responseEnvelope struct {
	OK       *bool           `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Revision string          `json:"revision"`
	Error    *struct {
		Code string `json:"code"`
	} `json:"error"`
}

type wireUser struct {
	Username  string `json:"username"`
	Enabled   *bool  `json:"enabled"`
	InRuntime *bool  `json:"in_runtime"`
	Links     *struct {
		Classic []string `json:"classic"`
		Secure  []string `json:"secure"`
		TLS     []string `json:"tls"`
		Domains []struct {
			Domain string `json:"domain"`
			Link   string `json:"link"`
		} `json:"tls_domains"`
	} `json:"links"`
}

func (c *Client) GetUser(ctx context.Context, name string) (RemoteUser, error) {
	if !remoteNamePattern.MatchString(name) {
		return RemoteUser{}, ErrUnavailable
	}
	if _, err := c.versionRevision(ctx); err != nil {
		return RemoteUser{}, err
	}
	return c.getUser(ctx, name)
}

func (c *Client) getUser(ctx context.Context, name string) (RemoteUser, error) {
	env, err := c.request(ctx, http.MethodGet, "/v1/users/"+url.PathEscape(name), "", nil, http.StatusOK)
	if err != nil {
		return RemoteUser{}, err
	}
	return decodeUser(env.Data, env.Revision, name)
}

// CreateUser never changes an existing credential. An uncertain write is
// reconciled by its stable external name, without repeating the POST.
func (c *Client) CreateUser(ctx context.Context, name, secret string) (RemoteUser, error) {
	if !remoteNamePattern.MatchString(name) || !validHex(secret, 32) {
		return RemoteUser{}, ErrUnavailable
	}
	secret = strings.ToLower(secret)
	revision, err := c.versionRevision(ctx)
	if err != nil {
		return RemoteUser{}, err
	}
	user, err := c.getUser(ctx, name)
	if err == nil {
		return activeMatchingUser(user, secret)
	}
	if !errors.Is(err, ErrNotFound) {
		return RemoteUser{}, err
	}
	body, _ := json.Marshal(struct {
		Username string `json:"username"`
		Secret   string `json:"secret"`
		Enabled  bool   `json:"enabled"`
	}{name, secret, true})
	env, writeErr := c.request(ctx, http.MethodPost, "/v1/users", revision, body, http.StatusCreated, http.StatusAccepted)
	if writeErr == nil {
		var created struct {
			User   json.RawMessage `json:"user"`
			Secret string          `json:"secret"`
		}
		if json.Unmarshal(env.Data, &created) != nil || !sameSecret(created.Secret, secret) {
			return RemoteUser{}, ErrUnavailable
		}
		createdUser, decodeErr := decodeUser(created.User, env.Revision, name)
		if decodeErr != nil || !sameSecret(createdUser.Secret, secret) || !createdUser.Enabled {
			return RemoteUser{}, ErrUnavailable
		}
	} else if !errors.Is(writeErr, ErrUnavailable) && !errors.Is(writeErr, ErrConflict) {
		return RemoteUser{}, writeErr
	}
	// A canceled caller may still have committed remotely. This independent,
	// strictly bounded read prevents a successful write being blindly retried.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	user, err = c.getUser(readCtx, name)
	if err != nil {
		if writeErr != nil {
			return RemoteUser{}, writeErr
		}
		return RemoteUser{}, ErrUnavailable
	}
	return activeMatchingUser(user, secret)
}

// DisableUser relies on Telemt 3.5.14's synchronous admission fencing, which
// also cancels existing sessions before a successful disable response. We do
// not infer revocation from InRuntime, which only reports name presence.
func (c *Client) DisableUser(ctx context.Context, name string) (RemoteUser, error) {
	if !remoteNamePattern.MatchString(name) {
		return RemoteUser{}, ErrUnavailable
	}
	if _, err := c.versionRevision(ctx); err != nil {
		return RemoteUser{}, err
	}
	user, err := c.getUser(ctx, name)
	if err != nil {
		return RemoteUser{}, err
	}
	// GET reflects the persisted config, not the admission fence. Even an
	// already-disabled record needs a positive /disable acknowledgement.
	env, writeErr := c.request(ctx, http.MethodPost, "/v1/users/"+url.PathEscape(name)+"/disable", user.Revision, nil, http.StatusOK, http.StatusAccepted)
	if writeErr == nil {
		result, decodeErr := decodeUser(env.Data, env.Revision, name)
		if decodeErr != nil || result.Enabled || !sameSecret(result.Secret, user.Secret) {
			return RemoteUser{}, ErrUnavailable
		}
	} else if !errors.Is(writeErr, ErrUnavailable) && !errors.Is(writeErr, ErrConflict) {
		return RemoteUser{}, writeErr
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	result, err := c.getUser(readCtx, name)
	if writeErr != nil {
		// A disk-first read cannot prove the in-process fence was installed.
		// Preserve the caller's revoking state after an uncertain mutation.
		return RemoteUser{}, writeErr
	}
	if err != nil {
		return RemoteUser{}, ErrUnavailable
	}
	if result.Enabled || !sameSecret(result.Secret, user.Secret) {
		return RemoteUser{}, ErrUnavailable
	}
	return result, nil
}

func activeMatchingUser(user RemoteUser, secret string) (RemoteUser, error) {
	if !sameSecret(user.Secret, secret) {
		return RemoteUser{}, ErrConflict
	}
	if !user.Enabled {
		return RemoteUser{}, ErrInactive
	}
	if !user.InRuntime {
		return RemoteUser{}, ErrUnavailable
	}
	return user, nil
}

func (c *Client) versionRevision(ctx context.Context) (string, error) {
	env, err := c.request(ctx, http.MethodGet, "/v1/system/info", "", nil, http.StatusOK)
	if err != nil {
		return "", err
	}
	var info struct {
		Version    string `json:"version"`
		ConfigHash string `json:"config_hash"`
	}
	if json.Unmarshal(env.Data, &info) != nil || info.Version != telemtVersion || info.ConfigHash != env.Revision {
		return "", ErrUnavailable
	}
	return env.Revision, nil
}

func (c *Client) request(ctx context.Context, method, path, revision string, body []byte, statuses ...int) (responseEnvelope, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var empty responseEnvelope
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return empty, ErrUnavailable
	}
	req.Header.Set("Authorization", c.authorization)
	req.Header.Set("Accept", "application/json")
	if method != http.MethodGet {
		if !validHex(revision, 64) {
			return empty, ErrUnavailable
		}
		req.Header.Set("If-Match", revision)
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// net/http errors may contain URLs; they must never cross this boundary.
		return empty, ErrUnavailable
	}
	defer resp.Body.Close()
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return empty, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return empty, ErrUnavailable
	}
	var env responseEnvelope
	if json.Unmarshal(data, &env) != nil || env.OK == nil {
		return empty, ErrUnavailable
	}
	accepted := false
	for _, status := range statuses {
		accepted = accepted || resp.StatusCode == status
	}
	if !accepted {
		if *env.OK || env.Error == nil || env.Error.Code == "" {
			return empty, ErrUnavailable
		}
		if resp.StatusCode == http.StatusNotFound && env.Error.Code == "not_found" {
			return empty, ErrNotFound
		}
		if resp.StatusCode == http.StatusConflict && (env.Error.Code == "revision_conflict" || env.Error.Code == "user_exists") {
			return empty, ErrConflict
		}
		return empty, ErrUnavailable
	}
	if !*env.OK || env.Error != nil || !validHex(env.Revision, 64) || len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) {
		return empty, ErrUnavailable
	}
	return env, nil
}

func decodeUser(data json.RawMessage, revision, expectedName string) (RemoteUser, error) {
	var wire wireUser
	if json.Unmarshal(data, &wire) != nil || wire.Username != expectedName || wire.Enabled == nil || wire.InRuntime == nil || wire.Links == nil {
		return RemoteUser{}, ErrUnavailable
	}
	secret := ""
	addLink := func(raw, mode string) bool {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "tg" || u.Host != "proxy" || u.Path != "" || u.User != nil || u.Fragment != "" {
			return false
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(q["secret"]) != 1 || len(q["server"]) != 1 || len(q["port"]) != 1 || q.Get("server") == "" {
			return false
		}
		port, err := strconv.Atoi(q.Get("port"))
		if err != nil || port < 1 || port > 65535 {
			return false
		}
		s := strings.ToLower(q.Get("secret"))
		switch mode {
		case "classic":
			if !validHex(s, 32) {
				return false
			}
		case "secure":
			if len(s) != 34 || !strings.HasPrefix(s, "dd") || !validHex(s, 34) {
				return false
			}
			s = s[2:]
		case "tls":
			if len(s) <= 34 || len(s) > 544 || !strings.HasPrefix(s, "ee") || !validHex(s, len(s)) {
				return false
			}
			s = s[2:34]
		default:
			return false
		}
		if secret != "" && !sameSecret(secret, s) {
			return false
		}
		secret = s
		return true
	}
	for _, link := range wire.Links.Classic {
		if !addLink(link, "classic") {
			return RemoteUser{}, ErrUnavailable
		}
	}
	for _, link := range wire.Links.Secure {
		if !addLink(link, "secure") {
			return RemoteUser{}, ErrUnavailable
		}
	}
	for _, link := range wire.Links.TLS {
		if !addLink(link, "tls") {
			return RemoteUser{}, ErrUnavailable
		}
	}
	for _, link := range wire.Links.Domains {
		if !addLink(link.Link, "tls") {
			return RemoteUser{}, ErrUnavailable
		}
	}
	if secret == "" {
		return RemoteUser{}, ErrUnavailable
	}
	return RemoteUser{Name: wire.Username, Secret: secret, Revision: revision, Enabled: *wire.Enabled, InRuntime: *wire.InRuntime}, nil
}

func validHex(value string, length int) bool {
	if len(value) != length || length%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameSecret(a, b string) bool {
	return validHex(a, 32) && validHex(b, 32) && subtle.ConstantTimeCompare([]byte(strings.ToLower(a)), []byte(strings.ToLower(b))) == 1
}
