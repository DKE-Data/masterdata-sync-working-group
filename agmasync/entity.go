package agmasync

import (
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
)

// EntityType is one of the four master-data entity types of the MVP scope.
//
// The values are the ones the `type` discriminator carries on the wire, and an
// `entityType` toggle carries the same, so a declaration or a selection can be
// compared against an entity's own type directly.
//
// The collection segments in the paths are spelled differently — plural and
// kebab-cased, `field-boundaries` for `fieldBoundary` — but nothing here
// converts between the two: the paths are only reached through the generated
// client, which has an operation per collection.
type EntityType string

// The entity types. See "Scope" in specification.md.
const (
	TypeParty         EntityType = "party"
	TypeFarm          EntityType = "farm"
	TypeField         EntityType = "field"
	TypeFieldBoundary EntityType = "fieldBoundary"
)

// EntityTypes lists every supported type. Iterating it is how the sample
// platform avoids hard-coding the set in more than one place.
var EntityTypes = []EntityType{
	TypeParty, TypeFarm, TypeField, TypeFieldBoundary,
}

// DependencyOrder lists the types so that a referenced type precedes the types
// that reference it. See "Entity dependencies" in specification.md.
//
// It is the order a participant sends in, and the property agrirouter's own
// delivery order guarantees. A reference is carried as the sender's own
// identifier and resolved against the mapping, so a field sent before the farm
// it names has nothing to resolve to; sending in this order means every
// reference's target is already bound by the time it is used.
//
// It is not [EntityTypes], which is the set rather than an order.
//
// Parties are the one type it does not order fully: a membership references a
// party from a party, so within parties the order is per object — see
// [PartyTier].
var DependencyOrder = []EntityType{
	TypeParty, TypeFarm, TypeField, TypeFieldBoundary,
}

// Valid reports whether the type is one this version of the protocol defines.
//
// Unknown entity types are not the same case as unknown values of an
// extensible enumeration, which must be tolerated and relayed unchanged. An
// entity type names a resource and a local table; there is nothing useful a
// participant can do with an entity whose type it has never heard of.
func (t EntityType) Valid() bool {
	switch t {
	case TypeParty, TypeFarm, TypeField, TypeFieldBoundary:
		return true
	default:
		return false
	}
}

// Envelope is the set of fields every entity shares, plus the type
// discriminator, read off an entity without regard to which type it is.
//
// See "Common envelope" in specification.md. Every field but Type is a pointer
// because every one of them is assigned by agrirouter and therefore absent on
// an object a participant is about to send for the first time. LocalId is
// absent for a second reason as well, and that absence is meaningful: on a
// delivered object it states that agrirouter holds no mapping for the
// receiving application, which is what makes the object recognisable as one
// the receiver must create locally and bind.
type Envelope struct {
	oapi.Envelope
	Type EntityType `json:"type"`
}

// EnvelopeOf reads the common fields of an entity of any type.
//
// The event stream carries all four types over one connection, so a receiver
// has to read `type`, `revision`, and `localId` before it knows which concrete
// schema to decode into. That ordering is why this exists rather than a
// four-way type switch at every call site.
func EnvelopeOf(e oapi.Entity) (Envelope, error) {
	raw, err := e.MarshalJSON()
	if err != nil {
		return Envelope{}, fmt.Errorf("agmasync: reading entity envelope: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, fmt.Errorf("agmasync: reading entity envelope: %w", err)
	}
	if !env.Type.Valid() {
		return env, fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, env.Type)
	}
	return env, nil
}

// Entity wrappers. The generated union has From* methods on a zero value; these
// save every caller the same three lines.

// FromParty wraps a party as an entity.
func FromParty(v oapi.Party) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromParty(v)
}

// FromFarm wraps a farm as an entity.
func FromFarm(v oapi.Farm) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromFarm(v)
}

// FromField wraps a field as an entity.
func FromField(v oapi.Field) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromField(v)
}

// FromFieldBoundary wraps a field boundary as an entity.
func FromFieldBoundary(v oapi.FieldBoundary) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromFieldBoundary(v)
}

// LocalRef builds a reference to another entity from the referencing
// endpoint's own identifier for the target.
//
// See "References" in specification.md. On send a participant may use either
// identifier, and using its own is what keeps agrirouterId off the write path:
// references can be built out of a participant's own keys, without first
// capturing and correlating canonical ones. agrirouter resolves the localId
// against the sending endpoint's mapping and rejects the write if that endpoint
// has not sent the target yet, so the target must be sent before the first
// reference to it — through the same endpoint.
func LocalRef(localID string) oapi.EntityReference {
	return oapi.EntityReference{LocalId: &localID}
}

// The party types `details` states. A party without `details` is of unknown
// party type. See "Party details" in specification.md.
const (
	PartyTypePerson       = "PERSON"
	PartyTypeOrganization = "ORGANIZATION"
)

// PartyTypeOf reads the party type a party states: [PartyTypePerson],
// [PartyTypeOrganization], or "" where it carries no `details`.
func PartyTypeOf(p oapi.Party) (string, error) {
	if !p.Details.IsSpecified() || p.Details.IsNull() {
		return "", nil
	}
	d, err := p.Details.Get()
	if err != nil {
		return "", fmt.Errorf("agmasync: reading party details: %w", err)
	}
	t, err := d.Discriminator()
	if err != nil {
		return "", fmt.Errorf("agmasync: reading party details: %w", err)
	}
	return t, nil
}

// PartyTier is the position of a party in send and delivery order: 1 for a
// party with person `details`, which may name organizations through its
// memberships, and 0 for any other, which references nothing.
//
// Sending tier 0 before tier 1 means every membership target is bound by the
// time a person names it. See "Entity dependencies" in specification.md and
// ADR 07.
func PartyTier(p oapi.Party) (int, error) {
	t, err := PartyTypeOf(p)
	if err != nil {
		return 0, err
	}
	if t == PartyTypePerson {
		return 1, nil
	}
	return 0, nil
}
