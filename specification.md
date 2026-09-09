# Introduction

Agricultural operations routinely involve several software products and machine
platforms from different vendors: farm management information systems (FMIS),
machine manufacturer platforms, terminal software, and service providers. The
same real-world objects — the customers a contractor works for, the farms they
belong to, and the fields that are worked — are represented independently in each
of these systems. Today these representations drift apart: a field boundary
redrawn in one system is not reflected in the others, a new customer must be
entered by hand everywhere, and RTK-surveyed boundaries or guidance lines are
hard to move between products.

This document specifies the *Agriculture Masterdata Sync Protocol* (**AgmaSync**)<sup>1</sup>: a set
of data formats and operations enabling connected systems
exchange and continuously synchronize agricultural **master data** across the
agrirouter platform, bidirectionally, over an n:m network of participants.

<sup>1</sup>"**AgmaSync**", derives from **Ag**riculture **Ma**sterdata **Sync**hronization and echoes the ancient Greek word **agma** (ἆγμα), meaning "fragment", symbolizing the bringing together of distributed information into a unified dataset.

## Scope

First iteration would cover the following entity types, referred to
throughout as the **MVP entities**:

- **Organizations**
- **Persons**
- **Farms**
- **Fields**, including their metadata
- **FieldBoundaries**, including their obstacles, and metadata

Further entity types (points of interest, guidance/AB lines, inputs, crops, work
orders, and work records) are out of scope for this version and are expected to
be added later without breaking the mechanisms defined here. Split and merge
lineage is deferred with them (see [Split and merge](#split-and-merge)).

## What this protocol is, and is not

This protocol facilitates the *synchronization* of master data. To do so,
agrirouter maintains a canonical copy of each entity (see
[the SSOT store](#agrirouter-as-the-single-source-of-truth)). That storage exists **only** to enable synchronization. In particular,
this protocol is explicitly **not**:

- a history or archival store for master data;
- a data-maintenance user interface;
- a product marketed as a central "source of truth" for the industry.

Users continue to create and edit master data in whichever connected system they
prefer. Any participating system may disconnect and later reconnect with no loss
of synchronization capability.

## Notational conventions

Data structures in this document are illustrated using JSON {{?RFC8259}} for
readability. Unless a section states otherwise, these illustrations are
**non-normative examples**. The normative on-the-wire encoding is defined in
[Encoding](#encoding). Field geometries are expressed using GeoJSON {{?RFC7946}}, and
timestamps use the date and time formats of {{?RFC3339}}.

# Terminology

**Participant:**
a software product or machine platform connected to agrirouter that takes part
  in master-data synchronization through one or more endpoints.

**Endpoint:**
an agrirouter endpoint as defined by the agrirouter platform. A participant may
  operate one or more endpoints, typically one per organization its
  own product holds. Master-data opt-in is configured per endpoint
  (see [Routing and opt-in](#routing-and-opt-in)), and the endpoint is the scope
  of the identifier mapping (see [Identifier mapping](#identifier-mapping)).

**Entity:**
a single master-data object of one of the supported types (an organization, a person, a farm,
  a field, or a field boundary).

**Canonical object:**
the version of an entity held by agrirouter in the Single Source of Truth store
  (see [the SSOT store](#agrirouter-as-the-single-source-of-truth)). It carries the agrirouter-assigned identifier and the
  cross-endpoint identifier mapping.

**Local identifier:**
the identifier by which one endpoint knows an entity in its own store. A
  participant's store is scoped to the endpoint, not to the participant: the
  same record can exists in several of the organizations a product holds,
  each of which is its own endpoint. A local identifier is therefore unique
  within an endpoint and says nothing outside it — two endpoints of one
  participant may use the same local identifier for unrelated entities, and
  different local identifiers for the same entity.

**agrirouter identifier:**
the stable, globally unique identifier that agrirouter assigns to the canonical
  object. Formatted as a UUID {{?RFC4122}}.

**Source system:**
for a given change, the participant in which the change originated.

# Architecture overview

## agrirouter as the Single Source of Truth

agrirouter holds a **Single Source of Truth (SSOT) entity store**. For every
synchronized entity it stores:

1. the *canonical version* of the entity, and
2. an *identifier mapping* that records, for each endpoint that knows the entity, that endpoint's local identifier for it.

The SSOT store is what makes robust n:m synchronization tractable: it provides a
single place against which updates are reconciled, it powers loop prevention (see
[Loop prevention](#loop-prevention)), and it lets a newly connected or returning system be seeded from a
known-good set (see [Initial load](#initial-load)).

The choice of a canonical central store — rather than a purely meshed exchange —
is a settled design decision for this protocol. The previously considered
standalone identifier-mapping mechanism is **not** a separate component; identifier
mapping is an intrinsic property of the SSOT store.

## Design principles

The protocol is designed around four principles. Conforming behaviour is derived
from them, and they SHOULD be used to resolve questions this document leaves open.

- **n:m exchange.** Any reasonable number of connected systems can exchange the same master data with each other.
- **agrirouter is the SSOT.** A canonical version of every entity plus a mapping to each endpoint's own identifier reduces drift and prevents update loops.
- **Hard validation and canonicity.** The exchange format MUST be unambiguous: every logical value has exactly one valid encoding, and non-conforming payloads are rejected rather than repaired (see [Encoding and canonicity](#encoding-and-canonicity)).
- **Facilitation, not a product.** The canonical store exists only to enable synchronization; it is not exposed or marketed as a standalone data product.

## Operations

Synchronization is carried by the HTTP operations of the companion OpenAPI
document (`openapi.yaml`). Each entity type has its own paths; `<types>` below is
the collection of one supported entity type (`organizations`, `persons`, `farms`,
`fields`, `field-boundaries`):

| Operation                                          | Purpose                                                                                        |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `PUT /masterdata/<types>/{localId}`                | Sends the entity itself (creation or update). An update carries the revision it was edited from, see [Concurrency control](#concurrency-control). |
| `POST /masterdata/<types>/requests`                | Actively requests an entity ("lazy loading"), see [Requesting objects](#requesting-objects-lazy-loading). |
| `POST /masterdata/<types>/{localId}/deactivation`  | Signals that the entity was deactivated in its source system (archival, deletion, or similar). |
| `PUT`/`DELETE /masterdata/<types>/{localId}/id-mapping/{agrirouterId}` | Binds or unbinds the endpoint's own identifier, see [Identifier mapping](#identifier-mapping). |

What agrirouter sends would potentially travel on two streams, which serve different
purposes and are independent of each other:

| Stream | Scoped to | Carries |
| --- | --- | --- |
| `GET /masterdata/events` | the application | live changes: canonical objects and deactivations, for every tenant the application is routed to. One frame per recipient endpoint, named by `recipientEndpointId` (see [Common envelope](#common-envelope)) |
| `GET /endpoints/{externalEndpointId}/masterdata-initial-load/events` | one endpoint | the canonical set of every opted-in entity type for that endpoint's [initial load](#initial-load), ending when the response closes |

Only `/masterdata/events` carries a position, as `Last-Event-ID` (see
[Downtime and resume](#downtime-and-resume)). An initial-load stream delivers a fixed limited set rather
than a sequence of changes and has no position at all.

The two may run concurrently and are not deduplicated, so an
object may potentially arrive on both if there was a change to object in the source system while an endpoint is loading corresponding set of objects. Since applying to local store should be idempotent
and guarded by `revision` (see
[Applying what agrirouter returns](#applying-what-agrirouter-returns)).

A write operation answers with the resulting canonical object, which is the
second channel and is not merely an acknowledgement — see
[Applying what agrirouter returns](#applying-what-agrirouter-returns).

Which entity types an endpoint takes part in is opt-in rather than a
declared capability, and is not directional in the MVP: an opted-in entity type
is exchanged in both directions (see [Routing and opt-in](#routing-and-opt-in)).

# Data model

This section defines the canonical object model for the MVP entities, fully aligned and reconciled with the outcomes of the FarmSPT project.
The model is deliberately close to the ISOXML / EFDI representation of the same concepts
(ISO 11783-10) so that existing task-data tooling can map to and from it, while
being expressed here in an encoding-independent way.

## Common envelope

Every entity shares a common envelope. Example
(non-normative):

~~~ json
{
  "type": "field",
  "agrirouterId": "1f2e3d4c-5b6a-7089-90ab-cdef01234567",
  "localId": "PFD-00042",
  "active": true,
  "revision": 7,
  "modifiedAt": "2026-07-14T09:20:00Z",
  "tenantId": "3a4b5c6d-7e8f-9012-3456-789abcdef012",
  "sourceEndpointId": "9f8e7d6c-5b4a-3210-fedc-ba9876543210",
  "recipientEndpointId": "2b3c4d5e-6f70-8192-a3b4-c5d6e7f80912"
}
~~~

Envelope fields:

- `type` (string, required): the entity type; one of `organization`, `person`, `farm`, `field`, `fieldBoundary`.
- `agrirouterId` (string): the agrirouter-assigned canonical identifier ({{?RFC4122}}). It is assigned by agrirouter on first receipt and is absent when a source system creates a not-yet-known entity. It MUST NOT be chosen or changed by a participant.
- `localId` (string): **always the identifier of the endpoint at the near end of the transfer, never of any other.** On send it is the sending endpoint's own identifier for the entity, and is required. On delivery agrirouter replaces it with the *receiving* endpoint's own identifier, and omits it when there is none — see [Identifier mapping](#identifier-mapping). "Another endpoint" includes a sibling endpoint of the same participant: a delivered object never names an identifier the receiving endpoint did not itself declare.
- `active` (boolean): whether the entity is currently active. Deactivation is expressed through the deactivation operation (see [Deactivation](#deactivation)); `active` on a delivered object reflects the current SSOT state.
- `revision` (integer): a monotonically increasing counter maintained by agrirouter for the canonical object. It is central to loop prevention and conflict detection (see [Loop prevention](#loop-prevention)). It is never taken from a sent object: the revision a participant edited from travels in the `x-agrirouter-base-revision` header, where it is compared and discarded (see [Concurrency control](#concurrency-control)).
- `modifiedAt` (string): the {{?RFC3339}} timestamp of the last accepted change.
- `tenantId` (string): the tenant the object belongs to ({{?RFC4122}}). It is set by agrirouter and MUST NOT be sent by a participant; on send the tenant follows from the acting endpoint, and any value a sender supplies is ignored. A single application stream carries every tenant the application is routed to (see [Routing and opt-in](#routing-and-opt-in)), so on delivery this is the field that says which of them an object belongs to, and a receiver holding data for several tenants MUST partition on it rather than on the connection.
- `sourceEndpointId` (string): the endpoint whose change produced the current canonical revision. It always belongs to `tenantId`. It is the key [origin suppression](#loop-prevention) is decided on.
- `recipientEndpointId` (string): on delivery, the endpoint this copy is for — the endpoint whose `localId` the envelope and every reference within it carry. It is set by agrirouter and MUST NOT be sent by a participant. Because the mapping is scoped to the endpoint, a single application stream carries one frame per recipient endpoint rather than one per object: an application with two opted-in endpoints in one tenant receives the same canonical object twice, rendered for each. On a write response it is the acting endpoint, which named itself in `x-agrirouter-endpoint-id`.

### References

A **reference** to another entity (for example a field referring to its farm) is
expressed as an object carrying the target's `agrirouterId`, the referencing
endpoint's `localId` for the target, or both:

~~~ json
{ "agrirouterId": "7c1d2e3f-4a5b-6c7d-8e9f-001122338899", "localId": "FRM-7" }
~~~

On the field of the envelope example above:

~~~ json
{
  "type": "field",
  "agrirouterId": "1f2e3d4c-5b6a-7089-90ab-cdef01234567",
  "localId": "PFD-00042",
  "name": "North 40",
  "farm": {
    "agrirouterId": "7c1d2e3f-4a5b-6c7d-8e9f-001122338899", "localId": "FRM-7" }
}
~~~

The `localId` in a reference is the *referencing* endpoint's identifier for the
target, not the target's canonical one. The two identifiers are therefore not
interchangeable in both directions:

- **On send**, a participant MAY use either. A reference carrying only a `localId`
  is resolved by agrirouter against the acting endpoint's own
  [identifier mapping](#identifier-mapping). If it does not resolve — the target has
  not been sent by that endpoint yet — the request MUST be rejected, and the
  participant MUST send the target before the object referencing it, through the
  same endpoint.
- **On delivery**, agrirouter MUST populate `agrirouterId`, and MUST replace the
  `localId` with the receiving endpoint's own identifier for the target, omitting it
  when there is none. It MUST NOT be left as the sender's: a receiving endpoint
  resolves the target through `agrirouterId`, so the sender's `localId` carries no
  meaning in the receiver's namespace, and passing it through would disclose the
  sender's internal key for the target. This is the same rule the envelope's own
  `localId` follows (see [Common envelope](#common-envelope)), and it is why a
  delivered object is rendered per recipient endpoint rather than once per
  object: every reference in it is resolved in that endpoint's namespace.

A reference to a party MUST additionally carry `type` (`organization` or `person`).
A receiving endpoint that does not hold the target has to
[request it](#requesting-objects-lazy-loading), which is per entity
type; the `agrirouterId` alone does not tell it which type to request. Slots whose
entity type is fixed — a field's farm — need no discriminator.

This keeps `agrirouterId` off the write path: a participant builds references from
its own identifiers, and does not have to capture and correlate canonical ids before
it can send the objects that reference them. Ordering still applies — a target must
be sent before the first reference to it.

## Party

A **party** is a legal or natural actor: an *organization* or a *person*. Both
share a common attribute set, because a farmer holds the fiscal identifiers
exactly as a company does. Only the commercial register entry is specific to
organizations.

Common attributes:

- `address` (object, optional): `street`, `poBox`, `postalCode`, `city`, `state`, `country` (ISO 3166-1 alpha-2).
- `contact` (object, optional): `phone`, `mobile`, `email`.
- `billingAddress` (object, optional): as for `address`.
- `taxNumber` (string, optional): identifier assigned by tax authorities.
- `taxId` (string, optional): numerical identifier assigned by tax authorities.
- `tradeId` (string, optional): numerical identifier assigned by public authorities.

No attribute records that a party *is* a contractor or *is* a customer. Those are
relations, and are read from the graph: a party that appears as a `partner` on
another party's farm is acting as a contractor or advisor there, and a party
whose farm names such a partner is that partner's client. The same party may do
both at once, in different relations.

## Organization

A legal entity that may hold land and to which persons may belong.

Canonical attributes, in addition to the common party attributes:

- `name` (string, required).
- `commercialRegistryNumber` (string, optional): unique identifier out of the commercial register.

## Person

A natural person. A person may hold land in their own right, and may belong to
one or more organizations.

Canonical attributes, in addition to the common party attributes:

- `lastName` (string, required).
- `firstName` (string, optional).
- `title` (string, optional).
- `memberships` (array, optional): the organizations this person belongs to. Each entry carries:

  - `organizationId` (reference, required): the organization.
  - `memberRole` (string, required): the role held there, from the [ADAPT Role](https://adaptstandard.org/dtd.html) list.

A person holding at least one membership is a **member** of the organizations it
names. Membership is state on the person, so one advisor serving several
organizations is a single canonical person rather than a copy per organization.

## Farm

A grouping of fields that the farmer considers part of the same management group.

Canonical attributes (subset):

- `owner` (reference, required): the organization or person that holds the farm.
- `name` (string, required).
- `address` (object, optional): as for a party.
- `geoReference` (`Point`, optional): longitude and latitude of the farm.
- `specialisedUsageType` (string, optional): production orientation of the farm, such as arable farming, dairy, vineyard, or orchard. Free-form. Participants SHOULD draw values from [AGROVOC](https://agrovoc.fao.org/) where a matching concept exists.
- `partners` (array, optional): parties holding a role on this farm — the contractor that works it, the advisor that reads it. Each entry carries:

  - `partnerId` (reference, required): the organization or person.
  - `partnerRole` (string, required): the role, from the same [ADAPT Role](https://adaptstandard.org/dtd.html) list as `memberRole`.

`partners` records a business relationship only. It MUST NOT be interpreted as
granting access to the farm or to anything below it: what an endpoint receives is
determined by opt-in and routing (see [Routing and opt-in](#routing-and-opt-in)),
never by an attribute inside a synchronized object. A participant receiving a
farm MUST NOT translate its `partners` entries into access grants in its own
system without a separate decision by its user.

## Field

A named physical space where production agriculture takes place, used to partition and identify data.
Canonical attributes (subset):

- `name` (string, required).
- `area` (number, optional): nominal area in square metres.
- `farm` (reference, optional): the farm this field belongs to.
- `owner` (reference, optional): the organization or person holding this field, for systems that attribute fields to a party directly. When absent, the field is held by its farm's owner. When present, it takes precedence for this field — that is how a field held by one party but managed under another's farm is expressed. A field MAY carry `owner` without a `farm`.
- `soil`(object, optional): 
  - `type` (Enum value like: `SAND`, `LOAMY_SAND`, `HEAVY_LOAMY_SAND`, `SANDY_TO_SILTY_LOAM`, `CLAYEY_LOAM`, `CLAY`), 
  - `ratingPoints` (integer 0–100, optional): soil rating points (Bodenzahl / Ackerzahl). Germany only, as defined by the [Bodenschätzungsgesetz](https://www.bundesfinanzministerium.de/Content/DE/Standardartikel/Themen/Steuern/Weitere_Steuerthemen/2014-07-21-bodenschaetzung-anlage-VRBodSchaetzG.pdf?__blob=publicationFile&v=1).
- `topography`(number, optional): slope, gradient like 7°
  - 👷‍♂️ _to be refined_
- `fieldBoundaries` (array, optional): references to the field [boundaries](#fieldboundary) as a GeoJSON
- `harvestPeriod` (object, optional): see [Harvest period](#harvest-period).
- `metadata` (object, optional): additional key/value metadata that does not fit a defined attribute. Participants MUST preserve metadata they do not understand and MUST relay it unchanged.

## FieldBoundary

A geometry that identifies the geo-spatial coordinates of a field.
The boundary can be used to define the area for a particular operation, a particular crop or crops, or for legal purposes.
A field can have different boundaries that may vary in geometry based on their specific use.
A field boundary include the outer boundary of the editable area and obstacles within the field (such as poles, biotopes or wet patches) that are left out.
Canonical attributes:

- `boundary` (GeoJSON): the field boundary as a GeoJSON `Polygon` or `MultiPolygon` {{?RFC7946}}.
- `boundaryType` (string): the boundary classification. Enum value like:

  - `CONCEPTUAL`: Used to define fields at the highest level, e.g. for communication with service providers.
  - `OPERATIONAL`: Used to define management areas for specific fieldwork.
  - `ECONOMIC_DEFINED`: Used for planning and analysis for economic purposes, e.g. for sustainability programmes or invoicing.
  - `ADMINISTRATIVE_RECEIVED`: Used for data organisation see [AgGateway](https://aggateway.org/Portals/1010/WebSite/About%20Us/FIELD%20BOUNDARY%20FLYER%20122123.pdf?ver=2024-01-03-212959-590)
- `creationMethod` (string): Enum value like:

  - `UNKNOWN`:	Creation method is unknown
  - `MANUAL`: Hand drawn in a computer system (FMIS) based on imagery or other information.
  - `DRIVEN`: Record a series of points that define the boundary by driving a machine (e.g. tractor) equipped with a GNSS receiver around the perimeter of the field.
  - `SURVEYED`: Defined by a professional surveyor	VALID
  - `AUTO_OPERATION`: Automatically generated in a software tool based on an as-applied/coverage map from a field operation
  - `AUTO_IMAGERY`: Automatically generated in a software tool based imagery
  - `ADMINISTRATIVE`:	Boundary is provided by some third party authority (generally governmental) and actual creation method is unknown (Based on [ADAPT Data Type: BoundaryCreationMethod](https://adaptstandard.org/dtd.html)
- `harvestPeriod` (object): see [Harvest period](#harvest-period). If the field that references this boundary also defines a `harvestPeriod`, the boundary's period MUST fall within it: `validFrom` no earlier than the field's `validFrom`, and `validTo` no later than the field's `validTo` (an absent field `validTo` imposes no upper bound).
- `obstacles` (array, optional): obstacles within the field, each a GeoJSON `Feature` whose geometry is a `Point`, `LineString`, or `Polygon` and whose properties carry an obstacle `kind`.
- `regulatoryRequirements` (string, optional): Enum value like:

  - `RED_ZONE_NITROGEN`: Red zone identification (N,P overfertilization)
  - `WATER_PROTECTION_AREA`: Including a water protection area
- `metadata` (object, optional): additional key/value metadata that does not fit a defined attribute. Participants MUST preserve metadata they do not understand and MUST relay it unchanged.

### Entity dependencies

A field references a farm, MAY reference a party as its owner, and MAY reference
the field boundaries that describe it; a farm references the party that owns it
and MAY reference further parties as partners; and a person MAY reference the
organizations it belongs to. A field boundary references nothing: the reference
runs from the field to its boundaries, not the other way. These dependencies
are significant for routing and initial load: a participant that is to receive fields
MUST also be enabled for the farms, parties, and field boundaries those fields
depend on, so that references can be resolved on the receiving side (see
[Routing and opt-in](#routing-and-opt-in)).

## Harvest period

Many systems attach fields (and other entities) to a *harvest year*: when a new
harvest year begins, the entity transitions into it while the previous year's data
is retained, and copies of attributes such as boundaries may be created for the new
year. Two conflicting conventions exist in the field: a discrete `year: NNNN`, and
a `valid-from` / `valid-to` interval. Complicating matters, a "harvest year" is not
always a calendar year — depending on region, crop, and climate it may span a few
months or several calendar years.

To keep the canonical form unambiguous, `harvestPeriod` is defined as an interval:

- `validFrom` (string, required): the start date ({{?RFC3339}} full-date).
- `validTo` (string, optional): the end date; absent means "open / current".
- `label` (string, optional): a human-facing designation such as `"2026"` or `"2025/2026"` for systems that present a discrete year.

A participant that natively uses a discrete year MUST map it to an interval on send
and MAY use `label` to round-trip its own presentation.

# Encoding and canonicity

## Encoding

The normative wire encoding for master-data payloads is a constrained subset of
the EFDI / ISOXML (ISO 11783-10) representation of the corresponding entities
(partfield, farm, customer). The exact subset and its Protobuf/EFDI form are being
finalized together with the FarmSPT alignment, the JSON shown
in this document is an illustrative projection of that model and is not itself the
binding format.

## Hard validation

Regardless of the finalized encoding, the following rules are normative:

- The format MUST be **canonical**: every logical value has exactly one valid encoding. Producers MUST emit the canonical form; there is no "tolerant" reading of equivalent-but-different encodings.
- agrirouter MUST validate every incoming master-data payload against the defined subset. If validation fails, the request MUST be **rejected with an error** and MUST NOT be applied to the SSOT or forwarded. Payloads are not silently repaired.
- Validation and rejection apply only to the operations defined here; they do not change the handling of other, pre-existing agrirouter traffic.

## Extensible enumerations

Agricultural vocabularies grow: a new boundary creation method or a new regulatory
zone appears long after an implementation has shipped. A JSON Schema `enum` is by
definition a closed set, so every such addition would be an incompatible change
requiring alignment with every deployed participant.

This protocol therefore adopts the Zalando RESTful API Guidelines convention for
open-ended value lists ([rule 112](https://opensource.zalando.com/restful-api-guidelines/#112)).
Where a value set is not fully under this protocol's control, or cannot be
considered complete for any imaginable future feature, the attribute is typed as a
plain string, the currently known values are given as `examples`, and its
description in [openapi.yaml](./openapi.yaml) is prefixed with `[Extensible enum](https://github.com/DKE-Data/masterdata-sync-working-group/blob/main/specification.md#extensible-enumerations)`.
`enum` is reserved for sets this protocol itself fixes and considers complete.

The values listed for an extensible enum are those known at the time of writing,
not an exhaustive set. Normatively:

- A value outside the listed set MUST NOT be a validation failure (see [Hard validation](#hard-validation)) and MUST NOT cause the entity to be rejected, dropped, or altered.
- Receivers MUST tolerate unknown values: relay them unchanged, and where the value drives behaviour, fall back to the handling they apply to an unknown value.
- Values are `UPPER_SNAKE_CASE`.
- Adding a value is a compatible change and MAY happen in a minor revision of this document. Removing or renaming a value is breaking and MUST NOT.

## Identifier mapping

agrirouter maintains, per canonical object, a mapping between its `agrirouterId`
and each endpoint's `localId` for that object. The mapping is keyed by the
**endpoint**: a local store is scoped to the endpoint, so a `localId` names a
record only in the namespace of the endpoint that sent it (see
[Terminology](#terminology)).

Keying it by the participant instead would be wrong in two ways, and both are
ordinary rather than exotic. A product that holds several organizations, each
onboarded as its own endpoint, commonly holds the same record in more than one of
them: keyed by the participant, the second organization's send resolves to the
first's canonical object, and two local records that the user maintains
separately are collapsed into one they cannot pull apart. And where those
organizations belong to different tenants, that same resolution would reach
across the tenant boundary and update another tenant's canonical object.

- On receiving an entity sent under `localId` X by endpoint E:

  - if the mapping already resolves (E, X) to a canonical object, that object is updated;
  - otherwise a new canonical object is created, `agrirouterId` is assigned, and (E, X) is recorded in its mapping.
- When agrirouter delivers a canonical object to endpoint E, it MUST set `localId` to E's own identifier for the object when the mapping holds one, so the receiver can reconcile against its local data without a lookup, and MUST omit `localId` when it holds none. An absent `localId` is meaningful: it states that agrirouter does not believe E holds this object, which is what makes an unbound or unbound-again object recognisable as one E must create locally (see [Disconnection and re-connection](#disconnection-and-re-connection)).
- **The mapping is delivered one endpoint at a time, and only to that endpoint.** A canonical object holds every endpoint's `localId`, but a delivered copy carries at most the recipient's own — named by `recipientEndpointId` (see [Common envelope](#common-envelope)). This holds between endpoints of one participant as much as between participants: an endpoint learns what *it* calls an object, never what a sibling calls it. Nothing in synchronization consumes another endpoint's identifiers — a receiver resolves through `agrirouterId` — and they are a participant's internal keys for a user's data. See [Security considerations](#security-considerations).
- The mapping MUST remain compatible with the ISOXML **LinkList** concept (ISO 11783-10, Annex E), so that identifier correspondence can be expressed to task-data-based tooling.

A mapping also comes into existence the other way round, when an endpoint
recognises a delivered canonical object as one it already holds. The endpoint
MUST declare that by **binding** its own identifier to the object; agrirouter
never infers a mapping from the content of an object. Binding is not a change to
the entity: it creates no revision, does not alter `sourceEndpointId`, and is
delivered to nobody. Until it has bound, an endpoint MUST NOT send that object,
because the send does not resolve and creates a second canonical object for the
same entity. During
[initial load](#initial-load) the bindings for a whole set are
carried on the confirmation that ends reconciliation. The concrete operations are
described in `openapi.yaml`.

Each endpoint binds for itself, including where a participant keeps one physical
record behind several of them. Such a participant holds one binding per
(endpoint, object) pair, and the `localId` it binds may be the same string for
each. Binding is what makes the second endpoint's copy sendable at all: without
it that endpoint's send does not resolve and mints a duplicate canonical object,
however well the first endpoint is bound. A delivery that carries no `localId` is
the prompt, and it asks the endpoint to bind — an endpoint that recognises the
object in a store it already shares binds and creates nothing.

Symmetrically, an endpoint that no longer holds an object — its user deleted it
locally, or it discarded its data while it was not a participant — MAY **unbind**
its identifier from the canonical object. Binding and unbinding are the same kind
of claim: a declaration about the endpoint's own store, which agrirouter records
and never infers. Unbinding is not the correction of a mistaken binding, and it
is not a [deactivation](#deactivation), which states that the entity is inactive
in the world and is delivered to every participant. It removes no canonical
object, affects no other endpoint's mapping, and reaches nobody.

Unbinding does not narrow what the endpoint receives: opt-in is the only such
filter (see [Security considerations](#security-considerations)). The object's
next change is therefore delivered again, carrying no `localId` for that
endpoint, and the endpoint MUST treat it as a canonical object it does not hold
— creating it locally and binding the identifier it then issues. An endpoint that
wants the object back sooner requests it (see
[Requesting objects (lazy loading)](#requesting-objects-lazy-loading)) rather
than waiting for a change.

Without unbinding these situations have no exit: the endpoint recreates the
object under a new local identifier, because most systems cannot choose their own
primary keys, and binding that identifier collides with the stale pair.

Every operation above is keyed by an identifier the endpoint is assumed to hold:
unbinding names a pair, [requesting](#requesting-objects-lazy-loading) names an
`agrirouterId`, and an ordinary send resolves through a `localId` already mapped.
An endpoint whose own store was restored from a backup or migrated may hold none
of them, and agrirouter does not disclose the mapping on demand — a participant's
correspondence table is its own to keep. A participant is expected to hold that
table durably and to restore it with the rest of its store.

A mapping otherwise outlives the connection that created it: it belongs to the
endpoint, and is not discarded when an entity type is opted out or when the
endpoint is disconnected from the hub. It does not outlive the endpoint itself —
removing the endpoint discards its mapping, there being no later request that
could resolve through it (see
[Disconnection and re-connection](#disconnection-and-re-connection)).

An endpoint MUST NOT reuse one of its local identifiers for two distinct
canonical objects. If an endpoint sends a `localId`
that is already mapped to a *different* canonical object than the one implied by
the request, agrirouter MUST reject it (see
[Asymmetric and non-unique mappings](#asymmetric-and-non-unique-mappings)). The
rule stops at the endpoint: two endpoints of one participant using the same
`localId` for unrelated entities is not a collision and MUST NOT be rejected.

# Synchronization processes

## Routing and opt-in

The agrirouter default-routing model — automatically routing from "left-side"
(machine) endpoints to all "right-side" (software) endpoints — is **not**
appropriate for master data, for two reasons: master-data exchange frequently
happens between two software endpoints, and inadvertently attaching an endpoint to
a master-data network can have a large blast radius.

Therefore:

- Master-data routes MUST NOT be created by the machine→software default-route logic. A participant takes part in master-data exchange only through explicit **opt-in**.
- Opt-in is expressed **per endpoint and per entity type**. An endpoint may, for example, be enabled to exchange fields but not customers.
- Opt-in does **not** carry a direction in the MVP: an opted-in entity type is read/write. Directional ("read only") opt-in is a possible later addition.
- Because of entity dependencies (see [Entity dependencies](#entity-dependencies)), an opt-in configuration MUST be **dependency-closed**: enabling fields requires the farms and field boundaries those fields reference, and the parties those farms reference, to be enabled as well. agrirouter MUST NOT record a configuration that is not dependency-closed, and MUST surface the dependency where the user makes the choice rather than silently enabling the missing types.
- Opting an entity type **in** on an endpoint that already takes part restarts that endpoint's [initial load](#initial-load): agrirouter cannot enumerate what the endpoint missed while the type was not opted in, and initial load is per endpoint rather than per entity type, so the whole set — every opted-in type — is sent again. Neither the canonical objects nor the endpoint's identifier mapping are discarded by an opt-out, so what was loaded before arrives matched rather than reconciled (see [Disconnection and re-connection](#disconnection-and-re-connection)).
- Opting an entity type **out** removes it from the configuration and stops its delivery. The endpoint's initial-load state is left as it is, and is discarded only with the last entity type.
- A participant learns that an endpoint was routed to the hub from the notification that already reports a change to the endpoints and routes visible to it (`ENDPOINTS_LIST_CHANGED` in G4), and reads the configuration then. There is no separate opt-in notification.

The configuration is therefore read-only in its entirety, and `openapi.yaml` exposes it as a single `GET`.

## Initial load

A newly connected system usually **already holds its own master data**. Initial
load reconciles that existing data with the SSOT. Each endpoint has one initial-load
state, held by agrirouter on the initial-load resource and read there by the
endpoint. It covers every entity type the endpoint is opted into; there is no
state, and no stream, per entity type. The defined progression is:

1. **`LOADING_FROM_AGRIROUTER`.** Entered when the user opts the endpoint into master data, or into a further entity type (see [Routing and opt-in](#routing-and-opt-in)), and by no other means. Every one of those is a user's instruction, carried out by agrirouter. A participant MUST NOT set this state, and an attempt to do so is rejected as an out-of-order transition. The endpoint collects the set by connecting to its initial-load stream, `GET /endpoints/{externalEndpointId}/masterdata-initial-load/events`, over which agrirouter sends every canonical object of every opted-in entity type it is entitled to receive. The set may include objects that are [deactivated](#deactivation): see [Deactivated objects are part of the set](#deactivated-objects-are-part-of-the-set).

   Order is agrirouter's, not the endpoint's. agrirouter MUST deliver the set so that a referenced object precedes the objects that reference it, as it does for catch-up on the live stream (see [Downtime and resume](#downtime-and-resume)); opt-in is dependency-closed (see [Routing and opt-in](#routing-and-opt-in)), so the target of every reference is in the set, and an endpoint can apply each object as it arrives. That references resolve is the only property of the order an endpoint may rely on. The order itself is unspecified beyond that and may change in a later version of this document, so an endpoint MUST NOT depend on the position of one entity type relative to another, and SHOULD NOT read completeness of an entity type out of the order it receives objects in. An object referenced from the live stream that the set has not delivered yet is [requested](#requesting-objects-lazy-loading).
2. **`RECONCILING`.** agrirouter closes the stream's HTTP response once it has sent the whole set, and advances the state. The endpoint now reconciles the set against its own data, which includes resolving conflicts with its user and this might take some time.
3. **`LOADING_TO_AGRIROUTER`.** Set by the endpoint to confirm it has finished reconciliation and is sending the bindings it has produced (see [Identifier mapping](#identifier-mapping)). It then sends agrirouter any objects not yet in the SSOT and objects it changed while resolving conflicts. This state cannot be reached without firstly being in `RECONCILING`.
4. **`COMPLETED`.** Set by the endpoint once it has sent everything. From this point on, initial load is done and further changes are communicated on long-lived `/masterdata/events` stream.

The set is fixed when the load starts. An entity type opted in while the
endpoint is in any of these states restarts the load: the endpoint re-enters
`LOADING_FROM_AGRIROUTER` and the set is sent again, now including the new
type. What was loaded before arrives matched, the identifier mapping being
unaffected (see [Re-connection](#re-connection)), so the cost of the repeat is
bandwidth rather than reconciliation.

An initial-load stream carries **no delivery position**, because it delivers a
fixed set rather than a sequence of changes. agrirouter MUST NOT accept `Last-Event-ID`
on it, and a connection that drops before the set is complete is recovered by
connecting again and taking the set from the beginning.

An endpoint MUST NOT treat the response ending as proof that the set arrived: a
dropped connection ends it the same way an orderly completion does. What the set
having been sent is recorded in is the endpoint's state — agrirouter advances
it to `RECONCILING` only after sending everything — so an endpoint that finds it
still at `LOADING_FROM_AGRIROUTER` connects again and takes the set once more.

Entering `LOADING_FROM_AGRIROUTER` and advancing to `RECONCILING` are
agrirouter's; the two remaining transitions are the endpoint's. The split follows
what each side can observe and who is asking: agrirouter starts the load, on a
user's instruction and never on a participant's, and declares the set sent, while
the endpoint declares reconciliation done and the push finished. The states
advance in that order, and agrirouter MUST reject any transition out of it.
Setting the state the endpoint is already in is not out of order: agrirouter MUST accept it and
answer with the current status, and where the request carries bindings it MUST
apply them again and report afresh which were rejected. An endpoint whose
confirmation went unanswered has no other way to learn whether its bindings were
recorded, the mapping not being readable (see
[Identifier mapping](#identifier-mapping)), so it repeats the confirmation. An endpoint has an initial-load
state only while it is opted into at least one entity type; there is no state for one that never was, the
absence of any toggle already saying that it does not participate.



A participant that has lost its store outright, keeping no position and no
correspondence, is not covered by this and is not expected to be: such a loss is
a property of the participant's whole store rather than of one endpoint, so it
would take every tenant and every endpoint with it, and re-onboarding is then the
proportionate answer. What that answer costs is a reconciliation: the mapping is
keyed by the endpoint and does not survive its removal (see
[Identifier mapping](#identifier-mapping)), so a re-onboarded endpoint is a new
one, and the canonical set reaches it carrying no `localId`s. An endpoint that
was kept — the store lost, the endpoint not — is the better-served case, and is
sent its set already matched.

Conflict detection during initial load, and its resolution, are the responsibility of
the **endpoint's own software**, which presents conflicts to the user. agrirouter
provides the canonical set to reconcile against; it does not adjudicate field-level
conflicts.

### Reporting that a user is needed

Resolution happens on a screen agrirouter cannot see, while the user who connected
the endpoint may well be looking at agrirouter. So an endpoint SHOULD report
that its reconciliation is waiting on a person — `awaitingUser`
on the initial-load resource — which agrirouter shows in place of its own "this
application is working through your data". agrirouter learns *that* a person is
needed and never what for: it is one bit per endpoint, not a conflict list.

- **The endpoint raises it and agrirouter clears it**, on the two transitions the endpoint drives — the confirmation and the completion. The step to `RECONCILING` MUST NOT clear it: that is agrirouter reporting it has finished sending, which asserts nothing about whether the user has finished deciding.
- **Every state before `COMPLETED` can carry it**, including while the set is still arriving, because conflicts surface object by object rather than only once the set is complete. Sending is no different: a rejected [non-unique mapping](#asymmetric-and-non-unique-mappings) or a [missing required attribute](#differing-requiredoptional-attributes) is a decision in the endpoint's software just the same.
- **Unset says nothing about the user.** It is ambiguous between having nothing to raise and not reporting at all, so it only ever upgrades what agrirouter shows, and an endpoint that omits it costs precision rather than correctness. Nothing in the protocol branches on it.

A participant SHOULD also supply a **master-data resolution URI** for each of its
endpoints: where in its own software a user resolves *that endpoint's* initial
load. agrirouter treats it as opaque and links to it while a user is awaited;
where none is supplied it can only name the application.

It is a property of the endpoint, set on the endpoint itself through the
agrirouter endpoint API, and is not part of this document's API — which has no
write operation on the master-data configuration at all.

Reconciliation does not pause delivery. The application's live changes stream
belongs to the application and carries every endpoint it serves (see
[Downtime and resume](#downtime-and-resume)), so one user's deliberation MUST NOT
stall it. The consequence for the endpoint is that it reconciles against a set
that keeps changing under it, and the longer a conflict sits the likelier the
object it concerns has moved on.

### The set is complete, including the participant's own writes

The canonical set an endpoint receives includes objects whose current revision
the requesting participant itself produced. [Origin suppression](#loop-prevention)
does not apply to initial load: it exists to keep an endpoint from being handed
a revision it already holds, and an endpoint taking the set has declared that
it does not know what it holds. Withholding those objects would leave a returning
participant reporting them in **loading to agrirouter** as absent from the SSOT,
and agrirouter would mint a second canonical object for each — the failure the
next rule exists to prevent, arrived at by another route.

### Deactivated objects are part of the set

The canonical set an endpoint receives includes objects that are inactive, each
carrying `active: false`. They are current state — the present truth about the
entity is that it was deactivated — not the history this protocol
[does not keep](#what-this-protocol-is-and-is-not).

Omitting them would corrupt data rather than merely withhold it. A seeding
endpoint usually holds its own copy of the entity; receiving no canonical object
for it, the endpoint reports it in **loading to agrirouter** as one not yet in
the SSOT, and agrirouter creates a *second*, active canonical object for an
entity that already exists. Every other participant then receives the
resurrection of something their user archived. The risk is highest exactly where
seeding matters most: an endpoint whose identifier mapping is gone, which no
longer recognises the entity by any identifier it holds.

An endpoint that receives an inactive object MUST NOT treat it as an ordinary
delivery of an object it does not hold:

- if it recognises the object in its own store, it [binds](#identifier-mapping) its identifier and marks its own copy inactive. Binding a dead object is worth doing: an unbound local copy is precisely what gets sent back as new later.
- if it does not recognise it, it ignores it and creates nothing. The rule that an absent `localId` means "create it locally and bind" (see [Identifier mapping](#identifier-mapping)) is about objects the endpoint is expected to hold, and does not extend to one that is inactive.

### Asymmetric and non-unique mappings

Systems do not always agree on entity granularity — for example two fields in one
system may correspond to a single field in another. Such n:1 correspondences are
not fully solvable by agrirouter, because the ambiguity exists even without
agrirouter in the loop.

The protocol therefore does not attempt to merge such objects automatically.
Instead, an endpoint MUST NOT send back one of its own local identifiers already
associated with a *different* object; agrirouter rejects the offending request (see
[Identifier mapping](#identifier-mapping)). This pushes resolution of a genuine n:1 situation to the
participating systems, which is the intended behaviour for this version.

Because resolution happens there, a rejection MUST carry what resolving it
requires. agrirouter MUST report, for each rejected binding:

- **which identifier is taken** — the endpoint's `localId`, already denoting a different canonical object, or the canonical object, already known to that endpoint under a different `localId`. The two are different problems in the endpoint's store and are not interchangeable;
- **the mapping that holds it** — the pair that stands. Both of its ends belong to the rejected endpoint itself, so naming it discloses nothing the endpoint does not already hold, and it is what lets the endpoint tell its user *which* of their records is in the way rather than only that something is.

Both MUST be machine-readable: the endpoint's handling differs by cause — some
rejections belong in front of a user, others must never reach one.

The same applies whether the binding was rejected singly or as one pair of an
[initial-load](#initial-load) confirmation; agrirouter MUST report a
bulk rejection per pair, since the endpoint has to resolve each on its own terms.

### Differing required/optional attributes

Systems disagree on which attributes are mandatory (one system may require a
farm on every field where another treats it as optional). The **stricter
recipient** is responsible for handling data that does not meet its own
requirements — for example by asking the user to assign a fallback value.
agrirouter neither enforces one system's requirements on another nor drops data to
satisfy them.

### Downtime and resume

This section concerns the application's live changes stream,
`GET /masterdata/events`. The initial-load streams cannot be resumed.

A connection may be lost while changes accumulate on both sides. On reconnection,
a participant resumes from the last event it last received and applied rather than
receiving everything again. That position is carried as `Last-Event-ID` header, is
distinct from the per-object `revision`, and MUST be derived from what the
participant has durably applied rather than from what its stream client last read.

A participant MAY apply events in parallel. Where it does, it applies complete out of
order, and the position it resends on resume MUST be the id of the earliest frame,
in the order the stream delivered them, that it has not yet applied. Ids are not
comparable by the participant, so this is a matter of remembering delivery order
rather than of comparing ids. Participants should take care to store the event id
for resuming correctly, otherwise they would risk potential data loss after
resuming stream.

Event ids are opaque strings. This document does not define their structure,
which may change between implementations and versions of this document, and a
participant MUST NOT interpret, compare, construct, or modify one. For
illustration only, an id may look like `v1.f8yQ_KL5hHBS.TBQjL_8GeXSs6TIxFzsdQQ`;
nothing in it is for the participant to read.

A participant MUST pass id via `Last-Event-ID` header exactly as agrirouter 
issued it in the `id:` field of an event. Event id will not always advance on
every frame: the same value may repeat across consecutive frames, as it does
throughout catch-up. Whatever id a frame carries is safe to resume from once that
frame and every frame before it have been applied; agrirouter never issues an id
that would skip an object the participant has not been sent.

A resume position does not expire: agrirouter serves catch-up from the current
state of each entity rather than from a retained log of changes, so an arbitrarily
long absence is a larger catch-up rather than a failed one. The consequence is that
a participant receives each changed entity once, carrying its current value, and
MUST NOT assume it observed every intermediate change to that entity.

Catch-up is ordered so that a referenced object precedes the objects that
reference it, whatever the order in which they were last changed, and agrirouter
marks its end with a `CAUGHT_UP` frame. That frame covers the catch-up as a whole
and names no entity type. The ordering guarantees only that references resolve as
objects arrive: a participant SHOULD NOT read completeness of an entity type out of
the order it receives objects in, and SHOULD NOT treat the arrival of one type as a
statement about another. The order itself, beyond that guarantee, is agrirouter's
and may change in a later version of this document; a participant MUST NOT depend
on it.

A participant that connects **without sending `Last-Event-ID`**, or sends one
agrirouter cannot validate, is served as a first connection on this stream:
agrirouter delivers everything it is entitled to.

Omitting `Last-Event-ID` is consequently the widest way a participant can ask for
data again on this stream: it re-delivers every object for every endpoint the
application holds, one frame per endpoint. A participant that needs less asks for one endpoint's
canonical set (see
[Initial load](#initial-load)), or refetches objects
individually (see [Requesting objects (lazy loading)](#requesting-objects-lazy-loading)).

## Disconnection and re-connection

[Downtime and resume](#downtime-and-resume) covers an endpoint that is still a
participant and merely offline. This section covers an endpoint whose
participation itself ends, and what it finds if it comes back.

Three distinct events end participation, at different scopes:

| Event | Scope |
|---|---|
| **Type opt-out** — an entity type removed from the endpoint's opt-in configuration | one entity type |
| **Hub disconnection** — the endpoint's route to the master-data hub removed | every entity type of that endpoint |
| **Endpoint removal** — the endpoint itself deleted from the tenant | the endpoint |

What each discards:

| | Canonical objects | Identifier mapping | Initial-load state |
|---|---|---|---|
| Type opt-out | retained | **retained** | retained; discarded with the last entity type |
| Hub disconnection | retained | **retained** | discarded |
| Endpoint removal | retained | **discarded** | discarded |

The initial-load state is per endpoint, so opting one of several entity types
out leaves it standing: the endpoint is still in step for what it remains opted
into. Opting the type back in restarts the load (see
[Routing and opt-in](#routing-and-opt-in)), which is what discarding the state
would have bought.

Canonical objects contributed by the endpoint are retained in every case: they
are the tenant's data, held on the tenant's behalf, and other endpoints are
synchronizing against them.

The identifier mapping survives the first two for the same reason it survives
[deactivation](#deactivation) — it is a property of the canonical object, not of
the connection, and neither event ends the endpoint it is keyed by (see
[Identifier mapping](#identifier-mapping)). Discarding it there would withhold
nothing: `agrirouterId` is stable, so a participant that kept its own
correspondence table would re-declare the same bindings on return, and only the
returning participant would be worse off.

Endpoint removal is the case where that reasoning runs out. The mapping is keyed
by the endpoint, so with the endpoint gone no request can resolve through those
pairs again and agrirouter discards them. A participant that re-onboards gets a
new endpoint with an empty mapping, takes the canonical set unmatched, and binds
what it recognises — the path an endpoint that lost its own table already takes.
Retaining the pairs would buy nothing and would keep a participant's internal
keys after the endpoint that declared them was deleted.

### Re-connection

Opting a type back in, or re-routing the endpoint to the hub, runs a full
[initial load](#initial-load) of the endpoint, covering every entity
type it is opted into: agrirouter cannot enumerate what the endpoint missed while
it was not a participant, so it re-sends the canonical set rather than a delta.

Because the endpoint is the same one, and its mapping survived, that set is
delivered with each object carrying the endpoint's own `localId` (see
[Identifier mapping](#identifier-mapping)), so matching is mechanical and a
participant that still holds its data has nothing to reconcile and nothing to
bind. Reconciliation is only as large as the divergence that accumulated while
the endpoint was away.

A participant that discarded its local copies in the meantime is in the opposite
position: the objects arrive carrying identifiers it no longer recognises. It
**unbinds** those (see [Identifier mapping](#identifier-mapping)) and takes them
as new, rather than reusing identifiers its own store has forgotten.

agrirouter marks a repeat load as such: an endpoint re-entering
`LOADING_FROM_AGRIROUTER` having previously reached `COMPLETED` carries the time
at which it did, on the initial-load resource. A participant MUST NOT infer from
the arrival of a canonical set that it is a first connection — without the marker
it would blind-create local objects for data it already holds. The marker is on
the initial-load resource rather than on the stream because it is read once, when
a set starts arriving, and the delivery channel carries entities.

On return a participant may assume only that it receives the **current** value of
each canonical object it is entitled to. Objects deactivated while it was away
arrive inactive. Changes made by others while it was away are not enumerable: as
in [Downtime and resume](#downtime-and-resume), agrirouter keeps no log, so a
participant that wants to show its user what changed in its absence must diff
against its own retained copy.

The retention above stops at the endpoint. A participant that removes an endpoint
and creates another in the same tenant is a new endpoint to agrirouter: the set
it is seeded with arrives carrying no `localId`s, and it binds what it
recognises. Re-onboarding is therefore a reconciliation and not a resumption,
which is the cost of a `localId` meaning something only inside the endpoint that
issued it.

## Loop prevention

Bidirectional synchronization risks an "infinite loop" of echoed updates: A's
change is delivered to B, B's system emits it as a change, which is delivered back
to A, and so on. This is aggravated by systems that emit change notifications even
when nothing actually changed.

The protocol relies on the SSOT to break these loops:

- agrirouter MUST NOT echo a change back to the **endpoint** it originated from. The unit of suppression is the endpoint: agrirouter records the originating `sourceEndpointId` for every revision it produces and does not deliver an object back to the endpoint named there. A change made by one endpoint is still delivered to the participant's other endpoints, each as its own frame. Where two of them are backed by one store the sibling may re-emit the object; that re-emission equals the current canonical revision and the no-op detection below drops it — but only once the sibling has **bound** its own identifier (see [Identifier mapping](#identifier-mapping)). An unbound sibling's send resolves to nothing and creates a duplicate canonical object instead, which no safeguard in agrirouter can distinguish from a genuine new entity. The delivery that carries no `localId` is where that is avoided.
- agrirouter maintains the `revision` counter per canonical object. An incoming entity that does not actually change the canonical object (it is equal to the current canonical revision) MUST NOT create a new revision and MUST NOT be forwarded. This suppresses no-op "updates" from systems that notify unconditionally.
- Participants SHOULD avoid re-emitting an object they have just received without a genuine local change. Because some systems cannot guarantee this, agrirouter's origin-suppression and no-op detection are the authoritative safeguards and do not depend on well-behaved participants.

## Concurrency control

Two participants can edit the same entity at the same time, and a participant can
edit an entity it read some time ago. Without a check, the later write silently
overwrites the earlier one. Every write to an existing canonical object is
therefore a **compare-and-swap** on `revision`: the participant states the
revision it edited from — its **base revision** — and agrirouter applies the write
only if that base is reconcilable with the current revision.

The base travels in the `x-agrirouter-base-revision` request header, alongside the
`x-agrirouter-endpoint-id` header that names the acting endpoint. It is a header
rather than a body field because it is a precondition on the request, not part of
the entity: `revision` in the body remains assigned by agrirouter alone, and a
client-supplied revision is compared and discarded, never assigned (see
[Common envelope](#common-envelope)).

On a write that resolves to an existing object, agrirouter MUST proceed as follows:

- **Payload equal to the current canonical value.** The write succeeds as a no-op, whatever the base: no new revision, nothing forwarded (see [Loop prevention](#loop-prevention)). This is what makes it safe to retry a write whose outcome was not observed.
- **Base equal to the current revision.** The write is applied and produces the next revision.
- **Base behind the current revision.** agrirouter MUST attempt a **three-way merge**: it compares the changes from the base to the current revision with the changes from the base to the sent object. Where the two do not overlap, it applies the participant's changes on top of the current revision and answers with the merged object — a success whose `revision` is *not* base + 1, and whose content the participant did not send. Where they overlap, the write is rejected with `412 Precondition Failed`.
- **Base absent.** The write is rejected with `428 Precondition Required`. Omitting the header would opt a participant out of concurrency control, and the integrations least able to surface a conflict to a user are the ones most likely to omit it.
- **Base that agrirouter never issued for the object.** Rejected with `412`.

Merging is what obliges agrirouter to keep anything at all beyond current state.
Both sides of the comparison are changes measured *from the base*, so the base has
to be reconstructable for any revision agrirouter has issued and a participant may
still be holding. agrirouter therefore retains whatever lets it reconstruct one.
How is not constrained here: prior states, per-attribute deltas, and anything else
that answers "what did this object look like at revision N" are equivalent for
this purpose, and the choice belongs to an implementation rather than to the
protocol.

**That retention is unbounded.** agrirouter keeps every base it has issued for as
long as it holds the object, and MUST NOT age one out: a participant returning
after an arbitrary absence, writing from the revision it last saw, is merged
against rather than rejected for having waited.

This is internal to conflict resolution and is not history in the sense this
document [disclaims](#what-this-protocol-is-and-is-not). It is never served: no
operation returns a past version and `revision` addresses no version but the current
one.

A create carries no base, there being no revision to compare against. A base on a
request that does not resolve to an existing object is rejected with `412`: the
participant believes it is updating an object agrirouter does not know under that
`localId` — after an [unbind](#identifier-mapping), for example — and creating one
silently would be exactly the duplicate that binding exists to prevent.

Every outcome answers with the resulting revision: a success carries the canonical
object, a rejection carries the current revision. A participant MUST take the
revision from the response rather than assume base + 1, and MUST apply a success
response as it applies a delivered object (see
[Applying what agrirouter returns](#applying-what-agrirouter-returns)), since
after a merge the response holds content it did not send. A rejected participant
obtains the current object — from its stream, or by
[requesting it](#requesting-objects-lazy-loading) — rebases its change, and
writes again. Where the change cannot be rebased mechanically, the conflict is
surfaced to the user in the participant's own software; agrirouter does not
adjudicate it.

A participant therefore keeps the last known `revision` of every object it holds.
Where its primary store has nowhere to put it — typical of an integration that
cannot propagate a foreign counter into its own data model — it keeps the revision
alongside the store rather than inside it.

## Applying what agrirouter returns

Canonical objects reach a participant on two channels: the event stream, and the
response to the participant's own write. Both carry the same thing — the
resulting canonical object — and a participant MUST apply both the same way. A
write response is not merely an acknowledgement. It is the only channel on which
the writing endpoint learns anything about the revision it just produced, since
[origin suppression](#loop-prevention) keeps that revision off its own stream,
and it carries state the participant did not send: the `agrirouterId` assigned to
a newly created object, and the resulting object where agrirouter reconciled the
write against a concurrent change rather than rejecting it (see
[Concurrency control](#concurrency-control)).

Two rules follow:

- **Apply is guarded by `revision`.** Within the stream, order suffices: a later frame supersedes an earlier one. Across the two channels it does not, because a response may be processed after a later stream frame has already been applied. A participant MUST NOT apply an object whose `revision` is lower than the one it already holds for that object.
- **An unobserved outcome means the object is unknown.** A participant whose write neither succeeded nor failed visibly — typically a connection lost after agrirouter had committed — MUST NOT assume the value it sent is the canonical one. It retries the write or [requests the object](#requesting-objects-lazy-loading); both answer with the current canonical state.

## Deactivation

`POST /masterdata/<types>/{localId}/deactivation` signals that an entity was
deactivated in its source system. "Deactivation" is intentionally generic: it covers archival, deletion, or
any state in which the source no longer considers the entity active. It is a
lifecycle transition of the canonical object (`active` becomes `false`), **not** a
hard removal from the SSOT — the canonical object and its identifier mapping are
retained so that synchronization and references remain intact.

Deactivation MUST be **idempotent**. The same entity may be deactivated more than
once (for example because several systems independently archive it, or a request
is retried). Deactivating an entity that is already inactive MUST succeed without
error and MUST NOT produce a new revision or a new outgoing notification.

The first deactivation of an entity is a write like any other and is subject to
[concurrency control](#concurrency-control): it carries the revision it was made
from, and a concurrent edit to the entity is a conflict of which only one side
succeeds. On an entity that is already inactive the base revision is ignored,
which is what the idempotency above requires.

Reactivation is possible by simply sending `PUT /masterdata/<types>/{localId}` with the entity's `active` property set to `true`.

## Split and merge

Splitting a field into several, and merging several back into one, is a common
process — more so in Europe than in North America, and dependent on crop type.
This version carries no representation of it. A split is exchanged as a
[deactivation](#deactivation) of the original and the creation of the new
entities, a merge as the reverse, and participants converge on the correct
current set from those alone.

What is deliberately not exchanged is the *lineage*: which entities preceded
which. Recording it would serve continuity of data derived from a field — task
history, yield records, crop plans — and those entity types are
[out of scope for this version](#scope). It is deferred to the version that
carries them, so that it can be settled against a concrete consumer.

Deferred is not undecided, and the shape it returns in is fixed by what this
version already settles elsewhere:

- **Operation-agnostic.** References to the entities a given entity supersedes, with nothing on the wire distinguishing a split from a merge — a split names one predecessor on each successor, a merge names several on one, and neither needs an operation of its own. A participant that does not model the distinction is unaffected by it.
- **Held by agrirouter, read-only to participants.** Lineage is a property of the canonical object, like the [identifier mapping](#identifier-mapping), not an attribute a sender restates on every write. Carried in the envelope it would be erased by the next whole-object send from a participant that does not model it, and that erasure would be a change like any other — a new revision, delivered to everyone.
- **Dereferenced on request.** A recipient receives the predecessors' identifiers, and [requests](#requesting-objects-lazy-loading) an entity it wants the content of. What comes back is that entity's *current* state — inactive, if it was deactivated by the split — because current state is the only thing agrirouter serves.

## Requesting objects (lazy loading)

`POST /masterdata/<types>/requests` lets a participant pull a single entity by
`agrirouterId` rather than wait for it to arrive. agrirouter delivers the
corresponding object on the event stream if the requester is entitled to it under
its opt-in configuration (see [Routing and opt-in](#routing-and-opt-in)).

A requested object is delivered even when the requesting endpoint was its last
writer. [Origin suppression](#loop-prevention) keeps an endpoint's own
revisions off its stream because it already holds them; a request states the
opposite, that the endpoint does not hold the object, and refetching something
it wrote itself is the very case the operation exists for. Answering that with
`202` and silence would leave the participant with no way back to its own data.

A request never widens what a participant can see. Opt-in is the only filter on
delivery, so every object a request can return is one agrirouter would deliver
anyway. What it addresses is that *delivered* is not *held*:

- a participant that lost an object locally refetches that object, rather than opting the entity type out and back in and taking a full initial load;
- during [initial load](#initial-load) an object arriving on the live stream may reference an object the initial-load stream has not delivered yet, the two streams being independent of each other.

A request is per entity type, which is why a reference to a party carries a `type`
discriminator (see [References](#references)).

A request is the right shape only when the participant knows which objects it
wants. One that has lost enough of them that naming each is impractical asks for
the endpoint's whole canonical set again instead (see
[Initial load](#initial-load)), and one that has lost its
delivery position as well takes everything (see
[Downtime and resume](#downtime-and-resume)).

# Security considerations

Transport authentication, authorization, and confidentiality are provided by the
agrirouter platform: master-data traffic travels over the same authenticated,
access-controlled channels as other agrirouter traffic, and the HTTP surface in
`openapi.yaml` is protected by OAuth 2.0 {{?RFC6749}} bearer tokens.

Beyond transport, two protocol-level concerns are relevant. First, the routing
opt-in of [Routing and opt-in](#routing-and-opt-in) is itself a security control: because attaching an endpoint to
a master-data network can expose a user's parties, farms, and field boundaries to
that endpoint, master-data routes MUST be created only through explicit,
per-endpoint, per-entity opt-in, never by default routing. Second, opt-in is the
**only** filter on what an endpoint receives: an endpoint opted into an entity
type receives every canonical object of that type in the exchange, and no
attribute inside a synchronized object narrows that (see [Farm](#farm)). The set
of entity types a user opts an endpoint into therefore defines exactly what
master data that endpoint is exposed to.

That statement is about master data, and is deliberately not a claim about the
platform. An application that holds an endpoint in a tenant can already enumerate
that tenant's other endpoints — their names, types, owning applications, and
capabilities — through ordinary agrirouter platform APIs, whether or not it takes
part in master-data exchange. This protocol neither widens nor narrows that.

What it does add, and therefore must bound, is per-object identifiers. An
endpoint's `localId` is a participant's internal primary key for one of the
user's records; disclosing it to a peer is finer-grained than anything the
platform exposes, scales with the size of the dataset rather than with the number
of endpoints, and lets one participant correlate another's records across the
tenant. It buys synchronization nothing, since receivers resolve through
`agrirouterId`. Hence the rule in
[Identifier mapping](#identifier-mapping): **a delivered object carries the
receiving endpoint's own local identifiers and no other endpoint's**, in the
envelope and in every reference within it. The bound is the endpoint rather than
the participant because the mapping is: an application reading a frame addressed
to one of its endpoints learns nothing about what its other endpoints call the
same object, which is what keeps the rule the same rule whoever is on the far
side of it.

Field boundaries and party contact details are personal and commercially
sensitive data. Participants SHOULD expose only the data necessary for
synchronization and SHOULD honour deactivation promptly.


