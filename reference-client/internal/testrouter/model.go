// Package testrouter is a working implementation of the agrirouter side of
// AgmaSync, sufficient to run the reference client against.
//
// It exists because nothing implements the protocol yet, so without it the
// reference client has nothing to talk to and the scenarios cannot be executed.
// It is a second reading of the specification rather than a stub: the rules that
// a participant can get wrong — revision counters, origin suppression, the
// three-way merge, the initial-load state machine, per-pair binding rejections —
// are implemented here, so a client that gets them wrong fails against it.
//
// It is not agrirouter. It holds everything in memory, authenticates by treating
// the bearer token as an application identifier, and stands in for the platform
// UI with a control plane under /_test that has no counterpart in openapi.yaml.
package testrouter

import (
	"encoding/json"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// envelopeAttributes are the attributes agrirouter owns on every entity. They
// are held on the canonical object as typed state rather than as content, so a
// participant sending one has it ignored rather than applied — `revision` and
// `tenantId` in particular are compared or discarded, never assigned.
var envelopeAttributes = map[string]bool{
	"type":             true,
	"agrirouterId":     true,
	"localId":          true,
	"active":           true,
	"revision":         true,
	"modifiedAt":       true,
	"tenantId":         true,
	"sourceEndpointId": true,
}

// deliveryOrder is the order objects are delivered in, on the initial-load
// stream and during catch-up. It places a referenced object before the objects
// that reference it, which is the only property of the order the specification
// promises and the only one a participant may rely on.
var deliveryOrder = []agmasync.EntityType{
	agmasync.TypeOrganization,
	agmasync.TypePerson,
	agmasync.TypeFarm,
	agmasync.TypeFieldBoundary,
	agmasync.TypeField,
}

// refSlot names a place in an entity where a reference to another entity sits.
//
// The protocol resolves references on the way in and rewrites them on the way
// out, so the router has to know where they are. `each` marks a slot that holds
// an array, and `attribute` the key within each element; a slot with no
// `attribute` is the reference itself.
type refSlot struct {
	key       string
	each      bool
	attribute string
}

// refSlots enumerates the reference-bearing slots of each entity type, as
// "Entity dependencies" in specification.md describes them. A field boundary
// references nothing: the reference runs from the field to its boundaries.
var refSlots = map[agmasync.EntityType][]refSlot{
	agmasync.TypePerson: {
		{key: "memberships", each: true, attribute: "organizationId"},
	},
	agmasync.TypeFarm: {
		{key: "owner"},
		{key: "partners", each: true, attribute: "partnerId"},
	},
	agmasync.TypeField: {
		{key: "farm"},
		{key: "owner"},
		{key: "fieldBoundaries", each: true},
	},
}

// object is one canonical entity in the SSOT.
type object struct {
	id       uuid.UUID
	typ      agmasync.EntityType
	tenantID uuid.UUID

	revision   int
	active     bool
	modifiedAt time.Time

	// sourceEndpointID is the endpoint whose change produced the current
	// revision. Origin suppression is decided on it.
	sourceEndpointID uuid.UUID

	// seq is the global position at which this object last changed. It backs
	// the opaque delivery position on the live stream: catch-up is everything
	// with a seq above the participant's own.
	seq uint64

	// content is the entity minus the envelope, references already resolved to
	// canonical identifiers.
	content map[string]json.RawMessage

	// stamps records, per content attribute, the revision at which that
	// attribute last changed. An attribute whose stamp is above the base
	// revision is one that changed under the participant's feet.
	stamps map[string]int

	// history records, per content attribute, the value it took at each
	// revision that changed it.
	//
	// A three-way merge compares the changes from the base to the current
	// revision with the changes from the base to the sent object, and both of
	// those need the attribute's value *at the base* — which the stamp alone
	// does not give. Without it a participant sending a whole object cannot be
	// told apart from one changing every attribute in it, and every merge
	// degrades into a conflict.
	//
	// Per attribute rather than whole prior states, which is a choice this
	// router makes rather than one the specification imposes: the two are
	// equivalent for the merge, since either reconstructs the base, and
	// per-attribute is simply the cheaper encoding when one attribute of
	// twenty changes at a time.
	//
	// It is not history in the sense the specification disclaims — it is never
	// served, and no operation here returns a past version.
	history map[string][]attributeValue
}

// attributeValue is one attribute's value from a given revision onwards. A nil
// value records that the attribute was absent.
type attributeValue struct {
	revision int
	value    json.RawMessage
}

// endpoint is one participant endpoint in one tenant.
type endpoint struct {
	id         uuid.UUID
	externalID string

	// appID identifies the participant. It decides which stream a frame is
	// published on — the connection unit is the application — and it is what the
	// identifier mapping is keyed by: a localId names a record in the
	// participant's namespace, so two endpoints of one application share one.
	appID string

	tenantID uuid.UUID

	// declared is what the participant said this endpoint is able to exchange.
	// It is the participant's write, it bounds what the user may select, and it
	// enables nothing on its own: an endpoint that has declared everything and
	// been selected for nothing exchanges nothing.
	declared map[agmasync.EntityType]bool

	// toggles is the user's selection — the opt-in itself, and always a subset
	// of declared. There is no participant-facing write for it: a user sets it
	// in agrirouter, which the control plane stands in for.
	toggles map[agmasync.EntityType]bool

	// selectionChangedAt is when the user last moved the selection, and is zero
	// until they first do. It is reported on the ROUTE_CHANGED frame, where it
	// lets a participant discard a repeat older than what it has already
	// applied, and nothing here is decided from it.
	selectionChangedAt time.Time

	// selectionChangedSeq is the change number the last move of the selection
	// took, and is zero until the user first moves it. It is what puts the move
	// in or out of a participant's catch-up, the frame being delivered above
	// the participant's position as a changed object is.
	//
	// It outlives the selection itself: an endpoint opted out of everything
	// keeps it, and that is deliberate. It is the whole of what agrirouter
	// retains about which endpoints once took part, and without it a withdrawal
	// made while a participant was away could never be restated — there is no
	// selection left to state, so nothing would ever mention that endpoint
	// again.
	selectionChangedSeq uint64

	load *loadState

	// previousLoadCompletedAt outlives the load state, and the opt-in that
	// caused it. An endpoint opted out of everything has no initial-load state
	// at all, but opting back in does not make it a newcomer: its identifier
	// mapping is still there, so the set it is sent is one it has been sent
	// before and the marker has to say so. Holding it on the load would lose it
	// exactly where a participant most needs it.
	previousLoadCompletedAt *time.Time

	// dropLoad arms one truncated initial-load stream, so that a test can put an
	// endpoint through the failure the state machine exists to survive: a set
	// that stops arriving without agrirouter ever having declared it sent.
	dropLoad bool

	// corruptLoad makes every initial-load stream open and then fail, rather
	// than end. It is the other shape of failed take: one that recurs on every
	// attempt, where dropLoad is survived by taking the set again.
	corruptLoad bool
}

func (e *endpoint) optedInto(t agmasync.EntityType) bool {
	return e.toggles[t]
}

// loadState is an endpoint's initial-load state. It exists only while the
// endpoint is opted into something; see endpoint.previousLoadCompletedAt for
// what outlives it.
type loadState struct {
	state        string
	awaitingUser bool
	updatedAt    time.Time

	// rejected holds the outcome of the last confirmation that carried
	// bindings, recomputed on every repeat of it.
	rejected []oapiRejection
}

// oapiRejection mirrors the wire type without importing the generated package
// into the store, so the store stays about the protocol rather than about JSON.
type oapiRejection struct {
	LocalID      string
	AgrirouterID uuid.UUID
	Reason       string
	Existing     *binding
}

type binding struct {
	LocalID      string
	AgrirouterID uuid.UUID
}
