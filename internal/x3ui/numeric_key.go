package x3ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	x3client "github.com/supercakecrumb/go-x3ui/client"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

var ErrKeyUnavailable = errors.New("key unavailable")

// The vendored FlexibleInt64 only unmarshals and otherwise marshals as an
// object. The panel contract requires a numeric/string tgId, so override only
// that wire field while retaining the existing generator's remaining fields.
type numericPanelClient struct {
	x3client.InboundClient
	TelegramID *int64 `json:"tgId"`
}

// ownedEmail never claims a historical unbound client by its mutable email.
// Multiple same-ID or same-email clients are ambiguous and fail closed.
func ownedEmail(inbound *x3client.Inbound, email string, id int64, now time.Time) (string, bool, error) {
	var settings struct {
		Clients []x3client.InboundClient `json:"clients"`
	}
	if !inbound.Enable || json.Unmarshal([]byte(inbound.Settings), &settings) != nil || id <= 0 || strings.TrimSpace(email) == "" {
		return "", false, ErrKeyUnavailable
	}
	var own *x3client.InboundClient
	for i := range settings.Clients {
		client := &settings.Clients[i]
		if client.TgID.Value != nil && *client.TgID.Value == id {
			if own != nil {
				return "", false, ErrKeyUnavailable
			}
			own = client
		}
	}
	if own != nil {
		if !own.Enable || own.Email == "" || (own.ExpiryTime > 0 && own.ExpiryTime <= now.UnixMilli()) {
			return "", false, ErrKeyUnavailable
		}
		count := 0
		for _, client := range settings.Clients {
			if client.Email == own.Email {
				count++
			}
		}
		if count != 1 {
			return "", false, ErrKeyUnavailable
		}
		for _, stats := range inbound.ClientStats {
			if stats.Email == own.Email && (!stats.Enable || (stats.ExpiryTime > 0 && stats.ExpiryTime <= now.UnixMilli()) || (stats.Total > 0 && stats.Up+stats.Down >= stats.Total)) {
				return "", false, ErrKeyUnavailable
			}
		}
		return own.Email, true, nil
	}
	for _, client := range settings.Clients {
		if client.Email == email {
			return "", false, ErrKeyUnavailable
		}
	}
	for _, stats := range inbound.ClientStats {
		if stats.Email == email {
			return "", false, ErrKeyUnavailable
		}
	}
	return email, false, nil
}

// GetUserKeyForTelegramID uses the same client generator, 3x-ui endpoints and
// VLESS renderer as legacy issuance, but proves numeric ownership first.
// Its lock is shared with bot GetUserKey so concurrent surfaces cannot add two
// credentials. No account/config/error body is logged or returned on failure.
func (sh *ServerHandler) GetUserKeyForTelegramID(ctx context.Context, server *database.Server, email string, id int64) (string, error) {
	if server == nil || id <= 0 || sh.IsServerDisabled(server.ID) || server.InboundID == nil || ctx.Err() != nil {
		return "", ErrKeyUnavailable
	}
	lock := &sh.keyLocks[uint64(id)%uint64(len(sh.keyLocks))]
	if !lock.TryLock() {
		return "", ErrKeyUnavailable
	}
	defer lock.Unlock()
	client, ok := sh.getX3Client(server.ID)
	if !ok {
		return "", ErrKeyUnavailable
	}
	read := func() (*x3client.Inbound, error) {
		var response x3client.APIResponse[[]x3client.Inbound]
		_, err := client.Resty.R().SetContext(ctx).SetResult(&response).Post("/panel/inbound/list")
		if err != nil || !response.Success {
			return nil, ErrKeyUnavailable
		}
		for i := range response.Obj {
			if response.Obj[i].ID == *server.InboundID {
				return &response.Obj[i], nil
			}
		}
		return nil, ErrKeyUnavailable
	}
	inbound, err := read()
	if err != nil {
		return "", ErrKeyUnavailable
	}
	selected, exists, err := ownedEmail(inbound, email, id, time.Now())
	if err != nil {
		return "", ErrKeyUnavailable
	}
	if !exists {
		if ctx.Err() != nil {
			return "", ErrKeyUnavailable
		}
		generated := client.GenerateDefaultInboundClient(selected, id)
		settings, err := json.Marshal(struct {
			Clients []numericPanelClient `json:"clients"`
		}{Clients: []numericPanelClient{{InboundClient: generated, TelegramID: &id}}})
		if err != nil {
			return "", ErrKeyUnavailable
		}
		var response x3client.APIResponse[any]
		_, err = client.Resty.R().SetContext(ctx).SetBody(map[string]any{"id": *server.InboundID, "settings": string(settings)}).SetResult(&response).Post("/panel/inbound/addClient")
		// Always reconcile the numeric client after a possibly ambiguous response;
		// never retry AddClient blindly.
		if ctx.Err() != nil {
			return "", ErrKeyUnavailable
		}
		inbound, err = read()
		if err != nil {
			return "", ErrKeyUnavailable
		}
		selected, exists, err = ownedEmail(inbound, email, id, time.Now())
		if err != nil || !exists {
			return "", ErrKeyUnavailable
		}
	}
	if ctx.Err() != nil {
		return "", ErrKeyUnavailable
	}
	key, err := x3client.GenerateVLESSLink(*inbound, selected)
	if err != nil {
		return "", ErrKeyUnavailable
	}
	key, err = replaceVLESSHost(key, server.IP, inbound.Port)
	if err != nil {
		return "", ErrKeyUnavailable
	}
	return key, nil
}
