---
phase: 35-hostiles
verified: 2026-06-30T00:00:00Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: none
  note: initial verification
---

# Phase 35: Hostiles + Spawn Rules Verification Report

**Phase Goal:** A survival night exists — zombie/skeleton/spider hunt and attack the player, gated by a faithful per-category cap and a day/night spawn rule. Introduces the targetSelector subsystem. The pig oracle stays byte-identical.
**Verified:** 2026-06-30
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria + 35-01b must-haves)

| #   | Truth | Status | Evidence |
| --- | ----- | ------ | -------- |
| SC1 | `mobAI` gains a second `targetSelector goalSelector` ticked in jar order; `buildAIFromDecl` routes TARGET-flag goals into it; `attackTargetID` thin-id; shared Melee/NearestAttackableTarget/HurtByTarget built once | VERIFIED | `server/ai_mob.go:42` `targetSelector goalSelector`; tick order `targetSelector.tick` (205) BEFORE `goals.tick` (206) then both `tickRunningGoals`; `attackTargetID int32` (65) + `getTarget`/`setTarget` (153,159); `lastHurtByMob`/`lastHurtByMobTimestamp` bookkeeping in `combat_mob.go:124-125` read by `ai_goals_target.go` |
| SC2 | Zombie/skeleton/spider each acquire a player target, path to it, deal melee damage through keystone; all three MONSTER | VERIFIED | `TestZombieBehavior`/`TestSkeletonBehavior`/`TestSpiderBehavior` each assert `ai.getTarget() == p.entityID` AND `p.health < startHealth` AND exact `dealt == ATTACK_DAMAGE` (zombie 3.0, skeleton/spider 2.0). All PASS. `categoryOf` maps all 3 → `categoryMonster` (`mob_category.go:101-118`). Live bot test `TestLiveZombieHuntsAndAttacks` confirmed PASS (20.0→0.0) |
| SC3 | Per-category cap (MONSTER 70 vs CREATURE 10, `/289` kept); MONSTER tallied in `countByCategory`; `checkDespawn` ported (cap bounds spawns) | VERIFIED | `monsterCap = maxInstancesPerChunk(70) * spawnableChunkCount / 289` (`spawner.go:100-101`); `maxInstancesPerChunk` returns 70 for categoryMonster (`mob_category.go:55-56`); `countByCategory` tallies via `categoryOf` (155-163); `TestMonsterCap`/`TestMonsterCapBlocks`/`TestSpawnCapAccounting` PASS. checkDespawn deferral recorded in-code (389) — cap is the spawn-bounding gate |
| SC4 | Day/night gate DECISION FORCED: documented gametime-darkness proxy, never silent daylight flood; deferral recorded | VERIFIED | `isDarkEnoughToSpawn` gates on `t.gametime % 24000 ∈ [13000,23000)` (`spawner.go:143-148`); MONSTER pass guarded by `if t.isDarkEnoughToSpawn()` (411); daytime hard-blocks proven by `TestMonsterPassDaytimeNoSpawn` PASS; full light-propagation/cave deferral documented (124-141) |
| SC5 | Standing: jar-verified RNG lockstep; CGO=0 + no new deps; pig oracle GREEN; (-race deferred to gate) | VERIFIED | NearestAttackableTarget gate `nextInt(10)` (`nearestTargetRandomInterval = 10`, NOT halved 5); Leap `nextInt(5)` (`leapReducedInterval = 5`, NOT 3); Spider daylight `nextInt(100)` (`spiderDaylightFleeChance = 100`); focused RNG tests PASS; `TestPluginPigEqualsGoNativePig` byte-identical PASS; CGO_ENABLED=0 build/vet clean; Docker -race confirmed by gate executor |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
| -------- | -------- | ------ | ------- |
| `server/plugin_mob_decl.go` | `nativeKind` field + `kind?` param | VERIFIED | `goalDecl.nativeKind` (73); `goalBuiltin` accepts `"kind?"` (281); loud conflict error if kind+callback (297) or kind+requires_update_every_tick (300) |
| `server/plugin_mob_ai.go` | `buildAIFromDecl` kind switch + targetSelector routing | VERIFIED | `buildNativeGoal` switch over all 6 kinds (272-289); flag-mismatch + unknown-kind panic (208,216); TARGET kinds routed to `m.targetSelector` (221), others to `m.goals` (223) |
| `server/ai_mob.go` | 2nd goalSelector + attackTargetID | VERIFIED | See SC1 |
| `server/mob_category.go` | 3 hostiles → categoryMonster; cap 70 | VERIFIED | Lines 55-56, 101-118 |
| `server/spawner.go` | monsterCap/isDarkEnoughToSpawn/MONSTER pass | VERIFIED | Lines 100-148, 411-412 (`submitSpawnScanFor(categoryMonster, ...)`) |
| `server/{zombie,skeleton,spider}_test.go` | real damage assertions | VERIFIED | All assert target+health-drop+exact-ATTACK_DAMAGE |
| 7 mob embed pairs | byte-identical | VERIFIED | diff empty for pig/cow/sheep/chicken/zombie/skeleton/spider |
| `server/vanilla_pig_embed.go` | boot-load all 7 | VERIFIED | `vanillaMobNames` lists all 7 (63-69); loop + loud re-assertion (91-105) |
| `server/commands_dbg.go` | /dbg zombie\|skeleton\|spider | VERIFIED | Cases at lines 35,40,45 |

### Key Link Verification

| From | To | Via | Status |
| ---- | -- | --- | ------ |
| hostile `.star` combat goals | Go-native 35-01 goals | `kind=` seam | WIRED — zombie/skeleton/spider `.star` declare `kind="melee_attack"/"hurt_by_target"/"nearest_attackable_target"/"leap_at_target"/"spider_attack"/"float"`; no hand-rolled Starlark combat RNG remains |
| kind-goal | targetSelector vs goals | `buildAIFromDecl` flag routing | WIRED (`plugin_mob_ai.go:220-224`) |
| MeleeAttackGoal | player damage | Phase-29 keystone doHurtTarget | WIRED — proven by health-drop assertion in all 3 behavior tests + live bot test |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| -------- | ------- | ------ | ------ |
| Build | `CGO_ENABLED=0 go build ./...` | exit 0 | PASS |
| Vet | `CGO_ENABLED=0 go vet ./server/` | exit 0, clean | PASS |
| Hostile damage | `go test -run 'TestZombieBehavior\|TestSkeletonBehavior\|TestSpiderBehavior'` | 3 PASS | PASS |
| Lockstep RNG | `go test -run 'TestNearestAttackableTargetGateUsesTen\|TestLeapAtTargetGateRNG\|TestSpiderAttackDaylightGate'` | 3 PASS | PASS |
| Pig oracle | `go test -run TestPluginPigEqualsGoNativePig` | PASS | PASS |
| Spawn gating | `go test -run 'Monster\|Spawn\|DarkEnough'` | all PASS (incl. daytime-no-spawn, cap-blocks) | PASS |
| Seam | `go test -run TestGoalKind` | 3 PASS (captures/rejects-callback/rejects-update) | PASS |
| Full suite | `go test ./server/ -count=1` | `ok 17.250s` | PASS |

Note: TestRegionPanicIsolated's known false-alarm recovered-panic stack did not cause a failure in this full run; package reported `ok`.

### Requirements Coverage

| Requirement | Description | Status | Evidence |
| ----------- | ----------- | ------ | -------- |
| MOB-SUB-10 | targetSelector machinery + shared combat goals | SATISFIED | SC1 |
| MOB-SUB-11 | Spawn gating: MONSTER cap + day/night gate + checkDespawn | SATISFIED | SC3, SC4 |
| MOB-HOST-01 | Zombie jar-faithful plugin | SATISFIED | TestZombieBehavior real damage; vanilla_zombie pair byte-identical |
| MOB-HOST-02 | Skeleton melee-only | SATISFIED | TestSkeletonBehavior real damage; bow deferral documented |
| MOB-HOST-03 | Spider with leap + daylight-gated aggression | SATISFIED | TestSpiderBehavior real damage; leap nextInt(5) + daylight nextInt(100) |

### Anti-Patterns Found

None blocking. The hostile combat goals route to verbatim Go-native 35-01 jar ports via `kind=`; no hand-rolled Starlark combat-RNG bodies remain. Documented deferrals (bow/RangedBowAttackGoal for skeleton, full light-propagation cave spawning, checkDespawn population cull) are cited in-code and structured to become real reads — not silent value-bakes.

### Human Verification Required

None. The core "hunt and attack" goal is machine-verifiable via the 3 behavior tests (target acquisition + real ATTACK_DAMAGE through the keystone) and was additionally confirmed by the live bot test (`TestLiveZombieHuntsAndAttacks`, 20.0→0.0) on a running server.

### Gaps Summary

No gaps. All 5 ROADMAP success criteria and all 5 requirements (MOB-SUB-10/11, MOB-HOST-01/02/03) are delivered and verified against the codebase. The critical execution-time gap — combat goals built Go-native with no declaration seam, leaving hostiles unable to damage the player — was closed by 35-01b: the `goal(kind=...)` seam + `buildNativeGoal` switch rewired all 3 hostiles to deal REAL damage, and the behavior tests now assert player health-drop (not merely boot-load). The pig oracle stays byte-identical (the kind-branch is never taken for the pig). Build/vet/full-suite all clean under CGO_ENABLED=0.

---

_Verified: 2026-06-30_
_Verifier: Claude (gsd-verifier)_
