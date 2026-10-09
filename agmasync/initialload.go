package agmasync

import (
	"context"
	"fmt"
	"net/http"

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

// DeclareCapabilities builds the masterdata configuration declaring the given entity
// types, closed over entity dependencies (see [DependencyClosure]).
//
// It is the `masterdata` field of the application's own PutEndpoint call. This
// package does not make that call: PutEndpoint upserts the whole endpoint, so a
// call carrying only the declaration would withdraw the endpoint's message
// capabilities and subscriptions. The application, which knows those, sends
// the declaration alongside them.
//
// Withdrawing a type narrows any selection naming it, which for that type has
// the effect of the user deselecting it. That is the only way a declaration
// changes what is delivered, and it can only ever remove.
func DeclareCapabilities(types ...EntityType) oapi.MasterdataConfig {
	closure := DependencyClosure(types)
	capabilities := make([]oapi.EntityTypeToggle, 0, len(closure))
	for _, t := range closure {
		capabilities = append(capabilities, oapi.EntityTypeToggle{EntityType: string(t)})
	}
	return oapi.MasterdataConfig{Capabilities: capabilities}
}

// DependencyClosure expands a set of entity types to the dependency-closed set
// that a declaration or a selection carries.
//
// Both steps must be closed. For a declaration it is what to send: one that is
// not closed is rejected. For the user's selection agrirouter closes it itself,
// so this is what a participant uses to reason about what opting into a type
// will bring with it — when telling a user what to expect, or when checking that
// its own store can hold everything a choice implies.
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
		// A boundary's field is the only required reference. Every other one
		// is optional, so it constrains nothing (ADR 13).
		if t == TypeFieldBoundary {
			add(TypeField)
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

// SelectedTypes reads the entity types the user selected, off an
// [EventRouteChanged] frame.
//
// The result is in [DependencyOrder], so walking it sends parents before the
// objects referencing them. An empty result means the endpoint exchanges nothing
// — the user deselected the last type, or removed the route. That is a statement
// and not an omission, the frame stating the selection in full.
//
// A toggle carries the same value as the entity `type` discriminator, so the
// comparison is direct. What this adds over a loop at each call site is the
// ordering.
func SelectedTypes(s oapi.RouteChangedEventData) []EntityType {
	out := make([]EntityType, 0, len(s.EntityTypes))
	for _, typ := range DependencyOrder {
		for _, entry := range s.EntityTypes {
			if entry.EntityType == string(typ) {
				out = append(out, typ)
				break
			}
		}
	}
	return out
}

// DeclaresCapability reports whether the endpoint declared it can exchange a type.
//
// It answers what the software is capable of, never what the user opted it into
// — see [SelectedTypes] for that.
func DeclaresCapability(cfg oapi.MasterdataConfig, t EntityType) bool {
	for _, toggle := range cfg.Capabilities {
		if toggle.EntityType == string(t) {
			return true
		}
	}
	return false
}

// GetInitialLoadStatus reads the endpoint's initial-load state.
//
// This is what says whether the canonical set has been sent. A participant
// MUST NOT read that from its initial-load stream ending, because a dropped
// connection ends the response exactly as an orderly completion does; an
// endpoint that finds itself still in [StateLoadingFromAgrirouter] connects
// again and takes the set from the beginning.
//
// Returns [ErrNotFound] for an endpoint opted into no entity type, which has no
// initial-load state at all.
func GetInitialLoadStatus(
	ctx context.Context, api *oapi.ClientWithResponses,
	externalEndpointID string, tenantID uuid.UUID,
) (oapi.InitialLoadStatus, error) {
	r, err := api.GetInitialLoadStatusWithResponse(ctx, externalEndpointID,
		&oapi.GetInitialLoadStatusParams{XAgrirouterTenantId: tenantID})
	if err != nil {
		return oapi.InitialLoadStatus{}, transportErr(err)
	}
	if resErr := (writeResult{
		statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404, body: r.Body,
	}).err(); resErr != nil {
		return oapi.InitialLoadStatus{}, resErr
	}
	if r.JSON200 == nil {
		return oapi.InitialLoadStatus{}, fmt.Errorf("%w: no initial load status", ErrEmptyResponse)
	}
	return *r.JSON200, nil
}

// IsRepeatLoad reports whether a canonical set now arriving is one this
// endpoint has been sent before.
//
// A participant MUST NOT infer from the arrival of a set that this is a first
// connection: without this check it creates local duplicates of data it
// already holds. The marker is previousLoadCompletedAt, which survives an
// opt-out, a disconnection, and endpoint removal, because the identifier
// mapping does. A masterdata reset discards it with the mapping, so the load
// that follows one is a first load. See "Re-connection" and "Masterdata reset"
// in specification.md.
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
// that a user is needed is [ReportUserAttention].
func SetInitialLoadState(
	ctx context.Context, api *oapi.ClientWithResponses,
	externalEndpointID string, tenantID uuid.UUID,
	upd oapi.InitialLoadStateUpdate,
) (oapi.InitialLoadStatus, error) {
	r, err := api.SetInitialLoadStateWithResponse(ctx, externalEndpointID,
		&oapi.SetInitialLoadStateParams{XAgrirouterTenantId: tenantID}, upd)
	if err != nil {
		return oapi.InitialLoadStatus{}, transportErr(err)
	}
	if r.StatusCode() == http.StatusConflict {
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
		return oapi.InitialLoadStatus{}, fmt.Errorf("%w: no initial load status", ErrEmptyResponse)
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
// be resolved on its own terms and rebound through [Bind]; use
// [NeedsUser] to tell the ones that need a person from the ones that do not.
func ConfirmReconciled(
	ctx context.Context, api *oapi.ClientWithResponses,
	externalEndpointID string, tenantID uuid.UUID,
	bindings []oapi.IdMappingBinding,
) (oapi.InitialLoadStatus, error) {
	return SetInitialLoadState(ctx, api, externalEndpointID, tenantID, oapi.InitialLoadStateUpdate{
		State:      StateLoadingToAgrirouter,
		IdMappings: &bindings,
	})
}

// CompleteInitialLoad declares that the endpoint has sent everything it holds.
func CompleteInitialLoad(
	ctx context.Context, api *oapi.ClientWithResponses,
	externalEndpointID string, tenantID uuid.UUID,
) (oapi.InitialLoadStatus, error) {
	return SetInitialLoadState(ctx, api, externalEndpointID, tenantID, oapi.InitialLoadStateUpdate{State: StateCompleted})
}

// ReportUserAttention tells agrirouter that this endpoint's reconciliation is
// waiting on a person.
func ReportUserAttention(
	ctx context.Context, api *oapi.ClientWithResponses,
	externalEndpointID string, tenantID uuid.UUID,
) (oapi.InitialLoadStatus, error) {
	r, err := api.ReportUserAttentionWithResponse(ctx, externalEndpointID,
		&oapi.ReportUserAttentionParams{XAgrirouterTenantId: tenantID})
	if err != nil {
		return oapi.InitialLoadStatus{}, transportErr(err)
	}
	if r.StatusCode() == http.StatusConflict {
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
		return oapi.InitialLoadStatus{}, fmt.Errorf("%w: no initial load status", ErrEmptyResponse)
	}
	return *r.JSON200, nil
}
