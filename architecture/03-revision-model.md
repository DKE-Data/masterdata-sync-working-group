<!-- TODO should review this entirely -->
<!-- also in spec should probably be string -->

# ADR 03 — Revision model: a monotonic integer assigned by agrirouter


- **Status:** WIP
- **Scope:** Canonical object versioning, loop prevention, no-op detection, resume

## Context

agrirouter must be able to tell that an incoming masterdata change does not actually modify the canonical object, so that systems which notify unconditionally do not produce empty revisions or spurious traffic.

A partner receiving a delivered object must be able to decide, cheaply and unambiguously, whether it supersedes the copy it already holds.

Per [ADR 01](./01-reference-architecture.md), **agrirouter is a single canonical authority 
that serializes every write to a given canonical object.** 

## Decision

`revision` is a monotonically increasing positive integer, assigned solely by
agrirouter and having following properties:

- **Total order** - `revision 8 > 7`, unambiguously, everywhere. A partner
  reconciling a delivered object gets a trivial "is this newer?" test — needs (2)
  and (3) — with no tie-break rules to implement.
- **Uniqueness** - because a single authority assigns the counter,
  two different revisions of the same object can never share a number.
- **Legible and cheap.** It is readable, comparable, and debuggable. That matters
  for a specification that several vendors must implement independently and
  interoperably.

### Alternative considered: a composite generation + content digest

A common versioning scheme in replicated stores is a composite revision — a
generation counter plus a digest of the content, e.g. `8-a1b2c3…`.

That construction exists to serve multi-writer designs with no central
authority, where any replica may accept a write and two replicas can
independently mint the same generation. This makes
a revision unique without coordination, it lets divergence be *detected* (two
revisions at the same generation = a conflict), and it supports deterministic
winner selection across a tree of retained branches.

None of those needs exist at the moment. We have a single authority, being able
to serialize writes for the same object.
Same-generation collisions cannot arise, divergence cannot occur at the canonical
object, and there is no branch tree to arbitrate.

## Consequences

### A client-supplied `revision` is a precondition, never an assignment

Writes carry the client's prior revision as a CAS precondition (see
[ADR 05](./05-stale-reads.md)). agrirouter MUST compare that value and discard it;
it MUST NOT be persisted, and the next revision is minted by agrirouter alone.

Without it a partner could fast-forward the counter, or mint a revision high enough
that its copy outranks every other — so "higher revision" would no longer mean
"later write", and a partner reconciling a delivered object could accept a stale
copy as the current one. The value is also guessable by construction (a
small integer, visible to every entitled reader), so it can only ever be a
concurrency token, never an authorization or authenticity one.

### A content digest may be used internally — but is not the revision

A digest MUST NOT form part of the revision identity. There is one place it fits
naturally, and there it is permitted as a non-normative implementation aid:

- **No-op detection (need 1).** Because we already require a
  [canonical encoding](../specification.md#encoding-and-canonicity) — "exactly one
  valid encoding per value" — a hash over the canonical form makes the
  did-anything-change test cheap and unambiguous: equal canonical-content hash ⇒
  no-op ⇒ do not bump `revision`, do not forward.

### The per-object revision is not the resume checkpoint

`revision` versions **one canonical object**. It is not, by itself, sufficient for
[resume](../specification.md#initial-load-and-seeding): a returning partner needs to
know what changed across *all* objects it is entitled to, not to walk each one. That
calls for a second, separate ordering - a **monotonic delivery sequence held per
application**, so a partner resumes from "the last position I confirmed."

The two are independent and MUST NOT be conflated: `revision` answers "is this
copy newer than mine" for one object, the delivery sequence answers "what have I
not seen yet" across all of them. [ADR 07](./07-sync-streaming.md) defines the
latter.

## Summary

- `revision` is a **monotonic positive integer assigned by agrirouter**; no digest,
  no signature. A revision on an inbound write is **compared, never assigned**.
- It gives a **total order**, which is what supersession, conflict detection, and
  resume actually need.
- The correctness invariant is: **agrirouter serializes writes per canonical
  object.** State it; preserve it.

