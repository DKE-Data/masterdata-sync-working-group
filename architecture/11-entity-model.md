# ADR 11 - Entity model: FarmSPT, AgmaSync and ADAPT

- **Status:** WIP
- **Scope:** The master-data entities of the specification, derived from
  FarmSPT, compared with ADAPT 2. Whether AgmaSync should encode them in the
  ADAPT data model, and what to take from ADAPT otherwise

## Context

AgmaSync's entities are the FarmSPT master data, encoded for
sync. FarmSPT defines the content: which attributes are exchanged, with which
data types and reference systems. Its structure follows ISOXML (Customer, Farm,
Partfield). From ADAPT it uses code lists only: the Role list for members,
BoundaryCreationMethod and GuidancePatternType.

The comparison in this ADR is therefore between FarmSPT, as AgmaSync encodes
it, and ADAPT. The question is whether AgmaSync should encode FarmSPT's content
in ADAPT's entities instead of its own schema, so that systems built on ADAPT
can join with less work.

### From FarmSPT to AgmaSync

| FarmSPT entity | AgmaSync | Change |
| --- | --- | --- |
| Accountowner, Customer/Grower | Party | One entity whatever its party type. Person or organization is optional `details`, since a Customer/Grower does not record it ([ADR 08](08-party-model.md)). `specialised_usage_type` moves to the farm. |
| Member | Person `details.memberships` | A member is a person with a role, not an entity. `title` added. |
| Farm | Farm | Partner becomes `partners`, a party reference with a role. Registration numbers (ZID, VVVO, international), billing address and time zone not yet taken. |
| Field | Field | Ownership status becomes an `owner` party reference. FieldIdentifier (ID, Source) becomes `local_id` and agrirouter's [identifier mapping](../specification.md#identifier-mapping). Site, soil taxonomy and field history not yet taken. `area` added. |
| Field Boundaries | FieldBoundary | An entity of its own. StartDate and valid-to become `harvest_period`. Source becomes `source_endpoint_id`. |
| POI | FieldBoundary `obstacles` | Reduced to geometry and kind inside the boundary. |
| Crop Season | `harvest_period` | Inlined as an interval, not an object. |
| Tramlines, Field Crop, Note, Task | none | Outside MVP scope. |

AgmaSync adds what FarmSPT leaves open: the envelope
(identity, revision, tenant, source), typed references between objects, strict
validation ([ADR 02](02-data-model.md)), and `metadata`.

### AgmaSync entities

```mermaid
classDiagram
    direction TB
    class Party {
        +name
        +details : PartyDetails or absent
        +address : Address
        +contact : Contact
        +billing_address : Address
        +tax_number
        +tax_id
        +trade_id
    }
    class PartyDetails {
        +party_type : person or organization
    }
    class PersonDetails {
        +title
        +first_name
        +last_name
        +memberships
    }
    class OrganizationDetails {
        +commercial_registry_number
    }
    class Membership {
        +organization_id : Party with organization details
        +member_role : ADAPT Role
    }
    class Farm {
        +name
        +owner
        +specialised_usage_type
        +partners
        +address : Address
        +geo_reference : Point
    }
    class Partner {
        +partner_id : Party
        +partner_role : ADAPT Role
    }
    class Field {
        +name
        +area
        +farm
        +owner
        +soil : SoilInfo
        +topography
        +field_boundaries
        +harvest_period : HarvestPeriod
        +metadata
    }
    class FieldBoundary {
        +boundary : Polygon or MultiPolygon
        +boundary_type
        +creation_method
        +harvest_period : HarvestPeriod
        +obstacles
        +regulatory_requirements
        +metadata
    }
    class Obstacle {
        +geometry : Point, LineString or Polygon
        +kind
    }
    Party "1" *-- "0..1" PartyDetails : details
    PartyDetails <|-- PersonDetails
    PartyDetails <|-- OrganizationDetails
    PersonDetails "1" *-- "0..n" Membership : memberships
    Party "0..1" o-- "0..n" Farm : owns
    Farm "1" *-- "0..n" Partner : partners
    Farm "0..1" o-- "0..n" Field : farm
    Party "0..1" o-- "0..n" Field : owns
    Field "1" o-- "0..n" FieldBoundary : field_boundaries
    FieldBoundary "1" *-- "0..n" Obstacle : obstacles
```

### The same entities in ADAPT

```mermaid
classDiagram
    direction TB
    class Party {
        +name
        +partyTypeCode
        +contactInfo : ContactInfo
        +parentPartyId : Party
    }
    class ContactInfo {
        +addressContactMethods
        +telecommunicationContactMethods
    }
    class Grower {
        +name
        +partyId : Party
    }
    class Farm {
        +name
        +growerId : Grower
        +partyId : Party
    }
    class Field {
        +name
        +farmId : Farm
        +arableArea
        +activeBoundaryId : FieldBoundary
        +timeZone
    }
    class FieldBoundary {
        +name
        +fieldId : Field
        +seasonIds : Season
        +boundary : Boundary
        +obstacles
        +headlands
    }
    class Boundary {
        +geometry : WKT Polygon or MultiPolygon
        +boundaryCreationMethodCode
        +partyRoles
    }
    class PartyRole {
        +partyId : Party
        +roleCode : Role
        +timeScopes
    }
    class Obstacle {
        +name
        +boundary : Boundary
        +isPassable
    }
    class Season {
        +name
        +start
        +end
    }
    Party "1" *-- "0..1" ContactInfo : contactInfo
    Party "0..1" o-- "0..n" Grower : partyId
    Grower "0..1" o-- "0..n" Farm : growerId
    Farm "0..1" o-- "0..n" Field : farmId
    Field "1" o-- "0..n" FieldBoundary : fieldId
    Season "0..n" o-- "0..n" FieldBoundary : seasonIds
    FieldBoundary "1" *-- "1" Boundary : boundary
    FieldBoundary "1" *-- "0..n" Obstacle : obstacles
    Boundary "1" *-- "0..n" PartyRole : partyRoles
```

### Mapping one farm both ways

[examples](../examples/) maps one farm both ways.
Findings from that exercise:

| | |
| --- | --- |
| AgmaSync attributes (envelope plus 6 entity types) | 42 |
| with a native ADAPT counterpart | 17, including owner → Grower (1:1) and harvest period → Season; 2 only after restructuring (boundary list inverted, area in ha) |
| needing a custom context item | 25, which need 42 custom definitions because lists and structured values take several each (e.g. `partners`: list, entry, party id, role). One of them, `AgmaSync-SeasonId`, only links a field to its Season, since an ADAPT Field has no `seasonIds` |
| of these, FarmSPT content | 17: tax number, tax id, trade id, commercial registry number, billing address, member first and last name, memberships, partners, farm address, farm geo reference, specialised usage type, field owner, soil, topography, boundary type, regulatory requirements |
| of these, added by AgmaSync | 8: the envelope (active, revision, modified at, tenant, source endpoint), `metadata` on field and boundary, person `title` |
| ADAPT attributes with no AgmaSync source | the boundary `name` ADAPT requires |

Here is an example for the farm's contractor. In AgmaSync:

```json
{
  "type": "farm",
  "name": "Hofgut Sonnenberg",
  …
  "partners": [
    {
      "partner_id": { "agrirouter_id": "0b6e…4a53" },
      "partner_role": "CUSTOM_SERVICE_PROVIDER"
    }
  ],
  …
}
```

In ADAPT, ADAPT's Farm has no partners, so the same data becomes nested
context items, each one declared in `customDataTypeDefinitions`:

```json
{
  "name": "Hofgut Sonnenberg",
  …
  "contextItems": [
    …
    { "definitionCode": "AgmaSync-Partners", "contextItems": [
      { "definitionCode": "AgmaSync-Partner", "contextItems": [
        { "definitionCode": "AgmaSync-PartnerPartyId", "valueText": "0b6e…4a53" },
        { "definitionCode": "AgmaSync-PartnerRole", "valueText": "CUSTOM_SERVICE_PROVIDER" }
      ]}
    ]},
    …
  ]
}
```

The gap is FarmSPT's content, not AgmaSync's design. Two thirds of the custom
items carry FarmSPT attributes, several of them
for German or EU rules (tax and trade ids, soil rating points, nitrogen red
zones). ADAPT has no slot for them. Encoding FarmSPT in ADAPT does not remove
them, it moves them into context items. Most of the rest is the sync envelope,
which ADAPT, a file format, has no concept of.

FarmSPT content not yet in AgmaSync shifts the balance both ways. Farm
registration numbers (ZID, VVVO) would be custom items as well. The time zone
has a native slot, though on the field (`Field.timeZone`), not the farm. The
entities outside MVP scope are where ADAPT is strongest: tramlines map to
GuidanceGroup and GuidancePattern, whose type list FarmSPT uses, and Field Crop to Crop, CropZone and Product. ADAPT covers FarmSPT's
operational data better than its master data.

ADAPT 2.0.2 as published is permissive. No object rejects unknown properties.
The rule that a context item carries a value or nested items is not enforced.
A `referenceId` is unique only "within a single data instance", meaning one
file.

## Options

- **A. Keep the FarmSPT-derived model.** Reuse ADAPT code lists where they fit,
  as FarmSPT does.
- **B. Encode FarmSPT in ADAPT.** ADAPT entities as the wire model. FarmSPT
  attributes without an ADAPT slot, and the envelope, go into `AgmaSync-`
  context items as in the example.
- **C. Keep the FarmSPT-derived model, align selectively, and publish a
  mapping.** A normative two-way mapping plus the `AgmaSync-` custom
  definitions, so ADAPT systems convert at their edge. Adopt ADAPT's structure
  where it is better for sync. Add a `name` to FieldBoundary, which ADAPT
  requires, so the mapping does not have to derive one.
- **D. Accept both on the wire.** agrirouter translates between them.
- **E. Wrap ADAPT in the AgmaSync envelope.** Each object is an AgmaSync
  envelope with typed fields. It carries the unchanged ADAPT component in
  `adapt`, and the FarmSPT attributes ADAPT lacks as typed fields in
  `extensions`, not as context items.
- **F. Like E, with the extensions inside `adapt`.** The FarmSPT attributes
  ADAPT lacks are typed properties of the ADAPT object itself, named with a
  vendor prefix: `_agmasync_partners`, a valid identifier in JavaScript and
  most other languages, so generated code and dot access work. Once ADAPT adds
  a slot, agrirouter writes the attribute natively or prefixed, depending on
  the receiver's ADAPT version.

## Criteria

1. **Adoption cost** for each kind of participant.
2. **Contract strength.** Can the published schema reject invalid data?
   ([ADR 02](02-data-model.md) chose strict validation because ISOXML's
   permissiveness made implementations diverge.)
3. **Fit with sync.** Identity, revision, and typed references per object.
4. **Lossless round trip** between participants.
5. **Evolution and governance.** Who controls change, and at what speed.
6. **Tooling.** Generated clients, validators, readability of payloads.
7. **Cost to agrirouter.**

## Analysis

### 1. Adoption cost by participant

Participants fall into three groups. Their share is not known (see Open
questions).

| Participant | A | B | C | D | E | F |
| --- | --- | --- | --- | --- | --- | --- |
| Built on ADAPT 2 | map to AgmaSync | lowest: native shape, still must handle `AgmaSync-` items | low: published mapping and definitions | low: send B's shape | low: `adapt` passes through unchanged, `extensions` are typed | low: `adapt` is a valid ADAPT object, `_agmasync_` properties are typed |
| Built on ISOXML / EFDI | low: FarmSPT maps its attributes to ISO 11783 Customer / Farm / Partfield | high: map to ADAPT, then to context items | low | low: send A's shape | medium to high: map to ADAPT, no context items | medium to high, as E |
| Proprietary REST (client / farm / field) | medium | medium to high | medium | medium: send A's shape | medium to high | medium to high |

D is cheapest for participants because each picks its format; the cost moves to
agrirouter (see 7).

B lowers the cost for one group and raises it for the group FarmSPT and ADR 2
aligned with. The saving for ADAPT systems is also smaller than it looks. 25 of
42 attributes, 17 of them FarmSPT's, would still arrive as `AgmaSync-` context
items, which they must understand anyway to read the data. Using ADAPT's shape
does not make FarmSPT's content understood.

### 2. Contract strength

Context items are an entity-attribute-value (EAV) model: a list of
code/value pairs. EAV suits open extension in files. For a validated API
contract it is a known anti-pattern.

- A JSON schema cannot say "when the code is X, the value must be a UUID
  referencing a party". That rule moves out of the schema into prose and into
  code at agrirouter and at every client.
- Everything can still be enforced. agrirouter can reject undeclared codes as a
  `400`, which ADAPT's own rules support. It can also check values against each
  definition's base type, regular expression, range and scopes. That is a second
  validator in addition to JSON schema. It is custom and must be reimplemented by
  every participant that wants to validate before sending.

Example: a participant misspells one name in the partner above.

| | Misspelling | JSON schema says |
| --- | --- | --- |
| AgmaSync | `"partner_rol": "CUSTOM_SERVICE_PROVIDER"` | `400`: `partner_role` is required |
| ADAPT | `"definitionCode": "AgmaSync-PartnerRol"` | valid: any string is a definition code |

Under ADAPT, only the custom validator notices that `AgmaSync-PartnerRol` is not
declared. A participant without it sends the object, and the role is lost.

This recreates the situation ADR 02 set out to avoid: validity defined outside
the schema, so implementations diverge in how strictly they apply it.

### 3. Fit with sync

- **Identity.** ADAPT has no stable identity per object across exchanges. Its
  references are scoped to one file. B keeps AgmaSync's envelope entirely as
  extensions, and must redefine what `farmId`, `growerId` and similar mean on a
  per-object API. In the example, a field's `"farmId": "0b6e…4a54"` is an
  agrirouter id, but ADAPT equally allows `"farmId": "FARM-3001"`, the sender's
  local id. Both are legitimate on send. Unlike `EntityReference`, a bare string
  does not say which it is, so B needs a rule for that and agrirouter has to
  tell them apart by lookup:

  ```json
  "farms": [
    {
      "id": {
        "referenceId": "0b6e…4a54",
        "uniqueIds": [
          { "idText": "0b6e…4a54", "idTypeCode": "UUID", "idSource": "https://agrirouter.com" },
          { "idText": "FARM-3001", "idTypeCode": "STRING", "idSource": "urn:example:farm-management-app" }
        ]
      },
      "name": "Hofgut Sonnenberg",
      …
    }
  ],
  "fields": [
    { "name": "Am Mühlenbach", "farmId": "0b6e…4a54", … }
  ]
  ```

  Same reference, as the sender's local id:

  ```json
  "farms":  [ { "id": { "referenceId": "FARM-3001" }, "name": "Hofgut Sonnenberg", … } ],
  "fields": [ { "name": "Am Mühlenbach", "farmId": "FARM-3001", … } ]
  ```

  FarmSPT's FieldIdentifier (ID and Source, repeatable) is ADAPT's `uniqueIds`
  (`idText`, `idSource`) under another name. AgmaSync drops both for `local_id`,
  because a list of every system's id would show each receiver the other
  participants' identifiers, which the specification rules out.
- **References.** In AgmaSync, every attribute that points at another object has
  the type `EntityReference`: `partner_id` above, `owner`, `farm`,
  `organization_id`. agrirouter finds them from the schema and applies the same
  rules to all: resolve a `local_id` on send, fill in `agrirouter_id` on
  delivery, lazy-load by the slot's entity type, reject a target not yet sent.
  Under B, this can be modelled, but only as an AgmaSync convention: the custom
  definition declares that it holds a reference, and to what, in its `keywords`,
  and agrirouter finds references from the published definitions.

  Example: the sender names the contractor by its own local id. In AgmaSync:

  ```json
  "partner_id": { "local_id": "ORG-1002" }
  ```

  Under B, the partner entry:

  ```json
  { "definitionCode": "AgmaSync-PartnerPartyId", "valueText": "ORG-1002" }
  ```
  and the definition it relies on:
  ```json
  { "definitionCode": "AgmaSync-PartnerPartyId", "dataDefinitionBaseTypeCode": "TEXT",
    "keywords": "reference:party", … }
  ```

  This works, with the cost described under Contract strength: the JSON schema
  does not enforce the convention, generic ADAPT tools do not understand it, and
  every participant implements it. In AgmaSync it comes with the schema type.
- **Aggregate boundaries.** ADAPT is better on one point. A boundary references
  its field (`fieldId`), whereas AgmaSync's field lists its boundaries
  (`field_boundaries`), mirroring FarmSPT, where boundaries sit
  under the field. Under AgmaSync's model ([ADR 03](03-revision-model.md)),
  adding a boundary therefore revises the field, which causes needless conflicts
  on the field. The child referencing its parent is the better design for a
  revision-based sync. The direction of the reference is a matter of encoding,
  so following ADAPT here leaves FarmSPT's content unchanged.
- **Grower and Party.** FarmSPT has no such split. Its Customer/Grower carries
  the party attributes itself. Two objects for one owner means two revisions to
  keep consistent. ADR 08 keeps FarmSPT's single object for that reason.
- **Write semantics.** Writes are JSON Merge Patch (RFC 7396): objects merge
  key by key, arrays are replaced whole. Under B, most attributes sit in the
  `contextItems` array, so plain RFC 7396 loses partial updates and turns
  unrelated edits into conflicts. Merging `contextItems` by `definitionCode`, as
  if the array were an object keyed by code, restores both.

  Example: from revision 7, one endpoint corrects the tax number and another
  the trade id. In AgmaSync these are different keys, so agrirouter merges both:

  ```json
  { "tax_number": "DE287654322" }
  { "trade_id": "DE287654321-00002" }
  ```

  Under B, with the keyed merge, the same two writes also merge:

  ```json
  { "contextItems": [ { "definitionCode": "AgmaSync-TaxNumber", "valueText": "DE287654322" } ] }
  { "contextItems": [ { "definitionCode": "AgmaSync-TradeId", "valueText": "DE287654321-00002" } ] }
  ```

  With plain RFC 7396, each write would replace the whole array: the first
  would wipe everything but the tax number, and the second would conflict with
  it.

  The keyed merge is a custom rule, not RFC 7396, and it needs more rules to
  be complete:

  - **Removal.** RFC 7396 removes with `null`, but there is no key to set to
    `null`, and ADAPT does not allow `"valueText": null`. A convention is
    needed, e.g. an item with neither `valueText` nor `contextItems`.
  - **Repeated codes.** `AgmaSync-Partner` occurs once per partner and has no
    key, so the partners list is replaced whole, as in AgmaSync. Metadata is
    worse: AgmaSync merges `metadata` per key, while `AgmaSync-Metadata` entries
    are a list, unless the merge also keys them by `AgmaSync-MetadataKey`.
  - **ADAPT's own arrays.** `telecommunicationContactMethods` holds phone,
    mobile and email together, where AgmaSync's `contact` has one key each.
    Merging them per item needs a key per array: `telecommunicationContactTypeCode`,
    `addressContactTypeCode`, `idSource` for `uniqueIds`.

  Every participant implements these rules, since no RFC 7396 library applies
  them.

### 4. Lossless round trip

The example round-trips losslessly only because of the custom definitions.
Where one side has something the other lacks, the loss is structural:

- ADAPT → AgmaSync: none. A Grower without a Party,
  `{ "id": { "referenceId": "g1" }, "name": "Müller" }`, becomes a party
  without `details`.
- AgmaSync → ADAPT: none, provided the receiver knows the `AgmaSync-`
  definitions. An ADAPT tool that does not know them keeps the values but loses
  their meaning. A party without `details` becomes a Party of type `UNKNOWN`.

Under B, a participant that ignores unknown context items, which ADAPT allows,
writes back an object without them. For example, an ADAPT tool renames the farm
and sends it back with only `name` and `growerId`, dropping `AgmaSync-Partners`.
What protects the partners then depends on write semantics, not on the model.
If the tool leaves out `contextItems` entirely, merge patch keeps them. If it
sends `contextItems` with only its own items, plain RFC 7396 replaces the array
and the partners, tax ids and envelope are gone. Only the keyed merge from
Write semantics keeps them.

### 5. Evolution and governance

- Under A and C, content comes from FarmSPT and the encoding is AgmaSync's. A
  FarmSPT attribute that enters scope becomes a typed AgmaSync attribute, and
  the mapping records its ADAPT slot or custom definition. When ADAPT adds a
  slot, the mapping moves an attribute out of custom items. Neither AgmaSync nor
  its participants have to change.
- Under B, the shape of every object follows AgGateway's release cycle and
  governance, while FarmSPT's content sits beneath it in `AgmaSync-`
  definitions. B couples AgmaSync to ADAPT without handing the content over.
- As FarmSPT's operational entities (tramlines, crops, tasks) come into scope, the
  mapping gains mostly native ADAPT entries. Alignment with ADAPT gets cheaper
  there, but it stays a question of mapping, not of the wire model.

### 6. Tooling

- **Generated clients.** Under B, generated code exposes `contextItems` lists,
  not `partners` or `tax_id`. Every participant writes its own lookup and
  conversion code for about 60% of the data. Reading the contractor's role,
  in Go:

  ```go
  // AgmaSync
  role := farm.Partners[0].PartnerRole

  // ADAPT
  var role string
  for _, ci := range farm.ContextItems {
      if ci.DefinitionCode != "AgmaSync-Partners" { continue }
      for _, p := range ci.ContextItems {
          for _, f := range p.ContextItems {
              if f.DefinitionCode == "AgmaSync-PartnerRole" { role = f.ValueText }
          }
      }
  }
  ```
- **Payloads.** The example needs 208 lines as AgmaSync and 1218 as ADAPT,
  including the definitions. On the wire, the definitions would be published
  once, not sent with every object.
- **Readability.** A typed payload documents itself. With context items, a
  reader needs the definitions next to the payload to understand it.

### 7. Cost to agrirouter

| | A | B | C | D | E | F |
| --- | --- | --- | --- | --- | --- | --- |
| Schema and validation | exists | rewrite, plus a validator driven by the definitions | exists | both | envelope and `extensions` typed; `adapt` by a tightened copy of the ADAPT schema | as E, with the `_agmasync_` properties declared in the tightened ADAPT schema, one schema per supported ADAPT version |
| Registry of custom definitions | none | required | publish once | required | none | none |
| Translation | none | none | none, done at the participant's edge | server-side in both directions | none | per ADAPT version, only for attributes that gained a native slot |
| Conflict and merge semantics | exists | custom merge: keyed by `definitionCode`, plus rules for removal, repeated codes and ADAPT's own arrays | exists | across two representations | RFC 7396; only ADAPT's own arrays are replaced whole | as E |

D is the most expensive. It also makes agrirouter responsible for lossy
translation between participants, so a field lost in translation becomes
agrirouter's fault.

### Option E in detail

The farm from the example under E:

```json
{
  "type": "farm",
  "agrirouter_id": "0b6e…4a54",
  "local_id": "FARM-3001",
  "revision": 12,
  …
  "adapt": {
    "name": "Hofgut Sonnenberg",
    "growerId": "GRW-0001",
    …
  },
  "extensions": {
    "specialised_usage_type": "arable farming",
    "partners": [
      {
        "partner_id": { "agrirouter_id": "0b6e…4a53" },
        "partner_role": "CUSTOM_SERVICE_PROVIDER"
      }
    ],
    …
  }
}
```

E keeps what makes AgmaSync work and removes the EAV problem of B. Assessed on
the [criteria](#criteria) above:

- **Contract strength.** The envelope and `extensions` are typed and validated
  by JSON schema, like today. No context items, no definitions registry, no
  second validator. `adapt` is only as strict as the ADAPT schema, which is
  permissive. AgmaSync would publish a tightened copy that rejects unknown
  properties.
- **Identity and references.** The envelope carries identity, revision and
  tenant, as today. References in `extensions` are `EntityReference`.
  References inside `adapt` (`growerId`, `farmId`, `fieldId`,
  `partyId`, `parentPartyId`, `seasonIds`) stay strings. They hold the near end's `local_id`,
  with the target's `agrirouter_id` alongside on delivery, as set out under
  [F](#option-f-in-detail). In E the canonical ids (`canonicalGrowerId`) go
  into `extensions`.
- **Write semantics.** RFC 7396 works: envelope, `adapt` and `extensions` are
  objects, so a change to `partners` does not touch `name`. Only ADAPT's own
  arrays (`telecommunicationContactMethods`, `addressContactMethods`,
  `uniqueIds`) are replaced whole.
- **Round trip.** An ADAPT tool that edits only `adapt` leaves `extensions`
  untouched, and merge patch keeps them. That is the case B cannot protect
  without a custom merge.
- **Two places for data.** Every attribute lives in `adapt` if ADAPT has a
  slot, otherwise in `extensions`. A reader must know which. When a later ADAPT
  version adds a slot, the attribute moves from `extensions` to `adapt`, which
  is a breaking change. Under C the same event changes only the mapping.
- **Structure inherited from ADAPT.** The body brings Grower and Season as
  objects of their own, and the boundary references its field. The boundary
  reference is wanted. Grower adds an object FarmSPT does not have. Season is
  an object FarmSPT wants as well (see Open questions).
- **Versioning.** The envelope must name the ADAPT version of `adapt`, and
  AgmaSync decides when to move to a new one.

E is the strongest option built on ADAPT. Compared with C, it trades a lower
cost for ADAPT participants against a higher one for ISOXML participants,
FarmSPT's content split across two vocabularies, and an extra Grower object to
sync.

### Option F in detail

The farm from the example under F:

```json
{
  "type": "farm",
  "agrirouter_id": "0b6e…4a54",
  "revision": 12,
  …
  "adapt": {
    "id": { "referenceId": "FARM-3001" },
    "name": "Hofgut Sonnenberg",
    "growerId": "GRW-0001",
    …
    "_agmasync_specialisedUsageType": "arable farming",
    "_agmasync_partners": [
      {
        "partner_id": { "agrirouter_id": "0b6e…4a53" },
        "partner_role": "CUSTOM_SERVICE_PROVIDER"
      }
    ],
    …
  }
}
```

A prefixed name is `_agmasync_` plus the AgmaSync attribute name in camelCase,
following ADAPT (`_agmasync_specialisedUsageType`), and its value has the
AgmaSync type, as in E's `extensions`. No ADAPT property starts with an
underscore, so the prefix cannot collide with a later ADAPT slot.

Compared with E:

- **One object.** `adapt` is a valid ADAPT 2.0.2 object, since ADAPT ignores
  unknown properties. Its `id.referenceId` replaces the envelope's `local_id`
  and follows the same rule: the id of the participant at the near end of the
  transfer, and on delivery `id` is left out where the receiver has none. On
  send it matches the `{local_id}` in the request path.
  Native references then resolve against `referenceId` as in an ADAPT file.
  A participant can hand
  `adapt` to ADAPT tooling as a whole. Tools that do not know the prefix drop
  or ignore those properties.
- **References.** Prefixed AgmaSync attributes hold `EntityReference`, as in
  E. A native reference (`growerId`, `farmId`, `fieldId`, `partyId`,
  `parentPartyId`, `seasonIds`) holds the `local_id` of the participant at the
  near end of the transfer, as `id.referenceId` does. On delivery it comes
  with the target's `agrirouter_id` in `_agmasync_canonical` plus the ADAPT
  name: `_agmasync_canonicalGrowerId`. Together the two carry what an
  `EntityReference` carries.
  - **On send**, the native reference holds the sender's id. agrirouter
    resolves it through the identifier mapping and rejects one that does not
    resolve, as today. The sender always has one, since it must bind a target
    before referencing it. The canonical id is ignored, so an endpoint can
    send back an object as received.
  - **On delivery**, the canonical id is always present. The native reference
    holds the receiver's id, and is left out where the receiver has none,
    which agrirouter knows from the mapping. That is the case whenever the
    receiver has not bound the target yet: in the initial load, after an
    unbind, or when a new target and the object referencing it arrive before
    the receiver has bound the target.

  Example: the farm, delivered to an endpoint that has not yet bound the
  grower:

  ```json
  "adapt": {
    "id": { "referenceId": "FARM-3001" },
    "name": "Hofgut Sonnenberg",
    "_agmasync_canonicalGrowerId": "0b6e…4a58",
    …
  }
  ```

  Once it binds the grower as `GRW-0001`, the endpoint sets
  `"growerId": "GRW-0001"` in its own copy, and later deliveries carry it:

  ```json
  "adapt": {
    "id": { "referenceId": "FARM-3001" },
    "name": "Hofgut Sonnenberg",
    "growerId": "GRW-0001",
    "_agmasync_canonicalGrowerId": "0b6e…4a58",
    …
  }
  ```

  A reference is set or changed only in the native slot. Where that is
  absent, merge patch keeps the current reference, and `null` removes it. For
  a list, `_agmasync_canonicalSeasonIds` holds every target and `seasonIds`
  the bound ones. A `seasonIds` sent replaces only the targets the sender has
  bound, so an echoed list keeps the others.

  An object the receiver has not bound arrives without `id`, and is valid
  ADAPT only once bound, as with a missing `fieldId`. The receiver creates it
  and binds its own id, as today.

  [option-f-sample.json](../examples/option-f-sample.json)
  shows the whole farm, with one unbound season.
- **Contract strength.** Unchanged. The tightened ADAPT schema declares each
  `_agmasync_` property with its type and still rejects unknown properties, so
  `_agmasync_partner` (singular) is a `400`, as in AgmaSync.
- **Write semantics and round trip.** Unchanged. Prefixed properties are
  object keys, so RFC 7396 merges them one by one. An ADAPT tool that sends
  `adapt` back without them leaves them in place, since absent keys are kept.
- **Moving into ADAPT.** This replaces E's breaking change. agrirouter stores
  each attribute once and writes it according to the ADAPT version the
  receiving endpoint declares. Suppose a later ADAPT version adds partner roles
  to Farm (hypothetical, modelled on `Boundary.partyRoles`). A receiver on that
  version gets the native slot:

  ```json
  "adapt": {
    "name": "Hofgut Sonnenberg",
    "partyRoles": [ { "partyId": "ORG-1002", "roleCode": "CUSTOM_SERVICE_PROVIDER" } ],
    "_agmasync_specialisedUsageType": "arable farming",
    …
  }
  ```

  A receiver still on 2.0.2 keeps getting `_agmasync_partners`, unchanged.
  On send, agrirouter accepts the shape of the sender's version only, so per
  version each attribute has exactly one place: a sender on the newer version
  that sends `_agmasync_partners` gets a `400`.
- **Cost of the move.** agrirouter owns one mapping per attribute that gained
  a slot, in both directions. Unlike D, it is between typed shapes that
  agrirouter defines.

F keeps E's strengths, makes the AgmaSync additions visible where they apply,
and lets attributes move into ADAPT without breaking existing participants.
The price is per-version rendering at agrirouter. The trade against C is the
same as for E.

## Recommendation

**Option C.**

1. Keep the FarmSPT-derived AgmaSync model as the contract, under ADR 02.
2. **Reverse the field–boundary reference**, following ADAPT: `fieldBoundary.field`
   replaces `field.field_boundaries` ([ADR 12](12-boundary-references-field.md)).
3. Keep reusing ADAPT code lists, as FarmSPT does. Wherever AgmaSync has an
   extensible enumeration, draw its values from the ADAPT list if one exists
   ([Extensible enumerations](../specification.md#extensible-enumerations)).
4. Publish a normative AgmaSync–ADAPT mapping and the `AgmaSync-` definitions,
   starting from the [example](../examples/). The definitions cover domain attributes only. The envelope beyond
   identity is sync state and stays out of ADAPT. ADAPT-based participants
   then convert at their edge, and there is one conversion instead of one per
   participant.
5. Keep the Grower concept out of the model, as FarmSPT does. For ADAPT →
   AgmaSync, the mapping states that a Grower becomes its Party, and that a
   Grower without a Party becomes a party without `details`.
6. Add an optional `name` to FieldBoundary, which ADAPT requires. The mapping
   falls back to the field name only where it is absent.

## Open questions

- **Harvest period.** FarmSPT makes the season an optional object the farmer
  creates, as ADAPT does with Season. AgmaSync inlines it. The season object
  serves to link tasks, which come with the Task entity. Until then
  only fields and boundaries would reference it.
