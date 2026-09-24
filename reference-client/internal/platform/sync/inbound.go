// Package sync applies what agrirouter delivers and sends what the platform
// changes.
//
// The two directions are deliberately not symmetric, and neither is the way
// their results are handled. A delivered object and the response to the
// platform's own write carry the same thing — the resulting canonical object —
// and both are applied the same way, through [Applier.Apply]. A write response
// is not an acknowledgement: it is the only channel on which the writing
// endpoint learns the revision it just produced, since origin suppression keeps
// that revision off its own stream, and after a merge it holds content the
// platform never sent.
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

// LocalIDs issues the platform's own identifiers for objects it is told about
// but does not hold.
//
// Most systems cannot choose their own primary keys, which is the whole reason
// binding exists: the platform creates the record, the store hands back
// whatever identifier it minted, and the platform then tells agrirouter that
// this is what it calls the object.
type LocalIDs interface {
	New(typ agmasync.EntityType) string
}

// Applier applies canonical objects to the platform's store.
//
// One Applier serves one tenant, through the endpoint that tenant is onboarded
// as. A product holding several tenants holds several of these over one store,
// and they share what that store holds: the identifier mapping is keyed by the
// application, so a record one tenant has bound is bound for all of them. What
// the tenant decides is which endpoint acts, and which of them shows the record
// to a user. What is not shared is the endpoint, which is why there is an
// Applier per tenant rather than one for the product.
type Applier struct {
	Store *store.Store

	// Tenant is the product's own key for the tenancy this applier works in —
	// what its users switch between, and what agrirouter names by UUID in
	// `tenantId` on everything it delivers. It decides
	// whose holdings a delivered record joins and which holdings a load offers
	// back, not which records or bindings are visible: those are the platform's.
	// See schema.sql.
	Tenant string

	Endpoint *agmasync.Endpoint
	IDs      LocalIDs
}

// Outcome says what applying one object did, which is mostly of interest to the
// caller because a created object has to be bound afterwards.
type Outcome struct {
	// LocalID is the platform's identifier for the object, minted here if the
	// object was new.
	LocalID string

	// Created is true where the platform did not hold the object.
	Created bool

	// NeedsBinding is true where the platform has just created a record for a
	// canonical object and has not yet told agrirouter what it calls it. Until
	// it does, it must not send that object: an unbound send does not resolve
	// and mints a second canonical object for the same entity.
	NeedsBinding bool

	// Superseded is true where the object was older than what the platform
	// already held and was therefore not applied.
	Superseded bool

	// Ignored is true where the object was inactive and unrecognised, and the
	// platform correctly created nothing.
	Ignored bool

	// Matched is true where the platform recognised the object as one of its
	// own records rather than creating one — the reconciliation of an initial
	// load, and never an ordinary delivery. A matched object needs binding for
	// the same reason a created one does: agrirouter holds no identifier for it
	// yet.
	Matched bool

	// AwaitingUser is true where deciding what the object was cost a person's
	// attention, or would have. It is reported to agrirouter as one bit for the
	// whole load; see [Loader].
	AwaitingUser bool

	// Blocked is true where the recogniser could not decide what the object was
	// and would not guess. Nothing was created, nothing was applied, and nothing
	// was bound: the object is left for a person, and [Loader] stops short of
	// declaring the load reconciled while any are outstanding.
	Blocked bool
}

// Apply applies one canonical object, and records the delivery position with
// it.
//
// Both happen in one transaction. A position must be derived from what the
// participant has durably applied, so committing the object and then advancing
// the position separately would lose the object on any crash in between —
// agrirouter would consider it delivered and never send it again.
//
// Pass an empty position for a write response, which carries none.
//
// No recognition is attempted: an object arriving without a localId in steady
// state is one the platform does not hold, and is created and bound. Matching it
// against existing records is the work of an initial load, where the platform
// has declared that it does not know what it holds — see [Loader].
func (a *Applier) Apply(entity oapi.Entity, position string) (Outcome, error) {
	return a.apply(entity, position, nil)
}

func (a *Applier) apply(
	entity oapi.Entity, position string, recognise Reconciler,
) (Outcome, error) {
	envelope, err := agmasync.EnvelopeOf(entity)
	if err != nil {
		return Outcome{}, err
	}
	if envelope.AgrirouterId == nil {
		return Outcome{}, fmt.Errorf("sync: a delivered object carries no agrirouterId")
	}

	var outcome Outcome
	err = a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		outcome, err = a.applyIn(tx, envelope, entity, recognise)
		if err != nil {
			return err
		}
		return tx.SetPosition(position)
	})
	return outcome, err
}

func (a *Applier) applyIn(
	tx *store.Tx, envelope agmasync.Envelope, entity oapi.Entity, recognise Reconciler,
) (Outcome, error) {
	typ := envelope.Type
	active := envelope.Active == nil || *envelope.Active

	// localId is always the near end of the transfer. On a delivered object it
	// is the platform's own identifier where agrirouter holds one, and absent
	// where it does not — and that absence is meaningful: it says agrirouter
	// does not believe this platform holds the object.
	localID := ""
	if envelope.LocalId != nil {
		localID = *envelope.LocalId
	}

	// Trust the mapping over the delivered identifier where both exist: the
	// platform's own table is what it has to stay consistent with.
	if row, err := tx.SyncRowByAgrirouterID(typ, *envelope.AgrirouterId); err == nil {
		localID = row.LocalID
	} else if !errors.Is(err, store.ErrNotFound) {
		return Outcome{}, err
	}

	if localID == "" {
		return a.applyUnheldObject(tx, envelope, entity, active, recognise)
	}

	// Apply is guarded by revision. Within the stream, order suffices: a later
	// frame supersedes an earlier one. Across the two channels it does not,
	// because a write response may be processed after a later stream frame has
	// already been applied — so an object whose revision is lower than the one
	// held must not be applied over it.
	row, err := tx.SyncRow(typ, localID)
	switch {
	case err == nil:
		if row.Revision != nil && envelope.Revision != nil && *envelope.Revision < *row.Revision {
			return Outcome{LocalID: localID, Superseded: true}, nil
		}
	case errors.Is(err, store.ErrNotFound):
		// No bookkeeping yet for a record we do hold: this is the first time
		// agrirouter has told us about it.
	default:
		return Outcome{}, err
	}

	if err := a.write(tx, typ, localID, entity, envelope, active); err != nil {
		return Outcome{}, err
	}
	return Outcome{LocalID: localID}, nil
}

// applyUnheldObject handles a delivery for an object agrirouter holds no
// identifier of ours for, which is what an absent localId says.
//
// Whether the platform holds it anyway is a different question, and only
// reconciliation asks it: during an initial load the recogniser is consulted
// first, and what it answers decides between matching the object to an existing
// record, creating one, and — for an inactive object — doing neither.
func (a *Applier) applyUnheldObject(
	tx *store.Tx, env agmasync.Envelope, entity oapi.Entity, active bool,
	recognise Reconciler,
) (Outcome, error) {
	var known Recognition
	if recognise != nil {
		var err error
		known, err = recognise.Recognise(tx, env, entity)
		if err != nil {
			return Outcome{}, err
		}
	}

	switch {
	case known.Blocked:
		// The recogniser could not tell what this object is and declined to
		// guess. Creating a record anyway is the one thing that must not happen
		// here: it is what turns an undecidable object into a duplicate, and a
		// duplicate is what the whole reconciliation exists to avoid.
		//
		// Whatever the recogniser wrote for the person who will decide is
		// committed with this transaction; the object itself is not applied and
		// not bound. [Loader] leaves the load short of reconciled while any are
		// outstanding, and the object is asked for again once they have answered.
		return Outcome{Blocked: true, AwaitingUser: known.AwaitingUser}, nil

	case known.LocalID != "" && !active:
		// A deactivated object the platform does recognise is bound and its own
		// copy marked inactive. Binding a dead object is worth doing: an unbound
		// local copy is precisely what gets offered back as new later, and
		// agrirouter would then mint a second, active canonical object for an
		// entity a user archived. The canonical content is not applied over the
		// record — what the set says about the entity is that it is gone.
		if err := tx.SetArchived(env.Type, known.LocalID, true); err != nil {
			return Outcome{}, err
		}
		if err := tx.PutSyncRow(store.SyncRow{
			EntityType:   env.Type,
			LocalID:      known.LocalID,
			AgrirouterID: env.AgrirouterId,
			Revision:     env.Revision,
			TenantID:     env.TenantId,
		}); err != nil {
			return Outcome{}, err
		}
		return Outcome{
			LocalID: known.LocalID, Matched: true, NeedsBinding: true,
			AwaitingUser: known.AwaitingUser,
		}, nil

	case known.LocalID != "":
		// Recognised and current: the canonical object is taken over the
		// platform's own record. A product with more to lose resolves this
		// attribute by attribute with its user — which is what AwaitingUser is
		// for — but the direction is not in doubt, agrirouter being the source of
		// truth for what it holds.
		if err := a.write(tx, env.Type, known.LocalID, entity, env, active); err != nil {
			return Outcome{}, err
		}
		return Outcome{
			LocalID: known.LocalID, Matched: true, NeedsBinding: true,
			AwaitingUser: known.AwaitingUser,
		}, nil

	case !active:
		// Unrecognised and inactive: ignored, and nothing is created. The rule
		// that an absent localId means "create it locally and bind" is about
		// objects the platform is expected to hold, and does not extend to one
		// that is already deactivated: creating a record for an entity the world
		// considers gone would be inventing data.
		return Outcome{Ignored: true, AwaitingUser: known.AwaitingUser}, nil
	}

	localID := a.IDs.New(env.Type)
	if err := a.write(tx, env.Type, localID, entity, env, active); err != nil {
		return Outcome{}, err
	}
	return Outcome{
		LocalID: localID, Created: true, NeedsBinding: true,
		AwaitingUser: known.AwaitingUser,
	}, nil
}

func (a *Applier) write(
	tx *store.Tx, typ agmasync.EntityType, localID string,
	entity oapi.Entity, envelope agmasync.Envelope, active bool,
) error {
	record, err := store.FromEntity(typ, entity)
	if err != nil {
		return err
	}
	record.Archived = !active

	if err := tx.UpsertRecord(record, localID); err != nil {
		return err
	}
	return tx.PutSyncRow(store.SyncRow{
		EntityType:   typ,
		LocalID:      localID,
		AgrirouterID: envelope.AgrirouterId,
		Revision:     envelope.Revision,
		TenantID:     envelope.TenantId,
	})
}

// Bind tells agrirouter what the platform calls an object it has just created,
// and is the step that makes the record sendable.
//
// Call it for every outcome carrying NeedsBinding. Until the platform has
// bound, it must not send that object: the send does not resolve against the
// mapping and creates a second canonical object for the same entity.
func (a *Applier) Bind(
	ctx context.Context, typ agmasync.EntityType, localID string, agrirouterID uuid.UUID,
) error {
	return a.Endpoint.Bind(ctx, typ, localID, agrirouterID)
}
