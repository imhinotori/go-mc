---
phase: 31-panicgoal-s2-consumer
reviewed: 2026-06-30T00:00:00Z
depth: standard
files_reviewed: 10
files_reviewed_list:
  - server/ai_goals_panic.go
  - server/ai_goals_panic_test.go
  - server/entity.go
  - server/combat_mob.go
  - server/plugin_entity.go
  - server/ai_mob.go
  - plugins/vanilla_pig/main.star
  - server/assets/vanilla_pig/main.star
  - server/plugin_pig_test.go
  - server/ai_mob_test.go
findings:
  critical: 0
  warning: 3
  info: 2
  total: 5
status: issues_found
---

# Phase 31: Code Review Report

**Reviewed:** 2026-06-30
**Depth:** standard (jar-fidelity focused — CFR re-disassembly of PanicGoal / Pig / DefaultRandomPos / RandomPos)
**Files Reviewed:** 10
**Status:** issues_found (0 BLOCKER, 3 WARNING, 2 INFO)

## Summary

PanicGoal@1 is a faithful 1:1 port. I re-disassembled `net.minecraft.world.entity.ai.goal.PanicGoal`,
`Pig.registerGoals`, `DefaultRandomPos.getPos`, and `RandomPos` from `temp/cache/26.2-inner.jar` with CFR
and verified every claimed bytecode citation against the Go port. The high-risk items in the review brief
all check out:

- **canUse branch order** — `shouldPanic` gate returns FIRST (line 126-128), before any RNG; then the
  (dead) on-fire branch; then `findRandomPosition`. Matches the jar exactly.
- **shouldPanic** — `hasLastDamage && lastDamageSource.is("panic_causes")` (ai_goals_panic.go:92). Uses
  the real not-null bool, NOT `was_hurt`/`hurtTime`, NOT `typeTag==0`. `is()` is the genuine data/tag read
  (damage_source.go:78), never const false. Decision B/C correctly implemented.
- **findRandomPosition** — reuses `generateRandomDirection(r, 5, 4)` verbatim (x/y/z draw order), 10
  unconditional candidates (30 nextInt), DefaultRandomPos mode (no up-snap, landMode=false). Verified
  against `DefaultRandomPos.getPos(mob, 5, 4)` -> `RandomPos.generateRandomPos`.
- **Pig.registerGoals** — javap-confirmed `addGoal(1, new PanicGoal(this, 1.25))`. priority 1, speed 1.25
  match. canContinueToUse keys on hasTarget (== `!navigation.isDone()`). start hands candidates with
  landMode=false.
- **RNG lockstep** — canUse short-circuits before the first draw on BOTH the Go goal (ai_goals_panic.go:126)
  AND both `.star` copies (main.star:90). The two `.star` files are **byte-identical** (`diff` empty,
  verified). Draw order/radii/landMode match across all three. The dry oracle pig never panics -> zero
  draws -> byte-identical (sound).
- **damage_in_tag / has_last_damage** — genuine `e.lastDamageSource.is(tagName)` (plugin_entity.go:291),
  not a duplicated id set in Starlark. `hasLastDamage` is set ONLY in the flag2 block (combat_mob.go:116)
  and never cleared by the hurtTime decrement (grep-confirmed: no other writer). Persistent not-null
  signal — faithful.
- **Executor's auto-fix** (TestPigGoalSetRegistered 4->5, ai_mob_test.go:137) — legitimate. It asserts the
  genuine goal-count invariant the plan changed; the @1 panicGoal/flagMove type check (line 155-157) is
  correct. No papering-over.
- **Tag ids** — `minecraft:fall`=10 (NOT in panic_causes), `minecraft:player_attack`=34 and
  `minecraft:mob_attack`=28 (both IN panic_causes set). Test premises valid.
- **No new deps** — go.mod/go.sum untouched across the commit range; the new goal file imports nothing.

The findings below are all faithful-scope deviations that are cited (not silent), plus two test-robustness
items. None block shipping the phase as the keystone's first consumer.

## Warnings

### WR-01: `isOnFire` hard-stub silently drops the burning-pig water-flee gameplay

**File:** `server/ai_goals_panic.go:102`
**Issue:** `isOnFire(_ *Entity) bool { return false }` is a constant. The vanilla default of `isOnFire()`
for a non-burning entity is indeed false, but a pig genuinely CAN burn (lava, fire blocks, lightning, flame
enchant). With this stub, a burning panicking pig will flee to a random land position instead of seeking
water — observably different from vanilla. Per the CLAUDE.md mandate, a stub is permitted only when the
dependent subsystem does not yet exist; here the prerequisite (Entity fire-tick / `remainingFireTicks`
state) genuinely is absent, and the deviation IS cited with an upgrade path (read `remainingFireTicks > 0`).
The canUse branch structure is preserved so it becomes a real read later. Acceptable faithful-scope, but it
is a behavior gap, not a true no-op — flagging so it is tracked, not lost.
**Fix:** When fire-tick state lands, replace with `return e.remainingFireTicks > 0` (Entity.isOnFire). Keep
the citation. No change needed now beyond ensuring this lands on a phase-33 / fire-subsystem to-do list.

### WR-02: dead on-fire branch would route water-block coords through the ground-snap, perturbing the target

**File:** `server/ai_goals_panic.go:131-136`
**Issue:** The on-fire branch sets `g.wantCandidates = [][3]float64{bp, bp, ... ×10}` and returns true,
which `start()` hands to `setWantCandidates` -> `snapStrollWant`. But vanilla's on-fire branch sets
posX/posY/posZ to the EXACT water block coords and `start()` calls `navigation.moveTo(those, speed)`
directly — no candidate re-selection, no `moveUpOutOfSolid` ground-snap. Because `lookForWater` is itself a
false-stub today this branch is unreachable dead code, so it is NOT a live bug. But when `isOnFire` /
`lookForWater` are un-stubbed (WR-01's upgrade), routing the water target through the LandRandomPos-mode
ground-snap would up-snap it out of the water column — defeating the very purpose (fleeing INTO water). The
water-flee target must bypass `snapStrollWant`.
**Fix:** When un-stubbing, do not feed the water position through `setWantCandidates`. Add a direct
single-target want (the `setWantTarget`/legacy path_to(3-float) seam) for the on-fire branch, mirroring
vanilla's `moveTo(posX,posY,posZ)` which does no snap. Document the divergence from the stroll candidate path.

### WR-03: `TestPanicGoalFleesOnPanicDamage` cannot prove PanicGoal (not stroll) acquired the target

**File:** `server/ai_goals_panic_test.go:71-85`
**Issue:** The flee test drives 200 ticks and asserts only `pig.ai.hasTarget`. But stroll@6 is ALSO a MOVE
goal that independently sets `hasTarget`. The test cannot distinguish a panic-acquired target from a
stroll-acquired one, so it could pass even if PanicGoal never fired (false green). The assertion message
claims "PanicGoal did not fire" but the test does not actually establish that. (PanicGoal@1 does preempt
stroll@6 by priority, so in practice panic wins the first eligible tick — but the test does not verify the
preemption.)
**Fix:** Either (a) assert `newPanicGoal(...).canUse(loop, pig) == true` directly right after the hit
(deterministic, isolates the panic path, mirroring the non-panic test's direct-canUse style), or (b) gate
the hasTarget assertion on the panic goal's running state / inspect which goal holds the MOVE flag via the
goalSelector. The sibling `TestPanicGoalIgnoresNonPanicDamage` already uses the cleaner direct-canUse style.

## Info

### IN-01: `panicSpeedModifier` 1.25 is stored but never wired into navigation speed (cited-deferred)

**File:** `server/ai_goals_panic.go:59, 180-187`
**Issue:** The goal carries `speedModifier = 1.25` but `start()` only hands position candidates; the v1 nav
uses the single `pigWalkSpeed = 0.15` const for all goals. So a panicking pig walks at the same pace as a
strolling pig instead of 1.25x. This is consistent between the Go goal and both `.star` copies (no oracle
divergence) and is cited with the same posture as stroll's speedModifier. Faithful-scope, documented. Listed
as INFO for the eventual per-request-speed nav upgrade.
**Fix:** When `navigation.moveTo` takes a per-request speed, multiply the base pace by `speedModifier`
(1.25 for panic, 1.0 for stroll). The faithful value is already stored on the goal.

### IN-02: `lookForWater` false-stub — acceptable while the on-fire branch is unreachable

**File:** `server/ai_goals_panic.go:113-115`
**Issue:** `lookForWater` returns `ok=false` unconditionally. Its RNG-free spiral scan
(`BlockPos.findClosestMatch(mp, 5, 1, isWater)`) is faithfully cited as the upgrade path. Because `isOnFire`
is false, this stub is never consulted, so it draws zero RNG and carries no oracle risk today. Tied to
WR-01/WR-02 for the un-stub.
**Fix:** Implement the real `findClosestMatch` over `getFluidState(WATER)`, gated on the mob's own block
having empty collision, when fluid nav lands. Pair with the WR-02 direct-target fix.

---

_Reviewed: 2026-06-30_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
