# AgmaSync–ADAPT mapping

Normative mapping between the AgmaSync entities ([specification](./specification.md#data-model))
and ADAPT 2.0.2. A participant built on ADAPT converts at its edge by this mapping
([ADR 11](./architecture/11-entity-model.md)). agrirouter does not translate.

Domain attributes without an ADAPT slot are carried as context items defined in
[adapt-definitions.json](./adapt-definitions.json), codes prefixed `AgmaSync-`.
A converter MUST use these definitions and no others for AgmaSync attributes, and
MUST include the definitions it uses in the catalog's
`customDataTypeDefinitions`. [examples](./examples/) shows one farm both ways.

## Envelope and identity

| AgmaSync | ADAPT |
| --- | --- |
| `agrirouter_id` | `id.referenceId`, and `id.uniqueIds[]` (`UUID`, source `https://agrirouter.com`) |
| `local_id` | `id.uniqueIds[]` (`STRING`, source = the participant's application) |
| `type` | the catalog collection |

The rest of the envelope (`active`, `revision`, `modified_at`, `tenant_id`,
`source_endpoint_id`) is sync state, not domain data, and has no ADAPT mapping.
It stays with the participant's sync layer
([Common envelope](./specification.md#common-envelope)).

**References.** An AgmaSync reference maps to the target's `referenceId`. In a
converted delivery every `referenceId` is the `agrirouter_id`. Converting for
send, a reference to a target the participant knows only by its own id becomes a
reference carrying that `local_id` ([References](./specification.md#references)).
Context items that hold a reference name the target type in `keywords`
(`reference:party`, `reference:organization`, `reference:season`).

## Party

| AgmaSync | ADAPT |
| --- | --- |
| `name` | `party.name` |
| `details.party_type` | `party.partyTypeCode`: `PERSON` ↔ `INDIVIDUAL`, `ORGANIZATION` ↔ `ORGANIZATION`, no `details` ↔ `UNKNOWN` |
| `details.title`, `first_name`, `last_name` | `AgmaSync-Title`, `-FirstName`, `-LastName` |
| `details.commercial_registry_number` | `AgmaSync-CommercialRegistryNumber` |
| `details.memberships[]` | `AgmaSync-Memberships` { `AgmaSync-Membership` { `-MemberOrganizationId`, `-MemberRole` } per entry } |
| `address` | `contactInfo.addressContactMethods[]` `PHYSICAL`, see [Addresses](#addresses) |
| `billing_address` | `contactInfo.addressContactMethods[]` `LEGAL`, see [Addresses](#addresses) |
| `contact.phone`, `mobile`, `email` | `contactInfo.telecommunicationContactMethods[]` `FIXED_PHONE`, `MOBILE_PHONE`, `EMAIL` |
| `tax_number`, `tax_id`, `trade_id` | `AgmaSync-TaxNumber`, `-TaxId`, `-TradeId` |

### Addresses

| AgmaSync | ADAPT `addressContactMethod` |
| --- | --- |
| `street`, `po_box` | `addressLines`: the first line is `street`, the second `po_box`. A PO box without a street leaves the first line empty |
| `postal_code` | `postalCode` |
| `city` | `city` |
| `state` | `countrySubdivision` |
| `country` | `countryCode` |

A `POSTAL` address has no AgmaSync counterpart.


## Grower

AgmaSync has no Grower ([ADR 08](./architecture/08-party-model.md)).

- AgmaSync → ADAPT: each party that owns a farm gets one Grower, linked by
  `grower.partyId`, named as the party.
- ADAPT → AgmaSync: a Grower becomes its Party. A Grower without a Party becomes
  a party without `details`, named by the Grower.

## Farm

| AgmaSync | ADAPT |
| --- | --- |
| `name` | `farm.name` |
| `owner` | `farm.growerId` → the owner's Grower (see [Grower](#grower)) |
| `specialised_usage_type` | `AgmaSync-SpecialisedUsageType` |
| `partners[]` | `AgmaSync-Partners` { `AgmaSync-Partner` { `-PartnerPartyId`, `-PartnerRole` } per entry } |
| `address` | `farm.partyId` → the farm's Party, `contactInfo.addressContactMethods[]` `PHYSICAL` (see [Addresses](#addresses) and below) |
| `geo_reference` | `AgmaSync-GeoReference` (WKT `POINT`) |

The farm's Party is not an AgmaSync object, and its `referenceId` is local to the
ADAPT document.

- AgmaSync → ADAPT: a farm with an `address` gets one Party, named as the farm,
  `partyTypeCode` `UNKNOWN`, `parentPartyId` the owner, carrying only the address.
- ADAPT → AgmaSync: the farm's Party contributes only its `PHYSICAL` address. It
  does not become a party, and its other attributes are not mapped.

## Field

| AgmaSync | ADAPT |
| --- | --- |
| `name`, `farm` | `field.name`, `field.farmId` |
| `area` (m²) | `field.arableArea` (ha) |
| `owner` | `AgmaSync-FieldOwnerPartyId` |
| `soil.type`, `soil.rating_points` | `AgmaSync-SoilType`, `-SoilRatingPoints` |
| `topography` | `AgmaSync-Topography` (`arcdeg`) |
| `harvest_period` | a `season`, referenced by `AgmaSync-SeasonId`, since an ADAPT Field has no `seasonIds` |
| `metadata` | see [Metadata](#metadata) |

## FieldBoundary

| AgmaSync | ADAPT |
| --- | --- |
| `field` | `fieldBoundary.fieldId` |
| `name` | `fieldBoundary.name` |
| `boundary` | `boundary.geometry` (WKT) |
| `creation_method` | `boundary.boundaryCreationMethodCode` |
| `boundary_type`, `regulatory_requirements` | `AgmaSync-BoundaryType`, `-RegulatoryRequirement` |
| `harvest_period` | `fieldBoundary.seasonIds[]` → `season` |
| `obstacles[]` | `obstacles[]`: geometry as `boundary.geometry`, `kind` as `AgmaSync-ObstacleKind` |
| `metadata` | see [Metadata](#metadata) |

AgmaSync → ADAPT: a boundary without `name` takes its field's name, which ADAPT
requires.

An obstacle may be a `Point` or `LineString`. ADAPT describes an obstacle
boundary as a Polygon or MultiPolygon but its schema accepts any WKT string, so
the geometry is carried unchanged.

## Harvest period

Seasons are not AgmaSync objects. Their `referenceId` is local to the ADAPT
document, and fields and boundaries with the same period share one season.

| AgmaSync | ADAPT |
| --- | --- |
| `label` | `season.name`, which ADAPT requires: without `label`, `valid_from/valid_to`, which maps back to no `label` |
| `valid_from` | `season.start`, 00:00:00Z |
| `valid_to` | `season.end`, 23:59:59Z, absent when open |

## Metadata

`metadata` maps to `AgmaSync-MetadataEntries`, with one
`AgmaSync-Metadata` { `-MetadataKey`, `-MetadataValue` } per key. The value is
JSON-encoded.

## Code lists

Values pass unchanged. `member_role` and `partner_role` are ADAPT `Role` codes,
`creation_method` is an ADAPT `BoundaryCreationMethod` code
([Extensible enumerations](./specification.md#extensible-enumerations)). The
other extensible enumerations have no ADAPT list and go into their context item
as they are.

## Open questions

- **How to map parent party.** ADAPT's `party.parentPartyId` is a single parent
  without a role, between any two parties. AgmaSync `memberships` are a list,
  with a role, from a person to an organization, so they stay in
  `AgmaSync-Memberships`. Two uses of `parentPartyId` are open:
  1. ADAPT → AgmaSync: an `INDIVIDUAL` whose `parentPartyId` is an
     `ORGANIZATION`, without `AgmaSync-Memberships`, becomes a person with one
     membership in it, role `UNKNOWN`. Otherwise a native ADAPT hierarchy is
     dropped. Any other `parentPartyId` has no AgmaSync counterpart.
  2. AgmaSync → ADAPT: a person with exactly one membership also gets
     `parentPartyId`, so generic ADAPT tools see the hierarchy. The membership is
     then carried twice, and `AgmaSync-Memberships` wins where present.
- **Unknown context items as metadata.** ADAPT → AgmaSync drops a context item
  that has no mapping, such as the standard `GS1-GLN` or another vendor's own
  definition. On a field or boundary it could go into `metadata` and come back:
  - the key is its `definitionCode`, prefixed (`adapt:GS1-GLN`) so that only
    prefixed keys return to ADAPT;
  - the value is its `valueText` as a JSON string. Nested items need a JSON
    encoding, or are left out;
  - a custom definition does not travel with the value. On the way back the
    converter either declares a minimal `TEXT` definition, or restores only
    standard codes;
  - `timeScopes` are lost unless folded into the value;
  - a party or farm has no `metadata`, so its unknown items are still dropped.

  Alternatively, the standard definitions could become native AgmaSync
  attributes, typed in the schema. ADAPT 2.0.2 has these for the AgmaSync
  entities:

  | Entity | ADAPT definition | AgmaSync attribute |
  | --- | --- | --- |
  | Farm | `GS1-GLN` | `gln` (string) |
  | Field | `GS1-GLN` | `gln` (string) |
  | Field | `BillableArea` (ha) | `billable_area` (m²) |
  | Field | `ReportedArea` (ha) | `reported_area` (m²) |
  | Party, FieldBoundary | none | |

  Other vendors' definitions would still need the `metadata` route, or be
  dropped.
- **Unknown attributes.** ADAPT properties with no AgmaSync counterpart are
  dropped on ADAPT → AgmaSync:

  | ADAPT | Property | Note |
  | --- | --- | --- |
  | all entities | `description` | free text |
  | Party | `parentPartyId` | see "How to map parent party" |
  | contact info | `country` (name), `FAX`, `POSTAL` address | `countryCode`, the other contact types and addresses are mapped |
  | Field | `activeBoundaryId` | AgmaSync has no current boundary. A field reference would undo [ADR 12](./architecture/12-boundary-references-field.md) and form a cycle with `fieldBoundary.field`. A flag on the boundary keeps the direction, but one per field cannot be enforced by the schema |
  | Field | `guidanceGroupIds` | guidance is outside MVP scope. When it comes in, a guidance group would likely reference its field, as a boundary does |
  | Field | `timeZone` | FarmSPT has a time zone, on the farm, not yet taken |
  | FieldBoundary | `headlands` | |
  | Boundary | `partyRoles` (e.g. `COLLECTOR`), GNSS survey metadata, `highDefinitionSourceLayerId` | |
  | Obstacle | `name`, `isPassable` | AgmaSync has only `kind` |
