# ADR 17 - Holes instead of obstacles

- **Status:** WIP
- **Scope:** Supersedes the field boundary obstacles of
  [ADR 11](./11-entity-model.md). The rest of ADR 11 stands.

## Context

A field boundary could leave an area out of the field in two ways: as an
entry in `obstacles`, or as a hole in `boundary.geometry`. ISOXML adds a third,
a hole sent as a polygon of its own ([ADR 02](./02-data-model.md)).

ADAPT advises holes as further rings of `boundary.geometry`. Its obstacle is
being reworked into a physical object with attributes such as height or
depth, which tell the operations it affects. What an AgmaSync obstacle would
encode is open until then.

## Decision

- **No obstacles.** `obstacles` is removed without replacement.
- **Holes are further rings.** An area inside the boundary that is not part
  of the field, such as a pond, is a ring of the polygon that encloses it, not
  a polygon of its own. A field inside such an area, such as an island in the
  pond, is another polygon of a `MULTIPOLYGON`.
- **The position of a ring says what it is**: the first is the exterior, every
  further one a hole. Its direction carries no meaning; agrirouter normalizes
  it, and orders holes and polygons, so an area has one encoding
  ([Encoding](../specification.md#encoding)).

## Consequences

- Not expressible: an obstacle's kind, name and passability, and point or line
  obstacles such as poles and fences.
- ISOXML and EFDI interior rings that are not passable map to holes.
- agrirouter does not check that a hole lies inside its exterior or that rings
  do not cross.
