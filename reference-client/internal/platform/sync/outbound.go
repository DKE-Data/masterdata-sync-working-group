package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
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
	var record store.Record
	var row store.SyncRow
	var bound bool

	err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		record, err = tx.LoadRecord(typ, localID)
		if err != nil {
			return err
		}
		row, err = tx.SyncRow(typ, localID)
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

	// A record whose binding was dropped must not be sent under its own
	// identifier: the send would not resolve and would mint a second canonical
	// object for the same entity. What replaces it is the object's next
	// delivery, which arrives carrying no localId and is created and bound as
	// something the platform does not hold.
	if row.Unbound {
		return Outcome{}, fmt.Errorf(
			"sync: %s %q is unbound and must be re-created rather than sent", typ, localID)
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
	// may arrive after a later stream frame has already been applied.
	return a.Apply(result, "")
}

// Deactivate tells agrirouter that a record has been deactivated locally, and
// applies the result.
func (a *Applier) Deactivate(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (Outcome, error) {
	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(typ, localID)
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

// Unbind declares that the platform no longer holds an object, and drops its
// own half of the correspondence.
//
// agrirouter is told first. If that fails the local mapping is left alone,
// which is the safe way round: a platform that had dropped the mapping locally
// but failed to say so would go on believing it holds an object agrirouter
// still maps to it, and its next send would resolve to the old pair.
func (a *Applier) Unbind(
	ctx context.Context, typ agmasync.EntityType, localID string,
) error {
	var row store.SyncRow
	if err := a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(typ, localID)
		return err
	}); err != nil {
		return err
	}
	if !row.Bound() {
		return nil
	}

	if err := a.Endpoint.Unbind(ctx, typ, localID, *row.AgrirouterID); err != nil {
		return err
	}
	return a.Store.Tx(a.Tenant, func(tx *store.Tx) error {
		return tx.Unbind(typ, localID)
	})
}
