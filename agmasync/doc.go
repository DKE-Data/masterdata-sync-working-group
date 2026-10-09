// Package agmasync implements the participant side of the Agriculture
// Masterdata Sync Protocol (AgmaSync), as defined by specification.md and
// openapi.yaml in the repository root.
//
// It is the part of the reference implementation that carries protocol
// behavior: the operations, the typed errors a participant has to branch on,
// consumption of the two event streams, and the initial-load state machine.
// Everything about how a participant stores or reconciles master data lives in
// the sample platform under reference-client/ instead, because that is not
// something the specification requires.
//
// The specification is the normative document. Where a symbol here exists
// because of a specific rule, its documentation names the section that rule is
// in, so this package doubles as an index into the specification.
//
// # Shape of the API
//
// Every operation is a function taking the generated client (oapi) and the
// identifiers it acts with, so a participant configures HTTP, authentication,
// and the base URL on the generated client it already holds.
//
// The specification distinguishes the application from the endpoint
// throughout, and so does this package. [Events] is application-scoped: an
// OAuth token authorizes an application, which may hold many endpoints across
// many tenants, and the live event stream belongs to the application and
// carries every one of them. Every other operation names the acting endpoint
// and its tenant. The entity operations name it by its agrirouter id, sent as
// x-agrirouter-endpoint-id, while the initial-load operations address it by
// the application's own external id, as endpoint management does. The two
// identifier styles are not interchangeable, which is why the two groups take
// different ones.
//
// Entities are the typed models. A write is per-type ([PutParty], [PutFarm],
// ...), as the model says which entity it is; the operations that carry only
// identifiers take an [EntityType]. A canonical object of any type, as an
// [Event] or [Deactivate] returns it, is an [Object] holding the model its
// type names. Attributes a model does not name are dropped, as a participant
// that does not model them would drop them anyway ("Writing an entity" in
// specification.md).
//
// # What this package deliberately does not do
//
// It does not persist anything. Two of the specification's rules are about
// durability — a delivery position must be derived from what the participant
// has durably applied, and an object must never be applied over a higher
// revision it already holds — and neither can be honored by a client library
// that does not own the store. Both are implemented in the sample platform,
// against a store that shows where the values go.
package agmasync
