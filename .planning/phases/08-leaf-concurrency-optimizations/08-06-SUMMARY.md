---
phase: 08-leaf-concurrency-optimizations
plan: 06
subsystem: concurrency
tags: [xsync, ants, audit, snapshot-discipline, hot-collections, opt-04, opt-06, race-clean, contention]

# Dependency graph
requires:
  - phase: 08-leaf-concurrency-optimizations (08-01)
    provides: "the async substrate — the per-subsystem ants pools (pathPool/trackerPool/spawnPool), submitOrDrop drop-on-overload, and asyncSubmitDrops (the xsync.Counter this plan finalizes as the one justified xsync use)"
  - phase: 08-leaf-concurrency-optimizations (08-02)
    provides: "OPT-01 snapshotRegion/computePath — the owner-side path snapshot proving pathPool workers read a copy, never the live ChunkManager/store"
  - phase: 08-leaf-concurrency-optimizations (08-04)
    provides: "OPT-02 per-player position+tracked snapshot — proving trackerPool workers read a copy, never the live players/p.tracked"
  - phase: 08-leaf-concurrency-optimizations (08-05)
    provides: "OPT-03 snapshotSpawnColumns — proving spawnPool workers read a solidity copy, never the live ChunkManager/store"
provides:
  - "OPT-04: the audited hot-path collection strategy — snapshot-and-stay-plain for EVERY tick-owned collection (entityStore.byID/buckets, ChunkManager.columns, clientIndex, players, p.tracked, sentChunks), each justified by a per-collection contention rationale (NOT blanket xsync)"
  - "the ONE justified xsync/v4 usage: asyncSubmitDrops (xsync.Counter) finalized as a genuine cross-boundary value — submit paths Inc() it, the tick reads Value() each tick into TickStats.AsyncDrops (TickLoop.AsyncDrops)"
  - "confirmation of the ants/v2 pools (one per subsystem) as the OPT-04 'worker pools use ants/v2' deliverable, with sizing + non-blocking-drop rationale recorded"
  - "the no-live-capture enforcement gate (AST scan of submitOrDrop closures) — a regression that reintroduces a live cross-boundary read fails the build"
affects: [08-07-OPT-06, future-async-subsystems]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Decide-per-collection-by-contention: a hot collection becomes xsync ONLY where a value is genuinely written/read across the async boundary; the snapshot discipline keeps every tick-owned map plain (single-owner = faster)"
    - "Cross-boundary counter as the justified xsync use: a multi-writer + tick-reader metric (asyncSubmitDrops) is an xsync.Counter; a plain int64 would race the read against the increments"
    - "AST-based discipline gate: parse production sources, locate submitOrDrop closures, fail on any live tick-owned collection token in a closure body (comment-free so audit prose can't false-positive)"

key-files:
  created:
    - "server/collections_audit.go — the per-collection contention audit table + the ants-pool sizing/non-blocking rationale + TickLoop.AsyncDrops (the read side of the one justified xsync.Counter)"
    - "server/collections_audit_test.go — TestHotCollectionsXsyncConcurrent (the xsync -race exercise) + TestNoLiveCollectionCapture (the snapshot gate) + TestTickOnlyMapsStayPlain (the plain-map pin)"
  modified:
    - "server/async.go — asyncSubmitDrops doc updated: it is the ONE justified xsync use the audit keeps (now read by the tick), not a placeholder to extend"
    - "server/tick.go — TickStats.AsyncDrops field + recordMSPT republishes asyncSubmitDrops.Value() each tick (the tick-goroutine read half of the cross-boundary counter)"

key-decisions:
  - "Snapshot-and-stay-plain is the honest OPT-04 outcome: NO tick-owned map was converted to xsync. OPT-01/02/03 all snapshot their inputs on the owner, so no worker reads a live tick-owned collection — a concurrent map would add overhead with zero correctness benefit (08-RESEARCH Open Question 1 / Pitfall 1 / CLAUDE.md)"
  - "idAlloc stays atomic.Int32, not xsync: an atomic counter IS the lock-free/specialized variant for a counter — it is the genuine cross-boundary primitive (accept goroutine + tick) but already correct; xsync would not upgrade it"
  - "The one justified xsync use is asyncSubmitDrops (xsync.Counter), made genuinely cross-boundary by wiring the tick to read it (TickStats.AsyncDrops) — the submit paths write it, the tick reads it, so a plain int64 would race; xsync.Counter is the correct lock-free primitive"
  - "The discipline is enforced by an AST gate (not a fragile text grep): it matches only real submitOrDrop closures and is comment-free, so the audit's own prose can never false-positive, and it was verified to FAIL when a live read is injected"

patterns-established:
  - "Per-collection contention audit: every hot collection gets a documented plain-vs-xsync verdict with its contention rationale before any swap"
  - "Justified-per-use xsync: the dependency is exercised only where contention is real, never blanket-applied"
  - "Enforcement gate for an architectural invariant: an AST test pins the snapshot discipline so the -race-clean-by-construction property cannot silently regress"

requirements-completed: [OPT-04, OPT-06]

# Metrics
duration: 5 min
completed: 2026-06-24
---

# Phase 8 Plan 06: OPT-04 Hot-Path Collections Audit Summary

**A per-collection contention audit concluding snapshot-and-stay-plain for every tick-owned map (no blanket xsync), with the one justified xsync.Counter (asyncSubmitDrops) finalized as a tick-read cross-boundary metric, the three ants pools confirmed, and an AST gate enforcing the snapshot discipline.**

## Performance

- **Duration:** 5 min
- **Started:** 2026-06-24T18:16:29Z
- **Completed:** 2026-06-24T18:21:12Z
- **Tasks:** 2
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments

- **The per-collection audit (the load-bearing deliverable):** every tick-owned collection gets a documented plain-vs-xsync verdict with its contention rationale. Conclusion: ALL stay plain.
- **The one justified xsync/v4 use:** `asyncSubmitDrops` (xsync.Counter) is now a genuine cross-boundary value — the submit paths `Inc()` it and the tick `Value()`-reads it each tick into `TickStats.AsyncDrops` (surfaced by `TickLoop.AsyncDrops`). xsync.Counter is correct because a plain int64 would race the concurrent inc-vs-read.
- **The ants pools confirmed** as the OPT-04 "worker pools use ants/v2" half, with sizing (pathPool=NumCPU, tracker/spawnPool=2) and non-blocking-drop rationale recorded.
- **The snapshot discipline gated:** an AST test scans every `submitOrDrop` closure and fails on any live tick-owned collection access — verified to bite (a precise file:line failure) when a live read is injected.
- **Docker -race clean** over all server packages — proof the snapshot discipline + the single xsync.Counter is race-free WITHOUT blanket xsync.

## The Per-Collection Audit Verdict

| Collection | Read live by a worker? | Decision | Rationale |
|---|---|---|---|
| `entityStore.byID` | NO | PLAIN map | OPT-01 reads `e.id` (value) on owner; OPT-02 copies pos+dims into the diff snapshot; OPT-03 re-resolves the live count on owner in applyTo; tickAI/tickPhysics range a copied `[]*Entity` |
| `entityStore.buckets` | NO | PLAIN map | `near()` returns a FRESH copied slice on the owner; no worker walks the bucket map |
| `ChunkManager.columns` | NO | PLAIN map | OPT-01 `snapshotRegion` + OPT-03 `snapshotSpawnColumns` copy solidity into an immutable snapshot on the owner |
| `clientIndex` | NO | PLAIN map | only owner dispatch/registration touch it; results carry an entity-id re-resolved on owner |
| `players` | NO | PLAIN slice | OPT-02 snapshots each player on owner; applyTo re-resolves by id (`playerByEntityID`) |
| `tickPlayer.tracked` | NO | PLAIN map | OPT-02 copies the set into the snapshot; worker returns a delta; applyTo mutates owner-side only |
| `tickPlayer.sentChunks` | NO | PLAIN map | only tickChunks/flushOutbound (owner) touch it |
| `idAlloc` | YES (atomic) | atomic.Int32 (NOT xsync) | the genuine cross-boundary primitive — already lock-free; an atomic counter IS the specialized variant |
| `asyncSubmitDrops` | YES (multi-writer + tick read) | **xsync.Counter** | the ONE justified xsync use: submit paths Inc(), tick reads Value() — a plain int64 would race |

**Honest outcome:** the snapshot discipline of OPT-01/02/03 made blanket xsync unnecessary. The only values crossing the async boundary are the immutable channel message, the already-atomic `idAlloc`, and the one `xsync.Counter`. This is the correct, non-over-engineered result — NOT a swap manufactured to "look productive."

## ants Pool Confirmation (OPT-04 "worker pools use ants/v2")

- `pathPool` = `runtime.NumCPU()` (heavy frequent A* — wants every core); `trackerPool`/`spawnPool` = `asyncSmallPoolSize` (2) (sparse submits).
- All bounded + recycling (no goroutine-per-mob blowup), all non-blocking (`ants.WithNonblocking(true)` via `newAsyncPool`) so a saturated Submit DROPS via `submitOrDrop` rather than stalling the tick.
- Released by `TickLoop.Close()`. No new pool created — the three are confirmed and documented.

## Task Commits

1. **Task 1: contention audit + the one justified xsync/v4 usage** - `1cf35be5` (feat)
2. **Task 2: xsync -race exercise + the no-live-capture snapshot gate** - `649c37fd` (test)

_Note: Task 2 is the TDD task; its tests assert already-holding discipline + the Task-1 counter, so it is one test commit (the gate was verified to fail-on-violation, then reverted)._

## Files Created/Modified

- `server/collections_audit.go` (created) - the audit table, the ants-pool rationale, `TickLoop.AsyncDrops()`
- `server/collections_audit_test.go` (created) - the three enforcement/exercise tests
- `server/async.go` (modified) - `asyncSubmitDrops` doc updated to the finalized one-justified-use
- `server/tick.go` (modified) - `TickStats.AsyncDrops` + `recordMSPT` republishes the counter each tick

## Decisions Made

See `key-decisions` frontmatter. The central decision: snapshot-and-stay-plain — no tick-owned map became xsync, because the OPT-01/02/03 snapshot discipline means no worker reads a live tick-owned collection (single-owner is faster). The one justified xsync use (asyncSubmitDrops) was made genuinely cross-boundary by wiring the tick to read it.

## Deviations from Plan

None - plan executed exactly as written.

The plan anticipated this exact outcome ("the default outcome is: NO blanket xsync; the audit names the single justified use"). The audit confirmed no NEW genuine contention point beyond `asyncSubmitDrops` exists, which is the plan's predicted correct result. The only additive work beyond the literal task text — wiring `asyncSubmitDrops` into `TickStats`/`TickLoop.AsyncDrops` so the tick genuinely READS the counter — was required to make it a real cross-boundary use (the plan's action 2 explicitly asks to "expose a read e.g. via Stats or a getter"), so it is in-scope, not a deviation.

## Issues Encountered

None.

## Known Stubs

None - the audit and its gate are complete and exercised; no placeholder data or unwired components.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- OPT-04 is delivered as an audited, justified decision with an enforcement gate. The snapshot-and-stay-plain conclusion is pinned against regression.
- The Docker `-race` gate is clean over `./server/...`, feeding directly into 08-07 (OPT-06, the consolidated race-safety gate).
- No blockers.

## Self-Check: PASSED

- `server/collections_audit.go` — FOUND
- `server/collections_audit_test.go` — FOUND
- Commit `1cf35be5` (feat 08-06) — FOUND
- Commit `649c37fd` (test 08-06) — FOUND
- `go test ./server/ -run 'TestHotCollections|TestNoLiveCollectionCapture|TestTickOnlyMapsStayPlain'` — PASS
- `CGO_ENABLED=0 go build ./...` + `go vet ./...` — PASS
- Docker `-race ./server/...` — clean

---
*Phase: 08-leaf-concurrency-optimizations*
*Completed: 2026-06-24*
