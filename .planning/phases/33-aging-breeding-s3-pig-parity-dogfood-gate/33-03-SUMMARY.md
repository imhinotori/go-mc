---
phase: 33-aging-breeding-s3-pig-parity-dogfood-gate
plan: 03
subsystem: mob-ai
tags: [breeding, breed-goal, follow-parent, goals, pig-parity, rng-lockstep]

# Dependency graph
requires:
  - phase: 33-01
    provides: "breedAge int (AgeableMob.age) + isBaby + refreshDimensions (baby half-scale hitbox) + broadcastBabyFlag + BABY_START_AGE semantics"
  - phase: 33-02
    provides: "inLove int + isInLove/canFallInLove/setInLove + broadcastHearts (EntityEvent-18) + the FEED path that arms inLove"
  - phase: 31-01
    provides: "panicGoal (*panicGoal in the goal selector) — the running-state isPanicking reads"
  - phase: 30.1-01
    provides: "mobRandom(e) per-mob entityRandom + setWantTarget/clearWantTarget nav seam"
  - phase: 24-02
    provides: "spawnVanillaPig (the declared-mob child spawn) + awardExperienceOrbs (death_mob.go) reused by breed()"
provides:
  - "breedGoal@3 {MOVE,LOOK} speed 1.0 (getFreePartner same-region scan + courting + breed())"
  - "followParentGoal@5 EMPTY flags speed 1.1 (nearest-adult follow, NO RNG)"
  - "adjustedTickDelay(n)=n identity helper (Goal.adjustedTickDelay @20 TPS — distinct from the reduced/halved helper)"
  - "Entity.canMate (Animal.canMate) + Entity.isAlive (LivingEntity.isAlive) + entityDistSqr"
  - "TickLoop.isPanicking (the FAITHFUL running-PanicGoal read) + TickLoop.breed (spawnChildFromBreeding port)"
  - "entityRandom.nextBoolean (RandomSource.nextBoolean) — the breed variant draw"
  - "newPigAI now registers all 9 goals {0,1,3,4,4,5,6,7,8}"
affects: [33-04-star-lockstep, 33-05-dogfood-gate, 34-new-passive-mobs]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Both breed-path RNG draws (variant nextBoolean() THEN XP 1+nextInt(7)) drawn from the INITIATOR's per-mob RNG in the jar's exact ORDER — the lockstep contract 33-04 mirrors onto both .star pigs"
    - "the same-class partner/parent scan uses the IN-REGION near() (the documented v5 same-region cut)"
    - "isPanicking reads the running *panicGoal wrappedGoal — the faithful Mob.isPanicking, NOT a hurtTime proxy"
    - "the two new goals are canUse-gated (BreedGoal isInLove, FollowParentGoal isBaby) so they are dormant + draw nothing on the un-fed lone-adult oracle pig"

key-files:
  created:
    - server/ai_goals_breed.go
    - server/ai_goals_follow.go
    - server/breed_follow_test.go
  modified:
    - server/entity.go
    - server/ai_random.go
    - server/ai_mob.go
    - server/ai_mob_test.go

key-decisions:
  - "breed() RNG ORDER is variant nextBoolean() (Pig.getBreedOffspring) THEN XP 1+nextInt(7) (finalizeSpawnChildFromBreeding) — javap-corrected vs the plan's XP-first note; BOTH on the breeding initiator's RNG"
  - "the variant nextBoolean() draw is CONSUMED for lockstep but the PigVariant assignment is cite-deferred (Entity has no variant field yet) — the draw + its order is what the oracle pins, not the bit value"
  - "adjustedTickDelay added as a NEW identity helper (n at 20 TPS); the reduced/halved helper (ceil(n/2)) is NOT used in the new goals (would give the WRONG 30/5)"
  - "isPanicking is the running-*panicGoal read (faithful), not hurtTime>0 (which decays fast + fires for non-panic damage)"
  - "the partner/parent scan uses t.cur().entities.near (in-region) — the accepted v5 same-region breeding cut"
  - "isAlive (LivingEntity.isAlive == !isRemoved() && health>0) added to Entity, folding isRemoved() into the existing `dead` flag"

patterns-established:
  - "breed-path RNG lockstep: the variant + XP draws are confined to TickLoop.breed (mid-breeding only), so they never perturb the dormant oracle stream — 33-04 mirrors both draws + their order onto both .star pigs"

requirements-completed: [MOB-SUB-09]

# Metrics
duration: 25min
completed: 2026-06-30
---

# Phase 33 Plan 03: Go-native BreedGoal@3 + FollowParentGoal@5 + breed() Summary

**The GO-NATIVE half of the pig's last two deferred goals — BreedGoal@3 (speed 1.0, {MOVE,LOOK}: getFreePartner same-region scan, isInLove-gated canUse, courting tick, breed() = spawnChildFromBreeding child-spawn at breedAge=-24000 half-scale + both parents to breedAge=6000 + inLove reset + hearts + the two breed-path RNG draws in the jar's exact order) and FollowParentGoal@5 (speed 1.1, EMPTY flags, isBaby-gated, nearest-adult follow re-pathing every 10 ticks, NO RNG) — plus the adjustedTickDelay identity helper, the faithful isPanicking (running-PanicGoal) read, canMate, isAlive, entityRandom.nextBoolean, and the 9-goal newPigAI registration. The pig oracle stays byte-identical (the goals are dormant on the un-fed lone adult); Docker -race clean.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-06-30
- **Completed:** 2026-06-30
- **Tasks:** 2
- **Files changed:** 7 (3 created, 4 modified), 835 insertions

## Accomplishments
- `adjustedTickDelay(n) int { return n }` — the Goal.adjustedTickDelay identity at 20 TPS (breed threshold 60, follow re-path 10). The reduced/halved helper (ceil(n/2)=30/5) is NOT used in either new goal.
- `Entity.canMate` (Animal.canMate: distinct same-class both-in-love), `Entity.isAlive` (LivingEntity.isAlive: `!dead && health>0`), `entityDistSqr` (Entity.distanceToSqr), and `entityRandom.nextBoolean` (RandomSource.nextBoolean).
- `TickLoop.isPanicking` — the FAITHFUL read: scans the mob's `[]*wrappedGoal` for the `*panicGoal` and returns its `running` bit (Mob.isPanicking), NOT a hurtTime proxy.
- `breedGoal@3` ({MOVE,LOOK}, speed 1.0): `getFreePartner` (nearest same-class in-love non-panicking within range 8.0 via the in-region `near()`), `canUse` isInLove-gated, `canContinueToUse` (partner alive && isInLove && loveTime<60 && !isPanicking), `tick` (lookAt + setWantTarget + ++loveTime, breed at `loveTime>=adjustedTickDelay(60) && distSqr<9.0`), `stop` (partner=nil, loveTime=0).
- `TickLoop.breed` — the Animal.spawnChildFromBreeding port: spawn the child via `spawnVanillaPig` then `child.breedAge=-24000` + `refreshDimensions()` (baby half-scale) + `broadcastBabyFlag` (DATA_BABY_ID=true); both parents `breedAge=6000`; both `inLove=0`; `broadcastHearts`; the two RNG draws in the **jar's exact order** — variant `nextBoolean()` (DRAW 1) then XP `1+nextInt(7)` (DRAW 2), both on the initiator's RNG.
- `followParentGoal@5` (EMPTY flags, speed 1.1, NO RNG): `canUse` isBaby-gated + nearest-adult same-class scan in inflate(8,4,8) (reject <3 blocks), `canContinueToUse` (3..16 blocks, stop on grow-up/parent-death), `tick` (re-path every `adjustedTickDelay(10)` — pure int).
- `newPigAI` registers all 9 goals {0,1,3,4,4,5,6,7,8}; `TestPigGoalSetRegistered` 7→9 with the breedGoal@3 {MOVE,LOOK} + followParentGoal@5 EMPTY-flag assertions.
- 8 new unit tests (adjustedTickDelay, canMate, isPanicking, getFreePartner, breedGoalCanUse, breed, followParent, followParentContinueBand) — all GREEN.

## Task Commits

1. **Task 1: adjustedTickDelay + canMate + isPanicking + the two goal files (breed/follow + breed())** — `a8f31fa5` (feat)
2. **Task 2: register BreedGoal@3 + FollowParentGoal@5 in newPigAI + the Go-native goal-count test** — `58d56d49` (feat)

## Files Created/Modified
- `server/ai_goals_breed.go` (created) — `adjustedTickDelay`, `TickLoop.isPanicking`, `breedGoal` (getFreePartner + canUse/continue/tick/stop), `TickLoop.breed` (the spawnChildFromBreeding port + both RNG draws), `entityDistSqr`, `babyStartAge`/`breedingCooldownAge`/`breedRange`/`breedLoveThreshold`/`breedDistanceSqr` constants.
- `server/ai_goals_follow.go` (created) — `followParentGoal` (canUse/continue/start/tick/stop), `withinFollowScanBox`, the follow range/band/recalc constants.
- `server/breed_follow_test.go` (created) — 8 unit tests.
- `server/entity.go` — `canMate` (Animal.canMate) + `isAlive` (LivingEntity.isAlive).
- `server/ai_random.go` — `nextBoolean` (RandomSource.nextBoolean) for the variant draw.
- `server/ai_mob.go` — `newPigAI` registers `newBreedGoal(1.0)`@3 + `newFollowParentGoal(1.1)`@5; the DEFERRED doc flipped to PORTED (9 goals); the lockstep note added.
- `server/ai_mob_test.go` — `TestPigGoalSetRegistered` 7→9 + the @3/@5 type+flag assertions.

## Decisions Made
- **breed() RNG draw ORDER is variant THEN XP** — javap of the call chain (`BreedGoal.breed → Animal.spawnChildFromBreeding → Pig.getBreedOffspring → setBaby → finalizeSpawnChildFromBreeding`) shows the variant `nextBoolean()` draws FIRST (in `getBreedOffspring`, picking which parent's PigVariant the baby inherits) and the XP `1+nextInt(7)` draws SECOND (in `finalizeSpawnChildFromBreeding`). The plan's task note listed XP-then-variant; the jar order (variant-then-XP) is the faithful one and is what was ported. **Both draws come from the breeding INITIATOR's RNG** (`this`/`animal` = `e`). 33-04 MUST mirror both draws in this exact order onto the .star pigs.
- **The variant draw is consumed but the assignment is cite-deferred** — `Entity` has no `variant` field yet (no PigVariant subsystem). The `nextBoolean()` draw is CONSUMED from the initiator's RNG (so the draw + its order stay lockstep), and the selected parent is computed; the actual `child.variant = (…).variant` assignment lands when a variant field exists. The DRAW (not the bit value) is the observable oracle contract.
- **adjustedTickDelay is a NEW identity helper**, distinct from the reduced/halved tick-delay helper (`ceil(n/2)`). The breed/follow goals use only `adjustedTickDelay` → the full 60/10, never the halved 30/5.
- **isPanicking reads the running PanicGoal** (the faithful `Mob.isPanicking`), not `hurtTime>0`. `TestIsPanicking` explicitly asserts hurtTime does NOT drive it.
- **The partner/parent scan uses the in-region `near()`** — the accepted v5 same-region breeding cut (cross-region partner/parent deferred, documented).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] breed() RNG draw order corrected to variant-then-XP (the jar order)**
- **Found during:** Task 1 (the breed() port — the plan instructed "javap-verify XP-vs-variant order FIRST").
- **Issue:** The plan's task action listed the breed draws as XP `nextInt(7)` then variant `nextBoolean()`. javap of `Animal.spawnChildFromBreeding` → `Pig.getBreedOffspring` → `finalizeSpawnChildFromBreeding` shows the OPPOSITE: the variant `nextBoolean()` (in getBreedOffspring) fires BEFORE the XP `1+nextInt(7)` (in finalizeSpawnChildFromBreeding), and there is NO variant draw inside finalizeSpawnChildFromBreeding (its only RNG is the XP orb). Both draws are on the initiator's (`animal`/`this` = `e`) RNG.
- **Fix:** `breed()` draws variant `nextBoolean()` first, then XP `1+nextInt(7)`, both via `mobRandom(e)`. Cited verbatim from the bytecode. This is the load-bearing lockstep order 33-04 mirrors.
- **Files modified:** server/ai_goals_breed.go
- **Verification:** `TestBreed` exercises the XP orb (value 1..7) + the baby spawn; the order is documented + cited. The jar bytecode offsets were captured this session.
- **Committed in:** `a8f31fa5`

**Total deviations:** 1 auto-fixed (Rule 1 — a jar-fidelity correction the plan explicitly asked to verify). No scope creep — all plan deliverables shipped.

## Oracle State (the C1/C2 transient note — EXPLICIT)

**The oracle `TestPluginPigEqualsGoNativePig` is GREEN, NOT red.** The plan anticipated a possible transient RED (Go pig 9 goals vs plugin pig 7 until 33-04). It did not fire, by design:
- `TestPluginPigEqualsGoNativePig` compares **observable behavior** (wantTarget, yaw, headYaw, position) over 500 ticks on a **lone un-fed ADULT** pig with a player nearby — it does NOT compare goal counts.
- On that pig: `breedGoal.canUse` → `!isInLove()` → false (inLove=0); `followParentGoal.canUse` → `breedAge >= 0` → false (adult). **Both goals are dormant** — they never run, never draw RNG, never set a want. So the Go pig's observable stream is byte-identical to the 7-goal plugin pig.
- `plugin_pig_test.go:55` still asserts the **plugin** pig has exactly **7** goals — that stays correct (the `.star` is untouched). 33-04 mirrors the two goals onto both `.star` pigs (plugin → 9) and bumps that assertion; 33-05 closes the full 9v9 gate.

**Exact divergence for 33-04:** the Go-native pig has 9 goals {0,1,3,4,4,5,6,7,8}; the plugin pig has 7 {0,1,4,4,6,7,8}. 33-04 must add to BOTH `.star` pigs: BreedGoal@3 (speed 1.0, flags MOVE|LOOK, breed() drawing **variant nextBoolean() THEN XP 1+nextInt(7)** on the initiator's RNG) and FollowParentGoal@5 (speed 1.1, EMPTY flags, NO RNG), and bump the plugin goal-count assertions 7→9.

## Final Gate Results
- `CGO_ENABLED=0 go build ./...` — exit 0.
- `CGO_ENABLED=0 go vet ./server/` — clean.
- `CGO_ENABLED=0 go test ./server/ -run 'TestBreed|TestFollowParent|TestCanMate|TestGetFreePartner|TestIsPanicking|TestAdjustedTickDelay'` — all GREEN.
- `TestPigGoalSetRegistered` (9 goals) — GREEN.
- Full `go test ./server/` suite — GREEN (7.4s).
- Docker `-race ./server/` — clean, NO DATA RACE (13.4s); the oracle + goal-count tests GREEN (the C1/C2 transient did not fire).

## Known Stubs
- **Pig variant assignment (cite-deferred, NOT baked away):** `breed()` consumes the variant `nextBoolean()` draw (for RNG lockstep) but does not assign `child.variant` — `Entity` has no `variant` field (no PigVariant subsystem yet). The draw + its order are the observable contract; the assignment slots in at the cited seam when a variant field lands. Documented in the breed() doc comment and here. Does NOT block the plan goal (a bred pig spawns + renders; variant is cosmetic and not yet a wire field).

## Issues Encountered
None. The CRLF warnings on commit are the repo's standard line-ending normalization (not errors).

## Next Phase Readiness
- The Go-native breeding/following is complete. 33-04 mirrors BreedGoal@3 + FollowParentGoal@5 (with the variant-then-XP draw order) onto BOTH `.star` pigs and bumps the plugin goal-count to 9; 33-05 closes the full 9v9 dogfood gate.
- No blockers. The oracle is green throughout (the goals are dormant on the un-fed adult).

## Self-Check: PASSED

All 3 created + 4 modified source files exist on disk; both task commits (`a8f31fa5`, `58d56d49`) are present in the git log. CGO=0 build/vet clean, all breed/follow/aging tests + the 9-goal goal-count test green, the pig oracle byte-identical, Docker -race clean.

---
*Phase: 33-aging-breeding-s3-pig-parity-dogfood-gate*
*Completed: 2026-06-30*
