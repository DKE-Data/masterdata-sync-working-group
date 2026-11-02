# ADR 18 - Polygons valid as in OGC Simple Features

- **Status:** WIP
- **Scope:** Supersedes the unchecked polygon validity of
  [ADR 17](./17-holes-not-obstacles.md). The rest of ADR 17 stands.

## Context

A hole has meaning only inside its exterior. A hole outside it, crossing
rings, or a hole sent as an overlapping polygon of its own
([ADR 02](./02-data-model.md)) leaves the field's area undefined, and every
receiver would resolve it differently.

## Decision

agrirouter rejects a `POLYGON` or `MULTIPOLYGON` that is not valid as in
[OGC Simple Features](https://portal.ogc.org/files/?artifact_id=25355)
(06-103r4 6.1.11), checked in longitude and latitude. It does not repair it.

## Consequences

- A receiver can compute a field's area from the geometry alone.
- A sender with self-intersecting or overlapping geometry gets a 400.
