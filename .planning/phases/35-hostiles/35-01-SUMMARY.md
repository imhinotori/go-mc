---
phase: 35-hostiles
plan: 01
subsystem: ai-targeting
tags: [targetSelector, combat-goals, melee, hostiles, mob-ai, rng-lockstep]

# Dependency graph
requires:
  - phase: 24-mob-ai-goal-selector
    provides: "the goalSelector type + flag-locking arbitration + the Goal/baseGoal contract + the flagTarget bit + the per-entity entityRandom (Mob.getRandom())"
  - phase: 31-panic-goal
    provides: "the lastDamageSource/hasLastDamage keystone + the flag2 store-point in applyDamageEntity (the bookkeeping site this plan extends)"
  - phase: 29-mob-combat
    provides: "the player hurt path (applyDamage), the mob damage path (applyDamageEntity), damageSource + the damage-type id table, broadcastToTrackers"
  - phase: 34-mob-as-plugin
    provides: "buildAIFromDecl + the starlarkGoal adapter + seedAttributes/attrAlias + the declared-mob spawn path"
provides:
  - "mobAI.targetSelector — a second independent goalSelector instance ticked in jar order before goalSelector"
  - "mobAI.attackTargetID + getTarget()/setTarget() — the Mob.getTarget()/setTarget() thin-id"
  - "buildAIFromDecl TARGET-flag routing (TARGET goals -> targetSelector, the rest -> goals)"
  - "entity.lastHurtByMob + lastHurtByMobTimestamp (the attacker-entity bookkeeping) recorded at the flag2 store-point"
  - "nearestAttackableTargetGoal (the FULL nextInt(10) lockstep gate) + hurtByTargetGoal (NO RNG) — the shared target goals"
  - "meleeAttackGoal (the 20-tick RNG-free cooldown + doHurtTarget through the player hurt path) — the shared melee goal"
  - "damageSourceMobAttack + broadcastMobSwing — the mob-attack source + the mob arm-swing broadcast"
  - "the follow_range/attack_damage/armor attrAlias entries so a declared hostile seeds them"
affects: [35-03, 35-04, 35-05, 36-wolf, hostile-plugin-decls]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "a SECOND goalSelector instance (targetSelector) on the same mobAI, independent flag-lock map, ticked in jar order BEFORE the action goalSelector with the matching explicit tickRunningGoals"
    - "the full-rate-tick RNG compensation: NearestAttackableTargetGoal uses the FULL DEFAULT_RANDOM_INTERVAL (nextInt(10)) NOT the jar's reducedTickDelay(10)=5, because the Go driver ticks serverAiStep every tick (no every-other-tick TARGET decimation) — the same identity rule as adjustedTickDelay"
    - "the targetSelector goals SET mobAI.attackTargetID (Mob.setTarget); the melee goal canUse-gates on it != 0 (Mob.getTarget) — the goals never move the mob, they set a target/want"
    - "a mob-attacks-PLAYER hit routes through the PLAYER hurt path (applyDamage), not applyDamageEntity (mob-victim), with a host-set mob_attack source"

key-files:
  created:
    - server/ai_goals_target.go
    - server/ai_goals_attack.go
    - server/ai_goals_target_test.go
  modified:
    - server/ai_mob.go
    - server/plugin_mob_ai.go
    - server/plugin_mob_decl.go
    - server/entity.go
    - server/combat_mob.go
    - server/damage_source.go
    - server/entity_events.go

# Decisions
decisions:
  - "NearestAttackableTargetGoal's canUse RNG gate uses nextInt(10) (the FULL DEFAULT_RANDOM_INTERVAL), NOT reducedTickDelay's 5 — the faithful Go bound under our full-rate serverAiStep (no TARGET decimation); pinned by TestNearestAttackableTargetGateUsesTen."
  - "The targetSelector is a second independent goalSelector instance on mobAI (its own lockedBy), ticked BEFORE goalSelector in jar order with the matching explicit tickRunningGoals — pure-additive, so the pig (zero TARGET goals) stays byte-identical."
  - "doHurtTarget routes through the PLAYER hurt path (applyDamage) with a host-set mob_attack damageSource (attacker = mob id, never plugin-forgeable, T-35-02)."

# Metrics
metrics:
  duration: "~1h"
  completed: 2026-06-30
  tasks: 3
  files-created: 3
  files-modified: 7
---

# Phase 35 Plan 01: targetSelector Machinery Summary

The FIRST combat-targeting subsystem (MOB-SUB-10, SC#1): a second `goalSelector` instance
(`targetSelector`) on `mobAI` ticked in jar order, the `buildAIFromDecl` TARGET-flag routing, the
`attackTargetID` + `lastHurtByMob`/timestamp bookkeeping, and the shared
NearestAttackableTargetGoal / HurtByTargetGoal / MeleeAttackGoal primitives — built once so every
hostile (zombie/skeleton/spider) and the Phase-36 wolf reuse them. Pure-additive over the passive
runtime: the pig oracle stays byte-identical.

## What landed

**Task 1 — targetSelector field + jar-order tick + TARGET routing + attackTargetID** (commit e82930a9)
- `mobAI.targetSelector goalSelector`: a fresh, independent second GoalSelector instance
  (Mob.targetSelector), ticked BEFORE `goals` with the matching explicit `tickRunningGoals` —
  the exact Mob.serverAiStep order the file header already documented. The pig's targetSelector is
  empty (zero TARGET goals), so its tick is a no-op that draws no RNG.
- `mobAI.attackTargetID int32` + `getTarget()`/`setTarget()`: the Mob.getTarget()/setTarget()
  thin-id (0 == null), beside the wantX/Y/Z nav-want fields.
- `buildAIFromDecl`: `gd.flags&flagTarget != 0` routes a declared goal into `m.targetSelector`,
  all other goals into `m.goals`.
- `attrAlias`: `follow_range` / `attack_damage` / `armor` aliases (their suppliers already exist).

**Task 2 — lastHurtByMob bookkeeping + ai_goals_target.go** (commit 22790de0)
- `entity.lastHurtByMob` + `lastHurtByMobTimestamp`: the attacker-entity bookkeeping, recorded at
  the combat_mob.go flag2 store-point (`src.attacker` + `int32(t.gametime)`) — pure field writes,
  NO RNG.
- `nearestAttackableTargetGoal`: the canUse RNG gate uses the FULL `nextInt(10)` (the lockstep
  compensation — verified against the jar ctor which does `reducedTickDelay(10)=5`); findTarget
  reuses the player scan bounded by FOLLOW_RANGE; start() sets `mobAI.attackTargetID`.
- `hurtByTargetGoal`: NO RNG; retaliates against `lastHurtByMob` when the timestamp is fresh,
  stamps its own timestamp so it fires once per hit.

**Task 3 — ai_goals_attack.go (MeleeAttackGoal) + RNG/cooldown tests** (commit a61aeff7)
- `meleeAttackGoal`: canUse is the 20-tick gameTime `lastCanUseCheck` gate (NO RNG) gated on
  `getTarget() != 0`; tick faces + SETS a nav want toward the target + checkAndPerformAttack;
  `doHurtTarget` deals `(float)ATTACK_DAMAGE` through the PLAYER hurt path (`applyDamage`) with a
  host-set `mob_attack` source; `isWithinMeleeAttackRange` ports the DEFAULT_ATTACK_REACH
  (`sqrt(2.04)-0.6`) inflated-AABB / player-hitbox overlap. Zombie delta = plain melee; Spider
  delta = the daylight gate (cite-deferred proxy).
- `damageSourceMobAttack` + `broadcastMobSwing`.
- `ai_goals_target_test.go`: the focused lockstep tests.

## Verification

- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go vet ./server/` — clean (with the out-of-scope 35-02 test file moved aside; see
  Deviations).
- `CGO_ENABLED=0 go test ./server/ -run 'TestPluginPigEqualsGoNativePig|TestNearestAttackable|TestHurtByTarget|TestMeleeAttack|TestLastHurtByMob'` — all PASS:
  - `TestNearestAttackableTargetGateUsesTen` — confirms the gate draws exactly one `nextInt(10)`
    (NOT 5) and the outcome equals `nextInt(10)==0`.
  - `TestNearestAttackableTargetAcquiresPlayer`, `TestHurtByTargetNoRNG`, `TestLastHurtByMob`,
    `TestMeleeAttackCooldown` — all green.
  - **`TestPluginPigEqualsGoNativePig` — byte-identical (PASS).**
- The full `go test ./server/` suite (future-plan file aside) passes (12.2s).

`-race` was not run: the environment has no C compiler (`gcc` absent), and the project mandate
gates on `CGO_ENABLED=0` build/vet/test. The AI goals are pure tick-owned state (TICK-05, no
goroutines), so `-race` is not the binding gate for this subsystem.

## Deviations from Plan

### Out-of-scope discovery (logged, NOT fixed)

**[Scope boundary] server/spawner_monster_test.go does not compile (a 35-02 artifact)**
- **Found during:** Task 1 `go vet ./server/`.
- **Symptom:** `undefined: runSpawnMonsterCycle`.
- **Root cause:** plan 35-02's implementation is already committed (7f00d4c2), and its
  `spawner_test.go` harness provides `newSpawnLoop`/`runSpawnCycle`/`totalEntities`/`findEntityOfType`,
  but `spawner_monster_test.go` (created by 35-02, listed in its SUMMARY's key-files) additionally
  calls `runSpawnMonsterCycle`, a helper defined NOWHERE. This is a 35-02 self-check gap in a 35-02
  (untracked) file — out of 35-01's `files_modified`.
- **Disposition:** left untouched per the SCOPE BOUNDARY. Logged to
  `.planning/phases/35-hostiles/deferred-items.md`. Because Go compiles the whole `_test` package
  before running any `-run`-filtered test, this file blocks ALL `go test ./server/` runs — so the
  35-01 verification temporarily moved it aside (an untracked working-tree file, never committed or
  modified), ran the gates, and restored it. Plan 35-02 must add the missing helper.

### Cite-deferred sub-behavior (recorded in code, never silently dropped)

- **mustSee line-of-sight** (NearestAttackableTargetGoal): no raycast/sensing subsystem in v1 — the
  LoS gate is a cited stub equal to "visible". Upgrade when sensing lands.
- **UNIVERSAL_ANGER gamerule + toIgnoreDamage filter** (HurtByTargetGoal): no gamerule subsystem
  (defaults FALSE, a no-op); the base goal's toIgnoreDamage is empty.
- **unseenMemoryTicks (300) + setAlertOthers** (HurtByTargetGoal.start): the lose-sight grace + the
  Zombie-only alert burst — cited no-ops in the shared goal.
- **MeleeAttackGoal.tick path-recalc RNG branch** (`nextFloat()<0.05` + `4+nextInt(7)`): v1's nav is
  the async setWantTarget path (no per-tick Path object), so the recalc RNG is deferred until the
  per-tick Path navigation lands. It fires only for a running melee goal with a live target, never on
  the pig.
- **ZombieAttackGoal aggressive metadata bit** + **SpiderAttackGoal daylight proxy**: the aggressive
  client-visual DATA flag is deferred; the spider daylight gate is wired to a cited proxy (returns
  "night" until 35-02's isDarkEnoughToSpawn proxy is reused — recorded in code).
- **LeapAtTargetGoal** (spider nextFloat leap): left to plan 35-05 per the plan's option.

## Known Stubs

The cite-deferred items above are structured stubs (each equals the vanilla default and reads through
a real seam later), not value-baking. No stub prevents the plan's goal (the targetSelector machinery
exists, is live, and is reusable). The hostile PLUGIN declarations (which wire these goals into the
zombie/skeleton/spider .star files) are plans 35-03/04/05 — this plan delivers the reusable Go
primitives, not the per-mob declarations.

## Self-Check: PASSED

- Created files exist: `server/ai_goals_target.go`, `server/ai_goals_attack.go`,
  `server/ai_goals_target_test.go`, `.planning/phases/35-hostiles/35-01-SUMMARY.md`.
- Commits exist: `e82930a9` (Task 1), `22790de0` (Task 2), `a61aeff7` (Task 3).
