package client

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// GenerateVLESSLink generates a VLESS link for the given inbound and email
// It was rewritten from 3xui JS script https://github.com/MHSanaei/3x-ui/blob/2ce9c3cc81799b954441e665e9661a20bc69f8c3/web/assets/js/model/inbound.js#L1364
func GenerateVLESSLink(inbound Inbound, email string) (string, error) {
	// Parse the settings field to extract the list of clients
	inboundSettings, err := parseInboundSettings(inbound)
	if err != nil {
		return "", fmt.Errorf("failed to parse inbound settings: %w", err)
	}

	// Find the client by email
	var client InboundClient
	found := false
	for _, c := range inboundSettings.Clients {
		if c.Email == email {
			client = c
			found = true
			break
		}
	}

	if !found {
		return "", fmt.Errorf("client with email %s not found in inbound", email)
	}

	// Parse the StreamSettings field to extract network and security details
	var streamSettings AddInboundStreamSettings
	if err := json.Unmarshal([]byte(inbound.StreamSettings), &streamSettings); err != nil {
		return "", fmt.Errorf("failed to parse StreamSettings: %w", err)
	}

	// Construct the base VLESS link
	link := fmt.Sprintf("vless://%s@%s:%d", client.ID, inbound.Listen, inbound.Port)

	// Add query parameters
	params := url.Values{}
	params.Set("type", streamSettings.Network)
	params.Set("security", streamSettings.Security)

	// Add Reality-specific parameters if applicable
	if streamSettings.Security == "reality" {
		reality := streamSettings.RealitySettings
		params.Set("pbk", reality.Settings.PublicKey)
		params.Set("fp", reality.Settings.Fingerprint)
		if len(reality.ServerNames) > 0 {
			params.Set("sni", reality.ServerNames[0])
		}
		if len(reality.ShortIDs) > 0 {
			params.Set("sid", reality.ShortIDs[0])
		}
		params.Set("spx", reality.Settings.SpiderX)
	}

	// Add flow from the client's flow setting if available
	if client.Flow != "" {
		params.Set("flow", client.Flow)
	}

	// Format the remark to include the email as a descriptive identifier
	remark := fmt.Sprintf("%s-%s", inbound.Remark, client.Email)

	// Finalize the VLESS link with query parameters and the remark
	vlessURL, _ := url.Parse(link)
	vlessURL.RawQuery = params.Encode()
	vlessURL.Fragment = url.QueryEscape(remark)

	return vlessURL.String(), nil
}

func parseInboundSettings(inbound Inbound) (InboundSettings, error) {
	var settings InboundSettings

	err := json.Unmarshal([]byte(inbound.Settings), &settings)
	if err != nil {
		return settings, fmt.Errorf("failed to parse settings: %w", err)
	}

	return settings, nil
}
