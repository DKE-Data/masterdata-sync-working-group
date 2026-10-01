# ADR 12 - A field boundary references its field

- **Status:** WIP
- **Scope:** The direction of the reference between a field and its boundaries

## Context

FarmSPT nests boundaries under the field, and AgmaSync mirrored it:
`field.field_boundaries` listed the field's boundaries. The list is state on the
field, so adding or removing a boundary is a revision of the field.

Under merge patch an array is replaced whole. Two endpoints that each add a
boundary from the same base both change `field_boundaries`, so the second is
rejected with `412` ([Concurrency control](../specification.md#concurrency-control)),
although neither touched the other's boundary. An endpoint that adds a boundary
also revises a field that another endpoint may be editing.

ADAPT has the reference the other way, `fieldBoundary.fieldId`.
[ADR 11](11-entity-model.md) recommends following it.

## Decision

**A field boundary references its field.** `field` (reference, required)
replaces `field.field_boundaries`. A field's boundaries are the boundaries that
name it.

- Adding, changing or removing a boundary revises the boundary only.
- Moving a boundary to another field is a change of its `field`.
- A boundary's `harvest_period` must still fall within that of the field its
  `field` names.

## Consequences

- **Dependencies reverse.** A boundary follows its field on send and on
  delivery. It moves from tier 0 to a tier after `field`
  ([ADR 07](07-sync-streaming.md#the-sweep-delivers-in-tier-order)).
- **Opt-in.** Declaring or selecting field boundaries requires fields. Fields
  no longer require field boundaries, so an endpoint without geometry can
  exchange fields alone.
- **Finding a field's boundaries** is a lookup on the receiving side, not a list
  on the field.
- **Encoding only.** FarmSPT's content is unchanged. ISOXML nests polygons in the
  partfield, so an ISOXML participant inverts at its edge.
