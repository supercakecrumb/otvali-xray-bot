package telegram

import (
	"context"
	"errors"
	"testing"

	"github.com/supercakecrumb/otvali-xray-bot/internal/remnawave"
)

type deletionStore struct{ found, fail bool }

func (s deletionStore) FindGrant(context.Context, int64) (remnawave.Mapping, bool, error) {
	if s.fail {
		return remnawave.Mapping{}, false, errors.New("private store error")
	}
	return remnawave.Mapping{}, s.found, nil
}

type deletionIssuer struct{ disabled bool }

func (deletionIssuer) Eligible(context.Context, int64) bool { return true }
func (deletionIssuer) Issue(context.Context, int64) (remnawave.Subscription, error) {
	panic("deletion must not issue access")
}
func (s deletionIssuer) CheckDisabled(context.Context, int64) error {
	if !s.disabled {
		return remnawave.ErrInactive
	}
	return nil
}
func TestBotRowDeletionCannotSilentlyLeaveStockholmAccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		store     deletionStore
		issuer    StockholmIssuer
		wantError bool
	}{
		{"legacy-only", deletionStore{}, nil, false},
		{"provider-disabled-feature-off", deletionStore{found: true}, nil, true},
		{"active", deletionStore{found: true}, deletionIssuer{}, true},
		{"disabled", deletionStore{found: true}, deletionIssuer{disabled: true}, false},
		{"database-failure", deletionStore{fail: true}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkStockholmDeletion(context.Background(), 7, tc.store, tc.issuer); (err != nil) != tc.wantError {
				t.Fatalf("incorrect deletion guard: %v", err)
			}
		})
	}
}
