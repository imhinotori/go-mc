# Phase 35 — pre-decompiled jar notes (hostiles: zombie / skeleton / spider + targetSelector + spawn rules)

Captured 2026-06-30 while Phase 34 planned (zero-idle pre-decompile). All bytecode from
temp/cache/26.2-inner.jar via CFR/javap (Zulu 25). PHASE 35 is the FIRST phase to introduce the
targetSelector subsystem (combat targeting) + monster spawn rules + ranged attack — the biggest
new-subsystem lift since the goal selector itself. The passive 8-goal runtime (30.1–33) + the
mob-as-plugin registry (34) are REUSED; the NEW work is: targetSelector goals, attack goals
(melee + ranged bow), brightness/spawn gating, and the hostile attribute base.

## ⚠ 26.2 PACKAGE REORG: hostiles moved into sub-packages
- net.minecraft.world.entity.monster.zombie.Zombie          (NOT monster.Zombie)
- net.minecraft.world.entity.monster.skeleton.AbstractSkeleton / Skeleton / WitherSkeleton
- net.minecraft.world.entity.monster.spider.Spider / CaveSpider
Wire types zombie/skeleton/spider — confirm present in data/entity/entity.go at phase open.

## --- registerGoals (all CONFIRMED via CFR) ---

### Zombie (zombie.Zombie.registerGoals → calls addBehaviourGoals)
```
goalSelector:
  4 ZombieAttackTurtleEggGoal(this, 1.0, 3)
  8 LookAtPlayerGoal(Player, 8.0)
  8 RandomLookAroundGoal
  + addBehaviourGoals():
    2 SpearUseGoal(this, 1.0, 1.0, 10.0, 2.0)          <-- 26.2-NEW (spear weapon goal) — likely cite-defer if no weapon system
    3 ZombieAttackGoal(this, 1.0, false)                <-- a MeleeAttackGoal subclass (the core melee)
    6 MoveThroughVillageGoal(this, 1.0, true, 4, canBreakDoors)
    7 WaterAvoidingRandomStrollGoal(this, 1.0)
targetSelector:
  1 HurtByTargetGoal(this).setAlertOthers(ZombifiedPiglin.class)
  2 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true)
  3 NearestAttackableTargetGoal<AbstractVillager>(this, false)
  3 NearestAttackableTargetGoal<IronGolem>(this, true)
  5 NearestAttackableTargetGoal<Turtle>(this, 10, true, false, Turtle.BABY_ON_LAND_SELECTOR)
```

### Skeleton (skeleton.AbstractSkeleton.registerGoals) — Skeleton extends AbstractSkeleton, RangedAttackMob
```
goalSelector:
  2 RestrictSunGoal(this)
  3 FleeSunGoal(this, 1.0)
  3 AvoidEntityGoal<Wolf>(this, Wolf, 6.0, 1.0, 1.2)
  5 WaterAvoidingRandomStrollGoal(this, 1.0)
  6 LookAtPlayerGoal(Player, 8.0)
  6 RandomLookAroundGoal
targetSelector:
  1 HurtByTargetGoal(this)
  2 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true)
  3 NearestAttackableTargetGoal<IronGolem>(this, true)
  3 NearestAttackableTargetGoal<Turtle>(this, 10, true, false, Turtle.BABY_ON_LAND_SELECTOR)
```
RANGED: AbstractSkeleton holds `private final RangedBowAttackGoal<AbstractSkeleton> bowGoal` +
`private final MeleeAttackGoal meleeGoal`; `reassessWeaponGoal()` swaps which is active based on
held item (bow → bowGoal, else meleeGoal). `performRangedAttack(LivingEntity, float)` fires an arrow.
Implements RangedAttackMob. Decompile reassessWeaponGoal + performRangedAttack + RangedBowAttackGoal at exec.

### Spider (spider.Spider.registerGoals)
```
goalSelector:
  1 FloatGoal(this)
  2 AvoidEntityGoal<Armadillo>(this, Armadillo, 6.0, 1.0, 1.2, e -> !e.isScared())
  3 LeapAtTargetGoal(this, 0.4)
  4 SpiderAttackGoal(this)                              <-- MeleeAttackGoal subclass (won't attack in daylight)
  5 WaterAvoidingRandomStrollGoal(this, 0.8)
  6 LookAtPlayerGoal(Player, 8.0)
  6 RandomLookAroundGoal
targetSelector:
  1 HurtByTargetGoal(this)
  2 SpiderTargetGoal<Player>(this, Player)              <-- NearestAttackableTargetGoal subclass, daylight-gated
  3 SpiderTargetGoal<IronGolem>(this, IronGolem)
```

## --- Attributes (createAttributes — all CONFIRMED) ---
- Monster.createMonsterAttributes() = Mob.createMobAttributes().add(ATTACK_DAMAGE)   [base for all hostiles]
- Zombie:   Monster + FOLLOW_RANGE 35.0 + MOVEMENT_SPEED 0.23 + ATTACK_DAMAGE 3.0 + ARMOR 2.0 + SPAWN_REINFORCEMENTS_CHANCE
- Skeleton (AbstractSkeleton): Monster + MOVEMENT_SPEED 0.25   (MAX_HEALTH from Mob base = 20; ATTACK_DAMAGE from Monster base)
- Spider:   Monster + MAX_HEALTH 16.0 + MOVEMENT_SPEED 0.3

## --- targetSelector subsystem (THE NEW LIFT) ---
The targetSelector is a SECOND GoalSelector on the mob (Mob.serverAiStep ticks targetSelector.tick()
BEFORE goalSelector.tick() — already noted in ai_mob.go's comment). It sets mob.target; the
goalSelector's attack goals (MeleeAttackGoal/RangedBowAttackGoal) canUse-gate on mob.getTarget()!=null.

### HurtByTargetGoal.canUse (CONFIRMED) — retaliate against whoever last hit the mob
```java
int timestamp = mob.getLastHurtByMobTimestamp();
LivingEntity last = mob.getLastHurtByMob();
if (timestamp == this.timestamp || last == null) return false;
if (last.is(PLAYER) && UNIVERSAL_ANGER gamerule) return false;
for (Class c : toIgnoreDamage) if (c.isAssignableFrom(last.getClass())) return false;
return canAttack(last, HURT_BY_TARGETING);          // TargetingConditions
```
NO RNG. start() also sets the target + (setAlertOthers) alerts nearby same-type mobs. Needs the
lastHurtByMob / lastHurtByMobTimestamp fields on the entity (LivingEntity damage bookkeeping) — check
if Phase 31 (lastDamageSource/hasLastDamage) already laid groundwork; likely needs extension.

### NearestAttackableTargetGoal.canUse (CONFIRMED) — acquire nearest valid target
```java
if (randomInterval > 0 && mob.getRandom().nextInt(randomInterval) != 0) return false;   // RNG GATE (lockstep!)
findTarget();                                          // AABB scan w/ TargetingConditions (range, LoS if mustSee)
return target != null;
```
randomInterval = reducedTickDelay(10). ⚠ SAME adjustedTickDelay/decimation question as Phase 34's
EatBlockGoal — the Go driver ticks every tick (no every-other-tick decimation), so the faithful bound
is the FULL reducedTickDelay-source value run every tick. Re-confirm the exact randomInterval ctor value
(the (Mob,Class,boolean) ctor sets randomInterval = reducedTickDelay(10); but our identity adjustedTickDelay
means it should be 10 — VERIFY the ctor uses reducedTickDelay directly vs adjustedTickDelay at exec, this is
a lockstep-critical bound). findTarget draws nothing beyond the gate. TargetingConditions.range uses
FOLLOW_RANGE attribute.

## --- spawn rules (NEW — brightness/position gating) ---
Monster static spawn predicates (CONFIRMED present):
- isDarkEnoughToSpawn(ServerLevelAccessor, BlockPos, RandomSource)
- checkMonsterSpawnRules(EntityType, level, EntitySpawnReason, pos, RandomSource)       [dark + difficulty]
- checkAnyLightMonsterSpawnRules(...)                                                    [any light]
- checkSurfaceMonstersSpawnRules(...)
Decompile all 4 at exec. These gate natural hostile spawning on light level / difficulty / surface.
The Phase-29/async.go natural-spawn loop (passive) extends to a hostile pass with these predicates.
SpawnPlacements registration (where each mob may spawn — ground/water type + the predicate) — check
data/ extraction for SpawnPlacements or hand-port from the EntityType registration. Light level read
needs the lighting engine (check what worldgen/lighting exists).

## --- attack goals (decompile at exec) ---
- MeleeAttackGoal (base): path to target, swing when in reach (getMeleeAttackRangeSqr), attack cooldown.
  ZombieAttackGoal / SpiderAttackGoal / SkeletonMeleeGoal are subclasses.
- RangedBowAttackGoal (Skeleton): bow draw timer, strafe movement, performRangedAttack → spawn Arrow entity.
- LeapAtTargetGoal (Spider): nextFloat-gated leap impulse toward target — HAS RNG, lockstep if dogfooded.
- AvoidEntityGoal (Skeleton/Wolf, Spider/Armadillo): flee path when the avoided entity is near.
- FleeSunGoal / RestrictSunGoal (Skeleton): daylight-avoidance navigation.

## --- SCOPE NOTES for the planner (when 35 opens) ---
- The DOGFOOD pattern continues: each hostile is a declare_mob plugin + the goal callbacks. But the
  NEW goal types (MeleeAttackGoal, RangedBowAttackGoal, HurtByTargetGoal, NearestAttackableTargetGoal,
  AvoidEntityGoal, LeapAtTargetGoal, the sun goals, SpiderAttackGoal) are real Go ports — the bulk of 35.
- The targetSelector is a second goalSelector instance per mob — the ai_mob.go runtime already ticks it
  (the comment is there); wire it live + add the target field + the damage-bookkeeping (lastHurtByMob).
- 26.2-NEW SpearUseGoal (Zombie prio 2) + ZombieAttackTurtleEggGoal — likely CITE-DEFER (no spear/turtle-egg
  subsystem); document. The core melee (ZombieAttackGoal) is the must-have.
- Arrow entity (Skeleton ranged) — a new projectile entity; may be its own sub-phase or a cite-deferred stub.
- RNG lockstep: NearestAttackableTargetGoal gate (nextInt(randomInterval)), LeapAtTargetGoal (nextFloat),
  reinforcement spawns (Zombie SPAWN_REINFORCEMENTS_CHANCE) — each needs draw-order discipline if dogfooded.
- The pig oracle stays untouched/green (hostiles are separate mobs). A per-hostile behavior test + focused
  RNG tests for each new RNG goal. The combat (melee damage application) ports the Phase-31 damage path.

## ============================================================================
## EXEC-TIME DECOMPILES (verbatim, captured 2026-06-30 at phase open during 34-close)
## ============================================================================

## --- NearestAttackableTargetGoal (target.NearestAttackableTargetGoal) — ⚠ LOCKSTEP-CRITICAL ---
## DEFAULT_RANDOM_INTERVAL = 10. ctor: this.randomInterval = reducedTickDelay(randomInterval) = ceilDiv(10,2) = 5.
## setFlags(EnumSet.of(Goal.Flag.TARGET)); targetConditions = TargetingConditions.forCombat().range(followDistance).selector(sel).
## canUse():
##   if (randomInterval > 0 && mob.getRandom().nextInt(randomInterval) != 0) return false;   // RNG GATE
##   findTarget(); return target != null;
## findTarget(): target = (targetType==Player) ? level.getNearestPlayer(targetConditions, mob, x, eyeY, z)
##                                             : level.getNearestEntity(getEntitiesOfClass(type, searchArea), targetConditions, ...);
##   searchArea = mob.getBoundingBox().inflate(followDistance) (followDistance = FOLLOW_RANGE attribute).
## start(): mob.setTarget(this.target); super.start().    NO RNG beyond the canUse gate.
## ⚠ THE SAME adjustedTickDelay/decimation COMPENSATION AS PHASE 34: the ctor does reducedTickDelay(10)=5 to
## compensate for vanilla evaluating TARGET goals every-OTHER server tick (the Mob.serverAiStep (tickCount+id)%2
## decimation). The Go driver ticks serverAiStep EVERY tick (tick_phases.go:368 — no decimation), so to fire at the
## vanilla real-world rate the Go gate MUST use the FULL interval = nextInt(10), NOT the halved nextInt(5). DO NOT
## call reducedTickDelay in the Go port of this ctor (use the raw 10), EXACTLY as adjustedTickDelay stays identity.
## This is the 1:1-faithful value given our full-rate tick. Pin it with a focused RNG test (nextInt(10) gate).

## --- MeleeAttackGoal (ai.goal.MeleeAttackGoal) — the shared melee, NO RNG ---
## canUse(): time = gameTime; if (time - lastCanUseCheck < 20) return false; lastCanUseCheck = time;
##           target = mob.getTarget(); if (target==null || !target.isAlive()) return false;
##           path = navigation.createPath(target, 0); if (path != null) return true;
##           return mob.isWithinMeleeAttackRange(target);
## checkAndPerformAttack(target): if (canPerformAttack(target)) { resetAttackCooldown(); mob.swing(MAIN_HAND);
##           mob.doHurtTarget(serverLevel, target); }    // doHurtTarget = the Phase-29 mob->target damage keystone (ATTACK_DAMAGE)
## NO RNG — the attack cadence is gametime/cooldown based. tick() paths to target + faces it + checkAndPerformAttack
## when in reach (getMeleeAttackRangeSqr). ZombieAttackGoal/SpiderAttackGoal are subclasses (Spider won't attack in
## daylight; Zombie is plain melee). Decompile ZombieAttackGoal/SpiderAttackGoal deltas + isWithinMeleeAttackRange at exec.

## --- Spawn rules: isDarkEnoughToSpawn + checkMonsterSpawnRules (Monster) — confirms the CONTEXT light-gate decision ---
## isDarkEnoughToSpawn(level, pos, random):
##   if (level.getBrightness(SKY, pos) > random.nextInt(32)) return false;                 // needs SKY light engine
##   int blockLimit = dimensionType.monsterSpawnBlockLightLimit();
##   if (blockLimit < 15 && level.getBrightness(BLOCK, pos) > blockLimit) return false;    // needs BLOCK light engine
##   int b = isThundering ? getMaxLocalRawBrightness(pos,10) : getMaxLocalRawBrightness(pos);
##   return b <= dimensionType.monsterSpawnLightTest().sample(random);                     // UniformInt(0,7).sample
## checkMonsterSpawnRules(type, level, reason, pos, random):
##   return (ignoresLightRequirements(reason) || isDarkEnoughToSpawn(level,pos,random)) && checkMobSpawnRules(type,level,reason,pos,random);
## ⇒ isDarkEnoughToSpawn READS the light engine (SKY/BLOCK brightness) which DOES NOT EXIST in v1 (flat-stone
##   superflat, no light propagation). This CONFIRMS the CONTEXT's FORCED decision: ship the gametime-darkness
##   proxy — gate hostile spawns on the day/night gametime window (the night portion of the dayTime cycle the
##   server already ticks), behind a clearly-cited isDarkEnoughToSpawn stub that EQUALS the vanilla night default
##   and becomes a real light read when the lighting engine lands. The light-test RNG (nextInt(32) + the
##   monsterSpawnLightTest UniformInt sample) is spawn-attempt RNG (off the mob stream); the proxy documents its
##   deferral. checkMobSpawnRules (position/difficulty/below-sky) is the OTHER half — port the position checks.

## --- ZombieAttackGoal (ai.goal.ZombieAttackGoal extends MeleeAttackGoal) — NO RNG ---
## = MeleeAttackGoal(zombie, speed, trackTarget=false) + raiseArmTicks / setAggressive (the arm-raise client
## animation flag). start: raiseArmTicks=0. stop: setAggressive(false). tick: super.tick(); ++raiseArmTicks;
## setAggressive(raiseArmTicks>=5 && ticksUntilNextAttack < attackInterval/2). The aggressive flag is a metadata
## bit (client visual) — server-side it's a DATA flag set; behaviorally ZombieAttackGoal == MeleeAttackGoal +
## that flag. v1: port as MeleeAttackGoal (the setAggressive metadata is a cite-deferrable client visual, OR set
## the DATA_ZOMBIE flag additively). NO RNG.

## --- Spider$SpiderAttackGoal (inner class, extends MeleeAttackGoal) — ⚠ HAS RNG (daylight flee) ---
## ctor: super(spider, 1.0, true). canUse(): super.canUse() && !mob.isVehicle().
## canContinueToUse():
##   float br = mob.getLightLevelDependentMagicValue();
##   if (br >= 0.5f && mob.getRandom().nextInt(100) == 0) { mob.setTarget(null); return false; }   // RNG: daylight-drop 1/100/tick
##   return super.canContinueToUse();
## ⇒ a spider in bright light (br>=0.5) drops its target 1-in-100 per tick (the "spiders calm in daylight" behavior).
## RNG: nextInt(100) drawn EACH canContinueToUse tick WHEN br>=0.5 (mob stream — lockstep if dogfooded; but the
## pig oracle is untouched, so a focused spider RNG test suffices). getLightLevelDependentMagicValue needs a light
## read — with no light engine, gate on the SAME gametime-darkness proxy (br = day? bright : dark) the spawn rule
## uses; document the deferral. The daylight-flee draw must still be made when "bright" per the proxy for fidelity.

## --- HurtByTargetGoal.start + the lastHurtByMob bookkeeping (the NEW entity state Phase 35 must add) ---
## HurtByTargetGoal.start():
##   mob.setTarget(mob.getLastHurtByMob()); targetMob = mob.getTarget(); timestamp = mob.getLastHurtByMobTimestamp();
##   unseenMemoryTicks = 300; if (alertSameType) alertOthers(); super.start();
## LivingEntity fields (javap-confirmed): private EntityReference<LivingEntity> lastHurtByMob; private int
## lastHurtByMobTimestamp; + getLastHurtByMob()/getLastHurtByMobTimestamp(). Set in LivingEntity.hurt/actuallyHurt
## when an entity attacker hits the mob (alongside the lastDamageSource Phase 31 already tracks — P31 tracked the
## damage SOURCE; this is the attacker ENTITY ref + a timestamp). Phase 35 must ADD lastHurtByMob (an attackTargetID-
## style thin-id ref to the attacker) + lastHurtByMobTimestamp, set at the mob-damage point (combat_mob.go
## applyDamageEntity flag2 store-point — damageSource.attacker already exists per PATTERNS). HurtByTargetGoal.canUse
## (in JARNOTES above) reads timestamp != this.timestamp && lastHurtByMob != null → retaliate. NO RNG in HurtByTarget.

## --- LeapAtTargetGoal (ai.goal.LeapAtTargetGoal) — ⚠ LOCKSTEP (same nextInt un-halving) ---
## flags {JUMP, MOVE}. ctor(mob, yd).  yd = Spider passes 0.4.
## canUse():
##   if (mob.hasControllingPassenger()) return false;
##   target = mob.getTarget(); if (target == null) return false;
##   d = mob.distanceToSqr(target); if (d < 4.0 || d > 16.0) return false;   // target in [4,16] sqr
##   if (!mob.onGround()) return false;
##   return mob.getRandom().nextInt(reducedTickDelay(5)) == 0;               // ⚠ jar reducedTickDelay(5)=ceilDiv(5,2)=3
## canContinueToUse(): !mob.onGround().
## start():
##   Vec3 m = mob.getDeltaMovement();
##   Vec3 delta = new Vec3(target.getX()-mob.getX(), 0.0, target.getZ()-mob.getZ());
##   if (delta.lengthSqr() > 1e-7) delta = delta.normalize().scale(0.4).add(m.scale(0.2));
##   mob.setDeltaMovement(delta.x, yd /*0.4*/, delta.z);                     // the leap impulse — NO RNG in start
## ⚠ THE SAME un-halving as NearestAttackableTargetGoal: the jar gate is nextInt(reducedTickDelay(5))=nextInt(3) to
## compensate for vanilla's every-OTHER-tick eval. The Go driver ticks EVERY tick (no decimation), so the FAITHFUL
## Go gate is the un-halved nextInt(5), NOT nextInt(3). DO NOT call reducedTickDelay — use the raw 5. Pin with the
## focused leap RNG test. (The leap canUse draws ONE nextInt(5) when target is in [4,16] sqr + onGround; zero otherwise.)

## --- OPEN exec-time decompiles (do at 35 open) ---
reassessWeaponGoal, performRangedAttack, RangedBowAttackGoal, MeleeAttackGoal, ZombieAttackGoal,
SpiderAttackGoal, LeapAtTargetGoal, AvoidEntityGoal, FleeSunGoal/RestrictSunGoal, NearestAttackableTargetGoal
ctor (the randomInterval value), findTarget, TargetingConditions, isDarkEnoughToSpawn + checkMonsterSpawnRules
+ checkSurfaceMonstersSpawnRules, SpawnPlacements registration, Zombie reinforcement spawn (SPAWN_REINFORCEMENTS_CHANCE).
