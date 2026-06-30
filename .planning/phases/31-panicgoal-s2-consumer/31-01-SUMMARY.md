---
phase: 31-panicgoal-s2-consumer
plan: 01
subsystem: mob-ai
tags: [panicgoal, mob-ai, damage-source, starlark-plugin, lockstep-rng, goal-selector, jar-port]

# Dependency graph
requires:
  - phase: 29-damage-keystone-s2
    provides: lastDamageSource keystone (damageSource value + is(tag) genuine data/tag read), applyDamageEntity flag2 fresh-hit store
  - phase: 30.1-faithful-randomstrollgoal-target-selection
    provides: generateRandomDirection (x/y/z draw order), setWantCandidates -> snapStrollWant candidate machinery, path_to 31-float overload
  - phase: 30-jumpcontrol-fluid-s1
    provides: FloatGoal@0 (the one-goal-per-file precedent + NO-RNG-short-circuit canUse pattern), the dry-pig oracle (TestPluginPigEqualsGoNativePig)
provides:
  - PanicGoal@1 (MOVE flag, speed 1.25) on the pig — Go-native newPigAI + both vanilla_pig/main.star copies, in lockstep
  - Entity.hasLastDamage bool — the faithful LivingEntity.getLastDamageSource() != null not-null signal (Decision B)
  - damage_in_tag(name) plugin handle — host-side DamageSource.is(TagKey) membership read (Decision C)
  - has_last_damage plugin read attr
  - TestPanicGoalFleesOnPanicDamage + TestPanicGoalIgnoresNonPanicDamage regression pair
affects: [phase-33-mob-gate, phase-36-wolf-anger, any goal consuming lastDamageSource]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Damage-keystone consumer goal: shouldPanic = hasLastDamage && lastDamageSource.is(tag) — a genuine not-null signal + genuine tag read, never a faked flag"
    - "Host-side tag-membership plugin handle (damage_in_tag) keeps the id set on the Go side; the plugin tests membership by NAME"
    - "Flee candidates reuse the stroll candidate/snap path at radius (5,4) — no separate snapFleeWant (Decision A)"

key-files:
  created:
    - server/ai_goals_panic.go
    - server/ai_goals_panic_test.go
  modified:
    - server/entity.go
    - server/combat_mob.go
    - server/plugin_entity.go
    - server/ai_mob.go
    - plugins/vanilla_pig/main.star
    - server/assets/vanilla_pig/main.star
    - server/plugin_pig_test.go
    - server/ai_mob_test.go

key-decisions:
  - "Decision A: reuse snapStrollWant radius-free — flee candidates flow through setWantCandidates(landMode=false), no snapFleeWant"
  - "Decision B: add Entity.hasLastDamage bool (not was_hurt, not typeTag==0 which is a real panic_causes member id 0)"
  - "Decision C: add damage_in_tag plugin handle wrapping damageSource.is, keeping the panic_causes id set on the Go side"
  - "Pig.registerGoals javap-verified: priority 1, speed 1.25d, PanicGoal(PathfinderMob, double)"
  - "panic speedModifier 1.25 is cited-deferred (the want carries position only; v1 nav uses the single pigWalkSpeed const) — same posture as stroll's speedModifier"
  - "isOnFire + lookForWater are cited false-stubs (no fire-tick / fluid-spiral state yet) — the on-fire water-flee branch never runs for a non-burning pig, drawing ZERO RNG"

patterns-established:
  - "PanicGoal: shouldPanic gate is the FIRST line of canUse — RETURN BEFORE ANY RNG (the dry oracle pig draws zero draws, stays byte-identical)"
  - "Lockstep goal addition: Go newPigAI + both .star copies in ONE plan, gated on the pig oracle + diff-empty"

requirements-completed: [MOB-GATE-01]

# Metrics
duration: ~35min
completed: 2026-06-30
---

# Phase 31 Plan 01: PanicGoal (S2 consumer) Summary

**PanicGoal@1 (MOVE, speed 1.25) wired onto the pig in lockstep (Go newPigAI + both vanilla_pig.star copies): a panic_causes hit (player_attack) makes the pig flee via the reused DefaultRandomPos(5,4) candidate/snap path; a non-panic hit (minecraft:fall) does not. shouldPanic = hasLastDamage && lastDamageSource.is("panic_causes") — a genuine not-null signal + genuine tag read, the first real consumer of the Phase-29 keystone.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-06-30
- **Completed:** 2026-06-30
- **Tasks:** 4
- **Files modified:** 10 (2 created, 8 modified)

## Accomplishments
- Ported `net.minecraft.world.entity.ai.goal.PanicGoal` 1:1 to `server/ai_goals_panic.go` (canUse zero-RNG short-circuit, findRandomPosition reuses generateRandomDirection at radius 5/4, lookForWater + isOnFire cited false-stubs, start hands candidates landMode=false, canContinueToUse keys on hasTarget)
- Added the faithful `Entity.hasLastDamage` not-null signal (set in hurtServer's flag2 block) + the `damage_in_tag` host-side plugin handle (Decisions B + C)
- Declared PanicGoal@1 in BOTH `vanilla_pig/main.star` copies, byte-identical, drawing the identical 30-nextInt stream IF a panic fires
- Added the flee + non-panic regression pair; updated all three pig-goal-count assertions to 5 goals at {0,1,6,7,8}
- Pig oracle (TestPluginPigEqualsGoNativePig) stays byte-identical; Docker `-race` over ./server/ fully clean (0 races, 0 failures)

## Task Commits

1. **Task 1: has_last_damage signal + damage_in_tag handle** - `23b717e8` (feat)
2. **Task 2 (TDD): panicGoal RED test** - `8edcd77d` (test) — **GREEN impl + register** - `757f6477` (feat)
3. **Task 3: both .star copies (lockstep)** - `160625b3` (feat)
4. **Task 4: boot-load assertions to 5 goals + gates** - `bfdd9441` (test)

**Plan metadata:** _this commit_ (docs: complete plan)

## Files Created/Modified
- `server/ai_goals_panic.go` (created) - the Go-native panicGoal, ported 1:1 from PanicGoal bytecode
- `server/ai_goals_panic_test.go` (created) - TestPanicGoalFleesOnPanicDamage + TestPanicGoalIgnoresNonPanicDamage
- `server/entity.go` - added `hasLastDamage bool` (faithful getLastDamageSource() != null signal)
- `server/combat_mob.go` - set `e.hasLastDamage = true` in the flag2 fresh-hit block
- `server/plugin_entity.go` - `has_last_damage` read attr, `damage_in_tag(name)` bound method, AttrNames
- `server/ai_mob.go` - `addGoal(1, newPanicGoal(panicSpeedModifier))`; newPigAI header (now 0,1,6,7,8)
- `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` - PANIC consts, panic_can_use/panic_stop/panic_continue, goal(@1, MOVE), header (byte-identical)
- `server/plugin_pig_test.go` - boot-load + declaration assertions 4 -> 5 goals, @1 MOVE check
- `server/ai_mob_test.go` - TestPigGoalSetRegistered 4 -> 5 goals, @1 panicGoal/MOVE type check

## Decisions Made
- **Pig.registerGoals (javap-verified):** priority 1, speed 1.25d, `PanicGoal(PathfinderMob, double)` — matches CONTEXT.md. The PanicGoal class bytecode (canUse/shouldPanic/findRandomPosition) was disassembled and confirmed exactly equal to the CONTEXT pseudocode (shouldPanic gate first → isOnFire/lookForWater → DefaultRandomPos.getPos(mob, 5, 4)).
- **panic speedModifier 1.25 is cited-deferred** (NOT wired): the want carries POSITION only, and the v1 nav uses the single `pigWalkSpeed` const — the same posture as stroll's speedModifier. The faithful 1.25 is stored on the goal for the upgrade where navigation.moveTo takes a per-request speed.
- **isOnFire / lookForWater are cited false-stubs.** No `remainingFireTicks` fire-tick state and no fluid-aware spiral-scan helper exist yet. `isOnFire` returns false (a pig is rarely on fire) so the on-fire water-flee branch never runs — faithful for a non-burning pig, drawing ZERO RNG. Upgrade paths documented on both: read `remainingFireTicks > 0` when fire-tick state lands; a real `findClosestMatch` over `getFluidState(WATER)` when fluid nav lands.
- **Oracle stayed green** with PanicGoal@1 added to both halves: the dry oracle pig never takes damage → shouldPanic false → canUse returns on line 1 → zero draws → byte-identical.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated a third stale pig-goal-count assertion (TestPigGoalSetRegistered)**
- **Found during:** Task 4 (Docker -race full-suite run)
- **Issue:** The plan named only TestPluginPigBootLoads + TestVanillaPigDeclaresGoalSet as the boot-load assertions to update, but a THIRD test — `server/ai_mob_test.go::TestPigGoalSetRegistered` — also asserts the Go-native pig goal count == 4 and the per-priority goal types. Adding PanicGoal@1 (5 goals) broke it. The targeted CGO=0 test set did not include it, so it surfaced only in the full Docker `-race` run.
- **Fix:** Updated the count assertion 4 -> 5 and added an `@1 panicGoal / flagMove` type assertion (mirroring the existing @0/@6/@7/@8 checks). Also corrected two stale docstring comments in plugin_pig_test.go (3 -> PanicGoal@1 + 3 passive).
- **Files modified:** server/ai_mob_test.go, server/plugin_pig_test.go
- **Verification:** Full Docker `-race` re-run: EXIT=0, 0 DATA RACE, 0 FAIL.
- **Committed in:** `bfdd9441` (Task 4 commit)

---

**Total deviations:** 1 auto-fixed (1 bug — a stale assertion in an unnamed sibling test, directly caused by this plan's goal addition)
**Impact on plan:** The fix was required for correctness (the goal set IS now 5). No scope creep — it is the same 4→5 update the plan prescribes for the two named boot-load tests, applied to a third test that asserts the identical invariant.

## Issues Encountered
- The first Docker `-race` run surfaced the scary `TestRegionPanicIsolated` recovered-panic stack (the KNOWN false alarm noted in the verification commands) AND the genuine `TestPigGoalSetRegistered` failure. Distinguished them by re-running with `-v`: 0 DATA RACE, exactly one `--- FAIL` (TestPigGoalSetRegistered). Fixed the real failure; the recovered-panic stack is expected noise from the panic-isolation test.

## TDD Gate Compliance
Task 2 followed RED → GREEN: a `test(...)` commit (`8edcd77d`, undefined-symbol RED) precedes the `feat(...)` commit (`757f6477`, implementation). No separate refactor commit was needed (the port was clean on first pass).

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- MOB-GATE-01 advanced: the pig now has 5 of its 8 goals ported (@0/@1/@6/@7/@8). The parity gate closes in Phase 33 with the remaining @3 BreedGoal / @4 TemptGoal / @5 FollowParentGoal (deferred: entity aging/breeding + held-item tags unbuilt).
- The lastDamageSource keystone now has a proven consumer pattern (hasLastDamage + is(tag) + damage_in_tag) that Phase 36 wolf-anger (an attacker read) can follow.
- Cited-deferred upgrade paths recorded: panic speedModifier wiring, isOnFire fire-tick read, lookForWater fluid spiral scan.

---
*Phase: 31-panicgoal-s2-consumer*
*Completed: 2026-06-30*

## Self-Check: PASSED
- Created files exist: server/ai_goals_panic.go, server/ai_goals_panic_test.go, 31-01-SUMMARY.md
- All task commits exist: 23b717e8, 8edcd77d, 757f6477, 160625b3, bfdd9441
