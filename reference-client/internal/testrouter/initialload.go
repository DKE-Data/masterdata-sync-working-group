package testrouter

import (
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// The initial-load states.
//
// Entries into stateLoadingFromAgrirouter are agrirouter's and follow from
// opt-in; the two exits are the endpoint's. There is no operation for asking
// that the set be sent again, so a participant setting that state is out of
// order like any other illegal transition.
const (
	stateLoadingFromAgrirouter = "LOADING_FROM_AGRIROUTER"
	stateReconciling           = "RECONCILING"
	stateLoadingToAgrirouter   = "LOADING_TO_AGRIROUTER"
	stateCompleted             = "COMPLETED"
)

// setLoadState moves an endpoint, for the transitions agrirouter drives.
func (r *Router) setLoadState(ep *endpoint, state string) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if ep.load == nil {
		ep.load = &loadState{}
	}
	ep.load.state = state
	ep.load.updatedAt = r.store.now().UTC()
	r.observer.record("initialLoadState", map[string]any{
		"externalEndpointId": ep.externalID,
		"state":              state,
		"driver":             "agrirouter",
	})
}

// startLoad puts an endpoint at the beginning of an initial load.
//
// This is what a widening of the user's selection does. The set is fixed when
// the load starts, so a type selected later starts it over, from whichever state
// the endpoint was in.
//
// Telling the participant is not done here: every move of the selection is
// announced, narrowings included, so publishing belongs with the change to the
// selection rather than with this one of its outcomes. See publishSelection.
func (r *Router) startLoad(ep *endpoint) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if ep.load == nil {
		ep.load = &loadState{}
	}
	// previousLoadCompletedAt is not touched here — it hangs off the endpoint
	// rather than off this state — and is what marks the arriving set a repeat.
	// Without it a participant blind-creates local objects for data it already
	// holds.
	ep.load.state = stateLoadingFromAgrirouter
	ep.load.awaitingUser = false
	ep.load.rejected = nil
	ep.load.updatedAt = r.store.now().UTC()
	r.observer.record("initialLoadState", map[string]any{
		"externalEndpointId": ep.externalID,
		"state":              stateLoadingFromAgrirouter,
		"driver":             "agrirouter",
	})
}

// publishSelection states to the participant what the user has just selected on
// this endpoint.
//
// The participant is not present when the user chooses, so this is the only way
// it learns. It is published for every move — a type selected, a type
// deselected, the last one deselected — so a participant is never left inferring
// from silence that a type it was sending is no longer wanted.
//
// The move takes a change number of its own, recorded on the endpoint. That is
// what a participant's catch-up selects on later, and it is why a move made
// while nobody was connected is not lost: the frame is not replayed from a log,
// it is rebuilt from the endpoint on the next connection.
func (r *Router) publishSelection(ep *endpoint) {
	r.store.mu.Lock()
	r.store.seq++
	ep.selectionChangedSeq = r.store.seq
	f := routeChangedFrame(ep, encodePosition(r.store.seq))
	appID := ep.appID
	r.store.mu.Unlock()

	r.hub.publish(appID, f)
}

// advanceLoadState applies one of the endpoint-driven transitions.
//
// The states advance in order and anything else is a conflict, including
// setting stateLoadingFromAgrirouter, which no participant may do. Setting the
// state the endpoint is already in is not out of order: it succeeds, and any
// bindings carried are applied again and their rejections recomputed, which is
// the only way an endpoint whose confirmation went unanswered can learn what was
// recorded.
func (r *Router) advanceLoadState(
	ep *endpoint, target string, bindings []binding,
) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if ep.load == nil {
		return errNotFound
	}
	current := ep.load.state

	// Entering LOADING_FROM_AGRIROUTER follows from opt-in and from nothing
	// else, so a participant setting it is out of order from every state —
	// including from itself, where a same-state repeat would otherwise be
	// allowed as it is for the two endpoint-driven transitions. Nothing else
	// travels on this operation, so there is nothing else it could have meant.
	if target == stateLoadingFromAgrirouter {
		return errInitialLoadConflict
	}

	legal := target == current ||
		(current == stateReconciling && target == stateLoadingToAgrirouter) ||
		(current == stateLoadingToAgrirouter && target == stateCompleted)
	if !legal {
		return errInitialLoadConflict
	}

	if target == stateLoadingToAgrirouter {
		ep.load.rejected = r.applyBindings(ep, bindings)
	}
	// agrirouter clears awaitingUser, and only on the two transitions the
	// endpoint drives. Repeating a state clears nothing: the endpoint has not
	// got anywhere, so nothing it reported has been overtaken.
	if target != current {
		ep.load.awaitingUser = false
	}
	if target == stateCompleted {
		completed := r.store.now().UTC()
		ep.previousLoadCompletedAt = &completed
	}

	ep.load.state = target
	ep.load.updatedAt = r.store.now().UTC()
	r.observer.record("initialLoadState", map[string]any{
		"externalEndpointId": ep.externalID,
		"state":              target,
		"driver":             "endpoint",
		"bindings":           len(bindings),
		"awaitingUser":       ep.load.awaitingUser,
	})
	return nil
}

// raiseAwaitingUser records that a person is needed, without moving the
// endpoint.
//
// It names no state, so there is no transition to be out of order and nothing
// for it to race: an endpoint reporting a conflict as the set arrives does not
// have to know, or guess, whether agrirouter has finished sending in the
// meantime. A completed load is the one refusal, and it is about the load rather
// than about the report — there is nobody left waiting.
func (r *Router) raiseAwaitingUser(ep *endpoint) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if ep.load == nil {
		return errNotFound
	}
	if ep.load.state == stateCompleted {
		return errInitialLoadConflict
	}

	ep.load.awaitingUser = true
	ep.load.updatedAt = r.store.now().UTC()
	r.observer.record("userAttention", map[string]any{
		"externalEndpointId": ep.externalID,
		"state":              ep.load.state,
	})
	return nil
}

// applyBindings records the bindings a reconciliation produced.
//
// Pairs are applied independently: one that cannot be recorded comes back as a
// rejection rather than failing the transition, since a single unresolvable pair
// should not block the load of a whole set. A pair naming an identifier that
// another pair in the same request also names is rejected on both sides, since
// the outcome would otherwise depend on the order they were applied in.
func (r *Router) applyBindings(ep *endpoint, bindings []binding) []oapiRejection {
	// A localId names a record within its entity type, which is how the mapping
	// is keyed, so the same string against two types is two identifiers and not
	// a duplicate. The type comes from the object each pair names, a binding
	// carrying none of its own.
	duplicates := map[localKey]int{}
	duplicateIDs := map[uuid.UUID]int{}
	for _, b := range bindings {
		if obj, ok := r.store.objects[b.AgrirouterID]; ok {
			duplicates[localKey{ep.appID, obj.typ, b.LocalID}]++
		}
		duplicateIDs[b.AgrirouterID]++
	}

	rejections := []oapiRejection{}
	for _, b := range bindings {
		duplicated := false
		if obj, ok := r.store.objects[b.AgrirouterID]; ok {
			duplicated = duplicates[localKey{ep.appID, obj.typ, b.LocalID}] > 1
		}
		if duplicated || duplicateIDs[b.AgrirouterID] > 1 {
			rejections = append(rejections, oapiRejection{
				LocalID:      b.LocalID,
				AgrirouterID: b.AgrirouterID,
				Reason:       agmasync.ReasonDuplicateInRequest,
			})
			continue
		}

		obj, ok := r.store.objects[b.AgrirouterID]
		if !ok {
			rejections = append(rejections, oapiRejection{
				LocalID:      b.LocalID,
				AgrirouterID: b.AgrirouterID,
				Reason:       agmasync.ReasonUnknownObject,
			})
			continue
		}

		err := r.store.bindLocked(ep, obj.typ, b.LocalID, b.AgrirouterID)
		switch e := err.(type) {
		case nil:
			r.observer.record("bind", map[string]any{
				"externalEndpointId": ep.externalID,
				"localId":            b.LocalID,
				"agrirouterId":       b.AgrirouterID.String(),
				"bulk":               true,
			})
		case *mappingConflict:
			rejections = append(rejections, oapiRejection{
				LocalID:      b.LocalID,
				AgrirouterID: b.AgrirouterID,
				Reason:       e.reason,
				Existing:     e.existing,
			})
		default:
			rejections = append(rejections, oapiRejection{
				LocalID:      b.LocalID,
				AgrirouterID: b.AgrirouterID,
				Reason:       agmasync.ReasonUnknownObject,
			})
		}
	}
	return rejections
}
