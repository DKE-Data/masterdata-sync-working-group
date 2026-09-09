package agmasync

import (
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
)

// EntityType is one of the five master-data entity types of the MVP scope.
//
// The values are the ones the `type` discriminator carries on the wire. They
// are not the same strings as the collection segments in the paths, which are
// plural and kebab-cased; [EntityType.Collection] converts.
type EntityType string

// The entity types. See "Scope" in specification.md.
const (
	TypeOrganization  EntityType = "organization"
	TypePerson        EntityType = "person"
	TypeFarm          EntityType = "farm"
	TypeField         EntityType = "field"
	TypeFieldBoundary EntityType = "fieldBoundary"
)

// EntityTypes lists every supported type. Iterating it is how the sample
// platform avoids hard-coding the set in more than one place.
var EntityTypes = []EntityType{
	TypeOrganization, TypePerson, TypeFarm, TypeField, TypeFieldBoundary,
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
var DependencyOrder = []EntityType{
	TypeOrganization, TypePerson, TypeFarm, TypeFieldBoundary, TypeField,
}

// Collection returns the path segment and opt-in toggle name for the type —
// `organizations`, `persons`, `farms`, `fields`, `field-boundaries`.
func (t EntityType) Collection() string {
	switch t {
	case TypeOrganization:
		return "organizations"
	case TypePerson:
		return "persons"
	case TypeFarm:
		return "farms"
	case TypeField:
		return "fields"
	case TypeFieldBoundary:
		return "field-boundaries"
	default:
		return string(t)
	}
}

// Valid reports whether the type is one this version of the protocol defines.
//
// Unknown entity types are not the same case as unknown values of an
// extensible enumeration, which must be tolerated and relayed unchanged. An
// entity type names a resource and a local table; there is nothing useful a
// participant can do with an entity whose type it has never heard of.
func (t EntityType) Valid() bool {
	switch t {
	case TypeOrganization, TypePerson, TypeFarm, TypeField, TypeFieldBoundary:
		return true
	default:
		return false
	}
}

// ParseCollection maps a collection segment back to its entity type.
func ParseCollection(collection string) (EntityType, bool) {
	for _, t := range EntityTypes {
		if t.Collection() == collection {
			return t, true
		}
	}
	return "", false
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
// The event stream carries all five types over one connection, so a receiver
// has to read `type`, `revision`, and `localId` before it knows which concrete
// schema to decode into. That ordering is why this exists rather than a
// five-way type switch at every call site.
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

// FromOrganization wraps an organization as an entity.
func FromOrganization(v oapi.Organization) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromOrganization(v)
}

// FromPerson wraps a person as an entity.
func FromPerson(v oapi.Person) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromPerson(v)
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

// LocalPartyRef builds a reference to an organization or person from the
// referencing endpoint's own identifier.
//
// A party reference carries a type discriminator because the target may be
// either, and a receiver that does not hold it has to request it — a request
// being per entity type, an agrirouterId alone would not say which collection
// to ask. Slots whose type is fixed, such as a field's farm, use [LocalRef].
func LocalPartyRef(t EntityType, localID string) (oapi.PartyReference, error) {
	if t != TypeOrganization && t != TypePerson {
		return oapi.PartyReference{}, fmt.Errorf(
			"agmasync: %w: a party reference is an organization or a person, not %q",
			ErrUnknownEntityType, t)
	}
	return oapi.PartyReference{
		Type:    oapi.PartyReferenceType(t),
		LocalId: &localID,
	}, nil
}
