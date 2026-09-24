package sync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	"github.com/google/uuid"
)

// Receiver is the platform's live receive loop: connect from the position it
// last durably applied, apply what arrives, bind what it had to create.
//
// It is application-scoped because the stream is. One connection carries every
// tenant the application is routed to, so the receiver holds an [Applier] per
// tenant and routes each frame by the tenant its object belongs to. See
// "Common envelope" in specification.md.
type Receiver struct {
	Client *agmasync.Client

	// Store is where the delivery position lives. It is the application's
	// position rather than one tenant's, one stream serving all of them.
	Store *store.Store

	// Tenants holds one applier per tenant this application is routed to, keyed
	// by the tenant's agrirouter identifier — the `tenantId` every delivered
	// object carries. They share one store, the mapping in it being the
	// application's.
	//
	// The tenant is the partition because it is the only thing on a delivered
	// object that says whose data it is: a delivered object names no recipient,
	// and `sourceEndpointId` names the endpoint the change came from, which is
	// somebody else's.
	Tenants map[uuid.UUID]*Applier

	// OnApplied is called after each frame is applied, for narration. It is not
	// part of the protocol.
	OnApplied func(agmasync.Event, Outcome)

	// OnCaughtUp is called when the CAUGHT_UP frame ends the backlog, which is
	// the only thing that says the connection is established and current. A
	// participant that means to act and then wait for the result — asking for an
	// object by identifier, say — has nothing else to hang that on: the delivery
	// it is waiting for is live, so it has to be connected before it asks.
	//
	// Narration and sequencing, like OnApplied, and not part of the protocol.
	OnCaughtUp func()

	// OnSelection is called once per ROUTE_CHANGED frame, with the endpoint's
	// selection as it stands after the change.
	//
	// The receiver cannot act on it itself: what a narrowing means is a product
	// decision — stop offering those types, tell the user, perhaps archive
	// something — and an empty EntityTypes means exchange has ended for that
	// endpoint. What the receiver must not do is drop it silently, which would
	// leave the platform sending types nobody wants until a write is refused.
	//
	// It is handed the transaction the frame's position is written in, and what
	// it persists through that transaction commits with the position or not at
	// all — the same bargain [Applier.Apply] makes for an entity frame, and for
	// the same reason. A position is a claim to have durably applied everything
	// at or below it, and the receiver has no way to make that claim about work
	// done somewhere it cannot see. Returning an error abandons both: the frame
	// is redelivered on the next connection, which the specification requires a
	// participant to tolerate, since the frame states the whole selection rather
	// than a delta.
	OnSelection func(*store.Tx, oapi.RouteChangedEventData) error

	// OnReset is called once per RESET_MASTERDATA_SYNC frame, after the
	// receiver has discarded the tenant's bindings and handed each listed
	// endpoint's empty selection to OnSelection. discarded counts the pairs
	// dropped. It runs in the frame's transaction, like OnSelection, and is
	// optional: what the protocol requires has been done by then.
	OnReset func(tx *store.Tx, reset oapi.MasterdataResetEventData, discarded int) error
}

// ReceiveResult counts what one run of the loop did.
type ReceiveResult struct {
	// Received counts frames carrying an object; CaughtUp says whether the
	// catch-up frame that ends the backlog was seen.
	Received int
	CaughtUp bool

	Created, Matched, Superseded, Ignored, Bound int

	// Position is where the participant may resume from, which is the position
	// of the last frame it durably applied.
	Position string
}

// CatchUp applies the backlog and returns when agrirouter says there is none
// left.
//
// The CAUGHT_UP frame is the only thing that says so. It carries no object and
// names no entity type — it covers the catch-up as a whole — and it carries the
// position reached, which is safe to keep once everything before it has been
// applied.
func (r *Receiver) CatchUp(ctx context.Context) (ReceiveResult, error) {
	return r.consume(ctx, true, nil)
}

// RunFromStart applies the stream from the beginning, ignoring the position the
// participant has stored.
//
// It is recovery, not routine: asking for everything again costs a redelivery
// of the whole backlog, which is idempotent — every apply is revision-guarded,
// so what has already been applied is recognised as older and skipped.
//
// What it is for is the frame that never arrived. Routing reaches a participant
// on the stream and nowhere else: there is no operation that reads it back, and
// catch-up restates it only for endpoints whose routing changed *above* the
// participant's position. So an endpoint that took a position past a
// ROUTE_CHANGED it failed to record has no way to ask what it is routed to —
// except to ask for the stream from the beginning, where every routing change
// is above the position again.
//
// The positions it records on the way are the frames' own, so they run behind
// the position it started from until the replay catches up. That is safe rather
// than merely tolerable: a position behind the truth costs redelivery, which
// the specification requires a participant to tolerate anyway.
func (r *Receiver) RunFromStart(ctx context.Context) (ReceiveResult, error) {
	beginning := ""
	return r.consume(ctx, false, &beginning)
}

// Run applies frames until the context is cancelled or the stream ends.
//
// A stream ending is not an error and not a signal: it is a dropped connection,
// and the answer is to connect again from the last durably applied position.
// This returns instead of reconnecting so that the caller owns the backoff.
func (r *Receiver) Run(ctx context.Context) (ReceiveResult, error) {
	return r.consume(ctx, false, nil)
}

// consume applies frames from the given position, or from the stored one where
// none is given.
func (r *Receiver) consume(
	ctx context.Context, untilCaughtUp bool, start *string,
) (ReceiveResult, error) {
	var res ReceiveResult

	// The position is read from the store and passed back exactly as agrirouter
	// issued it. Nothing here parses or compares one: an empty position asks for
	// everything, and any other value is opaque.
	from := ""
	if start != nil {
		from = *start
	} else {
		var err error
		if from, err = r.Store.Position(); err != nil {
			return res, err
		}
	}
	res.Position = from

	var err error

	stream, err := r.Client.Events(ctx, from)
	if err != nil {
		return res, err
	}
	defer func() { _ = stream.Close() }()

	// Nothing is read here. What each endpoint exchanges arrives on the stream:
	// catch-up restates it for every endpoint whose selection changed above our
	// position, emptied ones included, so a withdrawal made while we were away
	// reaches us as a frame rather than as an absence we would have to go
	// looking for.
	for ev, err := range stream.Events() {
		if err != nil {
			return res, err
		}

		if ev.Type == agmasync.EventCaughtUp {
			// Everything before this frame has been applied and committed —
			// entity frames with their own positions, selections with theirs —
			// so this position is one the participant may resume from. It covers
			// nothing that is not already durable, which is what makes taking it
			// safe rather than merely convenient.
			if ev.ID != "" {
				if err := r.savePosition(ev.ID); err != nil {
					return res, err
				}
				res.Position = ev.ID
			}
			res.CaughtUp = true
			if r.OnCaughtUp != nil {
				r.OnCaughtUp()
			}
			if untilCaughtUp {
				return res, nil
			}
			continue
		}
		if ev.Selection != nil {
			// The one frame on this stream that is not an entity, and it states
			// the whole selection rather than a delta, so handing it on is the
			// whole of acting on it. Its position travels with what the platform
			// persisted from it, which is the only way the receiver can claim to
			// have applied it.
			applied, err := r.applySelection(ev, *ev.Selection)
			if err != nil {
				return res, err
			}
			if applied && ev.ID != "" {
				res.Position = ev.ID
			}
			continue
		}
		if ev.Reset != nil {
			if err := r.applyReset(ev, *ev.Reset); err != nil {
				return res, err
			}
			if ev.ID != "" {
				res.Position = ev.ID
			}
			continue
		}
		if !ev.HasEntity() {
			// A frame type this version does not know is tolerated rather than
			// treated as a failure, and its position is not taken: the
			// participant has not applied whatever it carried.
			continue
		}

		applier, err := r.applierFor(ev)
		if err != nil {
			return res, err
		}

		outcome, err := applier.Apply(ev.Entity, ev.ID)
		if err != nil {
			return res, err
		}
		res.Received++
		res.Position = ev.ID

		switch {
		case outcome.Created:
			res.Created++
		case outcome.Matched:
			res.Matched++
		case outcome.Superseded:
			res.Superseded++
		case outcome.Ignored:
			res.Ignored++
		}

		// Binding is what makes the new record sendable, and it happens after
		// the object is committed: an endpoint that bound first and then failed
		// to store the object would be claiming to hold something it does not.
		// The other order costs a record that is applied but not yet bound,
		// which the next attempt fixes.
		if outcome.NeedsBinding {
			if err := applier.Bind(
				ctx, ev.Envelope.Type, outcome.LocalID, *ev.Envelope.AgrirouterId,
			); err != nil {
				return res, err
			}
			res.Bound++
		}

		if r.OnApplied != nil {
			r.OnApplied(ev, outcome)
		}
	}
	return res, nil
}

// applySelection hands a ROUTE_CHANGED frame to the platform, in the
// transaction that records the frame's position. It reports whether the
// position was taken.
//
// There is nothing to read behind it and nothing to merge: the frame states the
// endpoint's whole selection, so the platform replaces what it held for that
// endpoint with it. An empty EntityTypes is the statement that the endpoint
// exchanges nothing, and it is the one a receiver must not quietly drop — it is
// how a withdrawal arrives, including one made while this participant was away.
//
// Losing one is worse than seeing it twice, which is what the transaction is
// for. A position committed for a selection the platform had not durably
// recorded would put that frame below the position the next connection resumes
// from, and agrirouter does not restate what it has already stated above a
// participant's position — so a withdrawal would be lost for good, and the
// platform would go on offering types its user has switched off. The other way
// round costs a redelivery of a frame that states the whole selection, which is
// idempotent by construction.
func (r *Receiver) applySelection(
	ev agmasync.Event, sel oapi.RouteChangedEventData,
) (bool, error) {
	if r.OnSelection == nil {
		return false, nil
	}
	err := r.Store.Tx(r.tenantOf(sel), func(tx *store.Tx) error {
		if err := r.OnSelection(tx, sel); err != nil {
			return err
		}
		return tx.SetPosition(ev.ID)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// applyReset carries out a RESET_MASTERDATA_SYNC frame, in the transaction
// that records the frame's position.
//
// The tenant's pairs are dropped, because none of the agrirouterIds they name
// exists any longer, and each listed endpoint is told it exchanges nothing, which
// is what the reset stands for. Local records are left alone.
//
// The position is what keeps this safe to repeat. The specification requires a
// participant to hold a position past the reset before it takes part in the
// tenant's initial load again; committing it with the discard means there is no
// moment at which the pairs are gone and the position is not, so a resume can
// never deliver this reset again after the pairs made by the next load exist.
func (r *Receiver) applyReset(ev agmasync.Event, reset oapi.MasterdataResetEventData) error {
	// The pairs are found by the tenant the frame names, which each was
	// recorded with, so this needs no applier for the tenant: an application
	// with no endpoint left there is still told, for the pairs it holds, and
	// Endpoints is then empty. The tenancy only decides who the transaction
	// acts for.
	tenant := ""
	if applier, ok := r.Tenants[reset.TenantId]; ok {
		tenant = applier.Tenant
	}
	return r.Store.Tx(tenant, func(tx *store.Tx) error {
		discarded, err := tx.DiscardTenant(reset.TenantId)
		if err != nil {
			return err
		}
		if r.OnSelection != nil {
			for _, ep := range reset.Endpoints {
				if err := r.OnSelection(tx, oapi.RouteChangedEventData{
					EventType:   oapi.ROUTECHANGED,
					EndpointId:  ep.EndpointId,
					ExternalId:  ep.ExternalId,
					EntityTypes: []oapi.EntityTypeToggle{},
				}); err != nil {
					return err
				}
			}
		}
		if r.OnReset != nil {
			if err := r.OnReset(tx, reset, discarded); err != nil {
				return err
			}
		}
		return tx.SetPosition(ev.ID)
	})
}

// tenantOf names the tenancy a selection is about.
//
// A ROUTE_CHANGED names an endpoint rather than a tenant, so unlike a delivered
// object it cannot be routed off the envelope. Each of the product's tenancies
// is onboarded as its own endpoint, though, so the endpoint the frame names is
// the one an applier holds — and running in that tenancy is what lets a handler
// act on the withdrawal for the right set of holdings.
//
// An endpoint no applier claims leaves the empty tenancy, which is what the
// position alone needs: it belongs to the application and not to any one of its
// tenants.
func (r *Receiver) tenantOf(sel oapi.RouteChangedEventData) string {
	for _, applier := range r.Tenants {
		if applier.Endpoint != nil && applier.Endpoint.ID() == sel.EndpointId {
			return applier.Tenant
		}
	}
	return ""
}

// applierFor routes a frame to the tenant its object belongs to.
//
// The localId in a frame is resolved in the application's namespace, so the
// identifiers would be the same whichever applier took it. What the tenant
// decides is whose data this is: the tenancy the object belongs to is the one
// that ends up holding the record, and the one whose user sees it. A delivered
// object names no recipient, so `tenantId` is what says which — one stream
// carries every tenant the application is routed to, and a receiver holding
// data for several MUST partition on it rather than on the connection.
func (r *Receiver) applierFor(ev agmasync.Event) (*Applier, error) {
	id := ev.Envelope.TenantId
	if id == nil {
		// One tenant and none named leaves nothing to get wrong. With several
		// there is, so it is refused rather than guessed at.
		if len(r.Tenants) == 1 {
			for _, a := range r.Tenants {
				return a, nil
			}
		}
		return nil, fmt.Errorf("sync: a delivered object names no tenant")
	}

	applier, ok := r.Tenants[*id]
	if !ok {
		// Dropping it silently would lose the object: the position would move
		// past a frame nothing applied, and agrirouter does not send it again.
		return nil, fmt.Errorf("sync: no applier for tenant %s", *id)
	}
	return applier, nil
}

// savePosition records a position that covers no object of its own — the
// CAUGHT_UP frame's.
//
// The tenant is immaterial: the position belongs to the application, one
// stream carrying every tenant it holds.
func (r *Receiver) savePosition(id string) error {
	return r.Store.Tx("", func(tx *store.Tx) error {
		return tx.SetPosition(id)
	})
}
