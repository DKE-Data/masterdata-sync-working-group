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

Eight numbered stories, narrated as they run, each citing the sections of the
specification it is about. No Docker and nothing to start: the test router runs
in process.

1. Onboarding and first initial load
2. Repeat load after opt-out and opt-in, matched through the surviving mapping
3. A concurrent edit merged, and the overlapping one rejected
4. Unbind, receive the object again with no `localId`, recreate and rebind
5. A deactivated object in the canonical set, recognised and not
6. Resume after downtime, and a crash between applying and saving the position
7. Non-unique mapping rejected, both causes, singly and in bulk
8. Reconciliation that needs a person stops the load until it has one

They assert as they narrate. Every `ok` line is a claim checked at the moment it
is printed, so a scenario that would teach something untrue fails instead —
which is what makes them worth reading and worth keeping in CI. One at a time
with `go run ./cmd/scenarios -scenario 4`.

Scenario 8 asks you a question, because the protocol cannot answer it: agrirouter
provides the canonical set and adjudicates nothing, so which of a platform's
records a delivered object *is* comes down to a person. Run it from a terminal
and it prompts; with input redirected, or under `-batch` or `go test`, the
default answer stands and the run stays automatable.

## What is in here

| | |
| --- | --- |
| [scenarios/](scenarios/) | the seven stories, and the small world they run in |
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

Single operations against the test router or, once one exists, a real tenant. It
holds no store, so it cannot bind what it receives or resume where it left off —
that is the reference client's job, and everything interesting about
synchronizing is in there rather than here.

```
go run ./cmd/testrouter &                  # or point at a real agrirouter

export AGMASYNC_URL=http://localhost:8080
export AGMASYNC_TOKEN=fmis-alpha
export AGMASYNC_ENDPOINT_ID=<uuid>
export AGMASYNC_EXTERNAL_ENDPOINT_ID=ep-alpha

agmactl declare farm,field                 # what your software can exchange
agmactl select farm                        # test router only: stands in for the user
agmactl status                             # declaration, load state, rejected pairs
agmactl put farm farm.json                 # -base <n> on an update
agmactl replay -follow                     # watch the live stream
agmactl bind farm FRM-1 <agrirouterId>
```

Against the test router, a tenant and an endpoint come from the control plane:

```
curl -XPOST $AGMASYNC_URL/_test/tenants
curl -XPOST $AGMASYNC_URL/_test/endpoints -H 'content-type: application/json' \
  -d '{"applicationId":"fmis-alpha","tenantId":"<uuid>","externalId":"ep-alpha"}'
```

`select` exists only there. A user makes that decision in agrirouter, and a
participant learns of it on the event stream — which is why `agmactl status`
does not show it and `agmactl replay` does.

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
