package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// Record is one of the platform's own records, as the sync code handles it.
//
// It is deliberately not an oapi entity. The platform stores what it models in
// its own columns and everything else in Unmodelled, and turning one into the
// other is the codec's job — which is where a real integration's mapping work
// actually is.
type Record struct {
	EntityType agmasync.EntityType
	LocalID    string
	Archived   bool

	// Modelled holds the attributes this platform has columns for, keyed by
	// their protocol names.
	Modelled map[string]json.RawMessage

	// Unmodelled holds the attributes it does not, exactly as they arrived.
	//
	// Keeping them is not optional: the specification requires a participant to
	// preserve what it does not understand and relay it unchanged. A platform
	// that drops them silently degrades every other participant's data each
	// time it touches an object.
	Unmodelled map[string]json.RawMessage
}

// columns names the protocol attributes each entity type has real columns for.
// Everything else on a delivered object falls into Unmodelled.
var columns = map[agmasync.EntityType][]string{
	agmasync.TypeOrganization: {"name", "commercial_registry_number", "address"},
	agmasync.TypePerson:       {"last_name", "first_name", "title"},
	agmasync.TypeFarm:         {"name", "owner", "address"},
	agmasync.TypeField:        {"name", "area", "farm"},
	agmasync.TypeFieldBoundary: {
		"boundary_type", "creation_method", "boundary",
	},
}

// FromEntity turns a delivered entity into the platform's own shape.
//
// The envelope is stripped: type, identifiers, revision and the rest are
// agrirouter's bookkeeping and belong in agmasync_object, not in the platform's
// tables. What remains is divided by whether this platform models it.
func FromEntity(typ agmasync.EntityType, entity oapi.Entity) (Record, error) {
	raw, err := entity.MarshalJSON()
	if err != nil {
		return Record{}, fmt.Errorf("reading entity: %w", err)
	}

	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return Record{}, fmt.Errorf("reading entity: %w", err)
	}

	rawType, ok := all["type"]
	if !ok {
		return Record{}, fmt.Errorf("entity has no type, want %q", typ)
	}
	var objectType agmasync.EntityType
	if err := json.Unmarshal(rawType, &objectType); err != nil {
		return Record{}, fmt.Errorf("reading entity type: %w", err)
	}
	if objectType != typ {
		return Record{}, fmt.Errorf("entity is of type %q, not %q", objectType, typ)
	}

	envelope := map[string]bool{
		"type": true, "agrirouter_id": true, "local_id": true, "active": true,
		"revision": true, "modified_at": true, "tenant_id": true, "source_endpoint_id": true,
	}
	modelled := map[string]bool{}
	for _, name := range columns[typ] {
		modelled[name] = true
	}

	out := Record{
		EntityType: typ,
		Modelled:   map[string]json.RawMessage{},
		Unmodelled: map[string]json.RawMessage{},
	}
	for key, value := range all {
		switch {
		case envelope[key]:
			continue
		case modelled[key]:
			out.Modelled[key] = value
		default:
			out.Unmodelled[key] = value
		}
	}
	return out, nil
}

// ToEntity is FromEntity in reverse: the platform's own record as an entity to send.
//
// The unmodelled attributes go back out exactly as they came in, which is what
// relaying them unchanged means in practice.
func (r Record) ToEntity(localID string) (oapi.Entity, error) {
	fields := map[string]json.RawMessage{}
	for k, v := range r.Unmodelled {
		fields[k] = v
	}
	for k, v := range r.Modelled {
		fields[k] = v
	}

	id, err := json.Marshal(localID)
	if err != nil {
		return oapi.Entity{}, err
	}
	fields["local_id"] = id

	active, err := json.Marshal(!r.Archived)
	if err != nil {
		return oapi.Entity{}, err
	}
	fields["active"] = active

	typ, err := json.Marshal(string(r.EntityType))
	if err != nil {
		return oapi.Entity{}, err
	}
	fields["type"] = typ

	raw, err := json.Marshal(fields)
	if err != nil {
		return oapi.Entity{}, err
	}

	var entity oapi.Entity
	if err := entity.UnmarshalJSON(raw); err != nil {
		return oapi.Entity{}, fmt.Errorf("building entity: %w", err)
	}
	return entity, nil
}

// tableOf maps an entity type to the platform's table for it.
func tableOf(typ agmasync.EntityType) (string, error) {
	switch typ {
	case agmasync.TypeOrganization:
		return "organization", nil
	case agmasync.TypePerson:
		return "person", nil
	case agmasync.TypeFarm:
		return "farm", nil
	case agmasync.TypeField:
		return "field", nil
	case agmasync.TypeFieldBoundary:
		return "field_boundary", nil
	default:
		return "", fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, typ)
	}
}

// columnValues maps the modelled attributes onto the table's columns.
//
// This is the mapping work an integration actually has to do, and it is per
// type because the platform's schema is its own rather than a mirror of the
// protocol's.
//
// It runs in a transaction because references have to be resolved against the
// platform's own mapping; see [Tx.resolveRef].
func (t *Tx) columnValues(r Record) (map[string]any, error) {
	out := map[string]any{}

	var errs []error
	str := func(key, column string) {
		raw, ok := r.Modelled[key]
		if !ok {
			return
		}
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			errs = append(errs, fmt.Errorf("reading %s: %w", key, err))
			return
		}
		if v != nil {
			out[column] = *v
		}
	}

	switch r.EntityType {
	case agmasync.TypeOrganization:
		str("name", "name")
		str("commercial_registry_number", "commercial_registry_number")
		city, country, err := addressParts(r.Modelled["address"])
		if err != nil {
			return nil, err
		}
		out["city"], out["country"] = city, country
	case agmasync.TypePerson:
		str("last_name", "last_name")
		str("first_name", "first_name")
		str("title", "title")
	case agmasync.TypeFarm:
		str("name", "name")
		city, _, err := addressParts(r.Modelled["address"])
		if err != nil {
			return nil, err
		}
		out["city"] = city
		// A party slot admits either, and a delivered party reference says
		// which, so both are candidates only where it does not.
		ownerType, ownerLocal, err := t.resolveRef(
			r.Modelled["owner"], agmasync.TypeOrganization, agmasync.TypePerson)
		if err != nil {
			return nil, err
		}
		out["owner_type"], out["owner_local_id"] = ownerType, ownerLocal
	case agmasync.TypeField:
		str("name", "name")
		if raw, ok := r.Modelled["area"]; ok {
			var area *float64
			if err := json.Unmarshal(raw, &area); err != nil {
				return nil, fmt.Errorf("reading area: %w", err)
			}
			if area != nil {
				out["area"] = *area
			}
		}
		_, farmLocal, err := t.resolveRef(r.Modelled["farm"], agmasync.TypeFarm)
		if err != nil {
			return nil, err
		}
		out["farm_local_id"] = farmLocal
	case agmasync.TypeFieldBoundary:
		str("boundary_type", "boundary_type")
		str("creation_method", "creation_method")
		if raw, ok := r.Modelled["boundary"]; ok {
			out["boundary"] = string(raw)
		}
	default:
		return nil, fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, r.EntityType)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

func addressParts(raw json.RawMessage) (any, any, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var address struct {
		City    *string `json:"city"`
		Country *string `json:"country"`
	}
	if err := json.Unmarshal(raw, &address); err != nil {
		return nil, nil, fmt.Errorf("reading address: %w", err)
	}
	var city, country any
	if address.City != nil {
		city = *address.City
	}
	if address.Country != nil {
		country = *address.Country
	}
	return city, country, nil
}

// resolveRef turns a delivered reference into the platform's own foreign key
// for its target, and reports the target's entity type where the reference
// names one.
//
// A delivered reference carries the receiving participant's own identifier for
// the target only where agrirouter held one when the frame was rendered — which,
// for the whole of a first initial load, it does not: every object in the set is
// rendered before the endpoint has bound any of it. So the localId is a
// shortcut, and agrirouterId is what a reference actually resolves through.
//
// This is why delivery order matters. A referenced object precedes the objects
// referencing it, so by the time the reference is applied the platform has
// already applied its target and holds the row that answers this lookup. A
// reference that still resolves to nothing is left null rather than guessed at:
// the platform does not hold the target, and [Applier] will create and bind it
// when it arrives.
func (t *Tx) resolveRef(
	raw json.RawMessage, candidates ...agmasync.EntityType,
) (any, any, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var ref struct {
		Type         *string    `json:"type"`
		LocalID      *string    `json:"local_id"`
		AgrirouterID *uuid.UUID `json:"agrirouter_id"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return nil, nil, fmt.Errorf("reading reference: %w", err)
	}

	var kind any
	if ref.Type != nil {
		kind = *ref.Type
		candidates = []agmasync.EntityType{agmasync.EntityType(*ref.Type)}
	}
	if ref.LocalID != nil {
		return kind, *ref.LocalID, nil
	}
	if ref.AgrirouterID == nil {
		return kind, nil, nil
	}

	for _, typ := range candidates {
		row, err := t.SyncRowByAgrirouterID(typ, *ref.AgrirouterID)
		switch {
		case err == nil:
			if kind == nil {
				kind = string(typ)
			}
			return kind, row.LocalID, nil
		case errors.Is(err, ErrNotFound):
			continue
		default:
			return nil, nil, err
		}
	}
	return kind, nil, nil
}

// UpsertRecord writes one of the platform's records and records that the
// transaction's tenant holds it.
//
// The record itself is the platform's, not the tenant's: the same local
// identifier reached from another tenant is the same record, and writing
// through that one updates this record rather than a copy of it. What the
// tenant adds is the membership row — which is what a listing walks, and what a
// deletion removes.
func (t *Tx) UpsertRecord(r Record, localID string) error {
	table, err := tableOf(r.EntityType)
	if err != nil {
		return err
	}
	values, err := t.columnValues(r)
	if err != nil {
		return err
	}

	unmodelled, err := json.Marshal(r.Unmodelled)
	if err != nil {
		return fmt.Errorf("encoding unmodelled attributes: %w", err)
	}
	values["archived"] = boolToInt(r.Archived)
	values["unmodelled"] = string(unmodelled)

	names := []string{"local_id"}
	placeholders := []string{"?"}
	args := []any{localID}
	assignments := []string{}
	for name, value := range values {
		names = append(names, name)
		placeholders = append(placeholders, "?")
		args = append(args, value)
		assignments = append(assignments, name+" = excluded."+name)
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (local_id) DO UPDATE SET %s",
		table, join(names), join(placeholders), join(assignments))
	if _, err := t.tx.Exec(query, args...); err != nil {
		return fmt.Errorf("writing %s: %w", table, err)
	}
	return t.hold(r.EntityType, localID)
}

// hold records that the transaction's tenant holds a record. It is idempotent:
// a tenant that already holds it is not a second holder.
func (t *Tx) hold(typ agmasync.EntityType, localID string) error {
	if _, err := t.tx.Exec(`
		INSERT INTO tenant_entity (tenant_id, entity_type, local_id) VALUES (?, ?, ?)
		ON CONFLICT (tenant_id, entity_type, local_id) DO NOTHING`,
		t.tenant, string(typ), localID,
	); err != nil {
		return fmt.Errorf("recording that %s holds %s %q: %w", t.tenant, typ, localID, err)
	}
	return nil
}

// LoadRecord reads one of the platform's records back.
//
// Platform-wide, like the record it reads: a tenant that holds the record and
// one that does not both read the same row. Whether a tenant holds it is
// [Tx.Exists].
func (t *Tx) LoadRecord(typ agmasync.EntityType, localID string) (Record, error) {
	table, err := tableOf(typ)
	if err != nil {
		return Record{}, err
	}

	names := scanColumns(typ)
	query := fmt.Sprintf(
		"SELECT archived, unmodelled, %s FROM %s WHERE local_id = ?",
		join(names), table)

	var archived int
	var unmodelled string
	scanned := make([]sql.NullString, len(names))
	targets := []any{&archived, &unmodelled}
	for i := range scanned {
		targets = append(targets, &scanned[i])
	}

	if err := t.tx.QueryRow(query, localID).Scan(targets...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("reading %s: %w", table, err)
	}

	out := Record{
		EntityType: typ,
		LocalID:    localID,
		Archived:   archived != 0,
		Modelled:   map[string]json.RawMessage{},
		Unmodelled: map[string]json.RawMessage{},
	}
	if err := json.Unmarshal([]byte(unmodelled), &out.Unmodelled); err != nil {
		return Record{}, fmt.Errorf("decoding unmodelled attributes: %w", err)
	}

	values := map[string]sql.NullString{}
	for i, name := range names {
		values[name] = scanned[i]
	}
	if err := rebuildModelled(&out, values); err != nil {
		return Record{}, err
	}
	return out, nil
}

// scanColumns names the type-specific columns LoadRecord reads back.
func scanColumns(typ agmasync.EntityType) []string {
	switch typ {
	case agmasync.TypeOrganization:
		return []string{"name", "commercial_registry_number", "city", "country"}
	case agmasync.TypePerson:
		return []string{"last_name", "first_name", "title"}
	case agmasync.TypeFarm:
		return []string{"name", "owner_type", "owner_local_id", "city"}
	case agmasync.TypeField:
		return []string{"name", "area", "farm_local_id"}
	case agmasync.TypeFieldBoundary:
		return []string{"boundary_type", "creation_method", "boundary"}
	default:
		return nil
	}
}

// rebuildModelled is columnValues in reverse: the platform's columns back into
// the protocol's attributes.
//
// References go back out carrying this platform's own identifier for the
// target, which agrirouter resolves against its mapping. That is what keeps
// canonical identifiers off the write path — the platform never has to hold one
// to build a reference.
func rebuildModelled(r *Record, values map[string]sql.NullString) error {
	set := func(key string, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encoding %s: %w", key, err)
		}
		r.Modelled[key] = raw
		return nil
	}
	str := func(column, key string) error {
		if v, ok := values[column]; ok && v.Valid {
			return set(key, v.String)
		}
		return nil
	}
	address := func() error {
		out := map[string]string{}
		if v, ok := values["city"]; ok && v.Valid {
			out["city"] = v.String
		}
		if v, ok := values["country"]; ok && v.Valid {
			out["country"] = v.String
		}
		if len(out) == 0 {
			return nil
		}
		return set("address", out)
	}

	switch r.EntityType {
	case agmasync.TypeOrganization:
		return errors.Join(
			str("name", "name"),
			str("commercial_registry_number", "commercial_registry_number"),
			address(),
		)
	case agmasync.TypePerson:
		return errors.Join(
			str("last_name", "last_name"),
			str("first_name", "first_name"),
			str("title", "title"),
		)
	case agmasync.TypeFarm:
		var owner error
		if id, ok := values["owner_local_id"]; ok && id.Valid {
			ref := map[string]string{"local_id": id.String}
			if kind, ok := values["owner_type"]; ok && kind.Valid {
				ref["type"] = kind.String
			}
			owner = set("owner", ref)
		}
		return errors.Join(str("name", "name"), owner, address())
	case agmasync.TypeField:
		var area error
		if v, ok := values["area"]; ok && v.Valid {
			parsed, err := strconv.ParseFloat(v.String, 64)
			if err != nil {
				area = fmt.Errorf("decoding area: %w", err)
			} else {
				area = set("area", parsed)
			}
		}
		var farm error
		if v, ok := values["farm_local_id"]; ok && v.Valid {
			farm = set("farm", map[string]string{"local_id": v.String})
		}
		return errors.Join(str("name", "name"), area, farm)
	case agmasync.TypeFieldBoundary:
		var boundary error
		if v, ok := values["boundary"]; ok && v.Valid {
			r.Modelled["boundary"] = json.RawMessage(v.String)
		}
		return errors.Join(
			str("boundary_type", "boundary_type"),
			str("creation_method", "creation_method"),
			boundary,
		)
	default:
		return fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, r.EntityType)
	}
}

// DeleteRecord removes one of the platform's own records from the transaction's
// tenant, as a user deleting it in the platform's own software does.
//
// The record and its bookkeeping go only with the last tenant that held it.
// Until then the platform still holds the record — another of its tenants is
// showing it to a user — and dropping the pair would leave that one unable to
// send it and unable to recognise its next delivery.
//
// It is a local deletion and nothing else: it says nothing to agrirouter, which
// goes on holding the canonical object and the mapping to it. Tell agrirouter
// with [Applier.Unbind] first — the platform no longer holds the object — or the
// mapping is left naming a record that is gone and the object's next change is
// delivered under an identifier that resolves to nothing. Deleting a record the
// platform shares is not a deactivation either: what it deactivates for
// everybody is [Applier.Deactivate].
func (t *Tx) DeleteRecord(typ agmasync.EntityType, localID string) error {
	table, err := tableOf(typ)
	if err != nil {
		return err
	}

	if _, err := t.tx.Exec(`
		DELETE FROM tenant_entity
		 WHERE tenant_id = ? AND entity_type = ? AND local_id = ?`,
		t.tenant, string(typ), localID,
	); err != nil {
		return fmt.Errorf("releasing %s %q: %w", typ, localID, err)
	}

	held, err := t.heldByAny(typ, localID)
	if err != nil {
		return err
	}
	if held {
		return nil
	}

	if _, err := t.tx.Exec(
		fmt.Sprintf("DELETE FROM %s WHERE local_id = ?", table), localID,
	); err != nil {
		return fmt.Errorf("deleting from %s: %w", table, err)
	}
	if _, err := t.tx.Exec(`
		DELETE FROM agmasync_object
		 WHERE entity_type = ? AND local_id = ?`,
		string(typ), localID,
	); err != nil {
		return fmt.Errorf("deleting sync row: %w", err)
	}
	return nil
}

// ForgetRecord removes a record and leaves the binding to the canonical object
// behind, as a restore that missed the tables the records sit in does.
//
// It is not something a platform chooses. [Tx.DeleteRecord] is the deliberate
// deletion and takes the pair with it; this is the state a partial loss leaves,
// where the bookkeeping remembers an object the platform no longer holds. The
// binding is what makes it recoverable: it names the canonical object, which is
// what a request needs, and agrirouter is not told anything, since the platform
// wants the object back rather than to say it no longer holds it.
func (t *Tx) ForgetRecord(typ agmasync.EntityType, localID string) error {
	table, err := tableOf(typ)
	if err != nil {
		return err
	}
	if _, err := t.tx.Exec(`
		DELETE FROM tenant_entity
		 WHERE tenant_id = ? AND entity_type = ? AND local_id = ?`,
		t.tenant, string(typ), localID,
	); err != nil {
		return fmt.Errorf("releasing %s %q: %w", typ, localID, err)
	}

	held, err := t.heldByAny(typ, localID)
	if err != nil {
		return err
	}
	if held {
		return nil
	}
	if _, err := t.tx.Exec(
		fmt.Sprintf("DELETE FROM %s WHERE local_id = ?", table), localID,
	); err != nil {
		return fmt.Errorf("deleting from %s: %w", table, err)
	}
	return nil
}

// heldByAny reports whether any of the product's tenants still holds a record.
func (t *Tx) heldByAny(typ agmasync.EntityType, localID string) (bool, error) {
	var one int
	err := t.tx.QueryRow(`
		SELECT 1 FROM tenant_entity
		 WHERE entity_type = ? AND local_id = ? LIMIT 1`,
		string(typ), localID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking who holds %s %q: %w", typ, localID, err)
	}
	return true, nil
}

// SetArchived marks a record archived, which is how this platform expresses a
// deactivation it has been told about.
//
// It archives the record rather than one tenant's view of it: deactivation is a
// statement about the entity in the world, delivered to every participant, so
// showing it as current in the product's other tenants would be a local fiction.
func (t *Tx) SetArchived(typ agmasync.EntityType, localID string, archived bool) error {
	table, err := tableOf(typ)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(
		fmt.Sprintf("UPDATE %s SET archived = ? WHERE local_id = ?", table),
		boolToInt(archived), localID)
	if err != nil {
		return fmt.Errorf("archiving %s: %w", table, err)
	}
	return nil
}

// LocalIDs lists every record of one type the transaction's tenant holds, in a
// stable order.
//
// Through the membership table, so it is what this tenant shows its users
// rather than everything the platform has. It is what the push half of an
// initial load walks — the endpoint offers agrirouter what its tenant holds —
// and what reconciliation matches a delivered object against.
func (t *Tx) LocalIDs(typ agmasync.EntityType) ([]string, error) {
	if _, err := tableOf(typ); err != nil {
		return nil, err
	}
	rows, err := t.tx.Query(`
		SELECT local_id FROM tenant_entity
		 WHERE tenant_id = ? AND entity_type = ?
		 ORDER BY local_id`, t.tenant, string(typ))
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", typ, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning %s: %w", typ, err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Exists reports whether the transaction's tenant holds a record under this
// identifier. A record only another tenant holds is not this one's, even though
// it is the same record and the same binding.
func (t *Tx) Exists(typ agmasync.EntityType, localID string) (bool, error) {
	if _, err := tableOf(typ); err != nil {
		return false, err
	}
	var one int
	err := t.tx.QueryRow(`
		SELECT 1 FROM tenant_entity
		 WHERE tenant_id = ? AND entity_type = ? AND local_id = ?`,
		t.tenant, string(typ), localID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking whether %s holds %s %q: %w", t.tenant, typ, localID, err)
	}
	return true, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
