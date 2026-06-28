---
phase: 17-gameplay-completion
plan: 02
subsystem: gameplay
tags: [fluid-sim, flowing-fluid, scheduled-tick-queue, water-physics, jar-port, wave-2]

# Dependency graph
requires:
  - phase: 17-gameplay-completion
    plan: 01
    provides: "TickLoop.fluidSchedule field, fluid.go/fluidScheduleQueue stub seam, tickWorld -> tickFluids() wiring"
  - phase: 06-entities
    provides: "world.ChunkManager GetBlock/SetBlock (tick-owned block IO), tickPlayer x/y/z, dimMinY, playerWidth/Height"
provides:
  - "GAMEPLAY-05: vanilla FlowingFluid water flow port (getNewLiquid/spread/spreadToSides/getSlopeDistance) on a net-new getTickDelay-spaced scheduled-block-tick queue with deterministic packed-pos drain"
  - "Player fluid physics (EntityFluidInteraction port): playerInWater AABB detection + applyFluidPhysics (0.8 slowdown + 0.014 buoyant push) over the accepted-movement-delta model"
affects: [17-03-damage, 17-07-visual-gate]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Scheduled-block-tick queue: per-gametime bucket map keyed by (gametime+getTickDelay), drained deterministically (sorted by packed pos) - the Go analogue of ServerLevel.scheduleTick"
    - "Schedule-on-change (not on-load): water is scheduled only when placed/written/disturbed; pre-existing worldgen oceans stay static until disturbed (vanilla parity, Open Question 3)"
    - "Air-or-same-fluid passability subset: canPassThroughWall VoxelShape occlusion simplified to a solid/non-solid test for the v1 water gate"

key-files:
  created:
    - server/fluid_schedule.go
    - server/fluid_physics.go
    - server/fluid_test.go
    - server/fluid_schedule_test.go
  modified:
    - server/fluid.go

key-decisions:
  - "fluidSchedule lazily inits inside tickFluids (nil-check) and scheduleFluidTick - never touches SetWorld/tick.go, keeping 17-02 to its own files (parallel-wave conflict elimination)"
  - "canBeReplacedWith guard in spreadTo: never overwrite a source, never downgrade a flow - this is what terminates the spread deterministically (Pitfall 2)"
  - "isHole requires the cell itself to be passable (a solid floor cell is NOT a hole) - so flow over a flat floor spreads sideways instead of falsely treating the floor as a drop-off"
  - "applyFluidPhysics operates on the accepted MOVEMENT DELTA (Sulfur has no server-side velocity integrator yet) - the call-site wiring in subtick.go is deferred (subtick.go is 17-03-owned this wave)"

patterns-established:
  - "Pattern: fluid level encoded via the jar-exact getLegacyLevel (source->0, flowing->8-amount, falling->+8) and decoded back to (amount, falling, source) for the spread arithmetic"
  - "Pattern: walled flat-basin test fixture for the pure level-decrement gate (no drop-off slope bias), centered inside the loaded chunk so the chunk boundary never acts as a phantom drop-off"

requirements-completed: [GAMEPLAY-05]

# Metrics
duration: 35min
completed: 2026-06-25
---

# Phase 17 Plan 02: Fluid Simulation (GAMEPLAY-05) Summary

**Ported the vanilla FlowingFluid water simulation from the unobfuscated 26.2 jar — the level encoding (getLegacyLevel), the getNewLiquid/spread/spreadToSides/getSlopeDistance flow algorithm with jar-verified constants (dropOff=1, tickDelay=5, slopeFindDistance=4), and a net-new deterministic scheduled-block-tick queue it propagates on — plus the 26.2-renamed EntityFluidInteraction player physics (0.8 horizontal slowdown + 0.014 buoyant push); water now flows with vanilla-faithful ring decrement, terminates deterministically, and a player in water is slowed and buoyant.**

## Performance

- **Duration:** ~35 min
- **Completed:** 2026-06-25
- **Tasks:** 3 (all TDD: RED tests then GREEN port)
- **Files:** 4 created, 1 overwritten (the 17-01 fluid.go stub)

## Accomplishments

- **Net-new scheduled-block-tick queue** (`fluid_schedule.go`): the world's `ChunkManager` has no tick scheduler, so this is the Go analogue of vanilla `ServerLevel.scheduleTick(pos, fluid, delay)` — a per-gametime bucket map keyed by `gametime+getTickDelay(5)`, drained in **deterministic packed-pos order** (`drainDue` sorts by `packPos`) so the simulation is reproducible run-to-run (Pitfall 2).
- **FlowingFluid algorithm port** (`fluid.go`, each method citing `net.minecraft.world.level.material.FlowingFluid.<method>`):
  - `getLegacyLevel` — jar-exact `source?0 : (8-min(amount,8)) + (falling?8:0)` (verified bytecode).
  - `getNewLiquid` — max-reaching-neighbor minus dropOff(1), `>=2`-source-neighbor conversion (solid/source below), fluid-above falling rule, empty when `<=0`.
  - `spread` — down-first (falling column) + `>=3`-source-neighbor side spread + the water-hole rule.
  - `spreadToSides`/`getSpread`/`getSlopeDistance` — the 8-direction recursive slope-find within `getSlopeFindDistance(4)` biasing flow toward the nearest drop-off.
  - `spreadTo`+`canBeReplacedWith` — never overwrite a source / downgrade a flow, so the spread reaches a fixed point (**termination**, proven by `TestWaterSettles`).
- **Player fluid physics** (`fluid_physics.go`, citing the 26.2-renamed `Entity.updateFluidInteraction` / `EntityFluidInteraction` / `LivingEntity.getWaterSlowDown`): `playerInWater` AABB sampling + `applyFluidPhysics` (horizontal `0.8` slowdown + `0.014` buoyant push, both javap-verified).
- **Zero shared-file edits**: all three commits touch only `fluid*.go`; `tick.go`/`tick_phases.go`/`subtick.go` untouched (parallel-wave clean with 17-03/17-04).

## Task Commits

1. **Task 1: scheduled fluid-tick queue + jar-exact water level encoding** - `2502e13c` (feat)
2. **Task 2: FlowingFluid algorithm port (getNewLiquid/spread/slope-find)** - `9995aacf` (feat)
3. **Task 3: player fluid physics (in-water + 0.8 slowdown + buoyancy)** - `3929d5a9` (feat)

## Files Created/Modified

- `server/fluid_schedule.go` (created) — `fluidScheduleQueue` (per-gametime bucket map), `schedule`, deterministic `drainDue`, `empty`, `packPos`. The 17-01 `fluidScheduleQueue` stub type moves here (defined exactly once).
- `server/fluid.go` (overwrites 17-01 stub) — level encode/decode, water constants, `tickFluids` dispatcher (lazy queue init), `scheduleFluidTick`, and the full FlowingFluid algorithm (`fluidTick`, `getNewLiquid`, `spread`, `spreadToSides`, `getSpread`, `getSlopeDistance`, `isHole`, `sourceNeighborCount`, `spreadTo`, `canBeReplacedWith`).
- `server/fluid_physics.go` (created) — `playerInWater`, `applyFluidPhysics`, water physics constants.
- `server/fluid_schedule_test.go` (created) — `TestGetLegacyLevel`, `TestWaterLevelRoundTrip`, `TestScheduleDrainDeterministic`.
- `server/fluid_test.go` (created) — `TestGetNewLiquid`, `TestWaterSettles`, `TestFlowDownColumn`, `TestInWaterDetection`, `TestWaterSlowdown`, `TestBuoyancy`.

## Decisions / Open-Question resolutions (per the plan's output spec)

- **Scheduled-queue trigger policy (Open Question 3):** schedule-on-change — a fluid tick is enqueued only when a cell is written (`spreadTo`/`fluidTick`) or its neighbor drains (`scheduleNeighbors`). Pre-existing worldgen oceans are NOT scheduled on chunk-load, so they stay static until disturbed (vanilla parity; bounds the DoS surface T-17-05). A place/break adjacency hook (to start worldgen water flowing when disturbed) is the natural follow-up integration but is out of this plan's file scope (it lives in `block_interact.go`, 17-04-owned this wave).
- **Lazy `fluidSchedule` init:** constructed inside `tickFluids`/`scheduleFluidTick` via a nil-check, never at `SetWorld` — so this plan never edits the shared `tick.go` (17-01 handoff honored).
- **Waterlogged simplification (A3):** `playerInWater` treats a waterlogged block as a read-only source contributor for detection; the block is never overwritten. Full waterlog place/break interaction is a documented future item.
- **Lava (A4):** not implemented. The algorithm is fluid-agnostic; lava is a constant swap (dropOff/tickDelay/slopeFindDistance differ). The REQUIREMENTS gate is "water simulates", so lava is deferred — the `fluidState`/`getNewLiquid`/`spread` code is structured to accept a per-fluid constant set when lava is added.

## Player-physics integration point + feel limitation (for the GAMEPLAY-07 gate)

`applyFluidPhysics(p, dx, dy, dz)` is a **pure** function over the accepted movement delta: in water it scales the horizontal delta by `0.8` and adds `+0.014` to the vertical delta. It is fully unit-tested but **not yet wired** because the integration site — one line in `subtick.go`'s `collidePlayer`/accept path — is in `subtick.go`, which is **17-03's declared file in this wave**. Wiring it now would corrupt the parallel run. The deferred wiring is a single call: in `applyInput`'s `ServerboundMovePlayerPos`/`PosRot` cases, compute the claimed delta `(x-p.x, y-p.y, z-p.z)`, pass it through `applyFluidPhysics`, and accept the adjusted target. Because Sulfur is currently position-authoritative (no server-side velocity integrator), the in-water feel is an approximation of vanilla's per-tick velocity damping — the GAMEPLAY-07 visual gate confirms the feel and may motivate a true velocity integrator later.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] isHole falsely treated a flat solid floor as a drop-off**
- **Found during:** Task 2 (`TestWaterSettles` — flow over a flat floor never spread sideways).
- **Issue:** `isHole(pos)` only checked `canReplace(below(pos))`, so a solid floor cell (whose below is empty space under a 1-thick floor) was reported as a hole, suppressing side-spread.
- **Fix:** `isHole` now first requires `pos` itself to be passable (a solid cell is never a hole), matching `FlowingFluid.isWaterHole` semantics.
- **Files modified:** `server/fluid.go` (commit `9995aacf`).

**2. [Rule 1 - Bug] spreadTo clobbered the source / oscillated (non-termination)**
- **Found during:** Task 2 (`TestWaterSettles` — the source block was overwritten by a level-1 flow at ~pass 11).
- **Issue:** `spreadTo` only guarded on solidity, so a flowing neighbor could overwrite the level-0 source (water is not solid -> `canReplace` was true), and weaker flows could downgrade stronger ones forever.
- **Fix:** added `canBeReplacedWith(cur, incoming)` (PORT of `FluidState.canBeReplacedWith` semantics): air always replaceable; a source never replaced; a flow replaced only by a stronger (higher-amount, or falling) flow. This is also what makes the spread **terminate** deterministically.
- **Files modified:** `server/fluid.go` (commit `9995aacf`).

_(Both auto-fixes are within Task 2's own new code — not pre-existing.)_

## Known Stubs

- **`applyFluidPhysics` call-site (intentional, cross-plan):** the physics function is implemented + unit-tested but not yet called from the movement path. Resolution is a one-line wire in `subtick.go` (17-03-owned this wave) — see the integration section above. This does NOT block GAMEPLAY-05's core goal (water flows + the physics math is correct + tested); it defers only the live movement effect, which the GAMEPLAY-07 gate evaluates.

## Verification

- `CGO_ENABLED=0 go build ./...` exits 0 (no duplicate `fluidScheduleQueue` after the 17-01 stub overwrite).
- `go test ./server/...` passes (all 9 new fluid tests + `TestTickPhaseOrder`, no phase reorder).
- `grep "updateFluidHeightAndDoFluidPushing" server/` returns nothing (the pre-26.2 name is absent).
- `grep -c "getNewLiquid\|getLegacyLevel\|FlowingFluid" server/fluid.go` = 32 (jar citations present); `EntityFluidInteraction`/`updateFluidInteraction` cited in `fluid_physics.go`.
- Each of the three commits touches ONLY `fluid*.go` (no `tick.go`/`tick_phases.go`/`subtick.go`).
- Docker `-race` over `./server/...`: clean (`go test -race -timeout 1800s ./server/...` -> all packages ok).
- Determinism: `TestWaterSettles` runs the settle twice and asserts identical sampled levels; `TestScheduleDrainDeterministic` asserts reproducible packed-pos drain order.

## Self-Check: PASSED

- All 5 fluid files verified present on disk (fluid.go, fluid_schedule.go, fluid_physics.go, fluid_test.go, fluid_schedule_test.go).
- All 3 task commit hashes (2502e13c, 9995aacf, 3929d5a9) verified in git log.
