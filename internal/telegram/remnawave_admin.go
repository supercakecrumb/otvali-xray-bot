package telegram

import (
	"context"
	"errors"

	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type stockholmGrantFinder interface {
	FindGrant(context.Context, int64) (remnawave.Mapping, bool, error)
}

type stockholmRevocationChecker interface {
	CheckDisabled(context.Context, int64) error
}

func checkStockholmDeletion(ctx context.Context, telegramID int64, store stockholmGrantFinder, issuer StockholmIssuer) error {
	_, found, err := store.FindGrant(ctx, telegramID)
	if err != nil {
		return errors.New("could not verify Stockholm access")
	}
	if !found {
		return nil
	}
	checker, ok := issuer.(stockholmRevocationChecker)
	if !ok {
		return errors.New("Stockholm access must be checked before deleting the bot account")
	}
	return checker.CheckDisabled(ctx, telegramID)
}
