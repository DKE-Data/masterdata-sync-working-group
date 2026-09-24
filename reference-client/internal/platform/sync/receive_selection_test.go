package sync_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

// A withdrawal made while nobody was on the stream reaches the participant on
// its next connection, as a frame stating an empty selection.
//
// This is the case the protocol is built around: the endpoint has no selection
// left to state, so restating only what endpoints currently exchange would never
// mention it again and the platform would go on sending types nobody wants until
// a write is refused. agrirouter retains that the endpoint took part at some
// point, which is what puts the frame on catch-up.
func TestAWithdrawalMadeWhileAwayArrivesOnCatchUp(t *testing.T) {
	h := newHarness(t)
	applier := h.join("fmis-a", "ep-a", agmasync.TypeFarm)

	// A second endpoint of the same application, which the user never routed to
	// the hub. It never took part, so nothing is owed for it.
	h.router.AddEndpoint("fmis-a", h.tenant, "ep-quiet")

	// The user withdraws from ep-a while nobody is on the stream. The frame for
	// this reaches nobody live.
	if err := h.router.OptIn("ep-a"); err != nil {
		t.Fatalf("deselecting everything: %v", err)
	}

	client, err := agmasync.NewClient(h.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	seen := map[string][]agmasync.EntityType{}
	receiver := &psync.Receiver{
		Client:  client,
		Store:   applier.Store,
		Tenants: map[uuid.UUID]*psync.Applier{h.tenant: applier},
		OnSelection: func(_ *store.Tx, s oapi.RouteChangedEventData) error {
			seen[s.ExternalId] = agmasync.SelectedTypes(s)
			return nil
		},
	}

	if _, err := receiver.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up: %v", err)
	}

	// ep-a is stated as exchanging nothing. The empty list is the statement —
	// the receiver is told, rather than having to notice an absence and know
	// which endpoints it holds to do so.
	if got, ok := seen["ep-a"]; !ok || len(got) != 0 {
		t.Errorf("ep-a selection = %v (present %v), want empty", got, ok)
	}

	// ep-quiet never took part, so there is nothing to restate for it. An
	// endpoint the user never routed is not news, and saying so on every
	// connection would make catch-up proportional to how many endpoints the
	// application has rather than to how many ever took part.
	if _, ok := seen["ep-quiet"]; ok {
		t.Error("stated a selection for an endpoint that never took part")
	}
	if len(seen) != 1 {
		t.Fatalf("saw %d selections, want 1: %v", len(seen), seen)
	}
}

// A selection the platform cannot record takes no position with it, and is
// stated again on the next connection.
//
// This is the failure the transaction exists for. agrirouter restates a
// selection only above the participant's position, so a position covering a
// withdrawal the platform never durably recorded loses that withdrawal for good:
// nothing restates it, nothing detects the gap, and the platform goes on
// offering entity types its user switched off until a write is refused.
func TestASelectionThatCannotBeRecordedTakesNoPosition(t *testing.T) {
	h := newHarness(t)
	applier := h.join("fmis-a", "ep-a", agmasync.TypeFarm)

	// The user withdraws while nobody is on the stream, so the frame is owed to
	// the next connection rather than delivered live.
	if err := h.router.OptIn("ep-a"); err != nil {
		t.Fatalf("deselecting everything: %v", err)
	}

	client, err := agmasync.NewClient(h.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	// The platform records what a selection means to it — here, a marker record
	// standing in for whatever a product actually does — and then fails.
	mark := func(tx *store.Tx) error {
		return tx.UpsertRecord(store.Record{
			EntityType: agmasync.TypeOrganization,
			LocalID:    "SEL-MARK",
			Modelled:   map[string]json.RawMessage{"name": mustJSON(t, "ep-a")},
		}, "SEL-MARK")
	}

	failing := &psync.Receiver{
		Client:  client,
		Store:   applier.Store,
		Tenants: map[uuid.UUID]*psync.Applier{h.tenant: applier},
		OnSelection: func(tx *store.Tx, s oapi.RouteChangedEventData) error {
			// The tenancy is the one the named endpoint was onboarded as, so a
			// handler acting on a withdrawal acts on the right holdings.
			if tx.Tenant() != applier.Tenant {
				t.Errorf("tenant = %q, want the tenancy holding %q",
					tx.Tenant(), s.ExternalId)
			}
			if err := mark(tx); err != nil {
				return err
			}
			return errors.New("the platform could not record the selection")
		},
	}

	if _, err := failing.CatchUp(context.Background()); err == nil {
		t.Fatal("catch up succeeded, want the failure to reach the caller")
	}

	// Neither half of the transaction stands.
	if position, err := applier.Store.Position(); err != nil {
		t.Fatalf("reading the position: %v", err)
	} else if position != "" {
		t.Errorf("position = %q, want none taken for a selection that was not recorded", position)
	}
	if err := applier.Store.Tx(applier.Tenant, func(tx *store.Tx) error {
		_, err := tx.LoadRecord(agmasync.TypeOrganization, "SEL-MARK")
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("loading the marker = %v, want it rolled back with the position", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("reading back: %v", err)
	}

	// And because no position was taken, the withdrawal is still owed.
	var seen []string
	recovering := &psync.Receiver{
		Client:  client,
		Store:   applier.Store,
		Tenants: map[uuid.UUID]*psync.Applier{h.tenant: applier},
		OnSelection: func(tx *store.Tx, s oapi.RouteChangedEventData) error {
			seen = append(seen, s.ExternalId)
			return mark(tx)
		},
	}
	if _, err := recovering.CatchUp(context.Background()); err != nil {
		t.Fatalf("catching up again: %v", err)
	}
	if !slices.Contains(seen, "ep-a") {
		t.Fatalf("selections stated on the retry = %v, want the withdrawal again", seen)
	}

	if position, err := applier.Store.Position(); err != nil {
		t.Fatalf("reading the position: %v", err)
	} else if position == "" {
		t.Error("position = none, want the selection's own position once it was recorded")
	}
	if err := applier.Store.Tx(applier.Tenant, func(tx *store.Tx) error {
		_, err := tx.LoadRecord(agmasync.TypeOrganization, "SEL-MARK")
		return err
	}); err != nil {
		t.Errorf("loading the marker after the retry: %v", err)
	}
}

// A route change made while the participant is connected arrives as a frame carrying the
// selection itself.
func TestAMoveArrivesCarryingTheWholeSelection(t *testing.T) {
	h := newHarness(t)
	applier := h.join("fmis-a", "ep-a")

	client, err := agmasync.NewClient(h.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	var seen [][]agmasync.EntityType
	receiver := &psync.Receiver{
		Client:  client,
		Store:   applier.Store,
		Tenants: map[uuid.UUID]*psync.Applier{h.tenant: applier},
		OnSelection: func(_ *store.Tx, s oapi.RouteChangedEventData) error {
			if s.ExternalId == "ep-a" {
				seen = append(seen, agmasync.SelectedTypes(s))
			}
			return nil
		},
	}

	// Connecting says nothing: the user has never selected anything on this
	// endpoint, so it has never taken part and there is nothing to restate.
	if _, err := receiver.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up: %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("selection on connecting = %v, want nothing stated", seen)
	}

	// The user selects farms, which pulls in the parties farms reference.
	if err := h.router.OptIn("ep-a", agmasync.TypeFarm); err != nil {
		t.Fatalf("selecting farms: %v", err)
	}

	if _, err := receiver.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up: %v", err)
	}

	// The frame states the closure and not just what the user clicked: a
	// participant acts on it as it stands rather than closing it itself.
	want := []agmasync.EntityType{
		agmasync.TypeOrganization, agmasync.TypePerson, agmasync.TypeFarm,
	}
	if len(seen) == 0 || !slices.Equal(seen[len(seen)-1], want) {
		t.Errorf("selection after the move = %v, want %v", seen, want)
	}
}
