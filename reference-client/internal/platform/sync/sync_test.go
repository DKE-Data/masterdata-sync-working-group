package sync_test

import (
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"iter"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/agrirouter"
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
// share it. Both are onboarded into the harness's one agrirouter tenant.
func (h *harness) joinTenant(
	appID, externalID, tenant string, db *store.Store, types ...agmasync.EntityType,
) *psync.Applier {
	h.t.Helper()
	return h.joinAgrirouterTenant(h.tenant, appID, externalID, tenant, db, types...)
}

// joinAgrirouterTenant is joinTenant into an agrirouter tenant of its own.
func (h *harness) joinAgrirouterTenant(
	agrirouterTenant uuid.UUID, appID, externalID, tenant string, db *store.Store,
	types ...agmasync.EntityType,
) *psync.Applier {
	h.t.Helper()

	endpointID := h.router.AddEndpoint(appID, agrirouterTenant, externalID)
	if err := h.router.OptIn(externalID, types...); err != nil {
		h.t.Fatalf("opt in: %v", err)
	}

	client, err := agrirouter.NewClient(h.server.URL, agrirouter.WithBearerToken(appID))
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
		Endpoint: agrirouter.For(client, endpointID, externalID, agrirouterTenant),
		IDs:      &counterIDs{prefix: tenant},
	}
}

// createLocalFarm creates a record in the platform's own store, as its user
// would, with no involvement from the exchange.
func createLocalFarm(t *testing.T, a *psync.Applier, localID, name string) {
	t.Helper()
	upsertLocal(t, a, agmasync.TypeFarm, localID, map[string]any{"name": name})
}

// upsertLocal writes a record's modelled attributes as the platform's user
// would. Whatever the record held before is replaced.
func upsertLocal(
	t *testing.T, a *psync.Applier, typ agmasync.EntityType, localID string, modelled map[string]any,
) {
	t.Helper()

	attributes := map[string]json.RawMessage{}
	for k, v := range modelled {
		attributes[k] = mustJSON(t, v)
	}
	err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		return tx.UpsertRecord(store.Record{
			EntityType: typ,
			LocalID:    localID,
			Modelled:   attributes,
		}, localID)
	})
	if err != nil {
		t.Fatalf("writing local %s: %v", typ, err)
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

	createLocalFarm(t, a, "FRM-1", "Hof Nord")

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
		row, err = tx.SyncRow(a.Namespace(), agmasync.TypeFarm, "FRM-1")
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
	createLocalFarm(t, a, "FRM-1", "Hof Nord")

	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The user renames it locally, then the platform sends again.
	createLocalFarm(t, a, "FRM-1", "Hof Süd")
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
	createLocalFarm(t, a, "FRM-1", "Hof Nord")

	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	createLocalFarm(t, a, "FRM-1", "Hof Süd")
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("update: %v", err)
	}

	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(a.Namespace(), agmasync.TypeFarm, "FRM-1")
		return err
	}); err != nil {
		t.Fatalf("reading sync row: %v", err)
	}
	held := *row.Revision

	// Replay revision 1 over the top of what is held.
	stale := 1
	id := agmasync.CanonicalRef(*row.AgrirouterID).Id
	id.ReferenceId = strptr("FRM-1")
	old, err := agrirouter.FromFarm(oapi.Farm{
		TenantId: &h.tenant,
		Content:  oapi.FarmContent{Id: id, Name: "Hof Nord"},
		Revision: &stale,
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
		row, err = tx.SyncRow(a.Namespace(), agmasync.TypeFarm, "FRM-1")
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

	createLocalFarm(t, a, "FRM-1", "Hof Nord")
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	delivered := deliverTo(t, h, "fmis-b")
	if len(delivered) == 0 {
		t.Fatal("the other participant received nothing")
	}

	outcome, err := b.Apply(entityOf(t, delivered[0]), delivered[0].ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !outcome.Created || !outcome.NeedsBinding {
		t.Fatalf("outcome = %+v, want a created record awaiting binding", outcome)
	}

	var row store.SyncRow
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(b.Namespace(), agmasync.TypeFarm, outcome.LocalID)
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

	createLocalFarm(t, a, "FRM-1", "Hof Nord")
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
	if _, err := b.Apply(entityOf(t, delivered[0]), delivered[0].ID); err != nil {
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

// canonicalOf reads the canonical object as agrirouter holds it now, through a
// write that changes nothing: its response is the whole current object.
func canonicalOf(t *testing.T, a *psync.Applier, typ agmasync.EntityType, localID string) map[string]json.RawMessage {
	t.Helper()
	return putRaw(t, a, body(string(typ), localID, ""), nil)
}

// body is the JSON a participant sends: an entity of type typ written under
// localID, its content the given members after the id.
func body(typ, localID, members string) string {
	if members != "" {
		members = "," + members
	}
	return fmt.Sprintf(`{"type":%q,"content":{"id":{"referenceId":%q}%s}}`, typ, localID, members)
}

// contentOf reads the content attributes of an entity putRaw answered with.
func contentOf(t *testing.T, entity map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(entity["content"], &out); err != nil {
		t.Fatalf("reading content: %v", err)
	}
	return out
}

// putRaw writes JSON as another integration of the same participant would,
// bypassing the platform's own store, and applies nothing.
func putRaw(t *testing.T, a *psync.Applier, body string, base *int) map[string]json.RawMessage {
	t.Helper()
	got, err := a.Endpoint.PutJSON(context.Background(), []byte(body), base)
	if err != nil {
		t.Fatalf("put %s: %v", body, err)
	}
	raw, _ := got.MarshalJSON()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func loadRecord(t *testing.T, a *psync.Applier, typ agmasync.EntityType, localID string) store.Record {
	t.Helper()
	var record store.Record
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		record, err = tx.LoadRecord(typ, localID)
		return err
	}); err != nil {
		t.Fatalf("loading record: %v", err)
	}
	return record
}

func revisionAfter(t *testing.T, attributes map[string]json.RawMessage) int {
	t.Helper()
	var rev int
	if err := json.Unmarshal(attributes["revision"], &rev); err != nil {
		t.Fatalf("reading revision: %v", err)
	}
	return rev
}

// applyRaw writes JSON the platform did not originate and applies the result,
// as it would apply the delivery of another participant's change.
func applyRaw(t *testing.T, a *psync.Applier, body string, base int) {
	t.Helper()
	got, err := a.Endpoint.PutJSON(context.Background(), []byte(body), &base)
	if err != nil {
		t.Fatalf("put %s: %v", body, err)
	}
	if _, err := a.Apply(got, ""); err != nil {
		t.Fatalf("applying: %v", err)
	}
}

func TestAnAttributeThePlatformDoesNotModelSurvivesItsWrites(t *testing.T) {
	// A write only changes what it carries. This platform has no column for
	// _agmasync_specialisedUsageType and never sends it, so its writes leave it alone
	// for everybody who does model it, without the platform storing it.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)

	createLocalFarm(t, a, "FRM-1", "Hof Nord")
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	applyRaw(t, a, body("farm", "FRM-1", `"name":"Hof Nord","_agmasync_specialisedUsageType":"dairy"`),
		revisionAfter(t, canonicalOf(t, a, agmasync.TypeFarm, "FRM-1")))

	createLocalFarm(t, a, "FRM-1", "Hof Süd")
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got := contentOf(t, canonicalOf(t, a, agmasync.TypeFarm, "FRM-1"))
	if string(got["name"]) != `"Hof Süd"` {
		t.Errorf("name = %s, want the rename", got["name"])
	}
	if string(got["_agmasync_specialisedUsageType"]) != `"dairy"` {
		t.Errorf("_agmasync_specialisedUsageType = %s, want it kept by a write that left it out",
			got["_agmasync_specialisedUsageType"])
	}
}

func TestAClearedAttributeIsSentAsNull(t *testing.T) {
	// Leaving an attribute out keeps it, so a platform that clears one it
	// models has to say so. Only the part of the boundary it models is cleared:
	// the correction network, which it does not model, stays.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeField, agmasync.TypeFieldBoundary)

	upsertLocal(t, a, agmasync.TypeField, "PFD-1", map[string]any{"name": "North 40"})
	geometry := "POLYGON ((8 52, 8.1 52, 8.1 52.1, 8 52))"
	boundary := func(parts map[string]any) map[string]any {
		return map[string]any{
			"_agmasync_fieldRef": agmasync.LocalRef("PFD-1"),
			"boundary":           parts,
		}
	}
	upsertLocal(t, a, agmasync.TypeFieldBoundary, "FB-1", boundary(map[string]any{
		"geometry": geometry, "boundaryCreationMethodCode": "DRIVEN",
	}))
	for _, typ := range []agmasync.EntityType{agmasync.TypeField, agmasync.TypeFieldBoundary} {
		id := "PFD-1"
		if typ == agmasync.TypeFieldBoundary {
			id = "FB-1"
		}
		if _, err := a.Send(context.Background(), typ, id); err != nil {
			t.Fatalf("create %s: %v", typ, err)
		}
	}
	applyRaw(t, a, body("fieldBoundary", "FB-1", `"boundary":{"gNSSCorrectionNetworkName":"SAPOS"}`),
		revisionAfter(t, canonicalOf(t, a, agmasync.TypeFieldBoundary, "FB-1")))

	upsertLocal(t, a, agmasync.TypeFieldBoundary, "FB-1", boundary(map[string]any{"geometry": geometry}))
	if _, err := a.Send(context.Background(), agmasync.TypeFieldBoundary, "FB-1"); err != nil {
		t.Fatalf("clearing the creation method: %v", err)
	}

	got := contentOf(t, canonicalOf(t, a, agmasync.TypeFieldBoundary, "FB-1"))
	want := `{"gNSSCorrectionNetworkName":"SAPOS","geometry":"` + geometry + `"}`
	if string(got["boundary"]) != want {
		t.Errorf("boundary = %s, want %s: the creation method removed and the rest kept", got["boundary"], want)
	}
}

func TestAnAttributeRemovedElsewhereIsClearedLocally(t *testing.T) {
	// A delivered object is whole: an attribute it does not carry is unset, and
	// a column still holding the old value would send it back on the next write.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeField)

	upsertLocal(t, a, agmasync.TypeField, "PFD-1", map[string]any{
		"name": "North 40", "arableArea": map[string]any{"numericValue": 12.5, "unitOfMeasureCode": "ha"},
	})
	if _, err := a.Send(context.Background(), agmasync.TypeField, "PFD-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	applyRaw(t, a, body("field", "PFD-1", `"name":"North 40","arableArea":null`),
		revisionAfter(t, canonicalOf(t, a, agmasync.TypeField, "PFD-1")))

	if raw, ok := loadRecord(t, a, agmasync.TypeField, "PFD-1").Modelled["arableArea"]; ok {
		t.Errorf("arableArea = %s, want the column cleared", raw)
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
	entity, err := agrirouter.FromFarm(oapi.Farm{
		TenantId: &h.tenant,
		Content:  oapi.FarmContent{Id: agmasync.CanonicalRef(id).Id, Name: "Hof Vergangen"},
		Active:   &inactive,
		Revision: &revision,
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

func TestObjectNamingNoTenantIsDiscarded(t *testing.T) {
	// The live stream carries every tenant the application is routed to, so an
	// object naming none cannot be attributed to any one applier, even where
	// there is only one.
	h := newHarness(t)
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	id := uuid.New()
	entity, err := agrirouter.FromFarm(oapi.Farm{
		Content: oapi.FarmContent{Id: agmasync.CanonicalRef(id).Id, Name: "Hof Ohne"},
	})
	if err != nil {
		t.Fatalf("building entity: %v", err)
	}

	outcome, err := b.Apply(entity, "pos-1")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !outcome.Discarded || outcome.LocalID != "" {
		t.Errorf("outcome = %+v, want the object discarded", outcome)
	}
	position, err := b.Store.Position()
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if position != "" {
		t.Errorf("position = %q, want none taken for a discarded object", position)
	}
}

// deliverTo drains what one participant's live stream is holding.
// entityOf is the object a frame carries, as the platform applies it.
func entityOf(t *testing.T, ev agmasync.Event) oapi.Entity {
	t.Helper()
	entity, err := agrirouter.EntityOf(ev.Object)
	if err != nil {
		t.Fatal(err)
	}
	return entity
}

func deliverTo(t *testing.T, h *harness, appID string) []agmasync.Event {
	t.Helper()
	return deliverFrom(t, h, appID, "")
}

// deliverFrom is deliverTo resuming after position.
func deliverFrom(t *testing.T, h *harness, appID, position string) []agmasync.Event {
	t.Helper()

	client, err := agrirouter.NewClient(h.server.URL, agrirouter.WithBearerToken(appID))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := agmasync.Events(ctx, client, position)
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
	// One product, one store, two tenancies its users switch between, onboarded
	// into one agrirouter tenant as an endpoint each. One local identifier names
	// one record here whichever tenancy reaches it; agrirouter keys the mapping
	// by the endpoint, so each endpoint binds that record for itself, to the one
	// canonical object.
	h := newHarness(t)
	first := h.join("fmis-a", "ep-a1", agmasync.TypeFarm)
	second := h.joinTenant("fmis-a", "ep-a2", "tenant-two", first.Store, agmasync.TypeFarm)

	createLocalFarm(t, first, "FRM-1", "Hof Nord")
	createLocalFarm(t, second, "FRM-2", "Hof Süd")

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
	// copying it: same record, and after its own send the same canonical
	// object, bound through its own pair.
	if _, err := first.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("send from the first tenant: %v", err)
	}
	if err := second.Store.Tx(second.Tenant, func(tx *store.Tx) error {
		record, err := tx.LoadRecord(agmasync.TypeFarm, "FRM-1")
		if err != nil {
			return err
		}
		if string(record.Modelled["name"]) != `"Hof Nord"` {
			t.Errorf("name = %s, want the one record the platform holds", record.Modelled["name"])
		}
		_, err = tx.SyncRow(second.Namespace(), agmasync.TypeFarm, "FRM-1")
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("err = %v, want no pair for the second endpoint before it binds", err)
		}
		return tx.UpsertRecord(record, "FRM-1")
	}); err != nil {
		t.Fatalf("the second tenant taking on the platform's record: %v", err)
	}

	// Sending it unbound would mint a second canonical object. The sibling's
	// pair says what it is, so the send binds first and is then a no-op.
	outcome, err := second.Send(context.Background(), agmasync.TypeFarm, "FRM-1")
	if err != nil {
		t.Fatalf("send from the second tenant: %v", err)
	}
	if outcome.Created {
		t.Error("the second tenant's send must not create a record")
	}
	firstRow := syncRow(t, first, agmasync.TypeFarm, "FRM-1")
	secondRow := syncRow(t, second, agmasync.TypeFarm, "FRM-1")
	if !secondRow.Bound() || *secondRow.AgrirouterID != *firstRow.AgrirouterID {
		t.Errorf("second pair = %+v, want its own pair to the first's object %s",
			secondRow, *firstRow.AgrirouterID)
	}
	if *secondRow.Revision != *firstRow.Revision {
		t.Errorf("revision = %d, want %d: an equal payload is a no-op",
			*secondRow.Revision, *firstRow.Revision)
	}
}

func TestASiblingTenancyBindsTheRecordTheOtherHolds(t *testing.T) {
	// Bookkeeping is per endpoint, the records are the platform's. A delivery
	// reaching the second tenancy carries no localId — its endpoint has bound
	// nothing — but the store already holds the record through the first, so
	// it is joined and bound under the same local identifier, not created
	// again.
	h := newHarness(t)
	sender := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	first := h.join("fmis-b", "ep-b1", agmasync.TypeFarm)
	second := h.joinTenant("fmis-b", "ep-b2", "tenant-two", first.Store, agmasync.TypeFarm)

	createLocalFarm(t, sender, "FRM-1", "Hof Nord")
	if _, err := sender.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// One frame per endpoint, each rendered for its recipient.
	frames := map[uuid.UUID]agmasync.Event{}
	for _, ev := range deliverTo(t, h, "fmis-b") {
		frames[*ev.Envelope.RecipientEndpointId] = ev
	}
	forFirst, ok1 := frames[first.Endpoint.ID()]
	forSecond, ok2 := frames[second.Endpoint.ID()]
	if !ok1 || !ok2 {
		t.Fatalf("frames for %v, want one for each endpoint", frames)
	}

	// The first tenant creates the platform's record for the object and binds it.
	outcome, err := first.Apply(entityOf(t, forFirst), forFirst.ID)
	if err != nil {
		t.Fatalf("apply in the first tenant: %v", err)
	}
	if !outcome.Created || !outcome.NeedsBinding {
		t.Fatalf("outcome = %+v, want a created record awaiting binding", outcome)
	}
	if err := first.Bind(
		context.Background(), agmasync.TypeFarm, outcome.LocalID, *forFirst.Envelope.AgrirouterID,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	secondOutcome, err := second.Apply(entityOf(t, forSecond), forSecond.ID)
	if err != nil {
		t.Fatalf("apply in the second tenant: %v", err)
	}
	if secondOutcome.Created || !secondOutcome.Joined || !secondOutcome.NeedsBinding {
		t.Errorf("outcome = %+v, want the record joined and awaiting this endpoint's binding",
			secondOutcome)
	}
	if secondOutcome.LocalID != outcome.LocalID {
		t.Errorf("localId = %q, want the platform's own %q",
			secondOutcome.LocalID, outcome.LocalID)
	}
	// The same local identifier on a second endpoint is not a conflict.
	if err := second.Bind(
		context.Background(), agmasync.TypeFarm, secondOutcome.LocalID, *forSecond.Envelope.AgrirouterID,
	); err != nil {
		t.Fatalf("bind through the second endpoint: %v", err)
	}

	// What the second tenant also gains is the record: it holds it now, and
	// its user sees it.
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

func TestOneRecordInTwoAgrirouterTenantsIsBoundInEach(t *testing.T) {
	// The mapping is keyed by the agrirouter tenant too. A record two tenancies
	// hold, each onboarded into an agrirouter tenant of its own, is one record
	// here and two canonical objects there, and the platform keeps a pair for
	// each under the same local identifier. Keyed by the application alone, the
	// second tenancy's send would resolve to the first tenant's object, which it
	// cannot touch.
	h := newHarness(t)
	other := h.router.AddTenant()
	first := h.join("fmis-a", "ep-a1", agmasync.TypeFarm)
	second := h.joinAgrirouterTenant(
		other, "fmis-a", "ep-a2", "tenant-two", first.Store, agmasync.TypeFarm)

	createLocalFarm(t, first, "FRM-1", "Hof Nord")
	if _, err := first.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("send from the first tenant: %v", err)
	}

	// The second tenancy takes the record on and sends it into its own tenant.
	createLocalFarm(t, second, "FRM-1", "Hof Nord")
	if _, err := second.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("send from the second tenant: %v", err)
	}

	firstRow := syncRow(t, first, agmasync.TypeFarm, "FRM-1")
	secondRow := syncRow(t, second, agmasync.TypeFarm, "FRM-1")
	if !firstRow.Bound() || !secondRow.Bound() {
		t.Fatalf("bound = %v, %v, want the record bound in both tenants",
			firstRow.Bound(), secondRow.Bound())
	}
	if *firstRow.AgrirouterID == *secondRow.AgrirouterID {
		t.Error("one record in two agrirouter tenants is two canonical objects")
	}
	if firstRow.Endpoint.TenantID != h.tenant || secondRow.Endpoint.TenantID != other {
		t.Errorf("tenants = %s, %s, want each pair filed under its own tenant",
			firstRow.Endpoint.TenantID, secondRow.Endpoint.TenantID)
	}

	// Correcting in one tenant leaves the other's pair standing.
	if err := second.Correct(context.Background(), agmasync.TypeFarm,
		[]oapi.IdMappingCorrectionEntry{agmasync.Correction("FRM-1", nil)}); err != nil {
		t.Fatalf("correct in the second tenant: %v", err)
	}
	if !syncRow(t, first, agmasync.TypeFarm, "FRM-1").Bound() {
		t.Error("correcting in one tenant must not touch the other tenant's pair")
	}
}

func TestACorrectedSwapRebindsBothRecordsAndFetchesTheirObjects(t *testing.T) {
	// Two similar farms the user matched crosswise. The correction rewrites both
	// local pairs at once, and the requested objects bring each record the
	// content and revision of the farm it now names.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	ctx := context.Background()

	createLocalFarm(t, a, "FRM-N", "Hof Nord")
	createLocalFarm(t, a, "FRM-S", "Hof Süd")
	for _, id := range []string{"FRM-N", "FRM-S"} {
		if _, err := a.Send(ctx, agmasync.TypeFarm, id); err != nil {
			t.Fatalf("send %s: %v", id, err)
		}
	}
	north := *syncRow(t, a, agmasync.TypeFarm, "FRM-N").AgrirouterID
	south := *syncRow(t, a, agmasync.TypeFarm, "FRM-S").AgrirouterID

	createLocalFarm(t, b, "B-N", "Nord")
	createLocalFarm(t, b, "B-S", "Süd")
	for localID, id := range map[string]uuid.UUID{"B-N": south, "B-S": north} {
		if err := b.Endpoint.Bind(ctx, agmasync.TypeFarm, localID, id); err != nil {
			t.Fatalf("bind %s: %v", localID, err)
		}
		if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
			return tx.PutSyncRow(store.SyncRow{
				Endpoint: b.Namespace(), EntityType: agmasync.TypeFarm, LocalID: localID, AgrirouterID: &id,
			})
		}); err != nil {
			t.Fatalf("recording %s: %v", localID, err)
		}
	}

	// What arrives through the wrong pairs is applied to the wrong records.
	position := ""
	for _, ev := range deliverTo(t, h, "fmis-b") {
		if _, err := b.Apply(entityOf(t, ev), ev.ID); err != nil {
			t.Fatalf("apply: %v", err)
		}
		position = ev.ID
	}
	if got := loadRecord(t, b, agmasync.TypeFarm, "B-N").Modelled["name"]; string(got) != `"Hof Süd"` {
		t.Fatalf("before the correction B-N name = %s, want the crosswise \"Hof Süd\"", got)
	}

	// A requested object is delivered live, so the stream is open, and caught
	// up, before the correction requests them.
	client, err := agrirouter.NewClient(h.server.URL, agrirouter.WithBearerToken("fmis-b"))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := agmasync.Events(streamCtx, client, position)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()
	next, stop := iter.Pull2(stream.Events())
	defer stop()
	for {
		ev, err, ok := next()
		if !ok || err != nil {
			t.Fatalf("stream ended before catching up: %v", err)
		}
		if ev.Type == "CAUGHT_UP" {
			break
		}
	}

	if err := b.Correct(ctx, agmasync.TypeFarm, []oapi.IdMappingCorrectionEntry{
		agmasync.Correction("B-N", &north),
		agmasync.Correction("B-S", &south),
	}); err != nil {
		t.Fatalf("correct: %v", err)
	}
	for localID, want := range map[string]uuid.UUID{"B-N": north, "B-S": south} {
		if got := syncRow(t, b, agmasync.TypeFarm, localID).AgrirouterID; got == nil || *got != want {
			t.Errorf("%s bound to %v, want %s", localID, got, want)
		}
	}

	for applied := 0; applied < 2; {
		ev, err, ok := next()
		if !ok || err != nil {
			t.Fatalf("stream ended before both requested objects arrived: %v", err)
		}
		if !ev.HasEntity() {
			continue
		}
		if _, err := b.Apply(entityOf(t, ev), ev.ID); err != nil {
			t.Fatalf("apply: %v", err)
		}
		applied++
	}
	for localID, name := range map[string]string{"B-N": "Hof Nord", "B-S": "Hof Süd"} {
		if got := loadRecord(t, b, agmasync.TypeFarm, localID).Modelled["name"]; string(got) != strconv.Quote(name) {
			t.Errorf("%s name = %s, want %q", localID, got, name)
		}
		if syncRow(t, b, agmasync.TypeFarm, localID).Revision == nil {
			t.Errorf("%s holds no revision after its object was delivered", localID)
		}
	}
}

func TestAPartyOfUnknownTypeIsFiledAndSentAsAnOrganization(t *testing.T) {
	// This platform keeps only persons and organizations, so a party delivered
	// without _agmasync_details lands in the organization table, and its next write
	// states ORGANIZATION to everyone. That is its choice, which the
	// specification leaves to each participant.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeParty)

	created := putRaw(t, a, body("party", "PTY-1", `"name":"Hof Müller"`), nil)
	if _, err := a.Apply(entityFrom(t, created), ""); err != nil {
		t.Fatalf("applying: %v", err)
	}
	if _, err := a.Send(context.Background(), agmasync.TypeParty, "PTY-1"); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := contentOf(t, canonicalOf(t, a, agmasync.TypeParty, "PTY-1"))
	if string(got["_agmasync_details"]) != `{"partyTypeCode":"ORGANIZATION"}` {
		t.Errorf("_agmasync_details = %s, want the platform's ORGANIZATION stated", got["_agmasync_details"])
	}
}

func TestAPartyTypeChangeMovesTheRecordAndKeepsWhatItDoesNotModel(t *testing.T) {
	// A delivery stating PERSON moves the record to the person table. Its writes
	// then carry the person's modelled parts and leave parentOrganizationRef, which this
	// platform does not model, to whoever does.
	h := newHarness(t)
	a := h.join("fmis-a", "ep-a", agmasync.TypeParty)

	for _, id := range []string{"ORG-1", "PTY-1"} {
		upsertLocal(t, a, agmasync.TypeParty, id, map[string]any{
			"name": id, "_agmasync_details": map[string]any{"partyTypeCode": "ORGANIZATION"},
		})
		if _, err := a.Send(context.Background(), agmasync.TypeParty, id); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	applyRaw(t, a,
		body("party", "PTY-1", `"name":"PTY-1","_agmasync_details":{"partyTypeCode":"PERSON","lastName":"Meyer",`+
			`"parentOrganizationRef":{"id":{"referenceId":"ORG-1"}}}`),
		revisionAfter(t, canonicalOf(t, a, agmasync.TypeParty, "PTY-1")))

	// Read back from the person table. The organization table is read first, so
	// a row left behind there would answer instead.
	record := loadRecord(t, a, agmasync.TypeParty, "PTY-1")
	if string(record.Modelled["_agmasync_details"]) != `{"lastName":"Meyer","partyTypeCode":"PERSON"}` {
		t.Errorf("held details = %s, want the person's modelled parts", record.Modelled["_agmasync_details"])
	}
	record.Modelled["name"] = mustJSON(t, "Anke Meyer")
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		return tx.UpsertRecord(record, "PTY-1")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Send(context.Background(), agmasync.TypeParty, "PTY-1"); err != nil {
		t.Fatalf("update: %v", err)
	}

	var details struct {
		PartyTypeCode         string          `json:"partyTypeCode"`
		LastName              string          `json:"lastName"`
		ParentOrganizationRef json.RawMessage `json:"parentOrganizationRef"`
	}
	got := contentOf(t, canonicalOf(t, a, agmasync.TypeParty, "PTY-1"))
	if err := json.Unmarshal(got["_agmasync_details"], &details); err != nil {
		t.Fatal(err)
	}
	if details.PartyTypeCode != "PERSON" || details.LastName != "Meyer" || len(details.ParentOrganizationRef) == 0 {
		t.Errorf("_agmasync_details = %s, want the person kept with its parentOrganizationRef", got["_agmasync_details"])
	}
}

func entityFrom(t *testing.T, attributes map[string]json.RawMessage) oapi.Entity {
	t.Helper()
	raw, err := json.Marshal(attributes)
	if err != nil {
		t.Fatal(err)
	}
	var ent oapi.Entity
	if err := ent.UnmarshalJSON(raw); err != nil {
		t.Fatal(err)
	}
	return ent
}
