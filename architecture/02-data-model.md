# ADR 02 - Canonical data model

- **Status:** WIP
- **Scope:** Masterdata Sync data modeling approach and specific models for farms, fields, and clients

## Context

Agricultural industry today has a variety of ways to represent farms, fields, and clients. 

To name a few:
- ISOXML - international xml-based standard ISO 11783-10
- EFDI that is protobuf, but carries same model as ISOXML
- ADAPT - another data model used in agriculture
- proprietary designed API's, such as f.e John Deere Operations Center which provides a REST API with its own data model

ISOXML and EFDI are relatively widely used, however over time those models were noted to have a certain ambiguity and in some situations lack of clarity thus creating disagreements between different implementations. Lack of central validation mechanism also contributes to the problem, as implementations are strict to different degrees in their interpretation of the standard.

Examples:

It is legal from schema perspective to define a partfield with no polygons, but instead define exterior and interior line strings under partfield, but not inside any particular polygon. This is not valid and could've been potentially resolved on schema level.

Some line strings are not required to be closed (f.e fences), however while they are not supposed to be inside a polygon, schema allows to define them there, which is not valid and could've been potentially resolved on schema level.

When representing partfield it is possible to define several polygons. The polygon is a collection of one exterior line string (collection of points describing the outer border of the polygon) and zero or more interior line strings (collection of points describing holes in the polygon).
Canonically standard requires that holes in the partfield surface are always described in the same polygon with the exterior line string in which these holes are located. However, in practice schema allows to define holes in a separate polygon, which on occasions causes some confusion. Note that in this example creating schema that would prevent this ambiguity most likely is not possible, however it is possible to validate the data and reject if it does not follow this rule.

Despite these examples the terminology and approaches used in ISOXML and EFDI are widely known and used in many software implementations.

## Decision

AgmaSync aims to support the same terminology and overall semantics as ISOXML and EFDI, however it is not a strict copy of either of them. 
AgmaSync defines its own canonical data model that is used for the purpose of Masterdata Sync.

AgmaSync also defines a set of validation rules and corresponding error responses that are used to enforce a canonical representation of all entities and their relationships.

See exact data model definitions in [openapi.yaml](../openapi.yaml) and explanatory [specification.md](../specification.md).
