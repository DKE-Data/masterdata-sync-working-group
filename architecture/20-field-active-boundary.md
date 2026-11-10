# ADR 20 - A field names its active boundary by a weak reference

- **Status:** WIP
- **Scope:** The field's active boundary, and weak references. For weak
  references, supersedes [ADR 07](./07-sync-streaming.md)'s "an order that
  respects the graph respects every object dependency". The rest of ADR 07 and
  [ADR 12](./12-boundary-references-field.md) stand.

## Context

ADAPT's field names its active boundary (`activeBoundaryId`). JDOC keeps one
active boundary per field. AgmaSync could not express it: a boundary references
its field (ADR 12), so a reference back from the field makes a cycle the tier
order cannot respect.

## Decision

- A field carries `_agmasync_activeBoundaryRef`, optional, typed `WeakRef`.
- A **weak reference** is a reference outside the dependency order. Its wire
  form is that of `Ref`. The schema is separate so the difference is visible
  to readers and in generated code.
- **On send it resolves like a `Ref`.** Its target must have been sent or bound first, and
  is checked for tenant and entity type. A field and a new active boundary take
  three writes: field, boundary, field.
- **On delivery its target may come later.** The tier order is unchanged, so on
  initial load and catch-up a field arrives before its boundaries. A receiver
  applies the field without waiting and links the boundary when it arrives. The
  target is always in the set: it existed when the reference was written.
- No new opt-in closure. No cascade: moving or deactivating the boundary leaves
  the reference. A receiver treats a deactivated boundary, or one naming another
  field, as no active boundary.

## Rejected alternatives

| Alternative | Why not |
|---|---|
| A flag on the boundary | "One per field" cannot be enforced. Switching takes two writes, with none or two active in between. |
| A plain `Ref`, with the exception in prose | Invisible to generated code. Every implementation would hard-code one attribute name. |
| Forward reference on send: an unsent `referenceId` reserves an identifier, the later create takes it (two writes) | Saves one field revision per field created with an active boundary, at the cost below. |
| agrirouter checks that the boundary names the field | Goes stale when the boundary moves, so receivers check anyway. agrirouter enforces no references (ADR 13). |
| agrirouter clears the reference when the boundary moves or is deactivated | agrirouter would write revisions no participant made. |

### Forward reference against three writes

Both need the receiver rule above: the delivery order puts a field before its
boundary whichever way it was written. They differ on send only.

| | Forward reference | Three writes |
|---|---|---|
| Field revisions, field with new active boundary | 1 | 2 |
| Switch to an existing or new boundary | same | same |
| Weak target can be absent forever | yes | no |
| Aborted sequence leaves | dangling reference, stuck `local_id`, a wrong link if the `local_id` is reused | no active boundary |
| agrirouter write path | mappings without objects, reserved creates, rollback on rejection, release by correction, `404` on request | as `Ref` |
| Spec | reservation, release, exceptions in references, writing, correction and requests | the delivery rule |

Three writes cost an extra field revision only when a field and its active
boundary are both new. Switching a field's active boundary costs the same in both.
New fields come in bulk when an application first loads its data into
agrirouter: one extra revision per field that has an active boundary, written
once and delivered to each receiver. Afterwards new fields are rare. The forward
reference would instead add a state that every later feature touching the
identifier mapping has to account for.

## Consequences

- A writer leaves out a weak reference whose target it has not sent, and writes
  the object again once the target exists.
- A receiver keeps an unresolved weak reference and links it when the target
  arrives. One that drops it loses the link: the field is not delivered again
  when its boundary is created. Leaving the reference out of its writes keeps
  it for others; sending `null` removes it.
