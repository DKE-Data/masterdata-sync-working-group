package sync

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

// ByName recognises a canonical object as one of the platform's records when
// their names agree, ignoring case and surrounding space.
//
// It is a placeholder for whatever a product actually does, and deliberately a
// modest one: real reconciliation weighs addresses, registry numbers,
// geometries, and a user's answers. What it does illustrate is the shape the
// protocol needs from that step, whatever fills it —
//
//   - a record already bound to some canonical object is never a candidate. It
//     stands in a pair agrirouter holds, and matching it to a second object is
//     the non-unique mapping the confirmation would reject anyway.
//   - two records answering to one canonical object is not a match to pick
//     between. It is the n:1 granularity mismatch the specification pushes back
//     to the participating systems, so it matches nothing and asks for a person.
//   - a field boundary has no name and is not matched at all.
type ByName struct{}

// Recognise implements [Reconciler].
func (ByName) Recognise(
	tx *store.Tx, env agmasync.Envelope, entity oapi.Entity,
) (Recognition, error) {
	wanted, ok, err := nameOf(env.Type, entity)
	if err != nil || !ok {
		return Recognition{}, err
	}

	ids, err := tx.LocalIDs(env.Type)
	if err != nil {
		return Recognition{}, err
	}

	var found []string
	for _, id := range ids {
		row, err := tx.SyncRow(env.Type, id)
		switch {
		case err == nil && row.Bound():
			continue
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return Recognition{}, err
		}

		record, err := tx.LoadRecord(env.Type, id)
		if err != nil {
			return Recognition{}, err
		}
		held, ok := recordName(record)
		if ok && held == wanted {
			found = append(found, id)
		}
	}

	switch len(found) {
	case 0:
		return Recognition{}, nil
	case 1:
		return Recognition{LocalID: found[0]}, nil
	default:
		// Two records answering to one canonical object, which is not a match to
		// pick between. Saying only that a person is needed would leave this
		// reading as "not held" — an instruction to create a record — so the
		// endpoint would answer an object it could not identify with a third
		// copy of it. It blocks instead, and the load stops for the person it
		// just asked for.
		return Recognition{AwaitingUser: true, Blocked: true}, nil
	}
}

// nameOf reads the natural key this sample matches on out of a delivered
// object.
func nameOf(typ agmasync.EntityType, entity oapi.Entity) (string, bool, error) {
	raw, err := entity.MarshalJSON()
	if err != nil {
		return "", false, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", false, err
	}
	return nameFrom(typ, fields)
}

func recordName(record store.Record) (string, bool) {
	name, _, err := nameFrom(record.EntityType, record.Modelled)
	if err != nil {
		return "", false
	}
	return name, name != ""
}

func nameFrom(
	typ agmasync.EntityType, fields map[string]json.RawMessage,
) (string, bool, error) {
	read := func(key string) (string, error) {
		raw, ok := fields[key]
		if !ok {
			return "", nil
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return "", nil
		}
		return v, nil
	}

	var parts []string
	switch typ {
	case agmasync.TypePerson:
		last, err := read("last_name")
		if err != nil {
			return "", false, err
		}
		first, err := read("first_name")
		if err != nil {
			return "", false, err
		}
		parts = []string{last, first}
	case agmasync.TypeFieldBoundary:
		// No natural key, so nothing to recognise it by.
		return "", false, nil
	default:
		name, err := read("name")
		if err != nil {
			return "", false, err
		}
		parts = []string{name}
	}

	key := strings.ToLower(strings.TrimSpace(strings.Join(parts, " ")))
	return key, key != "", nil
}
