package remnawave

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStatusUnprovisionedIsReadOnly(t *testing.T) {
	s, api, store := serviceFixture()
	state, err := s.Status(context.Background(), 7)
	if err != nil || state != AccessUnprovisioned || api.creates != 0 || store.exists {
		t.Fatal("status must identify an eligible unprovisioned account without writing")
	}
}
func TestStatusDetectsInactiveDeletedAndConflictingMappedAccount(t *testing.T) {
	for _, mode := range []string{"ready", "disabled", "expired", "deleted", "conflict", "missing_squad"} {
		t.Run(mode, func(t *testing.T) {
			s, api, store := serviceFixture()
			if _, err := s.Issue(context.Background(), 7); err != nil {
				t.Fatal(err)
			}
			before := store.mapping
			creates := api.creates
			switch mode {
			case "disabled":
				api.user.Status = "DISABLED"
			case "expired":
				api.user.ExpireAt = s.now().Add(-time.Second)
			case "deleted":
				api.user = nil
			case "conflict":
				api.user.ID++
			case "missing_squad":
				api.user.ActiveInternalSquads = nil
			}
			state, err := s.Status(context.Background(), 7)
			if mode == "ready" {
				if err != nil || state != AccessReady {
					t.Fatal("active account should be ready")
				}
			} else if mode == "conflict" {
				if !errors.Is(err, ErrConflict) {
					t.Fatal("identity conflict must not be ready")
				}
			} else if !errors.Is(err, ErrInactive) {
				t.Fatal("known disabled/expired/deleted/unassigned access must remain disabled")
			}
			if api.creates != creates || store.mapping != before {
				t.Fatal("read-only status mutated identity or recreated revoked access")
			}
		})
	}
}
