---
phase: 30-jumpcontrol-fluid-s1
plan: 01
subsystem: physics
tags: [fluid, lava, water, mob-ai, entity, floatgoal, 1to1-jar-port]

# Dependency graph
requires:
  - phase: 17-gameplay-reconnect
    provides: "fluidState/decodeFluid water-only decode, playerInWater AABB scan, fluidSurfaceHeight (FlowingFluid.getHeight)"
  - phase: 29-damage-keystone-s2
    provides: "*Entity width/height AABB footprint (data/entity table copy)"
provides:
  - "fluidState.isLava flag (a cell is water XOR lava XOR neither) + lavaLevelOf + a decodeFluid lava branch"
  - "lavaStateID + a defensive encodeFluid lava branch for the deferred lava flow sim"
  - "mobInWater / mobInLava on *Entity (EntityFluidInteraction.isInFluid AABB scan over fluidAt)"
  - "mobFluidHeight(e, kind) (Entity.getFluidHeight(TagKey) — MAX over the AABB) + the fluidKind WATER/LAVA tag enum"
  - "fluidSurfaceHeightOf (kind-parameterized FlowingFluid.getHeight so lava-above-lava matches)"
  - "getFluidJumpThreshold (Entity.getFluidJumpThreshold = getEyeHeight()<0.4 ? 0.0 : 0.4) + mobEyeHeight default"
affects: [30-02-jumpcontrol-aistep, 30-03-floatgoal, panicgoal, mob-fluid-nav]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "fluidKind tag enum mirrors FluidTags.WATER/LAVA TagKey; fluidState.matchesKind collapses FluidState.is(tag)"
    - "Eye-height-dependent constants computed from the EntityDimensions default factor (height*0.85) until a per-type eye-height read exists — cited, never baked"

key-files:
  created:
    - "server/fluid_mob_test.go"
  modified:
    - "server/fluid.go"
    - "server/fluid_physics.go"

key-decisions:
  - "getFluidJumpThreshold ports the REAL jar formula getEyeHeight()<0.4 ? 0.0 : 0.4 — the pig (eye 0.765) reads 0.4, NOT the plan/CONTEXT's assumed 0.0 (a 1:1 correctness fix, jar-verified)"
  - "Lava is decode-only this phase (full read, never const-false); the lava FLOW sim (FlowingFluid.tick, getDropOff=2) is DEFERRED — FloatGoal only READS lava"
  - "encodeFluid got a defensive lava branch even though the water sim never feeds it, so the deferred lava flow sim routes through one primitive and lava can never silently collapse to air"

patterns-established:
  - "fluidKind WATER/LAVA tag enum + matchesKind for tag-filtered fluid reads"
  - "kind-parameterized fluidSurfaceHeightOf preserves breath.go's water-only helper (no .isWater consumer perturbed)"

requirements-completed: [MOB-SUB-05]

# Metrics
duration: 6min
completed: 2026-06-29
---

# Phase 30 Plan 01: JumpControl + Fluid (S1) — Mob Fluid Predicates + Lava Decode Summary

**Extended the water-only fluid decode to recognize lava (isLava + lavaLevelOf + a decodeFluid branch, every .isWater consumer audited unperturbed) and ported the four mob fluid predicates — mobInWater / mobInLava / mobFluidHeight(WATER|LAVA) / getFluidJumpThreshold — 1:1 from the jar, closing MOB-SUB-05.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-06-29T22:12:06Z
- **Completed:** 2026-06-29T22:18:31Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 3 (2 modified, 1 created)

## Accomplishments

- `fluidState` carries `isLava` (water XOR lava XOR neither); `lavaLevelOf` mirrors `waterLevelOf` against the generated `block.Lava{Level}`; `decodeFluid` branches water-first (unchanged) then lava, reusing the SHARED `FlowingFluid.getLegacyLevel` level→amount inversion.
- Audited every `.isWater` consumer (fluid flow sim, breath, playerInWater, encodeFluid): lava decodes `isWater:false` exactly as the old zero `fluidState{}` did, so water behavior is byte-identical (all water flow goldens green) and the water scheduler never flows lava.
- `mobInWater`/`mobInLava` port `EntityFluidInteraction.isInFluid`, mirroring `playerInWater`'s AABB scan on `*Entity` (entity Width/Height); shared `mobInFluid` + the `fluidKind` tag.
- `mobFluidHeight(e, kind)` ports `Entity.getFluidHeight(TagKey)` = MAX over the AABB cells of `cellY + getHeight - e.y` for tag-matching cells, via a kind-parameterized `fluidSurfaceHeightOf` (lava-above-lava matches; breath.go's water-only helper untouched).
- `getFluidJumpThreshold` ports the real jar formula `getEyeHeight() < 0.4 ? 0.0 : 0.4`; eye height defaults to `height*0.85` (EntityDimensions ctor) until a per-type read lands.

## Task Commits

Each task committed atomically:

1. **Task 1: Extend fluidState + decodeFluid for lava (audit every .isWater consumer)** — `8e8bb130` (feat)
2. **Task 2: Port mobInWater / mobFluidHeight / mobInLava / getFluidJumpThreshold** — `ff9581a1` (feat)

_Note: both tasks were TDD (RED via the shared fluid_mob_test.go, then GREEN). The two GREEN implementations were committed separately (fluid.go for Task 1; fluid_physics.go + the test file for Task 2)._

## Files Created/Modified

- `server/fluid.go` — `isLava` on `fluidState`; `lavaLevelOf`; `decodeFluid` lava branch; `lavaStateID`; defensive `encodeFluid` lava branch.
- `server/fluid_physics.go` — `fluidKind` enum + `matchesKind`; `mobInWater`/`mobInLava`/`mobInFluid`; `mobFluidHeight` + `fluidSurfaceHeightOf`; `getFluidJumpThreshold` + `mobEyeHeight` + the `0.4`/`0.85` cited consts.
- `server/fluid_mob_test.go` (new) — headless gates: lava decode, water-decode-unchanged, mob water+lava reads, the WATER/LAVA tag filter, the 0.4/0.0 threshold branches.

## Decisions Made

- **getFluidJumpThreshold = the real eye-height formula, not 0.0.** See Deviations (Rule 1).
- **Lava decode-only; lava flow sim deferred.** Cited `FlowingFluid.tick`/`getDropOff=2` as the deferred follow-up. FloatGoal only reads lava, so a decode-only extension fully satisfies MOB-SUB-05.
- **Defensive `encodeFluid` lava branch.** The water sim never feeds lava there (every spread/tick path is `if fs.isWater`-guarded), but the branch is written 1:1 so the deferred lava flow sim reuses the encode and lava can never silently become air.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug/jar-correctness] getFluidJumpThreshold returns 0.4 for the pig, not the plan's assumed 0.0**
- **Found during:** Task 2 (porting getFluidJumpThreshold)
- **Issue:** The plan, CONTEXT, and must_haves cite `getFluidJumpThreshold()` returning `0.0` as "the pig default." The jar bytecode (`javap -c -p net.minecraft.world.entity.Entity.getFluidJumpThreshold`) is `getEyeHeight() < 0.4 ? 0.0 : 0.4`, and there is NO `Mob` override. The pig's default eye height is `height*0.85 = 0.9*0.85 = 0.765` (≥ 0.4), so the pig's threshold is **0.4**. Porting `0.0` would violate the 1:1 mandate and change observable gameplay: FloatGoal.canUse = `getFluidHeight(WATER) > getFluidJumpThreshold()`, so a 0.0 threshold would let the pig start floating on a thinner water film than vanilla.
- **Fix:** Ported the faithful `getEyeHeight() < 0.4 ? 0.0 : 0.4` formula with eye height computed from the EntityDimensions default factor `height*0.85` (javap-verified `ldc float 0.85f`), structured to become a per-type eye-height read later per CLAUDE.md. Both branches are tested (pig → 0.4; a short entity → 0.0).
- **Files modified:** server/fluid_physics.go, server/fluid_mob_test.go
- **Verification:** `TestFluidJumpThreshold` asserts pig==0.4 and short-entity==0.0; jar bytecode cited inline.
- **Committed in:** `ff9581a1` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 jar-correctness bug)
**Impact on plan:** The deviation is mandated by the 1:1 jar rule (CLAUDE.md takes precedence over the plan's assumed constant). It strengthens correctness for Plan 03 (FloatGoal.canUse compares against this threshold). No scope creep — still a single cited constant/formula, structured to become a per-type read later. The `must_haves` truth "getFluidJumpThreshold() returns the cited 0.0 default for the pig" is intentionally NOT met because it contradicts the jar; the correct jar value (0.4) is delivered instead.

## Issues Encountered

- `-race` requires CGO and no native gcc is on PATH; ran the race gate in a `golang:1.26` Docker container instead (`MSYS_NO_PATHCONV=1` to stop Git Bash mangling the bind-mount path). Clean.
- The plan's two TDD tasks share one test file; the package therefore could not compile (and Task 1's GREEN could not run) until Task 2's predicates existed. Resolved by implementing both GREENs, running the full suite, then committing the two files separately (fluid.go for Task 1, fluid_physics.go + the test for Task 2) so each commit's content is task-scoped.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Plan 30-02 (jumpControl + the aiStep jump branch) and Plan 30-03 (FloatGoal@0 lockstep) can now READ `mobInWater`/`mobInLava`/`mobFluidHeight(kind)`/`getFluidJumpThreshold` directly — the predicates FloatGoal.canUse and the aiStep jump branch depend on.
- **Carryover for Plan 03:** FloatGoal.canUse compares `mobFluidHeight(WATER) > getFluidJumpThreshold()` — the threshold is **0.4** for the pig (not 0.0). The plugin pig's `float_can_use` and the Go-native oracle must use the same value in lockstep; the host handle (`entity.fluid_height` etc.) must surface the real predicate so both halves agree.
- **Deferred (cited):** the lava FLOW simulation (`FlowingFluid.tick` for lava, `getDropOff=2`); a per-type eye-height read (currently `height*0.85` default).

## Self-Check: PASSED

---
*Phase: 30-jumpcontrol-fluid-s1*
*Completed: 2026-06-29*
