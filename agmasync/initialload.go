package agmasync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// The initial-load states. See "Initial load" in specification.md.
//
// The progression is LoadingFromAgrirouter, Reconciling, LoadingToAgrirouter,
// Completed, and which side drives each transition follows what that side can
// observe: agrirouter starts the load and declares the set sent, the endpoint
// declares reconciliation done and the push finished.
const (
	// StateLoadingFromAgrirouter means the canonical set is owed to this
	// endpoint. Only agrirouter enters it, and only as a consequence of opt-in:
	// a user opting the endpoint into master data, or into a further entity
	// type. A participant cannot set it — the attempt is
	// [ErrInitialLoadConflict] — so an endpoint in this state is one a user's
	// decision put there.
	StateLoadingFromAgrirouter = oapi.LOADINGFROMAGRIROUTER

	// StateReconciling means agrirouter has sent the whole set. Only
	// agrirouter enters this state, and it does so only after sending
	// everything — which is why the state, not the stream closing, is the
	// proof that the set arrived.
	StateReconciling = oapi.RECONCILING

	// StateLoadingToAgrirouter is set by the endpoint to declare
	// reconciliation finished, carrying the bindings it produced. It cannot be
	// reached without having been in StateReconciling.
	StateLoadingToAgrirouter = oapi.LOADINGTOAGRIROUTER

	// StateCompleted is set by the endpoint once it has sent everything.
	// Steady-state synchronization applies from here on.
	StateCompleted = oapi.COMPLETED
)

// MasterdataConfig reads the endpoint's per-entity opt-in configuration.
//
// This is the only way to learn what a user agreed to share, and the resource
// has no write operation at all: opt-in is set by the user in agrirouter,
// alongside the route to the masterdata hub, so an endpoint that finds itself
// opted into a type was put there by a person. Opt-in is per endpoint, so what
// one endpoint exchanges says nothing about another.
//
// Read this when the endpoint's routes change: a route to the masterdata hub is
// reported like any other, by ENDPOINTS_LIST_CHANGED, and there is no separate
// opt-in notification.
//
// Where a user resolves an initial load is registered with the application, the
// way a RAC redirect URI is, and is not part of this API.
//
// The configuration is always dependency-closed — see [DependencyClosure] — so
// what arrives can be applied without waiting on a reference the endpoint will
// never be entitled to.
//
// An endpoint opted into nothing has empty toggles rather than no configuration;
// [ErrNotFound] means no such endpoint.
func (e *Endpoint) MasterdataConfig(ctx context.Context) (oapi.MasterdataConfig, error) {
	r, err := e.client.api.GetMasterdataConfigWithResponse(ctx, e.externalID)
	if err != nil {
		return oapi.MasterdataConfig{}, transportErr(err)
	}
	if resErr := (writeResult{
		statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404, body: r.Body,
	}).err(); resErr != nil {
		return oapi.MasterdataConfig{}, resErr
	}
	if r.JSON200 == nil {
		return oapi.MasterdataConfig{}, fmt.Errorf("agmasync: empty configuration response")
	}
	return *r.JSON200, nil
}

// DependencyClosure expands a set of entity types to the dependency-closed set
// that an opt-in configuration carries.
//
// agrirouter closes opt-in itself, so this does not build a request: it is what
// a participant uses to reason about what opting into a type will bring with it
// — when telling a user what to expect, or when checking that its own store can
// hold everything a choice implies.
//
// See "Entity dependencies" in specification.md.
func DependencyClosure(types []EntityType) []EntityType {
	want := make(map[EntityType]bool, len(EntityTypes))
	var add func(EntityType)
	add = func(t EntityType) {
		if want[t] {
			return
		}
		want[t] = true
		switch t {
		case TypeField:
			add(TypeFarm)
			add(TypeFieldBoundary)
			add(TypeOrganization)
			add(TypePerson)
		case TypeFarm:
			add(TypeOrganization)
			add(TypePerson)
		case TypePerson:
			add(TypeOrganization)
		}
	}
	for _, t := range types {
		add(t)
	}

	// Ordered by EntityTypes so the result is stable rather than map-ordered.
	out := make([]EntityType, 0, len(want))
	for _, t := range EntityTypes {
		if want[t] {
			out = append(out, t)
		}
	}
	return out
}

// OptedIn reports whether the configuration opts the endpoint into a type.
//
// The toggles name collections — `field-boundaries`, not `fieldBoundary` — so
// comparing against an [EntityType] directly finds nothing.
func OptedIn(cfg oapi.MasterdataConfig, t EntityType) bool {
	for _, toggle := range cfg.Toggles {
		if toggle.EntityType == t.Collection() {
			return true
		}
	}
	return false
}

// InitialLoadStatus reads the endpoint's initial-load state.
//
// This is what says whether the canonical set has been sent. A participant
// MUST NOT read that from its initial-load stream ending, because a dropped
// connection ends the response exactly as an orderly completion does; an
// endpoint that finds itself still in [StateLoadingFromAgrirouter] connects
// again and takes the set from the beginning.
//
// Returns [ErrNotFound] for an endpoint opted into no entity type, which has no
// initial-load state at all.
func (e *Endpoint) InitialLoadStatus(ctx context.Context) (oapi.InitialLoadStatus, error) {
	r, err := e.client.api.GetInitialLoadStatusWithResponse(ctx, e.externalID)
	if err != nil {
		return oapi.InitialLoadStatus{}, transportErr(err)
	}
	if resErr := (writeResult{
		statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404, body: r.Body,
	}).err(); resErr != nil {
		return oapi.InitialLoadStatus{}, resErr
	}
	if r.JSON200 == nil {
		return oapi.InitialLoadStatus{}, fmt.Errorf("agmasync: empty initial load status")
	}
	return *r.JSON200, nil
}

// IsRepeatLoad reports whether a canonical set now arriving is one this
// endpoint has been sent before.
//
// A participant MUST NOT infer from the arrival of a set that this is a first
// connection: without this check it creates local duplicates of data it
// already holds. The marker is previousLoadCompletedAt, which survives an
// opt-out, a hub disconnection, and endpoint removal, because the identifier
// mapping does. See "Re-connection" in specification.md.
func IsRepeatLoad(s oapi.InitialLoadStatus) bool {
	return s.PreviousLoadCompletedAt != nil
}

// SetInitialLoadState drives one of the endpoint's two transitions.
//
// Out-of-order transitions are [ErrInitialLoadConflict]. Repeating the state
// the endpoint is already in is not out of order: it succeeds, and any
// bindings carried are applied again and their rejections recomputed. That is
// the only way an endpoint whose confirmation went unanswered can learn which
// bindings were recorded, the mapping not being readable back.
//
// [StateLoadingFromAgrirouter] is refused from every state, including from
// itself. This operation moves the endpoint and does nothing else; reporting
// that a user is needed is [Endpoint.ReportUserAttention].
func (e *Endpoint) SetInitialLoadState(
	ctx context.Context, upd oapi.InitialLoadStateUpdate,
) (oapi.InitialLoadStatus, error) {
	r, err := e.client.api.SetInitialLoadStateWithResponse(ctx, e.externalID, upd)
	if err != nil {
		return oapi.InitialLoadStatus{}, transportErr(err)
	}
	if r.StatusCode() == 409 {
		msg := "initial load transition out of order"
		if r.JSON409 != nil && r.JSON409.Message != "" {
			msg = r.JSON409.Message
		}
		return oapi.InitialLoadStatus{}, &APIError{r.StatusCode(), msg, ErrInitialLoadConflict}
	}
	if resErr := (writeResult{
		statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
		notFound: r.JSON404, body: r.Body,
	}).err(); resErr != nil {
		return oapi.InitialLoadStatus{}, resErr
	}
	if r.JSON200 == nil {
		return oapi.InitialLoadStatus{}, fmt.Errorf("agmasync: empty initial load status")
	}
	return *r.JSON200, nil
}

// ConfirmReconciled declares that reconciliation is finished and carries the
// bindings it produced.
//
// It asserts that the conflicts the set surfaced have been resolved in the
// participant's own software, not merely that the set was received — receipt is
// already recorded, agrirouter having moved the endpoint to
// [StateReconciling] when it finished sending. Reconciliation is human-paced
// and unbounded, so sitting in [StateReconciling] for a long time is not a
// fault.
//
// Bindings are applied independently: a pair that cannot be recorded comes back
// in the status's RejectedIdMappings rather than failing the transition, since
// one unresolvable pair should not block the load of a whole set. Each has to
// be resolved on its own terms and rebound through [Endpoint.Bind]; use
// [NeedsUser] to tell the ones that need a person from the ones that do not.
func (e *Endpoint) ConfirmReconciled(
	ctx context.Context, bindings []oapi.IdMappingBinding,
) (oapi.InitialLoadStatus, error) {
	return e.SetInitialLoadState(ctx, oapi.InitialLoadStateUpdate{
		State:      StateLoadingToAgrirouter,
		IdMappings: &bindings,
	})
}

// CompleteInitialLoad declares that the endpoint has sent everything it holds.
func (e *Endpoint) CompleteInitialLoad(ctx context.Context) (oapi.InitialLoadStatus, error) {
	return e.SetInitialLoadState(ctx, oapi.InitialLoadStateUpdate{State: StateCompleted})
}

// The canonical set cannot be asked for through this API, and there is no
// operation that re-enters [StateLoadingFromAgrirouter]. Opting an entity type
// in restarts the load, and nothing else does.
//
// A participant that has fallen behind, or whose store was restored from a
// backup, does not need one. A backup carries the delivery position along with
// the data, positions do not expire, and catch-up serves the current value of
// everything that changed since — so resuming [Client.Events] from the restored
// position is the whole recovery. Objects the endpoint wrote itself are the
// exception, being withheld by origin suppression; a stale copy of one resolves
// on the next edit, since a stale base is answered with the current revision or
// a merge and the object can then be fetched with [Endpoint.Request], requests
// being exempt from suppression.

// ReportUserAttention tells agrirouter that this endpoint's reconciliation is
// waiting on a person.
//
// Resolution happens on a screen agrirouter cannot see, while the user who
// connected the endpoint may well be looking at agrirouter — so agrirouter
// shows "waiting for you in <app>" in place of its own "this application is
// working through your data". It is one bit per endpoint: agrirouter learns
// that a person is needed and never what for.
//
// It names no state, and that is the point of it being its own operation.
// Conflicts surface object by object, so this has to be sendable from any state
// before [StateCompleted] — including while the set is still arriving, when the
// endpoint has no state of its own to name and agrirouter may advance it at any
// moment. Naming nothing, the report cannot be out of order and cannot race a
// transition.
//
// The endpoint raises and agrirouter clears, on the two endpoint-driven
// transitions only; there is nothing here to lower it with. Repeating it while
// it is already raised does nothing. A [StateCompleted] load waits on nobody, so
// reporting one is [ErrInitialLoadConflict]. Nothing in the protocol branches on
// the flag, so an endpoint that never calls this costs precision rather than
// correctness.
func (e *Endpoint) ReportUserAttention(ctx context.Context) (oapi.InitialLoadStatus, error) {
	r, err := e.client.api.ReportUserAttentionWithResponse(ctx, e.externalID)
	if err != nil {
		return oapi.InitialLoadStatus{}, transportErr(err)
	}
	if r.StatusCode() == 409 {
		msg := "the initial load is completed and waits on nobody"
		if r.JSON409 != nil && r.JSON409.Message != "" {
			msg = r.JSON409.Message
		}
		return oapi.InitialLoadStatus{}, &APIError{r.StatusCode(), msg, ErrInitialLoadConflict}
	}
	if resErr := (writeResult{
		statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404, body: r.Body,
	}).err(); resErr != nil {
		return oapi.InitialLoadStatus{}, resErr
	}
	if r.JSON200 == nil {
		return oapi.InitialLoadStatus{}, fmt.Errorf("agmasync: empty initial load status")
	}
	return *r.JSON200, nil
}

// Binding pairs one of this participant's local identifiers with the canonical
// object it was matched to, for the bulk confirmation.
func Binding(localID string, agrirouterID uuid.UUID) oapi.IdMappingBinding {
	return binding(localID, agrirouterID)
}
