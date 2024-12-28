package client

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/go-resty/resty/v2"
)

func (c *Client) Login() error {
	// Use a context with a value to indicate that we are logging in
	ctx := context.WithValue(context.Background(), "skipSessionCheck", true)

	c.Logger.Info("Sending login request", slog.String("username", c.Username))

	// Create form-encoded payload
	formData := url.Values{}
	formData.Set("username", c.Username)
	formData.Set("password", c.Password)

	// Send the request with the context
	resp, err := c.Resty.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/x-www-form-urlencoded").
		SetBody(formData.Encode()).
		Post("/login")

	if err != nil {
		c.Logger.Error("Login request failed", slog.String("error", err.Error()))
		return err
	}

	if resp.StatusCode() != 200 {
		return fmt.Errorf("login failed: %s", resp.String())
	}

	// Set the last login time
	c.LastLoginTime = time.Now()

	c.Logger.Info("Login successful!")
	return nil
}

func (c *Client) checkAndRefreshSession(_ *resty.Client, req *resty.Request) error {
	// Check if we should skip the session check
	if req.Context() != nil {
		if skip, ok := req.Context().Value("skipSessionCheck").(bool); ok && skip {
			// Skip checking session during Login()
			return nil
		}
	}

	c.loginMutex.Lock()
	defer c.loginMutex.Unlock()

	// Proceed with the session check
	c.Logger.Info("Checking session expiration")

	if c.LastLoginTime.IsZero() {
		c.Logger.Warn("LastLoginTime is zero, need to login")
		return c.Login()
	}

	timeSinceLogin := time.Since(c.LastLoginTime)
	sessionDuration := time.Hour
	timeRemaining := sessionDuration - timeSinceLogin

	if timeRemaining <= 0 {
		c.Logger.Info("Session has expired, re-logging in")
		return c.Login()
	}

	if timeRemaining <= sessionExpiryDelta {
		c.Logger.Info("Session is about to expire, re-logging in")
		return c.Login()
	}

	c.Logger.Info("Session is valid", "expires_in", timeRemaining)
	return nil
}
