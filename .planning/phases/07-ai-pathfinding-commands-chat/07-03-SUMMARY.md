---
phase: 07-ai-pathfinding-commands-chat
plan: 03
subsystem: ai
tags: [natural-spawner, mob-category, spawn-placements, tickAI, navigation, pig, tick-loop, port-from-jar]

# Dependency graph
requires:
  - phase: 07-01
    provides: "mobAI + serverAiStep driver + newPigAI() (the GoalSelector goals)"
  - phase: 07-02
    provides: "groundNavigation (A* requestPath/tick) wired into serverAiStep"
  - phase: 06-03
    provides: "tickPhysics snapshot-then-range pattern + moveEntity (per-axis swept collision + re-bucket)"
  - phase: 06-01
    provides: "entityStore (add/near/get/byID) + EntityIDAllocator + NewEntity"
  - phase: 06-04
    provides: "world.ChunkManager.GetBlock / Get + blockSolidAt (the ON_GROUND world read)"
provides:
  - "server/mob_category.go — ported MobCategory enum + per-category maxInstancesPerChunk caps (CREATURE=10) + Pig->CREATURE mapping"
  - "server/spawner.go — faithful-but-minimal NaturalSpawner: countByCategory (cap accounting) + naturalSpawn (cap gate + ON_GROUND placement + Pig add with real AI)"
  - "filled tickAI() — drives serverAiStep for every AI mob + the throttled naturalSpawn, in the fixed pipeline slot"
  - "retired sinusoidal debug pig — the debug pig now wanders/navigates via the real ported AI (07-01/07-02), not sinApprox/cosApprox"
affects: [07-06, phase-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Port-from-jar cap accounting: per-category live count recomputed from the tick-owned store each cycle (Pitfall 3 anti-flood)"
    - "ON_GROUND placement collapsed to blockSolidAt(below) && !feet && !head, reusing the physics world read"
    - "tickAI snapshot-then-range (mirrors tickPhysics) + throttled spawn (gametime % spawnInterval) on the single-owner tick"

key-files:
  created:
    - server/mob_category.go
    - server/spawner.go
    - server/spawner_test.go
  modified:
    - server/tick_phases.go
    - server/debug.go

key-decisions:
  - "CREATURE cap = 10 read directly from MobCategory static-init bytecode (bipush 10); the global cap scales as maxInstancesPerChunk * spawnableChunkCount per getFilteredSpawningCategories"
  - "ON_GROUND placement ported from SpawnPlacementTypes$1.isSpawnPositionOk (solid spawnable block below + clear feet + clear head) via the existing blockSolidAt; light-level rule relaxed for v1 (no light engine)"
  - "Spawner throttled every spawnInterval=20 ticks with ONE bounded placement attempt; vanilla's per-tick density/probability model deferred"
  - "Debug pig motion delegated entirely to the real AI (newPigAI + serverAiStep); sinApprox/cosApprox/pigPhase removed"

patterns-established:
  - "Cap accounting: count live mobs by MobCategory from the authoritative tick-owned store before each spawn — never a side counter"
  - "Spawner candidate scan kept pure over the tick-owned world read so Phase 8 / OPT-03 can move it off-tick (the async-ready seam)"

requirements-completed: [AI-03]

# Metrics
duration: 38 min
completed: 2026-06-24
---

# Phase 7 Plan 03: Natural Mob Spawner + tickAI Fill + Retired Debug Pig Summary

**Faithful-but-minimal NaturalSpawner (CREATURE cap=10, ON_GROUND placement, both ported from the 26.2 jar) wired into a filled tickAI() that drives the real ported AI for every mob, with the throwaway sinusoidal debug pig replaced by genuine wander/navigation.**

## Performance

- **Duration:** ~38 min
- **Tasks:** 2 (both TDD)
- **Files modified:** 5 (3 created, 2 modified)

## Accomplishments
- Ported `net.minecraft.world.entity.MobCategory` (the full enum + per-category caps read from the static-init bytecode) and the faithful-minimal `net.minecraft.world.level.NaturalSpawner` (per-category cap accounting from the tick-owned store + the `SpawnPlacementTypes` ON_GROUND placement check).
- Filled the empty `tickAI()` stub in its fixed pipeline slot: it snapshots the store's AI mobs and runs `serverAiStep` (07-01 goals + 07-02 A* navigation) for each, then runs the throttled natural spawner — no pipeline reorder (`TestTickPhaseOrder` preserved).
- Retired the throwaway sinusoidal debug pig: the debug pig now gets a real `newPigAI()` and wanders/navigates via `serverAiStep`; `sinApprox`/`cosApprox` and the `pigPhase` field were removed.
- This wave wires AI-01 + AI-02 + AI-03 into the live tick — the world now populates with capped CREATURE mobs that walk and pathfind. Docker `-race` over `./server/... ./world/...` is clean.

## javap-Confirmed Java Values Ported

**`net.minecraft.world.entity.MobCategory` (static-init bytecode — the `max` constructor arg = `getMaxInstancesPerChunk()`):**
```
CREATURE:  35: bipush 10    -> max = 10   (the load-bearing v1 cap)
MONSTER:   11: bipush 70    -> max = 70
AMBIENT:   59: bipush 15    -> max = 15
AXOLOTLS / UNDERGROUND_WATER_CREATURE / WATER_CREATURE: iconst_5 -> 5
WATER_AMBIENT: bipush 20    -> max = 20
MISC:      iconst_m1        -> max = -1   (uncapped; filtered out of SPAWNING_CATEGORIES)
```
Constructor signature confirmed: `MobCategory(String name, String serialized, String abbrev, int max, boolean isFriendly, boolean isPersistent, int despawnDistance)`.

**`net.minecraft.world.level.NaturalSpawner` distance constants (`javap -constants`):**
```
private static final int MIN_SPAWN_DISTANCE = 24;
public  static final int SPAWN_DISTANCE_CHUNK = 8;     // <- ported as spawnDistanceChunk
public  static final int SPAWN_DISTANCE_BLOCK = 128;   // = 8 * 16
```

**`SpawnPlacementTypes$1` (ON_GROUND) `.isSpawnPositionOk(level, pos, type)` bytecode translation:**
```
above = pos.above(); below = pos.below()
belowState = level.getBlockState(below)
if !belowState.isValidSpawn(level, below, type): return false   // SOLID spawnable surface below
return isValidEmptySpawnBlock(pos, type) && isValidEmptySpawnBlock(above, type)  // feet + head clear air
```
`NaturalSpawner.isValidEmptySpawnBlock` rejects a full-collision cube / signal source / fluid / PREVENT_MOB_SPAWNING_INSIDE / block-dangerous. v1 collapses both to: solid at `(x,y-1,z)` AND air at `(x,y,z)` and `(x,y+1,z)` via the existing `blockSolidAt`.

## v1 Coverage vs Deferrals

**v1 covers:** passive CREATURE spawn attempts in loaded columns near players (SPAWN_DISTANCE_CHUNK=8), the per-category cap gate (`cap = maxInstancesPerChunk * spawnableChunkCount`) recomputed from the tick-owned store each cycle (Pitfall 3 anti-flood), and the ON_GROUND placement check. The spawned Pig carries a real `newPigAI()` so it immediately wanders/navigates.

**Deferred (documented in `spawner.go`):** the full multi-category density model + `PotentialCalculator`, the `LocalMobCapCalculator` per-player distance weighting, the per-position `MIN_SPAWN_DISTANCE=24` check, biome spawn lists (`MobSpawnSettings`/`WeightedList`) + the creature-probability roll, structure spawns (`isInNetherFortressBounds`), and light-level rules (no light engine yet). Spawned-mob persistence is also out of v1 scope (reuses the ENT-06 path if added later).

## Task Commits

1. **Task 1: Port MobCategory + faithful-minimal NaturalSpawner** - `1184a87b` (feat)
2. **Task 2: Fill tickAI() + retire the sinusoidal debug pig** - `29a059e8` (feat)

_Both tasks are `tdd="true"` and were committed as a single faithful-port commit each (the test file + the ported implementation together), since the implementation is a bytecode translation rather than discovered behavior._

## Files Created/Modified
- `server/mob_category.go` (created) - ported MobCategory enum + per-category caps + categoryOf(Pig->CREATURE)
- `server/spawner.go` (created) - countByCategory + spawnableColumns + findStandableY (ON_GROUND) + naturalSpawn (cap gate + Pig add with newPigAI)
- `server/spawner_test.go` (created) - the 8 AI-03 tests (cap, placement, store add, tickAI drives mobs, tickAI spawns, phase order unchanged, debug pig uses real AI)
- `server/tick_phases.go` (modified) - filled tickAI(): snapshot-then-range serverAiStep + throttled naturalSpawn, in the fixed slot
- `server/debug.go` (modified) - attached newPigAI() to the debug pig; removed sinApprox/cosApprox + pigPhase (sinusoidal mover retired)

## Decisions Made
- Counted the per-category live count from the authoritative `entityStore.byID` each cycle rather than a side counter, so a stale/uncounted mob cannot flood or starve (the load-bearing Pitfall-3 discipline, asserted by `TestSpawnCapAccounting`).
- Reused the existing `blockSolidAt` (the same world read physics uses) for the ON_GROUND check, so placement and collision can never disagree on where a block lives.
- Made the spawner candidate scan deterministic (sorted spawnable columns, column-center candidate) so the v1 attempt is test-reproducible while keeping the structure async-ready for Phase 8 / OPT-03.
- Throttled the spawner to once per `spawnInterval=20` ticks with one bounded placement attempt — cheap per tick and faithful to "vanilla attempts often but mostly no-ops under cap".

## Deviations from Plan

None - plan executed exactly as written.

The only adjustments were in the new test (`TestDebugPigUsesRealAI`): the test player is given `debugGaveItems: true` and the debug `damageEvery` is set to 0 so the focused spawn/motion assertion does not crash on the give-stone / damage-bite paths that require a real `Client.Send` (those are exercised by the interactive 07-06 gate, not this unit test). This is test scaffolding, not a behavior change.

## Issues Encountered
- `TestDebugPigUsesRealAI` initially panicked on a nil `Client.Send` because `tickDebug`'s give-stone and damage-bite triggers dereference the player's client. Resolved by arming the test player with `debugGaveItems: true` and disabling the damage interval in the test — isolating the spawn + AI-motion assertion.

## Known Stubs
None. The naturally-spawned and debug pigs are fully wired (real AI attached, driven by tickAI, tracked by the unchanged entityTracker). The documented deferrals (density model, light, biome lists, structure spawns) are faithful-scope omissions, not data-less stubs.

## Next Phase Readiness
- AI-03 complete: the world populates with capped, ON_GROUND-placed CREATURE mobs that wander and pathfind via the real ported AI, all on the single-owner tick (TICK-05), `-race` clean.
- Ready for the 07-06 interactive human-verify gate (observe vanilla wander + obstacle navigation in a real 26.2 client). The remaining plan in phase 7 is 07-06 (the interactive check); plans 04 and 05 are already complete.
- Phase 8 / OPT-03 can move the spawner's candidate scan off-tick using the async-ready seam established here.

## Self-Check: PASSED
- `server/mob_category.go`, `server/spawner.go`, `server/spawner_test.go` exist on disk.
- Commits `1184a87b` and `29a059e8` present in git log.
- All 8 plan tests pass; full `./server/... ./world/...` suite green; `go vet ./...` and `go build ./...` clean; Docker `-race` over `./server/... ./world/...` clean; zero new dependencies (go.mod/go.sum unchanged).

---
*Phase: 07-ai-pathfinding-commands-chat*
*Completed: 2026-06-24*
