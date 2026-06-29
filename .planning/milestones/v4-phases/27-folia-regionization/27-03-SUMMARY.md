---
phase: 27-folia-regionization
plan: 03
subsystem: infra
tags: [regionization, folia, N2, cross-region-transfer, async-rejoin, region-aware-emit, tracker, concurrency, REGION-01]

# Dependency graph
requires:
  - phase: 27-01-region-struct-extraction
    provides: the region struct (entities/world/levelRandom/blockTicks) + t.only()/t.region(id) the N=2 flip multiplies
  - phase: 27-02-coordinator-barrier
    provides: the conc fan-out/barrier coordinator + region.tick + the cross-region post-phase the N=2 flip fills
  - phase: 23-thin-id-handles
    provides: the thin-id entity/world/nav handles that now re-resolve against the OWNING region's store
  - phase: 24-vanilla-mobs-as-plugins
    provides: the frozen plugin registry + the declared-mob goal path whose callback is THE region-01 gate
provides:
  - "regionOf: a PURE, restart-stable (X^Z)&1 checkerboard chunk->region hash (regionCount=2); regionForColumn/regionForEntity/owningRegion routing"
  - "N=2: NewTickLoop builds 2 regions; a per-goroutine currentRegion (xsync.Map) registry routes the ~200 t.only() per-region call sites to the OWNING region across the conc fan-out with ZERO churn"
  - "2 regions tick CONCURRENTLY: region.tick fans out tickAI+tickPhysics per region; world-global phases + the async rejoin run on the coordinator (proven by TestTwoRegionsTickInParallel's simultaneity probe)"
  - "cross-region entity TRANSFER at the barrier: detectTransfers queues boundary-crossers mid-tick; applyCrossRegionTransfers moves the SAME *Entity A->B at the quiescent barrier (ai/nav/scratch travel, no double-tick/drop)"
  - "the async rejoin routes to the OWNING region: pathReady/spawnCandidatesReady.applyTo re-resolve owningRegion(id) and drop if gone; the spawn cap is cross-region (countByCategoryAcrossRegions)"
  - "region-aware handles (approach a): the entity/world/nav handles carry the OWNING region; starlarkGoal.handles binds it so a goal callback for a mob in R re-resolves R's store"
  - "the cross-region tracker: tracker.near spans regions (entitiesNearAcrossRegions) at the barrier so a player near the seam sees across the boundary"
  - "THE REGION-01 GATE: 2 parallel regions + transfer + cross-region tracker + region-aware Emit all Docker -race clean; CGO=0 default build preserved; the existing suite green"
affects: [phase-28-perf-scaling, folia regionization]

# Tech tracking
tech-stack:
  added: []   # no new dependency — reuses xsync/v4 (already in go.mod from Phase 8) for the currentRegion registry
  patterns:
    - "Per-goroutine current-region (currentRegion xsync.Map keyed by goroutine id): region.tick registers itself so the deep t.only() call sites resolve to the OWNING region across the fan-out without rewriting 200 sites (the lowest-churn region scoping)"
    - "Cross-region transfer at the barrier (27-RESEARCH Pattern 4): queue mid-tick (detectTransfers), drain at the quiescent barrier (applyCrossRegionTransfers) — remove-then-add the SAME *Entity, never a copy"
    - "World-global vs per-region phase split: the world (shared ChunkManager) is MUTATED only on the coordinator (tickWorld/tickChunks/chunkReady/dispatch edits); the fan-out only READS it — concurrent reads are race-clean by construction"
    - "Region-bound handles (approach a) over the goroutine-local fallback: a goal callback's handles explicitly carry the owning region so the resolution is correct even independent of the resolving goroutine"

key-files:
  created:
    - server/region_transfer.go
    - server/goroutine_id.go
    - server/region_transfer_test.go
    - server/region_plugin_test.go
    - server/region_race_test.go
  modified:
    - server/tick.go
    - server/region.go
    - server/region_coordinator.go
    - server/async.go
    - server/spawner.go
    - server/plugin_entity.go
    - server/plugin_mob_ai.go
    - server/tracker.go
    - server/item_entity.go
    - server/player_visibility.go
    - server/physics_test.go
    - server/async_stress_test.go
    - server/region_test.go
    - server/player_visibility_test.go

key-decisions:
  - "regionOf = (col.X ^ col.Z) & 1 (a checkerboard): a PURE function of the column only (no persisted region id), restart-stable because chunks are position-keyed; the XOR-parity guarantees every chunk has neighbours in the OTHER region so the seam is exercised everywhere a mob/player moves a column"
  - "Per-goroutine currentRegion registry (xsync.Map[goroutineID]*region) instead of threading *region into ~200 t.only() call sites: region.tick registers itself, only() consults it, a non-fan-out goroutine (coordinator / direct test calls) falls back to globalRegion — keeps the existing N=1-style tests + dispatch handlers working unchanged"
  - "World-global phases (tickWorld/tickChunks/tickEntities) + the async rejoin run on the COORDINATOR, only tickAI+tickPhysics fan out: the shared ChunkManager is not goroutine-safe, so confining its MUTATIONS to the coordinator (the fan-out only READS) keeps the parallel phase race-clean while preserving the exact observable phase order (TestTickPhaseOrder)"
  - "trace() emits only from the globalRegion goroutine so the phase trace stays one coherent sequence under the parallel fan-out (TestTickPhaseOrder byte-identical)"
  - "Handles carry the OWNING region (approach a) AND the goroutine-local only() fallback both exist: the goal-callback path is region-bound explicitly; direct/test handles fall back to only()"

patterns-established:
  - "The N=2 cross-region seam: detectTransfers/applyCrossRegionTransfers (entities), syncPlayerEntities (players transfer the same way), owningRegion(id) re-resolve (async rejoin + leave), entitiesNearAcrossRegions (tracker/pickup broad-phase at the barrier), countByCategoryAcrossRegions/mobNearAcrossRegions (cross-region spawn cap)"

requirements-completed: [REGION-01]

# Metrics
duration: ~35min
completed: 2026-06-28
---

# Phase 27 Plan 03: The N=2 flip + the REGION-01 gate Summary

**The world is statically split into N=2 regions that tick CONCURRENTLY: a pure restart-stable chunk->region hash, a per-goroutine current-region registry that routes the ~200 existing per-region call sites to the OWNING region across the conc fan-out with zero churn, cross-region entity TRANSFER at the barrier, the async rejoin re-routed to the owning region, region-bound plugin handles, and the cross-region tracker — all Docker -race clean, the CGO=0 default build preserved, and the existing suite green. THE REGION-01 GATE: a declared-mob goal callback for an entity in region R runs on R's goroutine and re-resolves R's store.**

## Performance
- **Duration:** ~35 min (active), plus the Docker -race verification (~50s server, ~34s targeted ×3)
- **Completed:** 2026-06-28T22:04:16Z
- **Tasks:** 4
- **Files modified:** 14 modified + 5 created

## Accomplishments
- `server/region_transfer.go`: `regionOf` (pure (X^Z)&1 restart-stable checkerboard, `regionCount=2`), `regionForColumn`/`regionForEntity`/`owningRegion`, `detectTransfers` + `applyCrossRegionTransfers` (the barrier hand-off), `withRegion`, `entitiesNearAcrossRegions`, `region.emitEntityEvent` (the region-scoped dispatch over the shared frozen registry).
- `server/goroutine_id.go`: `curGoroutineID` — the goroutine-local key for the `currentRegion` registry.
- `NewTickLoop` builds `regionCount` regions; the per-goroutine `currentRegion` (`xsync.Map`) lets `only()` resolve to the OWNING region across the fan-out (zero churn at the 200 `t.only()` sites); `SetWorld` wires the shared world into every region.
- `region.tick` registers its goroutine, fans out `tickAI`+`tickPhysics` over its store, runs `detectTransfers`; `tickOnce` runs the world-global phases (tickWorld/tickChunks/tickEntities) + the async rejoin on the coordinator, fans out the entity phases, then `applyCrossRegionTransfers` -> `applyAsyncResults` -> the cross-region post-phase.
- The async rejoin (`pathReady`/`spawnCandidatesReady.applyTo`) re-resolves the OWNING region (`owningRegion`, drop if gone); the spawn cap is cross-region (`countByCategoryAcrossRegions`/`mobNearAcrossRegions`), the spawn lands in the candidate's region.
- The entity/world/nav handles carry the OWNING region (approach a); `starlarkGoal.handles` binds it; the tracker's `near()` spans regions (`entitiesNearAcrossRegions`) at the barrier.
- **THE REGION-01 GATE proven:** `TestPluginHookRunsOnOwningRegion` (the goal callback runs on R's goroutine + resolves R's store), `TestTwoRegionsTickInParallel` (real simultaneity), `TestCrossRegionTransfer` (no double-tick/drop, ai/nav/scratch travel), `TestTrackerSeesAcrossRegions`, `TestRegionizedTickRace` (the combined gate), all **Docker -race clean** over `./server/ ./plugin/...`; **CGO=0** `go build ./...` + `./cmd/sulfur` stay pure-Go static; the FULL existing server + plugin + world suite passes UNCHANGED.

## Task Commits
1. **Task 1: N=2 hash + cross-region transfer + async-rejoin routing (TDD)** - `79abee79` (feat)
2. **Task 2: region-aware handles + region-aware Emit + the cross-region tracker (TDD)** - `89443381` (feat)
3. **Task 3: global-broadcast safety audit + the combined regionized scenario gate (TDD)** - `e9d9eef3` (test)
4. **Task 4: THE PHASE GATE — Docker -race over the fully regionized tick** - no code change required (the Docker -race gate over `./server/ ./plugin/...` + the targeted parallel/transfer/hook/race tests ×3 passed CLEAN; CGO=0 build clean — nothing to fix, no regression test needed).

_Note: Tasks 1-3 are TDD; the RED tests (region_transfer_test.go, region_plugin_test.go, region_race_test.go) were written first, then made GREEN by the implementation. Task 4's deliverable is the green -race gate itself._

## Files Created/Modified
- `server/region_transfer.go` (created) — the hash + transfer + cross-region helpers + region-scoped Emit.
- `server/goroutine_id.go` (created) — `curGoroutineID` for the per-goroutine current-region registry.
- `server/region_transfer_test.go` / `server/region_plugin_test.go` / `server/region_race_test.go` (created) — the parallelism/transfer/async-rejoin gates, THE region-01 hook gate + the cross-region tracker, the combined race + the broadcast-safety check.
- `server/tick.go` — N=2 region construction, the `currentRegion` registry, `only()` region-resolution, `SetWorld` wires all regions, the player entity owned by its region, `removePlayer` cross-region, the `onGoalCall` test seam, `trace()` global-region-only.
- `server/region.go` — `pendingTransfers` on the region.
- `server/region_coordinator.go` — `region.tick` fans out the entity phases + registers/detects transfers; `tickOnce` runs world-global phases on the coordinator + the cross-region post-phase; the global-broadcast safety audit.
- `server/async.go` / `server/spawner.go` — the async rejoin routes to the owning region; the cross-region spawn cap + the region-targeted spawn.
- `server/plugin_entity.go` / `server/plugin_mob_ai.go` — region-bound handles + `starlarkGoal.handles` binds the owning region.
- `server/tracker.go` — `near()` spans regions at the barrier.
- `server/item_entity.go` / `server/player_visibility.go` — region-aware item lifecycle + player-entity sync/transfer.
- `server/physics_test.go` / `server/async_stress_test.go` / `server/region_test.go` / `server/player_visibility_test.go` — test helpers wire the shared world into every region; N=2 store-sharding assertions.

## Decisions Made
- **regionOf = (X^Z)&1.** A pure, restart-stable checkerboard (chunks are position-keyed; no region id persisted). The XOR-parity puts every chunk's neighbours in the other region, exercising the seam everywhere.
- **Per-goroutine currentRegion registry, not a 200-site thread-through.** region.tick registers itself; only() consults it; the coordinator / direct test calls fall back to globalRegion — the existing N=1-style tests + dispatch handlers keep working unchanged.
- **World mutations confined to the coordinator; only tickAI+tickPhysics fan out.** The shared ChunkManager is not goroutine-safe, so the parallel phase only READS it — race-clean by construction — and the observable phase order is byte-identical (TestTickPhaseOrder).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test helpers wired the shared world into only globalRegion, leaving region 1's world nil under the N=2 fan-out**
- **Found during:** Task 1/2 (TestAllAsyncSubsystemsRaceClean transient panic; the gate test nil-deref)
- **Issue:** `newPhysicsLoop` / `newStressLoop` / the plugin-loop helper did `loop.only().world = mgr` — on the test goroutine `only()` resolves to globalRegion, so a mob that transferred to / spawned in region 1 fanned out on region 1's goroutine and read a NIL world (physics/AI block checks broke; a `Worker.Results()` nil deref surfaced when SetWorld was mis-called with a nil worker).
- **Fix:** wired the shared world into EVERY region in the test helpers (matching the production `SetWorld`, which now wires all regions); the plugin-loop helper wires the world directly into all regions (no nil-worker SetWorld).
- **Files modified:** server/physics_test.go, server/async_stress_test.go, server/region_plugin_test.go (created)
- **Verification:** TestAllAsyncSubsystemsRaceClean green ×10; the gate test green.
- **Committed in:** `79abee79` / `89443381`

**2. [Rule 1 - Bug] Two existing tests asserted the N=1 single-store internal that the N=2 sharding changes**
- **Found during:** Task 1 (TestRegionExtractionBehaviorNeutral asserted len(regions)==1; TestPlayerVisibility read loop.only().entities after moving a player across the seam → nil deref)
- **Issue:** these tests reached into `only()` assuming one store; under N=2 a player/entity lives in its OWNING region, so the reads must resolve cross-region (owningRegion). The OBSERVABLE gameplay is unchanged — only the internal store the tests poked changed.
- **Fix:** updated both tests to assert the N=2 invariant (regionCount regions; owningRegion(id) cross-region resolution); the behavior-neutral physics-landing assertion is unchanged.
- **Files modified:** server/region_test.go, server/player_visibility_test.go
- **Committed in:** `79abee79`

---

**Total deviations:** 2 auto-fixed (2 bugs). Both were test-harness/internal-assertion updates the N=2 store sharding necessitated — no production-behavior change; the observable gameplay stayed vanilla-identical (the full suite passes UNCHANGED otherwise).

## Issues Encountered
- **A transient TestAllAsyncSubsystemsRaceClean panic on the first full-suite run** traced to the nil-world-in-region-1 test-helper bug above (a transferred mob hitting region 1's nil world). Fixed by wiring the shared world into every region in the helpers; 10/10 + the Docker -race gate confirm it is resolved.

## Threat Surface
No new external attack surface — this is an internal concurrency change (no new network input, auth, or untrusted parsing). The STRIDE register's threats are all MITIGATED + proven:
- T-27-01 (a cross-region race): the fan-out touches only its own store + READS the shared world; cross-region reads/writes (transfer, tracker, cap, async rejoin) run at the quiescent barrier — Docker -race clean (Task 4) + TestRegionizedTickRace.
- T-27-03 (drop/double-tick on transfer): remove-then-add the SAME *Entity at the barrier; TestCrossRegionTransfer asserts present-in-B / absent-from-A / ticked-once.
- T-27-03-RJ (async rejoin to the wrong region): pathReady/spawnCandidatesReady re-resolve owningRegion(id), drop if gone — TestAsyncRejoinRoutesToOwningRegion.
- T-27-03-GT (gametime drift): unchanged from Plan 02 — the coordinator advances the ONE shared anchor exactly once.
- T-27-03-HR (handle resolves the wrong region): region-bound handles — TestPluginHookRunsOnOwningRegion.
- T-27-03-BCAST (broadcast races a registration): t.players drained pre/post fan-out, read-only during region ticks — TestGlobalBroadcastSafeFromRegion + the audit comment.

## User Setup Required
None — no external service configuration required.

## Next Phase Readiness
- The fully regionized tick (N=2, parallel, race-clean) closes Phase 27 / REGION-01. The real-client + perf-scaling gate is Phase 28 (explicitly separate).
- The N=2 split is STATIC (a locked deferral): dynamic region merge/split, per-region-sharded chunks (the world is currently one shared ChunkManager mutated on the coordinator), and the per-region scheduled-block/fluid ticks fanning out (they run on the coordinator today) are the natural Phase-28+ extensions.
- A plugin `entities_near` from a goal callback scopes to the CALLING region (race-safe in the fan-out, matching the Folia single-owner discipline); a cross-region query is a barrier-only operation. Documented as an N=2 boundary, revisit if a use case needs cross-region entity queries from a region tick.

---
*Phase: 27-folia-regionization*
*Completed: 2026-06-28*

## Self-Check: PASSED

- server/region_transfer.go — FOUND
- server/goroutine_id.go — FOUND
- server/region_transfer_test.go — FOUND
- server/region_plugin_test.go — FOUND
- server/region_race_test.go — FOUND
- .planning/phases/27-folia-regionization/27-03-SUMMARY.md — FOUND
- Commit 79abee79 (Task 1) — FOUND
- Commit 89443381 (Task 2) — FOUND
- Commit e9d9eef3 (Task 3) — FOUND
