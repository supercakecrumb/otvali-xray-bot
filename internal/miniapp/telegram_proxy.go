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
}

type TelegramProxyIssuer interface {
	List(context.Context, int64) ([]telegramproxy.Endpoint, error)
	Issue(context.Context, int64, string) (telegramproxy.Credential, error)
}

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
	return path == "/api/miniapp/telegram/proxies" || path == "/api/miniapp/telegram/proxy"
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
