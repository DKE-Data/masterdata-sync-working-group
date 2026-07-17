# ADR 05 - Solving stale reads

**Status:** Proposed

**Scope:** Preventing stale reads affecting masterdata sync processes

## Context

When multiple clients attempt to edit the same masterdata object concurrently, it is possible that one client will overwrite changes made by another client without being aware of it. This can lead to data inconsistencies and loss of important information.

Since we have a variety of reading processes (seeding, synchronization) that are not necessarily aware of each other, we need to have a cross-cutting mechanism to prevent stale reads / inform clients when that might be the case.

The simplest solution to this common issue is known as "Compare And Swap" or CAS. Every time a client wants to perform a write operation they have to provide a previous revision of the object they are trying to modify. In case if object has been modified in the meantime, the write operation will be rejected and the client will have to ensure to get the latest version of the object and re-apply their changes.

## Decision

We shall require CAS for write operations except following cases:
- object does not exist yet (create operation)
- object is being deactivated and is already deactivated (for deactivation idempotency)
- requested value for the object is exactly the same as the current value (there is no need to force client to resolve any conflicts, as the resulting value is expected to be the same)

We would use revision number as the CAS value, since it is already a monotonically increasing positive integer assigned solely by agrirouter and has the properties of total order and uniqueness within the object.

Revision that agrirouter has determined to be the current revision of the object will be returned to the client in the response of every write operation. Note that in situations when there was a mismatch on previous revision (see exceptions above), but the write operation is still considered successful, the response might contain a revision that is NOT simply the previous revision + 1, which means clients MUST always use the revision that is returned in the response.

The actual data store on the partner side (or in outbound integration done by agrirouter) might not be actually able to keep track of last agrirouter revision number, but it is still expected that such clients would be able to track that separately from their primary store (this mostly applies to outbound integrations which cannot propagate CAS revision down to the actual data store).

## Consequences

Special cases:
- when two clients attempt to create the same object concurrently, only one of them will succeed, and the other will receive a conflict error. The client that received the conflict error will have to fetch the latest version of the object and apply their changes or decide to discard the update. Even though this is not a CAS operation per se, it still constitutes a conflict.
- when client attempts to deactivate an object simultaneously with another client attempting to modify it, this is considered a conflict and only one of clients will succeed. Even though further deactivations are idempotent and their prior revision is ignored when object already deactivated - for the first deactivation attempt, CAS mechanism must be applied.

