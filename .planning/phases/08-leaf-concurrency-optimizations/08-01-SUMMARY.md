---
phase: 08-leaf-concurrency-optimizations
plan: 01
subsystem: infra
tags: [ants, xsync, goroutine-pool, concurrency, async, tick-loop, race]

# Dependency graph
requires:
  - phase: 04-world-streaming
    provides: "the proven chunkReady rejoin (asyncResult interface + applyAsyncResults seam + SetWorld/asyncBridge bounded-channel discipline) this plan generalizes"
  - phase: 06-entities
    provides: "the tick-owned entityStore (get/near) the result contracts re-validate against on apply"
  - phase: 07-ai-pathfinding-commands-chat
    provides: "the pure computePath over an immutable pathRequest snapshot (the OPT-01 hinge pathReady carries)"
provides:
  - "newAsyncPool: the per-subsystem NON-BLOCKING bounded ants/v2 pool factory (Submit drops on ErrPoolOverload, never stalls the tick)"
  - "submitOrDrop + asyncSubmitDrops (xsync.Counter): centralized drop-on-overload submit discipline with backpressure observability"
  - "the three asyncResult CONTRACTS pathReady / trackerDiffReady / spawnCandidatesReady (each carries an id/value not a live pointer, re-validates on the owner, ships a documented validate-then-no-op applyTo stub)"
  - "asyncIn2: the SECOND bounded rejoin channel + pathPool/trackerPool/spawnPool wired into NewTickLoop, drained on the owner by the UNCHANGED applyAsyncResults seam; TickLoop.Close() releases the pools"
  - "the async substrate -race stress scaffold (pool -> channel -> owner boundary, Docker -race clean)"
affects: [08-02-async-pathfinding, 08-04-async-tracker, 08-05-async-spawning, 08-06-hot-collections]

# Tech tracking
tech-stack:
  added:
    - "github.com/panjf2000/ants/v2 v2.12.1 (bounded recycling goroutine pool)"
    - "github.com/puzpuzpuz/xsync/v4 v4.5.0 (CLHT Map + Counter + MPMCQueue; first use: asyncSubmitDrops Counter)"
  patterns:
    - "per-subsystem non-blocking ants pool (one pool per async subsystem, drop-on-overload)"
    - "asyncResult contract: id/value (never a live pointer) + on-owner validity re-check + applyTo on the owner via asyncIn2"
    - "second rejoin channel additive behind the unchanged applyAsyncResults seam (no pipeline reorder)"

key-files:
  created:
    - "server/async.go - newAsyncPool, submitOrDrop, asyncSubmitDrops, the 3 asyncResult contracts + applyTo stubs, playerByEntityID"
    - "server/async_test.go - TestAsyncPoolNonBlocking, TestSubmitOrDrop*, TestAsyncRejoinRaceClean (-race target)"
  modified:
    - "server/tick.go - asyncIn2 + pathPool/trackerPool/spawnPool fields, constructed in NewTickLoop, released by Close()"
    - "server/tick_phases.go - applyAsyncResults extended to drain BOTH asyncIn and asyncIn2 on the owner (slot/order unchanged)"
    - "go.mod / go.sum - ants/v2 + xsync/v4"

key-decisions:
  - "asyncIn2 is a SECOND channel, NOT a replacement for asyncIn — keeps the Phase-4 chunkReady/SetWorld wiring (which tests depend on) untouched; applyAsyncResults drains both on the owner, purely additive (no pipeline reorder, TestTickPhaseOrder passes)."
  - "Pools constructed in NewTickLoop (not globals): pathPool=runtime.NumCPU() (heavy/frequent path compute), trackerPool/spawnPool=2 (sparse submits); TickLoop.Close() releases them idempotently/nil-safe."
  - "xsync introduced JUSTIFIED-PER-USE only (08-RESEARCH Pitfall 1): the single Wave-0 use is asyncSubmitDrops (xsync.Counter), a value genuinely written across the async boundary — NOT a blanket map swap (tick-only maps stay plain). OPT-04/08-06 extends the xsync surface where contention warrants."
  - "Non-blocking pools (ants.WithNonblocking) mirror world.Worker.Request's drop-on-full: a saturated Submit returns ErrPoolOverload and the work is DROPPED (subsystem keeps last action, re-requests next tick), never a blocking Submit that would stall the tick (Pitfall 4 / T-8-02)."
  - "The 3 result contracts carry an id/value and re-validate existence on apply (Pitfall 2/3): a late result for a despawned mob / departed player / empty scan is dropped on the owner, never crashed — proven by the rejoin scaffold submitting pathReady for a non-existent mob id."

patterns-established:
  - "newAsyncPool(size) -> non-blocking bounded ants pool; submitOrDrop(pool, work) -> the shared drop-on-overload submit site every OPT plan calls"
  - "asyncResult implementor: {id/value fields} + applyTo(*TickLoop) that re-resolves the target on the owner, drops if gone/changed, then mutates owner-side"
  - "rejoin via asyncIn2 drained in applyAsyncResults — the chunkReady discipline generalized to in-process compute pools"

requirements-completed: [OPT-04, OPT-06]

# Metrics
duration: 25min
completed: 2026-06-24
---

# Phase 8 Plan 01: Async Substrate (Wave 0) Summary

**The Phase-8 async substrate: a non-blocking bounded ants/v2 pool factory (drop-on-overload), the three asyncResult contracts (pathReady / trackerDiffReady / spawnCandidatesReady), and a second rejoin channel (asyncIn2) wired behind the UNCHANGED applyAsyncResults seam — proven -race clean before any executor is swapped.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-06-24T17:06Z
- **Completed:** 2026-06-24T17:31Z
- **Tasks:** 2
- **Files modified:** 6 (2 created, 4 modified)

## Accomplishments

- **Introduced the Phase-8 concurrency stack for the first time:** `ants/v2 v2.12.1` + `xsync/v4 v4.5.0` in `go.mod` (the ONLY new deps; no cgo dep; `CGO_ENABLED=0` build clean; `go mod tidy` idempotent).
- **`newAsyncPool` + `submitOrDrop`:** the per-subsystem bounded, NON-BLOCKING ants pool factory and the centralized drop-on-overload submit discipline (saturated Submit returns `ErrPoolOverload` → work dropped, tick never stalls), with `asyncSubmitDrops` (xsync.Counter) for backpressure observability — the JUSTIFIED-PER-USE xsync introduction.
- **The three asyncResult contracts** (`pathReady`/`trackerDiffReady`/`spawnCandidatesReady`): each implements the UNCHANGED `asyncResult interface{ applyTo(*TickLoop) }`, carries an id/value (never a live pointer), re-validates existence on the owner (drop-on-despawn/depart/empty), and ships a documented validate-then-no-op `applyTo` stub for OPT-01 (08-02) / OPT-02 (08-04) / OPT-03 (08-05) to fill.
- **`asyncIn2` + the three pools wired into `NewTickLoop`** (pathPool=NumCPU, tracker/spawn=2) and released by `TickLoop.Close()`; `applyAsyncResults` extended to drain BOTH `asyncIn` (Phase-4 chunkReady) AND `asyncIn2` (Phase-8 results) on the owner, non-blocking — **slot and pipeline order UNCHANGED** (TICK-05 preserved; `TestTickPhaseOrder` passes).
- **The -race stress scaffold:** `TestAsyncRejoinRaceClean` submits 200 closures to the path pool that rejoin on `asyncIn2` while the tick advances and drains — the pool→channel→owner boundary is **Docker `-race` clean (`-count=20`)**, so OPT-01/02/03 inherit a proven-clean substrate.

## Task Commits

1. **Task 1 (deps):** `13644d8` (build) — ants/v2 + xsync/v4
2. **Task 1 (substrate):** `4b06c2d` (feat) — newAsyncPool + submitOrDrop + the 3 asyncResult contracts + playerByEntityID
3. **Task 2 (wiring):** `bb98527` (feat) — asyncIn2 + pools in NewTickLoop + Close + applyAsyncResults dual-drain
4. **Task 2 (tests):** `264f70e` (test) — async substrate -race stress scaffold

## Files Created/Modified

- `server/async.go` (created) — the Phase-8 substrate: `newAsyncPool` (non-blocking bounded ants pool), `submitOrDrop` + `asyncSubmitDrops` (xsync.Counter), the `pathReady`/`trackerDiffReady`/`spawnCandidatesReady` contracts with documented `applyTo` stubs, and the `playerByEntityID` owner-side re-resolve helper.
- `server/async_test.go` (created) — `TestAsyncPoolNonBlocking` (overload drops, not blocks), `TestSubmitOrDropDropsWhenSaturated` / `TestSubmitOrDropNilPool`, `TestAsyncRejoinRaceClean` (the OPT-06 -race target).
- `server/tick.go` (modified) — added `asyncIn2` + `pathPool`/`trackerPool`/`spawnPool` fields, the `asyncIn2Buffer`/`asyncSmallPoolSize` consts, their construction in `NewTickLoop`, and `Close()`.
- `server/tick_phases.go` (modified) — `applyAsyncResults` now drains both channels on the owner, non-blocking, without touching the slot or order.
- `go.mod` / `go.sum` (modified) — ants/v2 v2.12.1 + xsync/v4 v4.5.0.

## Decisions Made

See `key-decisions` frontmatter. The load-bearing ones: asyncIn2 is a second channel (not a replacement) to keep Phase-4 wiring untouched; pools live on the TickLoop (not globals); xsync is introduced justified-per-use (asyncSubmitDrops Counter), not a blanket swap; non-blocking pools drop-on-overload to never stall the tick; result contracts carry id/value and re-validate on apply.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added the `playerByEntityID` owner-side re-resolve helper**
- **Found during:** Task 1 (trackerDiffReady.applyTo stub)
- **Issue:** The plan's `trackerDiffReady.applyTo` stub specifies "re-resolve the player by entityID over t.players", but no such lookup helper existed in the codebase (players are keyed by `*Client` in `clientIndex`, not by entity id).
- **Fix:** Added `func (t *TickLoop) playerByEntityID(id int32) *tickPlayer` — a tick-owned linear scan over `t.players` returning nil for a departed player, which is exactly the Pitfall-3 existence re-check the contract needs. Player counts are small and `applyTo` runs at most once per result on the owner, so a linear scan is correct and cheap.
- **Files modified:** `server/async.go`
- **Verification:** `go build`/`go vet` clean; `TestAsyncRejoinRaceClean` exercises the result-apply path under -race.
- **Committed in:** `4b06c2d` (Task 1 feat commit)

---

**Total deviations:** 1 auto-fixed (1 blocking).
**Impact on plan:** Necessary to satisfy the documented trackerDiffReady contract; no scope creep — it is the exact helper the plan's stub describes, just made concrete because the codebase had no entity-id→player lookup.

## Issues Encountered

- `go mod tidy` initially dropped both new deps because no import referenced them yet (deps-first, code-second ordering). Resolved by creating `server/async.go` (which imports both ants and xsync) before the final `go get` + `go mod tidy` — both deps then stuck. This is expected Go module behavior, not a defect.

## Verification Results

- `go test ./server/ -run 'TestAsyncPoolNonBlocking|TestAsyncRejoinRaceClean|TestTickPhaseOrder' -count=1` — **PASS** (TestTickPhaseOrder confirms no pipeline reorder).
- Full suite `go test ./server/... ./world/... ./save/...` — **PASS** (nothing regressed; additive plan).
- `go mod tidy && go vet ./... && CGO_ENABLED=0 go build ./...` — **clean**; only ants/v2 + xsync/v4 added (no cgo dep).
- Docker `-race` over `./server/...` `-count=1` — **clean**; the async substrate targeted tests `-race -count=20` — **clean** (the pool→channel→owner boundary holds under repeated concurrent stress).

## User Setup Required

None — no external service configuration required.

## Known Stubs

The three `applyTo` methods are **intentional, documented contract stubs** — the explicit deliverable of this contract-first Wave-0 plan, NOT unresolved work. Each performs its owner-side validity re-check (proving the despawn/depart/empty drop path) and then no-ops with a documented placeholder citing the plan that fills it:

| Stub | File | Filled by |
|------|------|-----------|
| `pathReady.applyTo` (re-check mob exists → no-op) | `server/async.go` | 08-02 (OPT-01) — assigns `nav.path = r.path` on the owner |
| `trackerDiffReady.applyTo` (re-resolve player → no-op) | `server/async.go` | 08-04 (OPT-02) — `p.client.Send` per packet on the owner |
| `spawnCandidatesReady.applyTo` (non-empty/store re-check → no-op) | `server/async.go` | 08-05 (OPT-03) — cap re-check + `entityStore.add` on the owner |

These are correct for the plan's goal (lay the substrate + contracts every later wave fills); the substrate itself — the pools, the channel, the drain, the validity re-checks — is fully live and -race proven.

## Next Phase Readiness

- **Wave 0 substrate complete.** OPT-04 (pool substrate) and OPT-06 (proven-clean rejoin) begin here: ants/v2 + xsync/v4 in go.mod, `newAsyncPool` (non-blocking bounded), the 3 asyncResult contracts, and `asyncIn2` wired behind the unchanged `applyAsyncResults` seam, all -race clean.
- **OPT-01/02/03 (08-02/04/05) inherit:** live per-subsystem pools (`pathPool`/`trackerPool`/`spawnPool`), the `submitOrDrop` drop-on-overload helper, the `asyncIn2` rejoin channel drained on the owner, and the three result-type contracts to implement `applyTo` against — no codebase scavenger hunt, just fill the documented stubs and add the submit sites.
- **OPT-05 (`.linear`) and OPT-04's broader xsync surface** are untouched by this plan and proceed independently; `klauspost/compress` lands with OPT-05 (08-03), not here.
- No blockers.

## Self-Check: PASSED

- Created files exist: `server/async.go`, `server/async_test.go`, `.planning/phases/08-leaf-concurrency-optimizations/08-01-SUMMARY.md` — all FOUND.
- Task commits exist: `13644d8` (build deps), `4b06c2d` (substrate feat), `bb98527` (wiring feat), `264f70e` (test) — all FOUND.
- Plan `<verification>` re-run: target tests PASS, full suite PASS, `go mod tidy`/`vet`/`build` clean (only ants/v2 + xsync/v4 added), Docker `-race` over `./server/...` clean (`-count=20` on the substrate tests).

---
*Phase: 08-leaf-concurrency-optimizations*
*Completed: 2026-06-24*
