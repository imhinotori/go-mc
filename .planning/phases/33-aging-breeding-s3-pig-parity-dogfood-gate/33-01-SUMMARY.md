---
phase: 33-aging-breeding-s3-pig-parity-dogfood-gate
plan: 01
subsystem: mob-ai
tags: [aging, ageable-mob, metadata, hitbox, pig-parity, synched-entity-data, boolean-serializer]

# Dependency graph
requires:
  - phase: 29-damage-keystone
    provides: "tickMobIFrames + the OUTSIDE-serverAiStep per-mob loop + broadcastToTrackers fan-out"
  - phase: 06-entity-tracker
    provides: "entityDataEntry framing + encodeSetEntityData/encodeSetEntityDataByID + Entity.metadata splice seam"
provides:
  - "breedAge int (AgeableMob.age) on Entity — the signed-int age machine (baby<0 / cooldown>0 / adult==0)"
  - "isBaby() == breedAge<0 + refreshDimensions (baby half-scale hitbox, adult x babyDimensionScale 0.5)"
  - "tickMobAging — pure-int per-tick aging in the OUTSIDE-serverAiStep loop + onGrewUp (-1->0 transition)"
  - "DATA_BABY_ID BOOLEAN metadata (index 16, boolSerializerID 8, javap-confirmed) + babyDataEntry"
  - "spawn-time baby carry (metadata splice + AABB shrink) + broadcastBabyFlag (the cross-0 fan-out)"
affects: [33-02-breeding, 33-03-breed, 33-04-follow-parent, 34-new-passive-mobs]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "AgeableMob aging ported as a pure-int machine OUTSIDE serverAiStep (the oracle-preserving optimization)"
    - "baby half-scale hitbox via captured adultWidth/adultHeight + a derived babyDimensionScale (not a hardcoded literal)"
    - "DATA_BABY_ID wire metadata mirrors the airDataEntry INT pattern with the BOOL codec (pk.Boolean)"

key-files:
  created:
    - server/aging_mob_test.go
  modified:
    - server/entity.go
    - server/combat_mob.go
    - server/tick_phases.go
    - server/entity_encode.go
    - server/plugin_mob_decl.go

key-decisions:
  - "breedAge is a NEW field (NOT the existing ItemEntity/XP-orb `age int`) — the AgeableMob.age semantics are distinct"
  - "aging runs in the tickMobIFrames per-mob loop (OUTSIDE serverAiStep), not in aiStep — pure-int, no draw, preserves the pig oracle RNG stream"
  - "babyDimensionScale = 0.5 derived (adult x 0.5), so Phase-34 mobs reuse the scale against their own dims, not the pig's 0.45 literal"
  - "boolSerializerID = 8, dataBabyIndex = 16 — both javap-confirmed (EntityDataSerializers registration order + AgeableMob defineId hierarchy)"
  - "spawn-time baby carry splices the babyDataEntry onto metadata only for a baby; an adult (the oracle pig) is skipped so its wire stays byte-identical"

patterns-established:
  - "Aging tick: the tickMobIFrames twin — a pure-int per-mob loop step that cannot perturb the oracle stream"
  - "refreshDimensions: the dimension toggle seam Plan C's breed() child-spawn reuses (set negative breedAge -> refresh to the small box)"

requirements-completed: [MOB-SUB-08]

# Metrics
duration: 22min
completed: 2026-06-30
---

# Phase 33 Plan 01: Aging + AgeableMob + Baby Half-Scale Hitbox + DATA_BABY_ID Summary

**A signed-int AgeableMob `breedAge` machine on Entity (baby ticks up to 0, cooldown ticks down to 0), an OUTSIDE-serverAiStep pure-int `tickMobAging` (the oracle-preserving twin of tickMobIFrames), the baby HALF-SCALE hitbox (refreshDimensions, adult 0.9 -> baby 0.45 via a derived 0.5 scale), and the DATA_BABY_ID BOOLEAN wire metadata (index 16, serializer 8) with its spawn carry + cross-0 grow-up broadcast — all jar-faithful, with the pig oracle byte-identical.**

## Performance

- **Duration:** ~22 min
- **Started:** 2026-06-30
- **Completed:** 2026-06-30
- **Tasks:** 3
- **Files modified:** 5 (1 created, 4 modified)

## Accomplishments
- `breedAge int` (AgeableMob.age) on Entity, distinct from the ItemEntity/XP-orb `age` — the signed-int age machine with the exact `<0 baby / >0 cooldown / ==0 adult` semantics from `AgeableMob.getAge/setAge/aiStep`.
- The baby HALF-SCALE hitbox (the plan-check BLOCKER fix): `refreshDimensions` (port of `Pig.getDefaultDimensions` -> `BABY_DIMENSIONS`), driving the live AABB to 0.45 for a baby pig (adult 0.9 x `babyDimensionScale` 0.5) and restoring 0.9 on grow-up — the load-bearing input to the breed/follow distSqr checks.
- `tickMobAging` (pure-int AgeableMob aiStep aging) wired into the per-mob loop OUTSIDE serverAiStep, with `onGrewUp` reproducing the `setAge` 0-crossing side effect (restore adult AABB + broadcast DATA_BABY_ID=false).
- DATA_BABY_ID BOOLEAN metadata (javap-confirmed index 16, serializer id 8) + `babyDataEntry`, the spawn-time baby carry, and `broadcastBabyFlag` (the cross-0 tracker fan-out).
- The pig oracle (`TestPluginPigEqualsGoNativePig`) stays byte-identical; Docker -race clean; the .star copies untouched.

## Task Commits

Each task was committed atomically:

1. **Task 1: breedAge + isBaby + baby half-scale hitbox + tickMobAging** - `02255bd9` (feat) — also carried `broadcastBabyFlag` (combat_mob.go compiles Task 1+2 together)
2. **Task 2: DATA_BABY_ID BOOLEAN metadata + spawn carry + cross-0 broadcast** - `ff7f9fe2` (feat)
3. **Task 3: aging + half-scale-AABB + cross-0 tests + oracle gate** - `726ababb` (test)

_Note: Task 1's commit carried `broadcastBabyFlag` (the Task-2-owned helper) because it lives in combat_mob.go alongside `onGrewUp`; the two compile as a unit and were split file-wise (combat_mob.go in Task 1, entity_encode.go + plugin_mob_decl.go in Task 2). All five aging/hitbox tests + the oracle gate are green._

## Files Created/Modified
- `server/entity.go` - `breedAge int`, `adultWidth/adultHeight` (captured at spawn in NewEntity), `babyDimensionScale = 0.5`, `isBaby()`, `refreshDimensions()`.
- `server/combat_mob.go` - `tickMobAging` (pure-int aging + the -1->0 onGrewUp call), `onGrewUp` (refreshDimensions + broadcastBabyFlag), `broadcastBabyFlag` (the cross-0 fan-out).
- `server/tick_phases.go` - `t.tickMobAging(e)` wired into the per-mob loop beside `tickMobIFrames` (OUTSIDE serverAiStep).
- `server/entity_encode.go` - `dataBabyIndex 16` + `boolSerializerID 8` (javap-cited) + `babyDataEntry` (BOOL codec via pk.Boolean).
- `server/plugin_mob_decl.go` - spawn-time DATA_BABY_ID carry (baby -> metadata splice + AABB shrink; adult skipped) + the `bytes` import.
- `server/aging_mob_test.go` - 5 tests: baby->adult, cooldown decay, adult no-op, half-scale AABB, grow-up restore.

## Decisions Made
- **breedAge is a NEW field**, not the existing `age int` (ItemEntity/XP-orb ticks-since-spawn). The AgeableMob.age semantics (signed, ticks toward 0) are unrelated to the item despawn counter.
- **Aging runs OUTSIDE serverAiStep** (in the tickMobIFrames per-mob loop), where vanilla runs it in `aiStep`. This is a cited oracle-preserving optimization: aging is pure-int with no RNG draw, so its observable behavior is identical to the in-aiStep position, but moving it off the goal-callback path keeps it off the per-mob RNG stream the pig oracle pins.
- **babyDimensionScale = 0.5 is derived** (adult x 0.5), not the pig's 0.45 literal, so Phase-34 mobs reuse the scale against their own adult dims (faithful to `BABY_DIMENSIONS = getDimensions().scale(0.5f)`).
- **boolSerializerID = 8, dataBabyIndex = 16** — both javap-confirmed this session: the EntityDataSerializers `registerSerializer` order (BYTE0 INT1 LONG2 FLOAT3 STRING4 COMPONENT5 OPTIONAL_COMPONENT6 ITEM_STACK7 **BOOLEAN8**) and the AgeableMob defineId hierarchy (Entity 0-7, LivingEntity 8-14, Mob 15, AgeableMob **16=DATA_BABY_ID**, 17=AGE_LOCKED).
- **Spawn-time baby carry is baby-only**: an adult (breedAge==0 — the oracle pig spawns here) is skipped entirely (no metadata entry, full dims), so its wire stays byte-identical. No baby is spawned by any live path yet (Plan C adds breed()), so the branch is currently inert for live spawns but correct the moment a negative age is set.

## Deviations from Plan

None - plan executed exactly as written. No bugs, missing functionality, or blocking issues were encountered; all javap confirmations matched the JARNOTES derivations (BOOLEAN id 8, DATA_BABY_ID index 16, BABY_START_AGE -24000, pig adult dims 0.9x0.9, `pk.Boolean` present).

**Cite-deferred (documented, not baked):** baby eye height (Pig.BABY_DIMENSIONS.withEyeHeight(0.40625f)) — our Entity has no eye-height field; not load-bearing for the goal distSqr checks (those read the AABB). Noted in `refreshDimensions` for 33-deviations.md (Plan D rolls it in).

## Issues Encountered
None. The two CRLF warnings on commit are the repo's standard line-ending normalization (not errors).

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The aging foundation is complete: Plan 33-02 (inLove / setInLove + heart particles) feeds it; Plan 33-03 (breed) sets `child.breedAge = BABY_START_AGE` and reuses `refreshDimensions` + `broadcastBabyFlag`; Plan 33-04 (FollowParentGoal) reads `isBaby()` + the half-scale AABB.
- The oracle gate (`TestPluginPigEqualsGoNativePig`) is GREEN with aging live — the byte-identical contract holds into the breeding plans.
- No blockers.

## Self-Check: PASSED

All 6 modified/created source files exist on disk; all 3 task commits (`02255bd9`, `ff7f9fe2`, `726ababb`) are present in the git log. CGO=0 build/vet clean, all aging + half-scale-AABB + cross-0 tests green, the pig oracle byte-identical, Docker -race clean, .star copies unchanged.

---
*Phase: 33-aging-breeding-s3-pig-parity-dogfood-gate*
*Completed: 2026-06-30*
