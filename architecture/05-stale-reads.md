# ADR 05 - Solving stale reads

**Status:** Proposed

**Scope:** Preventing stale reads affecting masterdata sync processes

## Context

When multiple clients attempt to edit the same masterdata object concurrently, it is possible that one client will overwrite changes made by another client without being aware of it. This can lead to data inconsistencies and loss of important information.

Since we have a variety of reading processes (seeding, synchronization) that are not necessarily aware of each other, we need to have a cross-cutting mechanism to prevent stale reads / inform clients when that might be the case.

The simplest solution to this common issue is known as "Compare And Swap" or CAS. Every time a client wants to perform a write operation they have to provide a previous revision of the object they are trying to modify. In case if object has been modified in the meantime, the write operation will be rejected and the client will have to ensure to get the latest version of the object and re-apply their changes.

## Decision

