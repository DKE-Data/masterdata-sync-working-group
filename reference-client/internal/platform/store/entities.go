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
// its own columns and nothing else, and turning one into the other is the
// codec's job — which is where a real integration's mapping work actually is.
//
// What it does not model it does not keep. A write only changes what it
// carries, so leaving an attribute out of every write is what keeps it for the
// participants that do model it. See "Writing an entity" in specification.md.
type Record struct {
	EntityType agmasync.EntityType
	LocalID    string
	Archived   bool

	// Modelled holds the attributes this platform has columns for, keyed by
	// their protocol names. One it holds no value for is absent.
	Modelled map[string]json.RawMessage
}

// columns names the protocol attributes each entity type has real columns for.
// Everything else on a delivered object is left to agrirouter.
var columns = map[agmasync.EntityType][]string{
	agmasync.TypeParty: {"name", "details", "address"},
	agmasync.TypeFarm:  {"name", "owner", "address"},
	agmasync.TypeField: {"name", "area", "farm"},
	agmasync.TypeFieldBoundary: {
		"field", "name", "boundary_type", "creation_method", "boundary",
	},
}

// FromEntity turns a delivered entity into the platform's own shape.
//
// The envelope is stripped: type, identifiers, revision and the rest are
// agrirouter's bookkeeping and belong in agmasync_object, not in the platform's
// tables. Of what remains, the platform takes what it models.
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
	}
	for key, value := range all {
		if modelled[key] && !envelope[key] {
			out.Modelled[key] = value
		}
	}
	return out, nil
}

// modelledAddress names the parts of an address each entity type has columns
// for. A party's depends on its kind: see [Record.addressParts].
var modelledAddress = map[agmasync.EntityType][]string{
	agmasync.TypeParty: {"city", "country"},
	agmasync.TypeFarm:  {"city"},
}

// addressParts names the parts of an address the record's table has columns
// for. The person table has none: this platform keeps addresses for
// organizations only.
func (r Record) addressParts() []string {
	if r.EntityType == agmasync.TypeParty && r.partyType() == agmasync.PartyTypePerson {
		return nil
	}
	return modelledAddress[r.EntityType]
}

// modelledDetails names the attributes of each party type the platform has
// columns for. A person's memberships are not among them, and so are kept.
var modelledDetails = map[string][]string{
	agmasync.PartyTypePerson:       {"title", "first_name", "last_name"},
	agmasync.PartyTypeOrganization: {"commercial_registry_number"},
}

// notNullable are the modelled attributes a write never sends as null: the ones
// the protocol requires, which are never removed, and references, where an
// empty column does not say the reference was cleared — it is also what an
// unresolved one leaves behind — and so must not remove the canonical one.
var notNullable = map[string]bool{
	"name": true, "boundary": true, "owner": true, "farm": true, "field": true,
}

// ToEntity is FromEntity in reverse: the platform's own record as an entity to send.
//
// A write is a merge patch, so the entity says what this platform holds and
// nothing more. Every modelled attribute goes out: with its value, or as null
// where the platform holds none, since leaving it out would keep whatever
// agrirouter has. An address goes out as the parts the platform models, which
// leaves the rest of it alone, and so do a party's details. Unmodelled
// attributes are left out, and kept.
func (r Record) ToEntity(localID string) (oapi.Entity, error) {
	fields := map[string]json.RawMessage{}
	for k, v := range r.Modelled {
		fields[k] = v
	}
	for _, name := range columns[r.EntityType] {
		if _, held := fields[name]; held || notNullable[name] || name == "address" || name == "details" {
			continue
		}
		fields[name] = json.RawMessage("null")
	}
	if r.EntityType == agmasync.TypeParty {
		if _, known := modelledDetails[r.partyType()]; !known {
			delete(fields, "details")
		} else {
			details, err := detailsPatch(r.Modelled["details"])
			if err != nil {
				return oapi.Entity{}, err
			}
			fields["details"] = details
		}
	}
	if parts := r.addressParts(); len(parts) == 0 {
		delete(fields, "address")
	} else {
		address, err := addressPatch(r.Modelled["address"], parts)
		if err != nil {
			return oapi.Entity{}, err
		}
		if address == nil {
			delete(fields, "address")
		} else {
			fields["address"] = address
		}
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

// addressPatch renders the address the platform holds as the parts it models,
// null for those it holds no value for. It is nil when the platform holds none
// of them: merging nulls into an address agrirouter does not hold would create
// an empty one. The cost is that clearing the last modelled part of an address
// leaves it in place — which this platform's screens never do, having no edit
// form.
func addressPatch(raw json.RawMessage, parts []string) (json.RawMessage, error) {
	held := map[string]json.RawMessage{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &held); err != nil {
			return nil, fmt.Errorf("reading address: %w", err)
		}
	}
	out := map[string]json.RawMessage{}
	holdsAny := false
	for _, part := range parts {
		value, ok := held[part]
		if !ok || string(value) == "null" {
			out[part] = json.RawMessage("null")
			continue
		}
		out[part] = value
		holdsAny = true
	}
	if !holdsAny {
		return nil, nil
	}
	return json.Marshal(out)
}

// partyType is the kind of party a record states, "" where it states none.
func (r Record) partyType() string {
	var details struct {
		PartyType string `json:"party_type"`
	}
	if raw := r.Modelled["details"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &details)
	}
	return details.PartyType
}

// detailsPatch renders a party's details as its party_type and the attributes
// of that type the platform models, null for those it holds no value for. It
// carries the party_type on every write, as the specification requires: what
// the rest merges into depends on it, and a changed one replaces the details
// whole. A record that states no party type sends no details at all — see
// [Record.ToEntity].
func detailsPatch(raw json.RawMessage) (json.RawMessage, error) {
	held := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &held); err != nil {
		return nil, fmt.Errorf("reading details: %w", err)
	}
	var partyType string
	if err := json.Unmarshal(held["party_type"], &partyType); err != nil {
		return nil, fmt.Errorf("reading party_type: %w", err)
	}
	out := map[string]json.RawMessage{"party_type": held["party_type"]}
	for _, part := range modelledDetails[partyType] {
		value, ok := held[part]
		if !ok {
			value = json.RawMessage("null")
		}
		out[part] = value
	}
	return json.Marshal(out)
}

// tablesOf lists the platform's tables a record of an entity type may sit in.
//
// One per type, except parties. The protocol has one party type and states in
// `details` whether it is a person or an organization; this platform, like most
// farm management systems, keeps persons and organizations in tables of their
// own, and the mapping between the two shapes is the codec's.
func tablesOf(typ agmasync.EntityType) ([]string, error) {
	switch typ {
	case agmasync.TypeParty:
		return []string{"organization", "person"}, nil
	case agmasync.TypeFarm:
		return []string{"farm"}, nil
	case agmasync.TypeField:
		return []string{"field"}, nil
	case agmasync.TypeFieldBoundary:
		return []string{"field_boundary"}, nil
	default:
		return nil, fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, typ)
	}
}

// tableFor picks the table a record goes into.
//
// A party goes by the party type its details state. One of unknown party type
// still has to go somewhere in a store that knows only persons and
// organizations, and organization is the lesser guess: it needs nothing but a
// name, where a person would need a name split into parts nobody gave.
//
// The guess is not kept apart from a recorded party type, so this platform's
// next write of the party states ORGANIZATION to every participant. That is
// this platform's choice, not the protocol's: one that wants to leave the party
// type open would mark the row and leave details out of its writes. See "Party
// details" in specification.md.
func tableFor(r Record) (string, error) {
	tables, err := tablesOf(r.EntityType)
	if err != nil {
		return "", err
	}
	if r.EntityType == agmasync.TypeParty && r.partyType() == agmasync.PartyTypePerson {
		return "person", nil
	}
	return tables[0], nil
}

// columnValues maps the modelled attributes onto the table's columns.
//
// This is the mapping work an integration actually has to do, and it is per
// table because the platform's schema is its own rather than a mirror of the
// protocol's.
//
// It runs in a transaction because references have to be resolved against the
// platform's own mapping; see [Tx.resolveRef].
func (t *Tx) columnValues(r Record, table string) (map[string]any, error) {
	out := map[string]any{}

	var errs []error
	// Every column is written, NULL for an attribute the record does not
	// carry: a delivered object is whole, so an attribute absent from it has
	// been removed, and a column still holding it would send it back.
	str := func(key, column string) {
		out[column] = nil
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

	// The details of the record's own party type. A party that changed party
	// type moves table, so nothing of the other party type is held.
	details := map[string]json.RawMessage{}
	if raw, ok := r.Modelled["details"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &details); err != nil {
			return nil, fmt.Errorf("reading details: %w", err)
		}
	}
	detail := func(key string) {
		out[key] = nil
		var v *string
		if raw, ok := details[key]; ok {
			if err := json.Unmarshal(raw, &v); err != nil {
				errs = append(errs, fmt.Errorf("reading %s: %w", key, err))
				return
			}
		}
		if v != nil {
			out[key] = *v
		}
	}

	switch table {
	case "organization":
		str("name", "name")
		detail("commercial_registry_number")
		city, country, err := addressParts(r.Modelled["address"])
		if err != nil {
			return nil, err
		}
		out["city"], out["country"] = city, country
	case "person":
		str("name", "name")
		detail("title")
		detail("first_name")
		detail("last_name")
	case "farm":
		str("name", "name")
		city, _, err := addressParts(r.Modelled["address"])
		if err != nil {
			return nil, err
		}
		out["city"] = city
		ownerLocal, err := t.resolveRef(r.Modelled["owner"], agmasync.TypeParty)
		if err != nil {
			return nil, err
		}
		out["owner_local_id"] = ownerLocal
	case "field":
		str("name", "name")
		out["area"] = nil
		if raw, ok := r.Modelled["area"]; ok {
			var area *float64
			if err := json.Unmarshal(raw, &area); err != nil {
				return nil, fmt.Errorf("reading area: %w", err)
			}
			if area != nil {
				out["area"] = *area
			}
		}
		farmLocal, err := t.resolveRef(r.Modelled["farm"], agmasync.TypeFarm)
		if err != nil {
			return nil, err
		}
		out["farm_local_id"] = farmLocal
	case "field_boundary":
		fieldLocal, err := t.resolveRef(r.Modelled["field"], agmasync.TypeField)
		if err != nil {
			return nil, err
		}
		out["field_local_id"] = fieldLocal
		str("name", "name")
		str("boundary_type", "boundary_type")
		str("creation_method", "creation_method")
		out["boundary"] = nil
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
// for its target, an object of type typ.
//
// A delivered reference carries the receiving participant's own identifier for
// the target only where agrirouter held one when the frame was rendered — which,
// for the whole of a first initial load, it does not: every object in the set is
// rendered before the endpoint has bound any of it. So the local_id is a
// shortcut, and agrirouter_id is what a reference actually resolves through.
//
// This is why delivery order matters. A referenced object precedes the objects
// referencing it, so by the time the reference is applied the platform has
// already applied its target and holds the row that answers this lookup. A
// reference that still resolves to nothing is left null rather than guessed at:
// the platform does not hold the target, and [Applier] will create and bind it
// when it arrives.
func (t *Tx) resolveRef(raw json.RawMessage, typ agmasync.EntityType) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var ref struct {
		LocalID      *string    `json:"local_id"`
		AgrirouterID *uuid.UUID `json:"agrirouter_id"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		return nil, fmt.Errorf("reading reference: %w", err)
	}
	if ref.LocalID != nil {
		return *ref.LocalID, nil
	}
	if ref.AgrirouterID == nil {
		return nil, nil
	}

	row, err := t.SyncRowByAgrirouterID(typ, *ref.AgrirouterID)
	switch {
	case err == nil:
		return row.LocalID, nil
	case errors.Is(err, ErrNotFound):
		return nil, nil
	default:
		return nil, err
	}
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
	table, err := tableFor(r)
	if err != nil {
		return err
	}
	values, err := t.columnValues(r, table)
	if err != nil {
		return err
	}

	values["archived"] = boolToInt(r.Archived)

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
	// A party whose party type changed leaves the table of its old one.
	tables, _ := tablesOf(r.EntityType)
	for _, other := range tables {
		if other == table {
			continue
		}
		if _, err := t.tx.Exec(
			fmt.Sprintf("DELETE FROM %s WHERE local_id = ?", other), localID,
		); err != nil {
			return fmt.Errorf("moving out of %s: %w", other, err)
		}
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
	tables, err := tablesOf(typ)
	if err != nil {
		return Record{}, err
	}
	for _, table := range tables {
		out, err := t.loadFrom(table, typ, localID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return out, err
	}
	return Record{}, ErrNotFound
}

func (t *Tx) loadFrom(table string, typ agmasync.EntityType, localID string) (Record, error) {
	names := scanColumns(table)
	query := fmt.Sprintf(
		"SELECT archived, %s FROM %s WHERE local_id = ?",
		join(names), table)

	var archived int
	scanned := make([]sql.NullString, len(names))
	targets := []any{&archived}
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
	}

	values := map[string]sql.NullString{}
	for i, name := range names {
		values[name] = scanned[i]
	}
	if err := rebuildModelled(&out, table, values); err != nil {
		return Record{}, err
	}
	return out, nil
}

// scanColumns names the table-specific columns LoadRecord reads back.
func scanColumns(table string) []string {
	switch table {
	case "organization":
		return []string{"name", "commercial_registry_number", "city", "country"}
	case "person":
		return []string{"name", "title", "first_name", "last_name"}
	case "farm":
		return []string{"name", "owner_local_id", "city"}
	case "field":
		return []string{"name", "area", "farm_local_id"}
	case "field_boundary":
		return []string{"field_local_id", "name", "boundary_type", "creation_method", "boundary"}
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
func rebuildModelled(r *Record, table string, values map[string]sql.NullString) error {
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

	details := func(partyType string, parts ...string) error {
		out := map[string]string{"party_type": partyType}
		for _, part := range parts {
			if v, ok := values[part]; ok && v.Valid {
				out[part] = v.String
			}
		}
		return set("details", out)
	}

	switch table {
	case "organization":
		return errors.Join(
			str("name", "name"),
			details(agmasync.PartyTypeOrganization, "commercial_registry_number"),
			address(),
		)
	case "person":
		return errors.Join(
			str("name", "name"),
			details(agmasync.PartyTypePerson, "title", "first_name", "last_name"),
		)
	case "farm":
		var owner error
		if id, ok := values["owner_local_id"]; ok && id.Valid {
			owner = set("owner", map[string]string{"local_id": id.String})
		}
		return errors.Join(str("name", "name"), owner, address())
	case "field":
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
	case "field_boundary":
		var boundary error
		if v, ok := values["boundary"]; ok && v.Valid {
			r.Modelled["boundary"] = json.RawMessage(v.String)
		}
		var field error
		if v, ok := values["field_local_id"]; ok && v.Valid {
			field = set("field", map[string]string{"local_id": v.String})
		}
		return errors.Join(
			field,
			str("name", "name"),
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
	tables, err := tablesOf(typ)
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

	if err := t.deleteFrom(tables, localID); err != nil {
		return err
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
	tables, err := tablesOf(typ)
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
	if err := t.deleteFrom(tables, localID); err != nil {
		return err
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
	tables, err := tablesOf(typ)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := t.tx.Exec(
			fmt.Sprintf("UPDATE %s SET archived = ? WHERE local_id = ?", table),
			boolToInt(archived), localID,
		); err != nil {
			return fmt.Errorf("archiving %s: %w", table, err)
		}
	}
	return nil
}

// deleteFrom removes a record from whichever of its type's tables holds it.
func (t *Tx) deleteFrom(tables []string, localID string) error {
	for _, table := range tables {
		if _, err := t.tx.Exec(
			fmt.Sprintf("DELETE FROM %s WHERE local_id = ?", table), localID,
		); err != nil {
			return fmt.Errorf("deleting from %s: %w", table, err)
		}
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
	if _, err := tablesOf(typ); err != nil {
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
	if _, err := tablesOf(typ); err != nil {
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
