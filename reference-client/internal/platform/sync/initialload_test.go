package sync_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
)

// loader is the endpoint's whole initial load, with the sample's own
// recognition step behind it.
//
// The types are passed in rather than read here, as they are in the loader
// itself. Every endpoint the harness joins is opted into farms, which pulls in
// the parties farms reference.
func loader(a *psync.Applier, types ...agmasync.EntityType) *psync.Loader {
	if len(types) == 0 {
		types = agmasync.DependencyClosure([]agmasync.EntityType{agmasync.TypeFarm})
	}
	return &psync.Loader{Applier: a, Reconciler: psync.ByName{}, Types: types}
}

// contributed puts one farm into the SSOT from another participant, so that the
// endpoint under test has a canonical set to be sent.
func contributed(t *testing.T, h *harness, name string) *psync.Applier {
	t.Helper()
	a := h.join("fmis-a", "ep-a", agmasync.TypeFarm)
	createLocalFarm(t, a, "FRM-1", name)
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("contributing a farm: %v", err)
	}
	return a
}

func syncRow(t *testing.T, a *psync.Applier, typ agmasync.EntityType, localID string) store.SyncRow {
	t.Helper()
	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(typ, localID)
		return err
	}); err != nil {
		t.Fatalf("reading the sync row for %s %q: %v", typ, localID, err)
	}
	return row
}

func status(t *testing.T, a *psync.Applier) oapi.InitialLoadStatus {
	t.Helper()
	s, err := a.Endpoint.InitialLoadStatus(context.Background())
	if err != nil {
		t.Fatalf("reading the initial load status: %v", err)
	}
	return s
}

func TestLoadRunsTheStatesThroughToCompleted(t *testing.T) {
	h := newHarness(t)
	contributed(t, h, "Hof Nord")
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	// The endpoint starts empty, so everything in the set is new to it and
	// everything it creates has to be bound before it may send.
	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if res.State != agmasync.StateCompleted {
		t.Errorf("state = %q, want COMPLETED", res.State)
	}
	if res.Repeat {
		t.Error("a first load must not report itself a repeat")
	}
	if res.Received != 1 || res.Created != 1 {
		t.Errorf("received %d, created %d, want one of each", res.Received, res.Created)
	}
	if len(res.Confirmed) != 1 || len(res.Rejected) != 0 {
		t.Errorf("confirmed %d bindings and had %d rejected, want 1 and 0",
			len(res.Confirmed), len(res.Rejected))
	}

	row := syncRow(t, b, agmasync.TypeFarm, res.Confirmed[0].LocalId)
	if !row.Bound() {
		t.Fatal("the created record holds no canonical identifier")
	}

	// Having completed, the endpoint may send that record like any other: the
	// binding it confirmed is what makes the write resolve rather than mint a
	// second canonical object.
	if _, err := b.Send(context.Background(), agmasync.TypeFarm, row.LocalID); err != nil {
		t.Fatalf("sending a record bound by the load: %v", err)
	}

	final := status(t, b)
	if final.PreviousLoadCompletedAt == nil {
		t.Error("a completed load must leave the marker that says it happened")
	}
}

func TestResponseEndingIsNotProofTheSetArrived(t *testing.T) {
	// The failure the state machine exists for: a set that stops arriving ends
	// the response exactly as a complete one does. Only the endpoint's state
	// tells them apart, so an endpoint that finds itself still at
	// LOADING_FROM_AGRIROUTER takes the set again.
	h := newHarness(t)
	a := contributed(t, h, "Hof Nord")
	createLocalFarm(t, a, "FRM-2", "Hof Süd")
	if _, err := a.Send(context.Background(), agmasync.TypeFarm, "FRM-2"); err != nil {
		t.Fatalf("contributing a second farm: %v", err)
	}

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	if err := h.router.DropNextInitialLoad("ep-b"); err != nil {
		t.Fatalf("arming the drop: %v", err)
	}

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want the set taken again after it was cut short", res.Attempts)
	}
	if res.Received != 2 {
		t.Errorf("received %d objects on the successful take, want 2", res.Received)
	}
	if res.State != agmasync.StateCompleted {
		t.Errorf("state = %q, want COMPLETED", res.State)
	}

	// The half set applied on the dropped attempt is not applied twice: the
	// second take matches it through the mapping the first one wrote.
	if res.Created > 2 {
		t.Errorf("created %d records for 2 canonical objects", res.Created)
	}
}

func TestGivingUpOnTheSetReportsTheFailureUnderneathIt(t *testing.T) {
	// The other shape of failed take: one that recurs. A dropped connection is
	// survived by taking the set again, so it is right that it is not an error —
	// but a stream that fails the same way every time is not survived, and the
	// attempt count on its own says only that the set never arrived. What the
	// caller needs is what went wrong, so the reason the last take came up short
	// is carried out to where the attempts run out.
	h := newHarness(t)
	contributed(t, h, "Hof Nord")

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	if err := h.router.CorruptInitialLoad("ep-b", true); err != nil {
		t.Fatalf("arming the corruption: %v", err)
	}

	l := loader(b)
	l.Attempts = 2

	res, err := l.Run(context.Background())
	if err == nil {
		t.Fatal("initial load succeeded, want it given up on after every take failed")
	}
	if res.Attempts != 2 {
		t.Errorf("attempts = %d, want the set taken twice before giving up", res.Attempts)
	}
	if !strings.Contains(err.Error(), "decoding MASTERDATA_CHANGED frame") {
		t.Errorf("error = %v, want it to carry the stream failure behind the attempts", err)
	}
}

func TestRecognisedRecordIsMatchedRatherThanDuplicated(t *testing.T) {
	// The endpoint holds the same farm already, under its own identifier, and
	// agrirouter has no mapping for it. Recognising it is what keeps the load
	// from creating a second local copy — and from offering that copy back as an
	// object the SSOT does not have.
	h := newHarness(t)
	contributed(t, h, "Hof Nord")

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	createLocalFarm(t, b, "B-77", "Hof Nord")

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if res.Matched != 1 || res.Created != 0 {
		t.Errorf("matched %d and created %d, want the record recognised", res.Matched, res.Created)
	}
	if res.Sent != 0 {
		t.Errorf("sent %d records, want none — the set already held it", res.Sent)
	}

	row := syncRow(t, b, agmasync.TypeFarm, "B-77")
	if !row.Bound() {
		t.Fatal("a recognised record must be bound, or it is offered back as new later")
	}

	var ids []string
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		ids, err = tx.LocalIDs(agmasync.TypeFarm)
		return err
	}); err != nil {
		t.Fatalf("listing farms: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("the platform holds %d farms, want the one it started with: %v", len(ids), ids)
	}
}

func TestRecordsTheSetDidNotContainAreOfferedBack(t *testing.T) {
	h := newHarness(t)
	contributed(t, h, "Hof Nord")

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	createLocalFarm(t, b, "B-99", "Hof West")

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if res.Sent != 1 {
		t.Errorf("sent %d records, want the one the canonical set did not contain", res.Sent)
	}

	row := syncRow(t, b, agmasync.TypeFarm, "B-99")
	if !row.Bound() || row.Revision == nil {
		t.Fatal("a record offered in the push holds the canonical identifier it was given")
	}

	// And the other participant now receives it, which is what "sending what the
	// SSOT does not have" is for.
	delivered := deliverTo(t, h, "fmis-a")
	found := false
	for _, ev := range delivered {
		env := ev.Envelope
		if env.AgrirouterId != nil && *env.AgrirouterId == *row.AgrirouterID {
			found = true
		}
	}
	if !found {
		t.Error("the pushed record never reached the other participant")
	}
}

func TestRepeatLoadIsRecognisedAndMatchesRatherThanReconciles(t *testing.T) {
	// Opting into a further entity type restarts the load. The mapping survives,
	// so the same objects arrive carrying this endpoint's own localId and match
	// rather than reconcile — and previousLoadCompletedAt is what says so before
	// a single object has been applied.
	h := newHarness(t)
	contributed(t, h, "Hof Nord")
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	first, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if first.Repeat {
		t.Fatal("the first load reported itself a repeat")
	}

	if err := h.router.OptIn("ep-b", agmasync.TypeFarm, agmasync.TypeField); err != nil {
		t.Fatalf("opting into a further type: %v", err)
	}

	second, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("repeat load: %v", err)
	}
	if !second.Repeat {
		t.Error("a load after a completed one must be recognised as a repeat")
	}
	if second.Created != 0 || second.Matched != 0 {
		t.Errorf("created %d and matched %d, want the set to arrive already matched",
			second.Created, second.Matched)
	}

	var ids []string
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		ids, err = tx.LocalIDs(agmasync.TypeFarm)
		return err
	}); err != nil {
		t.Fatalf("listing farms: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("the repeat load left %d farms, want no duplicates: %v", len(ids), ids)
	}
}

func TestRecognisedDeactivatedObjectIsBoundAndMarkedInactive(t *testing.T) {
	// A deactivated object is part of the set, and binding it is what keeps the
	// endpoint from offering its own copy back as new — which would resurrect,
	// for every other participant, something a user archived.
	h := newHarness(t)
	a := contributed(t, h, "Hof Nord")
	if _, err := a.Deactivate(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("deactivating: %v", err)
	}

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	createLocalFarm(t, b, "B-77", "hof nord")

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if res.Matched != 1 {
		t.Errorf("matched %d, want the deactivated object recognised", res.Matched)
	}
	if res.Sent != 0 {
		t.Error("a recognised deactivated object must not be offered back as new")
	}

	row := syncRow(t, b, agmasync.TypeFarm, "B-77")
	if !row.Bound() {
		t.Fatal("a recognised deactivated object is bound like any other")
	}

	var record store.Record
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		record, err = tx.LoadRecord(agmasync.TypeFarm, "B-77")
		return err
	}); err != nil {
		t.Fatalf("loading the record: %v", err)
	}
	if !record.Archived {
		t.Error("the endpoint's own copy must be marked inactive")
	}
	// Marked inactive, not replaced. What the set says about a deactivated
	// entity is that it is gone; overwriting the user's own archived record with
	// the canonical attributes would be churn with nothing behind it.
	if string(record.Modelled["name"]) != `"hof nord"` {
		t.Errorf("name = %s, want the endpoint's own copy left as it was",
			record.Modelled["name"])
	}
}

func TestUnrecognisedDeactivatedObjectIsIgnored(t *testing.T) {
	// The other half of the rule: nothing is created for an inactive object the
	// endpoint does not hold. "Absent localId means create it locally and bind"
	// is about objects the endpoint is expected to hold.
	h := newHarness(t)
	a := contributed(t, h, "Hof Nord")
	if _, err := a.Deactivate(context.Background(), agmasync.TypeFarm, "FRM-1"); err != nil {
		t.Fatalf("deactivating: %v", err)
	}

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if res.Ignored != 1 || res.Created != 0 {
		t.Errorf("ignored %d and created %d, want the object ignored", res.Ignored, res.Created)
	}

	var ids []string
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		ids, err = tx.LocalIDs(agmasync.TypeFarm)
		return err
	}); err != nil {
		t.Fatalf("listing farms: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("records were created for an unrecognised deactivated object: %v", ids)
	}
}

func TestAmbiguousMatchStopsTheLoadForAPersonAndResumesOnceTheyAnswer(t *testing.T) {
	// Two of the endpoint's records answer to one canonical object: the n:1
	// granularity mismatch the protocol pushes back to the participating
	// systems. It matches nothing, and the endpoint says a person is needed
	// without saying what for.
	//
	// What it must not do is carry on. Confirming would say reconciliation is
	// finished and clear the flag it just raised, and completing would have
	// agrirouter showing this endpoint as in sync while the user has decided
	// nothing.
	h := newHarness(t)
	contributed(t, h, "Hof Nord")

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	createLocalFarm(t, b, "B-1", "Hof Nord")
	createLocalFarm(t, b, "B-2", "Hof Nord")

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if !res.AwaitingUser {
		t.Error("an ambiguous match must report that a user is needed")
	}
	if len(res.Blocked) != 1 || res.Blocked[0].Type != agmasync.TypeFarm {
		t.Fatalf("blocked %+v, want the one object nobody could decide", res.Blocked)
	}
	if res.State != agmasync.StateReconciling {
		t.Errorf("state = %q, want the load left short of reconciled", res.State)
	}

	// Raised as the object that needed a person was applied. Which state it was
	// raised in is not asserted, and could not be: agrirouter may finish sending
	// the set while the report is in flight. That the report does not have to
	// care is the point of it naming no state.
	raised := false
	for _, obs := range h.router.Observations() {
		if obs.Kind == "userAttention" {
			raised = true
		}
	}
	if !raised {
		t.Error("agrirouter was never told a user was needed")
	}
	if waiting := status(t, b); waiting.AwaitingUser == nil || !*waiting.AwaitingUser {
		t.Error("the flag must stand while the user still owes an answer")
	}

	// No third farm. Answering an object it could not identify with a copy of it
	// is the failure the blocked answer exists to prevent.
	var ids []string
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		var err error
		ids, err = tx.LocalIDs(agmasync.TypeFarm)
		return err
	}); err != nil {
		t.Fatalf("listing farms: %v", err)
	}
	if !slices.Equal(ids, []string{"B-1", "B-2"}) {
		t.Errorf("farms = %v, want the endpoint's own two and nothing invented", ids)
	}

	// The user answers: B-1 is that farm and B-2 was a duplicate of it. The
	// platform binds its choice, at both ends, and lets go of the other record.
	blocked := res.Blocked[0].AgrirouterID
	if err := b.Bind(context.Background(), agmasync.TypeFarm, "B-1", blocked); err != nil {
		t.Fatalf("binding the user's choice: %v", err)
	}
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		if err := tx.PutSyncRow(store.SyncRow{
			EntityType: agmasync.TypeFarm, LocalID: "B-1", AgrirouterID: &blocked,
		}); err != nil {
			return err
		}
		return tx.DeleteRecord(agmasync.TypeFarm, "B-2")
	}); err != nil {
		t.Fatalf("recording the user's choice: %v", err)
	}

	// Running the load again picks up where it stopped: the set is not sent
	// twice, the endpoint being past LOADING_FROM_AGRIROUTER, and what remains is
	// the confirmation and the completion.
	resumed, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("resuming the load: %v", err)
	}
	if len(resumed.Blocked) != 0 {
		t.Errorf("blocked %+v on the resumed run, want nothing outstanding", resumed.Blocked)
	}
	if resumed.State != agmasync.StateCompleted {
		t.Fatalf("state = %q, want COMPLETED once the user had answered", resumed.State)
	}
	if final := status(t, b); final.AwaitingUser != nil && *final.AwaitingUser {
		t.Error("completing the load must leave the flag cleared")
	}
}

func TestRejectionIsResolvedAgainstTheWholePairNotTheLocalIdAlone(t *testing.T) {
	// A rejection names a pair, not an entity type, so the type has to be found
	// from the platform's own rows. A local identifier alone does not find it:
	// identifiers are minted per type, so a farm and an organization may hold the
	// same one, and resolving on it picks whichever row was seen last. The
	// innocent record then loses a binding agrirouter is still holding, and the
	// refused one keeps a claim it has no right to.
	h := newHarness(t)
	contributed(t, h, "Hof Nord")
	b := h.join("fmis-b", "ep-b",
		agmasync.DependencyClosure([]agmasync.EntityType{agmasync.TypeFarm})...)

	delivered := deliverTo(t, h, "fmis-b")
	if len(delivered) == 0 {
		t.Fatal("the endpoint received nothing to create a record from")
	}
	outcome, err := b.Apply(delivered[0].Entity, delivered[0].ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The collision: an organization of this platform's own, under the identifier
	// the farm above already holds, bound in good standing.
	shared := outcome.LocalID
	if err := b.Store.Tx(b.Tenant, func(tx *store.Tx) error {
		return tx.UpsertRecord(store.Record{
			EntityType: agmasync.TypeOrganization,
			LocalID:    shared,
			Modelled:   map[string]json.RawMessage{"name": mustJSON(t, "Genossenschaft Nord")},
		}, shared)
	}); err != nil {
		t.Fatalf("seeding the organization: %v", err)
	}
	if _, err := b.Send(context.Background(), agmasync.TypeOrganization, shared); err != nil {
		t.Fatalf("binding the organization: %v", err)
	}

	// And the rejection, on the farm's pair only.
	row := syncRow(t, b, agmasync.TypeFarm, shared)
	if err := b.Bind(
		context.Background(), agmasync.TypeFarm, "B-OTHER", *row.AgrirouterID,
	); err != nil {
		t.Fatalf("binding under another identifier: %v", err)
	}

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].LocalId != shared {
		t.Fatalf("rejected %+v, want the farm's pair alone", res.Rejected)
	}

	if syncRow(t, b, agmasync.TypeFarm, shared).Bound() {
		t.Error("the farm's claim was refused and must not be kept")
	}
	if !syncRow(t, b, agmasync.TypeOrganization, shared).Bound() {
		t.Error("the organization's binding stands and must survive another type's rejection")
	}
}

func TestRejectedBindingIsDroppedLocallyAndNotSentAsNew(t *testing.T) {
	// A pair agrirouter will not record leaves the endpoint holding a claim it
	// has to give up: agrirouter's mapping is what a send resolves through. The
	// record is then neither bound nor pushed, because pushing it would mint a
	// second canonical object for an entity that already has one.
	//
	// The two tables drift apart here the way a partial failure leaves them: the
	// endpoint created a record for a delivered object and then told agrirouter
	// it calls that object something else. Nothing reports the drift until the
	// confirmation, which is what a per-pair rejection is for.
	h := newHarness(t)
	contributed(t, h, "Hof Nord")
	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)

	delivered := deliverTo(t, h, "fmis-b")
	if len(delivered) == 0 {
		t.Fatal("the endpoint received nothing to create a record from")
	}
	outcome, err := b.Apply(delivered[0].Entity, delivered[0].ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	row := syncRow(t, b, agmasync.TypeFarm, outcome.LocalID)
	if err := b.Bind(
		context.Background(), agmasync.TypeFarm, "B-OTHER", *row.AgrirouterID,
	); err != nil {
		t.Fatalf("binding under another identifier: %v", err)
	}

	res, err := loader(b).Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("rejected %d bindings, want the one that could not stand: %+v", len(res.Rejected),
			res.Rejected)
	}
	rejection := res.Rejected[0]
	if rejection.Reason != agmasync.ReasonAgrirouterIDAlreadyBound {
		t.Errorf("reason = %q, want the canonical object reported as already known here",
			rejection.Reason)
	}
	if agmasync.NeedsUser(rejection) {
		t.Error("this cause is the endpoint's own to resolve and must not reach a user")
	}
	if rejection.ExistingMapping == nil {
		t.Fatal("a rejection must name the mapping that stands, or nothing can be resolved")
	}
	if rejection.ExistingMapping.LocalId != "B-OTHER" {
		t.Errorf("the mapping that stands is %q, want the pair agrirouter actually holds",
			rejection.ExistingMapping.LocalId)
	}

	after := syncRow(t, b, agmasync.TypeFarm, outcome.LocalID)
	if after.Bound() {
		t.Error("a refused claim must not be kept locally")
	}
	if res.Sent != 0 {
		t.Error("a record whose binding was rejected must not be sent as new")
	}
}

// asksAPerson is a recogniser that does what a product's does: it puts the
// object in front of somebody and waits. What it records is what agrirouter
// showed while it was waiting, which is the thing a flag raised off the
// returned Recognition cannot get right.
type asksAPerson struct {
	attention *psync.Attention
	endpoint  *agmasync.Endpoint

	asked  int
	flagUp bool
}

func (a *asksAPerson) Recognise(
	tx *store.Tx, env agmasync.Envelope, entity oapi.Entity,
) (psync.Recognition, error) {
	a.asked++
	a.attention.Raise(context.Background())

	// Read with the question still open and unanswered — the interval the flag
	// exists to describe.
	status, err := a.endpoint.InitialLoadStatus(context.Background())
	if err != nil {
		return psync.Recognition{}, err
	}
	if status.AwaitingUser != nil && *status.AwaitingUser {
		a.flagUp = true
	}

	// Nobody answered, so the object is left for whoever does.
	return psync.Recognition{Blocked: true, AwaitingUser: true}, nil
}

func TestTheFlagIsUpWhileTheQuestionIsOpenAndNotOnlyOnceItIsAnswered(t *testing.T) {
	// A recogniser that asks a person is waiting from the moment it asks, and
	// has returned nothing yet. Raising the flag from what recognition returns
	// puts agrirouter's "waiting for you in <app>" up when the waiting ends —
	// and, for a recogniser that gives up on a timeout, only once that timeout
	// has run. The endpoint shares the flag with the loader instead and raises
	// it when it asks.
	h := newHarness(t)
	contributed(t, h, "Hof Nord")

	b := h.join("fmis-b", "ep-b", agmasync.TypeFarm)
	createLocalFarm(t, b, "B-1", "Hof Nord GmbH")

	attention := &psync.Attention{}
	asking := &asksAPerson{attention: attention, endpoint: b.Endpoint}
	l := loader(b)
	l.Reconciler, l.Attention = asking, attention

	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	if asking.asked == 0 {
		t.Fatal("the recogniser was never asked, so the test proves nothing")
	}
	if !asking.flagUp {
		t.Error("agrirouter still showed nobody waiting while the question was open")
	}

	// And what the load reports is what was raised, including on the ending this
	// recogniser produces: stopped at RECONCILING with the object undecided.
	if !res.AwaitingUser {
		t.Error("the load must report the flag its recogniser raised")
	}
	if res.UserAttentionErr != nil {
		t.Errorf("reporting a person was needed failed: %v", res.UserAttentionErr)
	}
	if len(res.Blocked) != 1 {
		t.Fatalf("blocked %+v, want the object left for a person", res.Blocked)
	}
	if res.State != agmasync.StateReconciling {
		t.Errorf("state = %q, want the load left short of reconciled", res.State)
	}

	// One report, however many questions were asked, and none repeated on a
	// later take of the set.
	reports := 0
	for _, obs := range h.router.Observations() {
		if obs.Kind == "userAttention" {
			reports++
		}
	}
	if reports != 1 {
		t.Errorf("agrirouter was told %d times, want one report for the load", reports)
	}
}
