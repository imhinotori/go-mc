---
phase: 27-folia-regionization
plan: 02
subsystem: infra
tags: [regionization, folia, tick-loop, concurrency, conc, barrier, coordinator, fan-out]

# Dependency graph
requires:
  - phase: 27-01-region-struct-extraction
    provides: the region struct at N=1 (entities/world/blockTicks/levelRandom) + t.only()/t.region(id) the coordinator fans out
  - phase: 08-async-substrate
    provides: the asyncIn2 + ants pools the per-region applyAsyncResults drains (unchanged at N=1)
provides:
  - "conc (github.com/sourcegraph/conc v0.3.0) added — pure-Go, CGO=0 confirmed (no cgo in go list -deps)"
  - "region.tick(gt): the per-region pipeline (tickWorld -> tickChunks -> tickEntities -> tickAI -> tickPhysics -> applyAsyncResults) in the existing order, gt read-only"
  - "tickOnce = the Folia coordinator: resolveSubtickInputs -> read shared gt -> conc.WaitGroup.Go(r.tick) per region -> wg.Wait() BARRIER -> cross-region post-phase (movement/equipment/tracker/flush) -> gametime++ EXACTLY once -> on_tick -> recordMSPT"
  - "recoverTick: hoisted panic backstop; a region panic re-raised by wg.Wait is caught, logged, gametime advanced exactly-once (never double), the tick survives"
  - "Behavior-neutral at N=1 PROVEN: the FULL existing server + world + plugin suite passes UNCHANGED, Docker -race ./server/ ./plugin/... clean"
affects: [27-03-N2-cross-region-transfer, folia regionization]

# Tech tracking
tech-stack:
  added:
    - "github.com/sourcegraph/conc v0.3.0 (pure-Go, CGO=0 — the per-tick fan-out/join barrier with structured panic propagation)"
  patterns:
    - "Coordinator fan-out/barrier (27-RESEARCH Pattern 2): tickOnce fans out region.tick via conc.WaitGroup, joins at wg.Wait, runs the cross-region post-phase quiescent"
    - "Shared 50ms gametime anchor advanced EXACTLY once per tick by the coordinator (Pitfall 3); regions read gt read-only and never advance it"
    - "Structured per-region panic isolation: conc recovers each region's panic and re-raises on Wait(); the hoisted recoverTick backstop survives it (T-27-02)"

key-files:
  created:
    - server/region_coordinator.go
    - server/region_coordinator_test.go
  modified:
    - go.mod
    - go.sum
    - server/region.go
    - server/tick_phases.go

key-decisions:
  - "applyAsyncResults stays UNCHANGED (drains both asyncIn + asyncIn2) and is called INSIDE region.tick — at N=1 only()==r so the global asyncIn2 drain still lands on the only region. This keeps every existing applyAsyncResults caller/test passing and the phase trace byte-identical. Plan 03 routes asyncIn2 to the OWNING region."
  - "The phase trace order is byte-identical: region.tick runs the per-region phases (tickWorld..applyAsyncResults), the coordinator runs the post-phase (tickEntityMovement/tickEquipment/tracker.Tick/flushOutbound) — same observable order TestTickPhaseOrder asserts, UNCHANGED."
  - "recoverTick uses an `advanced *bool` flag to make the gametime advance EXACTLY once: the normal path sets advanced=true after gametime++, so the panic path only advances if the panic preceded the normal advance (never double-advance — T-27-02-GT)."
  - "region.tickHook is a test-only seam (nil in production) for panic injection (TestRegionPanicIsolated) and barrier observation (TestCoordinatorBarrier), mirroring the existing applyInputHook/phaseTrace zero-cost test seams."

patterns-established:
  - "The explicit per-region (region.tick, steps 3-4) vs cross-region/global (coordinator post-phase, step 5) seam is the boundary Plan 03 fills with N=2 cross-region transfer + the cross-region tracker"

requirements-completed: [REGION-01]

# Metrics
duration: ~6min
completed: 2026-06-28
---

# Phase 27 Plan 02: Add the conc fan-out/barrier coordinator at N=1 (behavior-neutral) Summary

**tickOnce restructured into the Folia coordinator shape — drain (global) -> fan out the region tick(s) via conc -> BARRIER (wg.Wait) -> cross-region/global post-phase on the coordinator -> advance the shared gametime EXACTLY once -> on_tick — WHILE STAYING N=1, so the world ticks IDENTICALLY to before. The barrier discipline, the global/region split, and the shared-anchor invariant are proven with ZERO behavior change, the seam Plan 03 flips to N=2.**

## Performance

- **Duration:** ~6 min (active), plus the Docker -race verification (~25s in-container)
- **Completed:** 2026-06-28T21:22:48Z
- **Tasks:** 3
- **Files modified:** 4 modified + 2 created

## Accomplishments
- `go get github.com/sourcegraph/conc@v0.3.0` + `go mod tidy`; confirmed pure-Go (no cgo in `go list -deps github.com/sourcegraph/conc`) and `CGO_ENABLED=0 go build ./...` stays clean with conc in the graph.
- `server/region_coordinator.go`: `region.tick(gt)` runs the PER-REGION pipeline (tickWorld -> tickChunks -> tickEntities -> tickAI -> tickPhysics -> applyAsyncResults) in the EXACT existing order; `gt` is read-only inside the region (the region never advances gametime).
- `tickOnce` rewritten as the coordinator: `resolveSubtickInputs` (global) -> read the ONE shared `gt` -> `conc.WaitGroup.Go(func(){ r.tick(gt) })` per region -> `wg.Wait()` BARRIER -> the cross-region post-phase (tickEntityMovement, tickEquipment, tracker.Tick, flushOutbound) -> `gametime++` EXACTLY once -> on_tick Emit -> recordMSPT.
- `recoverTick` (hoisted from the old inline tickOnce recover): the outermost deferred backstop catches a panic from any phase — including one re-raised by `wg.Wait()` from a region goroutine (conc re-panics on the coordinator goroutine) — logs the stack, advances gametime exactly-once via the `advanced` flag, and records MSPT. A region panic is ISOLATED: the tick does not hang, the loop survives, gametime still advances.
- 3 new tests (TestCoordinatorBarrier, TestSharedGameTimeAdvancedOnce, TestRegionPanicIsolated) pass; the FULL existing server + world + plugin suite passes UNCHANGED (behavior-neutral N=1, incl. TestTickPhaseOrder byte-identical); Docker `-race ./server/ ./plugin/...` is GREEN (no race reports).

## Task Commits

1. **Task 1: Add conc + the region.tick per-region pipeline** - `8c742828` (feat)
2. **Task 2: tickOnce = fan-out -> barrier -> post-phase -> advance-once (TDD)** - `b436bc3f` (feat)
3. **Task 3: -race gate** - no code change required (the Docker -race gate passed CLEAN over the Task-1/2 coordinator; CGO=0 build clean with conc, no cgo in the graph — nothing to fix, no regression test needed).

_Note: Task 2 is the TDD task. The coordinator implementation landed in Task 1 (region_coordinator.go); Task 2 added the three behavior tests (the RED specs) which passed GREEN against it, plus the region.tickHook test seam. The load-bearing check — the FULL existing suite passing UNCHANGED — is the N=1 behavior-neutral proof._

## Files Created/Modified
- `server/region_coordinator.go` (created) - region.tick(gt) the per-region pipeline + tickOnce the conc coordinator (fan-out/barrier/post-phase/advance-once) + recoverTick the hoisted panic backstop.
- `server/region_coordinator_test.go` (created) - TestSharedGameTimeAdvancedOnce (region reads pre-advance gt, coordinator advances +1), TestCoordinatorBarrier (post-phase runs after the region tick joins), TestRegionPanicIsolated (a region panic is recovered, the tick survives + still advances).
- `go.mod` / `go.sum` - conc v0.3.0 added (pure-Go).
- `server/region.go` - added the `tickHook func()` test-only seam to the region struct (nil in production).
- `server/tick_phases.go` - removed the old inline tickOnce (moved to the coordinator); dropped the now-unused log/runtime-debug/host imports; applyAsyncResults + the phase methods UNCHANGED.

## Decisions Made
- **applyAsyncResults stays UNCHANGED and runs INSIDE region.tick.** It still drains BOTH asyncIn (per-region chunkReady) and asyncIn2 (global path/tracker/spawn). At N=1 only()==r, so the global drain still lands on the only region — keeping every existing applyAsyncResults caller + test passing AND the phase trace byte-identical. Plan 03 routes asyncIn2 results to the OWNING region (Pitfall 1).
- **The phase trace order is byte-identical.** region.tick runs the per-region phases; the coordinator runs the post-phase. TestTickPhaseOrder's expected trace is UNCHANGED — the structural fan-out/barrier split did not reorder any observable gameplay phase.
- **Exactly-once gametime advance via an `advanced *bool` flag.** The normal path sets advanced=true after gametime++; recoverTick advances only if the panic preceded the normal advance — so gametime advances exactly once whether or not a region panicked, never twice (T-27-02-GT).

## Deviations from Plan
None — the plan executed as written. Task 3 surfaced no race (the gate was clean), so no regression test was needed and Task 3 added no code (its deliverable is the green gate itself).

## Issues Encountered
- **`go mod tidy` removed conc after Task 1's `go get` because nothing imported it yet.** Adding the import in region_coordinator.go then re-running `go get` + `go mod tidy` restored it; the second tidy is stable (conc is now a real import). No impact — caught by the CGO=0 build at the end of Task 1.

## Threat Surface
No new external attack surface — this is an internal concurrency refactor (no new network input, auth, or untrusted parsing). The STRIDE register's four threats are all MITIGATED + proven:
- T-27-02 (a region panic hangs the tick): conc per-region recover + recoverTick backstop — TestRegionPanicIsolated.
- T-27-02-GT (gametime drift/double-advance): region reads gt read-only; coordinator advances exactly-once via the `advanced` flag — TestSharedGameTimeAdvancedOnce.
- T-27-02-BAR (post-phase reads a region mid-tick): wg.Wait is the barrier — TestCoordinatorBarrier.
- T-27-02-CGO (conc pulls cgo): confirmed pure-Go, no cgo in `go list -deps`, CGO=0 build clean.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The coordinator fan-out/barrier + the per-region/global/post-phase split are in place at N=1. Plan 03 flips to N=2: a static chunk->region hash, cross-region entity TRANSFER at the barrier, the cross-region tracker read, and the region-aware plugin Emit.
- The explicit seam Plan 03 fills: region.tick (per-region) vs the coordinator post-phase (cross-region). applyAsyncResults still drains the global asyncIn2 on the single region; Plan 03 routes those results to the OWNING region (Pitfall 1).
- The `world` race-gate timeout (carried from 27-01's deferred-items) should be raised/sharded before the Plan 03 N=2 full `-race ./server/ ./world/...` gate.

---
*Phase: 27-folia-regionization*
*Completed: 2026-06-28*

## Self-Check: PASSED

- server/region_coordinator.go — FOUND
- server/region_coordinator_test.go — FOUND
- .planning/phases/27-folia-regionization/27-02-SUMMARY.md — FOUND
- Commit 8c742828 (Task 1) — FOUND
- Commit b436bc3f (Task 2) — FOUND
