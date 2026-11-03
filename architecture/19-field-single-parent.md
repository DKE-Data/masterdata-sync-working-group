# ADR 19 - A field has one parent

- **Status:** WIP
- **Scope:** Supersedes the field's own grower of [ADR 08](./08-party-model.md)
  and [ADR 11](./11-entity-model.md), and the "any other combination" of
  [ADR 13](./13-optional-references.md). The rest of those ADRs stands.

## Context

A field could name a farm and a grower, the grower taking precedence. ADAPT
removed exactly this: a parent named at two levels can disagree
([ADAPT #38](https://github.com/AgGateway-ADAPT/Standard/issues/38)), and one
use case gets one model, so a source without farms creates them
([ADAPT #66](https://github.com/AgGateway-ADAPT/Standard/issues/66),
`field.farmId` in ADAPT 2.0.2).

## Decision

- A field names its farm only. Its grower is the farm's grower.
- A system that records a field's party but no farm for it writes one farm per
  party on its first field of that party. The farm should be named after the party and have its own local identifier.
- A customer or client SHOULD be a party.
- Declaration and selection are closed over `parties + fields → farms`.

## Rejected alternatives

| Alternative | Why not |
|---|---|
| Keep both references on the field, its own grower taking precedence over the farm's | A field can then name grower A while its farm names grower B, and nothing says which is right. Every receiver has to check both. And when a field moves onto a farm of its own grower, the sender has to remember to remove the field's grower, or the field keeps the old one even when the farm changes hands. |
| Also require a grower on every farm, as ADAPT's committee does in practice | A system without parties, such as a terminal, knows no owner and would have to make one up. ADR 13 already rejects such placeholder parties. The farm this ADR asks for is different: its owner is the customer the system knows. |
| Let agrirouter reject a field without a farm when the endpoint exchanges parties | A field whose customer is not known yet legitimately has no farm. agrirouter cannot tell it apart from a field whose system forgot to write the farm. |

## Consequences

- A system with parties and fields only also writes, selects and reads farms.
  A party with several farms is still one customer to it.
- A field held by another party than its farm's grower cannot be expressed.
