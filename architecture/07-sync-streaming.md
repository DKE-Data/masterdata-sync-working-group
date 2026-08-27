# ADR 07 - Sync streaming

- **Status:** WIP
- **Scope:** Delivery of canonical master data to partner applications - the store
  that backs delivery, the connection unit, catch-up, and resume

## Context

Delivery has to do two things without either being a special case: hand a
returning application everything that changed while it was away, and hand a new
application everything there is. [ADR 06](./06-initial-load.md) describes the
state machine around the second case. This ADR describes the mechanism implementing both.

### Queue based alternative

An alternative version considered is a persisted per-endpoint queue: opting
an entity type in wrote the canonical set into that endpoint's queue as ordinary
events, and `Last-Event-ID` was the only position an endpoint ever kept. That
construction has two failures, and both are structural rather than tuning
problems.

**The retention cliff.** A queue accumulates, so it has to be trimmed; trimming
evicts positions, and an application offline long enough has to reload from
scratch. Worse, the reload is itself written into the queue, so a set large enough
to outlive its own retention triggers a reload that materializes the set again -
a loop with no fixed point.

**Write amplification.** A queue holds a copy per recipient. Every change to a
canonical object was written once into each entitled endpoint's queue, and each
tenant's canonical set was copied again whenever one of that tenant's endpoints
was opted into a type. Storage therefore scaled with changes multiplied by
recipients, for data that exists once. A partner application holding 100k endpoints held 100k queues, each with its own retention to
trim and its own position to track.

## Decision

### Change number

First of all we introduce a concept of change number which is simply monotonically
increasing number that is assigned to every change to canonical masterdata store.
It would have following properties:
- **Uniqueness** - each change number is unique across all changes
- **Monotonicity** - change number given to writes (creates AND updates) created "later" is always greater than change number given to events created "earlier", which creates a total order for all changes and corresponding notion of time
- **Not Generally Linear** - change numbers are allowed to have gaps in the sequence.

### Delivery reads a compacted index, not a log

agrirouter maintains one record per canonical object, never one per change. Each
carries the object's identity, its entity type, the endpoint whose change produced
the current value, and a globally ordered `last_change_number` that is rewritten every
time the object changes. However `create_change_number` is never rewritten and only assigned
one time when the object is being created for the first time.

No change is ever given a number below one the application has already received,
and the number advances per record, so a cursor may stop anywhere and nothing is
silently skipped.

Because there is one record per object rather than one per change, what agrirouter
holds is proportional to the amount of master data in existence, not to how much
of it has ever been edited, and records are **not evicted**. The system cannot be in a state
which is too far behind to resume: an application away for 5 minutes and one
away for 5 weeks are served the same way, and the second merely gets more
back (assuming more objects changed in the meantime), while if an object has changed
50 times in that period, only the last known state is delivered, which is all the
application needs to know.

This means we do not support intermediate states. An object edited three times while an
application was disconnected is delivered once, carrying its current value; the
application never learns that the two earlier edits happened. That is consistent
with what this protocol
[declares itself not to be](../specification.md#what-this-protocol-is-and-is-not) -
a history store - and it is cheaper than replaying the intermediate
values only to overwrite them.

It also supports deletion at no additional cost. Because agrirouter
[deactivates rather than removes](../specification.md#deactivation), a deleted
object keeps its record, is marked inactive and takes a new `last_change_number`, so
deletions ride the same path as everything else. A design built on hard deletes
would need a second retention clock for tombstones purely so that catch-up could
express what had disappeared.

### One stream per app, tenant in the envelope

The connection unit is the **application**, not the endpoint. Tenant identity
travels in the event envelope, so a partner serving 100k farmers holds one
connection and one position rather than 100k of each, and server-side delivery
state is proportional to the number of applications.

Which tenants an application may read is decided by the hub
routes the user created ([ADR 04](./04-routing.md)), and is applied as a predicate on
the query rather than as a property of the connection.

Opt-in per entity type is not a second predicate alongside it. Opt-in is held per
endpoint ([Routing and opt-in](../specification.md#routing-and-opt-in)), and each
of an application's endpoints belongs to a different tenant, so two tenants on the
same stream routinely differ - one exchanging farms and fields, the other only
farms. The filter is therefore per tenant and type, read from each tenant's own
`masterdata-config`. An application MUST NOT assume the types it receives for one
tenant are the types it receives for another.

### The sweep delivers in tier order

A dependency is between two objects - this field references that farm - and the
sweep does not track them at that granularity. It orders by the type-level reference
graph instead: coarser, and enough, because an object can only reference objects of
the types its own type references, so an order that respects the type graph respects
every object dependency within it. That graph is fixed by the canonical model rather
than by the data, and each type sits at a fixed depth in it - its **tier**:

| tier | types | references |
| --- | --- | --- |
| 0 | `organization`, `fieldBoundary` | none |
| 1 | `person` | organizations, through memberships |
| 2 | `farm` | its owning party, its partner parties |
| 3 | `field` | its farm, its owning party, its boundaries |

Delivering every object of a tier before any object of the next places every parent
before every child, which is the whole point: an application can write each object
as it arrives, because everything that object references is already there.

A connection runs exactly one sweep. It covers every object the application is
entitled to read whose `last_change_number` is above `after` - the cursor the
application presented, exclusive - delivered in ascending `(tier, create_change_number)`.
On a first connection `after` is zero and the sweep is the whole entitled set.

`create_change_number` orders objects *within* a tier. A reference between two
objects of the same type - a field naming the field it was split from - stays inside
a tier, and creation order resolves it as long as such a reference is set when the
object is created: the ancestor exists first, so it carries the lower number. The
column also gives the scan a key nothing rewrites, and it is what lets the buffer
stop the scan (below).

The filter and the ordering deliberately read different columns: `last_change_number`
selects what the application is missing, `(tier, create_change_number)` puts it in
an order the application can apply as it arrives. Each object is delivered at its
current value, because the compacted index holds only that one.

**The scan is stable under concurrent writes**, which is what lets it run without
an upper bound. Neither sort column is ever rewritten, so no row moves under the
scan; `last_change_number` only rises, so a row that satisfies the filter cannot
stop satisfying it; and an object created while the sweep runs either lands ahead
of the scan within its own tier or falls in a tier the scan has already left - and
in both cases its creating change is in the buffer, because the subscription opened
first. Nothing is skipped, whatever happens during the pass. Where the sweep
*stops* is set by the buffer, below.

An object unchanged since `after` is outside the filter, so the sweep skips it even
when it delivers that object's children. The reference still resolves: unchanged
since the cursor means it was delivered before the cursor was written.

### Live changes are buffered until the sweep ends

A change landing while the sweep runs cannot go straight out. The sweep is walking
tier order, so a field created now would arrive before the farm it references,
which the sweep has yet to reach.

agrirouter therefore subscribes to live changes when the connection opens, holds
them in a **buffer** for the duration of the sweep, and flushes them once the sweep
ends. The whole sweep precedes the whole flush, so a parent delivered by the sweep
always precedes a child held in the buffer. After the flush the connection streams
live changes directly.

```mermaid
flowchart TB
    SUB["subscribe to live changes \n buffer everything that arrives"]
    SWEEP["sweep once, ascending (tier, create_change_number) \n last_change_number &gt; after"]
    MARK["emit CAUGHT_UP"]
    FLUSH["flush the buffer, same order as the sweep"]
    LIVE["stream live"]
    SUB --> SWEEP
    SWEEP --> MARK
    MARK --> FLUSH
    FLUSH --> LIVE
```

The buffer also tells the sweep where to stop. Because the subscription opens
first, every object created after it has its creating change in the buffer, so the
first change the buffer receives - the **`buffer_change_start_pin`** - is the
boundary: creation numbers below it belong to the sweep, at or above it are
buffered already. That boundary is that change's own number, drawn from the same
sequence as `create_change_number`, so the scan stops once the remaining objects
have creation numbers above the pin.

It bounds every tier rather than the sweep as a whole. Within a tier the scan runs
ascending `create_change_number`, so it leaves that tier at the pin and starts the
next one; an object created after the pin is skipped in whichever tier it belongs
to, and the flush delivers it.

An idle system produces no pin and needs none: with nothing being written the scan
reaches the end of each tier and stops. There is no case in between, because
appending to the index *is* a change - a system busy enough to keep the scan
finding new rows is busy enough to fill the buffer.

The buffer is **compacted like the index it shadows**: it is keyed by object
identity and holds only the latest change per object, so an object edited fifty
times during a sweep costs one entry. Two entries are dropped rather than sent:

- one superseded by a later change to the same object, which the key replaces;
- one for an object the sweep has already emitted at an equal or higher change
  number, which the sweep drops as it goes.

Both are the same rule - only the current value of any object is ever worth
sending - and both are cheap, because the buffer is keyed for exactly this lookup.

**The flush uses the sweep's order**, `(tier, create_change_number)`, rather than
change order. Change order is what compaction destroys: the buffer holds an object
at its *latest* change, which can fall after the creation of something that
references it. A farm created during the sweep, a field created against it, then a
second edit to the farm leaves the field at the earlier change and the farm at the
later one, so change order would flush the child first. The same happens a tier down,
between two fields, where only creation order separates them.

The live tail after the flush needs none of this: it carries one frame per change
rather than one per object, so a change that introduces a reference always follows
the one that created its target.

### The cursor is a single change number

The cursor is the change number of the last frame the application durably applied.
There is nothing else in it, in any state, and two cursors compare as the integers
they are.

**It does not advance while a sweep runs.** Sweep frames carry the change number
the sweep started from, because objects arrive in tier order and their change
numbers do not ascend with them - committing one would move the cursor by an
arbitrary amount in either direction. It starts advancing when the sweep and its
buffer flush are done, and from then on it tracks change order directly.

An interrupted sweep therefore starts again rather than resuming. It costs
redelivery of what it had already sent, which idempotent apply absorbs, and it
needs no second position to do it: `last_change_number > after` still selects every
object the first attempt delivered. A sweep contributes no position of its own
precisely so that it can be abandoned at any point.

agrirouter MUST put that number in the `id:` of every sweep frame rather than
omitting the field. SSE retains the last id it was given - the buffer is not reset
between events - so an omitted `id:` says the same thing to a client that follows
the spec. Sending it does not rely on that: a client that cleared its stored id
instead would reconnect without `Last-Event-ID` and be served as a first
connection, re-receiving everything it already holds.

The id remains **opaque**, and an application MUST NOT depend on it holding still
across a sweep. Carrying sweep progress in it is what would make an interrupted
sweep resumable, and that option is deliberately left open.

The value rides in `Last-Event-ID`, which SSE treats as opaque. Damage to it is
largely self-limiting - truncating or corrupting a decimal integer overwhelmingly
yields a smaller one, which costs redelivery rather than a gap - so agrirouter need
only reject a value above the current change number, and serve a rejected one as a
first connection.

### A cursor we cannot read is a first connection

An application that presents no `Last-Event-ID`, or one that fails validation, is
swept from zero.

Discarding the cursor is therefore how an application asks for every object it is
entitled to, again.

### A cursor is a position, not a record of entitlement

`after` says where in change order the application stopped. It does not say what
the application was allowed to see when it got there, and nothing else in the
cursor does either.

Data that existed below `after` but was not granted at the time is therefore
indistinguishable from data the application already holds. A tenant that grants
access today has objects created months ago: their change numbers sit below
`after`, so the filter excludes them, and they changed long ago, so the buffer
never sees them. Delivery has no way to tell that they are owed.

Recognising that they are owed, and getting them across, belongs to initial load
([ADR 06](./06-initial-load.md)).

### The cursor belongs to the application, not to agrirouter

agrirouter stores no position. It encodes the cursor into each frame's `id:`, the
application persists it, and on reconnect hands it back as `Last-Event-ID`.
Delivery is stateless in that respect: nothing on our side records where any
application has got to, which is what keeps a partner with 100k endpoints from
costing us 100k pieces of delivery state.

An application MUST persist a cursor only for objects it has **durably applied**,
never for objects it has merely received. Delivery is at-least-once: everything
after the committed position is sent again on reconnect, so a connection dropping
mid-sweep costs a redelivery rather than a gap.

### Parallel apply is fanned out behind the one socket

Entitlement is a predicate on the query rather than a property of the connection,
and the stream carries no scoping parameter, so a second connection would receive
the same frames as the first. An application that needs more throughput than one
consumer gives it therefore fans frames out to its own workers behind the single
socket, and that is the shape we support. Applying in parallel is allowed; letting
the cursor run ahead of the apply is not.

A cursor that moves **backwards** costs a redelivery, which idempotent apply absorbs. A cursor that
moves **forwards** past an object not yet applied is a permanent gap: the object is
below the `after` the next sweep is handed, so it is never sent again, and nothing on
either side detects the omission. Where the two are in tension an application MUST
prefer the older cursor.

### The end of the sweep is a frame

agrirouter MUST emit an in-band `CAUGHT_UP` frame when the sweep is exhausted,
before the buffer is flushed. It says that everything the application was missing
when the sweep began has now been delivered.

The frame is an **upper limit in the delivery, not a claim about the application**:
it says the objects before it are everything owed, not that the application has
worked through them.

`CAUGHT_UP` is the only boundary in the delivery. The sweep does not announce moving
from one tier to the next, and an application MUST NOT read completeness of an
entity type out of the order it receives objects in. The order guarantees that every
object arrives after the objects it references, and nothing beyond that.

### Origin suppression moves to read time

With a queue per endpoint, [loop prevention](../specification.md#loop-prevention)
filtered at enqueue. With one shared record per object there is nothing to filter
at write time, so the source application is carried on the record and suppression is
applied as it is read: an object whose most recent change came from the reading
application is not delivered back to it.

Object that was [merged](./05-stale-reads.md#a-merged-revision-is-returned-not-streamed)
should not be suppressed though, because no client actually saw the final
merged value and in this case source application either should not be recorded
or ignored at reading.

### Rejected alternative: materializing the canonical set into a queue

Described in [Context](#context). It buys one thing this design gives up: because
agrirouter mints fresh sequential numbers as it writes the block, delivery order
and position are the same axis, so a position advances during a load and an
interrupted one resumes where it stopped instead of starting again. Restarting an
interrupted sweep is the price of not writing those copies. It is worth paying,
because the copies are what create both the retention cliff and the per-endpoint
amplification, and neither has a fix that keeps the queue.

### Rejected alternative: delivering in creation order

Ordering the sweep by `create_change_number` alone reads dependency order off the
write history: nothing can reference an object that does not exist, so a parent's
creating change always carries the lower number. It needs to know nothing about
entity types, which is the whole of its appeal, and it holds only until a reference
changes. A field moved to a farm created after it, a farm gaining a partner
organization registered last week, a field's owner reassigned to a person created
today - each leaves a child whose creating number is below its parent's, and the
sweep emits it first. Re-pointing a reference is an ordinary edit rather than an
edge case, so the invariant does not survive contact with the data. Tiers cost a
declared acyclic type graph and buy an order that no sequence of edits can
invalidate.

### Rejected alternative: a live tail concurrent with the sweep

Sending live changes as they arrive, rather than buffering them, removes the
buffer and its bound. It also breaks the ordering the sweep exists to provide: a
child created during the sweep goes out immediately, while its parent waits for
the sweep to reach its tier. The reference does not resolve, and the
window is as long as the sweep. Buffering costs memory bounded by the objects
changed during one sweep, and that memory has a fallback; the ordering does not.

## Consequences

- **First connection, catch-up and resume are one mechanism.** They differ in the
  `after` they start from, not in kind. There is no separate delivery mode to
  build, document, or fall back to.
- **Nothing expires, so nothing has to be recovered.** An application away for any
  length of time is a larger sweep rather than a failed one. Rate limiting a large
  sweep is a throughput concern rather than a correctness one.
- **Delivery is type-aware, and the type graph is load-bearing.** Both the sweep
  and the flush order by tier, so a new entity type has to be given a tier before it
  can be delivered, and the type graph MUST stay acyclic. A cycle between two types
  has no tier assignment at all, and is the case that would force per-object
  dependency ordering.
- **Same-type references are carried by creation order, and only while they are set
  at creation.** A field naming the field it was split from, an organization naming
  its parent, sit inside one tier, where `(tier, create_change_number)` puts the
  ancestor first. A self-reference that can be re-pointed after creation is not
  ordered by anything here - it is the creation-order failure again, one tier down,
  with no coarser graph left to fall back on. Introducing one means ordering that
  tier per object.
- **The order promises resolvable references, nothing else.** Tiers are how that is
  achieved rather than something an application reads: anything keyed per entity type
  has to derive its own completion, and the delivery carries one boundary,
  `CAUGHT_UP`, covering the sweep as a whole.
- **An interrupted sweep is repeated, not resumed**, and the cost scales with how
  far it got. A large first load over an unreliable connection is the case to
  watch: a sweep that cannot finish between drops does not converge, so
  throughput and connection stability bound how large a set can be delivered.
- **Cursors are comparable integers.** An application applying in parallel commits
  the lowest position it has not yet applied, rather than tracking the order
  frames arrived in.
- **Entitlement gained below the cursor is not reachable from here.** The filter
  excludes it and the buffer never sees it. A cursor records a position, not what
  the application was entitled to on reaching it, so delivery cannot tell data it
  owes from data the application already holds.
- **Applications lose intermediate states.** An application MUST NOT infer that it
  observed every change to an object, and MUST NOT derive anything from the number
  of times an object was delivered.
- **Idempotent apply is key in two places**: redelivery on reconnect, and the
  overlap between the sweep and the buffer behind it. Within the stream, order is
  enough to make the later value win; across the stream and a write response it is
  not, so apply is additionally guarded by `revision`
  ([ADR 05](./05-stale-reads.md#consequences)).
- **Dependency-closed opt-in is structural.** An application opted into fields but
  not farms gets no farms at all and then every field with an unresolvable
  reference. [The rule](../specification.md#routing-and-opt-in) is what holds the
  sweep together.
- **Deactivated objects are retained indefinitely**, which is what makes a
  deletion deliverable at any distance rather than only within a retention
  window. Purging them would reintroduce exactly the horizon this design removes.
- **A sweep delivers them too**, and the absence of that filter is load-bearing.
  A first load is usually taken by a participant that already holds its own data,
  so withholding a deactivated object leads it to send that object back as new -
  duplicating the canonical object and resurrecting what a user archived.
- **The cursor is the application's coordination point.** Delivery being stateless
  on our side does not remove the state, it relocates it: an application that
  spreads apply across instances shares one cursor between them, and gains an
  ordering obligation it would not have with a position we held. That is the price
  of one connection and one piece of state per application rather than per endpoint.
- **A forward cursor is unrecoverable at the object level.** An application that
  suspects it skipped objects cannot ask for them individually - it discards the
  cursor and sweeps everything again.
- **agrirouter holds no position.** Where an application has got to is entirely in
  the cursor it sends. A partner that loses its cursor loses only its place, not
  its entitlement, and recovers by sweeping again.
