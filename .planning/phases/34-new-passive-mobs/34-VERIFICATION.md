---
phase: 34-new-passive-mobs
verified: 2026-06-30T00:00:00Z
status: passed
score: 6/6 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 5/6
  gaps_closed:
    - "categoryOf maps cow, sheep, AND chicken to CREATURE (ROADMAP SC#1)"
  gaps_remaining: []
  regressions: []
---

# Phase 34: New Passive Mobs Verification Report

**Phase Goal:** Cow, sheep, and chicken exist as jar-faithful Starlark plugins reusing the now-proven 8-goal subsystem set (Float/Panic/Breed/Tempt/FollowParent/Stroll/Look/LookAround), with per-mob extras: cow milking, sheep EatBlockGoal + shear/wool (with regrow), chicken egg-lay + slow-fall. The pig oracle stays byte-identical.
**Verified:** 2026-06-30
**Status:** passed
**Re-verification:** Yes — after gap closure (commit ad61a3de)

## Goal Achievement

### Observable Truths

| #   | Truth                                                                                                  | Status      | Evidence |
| --- | ------------------------------------------------------------------------------------------------------ | ----------- | -------- |
| 1   | All 3 mobs spawn/tick/walk/float/panic/tempt/breed via shared goals AND categoryOf maps all 3 to CREATURE (SC#1) | ✓ VERIFIED  | Goals + spawn/behave VERIFIED (prior pass). GAP CLOSED: categoryOf (mob_category.go:84-100) now maps Pig+Cow+Sheep+Chicken → categoryCreature (sheep at :92-96, chicken at :97-100, both jar-cited `data/entity <Mob>.Type == "creature"`). Cap accounting (spawner.go countByCategory / async.go counts[categoryCreature]) now tallies all 4 passives. TestCategoryOfAllPassivesAreCreature PASS (asserts pig/cow/sheep/chicken → categoryCreature). |
| 2   | Per-mob extras port 1:1: cow milk, sheep EatBlockGoal + shear/wool regrow, chicken egg-lay + slow-fall (SC#2) | ✓ VERIFIED  | tryMilkCow (attack_dispatch.go:897, AbstractCow.mobInteract BUCKET+!isBaby→milk_bucket, jar-cited); sheep_eat.go eatGrassBlock/sheepAte(setSheared(false) regrow)/trySheepShear/shearSheep + dataWoolIndex=18/woolDataEntry; chicken_aistep.go chickenAiStep (vy*=0.6 slow-fall + 2 nextFloat + nextInt(6000) egg-lay). All wired (tick_phases.go:380, handleInteract:773/783). 23 extra tests PASS. |
| 3   | Per-mob hitbox/node sizing correct (SC#3)                                                               | ✓ VERIFIED  | data/entity table carries per-mob dims (chicken 0.4×0.7, cow/sheep distinct) + Type:"creature"; NewEntity(decl.baseType) reads them; TestChickenBootLoads asserts 0.4×0.7. |
| 4   | The pig oracle stays byte-identical (the standing dogfood gate)                                         | ✓ VERIFIED  | TestPluginPigEqualsGoNativePig PASS (re-confirmed post-fix). The gap fix is additive (two new switch cases); the pig + default arms are untouched, so zero new pig RNG draws. |
| 5   | The boot-load generalization: all 4 embeds load; spawnVanillaMob(name); /dbg cow|sheep|chicken; main.go boots all 4 | ✓ VERIFIED  | loadVanillaMobRegistry (vanilla_pig_embed.go:66) loads all 4 with loud failure; spawnVanillaMob (vanilla_pig.go:51); /dbg cow|sheep|chicken (commands_dbg.go:20-31); main.go:347 LoadVanillaMobRegistry. |
| 6   | CGO=0 build/vet clean, full suite green, no new Go deps                                                 | ✓ VERIFIED  | CGO_ENABLED=0 go build ./... exit 0 (re-confirmed post-fix); TestSpawnCapAccounting PASS (cap accounting green). The fix added no Go deps. |

**Score:** 6/6 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| plugins/vanilla_cow,sheep,chicken/main.star + server/assets embeds | byte-identical pairs | ✓ VERIFIED | All 8 pairs (4 mobs × main.star+plugin.toml) diff-empty; pig pair also identical. |
| server/attack_dispatch.go tryMilkCow + createFilledResult | Cow.mobInteract 1:1 | ✓ VERIFIED | Jar-cited (javap AbstractCow.mobInteract + ItemUtils.createFilledResult), wired before tryFeedAnimal, Cow.ID-gated. |
| server/sheep_eat.go eat/shear/wool | Sheep.ate/mobInteract/shear 1:1 + DATA_WOOL | ✓ VERIFIED | eatGrassBlock→sheepAte(setSheared(false) regrow + ageUp), trySheepShear/shearSheep (shears→white wool + 5-nextFloat scatter + setSheared(true)); dataWoolIndex=18 BYTE serializer. |
| server/chicken_aistep.go chickenAiStep + dropChickenEgg | Chicken.aiStep 1:1 | ✓ VERIFIED | Slow-fall vy*=0.6 (!onGround && vy<0); egg-lay --eggTime<=0 → drop egg → 2 nextFloat pitch → nextInt(6000)+6000 reset, jar order. |
| server/mob_category.go categoryOf → CREATURE for all 3 | SC#1 mapping | ✓ VERIFIED | GAP CLOSED: Pig+Cow+Sheep+Chicken all map to categoryCreature. Sheep case (:92-96) and Chicken case (:97-100) added, jar-cited (`data/entity <Mob>.Type == "creature"`). TestCategoryOfAllPassivesAreCreature regression-locks it. |
| server/mob_category_test.go TestCategoryOfAllPassivesAreCreature | regression lock | ✓ VERIFIED | Asserts pig/cow/sheep/chicken all → categoryCreature; PASS. |
| server/vanilla_pig_embed.go / vanilla_pig.go boot-load + spawn | generalized to 4 mobs | ✓ VERIFIED | One registry, per-mob caps, loud-fail; spawnVanillaMob(name) + thin pig wrapper. |

### Key Link Verification

| From | To | Via | Status | Details |
| ---- | --- | --- | ------ | ------- |
| tick_phases.go serverAiStep | chickenAiStep | typ==entity.Chicken.ID gate | ✓ WIRED | Line 379-381, additive, pig is zero-cost skip. |
| handleInteract | tryMilkCow / trySheepShear | mob.typ gate before tryFeedAnimal | ✓ WIRED | attack_dispatch.go:773 (sheep) / :783 (cow). |
| cmd/sulfur/main.go | LoadVanillaMobRegistry → SetMobRegistry | boot | ✓ WIRED | main.go:347-350. |
| async.go natural spawn | spawnVanillaMob via pickNaturalCreatureMob | seeded per-region levelRandom → cap re-check counts[categoryCreature] | ✓ WIRED | GAP CLOSED: pick returns all 4 names AND categoryOf now maps all 4 to categoryCreature, so countByCategoryAcrossRegions[categoryCreature] correctly counts sheep/chicken. The anti-flood cap now bounds all 4 passives. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Build | CGO_ENABLED=0 go build ./... | exit 0 | ✓ PASS |
| Gap regression | CGO_ENABLED=0 go test ./server/ -run TestCategoryOfAllPassivesAreCreature -v | PASS | ✓ PASS |
| Pig oracle | CGO_ENABLED=0 go test ./server/ -run TestPluginPigEqualsGoNativePig -count=1 | PASS | ✓ PASS |
| Spawn-cap accounting | CGO_ENABLED=0 go test ./server/ -run TestSpawnCapAccounting -count=1 | PASS | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| ----------- | ----------- | ----------- | ------ | -------- |
| MOB-PASS-01 | 34-01, 34-04 | Cow plugin (Cow.registerGoals) + CREATURE + milking interact | ✓ SATISFIED | Plugin + tryMilkCow + categoryOf(Cow)→CREATURE all present and tested. |
| MOB-PASS-02 | 34-02, 34-04 | Sheep plugin + EatBlockGoal + shear/wool + regrow | ✓ SATISFIED | Plugin + EatBlockGoal + shear/wool/regrow VERIFIED; categoryOf(Sheep)→CREATURE now present (gap closed), SC#1 mapping clause satisfied. |
| MOB-PASS-03 | 34-03, 34-04 | Chicken plugin + aiStep egg-lay + slow-fall | ✓ SATISFIED | Plugin + slow-fall + egg-lay VERIFIED; categoryOf(Chicken)→CREATURE now present (gap closed), SC#1 mapping clause satisfied. |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| ---- | ---- | ------- | -------- | ------ |
| — | — | None. The prior `default: return categoryMisc swallows Sheep/Chicken` blocker is resolved — explicit Sheep/Chicken cases now precede the default arm. | — | — |

### Human Verification Required

None additional. (The 34-04 live-bot pass — /dbg each mob, milk cow, shear sheep + watch regrow, chicken slow-fall/egg — was noted as already run + PASS per the prior verification context; the gap fix is a pure cap-accounting correction with no new user-facing behavior to re-test by hand.)

### Gaps Summary

No gaps. All six observable truths are verified.

The single prior gap — ROADMAP Success Criterion #1's requirement that `categoryOf` map cow, sheep,
AND chicken to CREATURE — is CLOSED by commit ad61a3de. `categoryOf` (server/mob_category.go:84-100)
now has explicit `case entity.Sheep.ID: return categoryCreature` and `case entity.Chicken.ID: return
categoryCreature` arms, each jar-cited (`data/entity <Mob>.Type == "creature"`), mirroring the
existing pig/cow cases. The TestCategoryOfAllPassivesAreCreature regression asserts all four passives
map to categoryCreature and PASSES. The CREATURE anti-flood spawn cap now correctly counts naturally-
and /dbg-spawned sheep and chicken via countByCategoryAcrossRegions[categoryCreature]. The fix is
additive (two new switch cases ahead of the default arm); the pig oracle stays byte-identical
(TestPluginPigEqualsGoNativePig PASS) and spawn-cap accounting is green (TestSpawnCapAccounting PASS).
Build is clean under CGO_ENABLED=0. Phase goal achieved.

---

_Verified: 2026-06-30 (re-verification after gap closure)_
_Verifier: Claude (gsd-verifier)_
