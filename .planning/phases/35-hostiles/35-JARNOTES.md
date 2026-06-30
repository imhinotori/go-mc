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

## --- OPEN exec-time decompiles (do at 35 open) ---
reassessWeaponGoal, performRangedAttack, RangedBowAttackGoal, MeleeAttackGoal, ZombieAttackGoal,
SpiderAttackGoal, LeapAtTargetGoal, AvoidEntityGoal, FleeSunGoal/RestrictSunGoal, NearestAttackableTargetGoal
ctor (the randomInterval value), findTarget, TargetingConditions, isDarkEnoughToSpawn + checkMonsterSpawnRules
+ checkSurfaceMonstersSpawnRules, SpawnPlacements registration, Zombie reinforcement spawn (SPAWN_REINFORCEMENTS_CHANCE).
