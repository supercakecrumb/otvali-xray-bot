package x3ui

import (
	"fmt"
	"log/slog"

	x3client "github.com/supercakecrumb/go-x3ui/client"
	"github.com/supercakecrumb/otvali-xray-bot/internal/database"
)

func (sh *ServerHandler) CreateInbound(server *database.Server) (*x3client.Inbound, error) {
	// Define inbound configuration
	inboundPayload, err := sh.x3Clients[server.ID].GenerateDefaultInboundConfig(defaultInboundRemark, server.RealityCover, server.IP, defaultInboundPort)
	if err != nil {
		sh.logger.Error("Failed to create inbound payload", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to create inbound payload: %w", err)
	}

	// Create inbound
	inbound, err := sh.x3Clients[server.ID].AddInbound(inboundPayload)
	if err != nil {
		sh.logger.Error("Failed to create inbound", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to create inbound: %w", err)
	}

	return inbound, nil
}

func (sh *ServerHandler) GetUserKey(server *database.Server, email string, tgID int64) (string, error) {
	x3c := sh.x3Clients[server.ID]

	inbound, err := sh.getPrimaryInbound(server)
	if err != nil {
		sh.logger.Error("error during getting key", slog.String("error", err.Error()))
		return "", err
	}

	created := false
	for _, cs := range inbound.ClientStats {
		if cs.Email == email {
			created = true
			break
		}
	}
	if !created {
		sh.logger.Debug("User is not created yet", slog.String("email", email), slog.Int64("tgID", tgID))
		err = sh.createUserKey(server, x3c, email, tgID)
		if err != nil {
			sh.logger.Error("error cretin user key", slog.String("error", err.Error()))
			return "", err
		}
	}

	inbound, err = sh.getPrimaryInbound(server)
	if err != nil {
		sh.logger.Error("error during getting key", slog.String("error", err.Error()))
		return "", err
	}

	key, err := x3client.GenerateVLESSLink(*inbound, email)
	if err != nil {
		sh.logger.Error("error generating vless link", slog.String("error", err.Error()))
		return "", err
	}

	return key, nil
}

func (sh *ServerHandler) getPrimaryInbound(server *database.Server) (*x3client.Inbound, error) {
	x3c := sh.x3Clients[server.ID]

	inbounds, err := x3c.ListInbounds()
	if err != nil {
		sh.logger.Error("error getting primary inbound", slog.String("error", err.Error()))
		return nil, err
	}

	for i, inb := range inbounds {
		if inb.ID == *server.InboundID {
			return &inbounds[i], nil
		}
	}

	err = fmt.Errorf("primary inbound not found")
	sh.logger.Error("error getting primary inbound")
	return nil, err
}

func (sh *ServerHandler) createUserKey(server *database.Server, x3c *x3client.Client, email string, tgID int64) error {
	newUserConfig := x3c.GenerateDefaultInboundClient(email, tgID)
	err := x3c.AddInboundClient(*server.InboundID, newUserConfig)
	if err != nil {
		sh.logger.Error("error creating new inbound client", slog.String("error", err.Error()))
		return err
	}
	return nil
}
