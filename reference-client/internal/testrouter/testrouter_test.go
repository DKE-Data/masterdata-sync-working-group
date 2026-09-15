package testrouter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter"
	"github.com/google/uuid"
)

// fixture is one router with one tenant, serving two participants that each
// hold an endpoint there — the smallest setting in which origin suppression and
// the identifier mapping mean anything.
type fixture struct {
	t      *testing.T
	router *testrouter.Router
	server *httptest.Server
	tenant uuid.UUID
}

type participant struct {
	client   *agmasync.Client
	endpoint *agmasync.Endpoint
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	r := testrouter.New()
	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)

	return &fixture{t: t, router: r, server: srv, tenant: r.AddTenant()}
}

// join creates an endpoint for a participant and opts it in, which is what
// starts its initial load. Opt-in is a user's decision made in agrirouter, so it
// goes through the control plane rather than through the client.
func (f *fixture) join(appID, externalID string, types ...agmasync.EntityType) *participant {
	f.t.Helper()

	endpointID := f.router.AddEndpoint(appID, f.tenant, externalID)
	if err := f.router.OptIn(externalID, types...); err != nil {
		f.t.Fatalf("opt in %s: %v", externalID, err)
	}

	client, err := agmasync.NewClient(f.server.URL, agmasync.WithBearerToken(appID))
	if err != nil {
		f.t.Fatalf("building client: %v", err)
	}
	return &participant{client: client, endpoint: client.For(endpointID, externalID)}
}

func farm(localID, name string) oapi.Entity {
	ent, err := agmasync.FromFarm(oapi.Farm{LocalId: &localID, Name: name})
	if err != nil {
		panic(err)
	}
	return ent
}

func revisionOf(t *testing.T, ent oapi.Entity) int {
	t.Helper()
	env, err := agmasync.EnvelopeOf(ent)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}
	if env.Revision == nil {
		t.Fatal("delivered object carries no revision")
	}
	return *env.Revision
}

func TestCreateAssignsAgrirouterIDAndFirstRevision(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	got, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	env, err := agmasync.EnvelopeOf(got)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}
	if env.AgrirouterId == nil {
		t.Error("a created object must come back carrying its assigned agrirouterId")
	}
	if env.Revision == nil || *env.Revision != 1 {
		t.Errorf("revision = %v, want 1", env.Revision)
	}
	if env.LocalId == nil || *env.LocalId != "FRM-1" {
		t.Errorf("localId = %v, want the sender's own", env.LocalId)
	}
	if env.TenantId == nil || *env.TenantId != f.tenant {
		t.Errorf("tenantId = %v, want %s", env.TenantId, f.tenant)
	}
}

func TestUpdateWithoutBaseIsRejected(t *testing.T) {
	// Omitting the base would opt a participant out of concurrency control, and
	// the integrations least able to surface a conflict are the likeliest to
	// omit it, so agrirouter refuses rather than assuming.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	if _, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Süd"), nil)
	if !errors.Is(err, agmasync.ErrBaseRevisionRequired) {
		t.Errorf("error = %v, want ErrBaseRevisionRequired", err)
	}
}

func TestNoOpWriteProducesNoRevision(t *testing.T) {
	// A payload equal to the current canonical value succeeds whatever the
	// base. This is what makes retrying a write whose outcome was never
	// observed safe, and it is what breaks the echo loop between two endpoints
	// backed by one store.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	created, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before := revisionOf(t, created)

	again, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("no-op write should succeed without a base: %v", err)
	}
	if after := revisionOf(t, again); after != before {
		t.Errorf("revision moved from %d to %d on a no-op write", before, after)
	}
}

func TestStaleBaseMergesWhereChangesDoNotOverlap(t *testing.T) {
	// Two participants editing different attributes of one entity both succeed,
	// and the second gets back content it did not send — which is why a write
	// response has to be applied like a delivered object rather than treated as
	// an acknowledgement.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	created, err := a.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agmasync.EnvelopeOf(created)
	base := *env.Revision

	// B recognises the object as one it holds and binds its own identifier.
	if err := b.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-FARM-9", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// A renames it, moving the canonical revision on.
	renamed := oapi.Farm{LocalId: strptr("FRM-1"), Name: "Hof Süd"}
	entity, _ := agmasync.FromFarm(renamed)
	if _, err := a.endpoint.Put(context.Background(), entity, &base); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// B, still on the old base, changes a different attribute.
	address := oapi.Address{City: strptr("Rostock")}
	withAddress, _ := agmasync.FromFarm(oapi.Farm{
		LocalId: strptr("B-FARM-9"), Name: "Hof Nord", Address: &address,
	})
	merged, err := b.endpoint.Put(context.Background(), withAddress, &base)
	if err != nil {
		t.Fatalf("non-overlapping write should merge, got: %v", err)
	}

	mergedFarm, err := merged.AsFarm()
	if err != nil {
		t.Fatalf("reading merged farm: %v", err)
	}
	if mergedFarm.Name != "Hof Süd" {
		t.Errorf("name = %q, want A's rename carried into the merged result", mergedFarm.Name)
	}
	if mergedFarm.Address == nil || mergedFarm.Address.City == nil ||
		*mergedFarm.Address.City != "Rostock" {
		t.Error("B's own change is missing from the merged result")
	}
	if rev := revisionOf(t, merged); rev != base+2 {
		t.Errorf("revision = %d, want %d — a merge is not base + 1", rev, base+2)
	}
}

func TestStaleBaseIsRejectedWhereChangesOverlap(t *testing.T) {
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	created, err := a.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agmasync.EnvelopeOf(created)
	base := *env.Revision

	if err := b.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-FARM-9", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	if _, err := a.endpoint.Put(context.Background(), farm("FRM-1", "Hof Süd"), &base); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// B renames the same attribute from the same stale base.
	_, err = b.endpoint.Put(context.Background(), farm("B-FARM-9", "Hof West"), &base)
	var conflict *agmasync.RevisionConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want a RevisionConflict", err)
	}
	if conflict.CurrentRevision != base+1 {
		t.Errorf("CurrentRevision = %d, want %d so the participant can rebase",
			conflict.CurrentRevision, base+1)
	}
}

func TestBindingTheSameLocalIDTwiceIsRejectedWithItsCause(t *testing.T) {
	// The two mapping conflicts are different problems in the participant's own
	// store, and the specification requires the cause to be machine-readable
	// because the handling differs: one needs a user, the other does not.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	first, err := a.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := a.endpoint.Put(context.Background(), farm("FRM-2", "Hof Süd"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	firstEnv, _ := agmasync.EnvelopeOf(first)
	secondEnv, _ := agmasync.EnvelopeOf(second)

	if err := b.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-1", *firstEnv.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Claiming a second canonical object under an identifier that already
	// denotes one: a genuine granularity disagreement, which needs a user.
	err = b.endpoint.Bind(context.Background(), agmasync.TypeFarm, "B-1", *secondEnv.AgrirouterId)
	var conflict *agmasync.MappingConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want a MappingConflict", err)
	}
	if conflict.Rejection.Reason != agmasync.ReasonLocalIDAlreadyBound {
		t.Errorf("reason = %q, want %q",
			conflict.Rejection.Reason, agmasync.ReasonLocalIDAlreadyBound)
	}
	if !agmasync.NeedsUser(conflict.Rejection) {
		t.Error("a local id claimed by two canonical objects needs a user")
	}
	if conflict.Rejection.ExistingMapping == nil {
		t.Error("the rejection must name the mapping that stands in the way")
	}

	// The mirror image: claiming an object the participant already knows under
	// another identifier. Resolvable without troubling anybody.
	err = b.endpoint.Bind(context.Background(), agmasync.TypeFarm, "B-2", *firstEnv.AgrirouterId)
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want a MappingConflict", err)
	}
	if conflict.Rejection.Reason != agmasync.ReasonAgrirouterIDAlreadyBound {
		t.Errorf("reason = %q, want %q",
			conflict.Rejection.Reason, agmasync.ReasonAgrirouterIDAlreadyBound)
	}
	if agmasync.NeedsUser(conflict.Rejection) {
		t.Error("an object already held under another identifier must not reach a user")
	}
}

func TestDeactivationIsIdempotentAndIgnoresBaseOnceInactive(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	created, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	base := revisionOf(t, created)

	first, err := p.endpoint.Deactivate(context.Background(), agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	firstEnv, _ := agmasync.EnvelopeOf(first)
	if firstEnv.Active == nil || *firstEnv.Active {
		t.Error("the object should be inactive after deactivation")
	}

	// Repeating it, with a base that is now stale, must still succeed and must
	// not produce a revision — that is what makes a retry safe.
	stale := base
	again, err := p.endpoint.Deactivate(context.Background(), agmasync.TypeFarm, "FRM-1", &stale)
	if err != nil {
		t.Fatalf("deactivating an inactive entity must succeed: %v", err)
	}
	if revisionOf(t, again) != revisionOf(t, first) {
		t.Error("deactivating an already-inactive entity produced a new revision")
	}
}

func TestUnresolvableReferenceIsRejected(t *testing.T) {
	// A reference carrying a localId is resolved against the sender's own
	// mapping. If the target has not been sent yet it does not resolve, and the
	// write is rejected rather than accepted with a dangling reference.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeField)

	field, err := agmasync.FromField(oapi.Field{
		LocalId: strptr("PFD-1"),
		Name:    "North 40",
		Farm:    &oapi.EntityReference{LocalId: strptr("FRM-NOT-SENT-YET")},
	})
	if err != nil {
		t.Fatalf("building field: %v", err)
	}

	if _, err := p.endpoint.Put(context.Background(), field, nil); !errors.Is(
		err, agmasync.ErrValidation,
	) {
		t.Errorf("error = %v, want ErrValidation for an unresolvable reference", err)
	}
}

func TestInitialLoadTransitionsAndRejectsGoingBackwards(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	status, err := p.endpoint.InitialLoadStatus(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != agmasync.StateLoadingFromAgrirouter {
		t.Fatalf("state = %q, want %q after opt-in",
			status.State, agmasync.StateLoadingFromAgrirouter)
	}
	if agmasync.IsRepeatLoad(status) {
		t.Error("a first load must not be marked a repeat")
	}

	// Confirming before the set has been sent is a conflict: an endpoint cannot
	// have reconciled a set it has not finished receiving.
	if _, err := p.endpoint.ConfirmReconciled(context.Background(), nil); !errors.Is(
		err, agmasync.ErrInitialLoadConflict,
	) {
		t.Errorf("error = %v, want ErrInitialLoadConflict", err)
	}

	// Take the set, which is what moves the endpoint on.
	stream, err := p.endpoint.InitialLoadEvents(context.Background())
	if err != nil {
		t.Fatalf("initial load stream: %v", err)
	}
	for range stream.Events() {
	}
	stream.Close()

	status, err = p.endpoint.InitialLoadStatus(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != agmasync.StateReconciling {
		t.Errorf("state = %q, want %q once the set has been sent",
			status.State, agmasync.StateReconciling)
	}

	if _, err := p.endpoint.ConfirmReconciled(context.Background(), nil); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	final, err := p.endpoint.CompleteInitialLoad(context.Background())
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if final.State != agmasync.StateCompleted {
		t.Errorf("state = %q, want %q", final.State, agmasync.StateCompleted)
	}
	if !agmasync.IsRepeatLoad(final) {
		t.Error("previousLoadCompletedAt should be set once a load completes")
	}
}

func TestAskingForTheSetAgainIsRejected(t *testing.T) {
	// There is no operation for asking that the canonical set be sent again.
	// Initial load follows from opt-in, which is a user's decision, and a
	// participant that could start its own would land in RECONCILING in front
	// of nobody.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	_, err := p.endpoint.SetInitialLoadState(context.Background(), oapi.InitialLoadStateUpdate{
		State: agmasync.StateLoadingFromAgrirouter,
	})
	if !errors.Is(err, agmasync.ErrInitialLoadConflict) {
		t.Errorf("error = %v, want ErrInitialLoadConflict", err)
	}
}

func TestAwaitingUserCanBeRaisedWhileTheSetIsStillArriving(t *testing.T) {
	// Conflicts surface object by object, so an endpoint has to be able to say a
	// person is needed before the set has finished arriving — when it has no
	// state of its own to name and agrirouter may advance it at any moment.
	// Naming none is what makes that safe.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	status, err := p.endpoint.ReportUserAttention(context.Background())
	if err != nil {
		t.Fatalf("raising awaitingUser while the set arrives: %v", err)
	}
	if status.AwaitingUser == nil || !*status.AwaitingUser {
		t.Error("the flag was not raised")
	}
	if status.State != agmasync.StateLoadingFromAgrirouter {
		t.Errorf("state = %q, want the endpoint left where it was", status.State)
	}

	// Raising it again while it is already raised is not a conflict either.
	if _, err := p.endpoint.ReportUserAttention(context.Background()); err != nil {
		t.Errorf("repeating the report: %v", err)
	}

	// And agrirouter clears it, on the endpoint-driven transitions. Sending the
	// set does not: that says agrirouter has finished, not that the user has.
	stream, err := p.endpoint.InitialLoadEvents(context.Background())
	if err != nil {
		t.Fatalf("initial load stream: %v", err)
	}
	for range stream.Events() {
	}
	stream.Close()

	status, err = p.endpoint.InitialLoadStatus(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.AwaitingUser == nil || !*status.AwaitingUser {
		t.Error("the step to RECONCILING must not clear the flag")
	}

	status, err = p.endpoint.ConfirmReconciled(context.Background(), nil)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if status.AwaitingUser != nil && *status.AwaitingUser {
		t.Error("confirming reconciliation clears the flag")
	}
}

func TestAwaitingUserIsRefusedOnceTheLoadIsCompleted(t *testing.T) {
	// The one refusal, and it is about the load rather than about the report:
	// a completed load is waiting on nobody, so there is nothing for agrirouter
	// to show. Steady state has its own ways of surfacing a conflict.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	stream, err := p.endpoint.InitialLoadEvents(context.Background())
	if err != nil {
		t.Fatalf("initial load stream: %v", err)
	}
	for range stream.Events() {
	}
	stream.Close()

	if _, err := p.endpoint.ConfirmReconciled(context.Background(), nil); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := p.endpoint.CompleteInitialLoad(context.Background()); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if _, err := p.endpoint.ReportUserAttention(
		context.Background(),
	); !errors.Is(err, agmasync.ErrInitialLoadConflict) {
		t.Errorf("error = %v, want ErrInitialLoadConflict once the load is done", err)
	}
}

func strptr(s string) *string { return &s }

func TestAnOldBaseStillMergesRatherThanAgeingOut(t *testing.T) {
	// Merge bases are retained without a horizon, so a participant that has been
	// away across many revisions is merged against rather than refused. A `412`
	// therefore always means the changes genuinely overlapped, never that
	// agrirouter has forgotten what the base was.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	created, err := a.endpoint.Put(context.Background(), farm("FRM-1", "Hof 0"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agmasync.EnvelopeOf(created)
	longAgo := *env.Revision

	if err := b.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-1", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// A renames the farm many times over, moving the canonical revision far
	// beyond the base B is holding.
	base := longAgo
	for i := 1; i <= 50; i++ {
		name := fmt.Sprintf("Hof %d", i)
		updated, err := a.endpoint.Put(context.Background(), farm("FRM-1", name), &base)
		if err != nil {
			t.Fatalf("rename %d: %v", i, err)
		}
		base = revisionOf(t, updated)
	}

	// B writes from the revision it saw at the very beginning, touching an
	// attribute nobody else has.
	address := oapi.Address{City: strptr("Rostock")}
	entity, _ := agmasync.FromFarm(oapi.Farm{
		LocalId: strptr("B-1"), Name: "Hof 0", Address: &address,
	})
	merged, err := b.endpoint.Put(context.Background(), entity, &longAgo)
	if err != nil {
		t.Fatalf("a base 50 revisions old must still merge, got: %v", err)
	}

	mergedFarm, err := merged.AsFarm()
	if err != nil {
		t.Fatalf("reading merged farm: %v", err)
	}
	if mergedFarm.Name != "Hof 50" {
		t.Errorf("name = %q, want the intervening renames preserved", mergedFarm.Name)
	}
	if mergedFarm.Address == nil || mergedFarm.Address.City == nil {
		t.Error("B's own change is missing from the merged result")
	}
}

func TestTwoEndpointsOfOneApplicationShareOneNamespace(t *testing.T) {
	// The mapping is keyed by the application, not by the endpoint. A product
	// that holds two organizations onboards each as its own endpoint over one
	// store, so the same local identifier in each names the same record — and
	// the second organization's send resolves to the canonical object the first
	// created rather than minting a second for one record.
	f := newFixture(t)
	org1 := f.join("fmis-a", "ep-a1", agmasync.TypeFarm)
	org2 := f.join("fmis-a", "ep-a2", agmasync.TypeFarm)

	first, err := org1.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create in the first organization: %v", err)
	}
	firstEnv, _ := agmasync.EnvelopeOf(first)

	// It resolves, so it is an update — and an update without the revision it
	// was made from is refused. That refusal is itself the evidence: a send
	// that resolved to nothing would have been accepted as a create.
	if _, err := org2.endpoint.Put(
		context.Background(), farm("FRM-1", "Hof Süd"), nil,
	); !errors.Is(err, agmasync.ErrBaseRevisionRequired) {
		t.Fatalf("err = %v, want the send from the sibling to resolve and demand a base", err)
	}

	base := *firstEnv.Revision
	second, err := org2.endpoint.Put(context.Background(), farm("FRM-1", "Hof Süd"), &base)
	if err != nil {
		t.Fatalf("update from the second organization: %v", err)
	}
	secondEnv, _ := agmasync.EnvelopeOf(second)

	if *firstEnv.AgrirouterId != *secondEnv.AgrirouterId {
		t.Error("the same localId sent by two endpoints of one application is one object")
	}
	if *secondEnv.Revision != base+1 {
		t.Errorf("revision = %d, want %d: the sibling updated rather than created",
			*secondEnv.Revision, base+1)
	}
}

func TestBindingIsPerApplicationRatherThanPerEndpoint(t *testing.T) {
	// A participant that keeps one store behind several endpoints binds once
	// for the store, not once per endpoint. The binding one endpoint declares
	// is the participant's, so a sibling's send resolves through it without
	// binding again — and binding the same pair again is idempotent rather than
	// a second, separate claim.
	f := newFixture(t)
	sender := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	org1 := f.join("fmis-b", "ep-b1", agmasync.TypeFarm)
	org2 := f.join("fmis-b", "ep-b2", agmasync.TypeFarm)

	created, err := sender.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agmasync.EnvelopeOf(created)

	// The receiving participant binds once, through one of its endpoints.
	if err := org1.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "SHARED-1", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Repeating it through the sibling is the same claim about the same store,
	// and is recorded idempotently rather than rejected as a second binding.
	if err := org2.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "SHARED-1", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("re-declaring the participant's own binding must be idempotent: %v", err)
	}

	// The sibling that bound nothing of its own sends through the same pair.
	base := *env.Revision
	echoed, err := org2.endpoint.Put(context.Background(), farm("SHARED-1", "Hof Nord"), &base)
	if err != nil {
		t.Fatalf("re-emitting a bound object: %v", err)
	}
	echoedEnv, _ := agmasync.EnvelopeOf(echoed)
	if *echoedEnv.AgrirouterId != *env.AgrirouterId {
		t.Error("a sibling's send must resolve through the application's binding")
	}
	if *echoedEnv.Revision != base {
		t.Errorf("revision = %d, want %d: an equal payload is a no-op", *echoedEnv.Revision, base)
	}
}

func TestASiblingsSendResolvesRatherThanDuplicating(t *testing.T) {
	// What application-scoped keying buys. An endpoint re-emitting an object a
	// sibling holds sends the store's own identifier, which resolves, so the
	// no-op detection catches it and no second canonical object appears. Under
	// endpoint keying this send would have been an unresolved create.
	f := newFixture(t)
	org1 := f.join("fmis-a", "ep-a1", agmasync.TypeFarm)
	org2 := f.join("fmis-a", "ep-a2", agmasync.TypeFarm)

	created, err := org1.endpoint.Put(context.Background(), farm("SHARED-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agmasync.EnvelopeOf(created)

	echoed, err := org2.endpoint.Put(context.Background(), farm("SHARED-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("the sibling's send: %v", err)
	}
	echoedEnv, _ := agmasync.EnvelopeOf(echoed)
	if *echoedEnv.AgrirouterId != *env.AgrirouterId {
		t.Fatal("a sibling's send must not mint a second canonical object")
	}
	if *echoedEnv.Revision != *env.Revision {
		t.Errorf("revision = %d, want %d: an equal payload is a no-op",
			*echoedEnv.Revision, *env.Revision)
	}
}

func TestAnObjectTwoSiblingsAreEntitledToArrivesOnce(t *testing.T) {
	// Entitlement is decided per endpoint, delivery is not. A frame names no
	// endpoint, it is rendered in the application's namespace, and the
	// subscription it goes to is the application's — so two entitled siblings
	// would put two byte-identical frames on one connection. The receiver would
	// apply the second to no effect, which is not a reason to send it.
	f := newFixture(t)
	f.join("fmis-a", "ep-a1", agmasync.TypeFarm)
	f.join("fmis-a", "ep-a2", agmasync.TypeFarm)
	b := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	client, err := agmasync.NewClient(f.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listening from the live end, so what is counted is the fan-out of the one
	// write below rather than anything restated by catch-up.
	stream, err := client.Events(ctx, "")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer stream.Close()

	entities := make(chan string, 8)
	go func() {
		for ev, err := range stream.Events() {
			if err != nil {
				return
			}
			if ev.HasEntity() {
				entities <- ev.ID
			}
		}
	}()

	if _, err := b.endpoint.Put(ctx, farm("FRM-B", "Hof Ost"), nil); err != nil {
		t.Fatalf("the other participant's create: %v", err)
	}

	select {
	case <-entities:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the object to be delivered")
	}

	// The second frame is the failure, so the assertion is that the wait for it
	// runs out.
	select {
	case id := <-entities:
		t.Errorf("the object was delivered twice, second frame at position %s", id)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestRemovingAnEndpointLeavesTheMappingStanding(t *testing.T) {
	// The mapping belongs to the participant, so nothing about it hangs off one
	// endpoint. Type opt-out, hub disconnection and endpoint removal all leave
	// it standing: a participant that re-onboards finds its pairs intact and
	// resolves through them from the new endpoint, rather than reconciling a
	// store it never lost.
	f := newFixture(t)
	sender := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	receiver := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	created, err := sender.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agmasync.EnvelopeOf(created)

	if err := receiver.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-1", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := f.router.RemoveEndpoint("ep-b"); err != nil {
		t.Fatalf("removing the endpoint: %v", err)
	}

	// Re-onboarded under a new endpoint, the participant's binding is still
	// there: the same localId resolves, so the send is an update of the object
	// it bound and not a create.
	reonboarded := f.join("fmis-b", "ep-b-again", agmasync.TypeFarm)
	base := *env.Revision
	sent, err := reonboarded.endpoint.Put(context.Background(), farm("B-1", "Hof Süd"), &base)
	if err != nil {
		t.Fatalf("send after re-onboarding: %v", err)
	}
	sentEnv, _ := agmasync.EnvelopeOf(sent)
	if *sentEnv.AgrirouterId != *env.AgrirouterId {
		t.Error("a removed endpoint's binding belongs to the participant and must survive")
	}
}

func TestSelectingAnUndeclaredTypeIsRefused(t *testing.T) {
	// The two steps are separate and the declaration bounds the selection: a
	// user cannot opt an endpoint into a type its participant never said the
	// endpoint could exchange. Without this the two collapse into one and a
	// participant's own call decides what it is exposed to.
	f := newFixture(t)
	f.router.AddEndpoint("fmis-a", f.tenant, "ep-a")

	if err := f.router.Declare("ep-a", agmasync.TypeOrganization, agmasync.TypePerson); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if err := f.router.OptIn("ep-a", agmasync.TypeFarm); err == nil {
		t.Fatal("selecting an undeclared type succeeded, want refusal")
	}

	// The declared types are selectable, so the refusal is about the bound and
	// not about the call.
	if err := f.router.OptIn("ep-a", agmasync.TypeOrganization); err != nil {
		t.Fatalf("selecting a declared type: %v", err)
	}
}

func TestDeclaringEnablesNothingOnItsOwn(t *testing.T) {
	// Declaring is a statement about software. It creates no route and starts no
	// load: an endpoint declared for everything and selected for nothing has no
	// initial-load state at all.
	f := newFixture(t)
	endpointID := f.router.AddEndpoint("fmis-a", f.tenant, "ep-a")

	client, err := agmasync.NewClient(f.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	ep := client.For(endpointID, "ep-a")

	// The write echoes the declaration back in full — there is no resource to
	// read it from afterwards — and it says nothing about what is exchanged: it
	// is the endpoint's capability, not the user's choice.
	cfg, err := ep.Declare(context.Background(), agmasync.Declaration(agmasync.EntityTypes...))
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if len(cfg.Toggles) != len(agmasync.EntityTypes) {
		t.Errorf("declared %d types, want %d", len(cfg.Toggles), len(agmasync.EntityTypes))
	}

	if _, err := ep.InitialLoadStatus(context.Background()); !errors.Is(err, agmasync.ErrNotFound) {
		t.Errorf("initial load status = %v, want ErrNotFound", err)
	}
}

func TestRouteChangedStatesWhatTheEndpointExchanges(t *testing.T) {
	// The participant is not present when the user makes the selection, so the
	// stream carries ROUTE_CHANGED. It states the selection in full, so there is
	// nothing to go and read behind it.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := p.client.Events(ctx, "")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer stream.Close()
	next := nextSelection(t, stream, "ep-a")

	// Connecting restates what this endpoint already exchanges, the selection
	// having moved above the position this stream was opened from.
	onConnecting := next()
	want := []agmasync.EntityType{
		agmasync.TypeOrganization, agmasync.TypePerson, agmasync.TypeFarm,
	}
	if got := agmasync.SelectedTypes(*onConnecting); !slices.Equal(got, want) {
		t.Errorf("selection on connecting = %v, want %v", got, want)
	}

	if err := f.router.OptIn("ep-a", agmasync.TypeFarm, agmasync.TypeField); err != nil {
		t.Fatalf("widening the selection: %v", err)
	}

	frame := next()
	if frame.ExternalEndpointId != "ep-a" {
		t.Errorf("externalEndpointId = %q, want %q", frame.ExternalEndpointId, "ep-a")
	}
	if frame.EndpointId == uuid.Nil {
		t.Error("frame names no agrirouter endpoint id")
	}

	// The event type is repeated in the payload, as the other agrirouter event
	// streams do it, so a frame handed on without its SSE framing still says
	// what it is.
	if string(frame.EventType) != agmasync.EventRouteChanged {
		t.Errorf("eventType = %q, want %q", frame.EventType, agmasync.EventRouteChanged)
	}

	// changedAt says when the selection reached this state, which is what lets a
	// participant discard a repeat older than one it has already applied.
	if frame.ChangedAt.IsZero() {
		t.Error("frame carries no changedAt")
	}

	// The frame states the closure and not just what the user clicked. Fields
	// pull in the whole graph, opt-in being dependency-closed.
	if got := agmasync.SelectedTypes(*frame); !slices.Equal(got, agmasync.DependencyOrder) {
		t.Errorf("selected types = %v, want %v", got, agmasync.DependencyOrder)
	}
}

func TestDeselectingIsStatedToo(t *testing.T) {
	// Every move of the selection reaches a connected participant, including the
	// ones that take something away. Without this a participant is left inferring
	// from silence that a type it was sending is no longer wanted.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeField)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := p.client.Events(ctx, "")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer stream.Close()
	next := nextSelection(t, stream, "ep-a")

	// Stated on connecting. Fields pull in the whole graph, and SelectedTypes
	// hands them back in dependency order so a parent is always sent before what
	// references it.
	first := next()
	if got := agmasync.SelectedTypes(*first); !slices.Equal(got, agmasync.DependencyOrder) {
		t.Fatalf("selection on connecting = %v, want %v", got, agmasync.DependencyOrder)
	}

	// The user narrows to farms. No load starts — agrirouter has nothing more to
	// send — but the participant is told all the same.
	if err := f.router.OptIn("ep-a", agmasync.TypeFarm); err != nil {
		t.Fatalf("narrowing the selection: %v", err)
	}

	want := []agmasync.EntityType{
		agmasync.TypeOrganization, agmasync.TypePerson, agmasync.TypeFarm,
	}
	if got := agmasync.SelectedTypes(*next()); !slices.Equal(got, want) {
		t.Errorf("narrowed selection = %v, want %v", got, want)
	}

	// Deselecting the last type states an empty selection, which is how a
	// participant learns exchange has ended rather than by never hearing again.
	if err := f.router.OptIn("ep-a"); err != nil {
		t.Fatalf("deselecting everything: %v", err)
	}
	if got := agmasync.SelectedTypes(*next()); len(got) != 0 {
		t.Errorf("selection after deselecting everything = %v, want empty", got)
	}
}

// selectionFrames collects the ROUTE_CHANGED frames naming this endpoint. A
// frame names one endpoint, a selection belonging to one, so this is a filter
// rather than a search.
func selectionFrames(
	t *testing.T, stream *agmasync.Stream, externalID string,
) chan *oapi.RouteChangedEventData {
	t.Helper()
	frames := make(chan *oapi.RouteChangedEventData, 8)
	go func() {
		for ev, err := range stream.Events() {
			if err != nil {
				return
			}
			if ev.Type != agmasync.EventRouteChanged || ev.Selection == nil {
				continue
			}
			if ev.Selection.ExternalEndpointId == externalID {
				frames <- ev.Selection
			}
		}
	}()
	return frames
}

// nextSelection steps through those frames in order.
func nextSelection(
	t *testing.T, stream *agmasync.Stream, externalID string,
) func() *oapi.RouteChangedEventData {
	t.Helper()
	frames := selectionFrames(t, stream, externalID)
	return func() *oapi.RouteChangedEventData {
		t.Helper()
		select {
		case f := <-frames:
			return f
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a ROUTE_CHANGED frame")
			return nil
		}
	}
}

func TestAWithdrawalSurvivesBeingDisconnectedForIt(t *testing.T) {
	// The frame announcing a withdrawal is no use to a participant that was not
	// listening when it went out. What reaches it is catch-up, which restates the
	// selection of every endpoint whose selection changed above its position —
	// emptied ones included. An emptied endpoint has no selection left to state,
	// so agrirouter retains that it took part at some point in order to be able
	// to state that it now exchanges nothing.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	// The user withdraws while nobody is on the stream.
	if err := f.router.OptIn("ep-a"); err != nil {
		t.Fatalf("deselecting everything: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := p.client.Events(ctx, "")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer stream.Close()

	restated := nextSelection(t, stream, "ep-a")()
	if got := agmasync.SelectedTypes(*restated); len(got) != 0 {
		t.Errorf("selection after a withdrawal missed = %v, want empty", got)
	}
}

func TestAnEndpointThatNeverTookPartIsNeverMentioned(t *testing.T) {
	// Catch-up restates what changed, and an endpoint the user never routed to
	// the hub has never changed. Saying so on every connection would make
	// catch-up proportional to how many endpoints the application has rather
	// than to how many ever took part, most never taking part at all.
	f := newFixture(t)
	f.router.AddEndpoint("fmis-a", f.tenant, "ep-quiet")

	client, err := agmasync.NewClient(f.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Events(ctx, "")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer stream.Close()

	// Waiting for the catch-up to run out rather than for a frame: the assertion
	// is that none arrives.
	select {
	case f := <-selectionFrames(t, stream, "ep-quiet"):
		t.Errorf("an endpoint that never took part was stated: %v", f.EntityTypes)
	case <-time.After(500 * time.Millisecond):
	}
}

// completeLoad drives an endpoint from LOADING_FROM_AGRIROUTER to COMPLETED, so
// that a test can assert on what happens to a load that is over rather than one
// that is still running.
func completeLoad(t *testing.T, p *participant) {
	t.Helper()
	ctx := context.Background()

	stream, err := p.endpoint.InitialLoadEvents(ctx)
	if err != nil {
		t.Fatalf("initial load stream: %v", err)
	}
	for range stream.Events() {
	}
	stream.Close()

	if _, err := p.endpoint.ConfirmReconciled(ctx, nil); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := p.endpoint.CompleteInitialLoad(ctx); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestNarrowingLeavesTheInitialLoadStateAlone(t *testing.T) {
	// Deselecting a type stops its delivery and does nothing else. agrirouter
	// has no more to send than it already had, so there is no set to restart and
	// the endpoint is still in step for what it remains opted into. Widening is
	// the contrast, and asserting it too is what keeps this from passing
	// vacuously: a narrowing that wrongly called startLoad would look exactly
	// like a widening.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeField)
	completeLoad(t, p)

	ctx := context.Background()
	before, err := p.endpoint.InitialLoadStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if before.State != agmasync.StateCompleted {
		t.Fatalf("state = %q, want %q before narrowing", before.State, agmasync.StateCompleted)
	}

	// Drop fields and field boundaries, keeping the farms and the parties they
	// reference — a narrowing that still leaves the endpoint taking part.
	if err := f.router.OptIn("ep-a", agmasync.TypeFarm); err != nil {
		t.Fatalf("narrowing the selection: %v", err)
	}

	after, err := p.endpoint.InitialLoadStatus(ctx)
	if err != nil {
		t.Fatalf("status after narrowing: %v", err)
	}
	if after.State != agmasync.StateCompleted {
		t.Errorf("state = %q after narrowing, want %q: deselecting restarts nothing",
			after.State, agmasync.StateCompleted)
	}
	if !agmasync.IsRepeatLoad(after) {
		t.Error("previousLoadCompletedAt must survive a narrowing")
	}

	// Selecting a type back in does restart the load, since agrirouter cannot
	// enumerate what the endpoint missed while the type was off.
	if err := f.router.OptIn("ep-a", agmasync.TypeField); err != nil {
		t.Fatalf("widening the selection: %v", err)
	}
	widened, err := p.endpoint.InitialLoadStatus(ctx)
	if err != nil {
		t.Fatalf("status after widening: %v", err)
	}
	if widened.State != agmasync.StateLoadingFromAgrirouter {
		t.Errorf("state = %q after widening, want %q",
			widened.State, agmasync.StateLoadingFromAgrirouter)
	}
}

func TestDeselectingTheLastTypeDiscardsTheStateButNotTheRepeatMarker(t *testing.T) {
	// The other half of the rule: the state is left alone by a narrowing and
	// discarded only with the last type. What does not go with it is
	// previousLoadCompletedAt, which hangs off the endpoint — so an endpoint
	// that opts back in is a returning participant and not a newcomer, and the
	// set it is sent is one it has been sent before.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	completeLoad(t, p)

	ctx := context.Background()
	if err := f.router.OptIn("ep-a"); err != nil {
		t.Fatalf("deselecting everything: %v", err)
	}
	if _, err := p.endpoint.InitialLoadStatus(ctx); !errors.Is(err, agmasync.ErrNotFound) {
		t.Errorf("status = %v, want ErrNotFound once the last type is deselected", err)
	}

	if err := f.router.OptIn("ep-a", agmasync.TypeFarm); err != nil {
		t.Fatalf("selecting again: %v", err)
	}
	again, err := p.endpoint.InitialLoadStatus(ctx)
	if err != nil {
		t.Fatalf("status after selecting again: %v", err)
	}
	if again.State != agmasync.StateLoadingFromAgrirouter {
		t.Errorf("state = %q, want %q", again.State, agmasync.StateLoadingFromAgrirouter)
	}
	if !agmasync.IsRepeatLoad(again) {
		t.Error("a returning endpoint must be marked a repeat: it is not a newcomer")
	}
}

// optInOverHTTP drives the control plane the way a scenario does, rather than
// calling the in-process helper. The two go through one function now, and this
// is what keeps the HTTP face itself — parsing, status codes — from rotting: it
// was reachable only from the scenarios binary and the `it`-tagged container
// suite before, so a break in it could sit unnoticed.
func optInOverHTTP(t *testing.T, f *fixture, externalID string, collections ...string) int {
	t.Helper()
	body, err := json.Marshal(testrouter.OptInRequest{EntityTypes: collections})
	if err != nil {
		t.Fatalf("encoding opt-in: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut,
		f.server.URL+"/_test/endpoints/"+externalID+"/opt-in", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("opt-in over HTTP: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode
}

func TestOptInOverTheControlPlaneBehavesAsInProcess(t *testing.T) {
	f := newFixture(t)
	endpointID := f.router.AddEndpoint("fmis-a", f.tenant, "ep-a")
	client, err := agmasync.NewClient(f.server.URL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	ep := client.For(endpointID, "ep-a")

	if code := optInOverHTTP(t, f, "ep-a", "farm"); code != http.StatusOK {
		t.Fatalf("opt-in status = %d, want 200", code)
	}
	status, err := ep.InitialLoadStatus(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != agmasync.StateLoadingFromAgrirouter {
		t.Errorf("state = %q, want %q", status.State, agmasync.StateLoadingFromAgrirouter)
	}

	// One vocabulary, here as everywhere: an entity type is named as the `type`
	// discriminator carries it. The collection segment is refused rather than
	// quietly understood, so a caller learns the difference here instead of
	// against a conforming implementation.
	if code := optInOverHTTP(t, f, "ep-a", "farms"); code != http.StatusBadRequest {
		t.Errorf("selecting by collection segment = %d, want 400", code)
	}

	// The declaration bounds the selection on this path too, and the refusal
	// carries which type is missing.
	if err := f.router.Declare("ep-a", agmasync.TypeOrganization); err != nil {
		t.Fatalf("narrowing the declaration: %v", err)
	}
	if code := optInOverHTTP(t, f, "ep-a", "farm"); code != http.StatusBadRequest {
		t.Errorf("selecting an undeclared type = %d, want 400", code)
	}

	// Deselecting everything discards the state, as it does in process.
	if code := optInOverHTTP(t, f, "ep-a"); code != http.StatusOK {
		t.Fatalf("deselecting status = %d, want 200", code)
	}
	if _, err := ep.InitialLoadStatus(context.Background()); !errors.Is(
		err, agmasync.ErrNotFound,
	) {
		t.Errorf("status = %v, want ErrNotFound", err)
	}
}
