package remnawave

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	ErrNotFound    = errors.New("Remnawave account not found")
	ErrUnavailable = errors.New("Remnawave temporarily unavailable")
	ErrNotEligible = errors.New("Stockholm access is not enabled for this account")
	ErrInactive    = errors.New("Stockholm access is inactive")
	ErrConflict    = errors.New("Stockholm account requires administrator review")
)

type User struct {
	ID                   int64     `json:"id"`
	Username             string    `json:"username"`
	Status               string    `json:"status"`
	TelegramID           *int64    `json:"telegramId"`
	Tag                  string    `json:"tag"`
	ExpireAt             time.Time `json:"expireAt"`
	TrafficLimitBytes    int64     `json:"trafficLimitBytes"`
	SubscriptionURL      string    `json:"subscriptionUrl"`
	ActiveInternalSquads []Squad   `json:"activeInternalSquads"`
}

type Squad struct {
	UUID string `json:"uuid"`
}

type CreateUserRequest struct {
	Username             string    `json:"username"`
	TelegramID           int64     `json:"telegramId"`
	Tag                  string    `json:"tag"`
	Status               string    `json:"status"`
	ExpireAt             time.Time `json:"expireAt"`
	TrafficLimitBytes    int64     `json:"trafficLimitBytes"`
	TrafficLimitStrategy string    `json:"trafficLimitStrategy"`
	HWIDDeviceLimit      int       `json:"hwidDeviceLimit"`
	ActiveInternalSquads []string  `json:"activeInternalSquads"`
}

type API interface {
	FindUser(context.Context, string) (User, error)
	CreateUser(context.Context, CreateUserRequest) (User, error)
}

type Client struct {
	origin             string
	subscriptionOrigin string
	token              string
	http               *http.Client
}

func NewClient(c Config) (*Client, error) {
	if !c.Enabled || !validOrigin(c.BaseURL) || c.APIToken == "" {
		return nil, ErrNotEligible
	}
	return &Client{origin: c.BaseURL, subscriptionOrigin: c.SubscriptionOrigin, token: c.APIToken, http: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}, nil
}

func (c *Client) FindUser(ctx context.Context, username string) (User, error) {
	return c.request(ctx, http.MethodGet, "/users/by-username/"+url.PathEscape(username), nil)
}

func (c *Client) CreateUser(ctx context.Context, body CreateUserRequest) (User, error) {
	return c.request(ctx, http.MethodPost, "/users", body)
}

func (c *Client) request(ctx context.Context, method, path string, body any) (User, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return User{}, ErrUnavailable
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+"/api"+path, bytes.NewReader(encoded))
	if err != nil {
		return User{}, ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "otvali-xray-bot/remnawave")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return User{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && method == http.MethodGet {
		return User{}, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return User{}, ErrUnavailable
	}
	var result struct {
		Response User `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return User{}, ErrUnavailable
	}
	return result.Response, nil
}
