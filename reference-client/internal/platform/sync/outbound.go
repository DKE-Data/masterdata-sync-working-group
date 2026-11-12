package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

// Send sends one of the platform's records to agrirouter and applies what comes
// back.
//
// The base revision is the one the platform holds, which is what it edited
// from. A record the platform has never sent has none, and that absence is what
// says "this is a create"; a record it holds a revision for must send it, since
// a missing base on an update is refused outright.
//
// The response is applied exactly as a delivered object is. That is not
// ceremony: on a create it carries the assigned agrirouterId, and on a partial
// write the attributes it left out; on a no-op the revision is the current one,
// which need not be base + 1.
func (a *Applier) Send(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (Outcome, error) {
	return a.send(ctx, typ, localID, nil)
}

// send is [Applier.Send] against the selection the caller holds, where it
// passes one: see [Applier.unselectedRefs].
func (a *Applier) send(
	ctx context.Context, typ agmasync.EntityType, localID string, selected []agmasync.EntityType,
) (Outcome, error) {
	var record store.Record
	var row store.SyncRow
	var bound bool
	var dropped map[string]json.RawMessage

	err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		record, err = tx.LoadRecord(typ, localID)
		if err != nil {
			return err
		}
		// A reference to a type not selected is left out rather than sent: its
		// target is not bound here, so it would not resolve, and leaving it out
		// keeps what agrirouter holds for it.
		unselected, err := a.unselectedRefs(tx, typ, selected)
		if err != nil {
			return err
		}
		for attribute := range unselected {
			delete(record.Modelled, attribute)
		}
		// A weak reference resolves on send like any other, so one whose
		// target this endpoint has not sent yet is left out, which keeps what
		// agrirouter holds for it. Sending the target sends this record again.
		if dropped, err = a.dropUnboundWeakRefs(tx, &record); err != nil {
			return err
		}
		row, err = tx.SyncRow(a.Namespace(), typ, localID)
		switch {
		case err == nil:
			bound = row.Bound()
			return nil
		case errors.Is(err, store.ErrNotFound):
			// Never sent through this endpoint: a create, unless a sibling
			// has made it a canonical object already.
			return nil
		default:
			return err
		}
	})
	if err != nil {
		return Outcome{}, err
	}
	if !bound && !row.Unbound {
		if row, bound, err = a.bindAsSibling(ctx, typ, localID); err != nil {
			return Outcome{}, err
		}
	}

	// A record whose binding agrirouter refused must not be sent under its own
	// identifier: the send would not resolve and would mint a second canonical
	// object for the same entity. It waits for a person to bind or correct it.
	if row.Unbound {
		return Outcome{}, fmt.Errorf(
			"sync: %s %q is unbound and must be bound or corrected rather than sent", typ, localID)
	}

	entity, err := record.ToEntity(localID)
	if err != nil {
		return Outcome{}, err
	}

	var base *int
	if bound {
		base = row.Revision
	}

	result, err := a.Endpoint.Put(ctx, entity, base)
	if err != nil {
		return Outcome{}, err
	}

	// No position: a write response is not a delivery on the stream and carries
	// none. Applying it still has to go through the revision guard, because it
	// may arrive after a later stream frame has already been applied, and
	// against the same selection, or the reference left out would be cleared.
	outcome, err := a.apply(result, "", nil, selected)
	if err != nil {
		return outcome, err
	}
	// The response is applied as a whole object, which cleared the weak
	// references left out of the write. They are the platform's own and are
	// put back.
	if err := a.restoreWeakRefs(typ, localID, dropped); err != nil {
		return outcome, err
	}
	if bound {
		return outcome, nil
	}
	// A create: records already sent that name this one by a weak reference
	// left it out, and are sent again now that it resolves. For a field and a
	// new active boundary that is the third write.
	return outcome, a.sendWeakReferrers(ctx, typ, localID, selected)
}

// dropUnboundWeakRefs removes from record each weak reference whose target
// this endpoint has not sent or bound, and returns what it removed.
func (a *Applier) dropUnboundWeakRefs(tx *store.Tx, record *store.Record) (map[string]json.RawMessage, error) {
	dropped := map[string]json.RawMessage{}
	for attribute, raw := range record.Modelled {
		if !store.IsWeak(record.EntityType, attribute) || string(raw) == "null" {
			continue
		}
		var ref oapi.WeakRef
		if err := json.Unmarshal(raw, &ref); err != nil {
			return nil, fmt.Errorf("sync: reading %s: %w", attribute, err)
		}
		id, err := agmasync.IdentityOf(ref.Id)
		if err != nil {
			return nil, fmt.Errorf("sync: reading %s: %w", attribute, err)
		}
		if id.LocalID == nil {
			continue
		}
		row, err := tx.SyncRow(a.Namespace(), store.References[record.EntityType][attribute], *id.LocalID)
		switch {
		case errors.Is(err, store.ErrNotFound) || (err == nil && !row.Bound()):
			dropped[attribute] = raw
			delete(record.Modelled, attribute)
		case err != nil:
			return nil, err
		}
	}
	return dropped, nil
}

// restoreWeakRefs puts the weak references dropUnboundWeakRefs left out back
// on the platform's record.
func (a *Applier) restoreWeakRefs(typ agmasync.EntityType, localID string, dropped map[string]json.RawMessage) error {
	if len(dropped) == 0 {
		return nil
	}
	return a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		record, err := tx.LoadRecord(typ, localID)
		if err != nil {
			return err
		}
		for attribute, raw := range dropped {
			record.Modelled[attribute] = raw
		}
		return tx.UpsertRecord(record, localID)
	})
}

// sendWeakReferrers sends again the records this endpoint has already sent
// whose weak reference names the record just created.
func (a *Applier) sendWeakReferrers(
	ctx context.Context, typ agmasync.EntityType, localID string, selected []agmasync.EntityType,
) error {
	var referrers []store.WeakReferrer
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		all, err := tx.WeakReferrers(typ, localID)
		if err != nil {
			return err
		}
		for _, r := range all {
			row, err := tx.SyncRow(a.Namespace(), r.EntityType, r.LocalID)
			switch {
			case err == nil && row.Bound():
				referrers = append(referrers, r)
			case err != nil && !errors.Is(err, store.ErrNotFound):
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, r := range referrers {
		if _, err := a.send(ctx, r.EntityType, r.LocalID, selected); err != nil {
			return fmt.Errorf("sync: sending %s %q again for its weak reference: %w", r.EntityType, r.LocalID, err)
		}
	}
	return nil
}

// Archive tells agrirouter that a record has been archived locally, and
// applies the result.
func (a *Applier) Archive(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (Outcome, error) {
	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(a.Namespace(), typ, localID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}); err != nil {
		return Outcome{}, err
	}
	if !row.Bound() && !row.Unbound {
		var err error
		if row, _, err = a.bindAsSibling(ctx, typ, localID); err != nil {
			return Outcome{}, err
		}
	}
	if !row.Bound() {
		return Outcome{}, fmt.Errorf(
			"sync: %s %q was never sent, so there is nothing to archive", typ, localID)
	}

	result, err := a.Endpoint.Archive(ctx, typ, localID, row.Revision)
	if err != nil {
		return Outcome{}, err
	}
	return a.Apply(result, "")
}

// Unarchive tells agrirouter that a record archived earlier is no longer
// archived locally, and applies the result, which marks the local copy not
// archived again.
// It is the only way back: a send never changes the state.
func (a *Applier) Unarchive(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (Outcome, error) {
	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(a.Namespace(), typ, localID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}); err != nil {
		return Outcome{}, err
	}
	if !row.Bound() && !row.Unbound {
		var err error
		if row, _, err = a.bindAsSibling(ctx, typ, localID); err != nil {
			return Outcome{}, err
		}
	}
	if !row.Bound() {
		return Outcome{}, fmt.Errorf(
			"sync: %s %q was never sent, so there is nothing to unarchive", typ, localID)
	}

	result, err := a.Endpoint.Unarchive(ctx, typ, localID, row.Revision)
	if err != nil {
		return Outcome{}, err
	}
	return a.Apply(result, "")
}

// Correct replaces bindings the platform's user found wrong, and fetches the
// objects now bound so the records take their content and revision.
//
// agrirouter is told first and applies the set whole or not at all, so the
// local rows are rewritten only once it has. Every listed row is dropped before
// any is written, since the swap a correction usually is would otherwise pass
// through a state in which one object has two local names.
//
// A record corrected to none is left as never sent: it matched nothing, and its
// next send is a create. Content written through the wrong binding stands at
// agrirouter; fixing it is an ordinary write once the request has delivered.
func (a *Applier) Correct(
	ctx context.Context, typ agmasync.EntityType, entries []oapi.IdMappingCorrectionEntry,
) error {
	if err := a.Endpoint.Correct(ctx, typ, entries); err != nil {
		return err
	}
	ns := a.Namespace()
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		for _, e := range entries {
			if err := tx.DropSyncRow(ns, typ, e.LocalId); err != nil {
				return err
			}
		}
		for _, e := range entries {
			if e.AgrirouterId.IsNull() {
				continue
			}
			id := e.AgrirouterId.MustGet()
			if err := tx.PutSyncRow(store.SyncRow{
				Endpoint: ns, EntityType: typ, LocalID: e.LocalId, AgrirouterID: &id,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	for _, e := range entries {
		if e.AgrirouterId.IsNull() {
			continue
		}
		if err := a.Endpoint.Request(ctx, typ, e.AgrirouterId.MustGet()); err != nil {
			return fmt.Errorf("requesting %s %q after correcting it: %w", typ, e.LocalId, err)
		}
	}
	return nil
}

// bindAsSibling binds a record this endpoint has never sent where a sibling
// endpoint in the same agrirouter tenant already has: the record is one store's,
// so it is that canonical object here too. Sending it unbound would not resolve
// and would mint a second canonical object for it.
//
// The specification has a participant bind on its other entitled endpoints as
// soon as it learns the agrirouterId. This one binds at the latest moment that
// still prevents the duplicate — before the endpoint's own first send — or on
// the sibling's delivery, whichever comes first (see [Applier.Apply]).
//
// It reports the endpoint's new row, and whether there was a sibling's pair to
// take, holding the sibling's revision as the base the record was last seen at.
func (a *Applier) bindAsSibling(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (store.SyncRow, bool, error) {
	var sibling store.SyncRow
	err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		sibling, err = tx.SiblingRowByLocalID(a.Namespace(), typ, localID)
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return store.SyncRow{}, false, nil
	}
	if err != nil {
		return store.SyncRow{}, false, err
	}

	// agrirouter first, as everywhere else: a pair the platform holds and
	// agrirouter does not is one the next send resolves through and finds
	// nothing.
	if err := a.Endpoint.Bind(ctx, typ, localID, *sibling.AgrirouterID); err != nil {
		return store.SyncRow{}, false, err
	}
	row := store.SyncRow{
		Endpoint: a.Namespace(), EntityType: typ, LocalID: localID,
		AgrirouterID: sibling.AgrirouterID, Revision: sibling.Revision,
	}
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		return tx.PutSyncRow(row)
	}); err != nil {
		return store.SyncRow{}, false, err
	}
	return row, true, nil
}
