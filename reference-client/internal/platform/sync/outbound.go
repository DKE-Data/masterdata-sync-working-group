package sync

import (
	"context"
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
// ceremony: on a create it carries the assigned agrirouterId, and where
// agrirouter merged the write against a concurrent change it carries content
// the platform never sent and a revision that is not base + 1.
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
		row, err = tx.SyncRow(a.Endpoint.TenantID(), typ, localID)
		switch {
		case err == nil:
			bound = row.Bound()
			return nil
		case errors.Is(err, store.ErrNotFound):
			// Never sent, and never told about: a create.
			return nil
		default:
			return err
		}
	})
	if err != nil {
		return Outcome{}, err
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
	return a.apply(result, "", nil, selected)
}

// Deactivate tells agrirouter that a record has been deactivated locally, and
// applies the result.
func (a *Applier) Deactivate(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (Outcome, error) {
	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(a.Endpoint.TenantID(), typ, localID)
		return err
	}); err != nil {
		return Outcome{}, err
	}
	if !row.Bound() {
		return Outcome{}, fmt.Errorf(
			"sync: %s %q was never sent, so there is nothing to deactivate", typ, localID)
	}

	result, err := a.Endpoint.Deactivate(ctx, typ, localID, row.Revision)
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
	tenantID := a.Endpoint.TenantID()
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		for _, e := range entries {
			if err := tx.DropSyncRow(tenantID, typ, e.LocalId); err != nil {
				return err
			}
		}
		for _, e := range entries {
			if e.AgrirouterId.IsNull() {
				continue
			}
			id := e.AgrirouterId.MustGet()
			if err := tx.PutSyncRow(store.SyncRow{
				TenantID: tenantID, EntityType: typ, LocalID: e.LocalId, AgrirouterID: &id,
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
