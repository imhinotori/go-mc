# Projectile + Skeleton Bow — jar-verified port notes (26.2, CFR/javap)

Source: temp/cache/26.2-inner.jar. All numbers byte-verified. Cite in code.

## RangedBowAttackGoal<T extends Monster>  (net.minecraft.world.entity.ai.goal.RangedBowAttackGoal)
Skeleton builds it as: `new RangedBowAttackGoal<>(this, 1.0, 20, 15.0f)` → speedModifier=1.0, attackIntervalMin=20 (HARD) / 40 (normal), attackRadius=15.0 → attackRadiusSqr=225.0.
Fields: attackTime=-1, seeTime=0, strafingClockwise/Backwards=false, strafingTime=-1.
Flags: {MOVE, LOOK}. requiresUpdateEveryTick()=true.
- canUse: target != null && isHoldingBow (isHolding(Items.BOW)).
- canContinueToUse: (canUse || !navigation.isDone()) && isHoldingBow.
- start: setAggressive(true).
- stop: setAggressive(false); seeTime=0; attackTime=-1; stopUsingItem().
- tick():
  - target=getTarget(); if null return.
  - targetDistSqr = mob.distanceToSqr(target x,y,z).
  - hasLineOfSight = sensing.hasLineOfSight(target).  [v1 stub: always true, no LoS subsystem — cite]
  - hadLineOfSight = seeTime>0; if hasLoS != hadLoS: seeTime=0.
  - seeTime = hasLoS ? ++seeTime : --seeTime.
  - if targetDistSqr > attackRadiusSqr || seeTime<20: navigation.moveTo(target, speedModifier); strafingTime=-1.
    else: navigation.stop(); ++strafingTime.
  - if strafingTime>=20:
      if nextFloat()<0.3: strafingClockwise = !strafingClockwise.   (DRAW)
      if nextFloat()<0.3: strafingBackwards = !strafingBackwards.    (DRAW)
      strafingTime=0.
  - if strafingTime>-1:
      if targetDistSqr > attackRadiusSqr*0.75: strafingBackwards=false.
      else if targetDistSqr < attackRadiusSqr*0.25: strafingBackwards=true.
      moveControl.strafe(strafingBackwards?-0.5:0.5, strafingClockwise?0.5:-0.5).
      mob.lookAt(target, 30, 30).
    else: lookControl.setLookAt(target, 30, 30).
  - firing (charge-and-release):
      if isUsingItem():
        if !hasLoS && seeTime<-60: stopUsingItem().
        else if hasLoS && ticksUsingItem()>=20:
           stopUsingItem(); performRangedAttack(target, BowItem.getPowerForTime(pullTime=ticksUsingItem)); attackTime=attackIntervalMin.
      else if --attackTime<=0 && seeTime>=-60:
           startUsingItem(BOW hand).
  NOTE: BowItem.getPowerForTime(20) == 1.0f (full charge at 20 ticks). Skeleton always releases at exactly 20 → power 1.0.

## AbstractSkeleton.performRangedAttack(LivingEntity target, float power)   (power=1.0 from full-charge)
```
ItemStack bow = getItemInHand(BOW hand);
ItemStack projectile = getProjectile(bow);           // v1: ARROW
AbstractArrow arrow = getArrow(projectile, power, bow);  // = ProjectileUtil.getMobArrow(...)
double xd = target.getX() - getX();
double yd = target.getY(0.3333) - arrow.getY();       // aim at 1/3 target height
double zd = target.getZ() - getZ();
double dist = sqrt(xd*xd + zd*zd);
Projectile.spawnProjectileUsingShoot(arrow, serverLevel, projectile,
    xd, yd + dist*0.2f, zd, velocity=1.6f, inaccuracy=14 - difficulty.getId()*4);
playSound(SKELETON_SHOOT, 1.0, 1.0/(nextFloat()*0.4+0.8));
```
difficulty ids: PEACEFUL=0, EASY=1, NORMAL=2, HARD=3. inaccuracy: easy=10, normal=6, hard=2.
Arrow spawn Y: AbstractArrow ctor sets pos at shooter eye-ish; getY() of arrow used for yd.

## Projectile.shoot(xd,yd,zd, pow, uncertainty)
```
movement = getMovementToShoot(xd,yd,zd,pow,uncertainty);
setDeltaMovement(movement);
sd = movement.horizontalDistance();
setYRot(atan2(movement.x, movement.z) * 180/pi);
setXRot(atan2(movement.y, sd) * 180/pi);
yRotO=yRot; xRotO=xRot;
```
getMovementToShoot:
```
new Vec3(xd,yd,zd).normalize()
  .add(random.triangle(0, 0.0172275*uncertainty) x3)   // 3 independent triangle draws x,y,z
  .scale(pow);                                          // pow=1.6
```
RandomSource.triangle(mode, deviation) = mode + deviation*(nextDouble()-nextDouble()).  (2 nextDouble draws each)

## AbstractArrow physics
Constants: getAirDrag()=0.99f, getWaterInertia()=0.6f, getDefaultGravity()=0.05 (Arrow no override).
baseDamage: setBaseDamageFromMob(power): baseDamage = power*2.0 + random.triangle(difficulty.getId()*0.11, 0.57425).
  (power=1.0 → base ≈ 2.0 + triangle noise.)
tickDespawn: ++life; if life>=1200 discard.

tick():
```
physics = !isNoPhysics();  // true
movement = getDeltaMovement();
blockState @ blockPosition();
if !air && physics && collisionShape non-empty && aabb.move(pos).contains(position):
   setDeltaMovement(ZERO); setInGround(true).
if shakeTime>0: --shakeTime.
if isInWaterOrRain(): clearFire().
if isInGround() && physics:
   [ground: tickDespawn / startFalling logic]; ++inGroundTime; return.
inGroundTime=0;
originalPosition=position();
if isInWater(): applyInertia(waterInertia=0.6) + bubble particles.
[crit particles — skip, cosmetic]
yRot/xRot lerp from movement atan2.
checkLeftOwner();
blockHitResult = clip(originalPosition -> originalPosition+movement);
stepMoveAndHit(blockHitResult);   // moves to hit or end; onHitEntity/onHitBlock
if !isInWater(): applyInertia(airDrag=0.99).
if physics && !isInGround(): applyGravity().  // dy -= 0.05 (via Entity.applyGravity)
super.tick();
```
applyInertia(f): deltaMovement = deltaMovement.scale(f).
ORDER per tick (airborne): move by v → v *= 0.99 (drag) → v.y -= 0.05 (gravity).

onHitEntity(hitResult):
```
pow = getDeltaMovement().length();
arrowDamage = baseDamage;   // + enchant mods (v1 none)
damage = ceil(clamp(pow * arrowDamage, 0, MAXINT));
[crit: += random.nextInt(damage/2+2) — skip if not crit]
if isOnFire() && !enderman: entity.igniteForSeconds(5.0).
entity.hurtOrSimulate(damageSources().arrow(this, owner), damage);
if hit living: setArrowCount+1; doKnockback(mob, source); ...
```
damage source = arrow (owner attributed). doKnockback = projectile knockback (default like a hit).

## Go port plan (Leaf-faithful, optimization-only deviations)
- New Entity kind: Arrow (non-mob moving entity). Needs: owner id, baseDamage, life, inGround, shakeTime, deltaMovement, weapon/crit flags (v1: crit=false, pierce=0).
- Entity type id: entity.Arrow (data/entity). Spawn packet ClientboundAddEntity with arrow type + velocity (short-encoded) + yaw/pitch.
- Arrow tick: separate from mob AI tick — physics only (move, drag, gravity, ground-stick, entity hit → applyDamage, despawn@1200). Runs in tick_phases after mob AI.
- Skeleton: swap melee-only → ranged. Add goal kind="ranged_bow_attack" routed to a Go-native RangedBowAttackGoal. It calls a host seam performRangedAttack that spawns an Arrow into the tick entity set.
- LoS = stub true (no sensing subsystem). isUsingItem/ticksUsingItem = model the 20-tick charge with a per-goal counter (bow always full-charge → power 1.0).
- RNG: skeleton's own seeded RandomSource for strafe draws + performRangedAttack noise + shoot triangle noise + baseDamageFromMob noise. Draw ORDER must match. Pig oracle untouched (separate stream).
