package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	"github.com/google/uuid"
)

// Reconciler is the platform's own recognition step: shown a canonical object
// arriving in an initial load that agrirouter holds no identifier of ours for,
// which of this tenant's records — if any — is that same entity?
//
// It is the one part of an initial load the protocol cannot specify. agrirouter
// provides the set to reconcile against and adjudicates nothing; whether the
// farm called "Hof Nord" in the set is the farm called "Hof Nord" in the
// platform's tables is a judgement only the platform, and often only its user,
// can make. Everything else in [Loader] is protocol; this is product.
//
// It reads through the transaction the object is being applied in, so a
// recogniser sees the objects already applied by this load.
type Reconciler interface {
	Recognise(
		tx *store.Tx, env agmasync.Envelope, entity oapi.Entity,
	) (Recognition, error)
}

// ref names one of the platform's records the way everything else does: by
// entity type and local identifier together.
//
// A local identifier alone does not name a record. The platform mints them per
// type, so a farm and a field may hold the same one, and the mapping — like
// every table it is read from — is keyed by the pair. Anything that resolves a
// rejection back to a record has to carry both or it can resolve to the wrong
// one.
type ref struct {
	Type    agmasync.EntityType
	LocalID string
}

// Recognition is what recognising one object concluded.
type Recognition struct {
	// LocalID is the record the object was matched to, empty where the platform
	// does not hold the object.
	LocalID string

	// AwaitingUser says the decision needed a person — a conflict to resolve, a
	// required attribute the object does not carry, two of the platform's
	// records answering to one canonical object. It is reported to agrirouter as
	// one bit for the whole load and never as what it was about.
	AwaitingUser bool
}

// Loader drives an endpoint's initial load from wherever it currently stands to
// COMPLETED. See "Initial load" in specification.md.
//
// The four states are shared between the two sides: agrirouter enters
// LOADING_FROM_AGRIROUTER on a user's opt-in and advances to RECONCILING once it
// has sent the whole set, and the endpoint drives the two that remain. So this
// reads the state rather than assuming it, and is safe to run again after a
// crash: it re-enters at whatever state the endpoint is in and, being idempotent
// at every step, costs a repeat rather than a divergence.
type Loader struct {
	Applier *Applier

	// Reconciler recognises objects the platform holds but agrirouter does not
	// know it holds. A Loader without one creates a local record for every
	// object in the set, which is right for an endpoint that genuinely starts
	// empty and duplicates everything for one that does not.
	Reconciler Reconciler

	// Types are the entity types the user selected for this endpoint, which
	// decide what the canonical set contains and what may be offered back.
	//
	// They are supplied rather than fetched because there is nothing to fetch:
	// the selection reaches a participant on the ROUTE_CHANGED frame, and
	// [agmasync.SelectedTypes] reads it off one. The endpoint's own declaration
	// is not a substitute — it is a superset, and loading against it means
	// offering back types nobody selected.
	//
	// Empty means the endpoint takes part in nothing and Run does nothing.
	Types []agmasync.EntityType

	// Attempts caps how many times the canonical set is taken before the load is
	// given up on. Default 3.
	Attempts int
}

// LoadResult is what one run of a load did, which is mostly of interest for
// narrating it.
type LoadResult struct {
	// Repeat is true where this endpoint has completed a load before, and the
	// set now arriving is therefore one it has already been sent.
	Repeat bool

	// Types are the entity types the endpoint is opted into, in dependency
	// order.
	Types []agmasync.EntityType

	// Attempts counts how many times the set had to be taken. More than one
	// means a stream ended before agrirouter had sent everything.
	Attempts int

	// Counts over the last complete take of the set.
	Received, Created, Matched, Ignored, Superseded int

	// AwaitingUser is true where recognising something needed a person, and was
	// reported to agrirouter as such.
	AwaitingUser bool

	// Confirmed are the bindings the confirmation carried, Rejected the pairs
	// agrirouter would not record. A rejection does not fail the load: each is
	// resolved on its own terms, and until it is, the record it concerns is
	// neither bound nor sent.
	Confirmed []oapi.IdMappingBinding
	Rejected  []oapi.IdMappingRejection

	// Sent counts the records offered to agrirouter in LOADING_TO_AGRIROUTER —
	// the ones the canonical set did not contain.
	Sent int

	// State is where the endpoint ended up.
	State oapi.InitialLoadState
}

// Run drives the load to completion.
//
// An endpoint opted into nothing has no initial-load state at all, and Run does
// nothing rather than failing: an empty [Loader.Types] already says the endpoint
// does not take part.
func (l *Loader) Run(ctx context.Context) (LoadResult, error) {
	ep := l.Applier.Endpoint
	var res LoadResult

	// What the set will contain, and so what may be bound and offered back. The
	// selection is dependency-closed, so walking it in dependency order is
	// enough to send parents before the objects referencing them.
	for _, typ := range agmasync.DependencyOrder {
		for _, selected := range l.Types {
			if selected == typ {
				res.Types = append(res.Types, typ)
				break
			}
		}
	}
	if len(res.Types) == 0 {
		return res, nil
	}

	status, err := ep.InitialLoadStatus(ctx)
	if err != nil {
		return res, err
	}

	// Whether this set is one the endpoint has been sent before, asked before
	// anything is applied. Without it a returning participant creates local
	// duplicates of data it already holds — the arrival of a set is not evidence
	// of a first connection.
	res.Repeat = agmasync.IsRepeatLoad(status)

	// Why the last take came up short, carried out of the loop so that running
	// out of attempts reports the failure behind it. A set that is simply larger
	// than one connection survives leaves this nil.
	var incomplete error

	for status.State == agmasync.StateLoadingFromAgrirouter {
		if res.Attempts >= l.attempts() {
			if incomplete != nil {
				return res, fmt.Errorf(
					"sync: the canonical set did not arrive complete in %d attempts: %w",
					res.Attempts, incomplete)
			}
			return res, fmt.Errorf(
				"sync: the canonical set did not arrive complete in %d attempts", res.Attempts)
		}
		res.Attempts++
		if incomplete, err = l.takeCanonicalSet(ctx, &res); err != nil {
			return res, err
		}

		// The response ending is not proof that the set arrived: a dropped
		// connection ends it exactly as an orderly completion does. What records
		// the set having been sent is the endpoint's state, which agrirouter
		// advances only after sending everything — so an endpoint still at
		// LOADING_FROM_AGRIROUTER connects again and takes the set from the
		// beginning.
		status, err = ep.InitialLoadStatus(ctx)
		if err != nil {
			return res, err
		}
	}

	// The records whose bindings agrirouter refused, which the push below must
	// not offer back: an unbound send mints a second canonical object for an
	// entity that already has one.
	var dropped map[ref]bool

	if status.State == agmasync.StateReconciling ||
		status.State == agmasync.StateLoadingToAgrirouter {

		if res.Confirmed, err = l.bindings(res.Types); err != nil {
			return res, err
		}
		// Repeating the confirmation is not out of order, and is the only way an
		// endpoint that reached LOADING_TO_AGRIROUTER and then crashed can learn
		// which of its bindings were recorded: the mapping is not readable back,
		// and rejections are recomputed on every request that carries pairs.
		status, err = ep.ConfirmReconciled(ctx, res.Confirmed)
		if err != nil {
			return res, err
		}
		if status.RejectedIdMappings != nil {
			res.Rejected = *status.RejectedIdMappings
		}
		if dropped, err = l.dropRejected(res.Rejected); err != nil {
			return res, err
		}
	}

	if status.State == agmasync.StateLoadingToAgrirouter {
		if err := l.push(ctx, &res, dropped); err != nil {
			return res, err
		}
		if status, err = ep.CompleteInitialLoad(ctx); err != nil {
			return res, err
		}
	}

	res.State = status.State
	return res, nil
}

func (l *Loader) attempts() int {
	if l.Attempts > 0 {
		return l.Attempts
	}
	return 3
}

// takeCanonicalSet collects one delivery of the canonical set.
//
// A stream that fails part way is not an error: the set is then incomplete, the
// endpoint's state says so, and the answer is to take it again. Failing to apply
// what did arrive is an error, because nothing about taking the set again fixes
// it. That is the second return; the first is why this takeCanonicalSet was incomplete,
// which is kept rather than discarded so that a takeCanonicalSet failing the same way every
// time is reported as that failure instead of as a bare attempt count.
func (l *Loader) takeCanonicalSet(ctx context.Context, res *LoadResult) (incomplete, err error) {
	stream, err := l.Applier.Endpoint.InitialLoadEvents(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()

	// Counted per take, so what a scenario reports describes the delivery it
	// ended on rather than the sum of the ones that failed.
	res.Received, res.Created, res.Matched, res.Ignored, res.Superseded = 0, 0, 0, 0, 0

	for ev, err := range stream.Events() {
		if err != nil {
			// Not fatal, but not nothing either: it is the reason this take came
			// up short, and the only evidence distinguishing a dropped
			// connection from a failure that will recur on every attempt.
			return err, nil
		}
		if !ev.HasEntity() {
			continue
		}

		// No position: an initial-load stream carries none, delivering a fixed
		// set rather than a sequence of changes.
		out, err := l.Applier.apply(ev.Entity, "", l.Reconciler)
		if err != nil {
			return nil, err
		}

		res.Received++
		switch {
		case out.Created:
			res.Created++
		case out.Matched:
			res.Matched++
		case out.Ignored:
			res.Ignored++
		case out.Superseded:
			res.Superseded++
		}
		if out.AwaitingUser && !res.AwaitingUser {
			res.AwaitingUser = true

			// Raised as the conflict surfaces rather than once the set is
			// complete, because that is when the user is first waiting: from
			// here on agrirouter shows "waiting for you in <app>" instead of its
			// own "this application is working through your data". It names no
			// state, so it does not matter that agrirouter may finish sending
			// while this request is in flight. The confirmation clears it — the
			// endpoint raises, agrirouter clears.
			if _, err := l.Applier.Endpoint.ReportUserAttention(ctx); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

// bindings collects the pairs the confirmation carries.
//
// They are read from the platform's own table rather than accumulated while the
// set was applied, which matters for the endpoint that crashed mid-load: the
// table is what survived, and re-deriving the pairs from it is what lets the
// confirmation be repeated at all. Sending pairs agrirouter already holds is
// free — an identical pair is recorded idempotently — so the safe set to send is
// every one the platform holds.
//
// Only the opted-in types, though. A pair for a type the endpoint was opted out
// of is one agrirouter will not record, and asking it to would fill the
// rejections with pairs nobody is waiting on.
func (l *Loader) bindings(types []agmasync.EntityType) ([]oapi.IdMappingBinding, error) {
	wanted := map[agmasync.EntityType]bool{}
	for _, typ := range types {
		wanted[typ] = true
	}

	var rows []store.SyncRow
	if err := l.Applier.Store.Tx(l.Applier.Tenant, func(tx *store.Tx) error {
		var err error
		rows, err = tx.Bindings()
		return err
	}); err != nil {
		return nil, err
	}

	out := []oapi.IdMappingBinding{}
	for _, row := range rows {
		if !wanted[row.EntityType] || row.AgrirouterID == nil {
			continue
		}
		out = append(out, agmasync.Binding(row.LocalID, *row.AgrirouterID))
	}
	return out, nil
}

// dropRejected gives up the platform's half of every pair agrirouter would not
// record.
//
// The local claim has to go, whatever the cause. agrirouter's mapping is the one
// that decides what a send resolves to, so a row this platform keeps against a
// pair that was refused is a row it would go on sending through — either onto
// another endpoint's object or onto its own under a different identifier.
//
// What replaces the claim differs by cause and is not this code's business:
// AGRIROUTER_ID_ALREADY_BOUND says the platform matched two of its records to
// one canonical object and can resolve that itself, while
// LOCAL_ID_ALREADY_BOUND needs a person. Both are reported; neither is sent in
// the push below, since an unbound send would mint a duplicate canonical object
// for an entity that already has one — which is what the refs returned here are
// for.
func (l *Loader) dropRejected(rejected []oapi.IdMappingRejection) (map[ref]bool, error) {
	dropped := map[ref]bool{}
	if len(rejected) == 0 {
		return dropped, nil
	}

	err := l.Applier.Store.Tx(l.Applier.Tenant, func(tx *store.Tx) error {
		// A rejection names the pair and not its entity type, so the type comes
		// from the platform's own row for it — which is still there, the
		// rejection being agrirouter's refusal rather than a local change.
		//
		// Matched on the whole pair rather than on the local identifier. A
		// rejection restates the binding as submitted, and that binding came from
		// this table, so the pair identifies the row exactly — where the local
		// identifier alone would collide across types, and does so precisely in
		// the DUPLICATE_IN_REQUEST case, where one identifier appearing in
		// several pairs is what was rejected.
		rows, err := tx.Bindings()
		if err != nil {
			return err
		}
		type pair struct {
			localID      string
			agrirouterID uuid.UUID
		}
		typeOf := map[pair]agmasync.EntityType{}
		for _, row := range rows {
			if row.AgrirouterID == nil {
				continue
			}
			typeOf[pair{row.LocalID, *row.AgrirouterID}] = row.EntityType
		}

		// A rejected pair the platform cannot place is not passed over. The
		// local claim has to go and this is the only chance to drop it, so
		// leaving it standing would leave a record that goes on resolving
		// through a mapping agrirouter refused to record.
		var unplaced []error
		for _, r := range rejected {
			typ, ok := typeOf[pair{r.LocalId, r.AgrirouterId}]
			if !ok {
				unplaced = append(unplaced, fmt.Errorf(
					"%q/%s (%s)", r.LocalId, r.AgrirouterId, r.Reason))
				continue
			}
			if err := tx.Unbind(typ, r.LocalId); err != nil {
				return err
			}
			dropped[ref{Type: typ, LocalID: r.LocalId}] = true
		}
		if len(unplaced) > 0 {
			return fmt.Errorf(
				"sync: agrirouter rejected bindings the platform does not hold: %w",
				errors.Join(unplaced...))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dropped, nil
}

// push offers agrirouter everything the canonical set did not contain.
//
// That is what an unbound record is after reconciliation: one the platform holds
// and agrirouter has no canonical object for. In dependency order, because a
// reference travels as this platform's own identifier for its target and
// resolves against the mapping — a field sent before its farm names a farm
// agrirouter cannot resolve.
//
// A real product also re-sends here what its user changed while resolving
// conflicts. That is an ordinary [Applier.Send] against a bound record, and this
// sample, whose reconciliation takes the canonical object as it stands, has
// none.
func (l *Loader) push(ctx context.Context, res *LoadResult, skip map[ref]bool) error {
	for _, typ := range res.Types {
		var pending []string
		if err := l.Applier.Store.Tx(l.Applier.Tenant, func(tx *store.Tx) error {
			ids, err := tx.LocalIDs(typ)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if skip[ref{Type: typ, LocalID: id}] {
					continue
				}
				row, err := tx.SyncRow(typ, id)
				switch {
				case err == nil && row.Bound():
					continue
				case err == nil && row.Unbound:
					// A record the platform told agrirouter it no longer holds.
					// Offering it back would recreate, as a new canonical
					// object, the very entity it stopped claiming.
					continue
				case err != nil && !errors.Is(err, store.ErrNotFound):
					return err
				}

				record, err := tx.LoadRecord(typ, id)
				if err != nil {
					return err
				}
				// A record the platform archived and never shared stays local.
				// Creating a canonical object for it would announce an entity to
				// every other participant only to tell them it is gone.
				if record.Archived {
					continue
				}
				pending = append(pending, id)
			}
			return nil
		}); err != nil {
			return err
		}

		for _, id := range pending {
			if _, err := l.Applier.Send(ctx, typ, id); err != nil {
				return err
			}
			res.Sent++
		}
	}
	return nil
}
