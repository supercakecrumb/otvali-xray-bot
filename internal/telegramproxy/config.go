package telegramproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var endpointIDs = map[string]bool{"finland": true, "stockholm": true, "msk-finland": true, "msk-stockholm": true}
var safeID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var externalIDPattern = regexp.MustCompile(`^otv_[0-9a-f]{32}$`)
var hexSecret = regexp.MustCompile(`^[0-9a-f]{32}$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validDNS(host string) bool {
	if len(host) < 1 || len(host) > 253 || host != strings.ToLower(host) || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func validPublicHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == "" && !addr.Is4In6() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast()
	}
	return validDNS(host) && host != "localhost.localdomain" && !strings.HasSuffix(host, ".localhost") && !strings.HasSuffix(host, ".local") && !strings.HasSuffix(host, ".internal")
}

// ValidateConfig allows exactly the four approved routes and two explicit
// control planes. The control API is reachable only through literal loopback
// SSH forwards, never a user-supplied host or a public management endpoint.
func ValidateConfig(c Config) error {
	if !c.Enabled {
		return nil
	}
	if len(c.SecretKey) != 32 || len(c.Endpoints) != 4 || len(c.Providers) != 2 || (!c.AllowAllBound && len(c.AllowedTelegramIDs) == 0) {
		return ErrUnavailable
	}
	for id := range c.AllowedTelegramIDs {
		if id <= 0 {
			return ErrUnavailable
		}
	}
	providers := map[string]bool{}
	for _, p := range c.Providers {
		u, err := url.Parse(p.BaseURL)
		if err != nil || !safeID.MatchString(p.ID) || providers[p.ID] || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
			return ErrUnavailable
		}
		addr, err := netip.ParseAddr(u.Hostname())
		port, portErr := strconv.Atoi(u.Port())
		if err != nil || !addr.IsLoopback() || addr.Zone() != "" || portErr != nil || port < 1 || port > 65535 || len(p.Authorization) < 16 || len(p.Authorization) > 512 || strings.IndexFunc(p.Authorization, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return ErrUnavailable
		}
		providers[p.ID] = true
	}
	seen, used, primary := map[string]bool{}, map[string]int{}, 0
	for _, ep := range c.Endpoints {
		if !endpointIDs[ep.ID] || seen[ep.ID] || !providers[ep.ProviderID] || !validPublicHost(ep.PublicHost) || ep.PublicPort < 1 || ep.PublicPort > 65535 || !validDNS(ep.TLSDomain) || len([]rune(ep.Name)) < 1 || len([]rune(ep.Name)) > 80 || strings.ContainsAny(ep.Name, "\r\n\x00") {
			return ErrUnavailable
		}
		seen[ep.ID] = true
		used[ep.ProviderID]++
		if ep.Primary {
			primary++
		}
	}
	if primary != 1 || len(used) != 2 {
		return ErrUnavailable
	}
	for _, count := range used {
		if count != 2 {
			return ErrUnavailable
		}
	}
	pairs := map[string]string{}
	for _, ep := range c.Endpoints {
		base := strings.TrimPrefix(ep.ID, "msk-")
		if prior, ok := pairs[base]; ok && prior != ep.ProviderID {
			return ErrUnavailable
		}
		pairs[base] = ep.ProviderID
	}
	if pairs["finland"] == pairs["stockholm"] {
		return ErrUnavailable
	}
	return nil
}

func bindingHash(ep EndpointConfig) string {
	// Presentation-only changes do not silently invalidate credentials. API
	// forward ports may change without changing the remote credential identity.
	b, _ := json.Marshal([]any{ep.ID, ep.ProviderID, ep.PublicHost, ep.PublicPort, ep.TLSDomain})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func credentialURL(ep EndpointConfig, secret string) (string, error) {
	if !hexSecret.MatchString(secret) {
		return "", ErrUnavailable
	}
	u := url.URL{Scheme: "https", Host: "t.me", Path: "/proxy"}
	q := url.Values{"server": {ep.PublicHost}, "port": {strconv.Itoa(ep.PublicPort)}, "secret": {"ee" + secret + hex.EncodeToString([]byte(ep.TLSDomain))}}
	u.RawQuery = q.Encode()
	if !ValidCredentialURL(u.String()) {
		return "", ErrUnavailable
	}
	return u.String(), nil
}

// ValidCredentialURL is a defense-in-depth check for frontends. It accepts only
// native Telegram FakeTLS links; catalog/config binding is enforced by Service.
func ValidCredentialURL(raw string) bool {
	if len(raw) == 0 || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\x00") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "t.me" || u.Path != "/proxy" || u.RawPath != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 3 || len(q["server"]) != 1 || len(q["port"]) != 1 || len(q["secret"]) != 1 || !validPublicHost(q.Get("server")) {
		return false
	}
	port, err := strconv.Atoi(q.Get("port"))
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != q.Get("port") {
		return false
	}
	secret := q.Get("secret")
	if len(secret) < 36 || !strings.HasPrefix(secret, "ee") || !hexSecret.MatchString(secret[2:34]) {
		return false
	}
	domain, err := hex.DecodeString(secret[34:])
	if err != nil || !validDNS(string(domain)) || net.ParseIP(string(domain)) != nil {
		return false
	}
	return true
}
