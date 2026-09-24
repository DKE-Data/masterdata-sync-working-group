package sync_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

// A reset reaches the platform on its stream. It drops the tenant's pairs and
// keeps the records, is stated as an empty selection on the endpoint, and its
// position is held before anything after it is applied, so it is not delivered
// twice. The load after it is a first load, and the record goes back to
// agrirouter as new.
func TestAResetDropsThePairsAndKeepsTheRecords(t *testing.T) {
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	if _, err := loader(a).Run(context.Background()); err != nil {
		t.Fatalf("first load: %v", err)
	}
	createLocalFarm(t, a, "FRM-1", "Hof Nord")
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("send: %v", err)
	}
	before := syncRow(t, a, agmasync.TypeFarm, "FRM-1")

	client, err := agmasync.NewClient(h.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	var selections [][]agmasync.EntityType
	var positionAtReroute string
	resets := 0
	receiver := &psync.Receiver{
		Client:  client,
		Store:   a.Store,
		Tenants: map[uuid.UUID]*psync.Applier{h.tenant: a},
		OnSelection: func(tx *store.Tx, s oapi.RouteChangedEventData) error {
			types := agmasync.SelectedTypes(s)
			selections = append(selections, types)
			if resets == 1 && len(types) > 0 {
				// The route the user created after the reset. Whatever comes
				// after the reset must find its position already held: a crash
				// here would otherwise resume from before it.
				var err error
				positionAtReroute, err = tx.Position()
				return err
			}
			return nil
		},
		OnReset: func(_ *store.Tx, r oapi.MasterdataResetEventData, discarded int) error {
			resets++
			if discarded != 1 {
				t.Errorf("discarded %d pairs, want 1", discarded)
			}
			return nil
		},
	}
	if _, err := receiver.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up: %v", err)
	}
	beforeReset, err := a.Store.Position()
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	selections = nil

	// Reset and routed again, both while the platform is away.
	if err := h.router.ResetTenant(h.tenant); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := h.router.OptIn("ep-a", agmasync.TypeFarm); err != nil {
		t.Fatalf("routing again: %v", err)
	}
	if _, err := receiver.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up after reset: %v", err)
	}

	if resets != 1 {
		t.Fatalf("OnReset called %d times, want 1", resets)
	}
	if len(selections) != 2 || len(selections[0]) != 0 || len(selections[1]) == 0 {
		t.Errorf("selections = %v, want the reset's empty one and then the new route", selections)
	}
	if positionAtReroute == "" || positionAtReroute == beforeReset {
		t.Error("the reset's position was not held before what followed it was applied")
	}
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		if _, err := tx.SyncRow(agmasync.TypeFarm, "FRM-1"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("sync row after reset: err = %v, want none", err)
		}
		held, err := tx.Exists(agmasync.TypeFarm, "FRM-1")
		if err != nil {
			return err
		}
		if !held {
			t.Error("the reset deleted the platform's own record")
		}
		return nil
	}); err != nil {
		t.Fatalf("reading store: %v", err)
	}

	// The position was taken with the discard, so the reset is not told again.
	if _, err := receiver.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up again: %v", err)
	}
	if resets != 1 {
		t.Errorf("OnReset called %d times after resuming past it, want 1", resets)
	}

	// A first load, and the record goes back as a new object.
	res, err := loader(a).Run(context.Background())
	if err != nil {
		t.Fatalf("load after reset: %v", err)
	}
	if res.Repeat {
		t.Error("the load after a reset reported itself a repeat")
	}
	after := syncRow(t, a, agmasync.TypeFarm, "FRM-1")
	if !after.Bound() {
		t.Fatal("the record was not sent back after the reset")
	}
	if *after.AgrirouterID == *before.AgrirouterID {
		t.Error("the record is bound to the agrirouterId the reset discarded")
	}
}

// The pairs are found by the tenant the frame names, not through an applier,
// so a reset of a tenant the receiver holds no applier for — one the
// application has no endpoint left in — still clears them.
func TestAResetClearsThePairsOfATenantNoApplierClaims(t *testing.T) {
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	if _, err := loader(a).Run(context.Background()); err != nil {
		t.Fatalf("first load: %v", err)
	}
	createLocalFarm(t, a, "FRM-1", "Hof Nord")
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if err := h.router.ResetTenant(h.tenant); err != nil {
		t.Fatalf("reset: %v", err)
	}

	client, err := agmasync.NewClient(h.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	receiver := &psync.Receiver{
		Client:  client,
		Store:   a.Store,
		Tenants: map[uuid.UUID]*psync.Applier{},
	}
	if _, err := receiver.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up: %v", err)
	}

	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		if _, err := tx.SyncRow(agmasync.TypeFarm, "FRM-1"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("sync row after reset: err = %v, want none", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("reading store: %v", err)
	}
}
