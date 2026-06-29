---
phase: 30-jumpcontrol-fluid-s1
plan: 02
subsystem: mob-ai
tags: [jumpcontrol, jump-impulse, fluid, mob-ai, entity, plugin-handle, 1to1-jar-port]

# Dependency graph
requires:
  - phase: 30-jumpcontrol-fluid-s1
    plan: 01
    provides: "mobInWater / mobInLava / mobFluidHeight(kind) / getFluidJumpThreshold on *TickLoop; fluidWater/fluidLava kinds"
  - phase: 29-damage-keystone-s2
    provides: "*Entity plain-value tick-owned field block; serverAiStep RNG-confinement discipline; navHandle.stop mutate shape"
provides:
  - "jumpControl{jump bool} (JumpControl analogue: doJump arms, tick→setJumping+clear) + noJumpDelay int on mobAI"
  - "jumping bool + setJumping(bool) + isAffectedByFluids() (const-true cited) on *Entity"
  - "the serverAiStep JUMP slot after navigation.tick (jar order, RNG-free): jumpControl.tick + entityJumpStep"
  - "entityJumpStep — the LivingEntity.aiStep jump branch (jumpInLiquid +0.04 water/lava, jumpFromGround 0.42 land + noJumpDelay=10)"
  - "jumpInLiquid (vy += 0.03999999910593033) + jumpFromGround (vy = max(0.42, vy)) + isInShallowFluid"
  - "entity.in_water/fluid_height/in_lava frozen-scalar read attrs + nav.jump() mutate (capNav → jumpControl.doJump)"
affects: [30-03-floatgoal, 31-panicgoal, panicgoal, leapgoal, mob-fluid-nav]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "JumpControl as a plain value type on mobAI; doJump/tick coalesce a per-tick jump intent to one bool (idempotent, DoS-bounded)"
    - "the aiStep jump branch as a TickLoop method reading the Plan-01 fluid predicates; impulse mutates e.vy in tickAI BEFORE tickPhysics gravity"
    - "cited-constant ports for not-yet-built reads (getJumpPower=0.42 from JUMP_STRENGTH default; isAffectedByFluids/isSprinting/needsSync) structured to become real reads later"

key-files:
  created:
    - "server/jump.go"
    - "server/jump_test.go"
  modified:
    - "server/ai_mob.go"
    - "server/entity.go"
    - "server/plugin_entity.go"

key-decisions:
  - "fluidJumpImpulse ported as the EXACT jar double 0.03999999910593033 (the ldc2_w literal), NOT a clean 0.04 — the 1:1 numeric-op mandate"
  - "jumpFromGround sets vy = max(0.42, vy) (Math.max(getJumpPower, dm.y)), NOT a plain assignment — a mob already rising faster keeps its speed (jar-faithful)"
  - "noJumpDelay decrements at the TOP of serverAiStep (jar aiStep top) AND is reset to 0 when the mob is not jumping / not in a fluid (both falsey paths of the branch) — the full jar control flow, not just the decrement"
  - "isAffectedByFluids/isSprinting/needsSync ported as cited const-true/const-false/deferred (port the minimum the branch reads; CONTEXT Discretion), structured to become real reads"

patterns-established:
  - "jumpControl value type + doJump/tick coalescing; the RNG-free JUMP slot inserts after navigation.tick"
  - "nav.jump() mutate routing to jumpControl.doJump (capNav-gated, owner-resolved, idempotent)"

requirements-completed: [MOB-SUB-04]

# Metrics
duration: 9min
completed: 2026-06-29
---

# Phase 30 Plan 02: JumpControl + Fluid (S1) — Mob Jump Chain + Handle Ops Summary

**Ported the full mob jump chain 1:1 from the jar — jumpControl + noJumpDelay + jumping/setJumping, the RNG-free JUMP slot in serverAiStep after navigation.tick, and the aiStep jump branch (jumpInLiquid +0.04 for water/lava, jumpFromGround 0.42 for land with a 10-tick delay gate) — plus the plugin handle ops entity.in_water/fluid_height/in_lava (frozen scalars) and nav.jump(), closing MOB-SUB-04. The mechanism is PURE (no new RNG), so the pig oracle stays byte-identical.**

## Performance

- **Duration:** ~9 min
- **Started:** 2026-06-29T22:24:23Z
- **Completed:** 2026-06-29T22:33:01Z
- **Tasks:** 3 (all TDD)
- **Files:** 5 (2 created, 3 modified)

## Accomplishments

- `jumpControl{jump bool}` value type on `mobAI` (the `net.minecraft.world.entity.ai.control.JumpControl` analogue): `doJump()` arms the flag; `tick(e)` calls `e.setJumping(jump)` then clears it — a per-tick jump intent coalesced to one bool (idempotent, the T-30-06 DoS bound). `noJumpDelay int` on `mobAI` (LivingEntity.noJumpDelay).
- `jumping bool` + `setJumping(bool)` + `isAffectedByFluids()` (const-true, cited) on `*Entity`, in the MOB-SUB plain-value tick-owned field block.
- The JUMP slot in `serverAiStep` AFTER `navigation.tick` (jar order `goals → navigation → moveControl/lookControl/jumpControl`): `noJumpDelay--` at the top of the step, then `jumpControl.tick(e)` + `entityJumpStep(e)`. PURE — no RNG draw — so the pig oracle is unperturbed.
- `entityJumpStep` ports the `LivingEntity.aiStep` jump branch verbatim: `if (jumping && isAffectedByFluids())` → water swim impulse / lava swim impulse / land jump, with the exact disjuncts (`inWaterAndHasFluidHeight && (!onGround || fluidHeight>threshold)`, `isInLava && (!onGround || !isInShallowFluid(LAVA))`, `(onGround || (inWaterAndHasFluidHeight && fluidHeight<=threshold)) && noJumpDelay==0`). Both `!jumping` and `!isAffectedByFluids` paths reset `noJumpDelay = 0` (the full jar control flow).
- `jumpInLiquid` (`vy += 0.03999999910593033`, the exact jar double), `jumpFromGround` (`vy = max(0.42, vy)` with the sprint nudge cited const-false), `isInShallowFluid` (`getFluidHeight(tag) <= getFluidJumpThreshold()`). The impulse lands in `tickAI` BEFORE `tickPhysics` integrates gravity (confirmed tick ordering), so it is not cancelled the same tick.
- Plugin handle ops: `entity.in_water`/`fluid_height`/`in_lava` frozen-scalar reads (host-computed via `h.t.mob*`, re-resolved through `h.store()` per Pitfall 7, gated `capEntitiesRead`, registered in `Attr` + `AttrNames`) and `nav.jump()` (gated `capNav`, owner-resolved, routes to `e.ai.jumpControl.doJump()`, errors cleanly on removed/non-AI).

## Task Commits

Each task committed atomically (all three TDD: RED via the shared jump_test.go, then GREEN):

1. **Task 1: jumpControl + noJumpDelay + jumping/setJumping + the JUMP slot** — `eb3c20f0` (feat)
2. **Task 2: the aiStep jump branch (jumpInLiquid +0.04 / jumpFromGround 0.42)** — `a9306b00` (feat)
3. **Task 3: handle attrs in_water/fluid_height/in_lava + nav.jump()** — `c0888bd2` (feat)

_Note: the three tasks share one test file; the package compiles only once all three GREENs exist. Following Plan 01's documented approach, all GREENs were implemented, the full suite was run, then the files were committed task-scoped (ai_mob.go+entity.go+jump_test.go for Task 1, jump.go for Task 2, plugin_entity.go for Task 3)._

## Files Created/Modified

- `server/ai_mob.go` — `jumpControl` value type + `doJump`/`tick`; `jumpControl`+`noJumpDelay` fields on `mobAI`; the `noJumpDelay--` decrement + JUMP slot in `serverAiStep`.
- `server/entity.go` — `jumping bool` field; `setJumping(b)`; `isAffectedByFluids()` (const-true cited).
- `server/jump.go` (new) — `entityJumpStep` (the aiStep jump branch); `jumpInLiquid`/`jumpFromGround`/`isInShallowFluid`; the `fluidJumpImpulse`/`baseJumpPower`/`jumpPowerEpsilon`/`landJumpDelay` cited consts.
- `server/plugin_entity.go` — `in_water`/`fluid_height`/`in_lava` read cases + AttrNames; `navHandle.jump` mutate + Attr/AttrNames registration.
- `server/jump_test.go` (new) — headless gates: jumpControl set/clear, the RNG-free JUMP slot, noJumpDelay decrement, water/lava/land impulses, the delay gate, non-jumping no-op, the handle reads (frozen scalars + cap gate), nav.jump (arm + capNav + removed-entity clean error).

## Decisions Made

- **fluidJumpImpulse = the exact jar double 0.03999999910593033**, not a clean 0.04 — the `ldc2_w` literal from `LivingEntity.jumpInLiquid`. The 1:1 numeric-op mandate forbids paraphrasing the literal.
- **jumpFromGround sets vy = max(0.42, vy)**, mirroring `Math.max((double)getJumpPower(), dm.y)` — NOT a plain assignment. A mob already rising faster than the jump keeps its speed (jar-faithful).
- **noJumpDelay full control flow:** decremented at the top of the step (jar aiStep top `if (noJumpDelay > 0) noJumpDelay--;`) AND reset to 0 on BOTH the `!jumping` and `!isAffectedByFluids` falsey paths of the branch — the complete jar logic, not just the decrement.
- **Cited-constant ports for not-yet-built reads:** `getJumpPower()=0.42` (the JUMP_STRENGTH RangedAttribute default 0.41999998688697815, d2f → 0.42f, × blockJumpFactor 1.0 + jumpBoost 0); `isAffectedByFluids()` const-true (only ArmorStand-style overrides return false); `isSprinting()` const-false (no mob sprint flag yet); `needsSync` deferred (no Sulfur field; the tracker re-sends on the velocity change). Each is structured to become a real read later (CLAUDE.md), never baked away.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - jar-correctness] fluidJumpImpulse is the exact double 0.03999999910593033, not 0.04**
- **Found during:** Task 2 (porting jumpInLiquid)
- **Issue:** The plan/CONTEXT cite `vy += 0.04`. The jar bytecode (`javap LivingEntity.jumpInLiquid`) is `getDeltaMovement().add(0.0, ldc2_w 0.03999999910593033, 0.0)` — the literal is the double `0.03999999910593033`, not a clean `0.04`. Porting `0.04` would violate the 1:1 numeric-op mandate (a different float bit-pattern accumulates differently across repeated swim-jumps).
- **Fix:** Ported the exact `0.03999999910593033` constant with the bytecode literal cited inline. The tests assert with a `1e-12` tolerance against the exact constant.
- **Files:** server/jump.go, server/jump_test.go
- **Committed in:** `a9306b00` (Task 2)

**2. [Rule 1 - jar-correctness] jumpFromGround uses max(power, vy), not vy = power; noJumpDelay resets to 0 on the not-jumping path**
- **Found during:** Task 2 (porting jumpFromGround + the branch)
- **Issue:** The plan summarizes `jumpFromGround` as `vy = 0.42`. The jar is `setDeltaMovement(dm.x, Math.max((double)f, dm.y), dm.z)` — a max, not an assignment. Separately, the plan's branch sketch omits that the `!jumping`/`!isAffectedByFluids` paths set `noJumpDelay = 0` (the `ifeq 474` targets in the bytecode).
- **Fix:** Ported `vy = math.Max(f, e.vy)` and the `noJumpDelay = 0` reset on both falsey paths, matching the jar control flow exactly. `TestJumpFromGround` asserts a falling mob (vy=-1.0) jumps to 0.42 (the max).
- **Files:** server/jump.go
- **Committed in:** `a9306b00` (Task 2)

**3. [Rule 1 - test correction] TestNoJumpDelay isolated the decrement from the branch's reset**
- **Found during:** Task 1 GREEN (the RED test asserted noJumpDelay 2→1 with a non-jumping dry mob)
- **Issue:** The first-written test expected the decrement alone, but the jar branch resets `noJumpDelay = 0` for a non-jumping mob — so a dry non-jumping mob goes 2→0 in one step (decrement to 1, then reset to 0), which is CORRECT vanilla behavior. The test's assertion was wrong, not the port.
- **Fix:** Rewrote the test to put the mob in a deep water column AND arm the jump each step (so it takes the jumpInLiquid branch, which never touches noJumpDelay), isolating the top-of-step decrement: 2→1→0→0 (clamped). This makes the test faithful to the jar's full control flow.
- **Files:** server/jump_test.go
- **Committed in:** `eb3c20f0` (Task 1)

---

**Total deviations:** 3 auto-fixed (all jar-correctness / test-faithfulness; mandated by the 1:1 rule). No scope creep — each is a cited literal/control-flow correction. The must_haves truth "a jumping mob in water gains vy += 0.04" is delivered as the exact jar double 0.03999999910593033 (which IS the jar's "+0.04").

## Issues Encountered

- The shared test file means the package compiles only after all three GREENs exist (same as Plan 01). Resolved by implementing all GREENs, running the full suite, then committing files task-scoped.
- `-race` needs CGO + gcc (not on PATH); ran the race gate in a `golang:1.26` Docker container (`MSYS_NO_PATHCONV=1` to stop Git Bash mangling the bind-mount). Clean over the jump + handle + region + damage tests (strictRegion paths exercised).

## Verification

- `CGO_ENABLED=0 go build ./...` clean; `go vet ./...` clean.
- `CGO_ENABLED=0 go test ./server/ -count=1` GREEN (27.8s) — all jump tests + handle tests + the full existing suite.
- `TestPluginPigEqualsGoNativePig` GREEN — the JUMP mechanism adds NO RNG draw, so the oracle is byte-identical.
- Docker `-race` GREEN over the jump/handle/region/damage tests (6.9s).
- `go list -deps ./... | grep -i gopy` empty (no gopy leak); no global `math/rand` introduced in the new files.
- Every ported method cites its jar class/method; the 0.03999999910593033 / 0.42 / 10 / 1.0E-5 constants are javap-verified.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- **Plan 30-03 (FloatGoal@0 lockstep)** can now wire the Go-native FloatGoal (calling `e.ai.jumpControl.doJump()` in its tick) AND the plugin FloatGoal (`nav.jump()` + reading `entity.in_water`/`fluid_height`/`in_lava` in canUse/tick). The mechanism is in place; Plan 03 adds the only new RNG (FloatGoal.tick's `nextFloat() < 0.8`) inside the goal callback, in lockstep on both pigs.
- **Carryover for Plan 03:** `entity.fluid_height` returns the WATER height; FloatGoal.canUse compares it against `getFluidJumpThreshold()` which Plan 01 found is **0.4** for the pig (not 0.0). Plan 03 must surface that threshold consistently to both the Go-native and plugin canUse (e.g. a `fluid_jump_threshold` handle attr or a shared plugin constant matching 0.4) so the two halves agree in lockstep.
- **Deferred (cited):** a per-type `JUMP_STRENGTH` attribute read (currently the cited 0.42 default); a mob `sprinting` flag (the sprint-jump nudge is a no-op until then); the `needsSync` delta-codec marker; a per-type `isAffectedByFluids` override (ArmorStand-style false).

## Self-Check: PASSED

---
*Phase: 30-jumpcontrol-fluid-s1*
*Completed: 2026-06-29*
