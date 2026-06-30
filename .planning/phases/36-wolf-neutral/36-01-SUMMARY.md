---
phase: 36-wolf-neutral
plan: 01
subsystem: mob-ai
tags: [wolf, tamable, neutral-mob, anger, sit, follow-owner, target-goal, data-flags, go]

# Dependency graph
requires:
  - phase: 35-hostiles
    provides: "targetSelector + NearestAttackableTargetGoal/HurtByTargetGoal/MeleeAttackGoal/LeapAtTargetGoal ports + the buildNativeGoal kind seam + the entity-scan broad-phase (entities.near)"
  - phase: 33-passive-breed
    provides: "AgeableMob/Animal state pattern (breedAge/inLove) + babyDataEntry + findFreePartner nearest-wins scan"
  - phase: 34-passive-extras
    provides: "woolDataEntry (the DATA_WOOL byte) + the dataWoolIndex=18 derivation + the sheep setSheared broadcast pattern"
provides:
  - "The wolf base_type (entity.Wolf, ID 149) -> categoryCreature + wolfSupplier (Animal + spd 0.30000001192092896 + MH 8.0 + ATK 4.0)"
  - "The TamableAnimal/NeutralMob STATE: tame/orderedToSit/inSittingPose/ownerUUID/angerEndTime/angerTarget (+ owner-side lastHurtMob on both *Entity and *tickPlayer)"
  - "The DATA_FLAGS (index 18) metadata: wolfFlagsByte + wolfFlagsDataEntry + the wolf-gated spawn splice + the setWolfInSittingPose broadcast (owner-UUID index 19 wire DEFERRED)"
  - "The PARAMETERIZED nearestAttackableTargetGoal: targetClass (PLAYER|SKELETON) + an optional angerGate; the bare nearest_attackable_target is byte-identical Phase-35"
  - "Four NEW goal classes: SitWhenOrderedToGoal, FollowOwnerGoal, OwnerHurtByTargetGoal, OwnerHurtTargetGoal (all NO RNG)"
  - "Six new buildNativeGoal kinds: sit, follow_owner, owner_hurt_by, owner_hurt, angry_player_target, skeleton_target"
  - "The gametime-endpoint anger trigger at the combat store-point (angerEndTime = gameTime + 400 + nextInt(381), wolf+player-attacker-gated) + isAngryAt"
affects: [36-02 (taming interact, attack_dispatch.go), 36-03 (.star plugin + embed/load), 36-04 (the wolf gate/behavior tests)]

# Tech tracking
tech-stack:
  added: []  # no new Go dependency (go.mod unchanged)
  patterns:
    - "Additive goal parameterization: a class enum + an optional gate predicate on the EXISTING goal struct, with the zero-value path byte-identical to the prior behavior (the B1/B2 fix)"
    - "Gametime-endpoint timers: an int64 endpoint field compared to t.gametime (no per-tick decrement) — the W6-DISSOLVED anger model"
    - "Owner-side attack bookkeeping mirrored on BOTH *Entity and *tickPlayer (v1 owners are players)"

key-files:
  created:
    - server/ai_goals_sit.go
    - server/ai_goals_owner.go
  modified:
    - server/entity.go
    - server/entity_encode.go
    - server/ai_goals_target.go
    - server/plugin_mob_ai.go
    - server/plugin_mob_decl.go
    - server/mob_category.go
    - server/combat_mob.go
    - server/tick.go
    - server/attack_dispatch.go
    - server/commands_dbg.go
    - server/vanilla_pig_embed.go
    - level/attribute/defaults.go
    - level/attribute/defaults_test.go

key-decisions:
  - "DATA_FLAGS index = 18, DATA_OWNERUUID index = 19 — CONFIRMED via javap TamableAnimal.defineSynchedData (Animal adds nothing past AgeableMob 16/17, then DATA_FLAGS_ID [BYTE] then DATA_OWNERUUID_ID [OPTIONAL_LIVING_ENTITY_REFERENCE])"
  - "Anger is the gametime-ENDPOINT angerEndTime int64 (isAngry = endTime>0 && endTime-gameTime>0), NO per-tick decrement, NO ResetUniversalAngerTargetGoal — both dissolved"
  - "The B1/B2 parameterization is ADDITIVE: targetClass=PLAYER (zero) + angerGate=nil keeps the bare nearest_attackable_target byte-identical to Phase 35"
  - "owner.getLastHurtByMob (OwnerHurtByTargetGoal + SitWhenOrderedToGoal) is a CITED constant-false stub — players carry no inbound lastHurtByMob in v1; owner.getLastHurtMob (OwnerHurtTargetGoal) IS live via tickPlayer.lastHurtMob set in handleMobAttack"
  - "The FollowOwner teleport (tryToTeleportToOwner) DEFERS — only moveTo path-follow ships (shouldTryTeleportToOwner = cited constant-false)"
  - "The assets/vanilla_wolf embed + vanillaMobNames load entry DEFER to Plan C (the directory does not yet exist; embedding it now is a hard compile error) — only the vanillaWolfMobName const + /dbg wolf land here, the Phase-35 forward-declaration precedent"

patterns-established:
  - "Parameterize-don't-fork: add a class field + a gate to an existing goal rather than duplicating it, proving the zero-value path unchanged via the prior tests"
  - "Cited-stub discipline for not-yet-built read seams (player inbound bookkeeping, teleport, water malus, gamerules) — named predicates so the upgrade is a one-line read swap"

requirements-completed: [MOB-NEUT-01, MOB-NEUT-02]

# Metrics
duration: 38min
completed: 2026-06-30
---

# Phase 36 Plan 01: Wolf Neutral/Tameable Foundation Summary

**The entire SHARED Go foundation for the wolf — TamableAnimal/NeutralMob state, DATA_FLAGS metadata, the four new goal classes (sit/follow-owner/owner-hurt×2), the additively-parameterized target goal (PLAYER-gated-on-anger + SKELETON), the wolfSupplier/category/anger-trigger, and the base_type/dbg wiring — all wolf-gated so the pig oracle + the Phase-35 hostiles stay byte-identical.**

## Performance

- **Duration:** ~38 min
- **Tasks:** 2/2
- **Files modified:** 13 (2 created, 11 modified)
- **Commits:** bcdd81bc (Task 1), 9f854c57 (Task 2)

## Accomplishments

### Task 1 — State + metadata + supplier + category + wiring (commit bcdd81bc)
- **entity.go**: the TamableAnimal/NeutralMob field cluster — `tame`/`orderedToSit`/`inSittingPose`/`ownerUUID`/`angerEndTime int64`/`angerTarget` + the owner-side `lastHurtMob`/`lastHurtMobTimestamp` — each jar-cited, zero == the pig default, wolf-gated at every reader.
- **entity_encode.go**: `dataWolfFlagsIndex = 18` + `dataWolfOwnerIndex = 19` (CONFIRMED via javap — see below), `wolfFlagsByte(inSittingPose, tame)`, `wolfFlagsDataEntry`.
- **tick.go**: `tickPlayer.lastHurtMob`/`lastHurtMobTimestamp` (v1 owners are players; OwnerHurtTargetGoal reads them).
- **attack_dispatch.go**: `setLastHurtMob` on a landed player→mob hit (was a documented stub).
- **plugin_mob_decl.go**: the wolf-gated DATA_FLAGS spawn splice + `"wolf": entity.Wolf` baseTypeByName + the loud-error list.
- **defaults.go**: `wolfSupplier` (createAnimalAttributes + MOVEMENT_SPEED 0.30000001192092896 + MAX_HEALTH 8.0 + ATTACK_DAMAGE 4.0) + the suppliers map entry.
- **mob_category.go**: `entity.Wolf.ID → categoryCreature`.
- **vanilla_pig_embed.go** + **commands_dbg.go**: `vanillaWolfMobName` const + `/dbg wolf` arm + usage.

### Task 2 — Goal classes + parameterized target goal + anger trigger (commit 9f854c57)
- **ai_goals_sit.go** (new): `SitWhenOrderedToGoal` ({JUMP,MOVE}, NO RNG) + `setWolfInSittingPose` DATA_FLAGS broadcast.
- **ai_goals_owner.go** (new): `FollowOwnerGoal` ({MOVE}, lookAt + moveTo, teleport deferred), `OwnerHurtByTargetGoal`, `OwnerHurtTargetGoal` — all NO RNG, all verbatim ports.
- **ai_goals_target.go**: the B1/B2 parameterization — `nearestTargetClass` enum, `targetClass` + `angerGate` fields, branch in `findTarget`/`canContinueToUse`, `nearestEntityOfTypeAt` skeleton scan, `isAngryAt`, `newAngryPlayerTargetGoal` + `newSkeletonTargetGoal`. The bare `newNearestAttackableTargetGoal` is UNCHANGED.
- **plugin_mob_ai.go**: 6 new buildNativeGoal kinds + the panic valid-kinds list.
- **combat_mob.go**: the gametime-endpoint anger trigger at the flag2 store-point.

## VERIFIED accessor indices (confirmation, not discovery)

`javap -c -p net.minecraft.world.entity.TamableAnimal` (this session):
- `defineSynchedData` calls `Animal.defineSynchedData` (which adds NO accessor past AgeableMob's 16/17), then `define(DATA_FLAGS_ID, (byte)0)` [BYTE serializer], then `define(DATA_OWNERUUID_ID, Optional.empty())` [OPTIONAL_LIVING_ENTITY_REFERENCE].
- Chain: Entity 0–7, LivingEntity 8–14, Mob 15, AgeableMob 16+17, Animal +0 → **DATA_FLAGS = 18 (BYTE), DATA_OWNERUUID = 19**. (Sheep's DATA_WOOL is also index 18 — fine, indices are per-class-hierarchy.) Matches the JARNOTES expectation exactly.

## EXEC-TIME DECOMPILES (verbatim, this session)

- **SitWhenOrderedToGoal.canUse**: `!orderedToSit && !isTame → false; isInWater → false; !onGround → false; owner==null||owner.level!=mob.level → true; distanceToSqr(owner) < 144.0 && owner.getLastHurtByMob()!=null → false; return orderedToSit`. (144.0 = 12² exact.)
- **FollowOwnerGoal**: ctor `(this, 1.0, 10.0, 2.0)` → speed 1.0, startDistance 10 (²=100), stopDistance 2 (²=4). `canUse`: owner==null→false; unableToMoveToOwner→false; distSqr<100→false; true. `canContinueToUse`: navigation.isDone→false; unableToMoveToOwner→false; **distSqr > 4** (NO `!isOrderedToSit()` term — the pre-decompile guess was wrong). `tick`: shouldTryTeleportToOwner? (deferred=false) lookAt(owner,10,maxHeadXRot); `if (--timeToRecalcPath>0) return`; timeToRecalcPath=adjustedTickDelay(10)=10; teleport / `navigation.moveTo(owner, speedModifier)`.
- **OwnerHurtByTargetGoal.canUse**: `!isTame()||isOrderedToSit()→false; owner==null→false; ownerLastHurtBy=owner.getLastHurtByMob(); ts=owner.getLastHurtByMobTimestamp(); return ts!=this.timestamp && canAttack(ownerLastHurtBy,DEFAULT) && wantsToAttack(ownerLastHurtBy,owner)`. start: setTarget(ownerLastHurtBy); timestamp=owner.getLastHurtByMobTimestamp().
- **OwnerHurtTargetGoal.canUse**: the mirror — reads `owner.getLastHurtMob()` / `getLastHurtMobTimestamp()` (the OWNER attack-side bookkeeping). start: setTarget(ownerLastHurt); timestamp=owner.getLastHurtMobTimestamp().
- **NeutralMob.isAngryAt**: `canAttack(target) && ((isValidPlayerTarget(target) && isAngryAtAllPlayers(level)) || getPersistentAngerTarget().matches(target))`. `isAngryAtAllPlayers` reads the UNIVERSAL_ANGER gamerule (defaults FALSE → cited no-op), so the live path is `canAttack && persistentAngerTarget == target`.
- **NeutralMob.isAngry()**: `endTime = getPersistentAngerEndTime(); return endTime > 0 && (endTime - level.getGameTime()) > 0` — the gametime-endpoint, NO decrement.
- **Wolf static**: `PERSISTENT_ANGER_TIME = TimeUtil.rangeOfSeconds(20, 39)` → `UniformInt.of(400, 780)`; `sample = 400 + nextInt(381)`.
- **Wolf.createAttributes**: `MOVEMENT_SPEED 0.30000001192092896d, MAX_HEALTH 8.0d, ATTACK_DAMAGE 4.0d` (the JARNOTES "0.3" was shorthand for the float-widened literal; the supplier uses the bit-exact value).

## Parameterized target-goal shape (B1/B2)

`nearestAttackableTargetGoal` gained `targetClass nearestTargetClass` (PLAYER=0 default | SKELETON) + `angerGate func(t,e,targetID) bool` (nil == no gate):
- **findTarget** switches: PLAYER → the existing `nearestPlayerIDAt` (unchanged); SKELETON → the new `nearestEntityOfTypeAt(t, e, entity.Skeleton.ID, FOLLOW_RANGE)` (the `entities.near` broad-phase + `entityDistSqr` nearest-wins, the findFreePartner shape).
- **canUse**: after the nextInt(10) gate + findTarget, `if angerGate != nil && target != 0 && !angerGate(t,e,target) → target = 0`.
- **canContinueToUse**: branches — PLAYER resolves via `playerByEntityID` (byte-identical); SKELETON resolves via `t.cur().entities.get` + FOLLOW_RANGE².
- **ctors**: `newAngryPlayerTargetGoal` (PLAYER + `isAngryAt`), `newSkeletonTargetGoal` (SKELETON + nil). The bare `newNearestAttackableTargetGoal` is byte-identical.

## The gametime-endpoint anger model

On a fresh player hit at the combat_mob.go flag2 store-point, wolf-gated (`typ == entity.Wolf.ID`) AND player-attacker-gated (`playerByEntityID(src.attacker) != nil`): `angerEndTime = t.gametime + int64(400 + mobRandom(e).nextInt(381))` + `angerTarget = src.attacker`. ONE bounded nextInt(381) on the wolf's own RNG stream; the pig (never a wolf) and a non-player attacker draw ZERO. `isAngryAt` reads it as the gate for `angry_player_target`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Test] Removed the now-ported "wolf" from the living-fallback test**
- **Found during:** Task 1 (level/attribute test run)
- **Issue:** `TestLivingFallback` used `"wolf"` as an example of an UNREGISTERED living type expected to fall back to createLivingAttributes (MH 20 / spd 0.7 / no attack). Registering `wolfSupplier` correctly made "wolf" resolve to its real supplier, failing the test.
- **Fix:** Swapped `"wolf"` → `"axolotl"` (another still-unported living creature) in the fallback example, with a cited comment. The fallback path is still pinned.
- **Files modified:** level/attribute/defaults_test.go
- **Commit:** 9f854c57

### Scope deviation (cited, not silent)

**2. [Rule 3 - Blocking] Deferred the assets/vanilla_wolf embed directive to Plan C**
- **Issue:** Task 1 acceptance named "the //go:embed line includes assets/vanilla_wolf". That directory does not exist (Plan C ships the .star + plugin.toml); adding a missing directory to `//go:embed` is a hard compile error, and appending to `vanillaMobNames` would attempt to boot-load a non-existent declaration (a loud loader failure).
- **Resolution:** Defined the `vanillaWolfMobName` const + the `/dbg wolf` arm + usage here (the Phase-35 forward-declaration precedent — the zombie/skeleton/spider consts landed in 35-02 ahead of their embed wiring). `spawnVanillaMob(vanillaWolfMobName)` returns nil gracefully until Plan C's declaration boot-loads. The embed directive + the load entry land in Plan C alongside the asset. Documented in code.

## DEFERRALS (cited, recorded — never silently dropped)

- **FollowOwner teleport** (`tryToTeleportToOwner` safe-pos scan): only `moveTo` path-follow ships; `shouldTryTeleportToOwner` is a cited constant-false.
- **owner-UUID WIRE broadcast** (DATA_OWNERUUID index 19): the server-side `ownerUUID` ref drives the goals; the client owner-glow/collar render is deferred. The const is defined for the record.
- **ResetUniversalAngerTargetGoal**: dead under the default UNIVERSAL_ANGER gamerule (FALSE) + dissolved by the gametime-endpoint model (no decrement to reset).
- **owner.getLastHurtByMob** (OwnerHurtByTargetGoal + the SitWhenOrderedToGoal 144.0 guard): players carry no inbound lastHurtByMob in v1 → cited constant-false; the owner ATTACK side (getLastHurtMob, OwnerHurtTargetGoal) IS live.
- **FollowOwner WATER pathfinding-malus** save/restore: no per-PathType malus subsystem in v1 (the wolf crosses water via FloatGoal).
- **canAttack/wantsToAttack TargetingConditions** (LoS/team/invisibility): the same cited no-ops the Phase-35 goals carry (the existence check is the active gate).
- **The race detector** (`go test -race`): requires CGO/gcc, not present on this Windows host. The gate is `CGO_ENABLED=0 go build/vet/test` (all pass). The new RNG/anger paths are tick-owned (TICK-05), drawing on the per-entity mobRandom stream, with no shared mutable state introduced.

## Known Stubs

No goal-disabling stubs that block the plan's goal. The cited constant-false reads (owner inbound bookkeeping, teleport, malus) are FAITHFUL v1 reductions of not-yet-built subsystems — each a named predicate so the upgrade is a one-line read swap, not a baked-away value. The wolf foundation compiles, routes through buildNativeGoal, and is fully wolf-gated.

## Verification

- `CGO_ENABLED=0 go build ./...` → exit 0
- `CGO_ENABLED=0 go vet ./server/... ./level/...` → clean
- `CGO_ENABLED=0 go test ./server/ -count=1` → ok (full suite)
- `CGO_ENABLED=0 go test ./level/... -count=1` → ok
- `TestPluginPigEqualsGoNativePig` → PASS (the pig oracle byte-identical)
- `TestZombieBehavior / TestSkeletonBehavior / TestSpiderBehavior / TestNearestAttackableTargetGateUsesTen / TestHurtByTarget*` → PASS (the Phase-35 hostiles UNCHANGED by the parameterization)
- `git diff go.mod` → empty (no new dependency)

## Self-Check: PASSED

- Created files exist: server/ai_goals_sit.go, server/ai_goals_owner.go (+ all modified files present).
- Commits exist: bcdd81bc (Task 1), 9f854c57 (Task 2).
- All verification gates green (build/vet/test, pig oracle byte-identical, Phase-35 hostiles unchanged, go.mod empty).
