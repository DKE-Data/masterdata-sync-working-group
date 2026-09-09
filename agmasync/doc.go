// Package agmasync implements the participant side of the Agriculture
// Masterdata Sync Protocol (AgmaSync), as defined by specification.md and
// openapi.yaml in the repository root.
//
// It is the part of the reference implementation that carries protocol
// behaviour: the operations, the typed errors a participant has to branch on,
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
// The specification distinguishes the application from the endpoint
// throughout, and so does this package. A [Client] is application-scoped: an
// OAuth token authorizes an application, which may hold many endpoints across
// many tenants, and the live event stream belongs to the application and
// carries every one of them. An [Endpoint], obtained from [Client.For], is a
// handle on one of those endpoints, and carries every operation that has to
// name the acting endpoint — which is all of them except the application
// stream.
//
// # What this package deliberately does not do
//
// It does not persist anything. Two of the specification's rules are about
// durability — a delivery position must be derived from what the participant
// has durably applied, and an object must never be applied over a higher
// revision it already holds — and neither can be honoured by a client library
// that does not own the store. Both are implemented in the sample platform,
// against a store that shows where the values go.
package agmasync
