package client

import (
	"fmt"
	"log/slog"
	"net/url"
)

func (c *Client) Login() error {
	c.Logger.Info("Sending login request", slog.String("username", c.Username))

	// Create form-encoded payload
	formData := url.Values{}
	formData.Set("username", c.Username)
	formData.Set("password", c.Password)

	// Send the request
	resp, err := c.Resty.R().
		SetHeader("Content-Type", "application/x-www-form-urlencoded").
		SetBody(formData.Encode()). // Properly encode form data
		SetHeader("Accept", "*/*").
		SetHeader("Connection", "keep-alive").
		Post("/login")

	if err != nil {
		c.Logger.Error("Login request failed", slog.String("error", err.Error()))
		return err
	}

	// Log the response
	c.Logger.Info("Login response received",
		slog.Int("status", resp.StatusCode()),
		slog.String("response", resp.String()),
	)

	// Check for successful login
	if resp.StatusCode() != 200 {
		return fmt.Errorf("login failed: %s", resp.String())
	}

	c.Logger.Info("Login successful!")
	return nil
}
