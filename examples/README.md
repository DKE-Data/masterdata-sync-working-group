# German farm: AgmaSync and ADAPT

Hofgut Sonnenberg near Springe, Lower Saxony, as delivered by AgmaSync
([agmasync-sample.json](agmasync-sample.json)) and as an ADAPT 2.0.2 catalog ([adapt-sample.json](adapt-sample.json)).

- A GmbH owns the farm. Two partners hold roles on it: a contracting firm and
  an independent crop advisor (person). The firm comes from a system that does
  not record whether a party is a person or an organization, so it has no
  `details`.
- The farm manager, Dr. Katharina Brandt, privately owns the one field.
- The field has a single administrative boundary with two obstacles.

Every AgmaSync domain attribute is mapped by the normative
[AgmaSync–ADAPT mapping](../adapt-mapping.md). Of the envelope, only identity
is carried, the rest being sync state. Where ADAPT has no slot, the value
goes into a context item from [adapt-definitions.json](../adapt-definitions.json),
copied into `catalog.customDataTypeDefinitions` (codes prefixed `AgmaSync-`).
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
