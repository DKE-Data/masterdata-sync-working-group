package agmasync

import (
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// EntityType is one of the master-data entity types.
//
// The values are the ones the `type` discriminator carries on the wire, and an
// `entityType` toggle carries the same, so a capability declaration or a routing
// selection can be compared against an entity's own type directly.
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

// EntityTypes lists every supported type.
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
func (t EntityType) Valid() bool {
	switch t {
	case TypeParty, TypeFarm, TypeField, TypeFieldBoundary:
		return true
	default:
		return false
	}
}

// Envelope is the set of fields every entity shares, plus the type
// discriminator and the object's identity, read off an entity without regard
// to which type it is.
//
// See "Common envelope" in specification.md. Every field but Type is a pointer
// because every one of them is assigned by agrirouter and therefore absent on
// an object a participant is about to send for the first time.
//
// AgrirouterId and LocalId are not envelope fields on the wire: they are the
// two entries of `content.id` (see [IdentityOf]), lifted out because a
// receiver needs them before it decodes the content. LocalId is absent for a
// second reason as well, and that absence is meaningful: on a delivered object
// it states that agrirouter holds no mapping for the receiving application,
// which is what makes the object recognizable as one the receiver must create
// locally and bind.
type Envelope struct {
	oapi.Envelope
	Type EntityType `json:"type"`

	AgrirouterId *uuid.UUID `json:"-"`
	LocalId      *string    `json:"-"`
}

// EnvelopeOf reads the common fields of an entity of any type from its JSON.
//
// The event stream carries all four types over one connection, so a receiver
// has to read `type`, `revision`, and the identity before it knows which
// concrete schema to decode into. That ordering is why this exists rather than
// a four-way type switch at every call site.
func EnvelopeOf(raw []byte) (Envelope, error) {
	env, err := readEnvelope(raw)
	if err != nil {
		return env, err
	}
	if !env.Type.Valid() {
		return env, fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, env.Type)
	}
	return env, nil
}

// readEnvelope reads the envelope and the identity in `content.id`, without
// checking the type.
func readEnvelope(raw []byte) (Envelope, error) {
	var probe struct {
		Envelope
		Content struct {
			Id oapi.Id `json:"id"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Envelope{}, fmt.Errorf("agmasync: reading entity envelope: %w", err)
	}
	env := probe.Envelope
	id, err := IdentityOf(probe.Content.Id)
	if err != nil {
		return Envelope{}, err
	}
	env.AgrirouterId, env.LocalId = id.AgrirouterID, id.LocalID
	return env, nil
}

// Object is a canonical object of any entity type, decoded into its model.
type Object struct {
	// Envelope holds the object's common fields and its type, which says which
	// of Party, Farm, Field, and FieldBoundary is set. A receiver needs the
	// type and the revision before it can decide what to do with the object.
	Envelope Envelope

	// The object, decoded into its model. Exactly one is set, the one
	// Envelope.Type names.
	Party         *oapi.Party
	Farm          *oapi.Farm
	Field         *oapi.Field
	FieldBoundary *oapi.FieldBoundary
}

// ObjectOf decodes a canonical object of any entity type from its JSON.
func ObjectOf(raw []byte) (Object, error) {
	env, err := EnvelopeOf(raw)
	if err != nil {
		return Object{}, err
	}
	return decodeObject(env, raw)
}

// objectAs decodes the answer to an operation that names its entity type, as
// an object of type t. The type travels on the request, so the body need not
// repeat it; when it does, it must agree.
func objectAs(t EntityType, raw []byte) (Object, error) {
	env, err := readEnvelope(raw)
	if err != nil {
		return Object{}, err
	}
	switch env.Type {
	case "":
		env.Type = t
	case t:
	default:
		return Object{}, fmt.Errorf("agmasync: %w: asked for a %s, answered with a %s",
			ErrEntityTypeMismatch, t, env.Type)
	}
	return decodeObject(env, raw)
}

// decodeObject decodes raw into the model env.Type names.
func decodeObject(env Envelope, raw []byte) (Object, error) {
	var err error
	o := Object{Envelope: env}
	switch env.Type {
	case TypeParty:
		o.Party = new(oapi.Party)
		err = json.Unmarshal(raw, o.Party)
	case TypeFarm:
		o.Farm = new(oapi.Farm)
		err = json.Unmarshal(raw, o.Farm)
	case TypeField:
		o.Field = new(oapi.Field)
		err = json.Unmarshal(raw, o.Field)
	case TypeFieldBoundary:
		o.FieldBoundary = new(oapi.FieldBoundary)
		err = json.Unmarshal(raw, o.FieldBoundary)
	}
	if err != nil {
		return Object{}, fmt.Errorf("agmasync: decoding %s: %w", env.Type, err)
	}
	return o, nil
}

// The party types `_agmasync_details` states. A party without them is of
// unknown party type. See "Party details" in specification.md.
const (
	PartyTypePerson       = "PERSON"
	PartyTypeOrganization = "ORGANIZATION"
)

// PartyTypeOf reads the party type a party states: [PartyTypePerson],
// [PartyTypeOrganization], or "" where it carries no `_agmasync_details`.
func PartyTypeOf(p oapi.Party) (string, error) {
	details := p.Content.UnderscoreAgmasyncDetails
	if !details.IsSpecified() || details.IsNull() {
		return "", nil
	}
	d, err := details.Get()
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
// party with person `_agmasync_details`, which may name organizations through its
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
