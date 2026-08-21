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

Participant:
a software product or machine platform connected to agrirouter that takes part
  in master-data synchronization through one or more endpoints.

Endpoint:
an agrirouter endpoint as defined by the agrirouter platform. A participant may
  operate one or more endpoints. Master-data opt-in is configured per endpoint
  (see [Routing and opt-in](#routing-and-opt-in)).

Entity:
a single master-data object of one of the supported types (an organization, a person, a farm,
  a field, or a field boundary).

Canonical object:
the version of an entity held by agrirouter in the Single Source of Truth store
  (see [the SSOT store](#agrirouter-as-the-single-source-of-truth)). It carries the agrirouter-assigned identifier and the
  cross-participant identifier mapping.

Local identifier:
the identifier a participant uses for an entity within its own system — the
  identifier by which that participant knows the entity in its own store. Local
  identifiers are only unique within the issuing endpoint.

agrirouter identifier:
the stable, globally unique identifier that agrirouter assigns to the canonical
  object. Formatted as a UUID {{?RFC4122}}.

Source system:
for a given change, the participant in which the change originated.

# Architecture overview

## agrirouter as the Single Source of Truth

agrirouter holds a **Single Source of Truth (SSOT) entity store**. For every
synchronized entity it stores:

1. the *canonical version* of the entity, and
2. an *identifier mapping* that records, for each participant that knows the entity, that participant's local identifier for it.

The SSOT store is what makes robust n:m synchronization tractable: it provides a
single place against which updates are reconciled, it powers loop prevention (see
[Loop prevention](#loop-prevention)), and it lets a newly connected or returning system be seeded from a
known-good set (see [Initial load and seeding](#initial-load-and-seeding)).

The choice of a canonical central store — rather than a purely meshed exchange —
is a settled design decision for this protocol. The previously considered
standalone identifier-mapping mechanism is **not** a separate component; identifier
mapping is an intrinsic property of the SSOT store.

## Design principles

The protocol is designed around four principles. Conforming behaviour is derived
from them, and they SHOULD be used to resolve questions this document leaves open.

- **n:m exchange.** Any reasonable number of connected systems can exchange the same master data with each other.
- **agrirouter is the SSOT.** A canonical version of every entity plus a mapping to each participant's own identifier reduces drift and prevents update loops.
- **Hard validation and canonicity.** The exchange format MUST be unambiguous: every logical value has exactly one valid encoding, and non-conforming payloads are rejected rather than repaired (see [Encoding and canonicity](#encoding-and-canonicity)).
- **Facilitation, not a product.** The canonical store exists only to enable synchronization; it is not exposed or marketed as a standalone data product.

## Operations

Synchronization is carried by the HTTP operations of the companion OpenAPI
document (`openapi.yaml`). Each entity type has its own paths; `<types>` below is
the collection of one supported entity type (`organizations`, `persons`, `farms`,
`fields`, `field-boundaries`):

| Operation                                          | Purpose                                                                                        |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `PUT /masterdata/<types>/{localId}`                | Sends the entity itself (creation or update).                                                  |
| `POST /masterdata/<types>/requests`                | Actively requests an entity ("lazy loading"), see [Requesting objects](#requesting-objects-lazy-loading). |
| `POST /masterdata/<types>/{localId}/deactivation`  | Signals that the entity was deactivated in its source system (archival, deletion, or similar). |
| `PUT`/`DELETE /masterdata/<types>/{localId}/id-mapping/{agrirouterId}` | Binds or unbinds the endpoint's own identifier, see [Identifier mapping](#identifier-mapping). |

What agrirouter sends *unprompted* travels on one stream,
`GET /masterdata/events`: canonical objects, deactivations, and the
`CANONICAL_SET_END` markers that close an
[initial load](#initial-load-and-seeding). The stream belongs to the application
rather than to a single endpoint, and the position within it is carried as
`Last-Event-ID` (see [Downtime and resume](#downtime-and-resume)).

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
  "sourceEndpointId": "9f8e7d6c-5b4a-3210-fedc-ba9876543210"
}
~~~

Envelope fields:

- `type` (string, required): the entity type; one of `organization`, `person`, `farm`, `field`, `fieldBoundary`.
- `agrirouterId` (string): the agrirouter-assigned canonical identifier ({{?RFC4122}}). It is assigned by agrirouter on first receipt and is absent when a source system creates a not-yet-known entity. It MUST NOT be chosen or changed by a participant.
- `localId` (string): **always the identifier of the participant at the near end of the transfer, never of any other.** On send it is the sender's own identifier for the entity, and is required. On delivery agrirouter replaces it with the *receiving* endpoint's own identifier, and omits it when there is none — see [Identifier mapping](#identifier-mapping). A delivered object therefore never names another participant's identifier for anything.
- `active` (boolean): whether the entity is currently active. Deactivation is expressed through the deactivation operation (see [Deactivation](#deactivation)); `active` on a delivered object reflects the current SSOT state.
- `revision` (integer): a monotonically increasing counter maintained by agrirouter for the canonical object. It is central to loop prevention and conflict detection (see [Loop prevention](#loop-prevention)).
- `modifiedAt` (string): the {{?RFC3339}} timestamp of the last accepted change.
- `sourceEndpointId` (string): the endpoint whose change produced the current canonical revision.

### References

A **reference** to another entity (for example a field referring to its farm) is
expressed as an object carrying the target's `agrirouterId`, the referencing
participant's `localId` for the target, or both:

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

The `localId` in a reference is the *referencing* participant's identifier for the
target, not the target's canonical one. The two identifiers are therefore not
interchangeable in both directions:

- **On send**, a participant MAY use either. A reference carrying only a `localId`
  is resolved by agrirouter against the sender's own
  [identifier mapping](#identifier-mapping). If it does not resolve — the target has
  not been sent yet — the request MUST be rejected, and the participant MUST send the
  target before the object referencing it.
- **On delivery**, agrirouter MUST populate `agrirouterId`, and MUST replace the
  `localId` with the receiving endpoint's own identifier for the target, omitting it
  when there is none. It MUST NOT be left as the sender's: a receiving endpoint
  resolves the target through `agrirouterId`, so the sender's `localId` carries no
  meaning in the receiver's namespace, and passing it through would disclose the
  sender's internal key for the target. This is the same rule the envelope's own
  `localId` follows (see [Common envelope](#common-envelope)).

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
  - 👷‍♂️ _to be refined_
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

A field references a farm and MAY reference a party as its owner, a farm
references the party that owns it and MAY reference further parties as partners,
and a person MAY reference the organizations it belongs to. These dependencies
are significant for routing and seeding: a participant that is to receive fields
MUST also be enabled for the farms and parties those fields depend on, so that
references can be resolved on the receiving side (see
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

> The interval-versus-year modelling is **not yet finally settled**; the interval
> form above is the working proposal. See [Open issues](#open-issues).

# Encoding and canonicity

## Encoding

The normative wire encoding for master-data payloads is a constrained subset of
the EFDI / ISOXML (ISO 11783-10) representation of the corresponding entities
(partfield, farm, customer). The exact subset and its Protobuf/EFDI form are being
finalized together with the FarmSPT alignment (see [Open issues](#open-issues)); the JSON shown
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
and each participant's `localId` for that object.

- On receiving an entity sent by endpoint E under `localId` X:

  - if the mapping already resolves (E, X) to a canonical object, that object is updated;
  - otherwise a new canonical object is created, `agrirouterId` is assigned, and (E, X) is recorded in its mapping.
- When agrirouter delivers a canonical object to endpoint E, it MUST set `localId` to E's own identifier for the object when the mapping holds one, so the receiver can reconcile against its local data without a lookup, and MUST omit `localId` when it holds none. An absent `localId` is meaningful: it states that agrirouter does not believe E holds this object, which is what makes an unbound or unbound-again object recognisable as one E must create locally (see [Disconnection and re-connection](#disconnection-and-re-connection)).
- **The mapping is delivered one endpoint at a time, and only to that endpoint.** A canonical object holds every participant's `localId`, but a delivered copy carries at most the recipient's own. agrirouter MUST NOT disclose one participant's local identifiers to another: nothing in synchronization consumes them — a receiver resolves through `agrirouterId` — and they are a participant's internal keys for a user's data. See [Security considerations](#security-considerations).
- The mapping MUST remain compatible with the ISOXML **LinkList** concept (ISO 11783-10, Annex E), so that identifier correspondence can be expressed to task-data-based tooling.

A mapping also comes into existence the other way round, when an endpoint
recognises a delivered canonical object as one it already holds. The endpoint
MUST declare that by **binding** its own identifier to the object; agrirouter
never infers a mapping from the content of an object. Binding is not a change to
the entity: it creates no revision, does not alter `sourceEndpointId`, and is
delivered to nobody. Until it has bound, an endpoint MUST NOT send that object,
because the send does not resolve and creates a second canonical object for the
same entity. During
[initial load](#initial-load-and-seeding) the bindings for a whole set are
carried on the confirmation that ends reconciliation. The concrete operations are
described in `openapi.yaml`.

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
correspondence table is its own to keep. Such an endpoint recovers by asking for
the canonical set again (see [Initial load and seeding](#initial-load-and-seeding)),
which delivers every object it is entitled to carrying that endpoint's own
`localId`, and so restores the correspondence and the data together.

A mapping otherwise outlives the connection that created it: it is discarded only
with the endpoint itself, not when an entity type is opted out or the endpoint is
disconnected from the hub (see
[Disconnection and re-connection](#disconnection-and-re-connection)).

An endpoint MUST NOT reuse one of its own local identifiers for two distinct
canonical objects. If an endpoint sends a `localId` that is already mapped to a
*different* canonical object than the one implied by the request, agrirouter MUST
reject it (see [Asymmetric and non-unique mappings](#asymmetric-and-non-unique-mappings)).

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
- Because of entity dependencies (see [Entity dependencies](#entity-dependencies)), an opt-in configuration MUST be **dependency-closed**: enabling fields requires the farms those fields reference, and the parties those farms reference, to be enabled as well. agrirouter MUST reject a configuration that is not dependency-closed rather than silently enabling the missing types. Implementations SHOULD surface the dependency to the user.
- Opting an entity type **out** removes it from the configuration, and with it that entity type's initial-load state. Opting it back in starts a full initial load again: agrirouter cannot enumerate what the endpoint missed while the type was opted out. Neither the canonical objects nor the endpoint's identifier mapping are discarded, so the repeat load is matched rather than reconciled (see [Disconnection and re-connection](#disconnection-and-re-connection)).
- The opt-in decision SHOULD be offered to the user at endpoint onboarding, and MUST remain changeable afterwards.

The concrete configuration resource is described in `openapi.yaml`.

## Initial load and seeding

A newly connected system usually **already holds its own master data**. Seeding
reconciles that existing data with the SSOT. Each endpoint has, per entity type, a
seeding state, held by agrirouter on the initial-load resource and read there by
the endpoint. The defined progression is:

1. **`LOADING_FROM_AGRIROUTER`.** Entered when the user opts the endpoint into the entity type (see [Routing and opt-in](#routing-and-opt-in)), or when the endpoint asks for the set again (below), and by no other means. agrirouter sends the endpoint every canonical object of that type it is entitled to receive, over the ordinary event stream, closing the type's set with a `CANONICAL_SET_END` event so the endpoint can tell where it ends. The set includes objects that are [deactivated](#deactivation): see [Deactivated objects are part of the set](#deactivated-objects-are-part-of-the-set).
2. **`RECONCILING`.** Emitting that event moves the entity type on, because agrirouter knows it has sent everything and needs nothing reported back. The endpoint now reconciles the set against its own data, which includes resolving conflicts with its user and so takes as long as that takes.
3. **`LOADING_TO_AGRIROUTER`.** Set by the endpoint to confirm it has *reconciled* the set rather than merely received it, carrying the bindings reconciliation produced (see [Identifier mapping](#identifier-mapping)). It then sends agrirouter the objects it knows about, including any objects not yet in the SSOT and any objects it changed while resolving conflicts. Confirming from `LOADING_FROM_AGRIROUTER` MUST be rejected: an endpoint cannot have reconciled a set it has not finished receiving.
4. **`COMPLETED`.** Set by the endpoint once it has sent everything. Steady-state (day-2) synchronization applies from here on.

Two of these transitions are agrirouter's and two are the endpoint's, and the
split follows what each side can observe: agrirouter starts the load and declares
the set sent, the endpoint declares reconciliation done and the push finished. The
states advance in that order, and agrirouter MUST reject any other transition —
the re-entry below being the one exception. An entity type has an initial-load
state only while it is opted in; there is no state for one that never was, the
absence of a toggle already saying that it does not participate.

An endpoint MAY re-enter `LOADING_FROM_AGRIROUTER` from any of these states, which
asks agrirouter to send the canonical set again. The state asserts that the set is
owed to the endpoint, and that becomes true a second time when the endpoint's own
store is restored from a backup, migrated, or otherwise loses the correspondence
between its records and the canonical objects — a loss agrirouter cannot observe
and MUST therefore take on the endpoint's word. agrirouter MUST then re-send the
set as it does for a first load, and MUST mark it a repeat, the identifier mapping
being unaffected (see [Re-connection](#re-connection)). Re-entry matters as much
from `RECONCILING` and `LOADING_TO_AGRIROUTER` as from `COMPLETED`:
agrirouter has already sent the set by then, so an endpoint that loses its store
during reconciliation — a window that is human-paced and unbounded — could
otherwise neither advance nor return.

A participant SHOULD use this rather than opting the entity type out and back in.
Both reach the same state, but opting out and in is two writes to the opt-in
configuration rather than one transition: it is not atomic, so a failure between
them leaves the endpoint opted out with delivery stopped, and it records a
participant's own maintenance in the configuration that expresses what a user
agreed to share (see [Routing and opt-in](#routing-and-opt-in)).

Conflict detection during seeding, and its resolution, are the responsibility of
the **endpoint's own software**, which presents conflicts to the user. agrirouter
provides the canonical set to reconcile against; it does not adjudicate field-level
conflicts.

### Reporting that a user is needed

Resolution happens on a screen agrirouter cannot see, while the user who connected
the endpoint may well be looking at agrirouter. So an endpoint SHOULD report, per
entity type, that this type's reconciliation is waiting on a person — `awaitingUser`
on the initial-load resource — which agrirouter shows in place of its own "this
application is working through your data". agrirouter learns *that* a person is
needed and never what for: it is one bit, not a conflict list.

- **The endpoint raises it and agrirouter clears it**, on the two transitions the endpoint drives — the confirmation and the completion. The step to `RECONCILING` MUST NOT clear it: that is agrirouter reporting it has finished sending, which asserts nothing about whether the user has finished deciding.
- **Every state before `COMPLETED` can carry it**, including while the set is still arriving, because conflicts surface object by object rather than only once the set is complete. Sending is no different: a rejected [non-unique mapping](#asymmetric-and-non-unique-mappings) or a [missing required attribute](#differing-requiredoptional-attributes) is a decision in the endpoint's software just the same.
- **Unset says nothing about the user.** It is ambiguous between having nothing to raise and not reporting at all, so it only ever upgrades what agrirouter shows, and an endpoint that omits it costs precision rather than correctness. Nothing in the protocol branches on it.

A participant MAY also declare, as a `resolutionUrl` in its opt-in configuration,
where in its own software the user resolves this endpoint's initial load.
agrirouter treats it as opaque, links to it while a user is awaited, and where
none is declared can only name the application. It is per endpoint rather than per
conflict: at the moment the route is created there is nothing to resolve yet, and
a location that is right only once the first conflict exists would have to be
declared then, which is exactly when nobody is there to declare it.

Reconciliation does not pause delivery. The stream belongs to the application and
carries every endpoint it serves (see [Downtime and resume](#downtime-and-resume)),
so one user's deliberation MUST NOT stall it. The consequence for the endpoint is
that it reconciles against a set that keeps changing under it, and the longer a
conflict sits the likelier the object it concerns has moved on.

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
[initial-load](#initial-load-and-seeding) confirmation; agrirouter MUST report a
bulk rejection per pair, since the endpoint has to resolve each on its own terms.

### Differing required/optional attributes

Systems disagree on which attributes are mandatory (one system may require a
farm on every field where another treats it as optional). The **stricter
recipient** is responsible for handling data that does not meet its own
requirements — for example by asking the user to assign a fallback value.
agrirouter neither enforces one system's requirements on another nor drops data to
satisfy them.

### Downtime and resume

A connection may be lost while changes accumulate on both sides. On reconnection,
a participant resumes from the delivery position it last committed rather than
re-running a full initial load. That position is carried as `Last-Event-ID`, is
distinct from the per-object `revision`, and MUST be derived from what the
participant has durably applied rather than from what its stream client last read.

A participant MAY apply events in parallel. Where it does, applies complete out of
order, and the position it commits MUST be the one carried by the last event with
no unapplied event before it - tracked in the order the events arrived, since two
positions cannot be compared. That position MUST be committed in the same
transaction as the objects it accounts for, or after them where the participant's
stores make that impossible. A position that lags what a participant holds costs a
redelivery, which idempotent apply absorbs; one that runs ahead is a gap agrirouter
neither detects nor resends.

A delivery position is **opaque**. It MUST be returned exactly as agrirouter
issued it in the `id:` field of an event, and a participant MUST NOT parse,
construct, modify or increment one, nor compare two of them for order: its
structure is agrirouter's to change, it is not a single event identifier in every
state, and it is integrity-protected so that a locally built one is rejected. A
participant that needs to know whether it holds a complete canonical set uses
`CANONICAL_SET_END` and the entity type's initial-load state (see
[Initial load and seeding](#initial-load-and-seeding)) rather than the position.

A resume position does **not** expire: agrirouter serves catch-up from the current
state of each entity rather than from a retained log of changes, so an arbitrarily
long absence is a larger catch-up rather than a failed one. The consequence is that
a participant receives each changed entity once, carrying its current value, and
MUST NOT assume it observed every intermediate change to that entity.

A participant that connects **without sending `Last-Event-ID`**, or sends one
agrirouter cannot validate, is served as a first connection: agrirouter delivers
everything it is entitled to, in dependency order, closing each entity type with
`CANONICAL_SET_END`. Losing the delivery position therefore costs a participant
its place in the stream and not its entitlement, at the price of receiving the
whole set again. Because the identifier mapping is unaffected, those objects
arrive carrying the participant's own `localId` (see
[Identifier mapping](#identifier-mapping)) and match rather than reconcile. The
initial-load state of each entity type is unaffected too: a delivery position
records how much a participant has *received*, and says nothing about what it has
reconciled, so an entity type at **completed** stays there.

Omitting `Last-Event-ID` is consequently the widest way a participant can ask for
data again: it re-delivers every entity type for every endpoint
the application holds. A participant that needs less asks one entity type of one
endpoint for its canonical set (see
[Initial load and seeding](#initial-load-and-seeding)), or refetches objects
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
| Type opt-out | retained | **retained** | discarded |
| Hub disconnection | retained | **retained** | discarded |
| Endpoint removal | retained | discarded | discarded |

Canonical objects contributed by the endpoint are retained in every case: they
are the tenant's data, held on the tenant's behalf, and other endpoints are
synchronizing against them.

The identifier mapping is retained for the same reason it is retained across
[deactivation](#deactivation) — it is a property of the canonical object, not of
the connection. Discarding it would not withhold anything: `agrirouterId` is
stable, so a participant that kept its own correspondence table simply re-declares
the same bindings on return. It would only degrade the returning participant, whose
own data comes back unrecognizable.

### Re-connection

Opting a type back in, or re-routing the endpoint to the hub, runs a full
[initial load](#initial-load-and-seeding): agrirouter cannot enumerate what the
endpoint missed while it was not a participant, so it re-sends the canonical set
rather than a delta.

Because the mapping survived, that set is delivered with each object carrying the
endpoint's own `localId` (see [Identifier mapping](#identifier-mapping)), so
matching is mechanical and a participant that still holds its data has nothing to
reconcile and nothing to bind. Reconciliation is only as large as the divergence
that accumulated while the endpoint was away.

A participant that discarded its local copies in the meantime is in the opposite
position: the objects arrive carrying identifiers it no longer recognises. It
**unbinds** those (see [Identifier mapping](#identifier-mapping)) and takes them
as new, rather than reusing identifiers its own store has forgotten.

agrirouter marks a repeat load as such: an entity type re-entering
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

Endpoint identity is what the mapping hangs from, so the retention above holds
only for as long as the endpoint does. Whether re-onboarding a participant
reaches the same agrirouter endpoint is a platform behaviour outside this
specification; where it does not, the returning participant is a new endpoint,
its predecessor's mapping is unreachable, and the re-connection is a first
connection in every respect described here.

## Loop prevention

Bidirectional synchronization risks an "infinite loop" of echoed updates: A's
change is delivered to B, B's system emits it as a change, which is delivered back
to A, and so on. This is aggravated by systems that emit change notifications even
when nothing actually changed.

The protocol relies on the SSOT to break these loops:

- agrirouter MUST NOT echo a change back to the endpoint it originated from. The originating endpoint is identified by `sourceEndpointId` for the produced revision.
- agrirouter maintains the `revision` counter per canonical object. An incoming entity that does not actually change the canonical object (it is equal to the current canonical revision) MUST NOT create a new revision and MUST NOT be forwarded. This suppresses no-op "updates" from systems that notify unconditionally.
- Participants SHOULD avoid re-emitting an object they have just received without a genuine local change. Because some systems cannot guarantee this, agrirouter's origin-suppression and no-op detection are the authoritative safeguards and do not depend on well-behaved participants.

## Applying what agrirouter returns

Canonical objects reach a participant on two channels: the event stream, and the
response to the participant's own write. Both carry the same thing — the
resulting canonical object — and a participant MUST apply both the same way. A
write response is not merely an acknowledgement. It is the only channel on which
the writing endpoint learns anything about the revision it just produced, since
[origin suppression](#loop-prevention) keeps that revision off its own stream,
and it carries state the participant did not send: the `agrirouterId` assigned to
a newly created object, and the resulting object where agrirouter reconciled the
write against a concurrent change rather than rejecting it.

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
- **Dereferenced on request.** A recipient receives the predecessors' identifiers, and [requests](#requesting-objects-lazy-loading) an entity it wants the content of. What comes back is that entity's *current* state — inactive, if it was deactivated by the split — because agrirouter retains no past states of anything.

What remains genuinely open is listed under [Open issues](#open-issues).

## Requesting objects (lazy loading)

`POST /masterdata/<types>/requests` lets a participant pull a single entity by
`agrirouterId` rather than wait for it to arrive. agrirouter delivers the
corresponding object on the event stream if the requester is entitled to it under
its opt-in configuration (see [Routing and opt-in](#routing-and-opt-in)).

A request never widens what a participant can see. Opt-in is the only filter on
delivery, so every object a request can return is one agrirouter would deliver
anyway. What it addresses is that *delivered* is not *held*:

- a participant that lost an object locally refetches that object, rather than opting the entity type out and back in and taking a full initial load;
- during [initial load](#initial-load-and-seeding) a live change may reference an object whose own entity type has not been swept yet.

A request is per entity type, which is why a reference to a party carries a `type`
discriminator (see [References](#references)).

A request is the right shape only when the participant knows which objects it
wants. One that has lost enough of them that naming each is impractical asks for
the whole canonical set of that entity type again instead (see
[Initial load and seeding](#initial-load-and-seeding)), and one that has lost its
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

What it does add, and therefore must bound, is per-object identifiers. A
participant's `localId` is its internal primary key for one of the user's
records; disclosing it to a peer is finer-grained than anything the platform
exposes, scales with the size of the dataset rather than with the number of
endpoints, and lets one participant correlate another's records across the
tenant. It buys synchronization nothing, since receivers resolve through
`agrirouterId`. Hence the rule in
[Identifier mapping](#identifier-mapping): **a delivered object carries the
receiving endpoint's own local identifiers and no other endpoint's**, in the
envelope and in every reference within it.

Field boundaries and party contact details are personal and commercially
sensitive data. Participants SHOULD expose only the data necessary for
synchronization and SHOULD honour deactivation promptly.

# Open issues

The following items are still under discussion and affect provisional parts of
this document:

- **Unambiguous EFDI subset & hard validation** — the exact validated subset of [Encoding and canonicity](#encoding-and-canonicity).
- **Routing model** — the concrete default-route policy and opt-in switches of [Routing and opt-in](#routing-and-opt-in).
- **Conflict resolution** — that the endpoint's own software resolves field-level conflicts with its user, and that agrirouter provides the canonical set to reconcile against and does not adjudicate, is settled in [Initial load and seeding](#initial-load-and-seeding), as are the two mechanical cases: a rejected [non-unique mapping](#asymmetric-and-non-unique-mappings) and a [stricter recipient's](#differing-requiredoptional-attributes) own requirements. What is open is implementor guidance for the rest: how a partner presents a disagreement it cannot settle by its own rules, and how it reconciles against a set that keeps changing under it, delivery not being paused while a user deliberates. Two conflict outcomes are undefined rather than unguided and are tracked separately below: **Reactivation** and **Split / merge lineage**.
- **Loop prevention** — final handling of unconditional-notification systems in [Loop prevention](#loop-prevention).
- **Concurrency control** — writes carry the client's prior `revision` as a compare-and-swap precondition, and agrirouter may resolve a stale-base write by three-way merge instead of rejecting it. Both are settled in the architecture record but are not yet written into this document; only their delivery consequence is, in [Applying what agrirouter returns](#applying-what-agrirouter-returns).
- **Endpoint identity across re-onboarding** — whether a participant re-onboarding reaches the same agrirouter endpoint, which decides whether the mapping retention of [Disconnection and re-connection](#disconnection-and-re-connection) is reachable in the most common return path. A platform question, settled outside this document.
- **Learning of disconnection** — a participant is not told that a user opted a type out or disconnected the endpoint from the hub; it observes the absence on the initial-load resource. Whether that warrants a signal on the delivery channel is open.
- **Unbinding** — [Identifier mapping](#identifier-mapping) now lets an endpoint declare it no longer holds an object, which resolves the returning participant that discarded its local data. Open: whether a bulk form is needed for the mass case, as bindings have on the initial-load confirmation.
- **Auditing the mapping** — a participant that suspects a few of its pairs are stale, rather than knowing its table is lost, has no cheap way to check: agrirouter does not disclose the mapping, so the choices are a full re-load of the entity type or waiting for each object's next change. Open: whether that case is common enough to warrant reading the correspondence back, which is the one recovery the [initial-load](#initial-load-and-seeding) re-entry does not make proportionate.
- **`sourceEndpointId` on delivery** — [Identifier mapping](#identifier-mapping) settles that a delivered object carries no other endpoint's *local* identifiers. `sourceEndpointId` still names another endpoint, which the platform's own endpoint listing would disclose anyway, and which a receiver may want for a conflict UI. Whether it is needed on the delivered object at all — loop prevention is server-side — or should be reduced, is open.
- **Split / merge lineage** — deferred rather than open, and reopened by the entity types that would consume it. The shape is settled in [Split and merge](#split-and-merge); what is not, and needs a concrete consumer to settle, is: whether a predecessor MUST be deactivated by the split that supersedes it, or may stay active; whether the writes forming one split are related to each other on the wire, or arrive as unconnected changes; which endpoint may declare lineage, whether a declaration can be corrected or withdrawn, and what holds when two endpoints declare different predecessors for the same entity.
- **Reactivation** — [Deactivation](#deactivation) defines the transition into inactive and no way back out. An endpoint that has bound a deactivated object and whose user later un-archives it sends under a `localId` that resolves to that canonical object; whether that revives the entity, or is rejected, is undefined. Reachable in ordinary use once [deactivated objects are part of a seeded set](#deactivated-objects-are-part-of-the-set).
- **Harvest period** — interval-versus-year resolution in [Harvest period](#harvest-period).
- **Deactivation idempotency** — duplicate deactivations in [Deactivation](#deactivation).
