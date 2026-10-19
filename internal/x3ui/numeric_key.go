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

var (
	ErrKeyUnavailable = errors.New("key unavailable")
	ErrKeyNotFound    = errors.New("existing key not found")
	ErrKeyDisabled    = errors.New("key access disabled")
	ErrKeyConflict    = errors.New("key ownership conflict")
)

// ownedEmail never claims a historical unbound client by its mutable email.
// Multiple same-ID or same-email clients are ambiguous and fail closed.
func ownedEmail(inbound *x3client.Inbound, email string, id int64, now time.Time) (string, bool, error) {
	var settings struct {
		Clients []x3client.InboundClient `json:"clients"`
	}
	if !inbound.Enable {
		return "", false, ErrKeyDisabled
	}
	if json.Unmarshal([]byte(inbound.Settings), &settings) != nil || id <= 0 || strings.TrimSpace(email) == "" {
		return "", false, ErrKeyUnavailable
	}
	var own *x3client.InboundClient
	for i := range settings.Clients {
		client := &settings.Clients[i]
		if client.TgID.Value != nil && *client.TgID.Value == id {
			if own != nil {
				return "", false, ErrKeyConflict
			}
			own = client
		}
	}
	if own != nil {
		if !own.Enable || own.Email == "" || (own.ExpiryTime > 0 && own.ExpiryTime <= now.UnixMilli()) {
			return "", false, ErrKeyDisabled
		}
		count := 0
		for _, client := range settings.Clients {
			if client.Email == own.Email {
				count++
			}
		}
		if count != 1 {
			return "", false, ErrKeyConflict
		}
		for _, stats := range inbound.ClientStats {
			if stats.Email == own.Email && (!stats.Enable || (stats.ExpiryTime > 0 && stats.ExpiryTime <= now.UnixMilli()) || (stats.Total > 0 && stats.Up+stats.Down >= stats.Total)) {
				return "", false, ErrKeyDisabled
			}
		}
		return own.Email, true, nil
	}
	return "", false, ErrKeyNotFound
}

// existingInbound is shared by Mini App status and key retrieval. It only
// reads the panel; absence/deletion never provisions a replacement client.
func (sh *ServerHandler) existingInbound(ctx context.Context, server *database.Server, email string, id int64) (*x3client.Inbound, string, error) {
	if server == nil || id <= 0 || sh.IsServerDisabled(server.ID) || server.InboundID == nil || ctx.Err() != nil {
		return nil, "", ErrKeyUnavailable
	}
	lock := &sh.keyLocks[uint64(id)%uint64(len(sh.keyLocks))]
	if !lock.TryLock() {
		return nil, "", ErrKeyUnavailable
	}
	defer lock.Unlock()
	client, ok := sh.getX3Client(server.ID)
	if !ok {
		return nil, "", ErrKeyUnavailable
	}
	var response x3client.APIResponse[[]x3client.Inbound]
	_, err := client.Resty.R().SetContext(ctx).SetResult(&response).Post("/panel/inbound/list")
	if err != nil || !response.Success {
		return nil, "", ErrKeyUnavailable
	}
	for i := range response.Obj {
		inbound := &response.Obj[i]
		if inbound.ID != *server.InboundID {
			continue
		}
		selected, exists, err := ownedEmail(inbound, email, id, time.Now())
		if err != nil {
			return nil, "", err
		}
		if !exists {
			return nil, "", ErrKeyNotFound
		}
		return inbound, selected, nil
	}
	return nil, "", ErrKeyNotFound
}

func (sh *ServerHandler) CheckUserKeyForTelegramID(ctx context.Context, server *database.Server, email string, id int64) error {
	_, _, err := sh.existingInbound(ctx, server, email, id)
	return err
}

// GetUserKeyForTelegramID reveals an existing numeric-owned credential only.
// The original Telegram bot GetUserKey retains its separate provisioning path.
func (sh *ServerHandler) GetUserKeyForTelegramID(ctx context.Context, server *database.Server, email string, id int64) (string, error) {
	inbound, selected, err := sh.existingInbound(ctx, server, email, id)
	if err != nil {
		return "", err
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
