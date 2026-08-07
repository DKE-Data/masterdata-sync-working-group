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

This document specifies the *Agriculture Masterdata Sync Protocol* (AMSP): a set
of data formats and operations enabling connected systems
exchange and continuously synchronize agricultural **master data** across the
agrirouter platform, bidirectionally, over an n:m network of participants.

_Alternative name suggestion: "**AgmaSync**", derives from **Ag**riculture **Ma**sterdata **Sync**hronization and echoes the ancient Greek word **agma** (ἆγμα), symbolizing the bringing together of distributed information into a unified dataset._

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
be added later without breaking the mechanisms defined here.

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
  operate one or more endpoints. Capabilities and subscriptions for the
  `masterdata:*` message types are configured per endpoint.

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
- **Hard validation and canonicity.** The exchange format MUST be unambiguous: every logical value has exactly one valid encoding, and non-conforming messages are rejected rather than repaired (see [Encoding and canonicity](#encoding-and-canonicity)).
- **Facilitation, not a product.** The canonical store exists only to enable synchronization; it is not exposed or marketed as a standalone data product.

## Message types

Synchronization is carried by a family of `masterdata:*` agrirouter message
types. For each supported entity `<type>` (one of `organization`, `person`, `farm`, `field`, `fieldBoundary`):

| Message type                    | Purpose                                                                                     |
| ------------------------------- | ------------------------------------------------------------------------------------------- |
| `masterdata:<type>`             | Carries the entity itself (creation or update).                                             |
| `masterdata:<type>:request`     | Actively requests an entity from the network ("lazy loading"), by identifier or reference.  |
| `masterdata:<type>:deactivate`  | Signals that the entity was deactivated in its source system (archival, deletion, or similar). |

These message types are transported using the ordinary agrirouter mechanisms for
sending data and for receiving delivery events. A participant declares support for
a given message type<sup>1</sup> and direction<sup>2</sup> through its endpoint capabilities, and receives
objects for the types it is subscribed to. The concrete HTTP surface that a
participant uses to send, request, receive, and configure these messages is
described by the companion OpenAPI document (`openapi.yaml`).

<sup>1</sup> We are still to decide whether we just send every type to a participant and let them deal with filtering.

<sup>2</sup> For the MVP we will likley not support selecting a direction and every route will be read/write.

# Data model

This section defines the canonical object model for the MVP entities, fully aligned and reconciled with the outcomes of the FarmSPT project.
The model is deliberately close to the ISOXML / EFDI representation of the same concepts
(ISO 11783-10) so that existing task-data tooling can map to and from it, while
being expressed here in an encoding-independent way.

## Common envelope

Every `masterdata:<type>` object shares a common envelope. Example
(non-normative):

~~~ json
{
  "type": "field",
  "agrirouterId": "1f2e3d4c-5b6a-7089-90ab-cdef01234567",
  "localId": "PFD-00042",
  "idMappings": [
    { "endpointId": "urn:endpoint:jd:abc", "localId": "field-9931" },
    { "endpointId": "urn:endpoint:cci:xyz", "localId": "b1e7..." }
  ],
  "active": true,
  "revision": 7,
  "modifiedAt": "2026-07-14T09:20:00Z",
  "sourceEndpointId": "urn:endpoint:jd:abc",
  "previousVersions": []
}
~~~

Envelope fields:

- `type` (string, required): the entity type; one of `organization`, `person`, `farm`, `field`, `fieldBoundary`.
- `agrirouterId` (string): the agrirouter-assigned canonical identifier ({{?RFC4122}}). It is assigned by agrirouter on first receipt and is absent when a source system creates a not-yet-known entity. It MUST NOT be chosen or changed by a participant.
- `localId` (string, required on send): the sending participant's own identifier for the entity. See [Identifier mapping](#identifier-mapping) for how it is interpreted.
- `idMappings` (array): the identifier mapping maintained by agrirouter. Each element pairs an `endpointId` with that endpoint's `localId`. This field is populated by agrirouter on the objects it delivers and is ignored on send.
- `active` (boolean): whether the entity is currently active. Deactivation is expressed through the `:deactivate` message (see [Deactivation](#deactivation)); `active` on a delivered object reflects the current SSOT state.
- `revision` (integer): a monotonically increasing counter maintained by agrirouter for the canonical object. It is central to loop prevention and conflict detection (see [Loop prevention](#loop-prevention)).
- `modifiedAt` (string): the {{?RFC3339}} timestamp of the last accepted change.
- `sourceEndpointId` (string): the endpoint whose change produced the current canonical revision.
- `previousVersions` (array of references): references to prior entities that this entity supersedes, used for split and merge (see [Split and merge](#split-and-merge)). Empty for entities with no such lineage.

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
  not been sent yet — the message MUST be rejected, and the participant MUST send the
  target before the object referencing it.
- **On delivery**, agrirouter MUST populate `agrirouterId`, and SHOULD replace the
  `localId` with the receiving endpoint's own identifier for the target when known. A
  receiving endpoint resolves the target through `agrirouterId`: the sender's `localId`
  carries no meaning in the receiver's namespace.

A reference to a party MUST additionally carry `type` (`organization` or `person`).
A receiving endpoint that does not hold the target has to retrieve it through
[`masterdata:<type>:request`](#requesting-objects-lazy-loading), which is per entity
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
- `soil`(object, optional): `type` (Enum value like: `SAND`, `LOAMY_SAND`, `HEAVY_LOAMY_SAND`, `SANDY_TO_SILTY_LOAM`, `CLAYEY_LOAM`, `CLAY`), `rating points`
- `topography`(number, optional): slope, gradient like 7°
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

The normative wire encoding for `masterdata:*` payloads is a constrained subset of
the EFDI / ISOXML (ISO 11783-10) representation of the corresponding entities
(partfield, farm, customer). The exact subset and its Protobuf/EFDI form are being
finalized together with the FarmSPT alignment (see [Open issues](#open-issues)); the JSON shown
in this document is an illustrative projection of that model and is not itself the
binding format.

## Hard validation

Regardless of the finalized encoding, the following rules are normative:

- The format MUST be **canonical**: every logical value has exactly one valid encoding. Producers MUST emit the canonical form; there is no "tolerant" reading of equivalent-but-different encodings.
- agrirouter MUST validate every incoming `masterdata:*` message against the defined subset. If validation fails, the message MUST be **rejected with an error** and MUST NOT be applied to the SSOT or forwarded. Messages are not silently repaired.
- Validation and rejection apply only to the `masterdata:*` message types; they do not change the handling of other, pre-existing agrirouter message types.

## Identifier mapping

agrirouter maintains, per canonical object, a mapping between its `agrirouterId`
and each participant's `localId` for that object.

- On receiving a `masterdata:<type>` from endpoint E carrying `localId` X:

  - if the mapping already resolves (E, X) to a canonical object, that object is updated;
  - otherwise a new canonical object is created, `agrirouterId` is assigned, and (E, X) is recorded in its mapping.
- When agrirouter delivers a canonical object to endpoint E, it SHOULD include E's own `localId` (if known) so the receiver can reconcile against its local data without a lookup.
- The mapping MUST remain compatible with the ISOXML **LinkList** concept (ISO 11783-10, Annex E), so that identifier correspondence can be expressed to task-data-based tooling.

An endpoint MUST NOT reuse one of its own local identifiers for two distinct
canonical objects. If an endpoint sends a `localId` that is already mapped to a
*different* canonical object than the one implied by the message, agrirouter MUST
reject the message (see [Asymmetric and non-unique mappings](#asymmetric-and-non-unique-mappings)).

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
- Opt-in carries a **direction** (read, write, or both) per entity type. agrirouter MUST make each endpoint's read/write configuration discoverable to the other participants so that a system can adapt its behaviour — for instance, presenting an entity as read-only when it is not permitted to write it back.
- Because of entity dependencies (see [Field](#field)), enabling an endpoint to receive fields SHOULD also enable the farms those fields reference, and the parties those farms reference. Implementations SHOULD surface these dependencies to the user rather than silently enabling additional data.
- The opt-in decision SHOULD be offered to the user at endpoint onboarding, and MUST remain changeable afterwards.

The concrete configuration resource is described in `openapi.yaml`.

## Initial load and seeding

A newly connected system usually **already holds its own master data**. Seeding
reconciles that existing data with the SSOT. Each endpoint has, per entity type, a
seeding state. The defined progression is:

1. **Connected.** The endpoint is created and the user opts it into master-data exchange for one or more entity types (see [Routing and opt-in](#routing-and-opt-in)).
2. **Seeding from agrirouter.** agrirouter sends the endpoint every canonical object of the opted-in types that the endpoint is entitled to receive.
3. **Seeding to agrirouter.** After the endpoint has confirmed receipt of those objects, it sends agrirouter the objects it knows about, including any objects not yet in the SSOT and any objects it changed while resolving conflicts.
4. **Completed.** Steady-state (day-2) synchronization applies from here on.

Conflict detection during seeding, and its resolution, are the responsibility of
the **endpoint's own software**, which presents conflicts to the user. agrirouter
provides the canonical set to reconcile against; it does not adjudicate field-level
conflicts.

### Asymmetric and non-unique mappings

Systems do not always agree on entity granularity — for example two fields in one
system may correspond to a single field in another. Such n:1 correspondences are
not fully solvable by agrirouter, because the ambiguity exists even without
agrirouter in the loop.

The protocol therefore does not attempt to merge such objects automatically.
Instead, an endpoint MUST NOT send back one of its own local identifiers already
associated with a *different* object; agrirouter rejects the offending message (see
[Identifier mapping](#identifier-mapping)). This pushes resolution of a genuine n:1 situation to the
participating systems, which is the intended behaviour for this version.

### Differing required/optional attributes

Systems disagree on which attributes are mandatory (one system may require a
farm on every field where another treats it as optional). The **stricter
recipient** is responsible for handling data that does not meet its own
requirements — for example by asking the user to assign a fallback value.
agrirouter neither enforces one system's requirements on another nor drops data to
satisfy them.

### Downtime and resume

A connection may be lost while changes accumulate on both sides. On reconnection,
an endpoint resumes from its last known state rather than re-running a full seeding
from scratch; the `revision` counter (see [Loop prevention](#loop-prevention)) lets both sides determine what
has changed. The precise catch-up/resume semantics are tracked as an open item
(see [Open issues](#open-issues)).

## Loop prevention

Bidirectional synchronization risks an "infinite loop" of echoed updates: A's
change is delivered to B, B's system emits it as a change, which is delivered back
to A, and so on. This is aggravated by systems that emit change notifications even
when nothing actually changed.

The protocol relies on the SSOT to break these loops:

- agrirouter MUST NOT echo a change back to the endpoint it originated from. The originating endpoint is identified by `sourceEndpointId` for the produced revision.
- agrirouter maintains the `revision` counter per canonical object. An incoming `masterdata:<type>` that does not actually change the canonical object (it is equal to the current canonical revision) MUST NOT create a new revision and MUST NOT be forwarded. This suppresses no-op "updates" from systems that notify unconditionally.
- Participants SHOULD avoid re-emitting an object they have just received without a genuine local change. Because some systems cannot guarantee this, agrirouter's origin-suppression and no-op detection are the authoritative safeguards and do not depend on well-behaved participants.

## Deactivation

`masterdata:<type>:deactivate` signals that an entity was deactivated in its source
system. "Deactivation" is intentionally generic: it covers archival, deletion, or
any state in which the source no longer considers the entity active. It is a
lifecycle transition of the canonical object (`active` becomes `false`), **not** a
hard removal from the SSOT — the canonical object and its identifier mapping are
retained so that synchronization, references, and lineage remain intact.

Deactivation MUST be **idempotent**. The same entity may be the subject of more
than one `:deactivate` message (for example because several systems independently
archive it, or a message is retried). Receiving a `:deactivate` for an entity that
is already inactive MUST succeed without error and MUST NOT produce a new revision
or a new outgoing notification.

## Split and merge

Splitting a field into several, and merging several back into one, is a common
process — more so in Europe than in North America, and dependent on crop type. The
result is not merely a set of new entities: the new entities remain related to the
old ones. Some systems do not model this as an explicit operation at all.

Rather than distinguishing "split" from "merge" as separate operations, the
protocol represents both uniformly through lineage:

- A newly created entity that supersedes one or more prior entities carries references to them in `previousVersions` (see [Common envelope](#common-envelope)). A split produces several new entities that each reference the original; a merge produces one new entity that references several originals. No operation-specific message is required.
- A system that receives such an entity receives the *identifiers* of the referenced predecessors, and MAY retrieve their full data on demand using `masterdata:<type>:request` (see [Requesting objects](#requesting-objects-lazy-loading)).
- Systems that do not retain historical data may ignore `previousVersions`; the information is advisory lineage, not a required processing step.

## Requesting objects (lazy loading)

`masterdata:<type>:request` lets a participant actively pull an entity it does not
currently hold — for example a predecessor referenced through `previousVersions`,
or a dependency (a field's farm) it has not yet been seeded with. A request
identifies the wanted entity by `agrirouterId`, and agrirouter responds by
delivering the corresponding `masterdata:<type>` object if the requester is
entitled to it under its opt-in configuration (see [Routing and opt-in](#routing-and-opt-in)).

# Security considerations

Transport authentication, authorization, and confidentiality are provided by the
agrirouter platform: `masterdata:*` messages travel over the same authenticated,
access-controlled channels as other agrirouter traffic, and the HTTP surface in
`openapi.yaml` is protected by OAuth 2.0 {{?RFC6749}} bearer tokens.

Beyond transport, two protocol-level concerns are relevant. First, the routing
opt-in of [Routing and opt-in](#routing-and-opt-in) is itself a security control: because attaching an endpoint to
a master-data network can expose a user's parties, farms, and field boundaries to
that endpoint, master-data routes MUST be created only through explicit,
per-endpoint, per-entity opt-in, never by default routing. Second, agrirouter's
read/write configuration per endpoint governs which participants may modify shared
entities; endpoints MUST respect the read/write status communicated for an entity
type and MUST NOT rely on other participants to enforce it on their behalf.

Field boundaries and party contact details are personal and commercially
sensitive data. Participants SHOULD expose only the data necessary for
synchronization and SHOULD honour deactivation promptly.

# Open issues

The following items are still under discussion and affect provisional parts of
this document:

- **Unambiguous EFDI subset & hard validation** — the exact validated subset of [Encoding and canonicity](#encoding-and-canonicity).
- **Routing model** — the concrete default-route policy, opt-in switches, and read/write propagation of [Routing and opt-in](#routing-and-opt-in).
- **Initial load** — formalizing the initial-load state machine, conflict-resolution responsibilities, and downtime/resume semantics of [Initial load and seeding](#initial-load-and-seeding).
- **Loop prevention** — final handling of unconditional-notification systems in [Loop prevention](#loop-prevention).
- **Split / merge** — the lineage model of [Split and merge](#split-and-merge).
- **Harvest period** — interval-versus-year resolution in [Harvest period](#harvest-period).
- **`:deactivate` idempotency** — duplicate deactivations in [Deactivation](#deactivation).
