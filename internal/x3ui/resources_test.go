package x3ui

import (
	"net/url"
	"testing"
)

func TestReplaceVLESSHost(t *testing.T) {
	// Link exactly as GenerateVLESSLink produces it when inbound.Listen == "":
	// the authority has an empty host (vless://<uuid>@:443?...#remark).
	const uuid = "11111111-2222-3333-4444-555555555555"
	rawQuery := "flow=xtls-rprx-vision&fp=chrome&pbk=publickey123&security=reality&sid=0a1b&sni=example.com&spx=%2F&type=tcp"
	fragment := "otvali-inbound-user%40example.com"
	in := "vless://" + uuid + "@:443?" + rawQuery + "#" + fragment

	tests := []struct {
		name     string
		host     string
		port     int
		wantHost string // url.URL.Hostname()
		wantPort string // url.URL.Port()
	}{
		{"domain", "fi.otvali.aurorass.art", 443, "fi.otvali.aurorass.art", "443"},
		{"ipv4", "203.0.113.7", 8443, "203.0.113.7", "8443"},
		{"ipv6", "2001:db8::1", 443, "2001:db8::1", "443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceVLESSHost(in, tt.host, tt.port)
			if err != nil {
				t.Fatalf("replaceVLESSHost returned error: %v", err)
			}

			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("result is not a valid URL %q: %v", got, err)
			}

			if u.Hostname() != tt.wantHost {
				t.Errorf("host = %q, want %q (link=%q)", u.Hostname(), tt.wantHost, got)
			}
			if u.Port() != tt.wantPort {
				t.Errorf("port = %q, want %q (link=%q)", u.Port(), tt.wantPort, got)
			}

			// The client UUID (user info) must survive untouched.
			if u.User == nil || u.User.String() != uuid {
				t.Errorf("uuid = %v, want %q", u.User, uuid)
			}
			// Query parameters must be preserved verbatim.
			if u.RawQuery != rawQuery {
				t.Errorf("query = %q, want %q", u.RawQuery, rawQuery)
			}
			// Remark fragment must be preserved verbatim.
			if u.EscapedFragment() != fragment {
				t.Errorf("fragment = %q, want %q", u.EscapedFragment(), fragment)
			}
			if u.Scheme != "vless" {
				t.Errorf("scheme = %q, want %q", u.Scheme, "vless")
			}
		})
	}
}

func TestReplaceVLESSHostInvalidLink(t *testing.T) {
	if _, err := replaceVLESSHost("://not a url", "host", 443); err == nil {
		t.Fatal("expected an error for an unparseable link, got nil")
	}
}
