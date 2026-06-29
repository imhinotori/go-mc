---
phase: 30-jumpcontrol-fluid-s1
verified: 2026-06-29T00:00:00Z
status: human_needed
score: 4/4 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: none
human_verification:
  - test: "Drop a pig into a body of water on a live 26.2 client"
    expected: "The pig bobs / jumps to stay at the surface and does NOT sink + suffocate; it floats like vanilla"
    why_human: "In-game visual buoyancy/float behavior cannot be confirmed programmatically; the operator validates the FloatGoal feel live after each phase (per the standing visual-gate process)"
---

# Phase 30: JumpControl + Fluid (S1) Verification Report

**Phase Goal:** A mob can jump on command and detect fluid, so FloatGoal and any swimming/leaping mob work for `*Entity`, not just the player.
**Verified:** 2026-06-29
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A goal can claim a JUMP flag and the mob performs the real `jumpFromGround` impulse, consumed in `serverAiStep`'s JUMP slot in jar order (after `navigation.tick`) | ✓ VERIFIED | `server/ai_mob.go`: `navigation.tick(t,e)` (l.175) THEN `jumpControl.tick(e)` (l.185) THEN `entityJumpStep(e)` (l.186) — jar order. `noJumpDelay--` at the aiStep top (l.154). `server/jump.go entityJumpStep` ports the full LivingEntity.aiStep jump branch (water/lava `jumpInLiquid` +0.03999999910593033, land `jumpFromGround` 0.42 + `noJumpDelay=10`). Tests `TestServerAiStepJumpSlot`, `TestEntityJumpStep_{Water,Lava,Land,LandDelayGate,NotJumping}`, `TestJumpControl`, `TestNoJumpDelay` all PASS. |
| 2 | `mobIsInWater`/`mobFluidHeight`/`isInLava` predicates (on `fluidAt`) return jar-correct values for `*Entity`; a mob in water no longer sinks/suffocates | ✓ VERIFIED | `server/fluid.go`: `fluidState.isLava` + `lavaLevelOf` + `decodeFluid` lava branch (water `.isWater` consumers audited, water sim still `if fs.isWater`-guarded). `server/fluid_physics.go`: `mobInWater`/`mobInLava`/`mobFluidHeight(kind)`/`getFluidJumpThreshold` over the entity AABB via `fluidAt`. Tests `TestDecodeFluidLava`, `TestDecodeFluidWaterUnchanged`, `TestMobInWater`, `TestMobFluidHeight`, `TestFluidJumpThreshold` PASS. The wet test `TestFloatGoalKeepsPigAfloat` (differential vs an impulse-free control pig, 50 ticks) PASSES — the FloatGoal pig sinks less and stays in the water column. |
| 3 | FloatGoal@0 wired on the Go-native oracle AND the plugin pig IN LOCKSTEP (same plan), `nextFloat()<0.8` confined to the running-goal callback | ✓ VERIFIED | Go: `server/ai_mob.go` l.222 `m.goals.addGoal(0, newFloatGoal())`; `server/ai_goals_float.go` `floatGoal.canUse` (fluid predicates, NO RNG) + `tick` (`mobRandom(e).nextFloat() < floatJumpProbability(0.8)` → `jumpControl.doJump()`). Plugin: BOTH `plugins/vanilla_pig/main.star` and `server/assets/vanilla_pig/main.star` (byte-identical via diff) carry `goal(priority=0, flags=["JUMP"], can_use=float_can_use, tick=float_tick, requires_update_every_tick=True)`; the `rand_float() < FLOAT_JUMP_PROBABILITY` draw is inside `float_tick`, NOT `float_can_use`. `TestFloatGoalTickDraws` asserts EXACTLY one nextFloat per tick; `TestPluginPigEqualsGoNativePig` GREEN. |
| 4 | Standing: javap-verified, CGO=0, oracle GREEN, `-race` clean | ✓ VERIFIED | 13 `[VERIFIED javap …]` citations across jump.go/ai_goals_float.go/fluid_physics.go against the present `temp/cache/26.2-inner.jar`. `CGO_ENABLED=0 go build ./...` exit 0. `go list -deps` for gopy = empty (no cgo). `TestPluginPigEqualsGoNativePig` GREEN. Docker `golang:1.26 -race` GREEN over Float/Jump/Fluid/oracle (3.3s) AND region/strictRegion/race/spawner (13.5s). |

**Score:** 4/4 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `server/fluid.go` | `fluidState.isLava` + `lavaLevelOf` + decodeFluid lava branch | ✓ VERIFIED | `isLava` field (l.130), `lavaLevelOf` (l.96), lava branch in `decodeFluid` (l.158), `matchesKind` consumer-safe; water decode unchanged (`TestDecodeFluidWaterUnchanged` PASS) |
| `server/fluid_physics.go` | `mobInWater`/`mobInLava`/`mobFluidHeight`/`getFluidJumpThreshold` | ✓ VERIFIED | All four on `*TickLoop`; AABB scan over `fluidAt`; `getFluidJumpThreshold` ports the FULL `Entity.getFluidJumpThreshold` (eye<0.4?0.0:0.4 → pig=0.4) — a MORE faithful port than the plan's flat 0.0 stub, javap-cited |
| `server/jump.go` | `entityJumpStep` (jumpInLiquid +0.04 / jumpFromGround 0.42) | ✓ VERIFIED | Full branch port; `fluidJumpImpulse = 0.03999999910593033` (exact bytecode double), `baseJumpPower = 0.42`, `landJumpDelay`/`noJumpDelay=10`, `isInShallowFluid` lava-shallow gate, `isAffectedByFluids` |
| `server/ai_mob.go` | `jumpControl`+`noJumpDelay` on mobAI; JUMP slot; `addGoal(0,newFloatGoal())` | ✓ VERIFIED | `jumpControl` type + mobAI fields; slot after `navigation.tick`; FloatGoal@0 first in newPigAI (l.222) |
| `server/entity.go` | `jumping bool` + `setJumping` | ✓ VERIFIED | `jumping` (l.77), `setJumping` (l.296), `isAffectedByFluids`→true (l.304, javap-cited) |
| `server/plugin_entity.go` | `entity.in_water/fluid_height/in_lava` + `nav.jump()` | ✓ VERIFIED | Read attrs (l.191/196/201) in AttrNames (l.215) via `h.t.mob*`; `nav.jump` (l.739) → `jumpControl.doJump()` (l.786), gated capNav |
| `server/ai_goals_float.go` | Go-native floatGoal | ✓ VERIFIED | `floatGoal` + `newFloatGoal(newBaseGoal(flagJump))`; canUse no-RNG; tick single draw; requiresUpdateEveryTick=true |
| `plugins/vanilla_pig/main.star` | FloatGoal@0 declaration | ✓ VERIFIED | `priority=0, flags=["JUMP"], requires_update_every_tick=True`; draw in `float_tick` |
| `server/assets/vanilla_pig/main.star` | FloatGoal@0 (copy) | ✓ VERIFIED | byte-identical to the repo copy (`diff` → IDENTICAL) |
| `server/float_goal_test.go` | wet-behavior test | ✓ VERIFIED | `TestFloatGoalKeepsPigAfloat` (differential, not a stub) + `TestFloatGoalCanUse` + `TestFloatGoalTickDraws` all PASS |

### Key Link Verification

| From | To | Via | Status |
|------|-----|-----|--------|
| `serverAiStep` | `jumpControl.tick` + `entityJumpStep` | JUMP slot after navigation.tick | ✓ WIRED (l.175 → l.185-186) |
| `entityJumpStep` | `e.vy` | jumpInLiquid +0.04 / jumpFromGround 0.42 | ✓ WIRED |
| `nav.jump` | `jumpControl.doJump` | navHandle mutate | ✓ WIRED (l.786) |
| `newPigAI` | `newFloatGoal` | `addGoal(0, newFloatGoal())` | ✓ WIRED (l.222) |
| `floatGoal.tick` | `jumpControl.doJump` | `nextFloat()<0.8` | ✓ WIRED |
| `main.star float_tick` | `nav.jump` | `rand_float()<0.8` lockstep | ✓ WIRED (both copies) |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build CGO=0 | `CGO_ENABLED=0 go build ./...` | exit 0 | ✓ PASS |
| Float/Fluid/Jump/InWater tests | `go test ./server/ -run 'Float\|Fluid\|Jump\|InWater'` | ok 1.3s | ✓ PASS |
| The pig oracle GATE | `go test ./server/ -run TestPluginPigEqualsGoNativePig` | PASS | ✓ PASS |
| Full server suite | `go test ./server/ -count=1` | ok 39.2s | ✓ PASS |
| No gopy dep (CGO=0) | `go list -deps ./... \| grep -i gopy` | empty | ✓ PASS |
| Both main.star FloatGoal@0 identical | `diff` repo vs server/assets | IDENTICAL | ✓ PASS |
| Draw in tick() not canUse | grep `rand_float()` in float_tick only | confirmed | ✓ PASS |
| Docker -race (Float/Jump/oracle) | `golang:1.26 ... -race` | ok 3.3s | ✓ PASS |
| Docker -race (region/strictRegion/spawner) | `golang:1.26 ... -race` | ok 13.5s | ✓ PASS |

Note: an initial cold-container race run reported a spurious `undefined: pk` compile error; a clean re-run after module download (and a Linux `go vet ./server/` = exit 0) confirmed it was a transient first-container module-download artifact, not a source defect. Both race lanes are green.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| MOB-SUB-04 | 30-02, 30-03 | Mob JumpControl impulse seam consumed in serverAiStep's JUMP slot, claimable by a goal's JUMP flag | ✓ SATISFIED | jumpControl + JUMP slot + entityJumpStep + FloatGoal@0 consumer; jar order verified; oracle GREEN |
| MOB-SUB-05 | 30-01 | Mob fluid predicates (`mobIsInWater`/`mobFluidHeight`/`isInLava`) on `fluidAt` for `*Entity` | ✓ SATISFIED | lava decode + four predicates; tests prove water+lava reads + 0.4/0.0 threshold; water sim unperturbed |

No orphaned requirements: ROADMAP maps only MOB-SUB-04/05 to Phase 30, both claimed by plans, both satisfied.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| server/fluid.go | 14 | "placeholder" in a doc-comment | ℹ️ Info | Benign — describes a prior refactor (type now in fluid_schedule.go), not a stub |

No blocker or warning anti-patterns. No TODO/FIXME/unimplemented stubs in the phase-modified files. All "deferred" items (lava flow sim, float-pathing-over-water node evaluator, per-type eye-height read) are explicitly cited jar-faithful deferrals with default-value seams per CLAUDE.md, not silent stubs.

### Note on a Positive Deviation (no override needed)

Plan 30-01 Task 2 specified a flat `getFluidJumpThreshold()` returning `0.0` (const `mobFluidJumpThreshold = 0.0`). The shipped code instead ports the FULL `Entity.getFluidJumpThreshold` bytecode (`getEyeHeight() < 0.4 ? 0.0 : 0.4`), so the pig reads `0.4`. This is MORE jar-faithful than the plan asked for (CLAUDE.md's 1:1 mandate), is javap-cited, is tested (`TestFluidJumpThreshold` asserts pig=0.4, short-mob=0.0), and is mirrored consistently in both main.star copies (`FLUID_JUMP_THRESHOLD = 0.4`). The success-criterion intent ("jar-correct values") is exceeded, not missed — no gap.

### Human Verification Required

#### 1. Pig floats in water on a live client

**Test:** Drop a pig into a body of water on a real 26.2 client.
**Expected:** The pig bobs / swim-jumps to stay at the surface and does NOT sink to the bottom + suffocate — vanilla float behavior.
**Why human:** In-game visual buoyancy cannot be confirmed programmatically; the operator validates the FloatGoal feel live after each phase per the standing visual-gate process. The differential `TestFloatGoalKeepsPigAfloat` proves the impulses fire and the pig stays in the water column, but the rendered surface-bob is a human observation. This is the only outstanding item — it is NOT a gap.

### Gaps Summary

No gaps. All 4 ROADMAP success criteria, both requirements (MOB-SUB-04/05), all 10 artifacts, and all 6 key links are verified in the live codebase. The pig oracle gate `TestPluginPigEqualsGoNativePig` is GREEN, CGO=0 build clean, no gopy dependency, Docker `-race` clean on both lanes, and FloatGoal@0 is in lockstep across the Go-native oracle and BOTH byte-identical main.star copies with the RNG draw correctly confined to `tick()`. Status is `human_needed` solely because the standing in-game visual confirmation (pig floats) remains the operator's to validate — not a code defect.

---

_Verified: 2026-06-29_
_Verifier: Claude (gsd-verifier)_
