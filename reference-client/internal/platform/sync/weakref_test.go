package sync_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

// A field names its active boundary by a weak reference, so the boundary may
// arrive after the field. The writer sends field, boundary, field. A receiver
// applies the field at once and links the boundary when it is bound. See
// "Weak references" in specification.md.

func activeBoundaryIn(t *testing.T, modelled map[string]json.RawMessage) *string {
	t.Helper()
	raw, ok := modelled["_agmasync_activeBoundaryRef"]
	if !ok {
		return nil
	}
	var ref oapi.WeakRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatalf("reading %s: %v", raw, err)
	}
	return ref.Id.ReferenceId
}

func TestAnActiveBoundaryArrivingAfterItsFieldIsLinked(t *testing.T) {
	h := newHarness(t)
	types := []agmasync.EntityType{agmasync.TypeField, agmasync.TypeFieldBoundary}
	a := h.join("fmis-a", "ep-a", types...)
	b := h.join("fmis-b", "ep-b", types...)
	ctx := context.Background()

	// The writer sends the field, then the boundary. The field leaves out the
	// reference to FB-1, not sent yet, and creating FB-1 sends it again.
	upsertLocal(t, a, agmasync.TypeField, "PFD-1", map[string]any{
		"name": "North 40", "_agmasync_activeBoundaryRef": agmasync.LocalRef("FB-1"),
	})
	upsertLocal(t, a, agmasync.TypeFieldBoundary, "FB-1", map[string]any{
		"_agmasync_fieldRef": agmasync.LocalRef("PFD-1"),
		"boundary":           map[string]any{"geometry": "POLYGON ((8 52, 8.1 52, 8.1 52.1, 8 52))"},
	})
	if _, err := a.Send(ctx, agmasync.TypeField, "PFD-1"); err != nil {
		t.Fatalf("sending the field: %v", err)
	}
	if _, err := a.Send(ctx, agmasync.TypeFieldBoundary, "FB-1"); err != nil {
		t.Fatalf("sending the boundary: %v", err)
	}
	if got := activeBoundaryIn(t, loadRecord(t, a, agmasync.TypeField, "PFD-1").Modelled); got == nil || *got != "FB-1" {
		t.Errorf("the writer's active boundary = %v, want FB-1", got)
	}

	delivered := deliverTo(t, h, "fmis-b")
	if len(delivered) != 2 || delivered[0].Envelope.Type != agmasync.TypeField {
		t.Fatalf("delivered %d objects, want the field, then its boundary", len(delivered))
	}

	field, err := b.Apply(entityOf(t, delivered[0]), delivered[0].ID)
	if err != nil {
		t.Fatalf("applying the field: %v", err)
	}
	if got := activeBoundaryIn(t, loadRecord(t, b, agmasync.TypeField, field.LocalID).Modelled); got != nil {
		t.Errorf("active boundary before the boundary arrived = %q, want none yet", *got)
	}
	// A write meanwhile leaves the reference out, which keeps it.
	var row store.SyncRow
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(b.Namespace(), agmasync.TypeField, field.LocalID)
		return err
	}); err != nil {
		t.Fatalf("reading sync row: %v", err)
	}
	if err := b.Bind(ctx, agmasync.TypeField, field.LocalID, *row.AgrirouterID); err != nil {
		t.Fatalf("binding the field: %v", err)
	}
	upsertLocal(t, b, agmasync.TypeField, field.LocalID, map[string]any{"name": "North 41"})
	if _, err := b.Send(ctx, agmasync.TypeField, field.LocalID); err != nil {
		t.Fatalf("sending the field before its boundary arrived: %v", err)
	}

	boundary, err := b.Apply(entityOf(t, delivered[1]), delivered[1].ID)
	if err != nil {
		t.Fatalf("applying the boundary: %v", err)
	}
	got := activeBoundaryIn(t, loadRecord(t, b, agmasync.TypeField, field.LocalID).Modelled)
	if got == nil || *got != boundary.LocalID {
		t.Errorf("active boundary after the boundary arrived = %v, want %s", got, boundary.LocalID)
	}
}
