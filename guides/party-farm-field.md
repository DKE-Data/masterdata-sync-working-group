# Guide: parties, farms and fields

This guide is not normative but is meant as guidance for how a system whose own model differs from
`party ← farm ← field` can map onto it. The rules are in
[specification.md](../specification.md#field) and
[ADR 19](../architecture/19-field-single-parent.md).

## The model

A field names its farm (`_agmasync_farmRef`). A farm names its grower, a party
(`_agmasync_growerRef`). A field's grower is its farm's grower. Every reference
is optional.

| Your system has | Write | Select |
|---|---|---|
| customers, farms, fields | party, farm, field | party, farm, field |
| farms, fields | farm, field | farm, field |
| customers, fields | party, a farm per customer, field | party, farm, field |
| fields | field | field |

"Customer" stands for whatever your system calls the holder of the land, also known as client, owner or grower.

## Customers and fields, no farms

Write each customer as a party. On the first field of a customer, write a farm
for it, as the specification requires. Show farms to your user or not, as you
like.

Receiving:

- A field's customer is its farm's grower. Select farms to know it.
- A farm without a grower names no customer. Do not turn the farm into a
  party. Let your user pick a customer once and write it as the farm's
  `_agmasync_growerRef`. It then applies to all of that farm's fields.
- A field without a farm, or on a farm without a grower, has no customer. If
  your system requires one, ask your user.

Placing a field:

- A new field of a customer goes to the farm you created for that customer.
  If you have none and the party has exactly one farm, use that one.
- If the party has several farms, none of them yours, let your user choose,
  or pick the party's most recently modified farm. Creating a farm instead
  works, but adds a farm named after the customer next to the real ones.
- A field your user moves to another customer moves to a farm of that
  customer, chosen the same way.
- Do not deactivate a farm you created once it is empty. Others may maintain
  it. A system that shows farms can deactivate it.

## Farms and fields, no parties

Write farms without `_agmasync_growerRef`. Do not invent a party. A received
farm's grower is a reference to a type you do not select: ignore it and leave
it out of your writes.

## Customers written as farms

Possible, but to everyone else your system then models farms and fields only.
Party data does not reach you, and a party with several farms becomes several
customers. Write customers as parties instead.
