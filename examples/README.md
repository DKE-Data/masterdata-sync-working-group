# German farm: AgmaSync and ADAPT

Hofgut Sonnenberg near Springe, Lower Saxony, as delivered by AgmaSync
([agmasync-sample.json](agmasync-sample.json)) and as an ADAPT 2.0.2 catalog ([adapt-sample.json](adapt-sample.json)).

- A GmbH owns the farm. Two partners hold roles on it: a contracting firm and
  an independent crop advisor (person). The firm comes from a system that does
  not record whether a party is a person or an organization, so it has no
  `details`.
- The farm manager, Dr. Katharina Brandt, privately owns the one field.
- The field has a single administrative boundary with two obstacles.

Every AgmaSync attribute is mapped. Where ADAPT has no slot, the value goes into
a context item defined in `catalog.customDataTypeDefinitions` (codes prefixed
`AgmaSync-`). Group items such as `AgmaSync-Partner` carry their value in nested
context items. Definitions that hold a reference say so in `keywords`
(`reference:party`, `reference:organization`, `reference:season`).

## Mapping

| AgmaSync | ADAPT |
| --- | --- |
| `agrirouter_id` | `id.referenceId` and `id.uniqueIds[]` (`UUID`, source `https://agrirouter.com`) |
| `local_id` | `id.uniqueIds[]` (`STRING`, source = the receiving application) |
| `type` | the catalog collection |
| `active`, `tenant_id`, `source_endpoint_id` | `AgmaSync-Envelope` { `-Active`, `-TenantId`, `-SourceEndpointId` } |
| `revision`, `modified_at` | `AgmaSync-Envelope` { `-Revision` }, with `modified_at` as its `MODIFICATION` time scope |
| Party `name` | `party.name` |
| Party `details.party_type` | `party.partyTypeCode` (`ORGANIZATION` / `INDIVIDUAL`; no `details` is `UNKNOWN`) |
| Party `details.commercial_registry_number` | `AgmaSync-CommercialRegistryNumber` |
| Party `details.title`, `first_name`, `last_name` | `AgmaSync-Title`, `-FirstName`, `-LastName` |
| Party `details.memberships[]` | `AgmaSync-Memberships` { `AgmaSync-Membership` { `-MemberOrganizationId`, `-MemberRole` } per entry } |
| Party `address` | `contactInfo.addressContactMethods[]` (`PHYSICAL`); `po_box` as `AgmaSync-AddressPoBox` on `contactInfo` |
| Party `billing_address` | `AgmaSync-BillingAddress` on `contactInfo`, nested `AgmaSync-Address*` |
| Party `contact.phone` / `mobile` / `email` | `telecommunicationContactMethods[]` `FIXED_PHONE` / `MOBILE_PHONE` / `EMAIL` |
| Party `tax_number`, `tax_id`, `trade_id` | `AgmaSync-TaxNumber`, `-TaxId`, `-TradeId` |
| Farm `name` | `farm.name` |
| Farm `owner` | `farm.growerId` → `grower` (1:1 with the owner party, linked by `grower.partyId`) |
| Farm `specialised_usage_type` | `AgmaSync-SpecialisedUsageType` |
| Farm `partners[]` | `AgmaSync-Partners` { `AgmaSync-Partner` { `-PartnerPartyId`, `-PartnerRole` } per entry } |
| Farm `address` | `AgmaSync-Address`, nested `AgmaSync-Address*` |
| Farm `geo_reference` | `AgmaSync-GeoReference` (WKT `POINT`) |
| Field `name`, `farm` | `field.name`, `field.farmId` |
| Field `area` (m²) | `field.arableArea` (ha) |
| Field `owner` | `AgmaSync-FieldOwnerPartyId` |
| Field `soil.type`, `soil.rating_points` | `AgmaSync-SoilType`, `-SoilRatingPoints` |
| Field `topography` | `AgmaSync-Topography` (`arcdeg`) |
| Field `field_boundaries[]` | inverted: `fieldBoundary.fieldId` |
| Field `harvest_period` | `season`, referenced by `AgmaSync-SeasonId` (ADAPT Field has no `seasonIds`) |
| FieldBoundary `harvest_period` | `fieldBoundary.seasonIds[]` → `season` |
| `harvest_period` `label`, `valid_from`, `valid_to` | `season.name`, `start` (00:00Z), `end` (23:59:59Z) |
| FieldBoundary `boundary` | `boundary.geometry` (WKT) |
| FieldBoundary `creation_method` | `boundary.boundaryCreationMethodCode` |
| FieldBoundary `boundary_type`, `regulatory_requirements` | `AgmaSync-BoundaryType`, `-RegulatoryRequirement` |
| FieldBoundary `obstacles[]` | `obstacles[]`: geometry as `boundary.geometry`, `kind` as `AgmaSync-ObstacleKind` |
| `metadata` | `AgmaSync-MetadataEntries` { `AgmaSync-Metadata` { `-MetadataKey`, `-MetadataValue` (JSON) } per key } |

## Gaps and choices

- **Grower** maps 1:1 to the farm owner party.
- **FieldBoundary `name`** is required by ADAPT and absent in AgmaSync. It is
  derived from the field name and season.
- **Obstacle geometry**: ADAPT describes an obstacle boundary as a Polygon or
  MultiPolygon. The pole is a WKT `POINT`, which the schema accepts (it is a plain
  string) but which departs from that description.
- **Party `details`** maps to `partyTypeCode`: an organization to `ORGANIZATION`,
  a person to `INDIVIDUAL`, a party without `details` to `UNKNOWN`. Going the
  other way, `BUSINESS`, which ADAPT supersedes by `ORGANIZATION`, also becomes
  an organization.
- **Grower without a Party** becomes a party without `details`, named by the
  Grower.
- **Field area**: nominal area has no exact ADAPT equivalent. `arableArea` was
  chosen over the standard `ReportedArea` context item.
- **Standard definitions** cover only the enumerations used here (`PartyType`,
  `TelecommunicationContactType`, `AddressContactType`, `BoundaryCreationMethod`,
  `IdType`, `DateContext`). None of the standard context items fit an AgmaSync
  attribute. `GS1-GLN` (farm, field) would, if AgmaSync carried one.
