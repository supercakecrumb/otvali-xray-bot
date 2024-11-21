package client

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/exp/rand"
)

// ListInbounds fetches the list of inbounds.
func (c *Client) ListInbounds() ([]Inbound, error) {
	c.Logger.Info("Fetching inbound list")
	var response APIResponse[[]Inbound]

	// Send the request
	_, err := c.Resty.R().
		SetHeader("Accept", "application/json").
		SetResult(&response).
		Post("/panel/inbound/list")

	if err != nil {
		c.Logger.Error("Failed to fetch inbound list", "error", err)
		return nil, err
	}

	// Handle non-success responses
	if !response.Success {
		c.Logger.Error("Failed to fetch inbounds", "message", response.Msg)
		return nil, fmt.Errorf("failed to fetch inbounds: %s", response.Msg)
	}

	c.Logger.Info("Successfully fetched inbound list")
	return response.Obj, nil
}

func (c *Client) AddInbound(payload AddInboundPayload) (*Inbound, error) {
	c.Logger.Info("Adding inbound", "remark", payload.Remark, "port", payload.Port)

	// Send the request
	resp, err := c.Resty.R().
		SetHeader("Content-Type", "application/json").
		SetBody(payload).
		Post("/panel/inbound/add")

	if err != nil {
		c.Logger.Error("Failed to add inbound", "error", err)
		return nil, fmt.Errorf("failed to add inbound: %w", err)
	}

	// Unmarshal the response
	var response APIResponse[Inbound]
	if err := json.Unmarshal(resp.Body(), &response); err != nil {
		c.Logger.Error("Failed to unmarshal response", "error", err, "body", string(resp.Body()))
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Handle non-success responses
	if !response.Success {
		c.Logger.Error("Add inbound failed", "message", response.Msg)
		return nil, fmt.Errorf("add inbound failed: %s", response.Msg)
	}

	c.Logger.Debug("Inboud Added", slog.Any("Inbound", response.Obj))

	inbound := &response.Obj
	c.Logger.Info("Inbound successfully added", "id", inbound.ID, "remark", inbound.Remark, "port", inbound.Port)
	return inbound, nil
}

func (c *Client) GenerateDefaultInboundConfig(remark, realityCover, listenIP string, port int) (AddInboundPayload, error) {
	// Generate keys
	// Fetch new certificate
	cert, err := c.GetNewCertificate()
	if err != nil {
		return AddInboundPayload{}, fmt.Errorf("failed to fetch certificate: %w", err)
	}

	// Create default configuration
	settings := AddInboundSettings{
		Clients:    []Client{},
		Decryption: "none",
		Fallbacks:  []string{},
	}

	streamSettings := AddInboundStreamSettings{
		Network:       "tcp",
		Security:      "reality",
		ExternalProxy: []string{},
		RealitySettings: AddInboundRealitySettings{
			Show:        false,
			Xver:        0,
			Dest:        fmt.Sprintf("%s:443", realityCover),
			ServerNames: []string{realityCover, fmt.Sprintf("www.%s", realityCover)},
			PrivateKey:  cert.PrivateKey, // Use generated private key
			ShortIDs:    GenerateShortIDs(),
			Settings: struct {
				PublicKey   string `json:"publicKey"`
				Fingerprint string `json:"fingerprint"`
				ServerName  string `json:"serverName"`
				SpiderX     string `json:"spiderX"`
			}{
				PublicKey:   cert.PublicKey, // Use generated public key
				Fingerprint: "chrome",
				ServerName:  "",
				SpiderX:     "/",
			},
		},
		TCPSettings: AddInboundTCPSettings{
			AcceptProxyProtocol: false,
			Header: struct {
				Type string `json:"type"`
			}{
				Type: "none",
			},
		},
	}

	sniffing := map[string]interface{}{
		"enabled":      true,
		"destOverride": []string{"http", "tls", "quic", "fakedns"},
		"metadataOnly": false,
		"routeOnly":    false,
	}

	allocate := map[string]interface{}{
		"strategy":    "always",
		"refresh":     5,
		"concurrency": 3,
	}

	// Convert settings to JSON strings
	settingsJSON, _ := json.Marshal(settings)
	streamSettingsJSON, _ := json.Marshal(streamSettings)
	sniffingJSON, _ := json.Marshal(sniffing)
	allocateJSON, _ := json.Marshal(allocate)

	// Create the payload
	return AddInboundPayload{
		Up:             0,
		Down:           0,
		Total:          0,
		Remark:         remark,
		Enable:         true,
		ExpiryTime:     0,
		Listen:         listenIP,
		Port:           port,
		Protocol:       "vless",
		Settings:       string(settingsJSON),
		StreamSettings: string(streamSettingsJSON),
		Sniffing:       string(sniffingJSON),
		Allocate:       string(allocateJSON),
	}, nil
}

// GetNewCertificate fetches a new X25519 certificate from the server
func (c *Client) GetNewCertificate() (CertificateResponse, error) {
	c.Logger.Info("Requesting new X25519 certificate")

	// Send the request
	resp, err := c.Resty.R().
		SetHeader("Accept", "application/json").
		Post("/server/getNewX25519Cert")

	if err != nil {
		c.Logger.Error("Failed to fetch certificate", "error", err)
		return CertificateResponse{}, fmt.Errorf("failed to fetch certificate: %w", err)
	}

	// Parse the response
	var certResp FetchCertificateResponse
	if err := json.Unmarshal(resp.Body(), &certResp); err != nil {
		c.Logger.Error("Failed to parse certificate response", "error", err, "body", string(resp.Body()))
		return CertificateResponse{}, fmt.Errorf("failed to parse certificate response: %w", err)
	}

	// Check for success
	if !certResp.Success {
		c.Logger.Error("Failed to fetch certificate", "message", certResp.Msg)
		return CertificateResponse{}, fmt.Errorf("certificate fetch failed: %s", certResp.Msg)
	}

	c.Logger.Info("Certificate fetched successfully", "publicKey", certResp.Obj.PublicKey)
	return certResp.Obj, nil
}

// GenerateShortIDs generates short IDs based on a predefined sequence and lengths
func GenerateShortIDs() []string {
	seq := "0123456789abcdef"                    // Hexadecimal sequence
	lengths := []int{2, 4, 6, 8, 10, 12, 14, 16} // Fixed lengths for IDs

	// Seed the random generator
	rand.Seed(uint64(time.Now().UnixNano()))

	var shortIds []string
	for _, length := range lengths {
		shortId := ""
		for i := 0; i < length; i++ {
			shortId += string(seq[rand.Intn(len(seq))]) // Random character from seq
		}
		shortIds = append(shortIds, shortId)
	}

	return shortIds
}
