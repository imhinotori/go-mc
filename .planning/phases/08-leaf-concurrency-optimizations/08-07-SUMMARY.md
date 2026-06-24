---
phase: 08-leaf-concurrency-optimizations
plan: 07
subsystem: testing
tags: [race-detector, concurrency, ants, xsync, pathfinding, entity-tracker, mob-spawning, linear-region, docker, opt-06]

# Dependency graph
requires:
  - phase: 08-leaf-concurrency-optimizations (08-01)
    provides: "the async substrate — newAsyncPool (non-blocking bounded ants pools) + asyncIn2 + the 3 asyncResult contracts behind the unchanged applyAsyncResults seam"
  - phase: 08-leaf-concurrency-optimizations (08-02)
    provides: "OPT-01 async pathfinding — requestPath submits computePath to pathPool, pathReady.applyTo re-validates+adopts on the owner"
  - phase: 08-leaf-concurrency-optimizations (08-04)
    provides: "OPT-02 async entity tracker — asyncTracker submits per-player diffs to trackerPool, trackerDiffReady.applyTo emits owner-side"
  - phase: 08-leaf-concurrency-optimizations (08-05)
    provides: "OPT-03 async mob spawning — naturalSpawn submits the candidate scan to spawnPool, spawnCandidatesReady.applyTo re-checks the cap+adds on the owner"
  - phase: 08-leaf-concurrency-optimizations (08-03)
    provides: "OPT-05 .linear region codec (whole-region zstd) + the format-aware loader"
  - phase: 08-leaf-concurrency-optimizations (08-06)
    provides: "OPT-04 collections audit — snapshot-and-stay-plain; the one justified xsync.Counter (asyncSubmitDrops)"
provides:
  - "OPT-06: the combined all-subsystems Docker -race -count=10 acceptance gate proving every async subsystem race-clean by construction"
  - "TestAllAsyncSubsystemsRaceClean — pathPool + trackerPool + spawnPool driven SIMULTANEOUSLY under one running TickLoop for the cross-subsystem race boundary"
  - "the behavior-regression suite (path arrives + followed, tracker emits spawn/move/despawn, mobs spawn under cap, .linear round-trips) proving the swaps changed only WHEN, not WHAT"
  - "the documented rationale that OPT-06 is an automatable -race + behavior gate (no new wire surface), not a human-verify/capture-diff"
affects: [phase-09, regression-gates, ci]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Combined-load race gate: drive every async subsystem through one running loop simultaneously so the -race detector inspects cross-subsystem interleavings the isolated per-subsystem tests cannot surface"
    - "Behavior regression = SAME WHAT / DIFFERENT WHEN: assert observable outcomes (path arrives, tracker sends, mob spawns, region round-trips) are unchanged while the work moved off-tick"
    - "Automatable acceptance gate for internal optimizations: when no encoder/packet file changes (grep-confirmed), the existing capture-diff goldens remain the wire authority and the phase gate is -race + behavior, not a real-client verify"

key-files:
  created:
    - "server/async_stress_test.go — the combined all-subsystems stress (TestAllAsyncSubsystemsRaceClean) + the 3 behavior-regression tests"
    - "save/region/linear_bench_test.go — TestLinearRoundTripIntegration (.linear round-trip + footprint-vs-mca)"
  modified: []

key-decisions:
  - "OPT-06 is an AUTOMATABLE gate (Docker -race -count=10 + behavior regression), NOT a human-verify or a new capture-diff: grep-confirmed NO Phase-8 plan touched an encoder/packet file (entity_encode.go, world/packet.go, slot_encode.go, block_encode.go, play_join.go, login.go), so the bytes are unchanged and the existing capture-diff goldens (TestPlayBytesVsVanillaCapture, the entity/slot capture tests) remain the wire authority and stay green."
  - "The combined stress is the load-bearing proof the isolated per-subsystem tests cannot give: pathPool + trackerPool + spawnPool churn under one running TickLoop for 400 ticks, so the race detector sees the cross-subsystem pool->channel->owner boundary; -count=10 re-runs it to shake out an intermittent race."
  - "No race was found and no subsystem needed a fix — the gate is clean BY CONSTRUCTION via the single-owner rejoin discipline (snapshot-on-owner -> compute-off-tick -> apply-on-owner with target-existence re-validation). The plan is purely additive (two new test files)."

patterns-established:
  - "Combined cross-subsystem -race stress under a running loop as the phase-level concurrency acceptance bar"
  - "Behavior-regression-as-gate for additive optimizations (prove WHAT unchanged, WHEN shifted)"

requirements-completed: [OPT-06]

# Metrics
duration: 14min
completed: 2026-06-24
---

# Phase 8 Plan 07: OPT-06 Combined -race + Behavior-Regression Gate Summary

**The final Phase-8 acceptance gate: pathfinding + tracker + spawner driven SIMULTANEOUSLY under one running TickLoop pass the Docker `-race -count=10` detector clean, and the behavior regression proves the async swaps changed only WHEN work happens (off-tick), never WHAT the server does.**

## Performance

- **Duration:** 14 min
- **Completed:** 2026-06-24
- **Tasks:** 1
- **Files created:** 2

## Accomplishments

- **The combined all-subsystems stress (`TestAllAsyncSubsystemsRaceClean`)** builds a loaded 5x5-chunk world, registers 2 players with capturing clients, seeds 8 Pigs with the real `newPigAI()`, and advances the running `TickLoop` 400 ticks. Across those ticks the pipeline drives, every tick and simultaneously: `tickAI -> navigation.requestPath` submits A* to `pathPool` (OPT-01), `tickAI -> naturalSpawn` submits the candidate scan to `spawnPool` (OPT-03), `asyncTracker.Tick` submits per-player diffs to `trackerPool` (OPT-02), and `applyAsyncResults` drains `asyncIn2` on the owner — giving the `-race` detector the full cross-subsystem pool->channel->owner boundary. The test asserts real work happened (mobs spawned, the tracker tracked entities) so the gate is non-trivial, then quiesces in-flight workers before `Close()`.
- **Behavior regression proves the swaps are additive (SAME WHAT / DIFFERENT WHEN):**
  - `TestBehaviorRegressionPathArrives` — a Pig with a MOVE goal still navigates east; the off-tick path arrives 1+ ticks late via `applyAsyncResults` and is followed.
  - `TestBehaviorRegressionTrackerSends` — the async tracker still emits the full lifecycle: `AddEntity` (enter range) -> `TeleportEntity` (move) -> `RemoveEntities` (leave range), dropping the id from `tracked`.
  - `TestBehaviorRegressionMobSpawns` — under the CREATURE cap, a Pig (with a real `mobAI`) is added within a bounded number of `spawnInterval` cycles.
  - `TestLinearRoundTripIntegration` — a populated `.linear` region round-trips byte-identically and is 2.5% of the equivalent `.mca` footprint (OPT-05 intact post-integration).
- **THE PHASE GATE is GREEN:** `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/... ./world/... ./save/... -count=10` passes clean — every async subsystem `-race` clean by construction over 10 scheduler-stressed repeats. No race surfaced; no subsystem fix was needed.
- **OPT-06 is an automatable gate, not a real-client verify:** grep-confirmed no Phase-8 plan modified any encoder/packet file, so the existing capture-diff goldens remain the wire authority and stay green (verified: `TestPlayBytesVsVanillaCapture`, the entity/slot capture tests all pass).

## Task Commits

1. **Task 1: Combined all-subsystems stress + behavior regression + linear integration (OPT-06)** — `2f3cf5b5` (test)

**Plan metadata:** committed with this SUMMARY (docs).

## Files Created/Modified

- `server/async_stress_test.go` — `TestAllAsyncSubsystemsRaceClean` (the combined running-loop stress, the OPT-06 `-race` target) + `TestBehaviorRegressionPathArrives` / `...TrackerSends` / `...MobSpawns` (the additive-optimization assertions). Reuses the Phase-8 test builders (`newPhysicsLoop`/`putChunk`/`fillFloor`, `newTrackerPlayer`/`captureClient`/`drainPackets`/`drainAsyncTracker`, `newSpawnLoop`/`runSpawnCycle`, `fixedTargetGoal`, `newPigAI`).
- `save/region/linear_bench_test.go` — `TestLinearRoundTripIntegration`: the `.linear` write->read byte-identical round-trip + the `.linear` < `.mca` footprint assertion (reuses the 08-03 codec).

## Phase 8 closure — the OPT-01..06 coverage map

Every Leaf-parity async swap is behind its named existing seam (no rewrite), each worker snapshots on the owner, computes off-tick, and rejoins on the owner with target-existence re-validation:

- **OPT-01 (pathfinding, 08-02):** `requestPath` builds the immutable `pathRequest` snapshot on the tick, submits the PURE `computePath` to `pathPool`, returns immediately; `pathReady.applyTo` re-checks the mob exists + the target is unchanged before adopting the late path.
- **OPT-02 (tracker, 08-04):** `asyncTracker.Tick` snapshots per-player visibility on the owner, submits the diff MATH to `trackerPool`; `trackerDiffReady.applyTo` emits the packets + updates `p.tracked` owner-side, carrying a delta (added/removed ids) so the map stays plain.
- **OPT-03 (spawner, 08-05):** `naturalSpawn` snapshots the candidate columns' solidity on the owner, submits the scan to `spawnPool`; `spawnCandidatesReady.applyTo` re-checks the cap + the mobNear anti-piling guard against the LIVE store before adding.
- **OPT-04 (collections, 08-06):** snapshot-and-stay-plain — NO tick-owned map converted to xsync; the owner-snapshot discipline means no worker reads a live collection. `asyncSubmitDrops` (`xsync.Counter`) is the ONE justified xsync use (off-tick inc vs tick-goroutine read of `AsyncDrops` telemetry); an AST gate enforces no live cross-boundary capture.
- **OPT-05 (linear region, 08-03):** the `.linear` whole-region zstd codec + the format-aware loader — round-trips byte-identically, ~2.5% of `.mca` footprint here.
- **OPT-06 (this plan):** the combined Docker `-race -count=10` gate over `./server/... ./world/... ./save/...` is GREEN — the cross-cutting proof that every subsystem above is race-clean by construction, with the behavior regression confirming the optimizations are additive.

## Decisions Made

See `key-decisions` frontmatter. Headline: OPT-06 is automatable (no new wire surface, grep-confirmed) so the gate is `-race` + behavior-regression, not a human-verify/capture-diff; the combined cross-subsystem stress (not the isolated per-subsystem tests) is the load-bearing proof; the gate was clean by construction, so the plan is purely additive (two test files, no subsystem fix).

## Deviations from Plan

None - plan executed exactly as written. The combined `-race -count=10` gate was clean on the first run and stayed clean across all 10 repeats, so no subsystem needed a `fix(08-07)` — the single-owner rejoin discipline made it race-clean by construction, exactly as the plan predicted.

## Issues Encountered

None. `go build ./...`, `go vet ./...`, the native `go test ./...` (all packages), and the Docker `-race ./server/... ./world/... ./save/... -count=10` gate all passed cleanly. The capture-diff goldens stayed green (wire surface unchanged).

## User Setup Required

None - no external service configuration required. (The `-race` gate runs in the `golang:1.26` Docker image because the host is `CGO_ENABLED=0`; Docker + the image were already present.)

## Next Phase Readiness

- **Phase 8 (Leaf concurrency optimizations) is COMPLETE.** All of OPT-01..06 have landed and the combined `-race -count=10` acceptance bar is green. The server's async subsystems (pathfinding, entity tracker, mob spawner) are proven race-clean under combined load, and the optimizations are proven additive (no observable behavior change, no wire surface change).
- Ready to proceed to the next phase. No blockers. The behavior-regression + combined-race tests are now permanent CI gates guarding any future change to the async subsystems.

## Self-Check: PASSED

- `server/async_stress_test.go` — FOUND
- `save/region/linear_bench_test.go` — FOUND
- `.planning/phases/08-leaf-concurrency-optimizations/08-07-SUMMARY.md` — FOUND
- commit `2f3cf5b5` (test(08-07) combined gate) — FOUND
- `go build ./...` exit 0, `go vet ./...` clean, `go test ./...` green, Docker `-race -count=10` over `./server/... ./world/... ./save/...` GREEN, capture-diff goldens green.

---
*Phase: 08-leaf-concurrency-optimizations*
*Completed: 2026-06-24*
