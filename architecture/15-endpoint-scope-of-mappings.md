# ADR 15 - Identifier mapping scoped to the endpoint

- **Status:** WIP
- **Scope:** What a `local_id` is unique within, how a delivered object carries
  it, and what happens to a mapping when its endpoint is removed

## Context

[ADR 14](./14-tenant-scope-of-objects.md) keyed the mapping
`(application, tenant, local_id)`: one `local_id` per application, tenant and
canonical object, whichever of the application's endpoints in that tenant sends
it. That rests on an application keeping one store behind all of its endpoints
in a tenant.

Often, however, applications keep one store per endpoint instead. Where each endpoint represents a
different organization or account, each has its own data and its own
identifiers: John Deere Operations Center is one example, where the same field
is `abc` in org A and `xyz` in org B. Keyed per tenant, the integrator can bind
only one of them - binding the second is a `409`
([ADR 10](./10-identifier-binding.md)) - so it has to mint a surrogate `local_id`
per canonical object and keep its own table from that surrogate to the
identifier in each store. That is a second identifier mapping, beside the one
agrirouter keeps, and every such integrator has to build and maintain it.

Taking that burden away from integrators is the point of the identifier mapping:
agrirouter should hold the correspondence for every store an integrator
connects, as that store identifies its records.

## Decision

### The mapping is keyed by the endpoint

A `local_id` names a record only in the namespace of the endpoint that sent it.
agrirouter identifies the endpoint in the mapping by the application's
`external_id` for it, which is unique within the tenant, so the key is
`(application, tenant, external_id, local_id)`. The tenant is there only because
`external_id` alone does not name an endpoint; an endpoint belongs to one tenant,
and everything [ADR 14](./14-tenant-scope-of-objects.md) decided about tenants
stands.

- A send by endpoint E under X resolves `(E, X)`, or creates an object in E's tenant.
- Two endpoints of one application may use the same `local_id` for unrelated
  objects, and different `local_id`s for the same one. Neither is a `409`.
- `x-agrirouter-endpoint-id` already names the acting endpoint on every write,
  bind, and request, so no operation changes shape.

### A delivered object is rendered per recipient endpoint

Every `local_id` in a payload - the object's own and one per
[reference](../specification.md#references) - is resolved in the recipient's
namespace, and the ADAPT `Id` carries one `referenceId`. A frame therefore
serves exactly one endpoint and names it in `recipient_endpoint_id`. An object
entitled to two of an application's endpoints arrives twice, once rendered for
each. The stream stays one per application with one position
([ADR 07](./07-sync-streaming.md)), and fan-out happens at render time. All
frames of one object carry the same `id`, so a participant that resumes after
applying only some of them is sent all of them again.

### The mapping does not survive the endpoint

The mapping is the endpoint's, so its lifecycle is the endpoint's too. Removing
an endpoint discards its mapping in the same step, as it discards the endpoint's
initial-load state and routes, and an endpoint onboarded later under the same
`external_id` starts empty. agrirouter then holds no mapping without an owner,
and needs no rules for when an orphaned one expires. Opting types out and
removing the masterdata route keep it, as before.

A participant SHOULD keep its own correspondence table for a removed endpoint's
`external_id`. On re-onboarding the canonical set arrives with no `local_id`s
and no repeat marker; a participant that kept the table matches it by
`agrirouter_id` and re-binds in bulk on the initial-load confirmation, so the
re-onboarding reconciles without its user. Only what diverged while the
endpoint was absent can need one. The user still creates the masterdata route
again: initial load stays user-initiated.

A [masterdata reset](../specification.md#masterdata-reset) is announced to every
application that has had an endpoint in the tenant take part, including one
whose endpoints there were all removed: agrirouter retains that fact per
application and tenant. The event is what tells such an application to discard
the tables it kept, which would otherwise name objects that no longer exist.

## Rejected alternatives

| Alternative | Why not |
|---|---|
| Keep `(application, tenant, local_id)` | Every integrator with one store per endpoint keeps a second mapping of its own, which is the burden the identifier mapping exists to take away. One store per organization or account is common, not exotic. |
| Key by the agrirouter endpoint id | Equivalent while the endpoint lives. The `external_id` is what the participant's own table and the initial-load paths already use, and it is what a re-onboarded endpoint shares with the removed one. |
| Mapping survives endpoint removal | Matches a re-onboarded endpoint without the participant's help, but leaves mappings with no endpoint to own them, and with them the question of how long agrirouter keeps them for an endpoint that may never return. Every other piece of an endpoint's state ends with it. |


## Consequences

- **Integrators use each store's own identifiers.** An integrator with one store
  per endpoint sends that store's ids as `local_id`s and keeps no mapping of its
  own; within a tenant, agrirouter's per-endpoint frames say which store a change
  is for.
- **Every endpoint binds for itself.** A participant with one store behind
  several endpoints holds one binding per (endpoint, object). A sibling that
  sends before binding creates a duplicate, and no safeguard in agrirouter can
  tell that from a new entity ([Loop prevention](../specification.md#loop-prevention)).
  The frame without a `local_id` is its cue to bind.
- **More frames, same position.** Only applications with several entitled
  endpoints in one tenant receive more than one frame per change; delivery state
  stays per application.
- **A participant that keeps its table re-onboards without user intervention.** agrirouter
  discards a removed endpoint's mapping, so the re-onboarded endpoint's set
  arrives unmatched; the participant's own table is what matches it.
