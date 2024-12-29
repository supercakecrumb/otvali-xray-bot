package client

import (
	"encoding/json"
	"fmt"
	"strconv"
)

type APIResponse[T any] struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     T      `json:"obj"`
}

// Inbound represents a single inbound entry.
type Inbound struct {
	ID             int           `json:"id"`
	Up             int64         `json:"up"`
	Down           int64         `json:"down"`
	Total          int64         `json:"total"`
	Remark         string        `json:"remark"`
	Enable         bool          `json:"enable"`
	ExpiryTime     int64         `json:"expiryTime"`
	ClientStats    []ClientStats `json:"clientStats"`
	Listen         string        `json:"listen"`
	Port           int           `json:"port"`
	Protocol       string        `json:"protocol"`
	Settings       string        `json:"settings"`
	StreamSettings string        `json:"streamSettings"`
	Tag            string        `json:"tag"`
	Sniffing       string        `json:"sniffing"`
	Allocate       string        `json:"allocate"`
}

// InboundSettings represents the parsed settings for an inbound
type InboundSettings struct {
	Clients    []InboundClient `json:"clients"`
	Decryption string          `json:"decryption"`
	Fallbacks  []string        `json:"fallbacks"`
}

// ClientStats represents individual client statistics within an inbound.
type ClientStats struct {
	ID         int    `json:"id"`
	InboundID  int    `json:"inboundId"`
	Enable     bool   `json:"enable"`
	Email      string `json:"email"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	ExpiryTime int64  `json:"expiryTime"`
	Total      int64  `json:"total"`
	Reset      int64  `json:"reset"`
}

// OnlinesResponse represents the `inbound/onlines` response object.
type OnlinesResponse []string

// AddInboundClientPayload represents the payload for the addClient API
type AddInboundClientPayload struct {
	ID       int                    `json:"id"`
	Settings AddInboundClientConfig `json:"settings"`
}

// AddInboundClientConfig represents the settings field in the addClient payload
type AddInboundClientConfig struct {
	Clients []InboundClient `json:"clients"`
}

// InboundClient represents a single client to be added to an inbound
type InboundClient struct {
	ID         string        `json:"id"`
	Flow       string        `json:"flow"`
	Email      string        `json:"email"`
	LimitIP    int           `json:"limitIp"`
	TotalGB    int           `json:"totalGB"`
	ExpiryTime int64         `json:"expiryTime"`
	Enable     bool          `json:"enable"`
	TgID       FlexibleInt64 `json:"tgId"`
	SubID      string        `json:"subId"`
	Reset      int           `json:"reset"`
}

// AddInboundPayload represents the payload for the Add Inbound API
type AddInboundPayload struct {
	Up             int64  `json:"up"`
	Down           int64  `json:"down"`
	Total          int64  `json:"total"`
	Remark         string `json:"remark"`
	Enable         bool   `json:"enable"`
	ExpiryTime     int64  `json:"expiryTime"`
	Listen         string `json:"listen"`
	Port           int    `json:"port"`
	Protocol       string `json:"protocol"`
	Settings       string `json:"settings"`       // JSON string
	StreamSettings string `json:"streamSettings"` // JSON string
	Sniffing       string `json:"sniffing"`       // JSON string
	Allocate       string `json:"allocate"`       // JSON string
}

// AddInboundSettings represents the parsed settings for an inbound
type AddInboundSettings struct {
	Clients    []Client `json:"clients"`
	Decryption string   `json:"decryption"`
	Fallbacks  []string `json:"fallbacks"`
}

// AddInboundStreamSettings represents the stream settings for an inbound
type AddInboundStreamSettings struct {
	Network         string                    `json:"network"`
	Security        string                    `json:"security"`
	ExternalProxy   []string                  `json:"externalProxy"`
	RealitySettings AddInboundRealitySettings `json:"realitySettings"`
	TCPSettings     AddInboundTCPSettings     `json:"tcpSettings"`
}

// AddInboundRealitySettings represents reality-specific stream settings
type AddInboundRealitySettings struct {
	Show        bool     `json:"show"`
	Xver        int      `json:"xver"`
	Dest        string   `json:"dest"`
	ServerNames []string `json:"serverNames"`
	PrivateKey  string   `json:"privateKey"`
	ShortIDs    []string `json:"shortIds"`
	Settings    struct {
		PublicKey   string `json:"publicKey"`
		Fingerprint string `json:"fingerprint"`
		ServerName  string `json:"serverName"`
		SpiderX     string `json:"spiderX"`
	} `json:"settings"`
}

// AddInboundTCPSettings represents TCP-specific settings
type AddInboundTCPSettings struct {
	AcceptProxyProtocol bool `json:"acceptProxyProtocol"`
	Header              struct {
		Type string `json:"type"`
	} `json:"header"`
}

// CertificateResponse represents the response from /server/getNewX25519Cert
type CertificateResponse struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

// FetchCertificateResponse wraps the API response
type FetchCertificateResponse struct {
	Success bool                `json:"success"`
	Msg     string              `json:"msg"`
	Obj     CertificateResponse `json:"obj"`
}

// FlexibleInt64 handles fields that could be either a string or an int64
// It's not a great way, but this is what chatGPT created for me. It's 5 in the morning, I'm sorry.
type FlexibleInt64 struct {
	Value *int64
}

// UnmarshalJSON custom unmarshaler for FlexibleInt64
func (f *FlexibleInt64) UnmarshalJSON(data []byte) error {
	var num int64
	if err := json.Unmarshal(data, &num); err == nil {
		f.Value = &num
		return nil
	}

	var str string
	if err := json.Unmarshal(data, &str); err == nil && str != "" {
		parsedNum, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid tgId format: %s", str)
		}
		f.Value = &parsedNum
		return nil
	}

	// If empty string or invalid value, set to nil
	f.Value = nil
	return nil
}
