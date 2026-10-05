# German farm: AgmaSync and ADAPT

Hofgut Sonnenberg near Springe, Lower Saxony, as delivered by AgmaSync
([agmasync-sample.json](agmasync-sample.json)) and as an ADAPT 2.0.2 catalog ([adapt-sample.json](adapt-sample.json)).

- A GmbH owns the farm. Two partners hold roles on it: a contracting firm and
  an independent crop advisor (person). The firm comes from a system that does
  not record whether a party is a person or an organization, so it has no
  `details`.
- The farm manager, Dr. Katharina Brandt, privately owns the one field.
- The field has a single administrative boundary with two obstacles.

Every AgmaSync domain attribute is mapped. Of the envelope, only identity
is carried, the rest being sync state. Where ADAPT has no slot, the value
goes into a context item defined in `catalog.customDataTypeDefinitions`
(codes prefixed `AgmaSync-`).
Group items such as `AgmaSync-Partner` carry their value in nested context items.
Definitions that hold a reference say so in `keywords` (`reference:party`,
`reference:organization`, `reference:season`).

## Choices in this example

- **Field area**: nominal area has no exact ADAPT equivalent. `arableArea` was
  chosen over the standard `ReportedArea` context item.
- **Standard definitions** cover only the enumerations used here (`PartyType`,
  `TelecommunicationContactType`, `AddressContactType`, `BoundaryCreationMethod`,
  `IdType`). None of the standard context items fit an AgmaSync
  attribute. `GS1-GLN` (farm, field) would, if AgmaSync carried one.

## Option F

[option-f-sample.json](option-f-sample.json) is the same farm under
[ADR 11](../architecture/11-entity-model.md#option-f-in-detail) option F, as
delivered to one endpoint, in ADAPT 2.0.2.

- Each object is an AgmaSync envelope with an ADAPT component in `adapt`.
  The receiving endpoint's own id is `adapt.id.referenceId`, in place of the
  envelope's `local_id`. `uniqueIds` is not used.
- Where ADAPT has no slot, the value is a property `-agmasync-` plus the
  AgmaSync attribute name in camelCase, with the AgmaSync type. The attributes of a party's
  `details` are named directly, since `partyTypeCode` carries `party_type`.
  Prefixed properties also appear on nested ADAPT objects (obstacle `-agmasync-kind`).
- Owner becomes a Grower object of its own, for the farm (`GRW-0001`) and for
  the field (`GRW-0002`). The field has no native slot, so it references its
  grower as `-agmasync-growerId`, like a native reference with its canonical id.
- Farm address and geo reference are prefixed, not modelled as an extra Party
  as in the ADAPT sample.
  The farm address reuses ADAPT's type, as `-agmasync-addressContactMethods`.
- Native references (`growerId`, `partyId`, `farmId`, `fieldId`) hold the
  receiving endpoint's `local_id`, each with the target's `agrirouter_id` in
  `-agmasync-canonical` plus the ADAPT name. Prefixed AgmaSync attributes keep
  `EntityReference`.
- Another endpoint has just created the 2025/2026 season and assigned the
  boundary to it. The receiver has not bound the season yet: the season
  arrives without `id`, and the boundary carries the season's
  `agrirouter_id` in `-agmasync-canonicalSeasonIds`, with no `seasonIds`.
- The boundary's harvest period becomes that Season, since ADAPT has
  `seasonIds` on boundaries. The field's stays inline in
  `-agmasync-harvestPeriod`, since an ADAPT Field has no Season slot.
