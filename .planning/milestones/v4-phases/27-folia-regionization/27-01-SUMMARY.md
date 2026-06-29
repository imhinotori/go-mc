---
phase: 27-folia-regionization
plan: 01
subsystem: infra
tags: [regionization, folia, tick-loop, concurrency, refactor, single-owner, TICK-05]

# Dependency graph
requires:
  - phase: 08-async-substrate
    provides: the asyncResult/applyTo rejoin discipline (id-carry, re-resolve on owner, drop-if-gone) the region routing reuses
  - phase: 23-thin-id-handles
    provides: the thin-id entity/world/nav handles that re-resolve through the store (now the region's store)
  - phase: 24-vanilla-mobs-as-plugins
    provides: the frozen plugin registry + the working single-owner plugin/entity seam being re-homed
provides:
  - "A `region` struct that owns the WORLD-half of the tick state (entities, world+worker, chunkReady bridge, block/fluid scheduled ticks, levelRandom, spawn-scan gate)"
  - "The explicit, documented per-region vs global boundary contract (region.go)"
  - "TickLoop.regions slice + region(id)/only() accessors; N=1 (one region == today's single-owner loop)"
  - "Behavior-neutral extraction proven: the FULL existing server + world + plugin suite passes UNCHANGED, Docker -race clean"
affects: [27-02-coordinator-barrier, 27-03-N2-cross-region-transfer, folia regionization]

# Tech tracking
tech-stack:
  added: []   # NO new dependency — conc lands in Plan 02
  patterns:
    - "Region-as-a-slice-of-the-single-owner-loop (27-RESEARCH Pattern 1): the region holds the per-world fields; TickLoop keeps the global/coordination fields"
    - "N=1 accessor seam: t.only() returns the single region so the field move re-points call sites without changing the single-sourced store"

key-files:
  created:
    - server/region.go
    - server/region_test.go
  modified:
    - server/tick.go
    - server/tick_phases.go
    - server/async.go
    - server/tracker.go
    - server/plugin_entity.go
    - server/python_bridge.go
    - server/persistence.go

key-decisions:
  - "Use a method seam (t.only()) instead of a duplicate store: the region is the SOLE holder of entities/world/etc; TickLoop reaches them only through only()/region(), so the store stays single-sourced (no aliased store to drift)"
  - "Alias the save/region import to `regionfile` (the unqualified `region` identifier is now the per-region tick-owner type)"
  - "Mechanical field-move via a bounded perl rewrite over the confirmed TickLoop receiver set (t, h.t, loop, loop2, refLoop, goLoop, asyncLoop, dst) — no logic/ordering/numeric change"

patterns-established:
  - "Per-region/global boundary documented as a contract in region.go (the design of the whole phase)"
  - "Per-region levelRandom is NEVER shared (newRegion seeds a fresh LegacyRandomSource; test asserts distinct pointers)"

requirements-completed: [REGION-01]

# Metrics
duration: ~50min
completed: 2026-06-28
---

# Phase 27 Plan 01: Extract the region struct at N=1 (behavior-neutral) Summary

**The WORLD-half of TickLoop (entityStore, ChunkManager+worker, chunkReady bridge, scheduled block/fluid ticks, levelRandom, spawn-scan gate) extracted onto a `region` struct at N=1, where the single region behaves IDENTICALLY to the pre-extraction single-owner loop — the safety net the rest of Folia regionization rests on.**

## Performance

- **Duration:** ~50 min (active), plus long-running Docker -race verification
- **Completed:** 2026-06-28T21:12:16Z
- **Tasks:** 3
- **Files modified:** 62 (server package field-move footprint) + 2 created

## Accomplishments
- `server/region.go`: the `region` struct (9 per-region fields + id + coord back-ref), `newRegion`, `regionID`/`globalRegion`, and the explicit per-region vs global boundary contract as a doc comment.
- The world-owned fields moved off `TickLoop` onto the single region; `NewTickLoop` builds `regions = []*region{newRegion(globalRegion, t)}` and wires the per-region store + (never-shared) levelRandom there.
- Every per-region read/write re-pointed through `t.only().<field>` mechanically (the full pipeline, SetWorld/chunkReady/drainRegistrations, the async rejoin, the tracker, the thin-id handles, the spawner/structure/fluid/sugar-cane call sites) — a PURE field move, no logic change.
- Behavior-neutral PROVEN: the FULL existing server + world + plugin suite passes UNCHANGED (no test needed a behavior edit), and the Docker `-race ./server/` gate is GREEN (~19s).

## Task Commits

1. **Task 1: Define the region struct + per-region/global boundary** - `99e373e0` (feat)
2. **Task 2: Move world-owned fields onto the single region; re-point phase loops (TDD)** - `5070f649` (feat)
3. **Task 3: -race gate — prove the extraction is race-clean** - `bbfbf458` (test)

_Note: Task 2 is the TDD task; its RED test (TestRegionExtractionBehaviorNeutral) was written first (failed to compile: `loop.regions undefined`), then made GREEN by the field move. Both landed in `5070f649`._

## Files Created/Modified
- `server/region.go` (created) - the `region` struct + `newRegion` + the per-region/global boundary contract (143 lines).
- `server/region_test.go` (created) - TestRegionStructHasPerRegionFields (distinct RNG pointers), TestRegionExtractionBehaviorNeutral (N=1 == today), TestExtractionRaceClean (the -race regression backstop).
- `server/tick.go` - removed the 9 per-region fields; added `regions []*region`; built the single region in NewTickLoop; added `region(id)`/`only()`; dropped the now-unused `level/ticks` + `world/levelgen` imports.
- `server/tick_phases.go`, `server/async.go`, `server/tracker.go`, `server/plugin_entity.go`, `server/python_bridge.go`, `server/spawner.go`, `server/structure_spawn.go`, `server/fluid*.go`, `server/sugar_cane.go`, etc. - per-region call sites re-pointed to `t.only().<field>` (mechanical).
- `server/persistence.go` - aliased `save/region` import to `regionfile` (name collision with the new `region` type).

## Decisions Made
- **Single-sourced store via the only() seam, not a duplicate field.** The region owns the store; TickLoop reads it only through `only()`/`region()`. Avoids the T-27-EXT-1 aliased-store risk; TestRegionExtractionBehaviorNeutral asserts the same store backs the phase loop and the accessor.
- **Aliased `save/region` → `regionfile`.** The unqualified `region` identifier is now the central per-region tick-owner type; the anvil region-FILE IO import is renamed in persistence.go.
- **Bounded mechanical rewrite.** The field move was applied via a perl transform anchored on the confirmed TickLoop receiver set, then verified by `go build ./...` + `go vet` + the full suite — keeping it a literal field move with zero logic/ordering/numeric drift.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `region` type name collided with the `save/region` import**
- **Found during:** Task 1 (defining `type region struct`)
- **Issue:** `server/persistence.go` imports `github.com/imhinotori/sulfur/save/region` under the bare name `region`; the new package-scope `region` type made the build fail (`region already declared through import`).
- **Fix:** Aliased the import to `regionfile` and rewrote its `region.At/In/Open/Create/Region/ErrNoSector/ErrNoData` usages accordingly. No behavior change (pure rename).
- **Files modified:** server/persistence.go
- **Verification:** `CGO_ENABLED=0 go build ./server/` clean; persistence tests pass in the full suite.
- **Committed in:** `99e373e0` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** The alias was required for the new type to compile; pure rename, no scope creep. The extraction otherwise executed exactly as written.

## Issues Encountered
- **The `world` -race suite exceeds the 900s gate timeout under the race detector.** Task 3's full `-race ./server/ ./world/...` run reported a FAIL that was a 901s TIMEOUT dump (an all-goroutines dump rooted in `world/structure` stronghold ring generation), NOT a `WARNING: DATA RACE`. Resolution: (a) `./server/` -race — the load-bearing gate for this phase — is CLEAN in ~19s; (b) `world` imports nothing from `server` and zero `world/**` files were touched, so the extraction cannot have changed its race behavior; (c) re-running `./world/` with `-timeout 2400s` passes CLEAN at 2054.98s, proving it is a timeout (the instrumented worldgen suite needs ~2055s under race), not a race. Logged to `deferred-items.md` as a pre-existing race-gate timeout (raise the world gate timeout or shard it in a future ops plan). Out of scope for 27-01.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The `region` struct + the per-region/global boundary are in place; Plan 02 adds the coordinator fan-out/barrier (introducing `conc`) over the single region (still N=1, still serial/green).
- The thin-id handles (plugin_entity.go) still carry `*TickLoop` and re-resolve through `t.only().entities` — Plan 03 re-points them at the OWNING region when N=2 lands.
- `levelRandom` is per-region and never-shared from day one; the cross-region tracker / transfer / N=2 hash remain for Plan 03.
- The `world` race-gate timeout should be raised (or sharded) before the Plan 03 N=2 gate so the full `-race ./server/ ./world/...` run completes cleanly in one pass.

---
*Phase: 27-folia-regionization*
*Completed: 2026-06-28*

## Self-Check: PASSED

- server/region.go — FOUND
- server/region_test.go — FOUND
- .planning/phases/27-folia-regionization/27-01-SUMMARY.md — FOUND
- .planning/phases/27-folia-regionization/deferred-items.md — FOUND
- Commit 99e373e0 (Task 1) — FOUND
- Commit 5070f649 (Task 2) — FOUND
- Commit bbfbf458 (Task 3) — FOUND
