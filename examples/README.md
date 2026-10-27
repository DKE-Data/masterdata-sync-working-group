# Sample farm

Hofgut Sonnenberg near Springe, Lower Saxony.

- A GmbH owns the farm.
- Its employee, Dr. Katharina Brandt, privately owns the one field. Her
  `parentOrganizationRef` names the GmbH.
- The field has a single administrative boundary with two obstacles.

## The wire format

[agmasync-current-sample.json](agmasync-current-sample.json) is the farm as delivered to one
endpoint, in the encoding of [ADR 11](../architecture/11-entity-model.md)
option F.

- Each object is an envelope of sync state with its master data in `content`,
  based on ADAPT 2.0.2.
- `content.id` carries the agrirouter id (`idSource` `https://agrirouter.com`) and
  the receiving endpoint's local id, repeated in `referenceId`. References are `_agmasync_<name>Ref` objects
  with the same `id`, in place of ADAPT's `<name>Id` strings.
- Where ADAPT has no slot, the value is a property `_agmasync_` plus the
  AgmaSync attribute name in camelCase, with the AgmaSync type. Prefixed
  properties also appear on nested ADAPT objects (obstacle `_agmasync_kind`).
- `_agmasync_growerRef` on the farm and on the field names a party. There is
  no Grower object.
- Another endpoint has just created the boundary, and the receiver has not
  bound it yet: its `id` carries only the agrirouter entry, with no
  `referenceId`.

## ADR 11 comparison

These two files predate [ADR 16](../architecture/16-person-organization.md)'s change
from memberships and farm partners to `parentOrganizationRef`, and keep the model ADR 11
compared.

The same farm as AgmaSync's FarmSPT-derived model
([agmasync-old-sample.json](agmasync-old-sample.json)) and as an ADAPT 2.0.2 catalog
([adapt-old-sample.json](adapt-old-sample.json)), the inputs to ADR 11's analysis.

Every AgmaSync domain attribute is mapped. Of the envelope, only identity
is carried, the rest being sync state. Where ADAPT has no slot, the value
goes into a context item defined in `catalog.customDataTypeDefinitions`
(codes prefixed `AgmaSync-`).
Group items such as `AgmaSync-Partner` carry their value in nested context items.
Definitions that hold a reference say so in `keywords` (`reference:party`,
`reference:organization`, `reference:season`).

- **Field area**: nominal area has no exact ADAPT equivalent. `arableArea` was
  chosen over the standard `ReportedArea` context item.
- **Standard definitions** cover only the enumerations used here (`PartyType`,
  `TelecommunicationContactType`, `AddressContactType`, `BoundaryCreationMethod`,
  `IdType`). None of the standard context items fit an AgmaSync
  attribute. `GS1-GLN` (farm, field) would, if AgmaSync carried one.
