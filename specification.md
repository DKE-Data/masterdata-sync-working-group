<!-- 
  To be reviewed: this file is entiry written by AI at this moment and MUST 
  be reviewed by the team before proceeding. This here is just a 'primer'
  to get the repository setup.
 -->

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
of data formats, message types, and behavioural rules that let connected systems
exchange and continuously synchronize agricultural **master data** across the
agrirouter platform, bidirectionally, over an n:m network of participants.

The protocol is defined on top of the agrirouter message transport. It does not
replace that transport; it adds a normative agreement — a set of "playing rules" —
that every participating system follows so that synchronization is technically
interoperable and predictable for users.

## Scope

The first version of this protocol covers three entity types, referred to
throughout as the **MVP entities**:

- **Customers**
- **Farms**
- **Fields**, including their boundaries, obstacles, and metadata

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
: A software product or machine platform connected to agrirouter that takes part
  in master-data synchronization through one or more endpoints.

Endpoint:
: An agrirouter endpoint as defined by the agrirouter platform. A participant may
  operate one or more endpoints. Capabilities and subscriptions for the
  `masterdata:*` message types are configured per endpoint.

Entity:
: A single master-data object of one of the supported types (a customer, a farm,
  or a field).

Canonical object:
: The version of an entity held by agrirouter in the Single Source of Truth store
  (see [the SSOT store](#agrirouter-as-the-single-source-of-truth)). It carries the agrirouter-assigned identifier and the
  cross-participant identifier mapping.

External identifier:
: The identifier a participant uses for an entity within its own system. External
  identifiers are only unique within the issuing endpoint.

agrirouter identifier:
: The stable, globally unique identifier that agrirouter assigns to the canonical
  object. Formatted as a UUID {{?RFC4122}}.

Source system:
: For a given change, the participant in which the change originated.

# Architecture overview

## agrirouter as the Single Source of Truth

agrirouter holds a **Single Source of Truth (SSOT) entity store**. For every
synchronized entity it stores:

1. the *canonical version* of the entity, and
2. an *identifier mapping* that records, for each participant that knows the
   entity, that participant's external identifier for it.

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

- **n:m exchange.** Any reasonable number of connected systems can exchange the
  same master data with each other.
- **agrirouter is the SSOT.** A canonical version of every entity plus a mapping
  to each participant's own identifier reduces drift and prevents update loops.
- **Hard validation and canonicity.** The exchange format MUST be unambiguous:
  every logical value has exactly one valid encoding, and non-conforming messages
  are rejected rather than repaired (see [Encoding and canonicity](#encoding-and-canonicity)).
- **Facilitation, not a product.** The canonical store exists only to enable
  synchronization; it is not exposed or marketed as a standalone data product.

## Message types

Synchronization is carried by a family of `masterdata:*` agrirouter message
types. For each supported entity `<type>` (one of `field`, `farm`, `customer`):

| Message type                    | Purpose                                                                                     |
| ------------------------------- | ------------------------------------------------------------------------------------------- |
| `masterdata:<type>`             | Carries the entity itself (creation or update).                                             |
| `masterdata:<type>:request`     | Actively requests an entity from the network ("lazy loading"), by identifier or reference.  |
| `masterdata:<type>:deactivate`  | Signals that the entity was deactivated in its source system (archival, deletion, or similar). |

These message types are transported using the ordinary agrirouter mechanisms for
sending data and for receiving delivery events. A participant declares support for
a given message type and direction through its endpoint capabilities, and receives
objects for the types it is subscribed to. The concrete HTTP surface that a
participant uses to send, request, receive, and configure these messages is
described by the companion OpenAPI document (`openapi.yaml`).

# Data model

This section defines the canonical object model for the MVP entities. The model
is deliberately close to the ISOXML / EFDI representation of the same concepts
(ISO 11783-10) so that existing task-data tooling can map to and from it, while
being expressed here in an encoding-independent way.

> The object model and its encoding are being reconciled with the outcomes of the
> FarmSPT project, on which the SSOT entity store builds. Details in this section
> that touch canonical form, definitions, and identifier handling are therefore
> **provisional** until that alignment is complete. See [Open issues](#open-issues).

## Common envelope

Every `masterdata:<type>` object shares a common envelope. Example
(non-normative):

~~~ json
{
  "type": "field",
  "agrirouterId": "1f2e3d4c-5b6a-7089-90ab-cdef01234567",
  "externalId": "PFD-00042",
  "idMappings": [
    { "endpointId": "urn:endpoint:jd:abc", "externalId": "field-9931" },
    { "endpointId": "urn:endpoint:cci:xyz", "externalId": "b1e7..." }
  ],
  "active": true,
  "revision": 7,
  "modifiedAt": "2026-07-14T09:20:00Z",
  "sourceEndpointId": "urn:endpoint:jd:abc",
  "previousVersions": []
}
~~~

Envelope fields:

- `type` (string, required): the entity type; one of `customer`, `farm`, `field`.
- `agrirouterId` (string): the agrirouter-assigned canonical identifier
  ({{?RFC4122}}). It is assigned by agrirouter on first receipt and is absent when
  a source system creates a not-yet-known entity. It MUST NOT be chosen or changed
  by a participant.
- `externalId` (string, required on send): the sending participant's own
  identifier for the entity. See [Identifier mapping](#identifier-mapping) for how it is interpreted.
- `idMappings` (array): the identifier mapping maintained by agrirouter. Each
  element pairs an `endpointId` with that endpoint's `externalId`. This field is
  populated by agrirouter on the objects it delivers and is ignored on send.
- `active` (boolean): whether the entity is currently active. Deactivation is
  expressed through the `:deactivate` message (see [Deactivation](#deactivation)); `active` on a
  delivered object reflects the current SSOT state.
- `revision` (integer): a monotonically increasing counter maintained by
  agrirouter for the canonical object. It is central to loop prevention and
  conflict detection (see [Loop prevention](#loop-prevention)).
- `modifiedAt` (string): the {{?RFC3339}} timestamp of the last accepted change.
- `sourceEndpointId` (string): the endpoint whose change produced the current
  canonical revision.
- `previousVersions` (array of references): references to prior entities that this
  entity supersedes, used for split and merge (see [Split and merge](#split-and-merge)). Empty for
  entities with no such lineage.

A **reference** to another entity (for example a field referring to its farm) is
expressed as an object carrying, at minimum, the `agrirouterId` of the target when
known, and MAY additionally carry the referencing participant's `externalId` for
that target:

~~~ json
{ "agrirouterId": "…", "externalId": "FRM-7" }
~~~

## Customer

A customer is the natural or legal person a farm belongs to or a contractor works
for. Canonical attributes (subset, aligned with ISOXML `CTR`):

- `name` (object): either a person name (`firstName`, `lastName`, optional
  `title`) or an `organizationName`. Exactly one form MUST be present.
- `address` (object, optional): `street`, `poBox`, `postalCode`, `city`, `state`,
  `country` (ISO 3166-1 alpha-2).
- `contact` (object, optional): `phone`, `mobile`, `email`.

## Farm

A farm groups fields and belongs to a customer. Canonical attributes (subset,
aligned with ISOXML `FRM`):

- `name` (string, required).
- `customer` (reference, optional): the owning customer (see [Common envelope](#common-envelope)).
- `address` (object, optional): as for a customer.

## Field

A field is the core spatial entity. Canonical attributes (subset, aligned with
ISOXML `PFD`, "partfield"):

- `name` (string, required).
- `area` (number, optional): nominal area in square metres.
- `customer` (reference, optional): the associated customer.
- `farm` (reference, optional): the associated farm.
- `boundary` (GeoJSON, optional): the field boundary as a GeoJSON
  `Polygon` or `MultiPolygon` {{?RFC7946}}.
- `obstacles` (array, optional): obstacles within the field, each a GeoJSON
  `Feature` whose geometry is a `Point`, `LineString`, or `Polygon` and whose
  properties carry an obstacle `kind`.
- `harvestPeriod` (object, optional): see [Harvest period](#harvest-period).
- `metadata` (object, optional): additional key/value metadata that does not fit a
  defined attribute. Participants MUST preserve metadata they do not understand
  and MUST relay it unchanged.

### Entity dependencies

A field MAY reference a customer and a farm; a farm MAY reference a customer.
These dependencies are significant for routing and seeding: a participant that is
to receive fields MUST also be enabled for the customers and farms those fields
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
- `label` (string, optional): a human-facing designation such as `"2026"` or
  `"2025/2026"` for systems that present a discrete year.

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

- The format MUST be **canonical**: every logical value has exactly one valid
  encoding. Producers MUST emit the canonical form; there is no "tolerant" reading
  of equivalent-but-different encodings.
- agrirouter MUST validate every incoming `masterdata:*` message against the
  defined subset. If validation fails, the message MUST be **rejected with an
  error** and MUST NOT be applied to the SSOT or forwarded. Messages are not
  silently repaired.
- Validation and rejection apply only to the `masterdata:*` message types; they do
  not change the handling of other, pre-existing agrirouter message types.

## Identifier mapping

agrirouter maintains, per canonical object, a mapping between its `agrirouterId`
and each participant's `externalId` for that object.

- On receiving a `masterdata:<type>` from endpoint E carrying `externalId` X:
  - if the mapping already resolves (E, X) to a canonical object, that object is
    updated;
  - otherwise a new canonical object is created, `agrirouterId` is assigned, and
    (E, X) is recorded in its mapping.
- When agrirouter delivers a canonical object to endpoint E, it SHOULD include E's
  own `externalId` (if known) so the receiver can reconcile against its local data
  without a lookup.
- The mapping MUST remain compatible with the ISOXML **LinkList** concept
  (ISO 11783-10, Annex E), so that identifier correspondence can be expressed to
  task-data-based tooling.

An endpoint MUST NOT reuse one of its own external identifiers for two distinct
canonical objects. If an endpoint sends an `externalId` that is already mapped to a
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

- Master-data routes MUST NOT be created by the machine→software default-route
  logic. A participant takes part in master-data exchange only through explicit
  **opt-in**.
- Opt-in is expressed **per endpoint and per entity type**. An endpoint may, for
  example, be enabled to exchange fields but not customers.
- Opt-in carries a **direction** (read, write, or both) per entity type. agrirouter
  MUST make each endpoint's read/write configuration discoverable to the other
  participants so that a system can adapt its behaviour — for instance, presenting
  an entity as read-only when it is not permitted to write it back.
- Because of entity dependencies (see [Field](#field)), enabling an endpoint to receive
  fields SHOULD also enable the customers and farms those fields reference.
  Implementations SHOULD surface these dependencies to the user rather than
  silently enabling additional data.
- The opt-in decision SHOULD be offered to the user at endpoint onboarding, and
  MUST remain changeable afterwards.

The concrete configuration resource is described in `openapi.yaml`.

## Initial load and seeding

A newly connected system usually **already holds its own master data**. Seeding
reconciles that existing data with the SSOT. Each endpoint has, per entity type, a
seeding state. The defined progression is:

1. **Connected.** The endpoint is created and the user opts it into master-data
   exchange for one or more entity types (see [Routing and opt-in](#routing-and-opt-in)).
2. **Seeding from agrirouter.** agrirouter sends the endpoint every canonical
   object of the opted-in types that the endpoint is entitled to receive.
3. **Seeding to agrirouter.** After the endpoint has confirmed receipt of those
   objects, it sends agrirouter the objects it knows about, including any objects
   not yet in the SSOT and any objects it changed while resolving conflicts.
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
Instead, an endpoint MUST NOT send back one of its own external identifiers already
associated with a *different* object; agrirouter rejects the offending message (see
[Identifier mapping](#identifier-mapping)). This pushes resolution of a genuine n:1 situation to the
participating systems, which is the intended behaviour for this version.

### Differing required/optional attributes

Systems disagree on which attributes are mandatory (one system may require a
customer on every field where another treats it as optional). The **stricter
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

- agrirouter MUST NOT echo a change back to the endpoint it originated from. The
  originating endpoint is identified by `sourceEndpointId` for the produced
  revision.
- agrirouter maintains the `revision` counter per canonical object. An incoming
  `masterdata:<type>` that does not actually change the canonical object (it is
  equal to the current canonical revision) MUST NOT create a new revision and MUST
  NOT be forwarded. This suppresses no-op "updates" from systems that notify
  unconditionally.
- Participants SHOULD avoid re-emitting an object they have just received without a
  genuine local change. Because some systems cannot guarantee this, agrirouter's
  origin-suppression and no-op detection are the authoritative safeguards and do
  not depend on well-behaved participants.

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

- A newly created entity that supersedes one or more prior entities carries
  references to them in `previousVersions` (see [Common envelope](#common-envelope)). A split produces
  several new entities that each reference the original; a merge produces one new
  entity that references several originals. No operation-specific message is
  required.
- A system that receives such an entity receives the *identifiers* of the
  referenced predecessors, and MAY retrieve their full data on demand using
  `masterdata:<type>:request` (see [Requesting objects](#requesting-objects-lazy-loading)).
- Systems that do not retain historical data may ignore `previousVersions`; the
  information is advisory lineage, not a required processing step.

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
a master-data network can expose a user's customers, farms, and field boundaries to
that endpoint, master-data routes MUST be created only through explicit,
per-endpoint, per-entity opt-in, never by default routing. Second, agrirouter's
read/write configuration per endpoint governs which participants may modify shared
entities; endpoints MUST respect the read/write status communicated for an entity
type and MUST NOT rely on other participants to enforce it on their behalf.

Field boundaries and customer contact details are personal and commercially
sensitive data. Participants SHOULD expose only the data necessary for
synchronization and SHOULD honour deactivation promptly.

# Open issues

The following items are actively tracked by the working group and affect
provisional parts of this document:

- **Object model & data format alignment with FarmSPT** — the canonical model
  [the data model](#data-model) and encoding [Encoding](#encoding) are being reconciled with FarmSPT
  outcomes before being locked down. *(AR-2349)*
- **Unambiguous EFDI subset & hard validation** — the exact validated subset of
  [Encoding and canonicity](#encoding-and-canonicity). *(AR-2020)*
- **Routing model** — the concrete default-route policy, opt-in switches, and
  read/write propagation of [Routing and opt-in](#routing-and-opt-in). *(AR-2353)*
- **Initial load / seeding** — formalizing the seeding state machine, conflict UX
  responsibilities, and downtime/resume semantics of [Initial load and seeding](#initial-load-and-seeding). *(AR-2019)*
- **Loop prevention** — final handling of unconditional-notification systems in
  [Loop prevention](#loop-prevention). *(AR-1915)*
- **Split / merge** — the lineage model of [Split and merge](#split-and-merge). *(AR-2036)*
- **Harvest period** — interval-versus-year resolution in [Harvest period](#harvest-period). *(AR-2037)*
- **`:deactivate` idempotency** — duplicate deactivations in [Deactivation](#deactivation).
  *(AR-2084)*

The conceptual/normative work is tracked under epic **AR-2017** (Master Data
Exchange Processes); its application to the John Deere Operations Center
integration is tracked under epic **AR-1713**.
