# Reference client

A working AgmaSync participant, and a working implementation of the agrirouter
side to run it against. Both are illustrative and neither is normative: where
this disagrees with [specification.md](../specification.md) or
[openapi.yaml](../openapi.yaml), the specification is right and this is a bug.

What it is for is the part of the protocol prose cannot settle. Apply guarded by
`revision` across two channels, `localId` meaning the near end of the transfer
and nothing else, bind-before-send, unbind-then-recreate, a position derived
from what was *durably applied*, and a four-state initial load driven from both
ends — a vendor reading about those has no way to check their reading. Running
them does.

## Start here

```
go run ./cmd/scenarios
```

Fifteen numbered stories, narrated as they run, each citing the sections of the
specification it is about. No Docker and nothing to start: the test router runs
in process.

1. Onboarding and first initial load
2. Repeat load after opt-out and opt-in, matched through the surviving mapping
3. A concurrent edit merged, and the overlapping one rejected
4. Unbind, receive the object again with no `localId`, recreate and rebind
5. A deactivated object in the canonical set, recognised and not
6. Resume after downtime, and a crash between applying and saving the position
7. Non-unique mapping rejected, both causes, singly and in bulk
8. Reconciliation that needs a person stops the load until it has an answer
9. An object asked back for after the endpoint that wrote it lost it
10. A live change referencing what the set has not delivered, wating rather than requesting
11. Attributes a participant does not model, relayed unchanged
12. A recipient stricter than the protocol
13. A route removal performed while the participant was offline
14. A field split in two, and the two merged back, with no lineage on the wire
15. A reference that does not resolve

They assert as they narrate. Every `ok` line is a claim checked at the moment it
is printed, so a scenario that would teach something untrue fails instead —
which is what makes them worth reading and worth keeping in CI. One at a time
with `go run ./cmd/scenarios -scenario 4`.

Scenarios 8 and 12 ask you a question, because the protocol cannot answer it:
agrirouter provides the canonical set and adjudicates nothing, so which of a
platform's records a delivered object *is* — and what to put in a required
attribute the object does not carry — comes down to a person. Run them from a
terminal and they prompt; with input redirected, or under `-batch` or `go test`,
the default answer stands and the run stays automatable.

## What is in here

| | |
| --- | --- |
| [scenarios/](scenarios/) | the stories above, and the small world they run in |
| [internal/platform/](internal/platform/) | one plausible FMIS: a SQLite store, and the sync it does over it |
| [internal/testrouter/](internal/testrouter/) | the agrirouter side, in memory, generated from the same `openapi.yaml` |
| [cmd/agmactl/](cmd/agmactl/) | single operations from a shell |
| [cmd/testrouter/](cmd/testrouter/) | the test router over HTTP |
| [cmd/scenarios/](cmd/scenarios/) | the narrated runner |

The protocol layer is the [agmasync](../agmasync) module beside this one, which
a vendor can import on its own: it has three dependencies and knows nothing
about SQLite, Echo or testcontainers.

Everything here is under `internal/` except the scenarios, so that nobody can
accidentally depend on this sample's design choices. The choices worth reading
rather than copying are in [store/schema.sql](internal/platform/store/schema.sql)
— in particular the split between the platform's own tables and the `agmasync_`
ones beside them, which hold the two things a participant has to keep and
usually has nowhere to put: the correspondence, and the delivery position.

### The test router is not a stub

It is a second reading of the specification: revision counters, origin
suppression, the three-way merge, catch-up ordered parents-before-children, the
initial-load state machine with its out-of-order transitions, and per-pair
binding rejections are all implemented. A client that gets those wrong fails
against it. It holds everything in memory, treats the bearer token as the
application identifier, and stands in for agrirouter's own screens with a
control plane under `/_test` that has no counterpart in `openapi.yaml`.

## agmactl

Single operations against the test router or, once implemented, the real agrirouter. It
holds no store, so it cannot work out what a delivered object corresponds to, or
resume where it left off.

```
go run ./cmd/testrouter &                  # or point at a real agrirouter

export AGMASYNC_URL=http://localhost:8080
export AGMASYNC_TOKEN=fmis-alpha
export AGMASYNC_EXTERNAL_ENDPOINT_ID=ep-alpha

TENANT=$(curl -s -XPOST $AGMASYNC_URL/_test/tenants | sed 's/.*"tenantId":"\([^"]*\)".*/\1/')
export AGMASYNC_ENDPOINT_ID=$(curl -s -XPOST $AGMASYNC_URL/_test/endpoints \
  -H 'content-type: application/json' \
  -d "{\"applicationId\":\"$AGMASYNC_TOKEN\",\"tenantId\":\"$TENANT\",\"externalId\":\"$AGMASYNC_EXTERNAL_ENDPOINT_ID\"}" \
  | sed 's/.*"endpointId":"\([^"]*\)".*/\1/')

agmactl declare farm,field                 # what your software can exchange
agmactl route farm                         # test router only: stands in for the user
agmactl status                             # declaration, load state, rejected pairs
agmactl put farm farm.json                 # -base <n> on an update
agmactl replay -follow                     # watch the live stream
agmactl bind farm FRM-1 <agrirouterId>
```

`put` reads a type and a file (or `-` for stdin); the JSON body itself must
carry `type` and `local_id`, since those travel in the payload rather than as separate flags:

```
echo '{"type":"organization","local_id":"org-1","name":"gamma corp"}' \
  | agmactl put organization -
```

### Driving an initial load

Routing a type for the first time puts the endpoint in
`LOADING_FROM_AGRIROUTER`, owing it the canonical set. `agmactl` holds no store, so it has no conflicts of its own to raise reconciling that set against anything — which
is why one command carries the whole thing through to `COMPLETED`:

```
agmactl load                               # take the set, confirm, complete
agmactl load -auto                         # ...binding everything else agmactl "does not hold"
```

`load` prints every object the same way `replay` prints an event. From there it confirms
immediately, carrying whatever `localId=agrirouterId` pairs were given on the
command line in place of a store to read them from, and since
confirming always advances to `LOADING_TO_AGRIROUTER` regardless of what it
rejects completes right behind it.

The one thing it will not carry past is a rejection agrirouter says needs a
person (`LOCAL_ID_ALREADY_BOUND`, not one this participant's own bookkeeping
could resolve): `load` prints it and stops at `LOADING_TO_AGRIROUTER` instead
of completing. Resolve it and drive the rest by hand:

```
agmactl confirm org-1=<agrirouterId>       # redo, with the corrected binding
agmactl complete                           # finish, now that nothing is pending
agmactl attention                          # tell agrirouter a person is needed
```

`complete` only succeeds from `LOADING_TO_AGRIROUTER`, and `attention` only
before `COMPLETED` — both surface an out-of-order transition as
`ErrInitialLoadConflict` (HTTP 409) rather than silently doing nothing.

`-auto` works the same way on the live stream: `agmactl replay -follow -auto`
binds an object the moment it arrives with no `local_id`, same as `load -auto`
does for the fixed set. Unlike `load`, it never stops for a rejection that
needs a person — a `refused:` line is printed and the stream keeps running,
since one object nobody has resolved yet must not end a `-follow` watching for
everything else.

## Tests

```
go test ./...                    # unit tests, plus the scenarios against the in-process router
go test --tags=it ./...          # the same scenarios against the containerised router (Docker)
go vet ./...
go vet --tags=it ./...
```

`go test --tags=it ./...` is worth the minute it costs: in process the streams are an
`io.Pipe`, which cannot show whether a long-lived SSE response survives a real
server, whether an initial-load response really closes, or whether a resumed
stream reconnects over a socket.
