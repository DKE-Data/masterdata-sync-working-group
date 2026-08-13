# ADR 05 - Solving stale reads

**Status:** Proposed

**Scope:** Preventing stale reads affecting masterdata sync processes

## Context

When multiple clients attempt to edit the same masterdata object concurrently, it is possible that one client will overwrite changes made by another client without being aware of it. This can lead to data inconsistencies and loss of important information.

Since we have a variety of reading processes (seeding, synchronization) that are not necessarily aware of each other, we need a cross-cutting mechanism that rejects writes based on a stale read and tells the client that this is what happened.

The simplest solution to this common issue is known as "**Compare And Swap**" (**CAS**). Every time a client wants to perform a write operation they have to provide a previous revision of the object they are trying to modify. In case the object has been modified in the meantime, the write operation will be rejected and the client will have to get the latest version of the object before re-applying their changes.

## Decision

We shall require CAS for write operations except following cases:
- object does not exist yet (_create_ operation)
- object is being or is already deactivated (for deactivation idempotency)
- requested value for the object is exactly the same as the current value (there is no need to force client to resolve any conflicts, as the resulting value is expected to be the same)
- agrirouter was able to resolve the conflict automatically via three-way merge [see Automatic conflict resolution](#automatic-conflict-resolution)

We would use revision number as the CAS value, since it is already a monotonically increasing positive integer assigned solely by agrirouter and has the properties of total order and uniqueness within the object.

The revision which agrirouter determined to be the current one for the object will be returned to the client in the response of every write operation. Note that in situations when there was a mismatch on previous revision (see exceptions above), but the write operation is still considered successful, the response might contain a revision that is NOT simply the previous revision + 1, which means clients MUST always use the revision that is returned in the response.

A partner's primary store often has nowhere to put the agrirouter revision - this is typical of outbound integrations, where the revision cannot be propagated down to the actual data store. Such clients are still expected to keep the last known revision per object, alongside their primary store rather than inside it.

```mermaid
flowchart TB
    A["Client A<br/>last known revision 8"]
    B["Client B<br/>last known revision 8"]
    C["agrirouter<br/>current revision 8->9"]
    A -->|"writes with revision 8"| C
    B -->|"writes with revision 8"| C
    C -->|"rejects B's write<br/>with conflict error"| B
    %% ceasg:{"id":"h0g1x7i7"} %%
    %% mermaid-flow:pos A=102,59 B=389,62 C=239,-97
```

### Automatic conflict Resolution


Given f.e such two writes that were done at approximately same time:

`PUT /masterdata/field-boundaries/123`

```json
{
    "harvestPeriod": {
        "validFrom": "2024-01-01",
        "validTo": "2024-12-31"
    },
    ///... all other fields are same as in the current revision
}
```

`PUT /masterdata/field-boundaries/123`

```json
{
    "boundary": {
        "type": "Polygon",
        "coordinates": [
            [
                [10.0, 50.0],
                [10.0, 51.0],
                [11.0, 51.0],
                [11.0, 50.0],
                [10.0, 50.0]
            ]
        ]
    },
    ///... all other fields are same as in the current revision
}
```

, which might have been done concurrently by two different clients or semi-concurrently (f.e if one of systems lags on receiving the latest revision from sync stream or sync stream is down).

One of changes might be rejected by CAS, which would potentially result in data loss in case if system that was rejected was unable to surface the conflict to the user (f.e change coming from outbound integration such as John Deere where we cannot control the path of user storing the data).

However, in this example the two changes are technically not conflicting directly, they only conflict on the revision number and given that agrirouter sees the full object for all its revisions (at different times), it may be able to apply three way merge conflict resolution.

This is approximate example of how it might work (actual implementation could be different):

```mermaid
flowchart TB
    subgraph sub1 ["PUT request"]
        ClientBSentRepresentation["Client B:<br/>Representation B"]
        A("Reference to revision 8")
    end
    BaseRevision["Base Revision:<br/>revision 8"]
    ClientASentRepresentation["Client A:<br/>Representation A"]
    Revision9["Revision 9 <br/>after applying Client A changes"]
    PatchA["`Patch A:<br/>Base -> Client A`"]
    PatchB["Patch B:<br/>Base -> Client B"]
    MergedRevision["Merged Revision:<br/>revision 10"]
    BaseRevision -->|"Client A changes"| Revision9
    ClientASentRepresentation -->|"Client A changes"| Revision9
    Revision9 -->|"diff"| PatchA
    BaseRevision -->|"diff"| PatchA
    ClientBSentRepresentation -->|"diff"| PatchB
    BaseRevision -->|"diff"| PatchB
    PatchB <-->|"check for conflicts"| PatchA
    Revision9 --> MergedRevision
    PatchB -->|"when no conflict:<br/>apply Patch B to Revision 9"| MergedRevision
    A --> BaseRevision
    %% ceasg:{"id":"2fpch1lu"} %%
    %% mermaid-flow:pos BaseRevision=583,-32 ClientASentRepresentation=302,-6 ClientBSentRepresentation=827,248 Revision9=367,113 PatchA=333,378 PatchB=637,375 MergedRevision=571,583 A=759,162
    %% mermaid-flow:gpos sub1=718,71,207,227
```


## Rejected alternatives

### Conflict-free replicated data types (CRDTs)

We considered modelling master-data objects as **conflict-free replicated data types
(CRDTs)**, which would merge concurrent edits automatically and would decouple write
acceptance from sync liveness (with CAS, a client that cannot reach agrirouter cannot
learn the fresh revision it needs to write). We reject them because for the fields
that carry an object's actual meaning - a boundary polygon, a name, a status - there
is no meaningful automatic merge, so a CRDT degrades to silently discarding one edit,
which is exactly the loss this ADR exists to prevent, only harder to notice. They also
impose per-object causal metadata (version vectors, tombstones) on every partner and
on the wire, where CAS needs only a single integer. Using CRDTs *inside* a
partner's own store remains a fine implementation choice.

## Consequences

Special cases:
- when two clients attempt to create the same object concurrently, only one of them will succeed, and the other will receive a conflict error. The client that received the conflict error will have to fetch the latest version of the object and apply their changes or decide to discard the update. Even though this is not a CAS operation per se, it still constitutes a conflict.
- when a client attempts to deactivate an object simultaneously with another client attempting to modify it, this is considered a conflict and only one of the clients will succeed. Even though further deactivations are idempotent and their prior revision is ignored when the object is already deactivated, for the first deactivation attempt, CAS mechanism must be applied.

