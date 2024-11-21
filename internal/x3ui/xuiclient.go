package x3ui

import (
	"fmt"
	"log/slog"

	x3client "github.com/supercakecrumb/go-x3ui/client" // Replace with actual import path
)

// InitializeX3uiClient creates a new x3ui client connected via the SSH tunnel
func InitializeX3uiClient(localPort int, username, password string, logger *slog.Logger) (*x3client.Client, error) {
	baseURL := fmt.Sprintf("https://localhost:%d", localPort)
	insecure := true // Set to true if you need to ignore TLS verification; adjust as needed

	client := x3client.NewClient(baseURL, username, password, insecure, logger)
	return client, nil
}
