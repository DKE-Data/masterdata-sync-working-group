package sync_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter"
	"github.com/google/uuid"
)

// counterIDs mints identifiers the way a platform with an auto-increment key
// would: sequentially, and not of the platform's choosing in any meaningful
// sense. That is the reason binding exists at all.
type counterIDs struct {
	prefix string
	n      int
}

func (c *counterIDs) New(typ agmasync.EntityType) string {
	c.n++
	return fmt.Sprintf("%s-%s-%d", c.prefix, typ, c.n)
}

type harness struct {
	t      *testing.T
	router *testrouter.Router
	server *httptest.Server
	tenant uuid.UUID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	r := testrouter.New()
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)

	return &harness{t: t, router: r, server: srv, tenant: r.AddTenant()}
}

// join onboards one of a platform's tenants: an endpoint of its own, and an
// applier acting for that tenant over the platform's store.
func (h *harness) join(appID, externalID string, types ...agmasync.EntityType) *psync.Applier {
	h.t.Helper()
	return h.joinTenant(appID, externalID, "tenant-"+externalID, nil, types...)
}

// joinTenant onboards a second tenant of a platform that already has one, over
// the same store — the multi-tenant case. Pass the store of the first tenant to
// share it.
func (h *harness) joinTenant(
	appID, externalID, tenant string, db *store.Store, types ...agmasync.EntityType,
) *psync.Applier {
	h.t.Helper()

	endpointID := h.router.AddEndpoint(appID, h.tenant, externalID)
	if err := h.router.OptIn(externalID, types...); err != nil {
		h.t.Fatalf("opt in: %v", err)
	}

	client, err := agmasync.NewClient(h.server.URL, agmasync.WithBearerToken(appID))
	if err != nil {
		h.t.Fatalf("client: %v", err)
	}

	if db == nil {
		var err error
		db, err = store.Open(":memory:")
		if err != nil {
			h.t.Fatalf("store: %v", err)
		}
		h.t.Cleanup(func() { _ = db.Close() })
	}

	return &psync.Applier{
		Store:    db,
		Tenant:   tenant,
		Endpoint: client.For(endpointID, externalID),
		IDs:      &counterIDs{prefix: tenant},
	}
}

// createLocalFarm creates a record in the platform's own store, as its user
// would, with no involvement from the exchange.
func createLocalFarm(t *testing.T, a *psync.Applier, localID, name string, extra map[string]any) {
	t.Helper()

	modelled := map[string]json.RawMessage{"name": mustJSON(t, name)}
	unmodelled := map[string]json.RawMessage{}
	for k, v := range extra {
		unmodelled[k] = mustJSON(t, v)
	}

	err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		return tx.UpsertRecord(store.Record{
			EntityType: agmasync.TypeFarm,
			LocalID:    localID,
			Modelled:   modelled,
			Unmodelled: unmodelled,
		}, localID)
	})
	if err != nil {
		t.Fatalf("seeding farm: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return raw
}

func TestSendCreatesAndRecordsTheCorrespondence(t *testing.T) {
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)

	createLocalFarm(t, a, "FRM-1", "Hof Nord", nil)

	outcome, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if outcome.LocalID != "FRM-1" {
		t.Errorf("localId = %q, want the platform's own", outcome.LocalID)
	}

	// The response is applied, not merely acknowledged: it is the only place
	// the sender learns the assigned agrirouterId and its revision.
	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(agmasync.TypeFarm, "FRM-1")
		return err
	}); err != nil {
		t.Fatalf("reading sync row: %v", err)
	}
	if !row.Bound() {
		t.Fatal("the platform should hold the canonical identifier after a create")
	}
	if row.Revision == nil || *row.Revision != 1 {
		t.Errorf("revision = %v, want 1", row.Revision)
	}
}

func TestUpdateCarriesTheHeldRevisionAsBase(t *testing.T) {
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	createLocalFarm(t, a, "FRM-1", "Hof Nord", nil)

	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The user renames it locally, then the platform sends again.
	createLocalFarm(t, a, "FRM-1", "Hof Süd", nil)
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("update should carry the held revision as its base: %v", err)
	}

	var base any
	for _, obs := range h.router.Observations() {
		if obs.Kind == "put" {
			base = obs.Detail["baseRevision"]
		}
	}
	if base == nil {
		t.Error("the second write went out with no base revision")
	}
}

func TestApplyIsGuardedByRevision(t *testing.T) {
	// A write response may be processed after a later stream frame has already
	// been applied, so order alone is not enough across the two channels: an
	// object whose revision is lower than the one held must not be applied.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	createLocalFarm(t, a, "FRM-1", "Hof Nord", nil)

	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	createLocalFarm(t, a, "FRM-1", "Hof Süd", nil)
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("update: %v", err)
	}

	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(agmasync.TypeFarm, "FRM-1")
		return err
	}); err != nil {
		t.Fatalf("reading sync row: %v", err)
	}
	held := *row.Revision

	// Replay revision 1 over the top of what is held.
	stale := 1
	old, err := agmasync.FromFarm(oapi.Farm{
		AgrirouterId: row.AgrirouterID,
		LocalId:      strptr("FRM-1"),
		Name:         "Hof Nord",
		Revision:     &stale,
	})
	if err != nil {
		t.Fatalf("building stale object: %v", err)
	}

	outcome, err := a.Apply(old, "")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !outcome.Superseded {
		t.Error("an object older than the one held must not be applied")
	}

	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(agmasync.TypeFarm, "FRM-1")
		return err
	}); err != nil {
		t.Fatalf("reading sync row: %v", err)
	}
	if *row.Revision != held {
		t.Errorf("revision fell back from %d to %d", held, *row.Revision)
	}
}

func TestDeliveryWithoutLocalIDIsCreatedAndBound(t *testing.T) {
	// An absent localId says agrirouter does not believe this platform holds
	// the object. The platform creates it, mints an identifier, and binds —
	// and only then may it send that object.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	createLocalFarm(t, a, "FRM-1", "Hof Nord", nil)
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	delivered := deliverTo(t, h, "fmis-b")
	if len(delivered) == 0 {
		t.Fatal("the other participant received nothing")
	}

	outcome, err := b.Apply(delivered[0].Entity, delivered[0].ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !outcome.Created || !outcome.NeedsBinding {
		t.Fatalf("outcome = %+v, want a created record awaiting binding", outcome)
	}

	var row store.SyncRow
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(agmasync.TypeFarm, outcome.LocalID)
		return err
	}); err != nil {
		t.Fatalf("reading sync row: %v", err)
	}

	if err := b.Bind(
		context.Background(), agmasync.TypeFarm, outcome.LocalID, *row.AgrirouterID,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Having bound, the platform may now send its own edits to that object.
	if _, err := b.Send(context.Background(), agmasync.TypeFarm, outcome.LocalID); err != nil {
		t.Fatalf("sending a bound record should resolve: %v", err)
	}
}

func TestPositionAdvancesOnlyWithTheObjectItCovers(t *testing.T) {
	// The position must be derived from what has been durably applied. Storing
	// it in the same transaction as the object is what makes that true: after a
	// successful apply both moved, and after a failed one neither did.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	createLocalFarm(t, a, "FRM-1", "Hof Nord", nil)
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	before, err := b.Store.Position()
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if before != "" {
		t.Errorf("position = %q, want empty before anything is applied", before)
	}

	delivered := deliverTo(t, h, "fmis-b")
	if len(delivered) == 0 {
		t.Fatal("the other participant received nothing")
	}
	if _, err := b.Apply(delivered[0].Entity, delivered[0].ID); err != nil {
		t.Fatalf("apply: %v", err)
	}

	after, err := b.Store.Position()
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if after != delivered[0].ID {
		t.Errorf("position = %q, want the applied frame's id %q", after, delivered[0].ID)
	}
}

func TestUnmodelledAttributesSurviveARoundTrip(t *testing.T) {
	// A participant must preserve what it does not model and relay it
	// unchanged. This platform has no column for specialisedUsageType, so if it
	// did not keep the value, sending the record back would erase it for
	// everybody.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)

	createLocalFarm(t, a, "FRM-1", "Hof Nord", map[string]any{
		"specialisedUsageType": "dairy",
	})
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	var record store.Record
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		record, err = tx.LoadRecord(agmasync.TypeFarm, "FRM-1")
		return err
	}); err != nil {
		t.Fatalf("loading record: %v", err)
	}

	raw, ok := record.Unmodelled["specialisedUsageType"]
	if !ok {
		t.Fatal("an attribute the platform does not model was dropped")
	}
	var usage string
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if usage != "dairy" {
		t.Errorf("specialisedUsageType = %q, want %q relayed unchanged", usage, "dairy")
	}
}

func TestUnrecognisedInactiveObjectCreatesNothing(t *testing.T) {
	// An inactive object the platform does not hold is ignored. Creating a
	// record for an entity the world considers gone would be inventing data,
	// and the "absent localId means create and bind" rule does not reach it.
	h := newHarness(t)
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	inactive := false
	revision := 4
	id := uuid.New()
	entity, err := agmasync.FromFarm(oapi.Farm{
		AgrirouterId: &id,
		Name:         "Hof Vergangen",
		Active:       &inactive,
		Revision:     &revision,
	})
	if err != nil {
		t.Fatalf("building entity: %v", err)
	}

	outcome, err := b.Apply(entity, "")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !outcome.Ignored {
		t.Errorf("outcome = %+v, want the object ignored", outcome)
	}
	if outcome.LocalID != "" {
		t.Errorf("a record was created under %q for an unrecognised inactive object",
			outcome.LocalID)
	}
}

// deliverTo drains what one participant's live stream is holding.
func deliverTo(t *testing.T, h *harness, appID string) []agmasync.Event {
	t.Helper()

	client, err := agmasync.NewClient(h.server.URL, agmasync.WithBearerToken(appID))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Events(ctx, "")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()

	var out []agmasync.Event
	for ev, err := range stream.Events() {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if ev.Type == "CAUGHT_UP" {
			break
		}
		if ev.HasEntity() {
			out = append(out, ev)
		}
	}
	return out
}

func strptr(s string) *string { return &s }

func TestTwoTenantsOfOnePlatformShareOneRecord(t *testing.T) {
	// The case the application-scoped mapping decides. One product, one store,
	// two tenants its users switch between — and one local identifier names one
	// record whichever of them reaches it, because that is what agrirouter
	// resolves it as. Each tenant is onboarded as its own endpoint, and what the
	// tenant decides is which of them holds the record, not which record the
	// identifier names.
	h := newHarness(t)
	first := h.join("fmis-a", "ep-a1", agmasync.TypeFarm)
	second := h.joinTenant("fmis-a", "ep-a2", "tenant-two", first.Store, agmasync.TypeFarm)

	createLocalFarm(t, first, "FRM-1", "Hof Nord", nil)
	createLocalFarm(t, second, "FRM-2", "Hof Süd", nil)

	// Each tenant lists what it holds and not the other's holdings.
	for _, c := range []struct {
		applier *psync.Applier
		want    string
	}{{first, "FRM-1"}, {second, "FRM-2"}} {
		var ids []string
		if err := c.applier.Store.Tx(c.applier.Tenant, func(tx *store.Tx) error {
			var err error
			ids, err = tx.LocalIDs(agmasync.TypeFarm)
			return err
		}); err != nil {
			t.Fatalf("listing %s's farms: %v", c.applier.Tenant, err)
		}
		if len(ids) != 1 || ids[0] != c.want {
			t.Errorf("%s holds %v, want only %q", c.applier.Tenant, ids, c.want)
		}
	}

	// The second tenant taking on the first tenant's record joins it rather than
	// copying it: same row, same bookkeeping, and after the send the same
	// canonical object.
	if _, err := first.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("send from the first tenant: %v", err)
	}

	var held store.Record
	var row store.SyncRow
	if err := second.Store.Tx(second.Tenant, func(tx *store.Tx) error {
		var err error
		if held, err = tx.LoadRecord(agmasync.TypeFarm, "FRM-1"); err != nil {
			return err
		}
		row, err = tx.SyncRow(agmasync.TypeFarm, "FRM-1")
		return err
	}); err != nil {
		t.Fatalf("the second tenant reading the platform's record: %v", err)
	}
	if string(held.Modelled["name"]) != `"Hof Nord"` {
		t.Errorf("name = %s, want the one record the platform holds", held.Modelled["name"])
	}
	if !row.Bound() {
		t.Error("the binding is the platform's, so it stands for the second tenant too")
	}
}

func TestOneTenantsBindingSpeaksForTheWholePlatform(t *testing.T) {
	// Bookkeeping is the platform's, so a delivery reaching a second tenant is
	// matched against the binding the first tenant produced. If it matched per
	// tenant, the second would create a duplicate record and bind a second
	// identifier to an object agrirouter already knows this participant's name
	// for — which is the 409 the specification calls a non-unique mapping.
	h := newHarness(t)
	sender := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	first := h.join("fmis-b", "ep-b1", agmasync.TypeFarm)
	second := h.joinTenant("fmis-b", "ep-b2", "tenant-two", first.Store, agmasync.TypeFarm)

	createLocalFarm(t, sender, "FRM-1", "Hof Nord", nil)
	if _, err := sender.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	delivered := deliverTo(t, h, "fmis-b")
	if len(delivered) == 0 {
		t.Fatal("the receiving platform received nothing")
	}

	// The first tenant creates the platform's record for the object and binds it.
	outcome, err := first.Apply(delivered[0].Entity, delivered[0].ID)
	if err != nil {
		t.Fatalf("apply in the first tenant: %v", err)
	}
	if !outcome.NeedsBinding {
		t.Fatalf("outcome = %+v, want a created record awaiting binding", outcome)
	}
	var row store.SyncRow
	if err := first.Store.Tx(first.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(agmasync.TypeFarm, outcome.LocalID)
		return err
	}); err != nil {
		t.Fatalf("reading sync row: %v", err)
	}
	if err := first.Bind(
		context.Background(), agmasync.TypeFarm, outcome.LocalID, *row.AgrirouterID,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// The same object reaching the second tenant finds the platform's own record
	// through the mapping. Nothing is created, and there is nothing left to
	// bind — agrirouter already holds this participant's identifier for it.
	secondOutcome, err := second.Apply(delivered[0].Entity, delivered[0].ID)
	if err != nil {
		t.Fatalf("apply in the second tenant: %v", err)
	}
	if secondOutcome.Created || secondOutcome.NeedsBinding {
		t.Errorf("outcome = %+v, want the second tenant to join the record the platform holds",
			secondOutcome)
	}
	if secondOutcome.LocalID != outcome.LocalID {
		t.Errorf("localId = %q, want the platform's own %q",
			secondOutcome.LocalID, outcome.LocalID)
	}

	// What the second tenant does gain is the record: it holds it now, and its
	// user sees it, which is the only thing the tenant partitions.
	var holds bool
	if err := second.Store.Tx(second.Tenant, func(tx *store.Tx) error {
		var err error
		holds, err = tx.Exists(agmasync.TypeFarm, secondOutcome.LocalID)
		return err
	}); err != nil {
		t.Fatalf("checking what the second tenant holds: %v", err)
	}
	if !holds {
		t.Error("applying a delivery in a tenant must leave that tenant holding the record")
	}
}
