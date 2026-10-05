# ADR 13 - Optional references between parties, farms and fields

- **Status:** Decided
- **Scope:** Which references constrain opt-in, and who enforces the rest

## Context

Some platforms model parties and fields but no farms. Others model farms and
fields but no parties. Opt-in required `fields → farms → parties`, and a farm
required an `owner`, so neither could declare fields, and a platform without
parties could not write a farm.

## Decision

**Only a boundary's `field` is a required reference.** Farm `owner` becomes
optional. Declaration and selection are closed over `field boundaries → fields`
alone, and any other combination of entity types is valid.

**Participants handle the optional references.** An endpoint ignores a reference
to a type not selected on it and leaves that attribute out of its writes, which
keeps it under merge patch. Its own data model already decides which attributes
it requires on its own writes. For data from others, the stricter recipient
handles it ([Differing required/optional attributes](../specification.md#differing-requiredoptional-attributes)).

**agrirouter enforces none of it.** Writes are validated against the schema only.

**Delivery is unchanged.** References are delivered as stored.

## Rejected alternatives

| Alternative | Why not |
|---|---|
| Per-declaration profiles enforced by agrirouter (full: farm `owner` required; farm+field: field `farm` required; party+field: field `owner` required) | Each platform's model already enforces this on its own writes. It adds profile checks, error codes and profile changes for a guarantee readers still cannot rely on, since data from other profiles arrives incomplete anyway. |
| A placeholder owner party for farms without one | A guess that becomes permanent and never matches. |

## Consequences

- **Tiers and sweeps are unchanged.** Selections only remove references from the
  graph, so the [tier order](07-sync-streaming.md#the-sweep-delivers-in-tier-order) still holds.
- **A writer that sends `null` for a reference it does not model removes it for
  everyone.** A generator serializing unset attributes as `null` does this, and
  there is no history to restore from.
- **Ownership models do not translate.** A field from a farm+field platform reaches a
  party+field platform without an owner, and the reverse without a farm. A user
  links each object once, and merge patch keeps the link afterwards.
- **Field ownership through the farm is invisible without farms.** A field whose
  owner is only inherited from its farm arrives ownerless at an endpoint that
  does not select farms.
