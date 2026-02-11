package x3ui

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	x3client "github.com/supercakecrumb/go-x3ui/client" // Replace with actual import path
)

// InitializeX3uiClient creates a new x3ui client connected via the SSH tunnel
func InitializeX3uiClient(localPort int, username, password string, logger *slog.Logger) (*x3client.Client, error) {
	scheme := strings.ToLower(strings.TrimSpace(os.Getenv("X3UI_SCHEME")))
	schemes := []string{"https", "http"}
	if scheme != "" {
		schemes = []string{scheme}
	}

	var lastErr error
	for _, currentScheme := range schemes {
		baseURL := fmt.Sprintf("%s://localhost:%d", currentScheme, localPort)
		insecure := currentScheme == "https"
		client := x3client.NewClient(baseURL, username, password, insecure, logger)

		// Validate the scheme early to avoid HTTPS->HTTP mismatch at first use.
		if _, err := client.ListInbounds(); err != nil {
			lastErr = err
			if currentScheme == "https" && isHTTPResponseToHTTPS(err) && scheme == "" {
				logger.Warn("X3UI HTTPS failed, retrying over HTTP", slog.Int("local_port", localPort))
				continue
			}
			return nil, err
		}

		return client, nil
	}

	return nil, lastErr
}

func isHTTPResponseToHTTPS(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "server gave HTTP response to HTTPS client")
}
