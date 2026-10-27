# ADR 16 - Replace person's membership with organization, remove farm partners

- **Status:** WIP
- **Scope:** Supersedes the memberships and farm partners of
  [ADR 08](./08-party-model.md). The rest of ADR 08 stands.

## Context

ADR 08 gave a person a list of memberships, each an organization with a role,
and a farm a list of partners, each a party with a role. Both come from FarmSPT.
ADAPT has neither in its master data: a party has one role-less
`parentPartyId`, and `PartyRole` appears only on operations and boundaries.

## Decision

- **No farm partners.** `_agmasync_partners` is removed without replacement.
- **A person names at most one organization**, such as its employer, in
  `_agmasync_details.parentOrganizationRef`. It carries no role, and its target
  must be a party whose `_agmasync_details` is an `ORGANIZATION`.

This is ADAPT's `parentPartyId`, narrowed: only a person carries one, and only
an organization is named. The party graph therefore stays two levels deep and
acyclic by construction, with no cycle check on write and a fixed delivery tier
per party type ([ADR 07](./07-sync-streaming.md)). The name says
*organization* so it is not read as ADAPT's any-party parent.

## Consequences

- Not expressible: a person in several organizations, the role a person holds
  there, and a contractor or advisor working a farm.
- Receiving a farm no longer discloses third parties.
- The ADAPT Role list is no longer used.
- A farm keeps `_agmasync_address`. Moving it to a party of the farm's own,
  ADAPT's `Farm.partyId`, is rejected: a participant that keeps an address on
  the farm but models no persons or organizations would have to model parties
  and select the party entity type, only to carry that address.
