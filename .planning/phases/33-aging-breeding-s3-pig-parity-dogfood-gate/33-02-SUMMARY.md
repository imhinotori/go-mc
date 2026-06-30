---
phase: 33-aging-breeding-s3-pig-parity-dogfood-gate
plan: 02
subsystem: mob-ai
tags: [breeding, in-love, feed, particles, entity-event, pig-parity, mob-interact]

# Dependency graph
requires:
  - phase: 33-01
    provides: "breedAge int (AgeableMob.age) + isBaby + refreshDimensions (baby half-scale hitbox) + tickMobAging (the pure-int OUTSIDE-serverAiStep aging loop) + onGrewUp/broadcastBabyFlag + DATA_BABY_ID metadata"
  - phase: 32-01
    provides: "itemInTag(id, \"pig_food\") (Pig.isFood) + the held-item read (playerHoldsTempt / heldWindowSlot)"
  - phase: 29-damage-keystone
    provides: "broadcastToTrackers fan-out + encodeSoundEntity + encodeEntityEvent (the EntityEvent wire-out) + owningRegion/regionForColumn cross-region resolve discipline"
provides:
  - "inLove int (Animal.inLove) on Entity + isInLove/canFallInLove/setInLove(->600) helpers"
  - "ageUp(amount) (AgeableMob.ageUp: age + amount*20, clamp at 0) + getSpeedUpSecondsWhenFeeding (the (int)((float)(d/20)*0.1f) baby-feed speedup)"
  - "the adult-only inLove decrement + the 3-nextGaussian heart-velocity draws folded into tickMobAging (the Animal.aiStep tail, OUTSIDE serverAiStep)"
  - "the FEED path in handleInteract (Animal.mobInteract): pig_food + adult -> setInLove + consume; pig_food + baby -> ageUp + consume; cross-region dropped (v5 same-region cut)"
  - "encodeLevelParticles — the previously-missing ClientboundLevelParticles wire-out (javap-confirmed)"
  - "broadcastHearts (the faithful dedicated-server heart path: ClientboundEntityEvent(id, 18)) + entityEventInLoveHearts byte=18"
affects: [33-03-breed, 33-04-follow-parent, 33-05-gate, 34-new-passive-mobs]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Animal.aiStep tail (the in-love decrement + heart-velocity draws) folded into the pure-int tickMobAging loop OUTSIDE serverAiStep — dormant on the un-fed oracle pig (inLove 0)"
    - "the FAITHFUL dedicated-server heart path is ClientboundEntityEvent(id, 18) (setInLove's broadcastEntityEvent), NOT ClientboundLevelParticles — the server never wires the aiStep hearts (Level.addParticle is a server no-op)"
    - "the FEED path mirrors handleMobAttack's owningRegion resolve + same-region gate; cross-region feed dropped (the accepted v5 same-region cut)"

key-files:
  created:
    - server/inlove_feed_test.go
  modified:
    - server/entity.go
    - server/combat_mob.go
    - server/ai_random.go
    - server/entity_encode.go
    - server/attack_dispatch.go

key-decisions:
  - "The in-love heart particles reach the client via ClientboundEntityEvent(id, 18) (Animal.setInLove's broadcastEntityEvent), NOT ClientboundLevelParticles — javap-corrected vs the plan/JARNOTES assumption"
  - "playEatingSound() for a PIG is the empty base Animal.playEatingSound (Pig has NO override) — feeding a pig emits NO sound; javap-corrected vs the plan's GENERIC_EAT assumption"
  - "encodeLevelParticles is still built (the genuine ServerLevel.sendParticles wire-out, javap-confirmed) — useful infra + the plan deliverable — even though the in-love hearts use EntityEvent-18"
  - "ageUp multiplies amount by 20 (seconds->ticks) and clamps at 0; getSpeedUpSecondsWhenFeeding does the int-divide-first (d/20)*0.1f truncation — both ported verbatim from the bytecode"
  - "a cross-region feed is DROPPED (the accepted v5 same-region cut), never an inline foreign-region mutation; cited region_transfer.go queueDamageIntent as the full-fidelity path"

patterns-established:
  - "In-love tail: the tickMobAging extension — pure-int decrement + the 3 nextGaussian draws (dormant on the oracle), keeping the mob RNG stream lockstep with vanilla aiStep without touching the oracle"
  - "Heart trigger: broadcastHearts == encodeEntityEvent(id, 18) to trackers — the reusable seam Plan C's breed() fires after the parents reset inLove"

requirements-completed: [MOB-SUB-09]

# Metrics
duration: 11min
completed: 2026-06-30
---

# Phase 33 Plan 02: Animal In-Love + the FEED Interact Path + the HEART Particle Encoder Summary

**The Animal in-love subsystem (inLove int + setInLove->600 + canFallInLove/isInLove), the adult-only in-love decrement (the Animal.aiStep tail folded into the pure-int tickMobAging loop, with the 3 nextGaussian heart-velocity draws kept lockstep with vanilla but dormant on the oracle), the jar-verbatim FEED path in handleInteract (pig_food + adult -> setInLove + consume; pig_food + baby -> ageUp + consume), the previously-missing encodeLevelParticles wire-out, and the FAITHFUL dedicated-server heart path via ClientboundEntityEvent(18) — all 1:1 with the 26.2 jar, with the pig oracle byte-identical and Docker -race clean.**

## Performance

- **Duration:** ~11 min
- **Started:** 2026-06-30T08:04:56Z
- **Completed:** 2026-06-30
- **Tasks:** 3
- **Files modified:** 5 (1 created, 4 modified)

## Accomplishments
- `inLove int` (Animal.inLove) on Entity + `isInLove()` (inLove>0) / `canFallInLove()` (inLove<=0) / `setInLove()` (->600), plus `ageUp(amount)` (age + amount*20, clamp at 0) and `getSpeedUpSecondsWhenFeeding` (the `(int)((float)(d/20)*0.1f)` baby-feed speedup) — all ported verbatim from the bytecode.
- The Animal.aiStep tail folded into `tickMobAging` (the OUTSIDE-serverAiStep pure-int loop): `if breedAge!=0 inLove=0; if inLove>0 {--inLove; if %10==0 emitInLoveHearts}`, where `emitInLoveHearts` draws the 3 `nextGaussian()*0.02` heart-velocity values to keep the mob RNG stream lockstep with vanilla aiStep. Dormant on the un-fed oracle pig (inLove 0) → the byte-identical gate holds.
- The FEED path in `handleInteract` (Animal.mobInteract, verbatim): decode the ServerboundInteract entityId, resolve the mob via `owningRegion` (the handleMobAttack precedent), same-region gate, then `tryFeedAnimal` — the ADULT branch first (`age==0 && canFallInLove` → consume 1 + setInLove + broadcastHearts), then the BABY branch (`canAgeUp==isBaby` → consume 1 + ageUp).
- `encodeLevelParticles` — the previously-missing `ClientboundLevelParticles` wire-out, javap-confirmed field order (overrideLimiter bool, **alwaysShow** bool [the 26.2 addition], xyz double, xyz-dist float, maxSpeed float, count **Int** [writeInt, not VarInt], particle-type **VarInt trailing** [HEART = SimpleParticleType → no options bytes]).
- `broadcastHearts` — the FAITHFUL dedicated-server heart path: `ClientboundEntityEvent(id, 18)` (the burst `setInLove`/breed broadcast; the client's `handleEntityEvent(18)` spawns the 7 hearts). The server never wires the aiStep hearts (`Level.addParticle` is a server no-op).
- The pig oracle (`TestPluginPigEqualsGoNativePig`) stays byte-identical; Docker -race clean (12.5s); both `.star` copies untouched (diff empty); no new Go deps.

## Task Commits

Each task was committed atomically:

1. **Task 1: inLove field + helpers + the adult-only in-love decrement** - `b04e0294` (feat)
2. **Task 2: encodeLevelParticles (the HEART encoder gap) + broadcastHearts** - `9ecc4245` (feat)
3. **Task 3: the FEED path in handleInteract + feed/inLove tests + the oracle gate** - `ea8d7d00` (feat)

_Note: the plan marked the tasks `tdd="true"`; they were executed as cohesive feat commits (the encoder/helper tasks compile + are exercised by the Task-3 tests, including a byte-exact `TestHeartParticleEncoder` and the oracle gate). The feed/inLove test file lands with the feed-path code it covers._

## Files Created/Modified
- `server/entity.go` — `inLove int` (Animal.inLove) + `defaultInLoveTime` (600) + `isInLove`/`canFallInLove`/`setInLove`; `ageUp(amount)` (age + amount*20, clamp at 0) + `getSpeedUpSecondsWhenFeeding` (the `(int)((float)(d/20)*0.1f)` truncation, op-order verbatim).
- `server/combat_mob.go` — the Animal.aiStep tail appended to `tickMobAging` (`breedAge!=0 → inLove=0`; `inLove>0 → --inLove; %10 → emitInLoveHearts`); `emitInLoveHearts` (the 3 nextGaussian heart-velocity draws); `broadcastHearts` (the `encodeEntityEvent(id, 18)` heart trigger); `inLoveHeartGaussianScale` (0.02).
- `server/ai_random.go` — `nextGaussian()` (RandomSource.nextGaussian analogue, `NormFloat64`) for the heart-velocity draws.
- `server/entity_encode.go` — `encodeLevelParticles` (the javap-confirmed ClientboundLevelParticles wire-out) + `entityEventInLoveHearts byte = 18`.
- `server/attack_dispatch.go` — `handleInteract` wires the FEED path (decode entityId, owningRegion resolve, same-region gate); `tryFeedAnimal` (the Animal.mobInteract feed branch: adult-love then baby-ageUp, verbatim).
- `server/inlove_feed_test.go` — 9 tests: feed-adult-sets-love, feed-baby-ages-up, tiny-baby-speedup-truncates, ageUp-clamps-at-zero, feed-non-food-no-op, feed-already-in-love-no-op, inLove-decrement (4 sub-cases), heart-particle-encoder (byte-exact), feed-through-handleInteract (full dispatch).

## Decisions Made
- **The in-love hearts reach the client via `ClientboundEntityEvent(id, 18)`, not `ClientboundLevelParticles`.** javap of `Animal.setInLove` shows `level.broadcastEntityEvent(this, (byte)18)`, and `Animal.handleEntityEvent(18)` spawns the 7 hearts client-side. On a dedicated server `Level.addParticle` is the empty base no-op (`ServerLevel` does not override it), so the `aiStep` `inLove%10` heart loop emits NOTHING on the wire — it only draws the 3 nextGaussian velocity values. This is a correction to the plan/JARNOTES assumption that hearts go out via `ClientboundLevelParticles`.
- **`playEatingSound()` for a PIG is a no-op.** `Pig` does NOT override `Animal.playEatingSound`, and the base is `return` (empty). So feeding a pig emits NO sound — a correction to the plan's `GENERIC_EAT` assumption. The call is preserved as a documented no-op so a sound-overriding animal (Phase 34) slots in.
- **`encodeLevelParticles` is still built** (the plan deliverable + genuine `ServerLevel.sendParticles` infra), javap-confirmed and byte-tested, even though the in-love hearts use EntityEvent-18. `ServerLevel.sendParticles` constructs exactly this packet, so future server-spawned particles (and any later sendParticles-based effect) reuse it.
- **`ageUp` multiplies by 20** (the seconds→ticks conversion `i += amount * 20`) and clamps at 0; `getSpeedUpSecondsWhenFeeding` does the int-divide-first `(int)((float)(d/20)*0.1f)`. The two compose without double-counting (one returns seconds, the other converts to ticks).
- **A cross-region feed is dropped** (the accepted v5 same-region cut), mirroring the documented same-region breeding cut — never an inline foreign-region mutation. Cited `region_transfer.go` queueDamageIntent as the full-fidelity barrier path a later plan can adopt.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Heart particle wire corrected from ClientboundLevelParticles to ClientboundEntityEvent(18)**
- **Found during:** Task 2 (the HEART encoder)
- **Issue:** The plan + 33-JARNOTES assumed the in-love hearts are sent as a `ClientboundLevelParticles` broadcast on the `inLove%10` cadence. javap of `Animal.aiStep`, `Animal.setInLove`, `Animal.handleEntityEvent`, and `Level.addParticle` shows the truth: on a dedicated server the aiStep heart loop calls `Level.addParticle`, which is the empty base no-op (ServerLevel does NOT override it), so NO packet is emitted from the tick tail; the client-facing hearts come from `setInLove`'s `broadcastEntityEvent(this, (byte)18)`, and the CLIENT's `handleEntityEvent(18)` spawns 7 hearts locally.
- **Fix:** Built `broadcastHearts` on the faithful path (`encodeEntityEvent(e.id, 18)` to trackers, reusing the existing `encodeEntityEvent` + `broadcastToTrackers` seams). Still built `encodeLevelParticles` (the plan deliverable + genuine sendParticles infra, javap-confirmed + byte-tested). Kept the 3 nextGaussian draws in `emitInLoveHearts` so the mob RNG stays lockstep with vanilla aiStep (dormant on the oracle).
- **Files modified:** server/combat_mob.go, server/entity_encode.go
- **Verification:** `TestHeartParticleEncoder` byte-compares the encoder; the oracle gate stays byte-identical (the heart path is dormant on the un-fed pig).
- **Committed in:** `9ecc4245` (Task 2 commit)

**2. [Rule 1 - Bug] Pig eat sound is a no-op (Pig has no playEatingSound override)**
- **Found during:** Task 3 (the FEED path)
- **Issue:** The plan + JARNOTES specified `playEatingSound = SoundEvents.GENERIC_EAT` for the eat sound on feed. javap shows `Pig` does NOT override `playEatingSound`, and `Animal.playEatingSound` is the empty base (`return`). A pig therefore plays NO eat sound on feed — emitting GENERIC_EAT would be an observable divergence from vanilla.
- **Fix:** The feed path performs the `playEatingSound()` call as a documented no-op for a pig (no sound packet), exactly as the bytecode does. Cited so a sound-overriding animal (Phase 34) attaches its sound here.
- **Files modified:** server/attack_dispatch.go
- **Verification:** The feed tests assert inLove/consume effects with no sound dependency; the full server suite + Docker -race stay green.
- **Committed in:** `ea8d7d00` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1 — jar-fidelity corrections to plan assumptions the bytecode contradicted).
**Impact on plan:** Both corrections are required for the 1:1 mandate (the observable wire/sound must match vanilla). No scope creep — the plan's deliverables (inLove subsystem, FEED path, the encoder, the heart trigger) all shipped; only the HEART transport and the pig eat sound were corrected to what the jar actually does. `encodeLevelParticles` (the named GAP) was still built and byte-tested.

## Issues Encountered
None. The CRLF warnings on commit are the repo's standard line-ending normalization (not errors).

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- The in-love + feed foundation is complete: Plan 33-03 (BreedGoal) gates on `isInLove()` and calls `setInLove`/`resetLove` + reuses `broadcastHearts` (the EntityEvent-18 burst `finalizeSpawnChildFromBreeding` also fires); the breed XP-orb + child spawn build on this. Plan 33-04 (FollowParentGoal) reads `isBaby()` (unchanged from 33-01).
- `encodeLevelParticles` is available for any future `ServerLevel.sendParticles`-style server-spawned effect.
- The oracle gate (`TestPluginPigEqualsGoNativePig`) is GREEN with the in-love subsystem live (dormant on the un-fed pig) — the byte-identical contract holds into the breeding plans. Docker -race clean. No blockers.

## Self-Check: PASSED

---
*Phase: 33-aging-breeding-s3-pig-parity-dogfood-gate*
*Completed: 2026-06-30*
</content>
</invoke>
