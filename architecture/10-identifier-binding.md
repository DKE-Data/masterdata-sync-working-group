# ADR 10 - Binding a local identifier to a canonical object

- **Status:** WIP
- **Scope:** How an endpoint's `localId` becomes attached to a canonical object
  it did not create, and how it is detached when the endpoint no longer holds it

## Context

The mapping is keyed `(endpoint, localId)`, and a send that does not resolve
creates a canonical object
([Identifier mapping](../specification.md#identifier-mapping)). That covers the
endpoint introducing an entity to the network. It does not cover the opposite
and more frequent direction: an endpoint receives a canonical object, recognises
it as one it already holds, and has to say so.

[ADR 01](./01-reference-architecture.md) assumes the mechanism twice - the
receiver "reports it back, completing the id mapping" during seeding, and a
later edit resolves against that mapping - but neither the specification nor
`openapi.yaml` has it. Every write path is keyed by `{localId}` and
`agrirouterId` is `readOnly` on send.

The result is not a rejected call. [ADR 06](./06-initial-load.md)'s
load-to-agrirouter loop admits objects "changed while resolving a conflict",
which by definition already exist canonically, and the only answers available
are `201 Created` and a `409` raised for a different situation. So the endpoint's
reconciled object is sent, does not resolve, and is created a second time: a
silent duplicate of an object agrirouter delivered to it minutes earlier.

## Decision

Binding is an explicit, declarative operation, and is not a data write.

```
PUT /masterdata/{type}/{localId}/id-mapping/{agrirouterId}
x-agrirouter-endpoint-id: <the endpoint binding>
(no body)
```

- **`204`** - the mapping is recorded. Idempotent: re-binding the same pair changes nothing.
- **`409`** - `(endpoint, localId)` is already bound to a different canonical object, or this endpoint already has a different `localId` bound to that `agrirouterId`. Both are the n:1 case of [Asymmetric and non-unique mappings](../specification.md#asymmetric-and-non-unique-mappings) and are resolved in the endpoint. The body says which, and names the mapping in the way (see [A rejection names what is in the way](#a-rejection-names-what-is-in-the-way)).
- **`404`** - no such canonical object, or the endpoint is not entitled to it.

The mapping is an association, and both of its ends are in the path, so the
request carries no body: there is nothing for a body and a URL to disagree about,
and a retry after a lost response is trivially the same request. The endpoint at
the other end is not a segment because it is not one on the entity path either -
the binding is scoped exactly as the object it binds (see
[Which endpoint is acting](#which-endpoint-is-acting)). The segment carries the
name the concept already has in this API - `idMappings` on every delivered
entity, [Identifier mapping](../specification.md#identifier-mapping) in the
specification - rather than a second word for it.

Binding creates no revision, does not touch `sourceEndpointId`, and is delivered
to nobody. Nothing about the canonical object changes - only agrirouter's
knowledge of what this endpoint calls it. Afterwards the ordinary
`PUT /masterdata/{type}/{localId}` resolves to that object and updates it.

**An endpoint MUST bind before sending an object it received.** An unbound send
is not an error agrirouter can detect - it is a well-formed create, and it is
the duplicate above.

### The endpoint can also declare that it no longer holds an object

```
DELETE /masterdata/{type}/{localId}/id-mapping/{agrirouterId}
x-agrirouter-endpoint-id: <the endpoint binding>
```

`204` whether or not the pair was bound, so a retry after a lost response is
safe. `404` only for no such canonical object, or no entitlement. Same
properties as the bind: no revision, no `sourceEndpointId`, delivered to nobody.

The operation states *"I no longer hold this object"*. It is a claim about the
endpoint's own store, of the same kind as the bind and recorded the same way - at
face value, never inferred
([agrirouter never infers a mapping](#agrirouter-never-infers-a-mapping)). It is
not a `:deactivate`, which says the entity is inactive in the world and is
delivered to everyone, where this concerns one endpoint's copy.

Two situations produce it:

- the endpoint discarded its data while it was not a participant, and the mapping
  outlived the absence
  ([Disconnection and re-connection](../specification.md#disconnection-and-re-connection))
- its user deleted the object in its own system while the rest of the network
  kept it

In both the endpoint recreates the object under a *new* local identifier, since
most systems cannot choose their own primary keys, and binding that identifier
collides with the stale pair. Unbinding is what clears it.

Unbinding is not an instruction to stop sending. Opt-in is the only filter on
what an endpoint receives, so the object's next change is delivered again,
carrying no `localId`, and the endpoint treats it as new. An endpoint that wants
it back at once requests it by `agrirouterId` instead of waiting.

A wrong bind is recoverable through the same operation, within a limit: it
corrects identity, not data. Revisions a mis-bound endpoint already merged into
the canonical object stand and were fanned out. The `409` at bind time is where
the mistake is cheap to catch.

### Initial load binds in bulk

Matching happens across a whole canonical set, so binding one object per request
would put a round trip against every object an endpoint recognises. The
confirmation that ends reconciliation carries them instead:

```
PUT /endpoints/{eid}/masterdata-initial-load/farms/status
{
  "state": "LOADING_TO_AGRIROUTER",
  "idMappings": [ { "agrirouterId": "1f2e...4567", "localId": "b1e7" } ]
}
```

Same semantics as the singular operation, applied per pair. A pair that cannot be
recorded comes back in `rejectedIdMappings` rather than failing the transition -
one unresolvable n:1 should not block the load of a set - and the endpoint
handles it as it handles a `409` on the singular operation, by raising
`awaitingUser` if it cannot reconcile automatically.

### A rejection names the reason

A rejected pair carries a `reason` and, where there is one, the mapping that
holds the taken identifier:

```
"rejectedIdMappings": [
  { "agrirouterId": "1f2e...4567", "localId": "b1e7",
    "reason": "AGRIROUTER_ID_ALREADY_BOUND",
    "existingMapping": { "agrirouterId": "1f2e...4567", "localId": "a039" } }
]
```

The two n:1 causes are not the same problem for the endpoint. *Your `localId` is
taken* means it is about to attach a second canonical object to a row it already
has; *this object is taken* means the object arrived twice under different local
identifiers.

The same shape carries the singular `409`, so the endpoint has one rejection
handler rather than two.

Two rejections carry no `existingMapping`, nothing being in the way. An
`agrirouterId` the endpoint is not entitled
to, or that does not exist, is `UNKNOWN_OBJECT` - the `404` of the singular
operation, which on a bulk path cannot be a status code. And a request naming the
same `localId` or the same `agrirouterId` in two pairs is `DUPLICATE_IN_REQUEST`
for every pair involved, none applied: "applied independently" would otherwise
make the result depend on the order agrirouter happened to walk the list in, and
the endpoint is asserting something it contradicts within one request.

The state machine needs no new state for this. Binding is what "reconciled"
*means*, so it belongs on the transition that already asserts it.

### agrirouter never infers a mapping

Identity is declared by the endpoint, never derived by agrirouter from the
content of an object. Two fields with the same name on the same farm are
ordinary, boundaries differ between systems by simplification and projection,
and a wrong bind is tenant-wide, with an inverse that recovers the identity but
not the revisions written through it. The endpoint is also the only
side that knows: it is the one that reconciled, with its user where its own rules
could not settle it.

### Rejected alternatives

| Alternative | Why not |
|---|---|
| `agrirouterId` in the body of `PUT /masterdata/{type}/{localId}` | Fewest calls, and half of ADR 01 already draws it. But binding is then a data write: it sets `sourceEndpointId` to the binding endpoint, and since that endpoint has been holding its own copy the object usually differs from canonical somewhere, so it bumps `revision` and fans out a change nobody made. Where it happens to match exactly, no-op detection suppresses the revision and the mapping still has to be recorded on that suppressed path. Both branches need specifying, and one operation carries four outcomes - create, update, bind, conflict - instead of two. |
| A second path keyed by `agrirouterId` | Costs a path per entity type, the same as this decision, to buy the semantics of the row above, and breaks the uniform `/{localId}` keying of the rest of the API. |
| agrirouter matches on content | Above. |
| The endpoint keeps the correspondence privately | It already must, to reconcile deliveries against its own store. But a private table cannot be used on the write path, which is keyed by identifiers agrirouter knows. Nothing changes and the duplicates continue. |
| The confirmation's `idMappings` is authoritative for the set - pairs it omits are unbound | Covers the mass case in one call with no new verb. But an accidentally omitted pair then unbinds silently, it says nothing about an object deleted locally in ordinary operation, and it turns a list of assertions into a list plus an implied negation of everything else. |

## Consequences

- **The day-2 receive path gains a step.** An endpoint creating a local object
  for a delivered canonical one binds it in the same unit of work. Deferring is
  legal and costs nothing until that object is edited.
- **`idMappings` becomes complete.** The delivery clause that includes an
  endpoint's own `localId` when known starts to fire for objects it did not
  create, and the ISOXML LinkList correspondence the specification requires can
  actually be expressed.
- **References by `localId` keep working for received objects.**
  [References](../specification.md#references) resolve through the sender's own
  mapping, so an unbound endpoint could not refer to a farm it received but never
  edited, and would fall back to `agrirouterId` - the write path the design
  deliberately keeps clear.
- **`409` at bind time is the earliest the n:1 conflict can surface**, while the
  endpoint is reconciling and has a user in front of it, rather than later on an
  edit with the duplicate already created.
- **An endpoint's mapping tracks its own store in both directions.** What it
  holds it binds, what it loses it unbinds, and neither is a change to the
  entity. A stale pair - mapped to an object the endpoint no longer has - is
  recoverable rather than terminal.

