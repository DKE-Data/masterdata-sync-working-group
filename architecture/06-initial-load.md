# ADR 06 - Initial load

- **Status:** WIP
- **Scope:** Bringing a newly connected or returning endpoint into agreement with the canonical master data

## Context

[ADR 01](./01-reference-architecture.md) describes steady-state synchronization: once every participant is in step, a change made in one system propagates through the canonical store to the others. But the *first* time an endpoint joins master-data exchange - when the user routes it to the [masterdata hub](./04-routing.md) - there is a bootstrap problem that steady-state sync does not cover.

Note on the naming: the initial load process to fix this is also known as "seeding". This term is not used in the specification to avoid conflating with "seeding" as in "planting seeds", which might be introduced later when we cover other types of data. For exmaple workplans and field operations might refer to _seeding_ in that context. Hence only "initial load" is used in the specs.

The newly connected system is rarely empty. Users have usually already entered master data into it by other means, so it holds **its own set of entities under its own `localId`s**. At the same time, agrirouter already holds the canonical set the rest of the network agreed on. Neither side is a blank slate, and the two sets overlap in unknown ways: the same real-world field may exist on both sides under different identifiers, or exist in only one.

Therefore, bringing the two into agreement has two directions:

- **From agrirouter:** the endpoint must receive what the network already knows, so its user immediately sees the shared master data.
- **To agrirouter:** the network must learn what the endpoint holds that is not canonical yet, so nothing the user already had is lost.

The ordering is what makes this non-trivial. If the endpoint blindly pushed all of its local objects first, it would duplicate everything the network already had. So it must first learn the canonical set, reconcile its own data against it (via [identifier mapping](../specification.md#identifier-mapping)), and only then send what is genuinely new or was changed while resolving a conflict.

This is a mix of technical and organizational concerns: some of it is a defined message flow, and some of it - conflicts, granularity mismatches, differing required attributes - can only be resolved by a human in the partner's own software.

## Decision

Model initial load as an explicit **per-entity-type state machine** that agrirouter holds for each endpoint. Tracking it per entity type, rather than once for the whole endpoint, respects [entity dependencies](../specification.md#entity-dependencies): a field cannot be reconciled before the customers and farms it references.

```mermaid
flowchart TB
    START(( ))
    LF["LOADING_FROM_AGRIROUTER"]
    R["RECONCILING"]
    LT["LOADING_TO_AGRIROUTER"]
    C["COMPLETED"]
    START -->|"entity type opted into the hub"| LF
    LF -->|"agrirouter has sent the whole canonical set"| R
    R -->|"endpoint confirms it has reconciled"| LT
    LT -->|"endpoint has sent everything it holds"| C
    C -->|"entity type opted out (possible from any state)"| START
    %% ceasg:{"id":"6kuwmm6w"} %%
    %% mermaid-flow:pos START=119,100 LF=276,230 R=280,354 LT=259,477 C=119,590
```

The flow, per entity type:

1. **Opt in.** The user routes the endpoint to the [masterdata hub](./04-routing.md) and opts it into one or more entity types. Each such entity type enters `LOADING_FROM_AGRIROUTER`.
2. **Load from agrirouter.** agrirouter sends the endpoint every canonical object of that type it is entitled to receive. This direction is well-defined precisely because [agrirouter is the SSOT](./01-reference-architecture.md) - it already holds the authoritative set to hand over.
3. **Set delivered.** agrirouter closes the set with `CANONICAL_SET_END` ([ADR 07](./07-sync-streaming.md)) and moves the entity type to `RECONCILING`. This is mechanical and agrirouter drives it: it knows it has sent everything, so nothing needs to be reported back. The endpoint now holds the whole set, and the state says so.
4. **Confirm.** Reconciliation potentially finishes much later. Whatever conflicts it surfaced are settled in the partner's software - by a user where the partner's own rules cannot settle them - on a schedule agrirouter does not control. The endpoint confirms after *that*, moving the entity type to `LOADING_TO_AGRIROUTER`. This transition is explicit precisely because agrirouter can see the previous moment and not this one.
5. **Load to agrirouter.** The endpoint now sends the objects it holds that the canonical set did not contain, plus any it changed while resolving conflicts. Because it reconciled first, it sends genuinely new objects instead of duplicates of ones it just received.
6. **Complete.** When the endpoint has sent everything, the entity type enters `COMPLETED`, and ordinary steady-state synchronization ([ADR 01](./01-reference-architecture.md)) applies from then on.

The same six steps run again if the entity type is opted out and back in, but the
second time the endpoint's identifier mapping is still there, so step 2 delivers
objects the endpoint recognises and steps 4 and 5 have little to do
([Disconnection and re-connection](../specification.md#disconnection-and-re-connection)).
`previousLoadCompletedAt` on the initial-load resource is what tells the endpoint
which of the two it is in, and an endpoint that ignores it duplicates its own data.

The `RECONCILING` state enables differentiating between 3 and 4. In its absence, agrirouter would be unable to say whether it still owed the endpoint data or was waiting on a partner app - a distinction it needs for its own scheduling ([ADR 07](./07-sync-streaming.md)), and one the user-facing label wants too.

### The flow in concrete calls

The same six steps expressed as the actual operations of the master-data API,
for one entity type (`farms`). `{eid}` is the endpoint's `externalEndpointId`.

<!-- TODO: try to figure out scaling here because it might be tricky to
send response on SSE rather than on same instance (pod) that god /requests.
Also /requests might need to be part of initial load? -->

```mermaid
%% ceasg:{"id":"8xpvwi5e"} %%
sequenceDiagram
    autonumber
    actor U as User
    participant P as Partner (endpoint)
    participant AR as agrirouter (SSOT)

    U->>P: opt this endpoint into farms
    P->>AR: PUT /endpoints/{eid}/masterdata-config { toggles: [{ entityType: "farms" }] }
    AR-->>P: 200 MasterdataConfig
    Note over AR: farms → LOADING_FROM_AGRIROUTER

    P->>AR: GET /masterdata/events (SSE)
    AR-->>P: 200 text/event-stream
    loop every canonical farm the endpoint is entitled to
        AR-->>P: event: MASTERDATA_CHANGED id: evt-8842 data: { type: "farm", agrirouterId: 1f2e…4567, revision: 3, idMappings: [...] }
        Note over P: reconcile against own store (id mapping, user decides on conflicts)
    end
    AR-->>P: event: CANONICAL_SET_END data: { entityType: "farms" }
    Note over AR: farms → RECONCILING (agrirouter drives this - it knows it has sent everything)
    Note over P: whole set held - user works through what is left, on their own schedule. live changes keep arriving meanwhile
    opt referenced object not held yet
        P->>AR: POST /masterdata/organizations/requests { agrirouterId: 9ab0…1234 }
        AR-->>P: 202 Accepted
        AR-->>P: event: MASTERDATA_CHANGED (organization)
    end

    P->>AR: PUT /endpoints/{eid}/masterdata-initial-load/farms/status { state: "LOADING_TO_AGRIROUTER", idMappings: [{ agrirouterId: 1f2e…4567, localId: b1e7… }] }
    AR-->>P: 200 EntityInitialLoadStatus { state: "LOADING_TO_AGRIROUTER", rejectedIdMappings: [] }

    loop every local farm that is new or was changed while resolving a conflict
        P->>AR: PUT /masterdata/farms/{localId} { type: "farm", owner: {...}, name: "Hof Nord" }
        alt matched during reconciliation, bound above
            AR-->>P: 200 Farm { agrirouterId: 1f2e…4567, revision: 8 }
        else genuinely new to the network
            AR-->>P: 201 Farm { agrirouterId: 7c1d…8899, revision: 1 }
        else localId already mapped to a different canonical object
            AR-->>P: 409 Error (mapping conflict - resolved in the partner)
            P->>AR: PUT /endpoints/{eid}/masterdata-initial-load/farms/status { awaitingUser: true }
        end
    end

    P->>AR: PUT /endpoints/{eid}/masterdata-initial-load/farms/status { state: "COMPLETED" }
    AR-->>P: 200 EntityInitialLoadStatus { state: "COMPLETED" }
    Note over P,AR: steady-state synchronization from here on, over the same event stream
```

Points worth noting about the calls themselves:

- **The confirmation carries what reconciliation decided.** Matching a canonical
  object to one the endpoint already holds is a fact only the endpoint has, and
  it has to reach agrirouter or the object is sent back as new and duplicated.
  The bindings ride on the confirmation rather than one call per object, because
  matching happens over a whole set - see
  [ADR 10](./10-identifier-binding.md). It is also why the loop above has a `200`
  branch at all: an object matched and then edited during conflict resolution is
  an update to a canonical object, not a creation.
- **There is no bulk "give me everything" endpoint.** The from-agrirouter
  direction reuses the ordinary event stream (`GET /masterdata/events`); initial
  load is a sweep delivered over the same channel rather than a second delivery
  mechanism, which is what lets
  [resume](#resume-is-the-same-machinery-not-a-special-case) share the machinery.
  Stream position and reconnect are handled by [ADR 07](./07-sync-streaming.md).
- **Opting in is what starts the load.** The endpoint never sets
  `LOADING_FROM_AGRIROUTER` itself; `PUT .../masterdata-config` does. agrirouter
  also drives the step to `RECONCILING`, for the same reason - it is the side that
  knows the set has been sent. The two transitions the endpoint drives are the
  confirmation and the completion, both through
  `PUT .../masterdata-initial-load/{entityType}/status`. Only forward transitions
  are accepted - anything else is a `409`.
- **Opting out is the one way back.** Removing an entity type from the
  configuration discards its state, from whichever state it was in, and opting it
  back in starts the load again. This is not a transition the endpoint can drive 
  by changing state, but by opting-out via masterdata-config resource,
  and it is not an exception to the forward-only rule above: the endpoint's
  position kept advancing on the types it stayed opted into, so objects of the
  opted-out type that changed in the meantime now sit *behind* that position and
  no ordinary catch-up would reach them. A full sweep of the type is the only way
  back to agreement - the same shape as any other backfill in
  [ADR 07](./07-sync-streaming.md).
- **The stream position is the only cursor.** Opting an entity type in starts a
  sweep of that type's canonical set on the application's ordinary stream, so how
  far the endpoint has consumed is already expressed by its position - there is no
  second checkpoint to carry on the confirmation. See
  [ADR 07](./07-sync-streaming.md).
- **The endpoint reads the state machine, it does not keep one.** agrirouter is
  authoritative for every phase, including whether the canonical set finished
  arriving (that is the difference between
  `LOADING_FROM_AGRIROUTER` and `RECONCILING`). So
  `GET /endpoints/{eid}/masterdata-initial-load` answers both questions an
  endpoint that restarted mid-flow has: whether it still owes a push, and whether
  it holds the whole set. Its own cursor answers the second too
  ([ADR 07](./07-sync-streaming.md)), without a round trip, but the resource is
  the authority.
- **Waiting for the user does not pause the stream.** Delivery continues while
  conflicts are being resolved, so `RECONCILING` is not a quiet window
  and reconciliation runs against a set that keeps changing under it. One stream
  carries every tenant ([ADR 07](./07-sync-streaming.md)), so an endpoint that
  stalled it while one user deliberated would stop delivery for every other tenant
  the application serves.
- **The to-agrirouter direction uses the ordinary send operation.**  There is no
  special initial-load write path - `PUT /masterdata/farms/{localId}` is the same
  call the endpoint uses in steady state, and the same `409` signals a
  [non-unique mapping](#granularity-mismatches-and-differing-requirements).
- **The sequence runs per entity type**, so for example `farms` reach `COMPLETED` before `fields` is confirmed - a field cannot be
  reconciled against dependencies the endpoint has not received yet.
  `GET /endpoints/{eid}/masterdata-initial-load` returns every opted-in entity
  type at once.
- **There is no "not started" state.** An entity type has an initial-load state
  only once it is opted in - the absence of a toggle in `masterdata-config`
  already says it does not participate, and a second representation of that
  would allow the two to disagree. Opting out discards the state along with the
  toggle, so opting back in reloads rather than resumes - see above for why
  resuming would not be safe.

### Conflicts are resolved in the partner, not in agrirouter

While reconciling, the endpoint may find that its own object and the canonical object for the same real-world entity disagree. Consistent with [ADR 01](./01-reference-architecture.md), agrirouter does not adjudicate field-level conflicts: it provides the canonical set to reconcile against, and the **partner's own software presents the conflict to the user**. This keeps agrirouter out of business decisions it has no basis to make, and it is why the "to agrirouter" step can carry objects the user changed during resolution.

### Where the user is when conflicts appear

The conflicts are in the partner's software, but the user who connected the endpoint is not necessarily there. Three questions follow: whether agrirouter has to be told that conflicts exist, whether it should send the user somewhere, and what happens when no user is present at all.

**agrirouter is told that reconciliation needs user action.** The endpoint raises `awaitingUser` on the entity type's status the moment its own software detects that reconciliation is needed, and agrirouter clears it on the next forward transition. That is the whole of the reporting:

| what agrirouter holds | what agrirouter UI says about the endpoint |
| --- | --- |
| `LOADING_FROM_AGRIROUTER`, `awaitingUser` unset | agrirouter is sending your master data to *app* |
| `LOADING_FROM_AGRIROUTER`, `awaitingUser` set | waiting for you in *app* |
| `RECONCILING`, `awaitingUser` unset | *app* is working through your master data |
| `RECONCILING`, `awaitingUser` set | waiting for you in *app* |
| `LOADING_TO_AGRIROUTER`, `awaitingUser` unset | *app* is sending its data |
| `LOADING_TO_AGRIROUTER`, `awaitingUser` set | waiting for you in *app* |
| `COMPLETED` | in sync |

`awaitingUser` is a flag signifying that user action is needed to perform reconciliation. It is reset by a transition agrirouter owns rather than by a second call from the endpoint. It is specified per entity type, like the resource it sits on, so farms can be clean while fields wait.

**Every phase before `COMPLETED` can need a person.** Reconciling against the canonical set is the obvious source of conflicts, and they surface object by object as objects arrive - so the flag can be raised while the set is still being delivered, not only once it is complete. The push direction produces them too: a `PUT /masterdata/farms/{localId}` can come back `409` because the canonical object it names is already mapped to a different `localId` ([below](#granularity-mismatches-and-differing-requirements)), and resolving that is a decision in the partner's software just the same.

The flag therefore spans two windows rather than three. `LOADING_FROM_AGRIROUTER` and `RECONCILING` are one window - the same reconciliation work, before and after the last object lands - cleared by the confirmation. `LOADING_TO_AGRIROUTER` is the second, cleared by the completion. The `LOADING_FROM_AGRIROUTER` → `RECONCILING` step does **not** clear it: that transition is agrirouter saying it has finished sending, which asserts nothing about whether the user has finished deciding.

**An unset flag says nothing about the user.** It is ambiguous in two directions at once - the endpoint may not report the flag at all, or may report it and have nothing to raise yet. So reporting it only ever *upgrades* a label, and an endpoint that never sends it costs precision rather than correctness.

The flag is independent of the fact that the whole canonical set has been received: conflicts surface object by object as they arrive.

**A link, not a redirect.** When the route is created there is nothing to resolve yet, so redirecting at that moment lands the user on an empty page - and the flag that says otherwise arrives minutes or hours later. So the partner declares `resolutionUrl`, an optional opaque URL set on `PUT .../masterdata-config` next to the toggles - the moment it knows which of its own tenants this endpoint is - and agrirouter renders it as a link throughout initial load, as the call to action whenever `awaitingUser` is set and quietly otherwise. It is not per conflict and not templated by agrirouter, and where it is absent the label stands on its own.

**Route creation scenarios differ only in where the user already is.** The canvas arrow ([ADR 04](./04-routing.md)) and the entity-type toggles are two gates on the same thing, and the load starts once both are present:

- **Partner-initiated** - RAC, or the partner's own settings screen. The user is in the partner app when the load starts and never leaves it, so nothing is required of agrirouter. This is the shape to prefer, and the reason `resolutionUrl` is optional.
- **Drawn in the agrirouter canvas.** The user is in agrirouter and the work is elsewhere. This is the case the label and the link exist for.
- **Created automatically.** Does not arise: default routing never creates a `HubRoute`. Opting into master data stays an explicit act by a user, for the reason [ADR 04](./04-routing.md) gives - connecting an application unintentionally can overwrite a lot of data - so an endpoint created by a partner that supports master data is not silently opted in, and no initial load starts with nobody to tell.

**Waiting is not free**, which is why the partner should not rely on the user wandering back. Delivery does not expire, so a resolution left half done does not cost a reload of the canonical set ([ADR 07](./07-sync-streaming.md)) - but the set keeps moving underneath it, so the longer a conflict sits the likelier it is that the object the user is deciding about has changed again since it was surfaced. Pending resolution SHOULD therefore be surfaced in the partner's own UI rather than only on the screen the user happened to be on.

### Granularity mismatches and differing requirements

Two problems surface at initial load that agrirouter deliberately does **not** try to solve:

- **n:1 / non-unique mappings.** Two objects in one system can correspond to a single object in another - for example two fields that are a single field elsewhere. This is not solvable centrally, because the ambiguity exists even without agrirouter in the loop. So an endpoint MUST NOT map two of its own `localId`s onto the same canonical object; agrirouter rejects the second, pushing resolution back to the partner. See [Asymmetric and non-unique mappings](../specification.md#asymmetric-and-non-unique-mappings).
- **Differing required attributes.** One system may require a customer on every field where another treats it as optional. The **stricter recipient** handles this - e.g. by asking the user for a fallback value - rather than agrirouter enforcing one system's rules on another or silently dropping data.

### Resume is the same machinery, not a special case

A connection can drop mid-load, or an already-`COMPLETED` endpoint can go offline while changes accumulate on both sides. Rather than re-running initial load from scratch, the endpoint resumes from the last delivery position it processed. That position is the **delivery cursor** [ADR 03](./03-revision-model.md) calls for rather than the per-object `revision`, and it is carried as `Last-Event-ID` - its shape, and the fact that it can not expire, are described in [ADR 07](./07-sync-streaming.md).

A dropped connection mid-load is therefore not a distinct case: the cursor still names the tier - the entity type's position in the dependency graph, swept in ascending order ([ADR 07](./07-sync-streaming.md#catch-up-sweeps-by-tier-the-live-tail-follows-it)) - and the offset the sweep had reached, and delivery continues from there rather than from the beginning of the set.

Here is approximate lifecycle that we are expected to support:

```mermaid
flowchart TB
    START(( ))
    INITIAL_LOAD["INITIAL_LOAD \n (see states above)"]
    OPERATING["OPERATING"]
    DOWN["DOWN"]
    CATCHING_UP["CATCHING_UP"]
    START -->|"opted into the hub"| INITIAL_LOAD
    INITIAL_LOAD -->|"loaded"| OPERATING
    OPERATING -->|"application goes offline"| DOWN
    DOWN -->|"comes back online"| CATCHING_UP
    CATCHING_UP -->|"caught up from its cursor"| OPERATING
    %% ceasg:{"id":"un5r0wx1"} %%
    %% mermaid-flow:pos START=270,60 INITIAL_LOAD=270,165 OPERATING=270,275 DOWN=110,395 CATCHING_UP=430,395
```

## Consequences

- agrirouter holds an explicit per-endpoint, per-entity-type initial-load state and exposes it as a `status` subresource that the endpoint reads and advances by setting its state (confirm receipt, then complete), as part of the master-data API.
- The "from agrirouter, then to agrirouter" ordering is what prevents duplication: reconciliation happens against the canonical set before the endpoint sends anything.
- Some of the hard parts are intentionally organizational: conflict resolution and granularity mismatches live in partner software, so the protocol defines the flow and the failure signals but not the resolution.
- Initial load and resume share the same stream position, so a returning system is a continuation of the same state rather than a separate code path.
- agrirouter learns that a user action is needed, never what for. `awaitingUser` is one bit per entity type, monotonic within a window and cleared by the endpoint-driven transition that ends it.
- The bit is advisory. Endpoints that omit it cost only label precision, and nothing in the flow branches on it.
- `masterdata-config` gains an optional `resolutionUrl`, which is the only thing a partner has to supply for agrirouter to point a user at the right screen, and default routing never opts an endpoint into the hub.
- Either endpoint-driven transition *may* wait on a human - the confirmation on reconciliation, the completion on a rejected push - and neither necessarily does: an endpoint with no conflicts, or one whose conflicts its own rules settle, advances straight through. What agrirouter cannot tell is which case it is in, since it sees only when it finished sending. So an entity type can sit in either loading state for days without anything being wrong, and no timeout on them would be meaningful.
- `RECONCILING` separates "agrirouter still owes data" from "the endpoint still owes a decision". agrirouter needs that distinction for its own scheduling - on a reconnect it must know whether a sweep is outstanding ([ADR 07](./07-sync-streaming.md)) - and publishing it rather than hiding it keeps a single representation of the phase, consistent with there being no "not started" state.
- The state machine has agrirouter-driven and endpoint-driven edges. Opt-in and the step to `RECONCILING` are agrirouter's; the confirmation and the completion are the endpoint's.
- Confirming from `LOADING_FROM_AGRIROUTER` is a `409`. An endpoint cannot have reconciled a set it has not finished receiving.
- Initial-load state is keyed per endpoint and entity type, while the delivery cursor is keyed per application ([ADR 07](./07-sync-streaming.md)). One connection therefore carries the loads of many tenants at once, each at its own phase, and an application MUST NOT treat "my stream is in initial load" as a single condition.
- Because event delivery does not expire, an entity type can sit in either loading state indefinitely without putting the endpoint's position at risk. The only cost of a slow user is a canonical set that has moved on.
- Several details remain open: best-practice guidance for implementors on conflicts and differing requirements.
