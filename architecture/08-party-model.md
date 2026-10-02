# ADR 08 - Parties, membership, and partners

- **Status:** WIP
- **Scope:** How AgmaSync models the people and businesses behind farms - who holds
  land, who belongs to an organization, and who works a farm for someone else

## Context

Land is held by businesses and by individuals, and the same actor appears in
several guises: a contractor is a business to its clients and a customer to its
own subcontractors, a farmer is simultaneously a person and a farm business,
and a farm is worked by people who neither own it nor belong to its owner.

A model that names these guises as types forces an actor into one of them.
A model that expresses them as relations does not.

Many source systems do not record whether a party is a person or an organization
at all. ISOXML's Customer, FarmSPT's Customer/Grower and ADAPT's Grower carry a
name and contact data, nothing more. Such a system cannot tell which of two party types
it holds, and any guess it makes will disagree with a system that does know.

## Decision

**There is one party entity type.** A party carries a `name` and the contact and
fiscal block, because a farmer has a tax number and a trade id exactly as a
company does.

**Whether a party is a person or an organization is optional.** It is carried in
`details`, discriminated by its `party_type`:

- `PERSON`: `title`, `first_name`, `last_name`, `memberships`
- `ORGANIZATION`: `commercial_registry_number`

A party without `details` is of unknown party type. A participant that does not
distinguish never sends `details`, and on update its absence keeps what another
participant stated. One that files such a party under a party type of its own, or
lets its user pick one, states that party type to every participant on its next
write.

**A member is a person holding a role in an organization.** Membership is state
on the person, expressed as a list inside its `details`, so only a person can
carry one and one agronomist advising three organizations is one canonical
person rather than three copies. The target of a membership must be a party whose
`details` is an `ORGANIZATION`. agrirouter checks this on write, and rejects a
change or removal of party type on an organization that persons still name.

**A partner is a party holding a role on a farm** - the contractor that works it,
the advisor that reads it. Partnership is state on the farm, also a list.

Membership and partnership are the same idea at two attachment points, and both
draw their role from the [ADAPT Role](https://adaptstandard.org/dtd.html) list.

**A farm is owned by a party**, of any party type.

There is no attribute anywhere declaring that a party *is* a contractor or *is* a
customer. Those are relations, read off the graph.

```mermaid
classDiagram
    direction TB
    class Party {
        +name
        +details : Details or absent
        +address
        +contact
        +billing_address
        +tax_number
        +tax_id
        +trade_id
    }
    class Details {
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
        +owner : Party
        +specialised_usage_type
        +partners
    }
    class Partner {
        +partner_id : Party
        +partner_role : ADAPT Role
    }
    Party "1" *-- "0..1" Details : details
    Details <|-- PersonDetails
    Details <|-- OrganizationDetails
    PersonDetails "1" *-- "0..n" Membership : memberships
    Party "1" o-- "0..n" Farm : owns
    Farm "1" *-- "0..n" Partner : partners
```

The reference properties `organization_id`, `owner` and `partner_id` are named inside
the boxes rather than drawn as edges in order not to clutter the diagram.

## Scenarios

Arrows below mean *holds a reference to*.

### Scenario A: a farmer working their own land

```mermaid
flowchart LR
    B["FieldBoundary"] -->|"field"| FI["Field \n Long Meadow"]
    FI -->|"farm"| F["Farm \n Manor Farm"]
    F -->|"owner"| P["Party \n Sarah Ashcroft \n person"]
```

No organization, no membership, no partner. The person is the business.

### Scenario B: a farm business with several farms and staff

```mermaid
flowchart LR
    F1["Farm \n Manor Farm"] -->|"owner"| O["Party \n Ashcroft Farms Ltd \n organization"]
    F2["Farm \n Hill Farm"] -->|"owner"| O
    P1["Party \n Sarah Ashcroft \n person \n role: FARM_MANAGER"] -->|"organization_id"| O
    P2["Party \n James Ashcroft \n person \n role: OPERATOR"] -->|"organization_id"| O
```

Two farms, one owner, two people carrying their roles on themselves.

### Scenario C: a contractor working someone else's land

```mermaid
flowchart LR
    F["Farm \n Manor Farm"] -->|"owner"| O["Party \n Ashcroft Farms Ltd \n organization"]
    F -->|"partner CUSTOM_SERVICE_PROVIDER"| M["Party \n Brookfield Contracting Ltd \n organization"]
    F -->|"partner CROP_ADVISOR"| S["Party \n Wessex Agronomy \n organization"]
```

The contractor is directly referenced by the farm it works on.

### Scenario D: an actor in several roles at once

```mermaid
flowchart LR
    FM["Farm \n Brookfield Home Farm"] -->|"owner"| M["Party \n Brookfield Contracting Ltd \n organization"]
    FW["Farm \n Manor Farm"] -->|"partner CUSTOM_SERVICE_PROVIDER"| M
    FM -->|"partner CUSTOM_SERVICE_PROVIDER"| S["Party \n Fenland Harvesting Ltd \n organization"]
```

Brookfield owns land, works Ashcroft's land, and hires Fenland for its own harvest. All
three hold simultaneously. No single attribute on Brookfield could have carried them.

### Scenario E: a customer of unknown party type

```mermaid
flowchart LR
    F["Farm \n Müller Hof"] -->|"owner"| P["Party \n Müller \n no details"]
```

A system that keeps only a customer name creates the party without `details`.
Another participant that knows Müller is a person adds `details` later. The
first system's updates omit `details` and leave it in place. Both hold the same
canonical party throughout.

## Consequences

- Entity types are `party`, `farm`, `field`, `fieldBoundary`.
- `details` merges like any nested object while its `party_type` is unchanged. A write
  that changes `party_type` replaces `details` whole, so no attribute of the other party
  type survives.
- A membership references a party of the same entity type. Delivery order is
  therefore per object, not per type: parties with person `details` follow all
  other parties (see [ADR 07](07-sync-streaming.md)). A participant sends in the
  same order - parties without memberships first.
- A person may own a farm, and a member is an ordinary person, so a farm manager
  who also farms privately is expressible.
- A field may name an owner of its own, for systems that attribute fields to a
  party directly rather than only through the farm. It falls back to the farm's
  owner when absent, so the common case stays a single statement of ownership
  while a field held apart from the farm managing it remains expressible.
- Membership and partner entries are embedded and carry no `agrirouter_id`. They
  are revised with the object holding them: changing who works for an
  organization revises the person, changing which contractor works a farm revises
  the farm. In both cases the object whose own record changed.
- An organization's roster is therefore not visible from the organization alone -
  it is assembled from the persons pointing at it.
- Receiving a farm discloses its partners, which is information about third
  parties who are not themselves party to that exchange.

## Notes on naming and placement

**`Party` is ADAPT's term.** ADAPT defines a party as a business entity or an
individual, carrying a required party type code: `ORGANIZATION`, `INDIVIDUAL` or
`UNKNOWN`. AgmaSync takes the concept, the name and the three party types, with two
departures: it says **person** where ADAPT says *individual*, and it expresses
`UNKNOWN` as the absence of `details`, so a participant that does not record the
party type has nothing to send. A Grower without a Party maps to a party without
`details` as well. The role vocabulary on memberships and partners is ADAPT's as
well.

**`specialised_usage_type` sits on the farm.** FarmSPT places it on Customer/Grower.
A production orientation describes what is grown where, and one party may run an
arable farm and a dairy farm at the same time, so the farm is the narrower and
more accurate holder. This is a deliberate departure from the source document.

## Partners do not grant access

`partners` records a business relationship. It MUST NOT be read as granting
visibility of the farm or of anything below it. Who receives what is decided by endpoint opt-in and routing, before anything is
sent.

A participant receiving a farm MUST NOT translate its `partners` entries into
access grants in its own system without a separate decision by its user.
