package miniapp

import (
	"context"
	"errors"
	"net/http"

	"github.com/supercakecrumb/otvali-xray-bot/internal/telegramproxy"
)

// TelegramProxyBackend is optional so existing account/VPN backends can run
// with Telegram proxy issuance disabled.
type TelegramProxyBackend interface {
	TelegramProxies(context.Context, int64) ([]telegramproxy.Endpoint, error)
	TelegramProxy(context.Context, int64, string) (telegramproxy.Credential, error)
	TelegramProxyAll(context.Context, int64) ([]telegramproxy.Credential, []string, error)
}

type TelegramProxyIssuer interface {
	List(context.Context, int64) ([]telegramproxy.Endpoint, error)
	Issue(context.Context, int64, string) (telegramproxy.Credential, error)
	IssueAll(context.Context, int64) ([]telegramproxy.Credential, []string, error)
}

const telegramProxyAllPath = "/api/miniapp/telegram/proxies/all"

// SetTelegramProxy is startup wiring; do not change providers during requests.
func (s *Service) SetTelegramProxy(issuer TelegramProxyIssuer) { s.telegramProxy = issuer }

func (s *Service) TelegramProxies(ctx context.Context, actor int64) ([]telegramproxy.Endpoint, error) {
	if _, err := s.bound(ctx, actor); err != nil {
		return nil, err
	}
	if s.telegramProxy == nil {
		return nil, ErrUnavailable
	}
	endpoints, err := s.telegramProxy.List(ctx, actor)
	if err != nil {
		return nil, telegramProxyError(err)
	}
	return endpoints, nil
}

func (s *Service) TelegramProxy(ctx context.Context, actor int64, id string) (telegramproxy.Credential, error) {
	if _, err := s.bound(ctx, actor); err != nil {
		return telegramproxy.Credential{}, err
	}
	if !validTelegramProxyID(id) {
		return telegramproxy.Credential{}, ErrNotFound
	}
	if s.telegramProxy == nil {
		return telegramproxy.Credential{}, ErrUnavailable
	}
	credential, err := s.telegramProxy.Issue(ctx, actor, id)
	if err != nil {
		return telegramproxy.Credential{}, telegramProxyError(err)
	}
	if credential.EndpointID != id || !telegramproxy.ValidCredentialURL(credential.URL) {
		return telegramproxy.Credential{}, ErrUnavailable
	}
	return credential, nil
}

// TelegramProxyAll issues every route for the bound actor in configuration
// order. At least one credential is returned on success; routes that could
// not be issued now are listed by endpoint ID. Revoked routes are omitted.
func (s *Service) TelegramProxyAll(ctx context.Context, actor int64) ([]telegramproxy.Credential, []string, error) {
	if _, err := s.bound(ctx, actor); err != nil {
		return nil, nil, err
	}
	if s.telegramProxy == nil {
		return nil, nil, ErrUnavailable
	}
	credentials, unavailable, err := s.telegramProxy.IssueAll(ctx, actor)
	if err != nil {
		return nil, nil, telegramProxyError(err)
	}
	credentials, unavailable = sanitizeTelegramProxies(credentials, unavailable)
	if len(credentials) == 0 {
		return nil, nil, ErrUnavailable
	}
	return credentials, unavailable, nil
}

// sanitizeTelegramProxies never trusts an issuer: unknown or duplicate routes
// and malformed links are dropped, and a dropped known route is reported as
// unavailable. Both results are non-nil so they encode as JSON arrays.
func sanitizeTelegramProxies(credentials []telegramproxy.Credential, unavailable []string) ([]telegramproxy.Credential, []string) {
	issued := make([]telegramproxy.Credential, 0, len(credentials))
	missing := []string{}
	seen := map[string]bool{}
	for _, credential := range credentials {
		if !validTelegramProxyID(credential.EndpointID) || seen[credential.EndpointID] {
			continue
		}
		seen[credential.EndpointID] = true
		if !telegramproxy.ValidCredentialURL(credential.URL) {
			missing = append(missing, credential.EndpointID)
			continue
		}
		issued = append(issued, telegramproxy.Credential{EndpointID: credential.EndpointID, Name: credential.Name, URL: credential.URL})
	}
	for _, id := range unavailable {
		if validTelegramProxyID(id) && !seen[id] {
			seen[id] = true
			missing = append(missing, id)
		}
	}
	return issued, missing
}

func telegramProxyError(err error) error {
	switch {
	case errors.Is(err, telegramproxy.ErrNotEligible):
		return ErrForbidden
	case errors.Is(err, telegramproxy.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, telegramproxy.ErrInactive), errors.Is(err, telegramproxy.ErrConflict):
		return ErrAccessDisabled
	default:
		return ErrUnavailable
	}
}

func validTelegramProxyID(id string) bool {
	switch id {
	case "finland", "stockholm", "msk-finland", "msk-stockholm":
		return true
	default:
		return false
	}
}

func telegramProxyPath(path string) bool {
	return path == "/api/miniapp/telegram/proxies" || path == "/api/miniapp/telegram/proxy" || path == telegramProxyAllPath
}

func (h *handler) serveTelegramProxy(ctx context.Context, w http.ResponseWriter, r *http.Request, actor int64, id string) {
	backend, ok := h.backend.(TelegramProxyBackend)
	if !ok {
		writeBackendError(w, ErrUnavailable)
		return
	}
	if r.URL.Path == "/api/miniapp/telegram/proxies" {
		endpoints, err := backend.TelegramProxies(ctx, actor)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		// Copy a strict metadata allowlist. List responses never contain grants,
		// URLs, secrets or inferred connection states.
		metadata := make([]telegramproxy.Endpoint, 0, len(endpoints))
		for _, endpoint := range endpoints {
			if !validTelegramProxyID(endpoint.ID) || endpoint.Protocol != "mtproto" {
				writeBackendError(w, ErrUnavailable)
				return
			}
			metadata = append(metadata, telegramproxy.Endpoint{ID: endpoint.ID, Name: endpoint.Name, Protocol: endpoint.Protocol, Primary: endpoint.Primary})
		}
		h.recordActivity(ctx, actor, "miniapp_telegram_proxies")
		writeJSON(w, http.StatusOK, map[string]any{"proxies": metadata})
		return
	}
	if r.URL.Path == telegramProxyAllPath {
		credentials, unavailable, err := backend.TelegramProxyAll(ctx, actor)
		if err != nil {
			writeBackendError(w, err)
			return
		}
		credentials, unavailable = sanitizeTelegramProxies(credentials, unavailable)
		if len(credentials) == 0 {
			writeBackendError(w, ErrUnavailable)
			return
		}
		h.recordActivity(ctx, actor, "miniapp_telegram_proxy_response")
		writeJSON(w, http.StatusOK, map[string]any{"proxies": credentials, "unavailable": unavailable})
		return
	}
	credential, err := backend.TelegramProxy(ctx, actor, id)
	if err != nil {
		writeBackendError(w, err)
		return
	}
	if credential.EndpointID != id || !telegramproxy.ValidCredentialURL(credential.URL) {
		writeBackendError(w, ErrUnavailable)
		return
	}
	h.recordActivity(ctx, actor, "miniapp_telegram_proxy_response")
	writeJSON(w, http.StatusOK, map[string]any{"proxy": credential, "state": "issued"})
}
