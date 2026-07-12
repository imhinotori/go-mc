// breeze.go -- BREEZE (net.minecraft.world.entity.monster.breeze.Breeze), 1:1 port from the unobfuscated
// 26.2 jar. Breeze is a trial-chamber hostile that JUMPS around its target (BreezeAi LongJump/Slide) and
// fires WIND CHARGE projectiles at range (BreezeAi Shoot -> BreezeWindCharge). It is a Brain mob; this port
// lands the attributes + spawn + the target acquisition + the SIGNATURE shoot cadence (windup/recover/
// cooldown) driving the ranged attack, with the WindCharge projectile ENTITY deferred behind the
// hurtingprojectile subsystem. Additive + per-type-gated behind e.isBreeze (false for every other entity;
// the pig oracle stays byte-identical).
//
// VANILLA (verified javap this task):
//   createAttributes: Mob.createMobAttributes + MOVEMENT_SPEED 0.6299999952316284 + MAX_HEALTH 30.0 +
//     FOLLOW_RANGE 24.0 + ATTACK_DAMAGE 3.0.
//   Shoot behavior: SHOOT_INITIAL_DELAY_TICKS = round(15.0f) = 15 (windup), SHOOT_RECOVER_DELAY_TICKS =
//     round(4.0f) = 4, SHOOT_COOLDOWN_TICKS = round(10.0f) = 10. On the fire tick it constructs a
//     BreezeWindCharge(this, level) and shoots it toward the target (getFiringYPosition aim).
//   withinInnerCircleRange(vec): Vec3.atCenterOf(blockPosition()).closerThan(vec, 4.0, 10.0) -- the inner
//     ring the Breeze slides to keep.
//   causeFallDamage: if fallDist > 3.0 play BREEZE_LAND; the Breeze takes NO fall damage (returns false).
//   canAttack(target): only a live target; BreezeAttackEntitySensor feeds the nearest attackable within
//     FOLLOW_RANGE (24).
//
// LANDED: the 4 attributes, spawn, the nearest-player target acquisition (FOLLOW_RANGE 24), the SIGNATURE
// shoot cadence (breezeShootCooldown: fire when in range + off cooldown, then arm SHOOT_COOLDOWN_TICKS),
// and the BreezeAi Slide reposition (breezeSlide: flee-away when inside the inner circle, else a point
// behind the target / in the middle circle -- the Breeze now MOVES around its target). RNG on the Breeze
// OWN stream only.
//
// v1 REDUCTIONS (cited): the Slide behavior is ported 1:1 (breezeSlide), but LongJump (the big vertical
// repositioning jump gated on the BREEZE_JUMP_COOLDOWN memory) is REDUCED -- it needs the Brain
// JumpControl + LongJumpToRandomPos midair steering the code-driven breeze does not carry. The Slide is
// the primary ground reposition, so the Breeze repositions faithfully without the jump. The full Brain
// graph (memory/activity scheduling) is collapsed into breezeAiStep. Sounds + animation states are
// client-cosmetic.

package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Breeze constants (VERIFIED javap this task).
const (
	breezeMaxHealth         = 30.0                // createAttributes MAX_HEALTH 30.0
	breezeMovementSpeed     = 0.6299999952316284  // createAttributes MOVEMENT_SPEED (float-widened)
	breezeFollowRange       = 24.0                // createAttributes FOLLOW_RANGE 24.0
	breezeAttackDamage      = 3.0                 // createAttributes ATTACK_DAMAGE 3.0
	breezeShootInitial      = 15                  // SHOOT_INITIAL_DELAY_TICKS = round(15.0f)
	breezeShootRecover      = 4                   // SHOOT_RECOVER_DELAY_TICKS = round(4.0f)
	breezeShootCooldownT    = 10                  // SHOOT_COOLDOWN_TICKS = round(10.0f)
	breezeInnerCircleXZ     = 4.0                 // withinInnerCircleRange closerThan x/z (ldc2_w 4.0d)
	breezeInnerCircleY      = 10.0                // withinInnerCircleRange closerThan y (ldc2_w 10.0d)
	breezeFallLandDist      = 3.0                 // causeFallDamage: fallDist > 3.0 plays BREEZE_LAND
	breezeAttackRangeMaxSqr = 256.0               // Shoot.isTargetWithinRange: distanceToSqr < 256.0 (range = sqrt(256) = 16 blocks); the value is compared against the ALREADY-SQUARED distance -- do NOT square it again
	breezeShootPower        = 0.7                 // Shoot: spawnProjectileUsingShoot(...) velocity 0.7f (ldc 0.7f)
	breezeShootBaseInacc    = 5.0                 // Shoot inaccuracy = 5 - difficulty.getId()*4 (iconst_5; imul 4; isub)
	breezeFiringYExtra      = 0.30000001192092896 // getFiringYPosition: getY() + getBbHeight()/2 + 0.3d (ldc2_w)
	breezeTargetAimFrac     = 0.3                 // Shoot target aim: target.getY(0.3) (non-passenger) (ldc2_w 0.3d)

	// BreezeAi movement constants (VERIFIED javap -constants BreezeAi this task).
	breezeSlideSpeed       = 0.6                // BreezeAi.SPEED_MULTIPLIER_WHEN_SLIDING (0.6f) == the Slide WalkTarget speedModifier
	breezeMiddleCircleHi   = 8.0                // Slide.randomPointInMiddleCircle: length - Mth.lerp(nextDouble, 8.0, 4.0)
	breezeMiddleCircleLo   = 4.0                // Slide.randomPointInMiddleCircle lerp hi/lo (start 8.0, end 4.0)
	breezeSlidePosAwayH    = 5                  // Slide.start: DefaultRandomPos.getPosAway(breeze, 5, 5, target.pos) horizontal
	breezeSlidePosAwayV    = 5                  // Slide.start: DefaultRandomPos.getPosAway(breeze, 5, 5, target.pos) vertical
	breezePosAwayMaxRadian = 1.5707963705062866 // DefaultRandomPos.getPosAway maxXzRadiansFromDir (pi/2, ldc2_w)
	breezeLoSExtraRange    = 50.0               // BreezeUtil.getMaxLineOfSightTestRange: max(50.0, FOLLOW_RANGE)
	breezeBehindTargetYaw  = 90.0               // BreezeUtil.randomPointBehindTarget: yHeadRot+180 + (float)nextGaussian*90/2
	breezeBehindDistLo     = 4.0                // BreezeUtil.randomPointBehindTarget: Mth.lerp(nextFloat, 4.0f, 8.0f)
	breezeBehindDistHi     = 8.0
	breezeDegToRad         = 0.017453292 // Vec3.directionFromRotation: yaw*(-0.017453292f) (deg->rad float factor)
)

// spawnBreeze creates a hostile Breeze at (x,y,z) and adds it to the owner region store. Minimal e.ai
// (per-entity rng + attack-target slot); NO goalSelector (the behavior is the code-driven breezeAiStep,
// like spawnBlaze/spawnGhast). initSpawnHealth seeds health from MAX_HEALTH (30.0). Cite
// Breeze(EntityType, Level) + createAttributes.
func (t *TickLoop) spawnBreeze(x, y, z float64) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.Breeze, x, y, z)
	b.isBreeze = true
	initSpawnHealth(b) // setHealth(getMaxHealth()) -> 30.0
	b.ai = &mobAI{}
	reseedMobAI(b.ai, b.id)
	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	return b
}

// breezeTarget reads the Breeze current attack-target player, or nil (mirrors blazeTarget).
func (t *TickLoop) breezeTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// breezeAcquireNearestPlayer ports the BreezeAttackEntitySensor + target: nearest live player within
// FOLLOW_RANGE (24.0). NO RNG. Cite Breeze BreezeAttackEntitySensor + canAttack.
func (t *TickLoop) breezeAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 24.0
	rangeSqr := followRange * followRange
	var best *tickPlayer
	bestSqr := rangeSqr
	for _, p := range t.players {
		if p == nil || p.dead || p.gameMode == gameModeSpectator || p.gameMode == gameModeCreative {
			continue
		}
		d := distanceToSqrPlayer(p, e)
		if d <= bestSqr {
			bestSqr = d
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID
	} else {
		e.ai.attackTargetID = 0
	}
}

// breezeAiStep ports the Breeze per-tick server logic (customServerAiStep brain reduced to the Shoot
// cadence + jump-around intent), driven per-type from tickAI (gated on typ == entity.Breeze.ID, AFTER
// serverAiStep). Order: (1) acquire nearest player; (2) tick the shoot cooldown; (3) with a target in
// range + off cooldown, FIRE a wind charge (DEFERRED projectile) and arm SHOOT_COOLDOWN_TICKS. RNG on the
// Breeze OWN stream only. Cite Breeze.customServerAiStep + BreezeAi Shoot.
func (t *TickLoop) breezeAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	t.breezeAcquireNearestPlayer(e)
	if e.breezeShootCooldown > 0 {
		e.breezeShootCooldown-- // Shoot cadence countdown (windup/recover/cooldown collapsed)
	}
	target := t.breezeTarget(e)
	if target == nil {
		return
	}

	// LOCOMOTION (BreezeAi Slide, FIGHT activity prio 4): with an ATTACK_TARGET present the Breeze
	// repositions around it -- this runs BEFORE the shoot-range gate so a Breeze whose target is beyond
	// the 16-block shoot range still slides to close/keep distance (in the jar the FIGHT activity is
	// entered on target acquisition, not shoot range). Slide flees away when inside the inner circle,
	// else picks a point behind the target / in the middle circle. LongJump (the big vertical jump on
	// BREEZE_JUMP_COOLDOWN) is the cited v1 REDUCTION. RNG on the Breeze OWN stream. Cite BreezeAi Slide.
	t.breezeSlide(e, target)
	// Shoot.isTargetWithinRange (bytecode 8-25): position().distanceToSqr(target.position()) < 256.0
	// (== range 16 blocks). ATTACK_RANGE_MAX_SQRT (256) is compared DIRECTLY against the squared
	// distance -- it is NOT re-squared (the field name is misleading; the constant is already dist^2).
	if distanceToSqrPlayer(target, e) >= breezeAttackRangeMaxSqr {
		return
	}
	if e.breezeShootCooldown == 0 {
		t.breezeFireWindCharge(e, target)
		e.breezeShootCooldown = breezeShootInitial + breezeShootRecover + breezeShootCooldownT // full cadence
	}
}

// breezeFireWindCharge ports BreezeAi Shoot fire tick: construct a BreezeWindCharge(this, level) aimed at
// the target and shoot it. The WindCharge projectile ENTITY is DEFERRED behind the hurtingprojectile
// subsystem -- the aim + cadence are faithful so the projectile fires the moment BreezeWindCharge lands.
// Cite BreezeAi Shoot (new BreezeWindCharge(breeze, level)).
func (t *TickLoop) breezeFireWindCharge(e *Entity, target *tickPlayer) {
	// getFiringYPosition() = getY() + getBbHeight()/2.0 + 0.30000001192092896 -- the muzzle height.
	firingY := e.y + e.height/2.0 + breezeFiringYExtra

	// The aim vector: dx = target.getX() - breeze.getX(); dy = target.getY(0.3) - firingY;
	// dz = target.getZ() - breeze.getZ() (target.getY(0.3) = target feet + height*0.3, non-passenger path).
	dx := target.x - e.x
	dy := (target.y + float64(playerHeight)*breezeTargetAimFrac) - firingY
	dz := target.z - e.z

	// getMovementToShoot(dx, dy, dz, velocity=0.7f, inaccuracy): normalize + triangle(0, 0.0172275*inacc)
	// per axis, then scale by velocity. inaccuracy = 5 - difficulty.getId()*4 (NORMAL id 2 -> -3; the
	// triangle is symmetric so the sign only mirrors the draw). RNG on the Breeze OWN stream.
	inaccuracy := breezeShootBaseInacc - float64(t.levelDifficulty)*4.0
	r := mobRandom(e)
	vx, vy, vz := normalizeVec3(dx, dy, dz)
	spread := 0.0172275 * inaccuracy
	vx += arrowTriangle(r, 0, spread)
	vy += arrowTriangle(r, 0, spread)
	vz += arrowTriangle(r, 0, spread)
	// scale by velocity (0.7f) -- but pass the pre-scale vector + power to spawnHurtingProjectileShot,
	// which itself does look.normalize()*power. To avoid double-normalizing away the spread, scale here
	// and hand the resulting vector as the direction with power == its magnitude.
	vx *= breezeShootPower
	vy *= breezeShootPower
	vz *= breezeShootPower

	// Spawn the BreezeWindCharge on the existing hurtingprojectile machinery (hurtWindCharge: 1.0 hit
	// damage + the gust explosion knockback, inertia 1.0). The muzzle is (breeze.x, firingY, breeze.z).
	// Pass power == the scaled-vector magnitude so spawnHurtingProjectileShot's normalize*power reproduces
	// exactly (vx,vy,vz). A zero vector (target atop the breeze) degenerates to no launch -- guarded.
	mag := math.Sqrt(vx*vx + vy*vy + vz*vz)
	if mag <= 0 {
		return
	}
	t.spawnHurtingProjectileShot(e.id, hurtWindCharge, e.x, firingY, e.z, vx, vy, vz, mag)
}

// breezeDeflectsProjectile ports the decision in Breeze.deflection(Projectile): a Breeze is in the
// EntityTypeTags.DEFLECTS_PROJECTILES tag (deflects_projectiles.json == {minecraft:breeze}), so it DEFLECTS
// every incoming projectile EXCEPT its own wind-charge family (BREEZE_WIND_CHARGE / WIND_CHARGE) -- those
// pass through (NONE). Returns true when the projectile should be REVERSE-deflected (an arrow bounces back),
// false for a wind charge (NONE) or a non-breeze entity. Mirrors the bytecode branch:
//
//	if (projectile.is(BREEZE_WIND_CHARGE) || projectile.is(WIND_CHARGE)) return NONE;
//	return this.is(DEFLECTS_PROJECTILES) ? PROJECTILE_DEFLECTION : NONE;
//
// PROJECTILE_DEFLECTION plays BREEZE_DEFLECT then applies REVERSE.deflect. Cite Breeze.deflection +
// EntityTypeTags.DEFLECTS_PROJECTILES.
func breezeDeflectsProjectile(breeze *Entity, projectileType entity.ID) bool {
	if !breeze.isBreeze {
		return false
	}
	if projectileType == entity.BreezeWindCharge.ID || projectileType == entity.WindCharge.ID {
		return false // the Breeze's own wind-charge family is NOT deflected (ProjectileDeflection.NONE)
	}
	return true // this.is(DEFLECTS_PROJECTILES) == true for a breeze -> PROJECTILE_DEFLECTION (REVERSE)
}

// breezeDeflectArrow applies ProjectileDeflection.REVERSE.deflect to an arrow that struck a breeze (the
// observable "arrows bounce back off a breeze"). REVERSE.deflect (ProjectileDeflection lambda$static$1):
//
//	f = 170.0f + random.nextFloat()*20.0f;
//	setDeltaMovement(getDeltaMovement().scale(-0.5));   // reverse + halve the velocity
//	setYRot(getYRot() + f); yRotO += f;                 // spin the yaw ~180 deg
//
// The nextFloat() draw is on the ARROW's OWN per-entity stream (Projectile.deflect passes this.random), so no
// mob stream is perturbed. The arrow is NOT consumed (it flies off deflected). Cite ProjectileDeflection.REVERSE
// + Projectile.deflect (passes the projectile's own RandomSource).
func breezeDeflectArrow(a *Entity) {
	if a.arrowRNG == nil {
		a.arrowRNG = newEntityRandom(uint64(a.id))
	}
	f := 170.0 + float64(a.arrowRNG.nextFloat())*20.0
	a.vx *= -0.5
	a.vy *= -0.5
	a.vz *= -0.5
	a.yaw += float32(f)
	a.headYaw = a.yaw
}

// arrowFindHitBreeze is the breeze-scoped sibling of arrowFindHitPlayer: the FIRST breeze (nearest by entry
// param along the segment) whose collision AABB the arrow's flight segment (origin->end) passes through,
// excluding the arrow's shooter. Used to REVERSE-deflect a projectile that reaches a breeze (a breeze is in
// EntityTypeTags.DEFLECTS_PROJECTILES). Scans the arrow's OWN region store (t.cur(), registered by tickArrows'
// withRegion). Zero cost in a breeze-free world (the loop finds none). Cite ProjectileUtil.getEntityHitResult
// scoped to the deflecting mob.
func (t *TickLoop) arrowFindHitBreeze(a *Entity, ox, oy, oz, nx, ny, nz float64) *Entity {
	region := t.cur()
	if region == nil || region.entities == nil {
		return nil
	}
	half := entity.Arrow.Width / 2.0
	var best *Entity
	bestT := 2.0
	for _, m := range region.entities.all() {
		if m == nil || !m.isBreeze || m.dead || !m.isAlive() {
			continue
		}
		if m.id == a.arrowShooterID {
			continue // a breeze never deflects a projectile it (somehow) shot
		}
		// The breeze collision AABB (Breeze bbox width/height, base at feet), inflated by the arrow half-size.
		w := entity.Breeze.Width / 2.0
		minX := m.x - w - half
		maxX := m.x + w + half
		minY := m.y - half
		maxY := m.y + entity.Breeze.Height + half
		minZ := m.z - w - half
		maxZ := m.z + w + half
		if hit, tHit := segmentAABB(ox, oy, oz, nx, ny, nz, minX, minY, minZ, maxX, maxY, maxZ); hit {
			if tHit < bestT {
				bestT = tHit
				best = m
			}
		}
	}
	return best
}

// breezeSlide ports net.minecraft.world.entity.monster.breeze.Slide (the FIGHT-activity reposition
// sink). Mirrors Slide.checkExtraStartConditions (onGround, not in water, pose STANDING) and Slide.start,
// which only (re)commits a WALK_TARGET when the Breeze has NO active path (WALK_TARGET absent). Reduction:
// pose STANDING is always true in v1 (no Breeze pose subsystem), so the gate collapses to onGround and
// not-in-water. On commit it sets the navigation want (WalkTarget speed modifier 0.6). LongJump (the big
// vertical jump on BREEZE_JUMP_COOLDOWN) is the cited v1 REDUCTION (needs the Brain JumpControl +
// LongJumpToRandomPos midair steering). Cite Slide.checkExtraStartConditions + Slide.start + BreezeAi.
func (t *TickLoop) breezeSlide(e *Entity, target *tickPlayer) {
	if e.ai == nil {
		return
	}
	// Slide.checkExtraStartConditions: onGround and not in water and pose STANDING (STANDING always true).
	if !e.onGround || t.entityInWater(e) {
		return
	}
	// Slide.start only (re)commits when WALK_TARGET is ABSENT; nav-active is the present-path analogue.
	if e.ai.navigation.active() {
		return
	}
	inner := breezeWithinInnerCircle(e, target.x, target.y, target.z)
	haveDest := false
	var destX, destY, destZ float64
	if inner {
		ax, ay, az, ok := t.breezeGetPosAway(e, target.x, target.z)
		if ok && t.breezeHasLineOfSight(e, ax, ay, az) {
			distAway := sqrDist(target.x, target.y, target.z, ax, ay, az)
			distSelf := sqrDist(target.x, target.y, target.z, e.x, e.y, e.z)
			if distAway > distSelf {
				destX, destY, destZ, haveDest = ax, ay, az, true
			}
		}
	}
	if !haveDest {
		if mobRandom(e).nextBoolean() {
			destX, destY, destZ = breezeRandomPointBehindTarget(e, target)
		} else {
			destX, destY, destZ = breezeRandomPointInMiddleCircle(e, target)
		}
	}
	e.ai.setWantTargetMod(math.Floor(destX)+0.5, math.Floor(destY), math.Floor(destZ)+0.5, breezeSlideSpeed)
}

// breezeWithinInnerCircle ports Breeze.withinInnerCircleRange(vec): from the Breeze BLOCK CENTER
// (floor+0.5), dx^2+dz^2 < 4.0^2 and |dy| < 10.0 (Vec3.atCenterOf(blockPosition()).closerThan(vec, 4.0,
// 10.0)). Cite Breeze.withinInnerCircleRange + Vec3.closerThan(Vec3, double, double).
func breezeWithinInnerCircle(e *Entity, tx, ty, tz float64) bool {
	cx := math.Floor(e.x) + 0.5
	cy := math.Floor(e.y) + 0.5
	cz := math.Floor(e.z) + 0.5
	dx := tx - cx
	dy := ty - cy
	dz := tz - cz
	return (dx*dx+dz*dz) < breezeInnerCircleXZ*breezeInnerCircleXZ && math.Abs(dy) < breezeInnerCircleY
}

// breezeGetPosAway ports DefaultRandomPos.getPosAway(breeze, 5, 5, target.pos): the 10-candidate flee-pos
// chooser (each candidate draws nextFloat + nextDouble + nextInt via generateRandomDirectionWithinRadians),
// away vector = breeze.position() - target.position(). Reuses the shared generateRandomDirectionWithinRadians
// with the Slide horizontal/vertical (5, 5) and maxXzRadiansFromDir pi/2. Returns the absolute flee pos
// (block center X/Z, integer Y) or ok=false. RNG on the Breeze OWN stream. Cite DefaultRandomPos.getPosAway.
func (t *TickLoop) breezeGetPosAway(e *Entity, avoidX, avoidZ float64) (x, y, z float64, ok bool) {
	r := mobRandom(e)
	dirX := e.x - avoidX
	dirZ := e.z - avoidZ
	bestWeight := math.Inf(-1)
	have := false
	var bx, by, bz float64
	for i := 0; i < 10; i++ { // generateRandomPos: for i<10, NO break (all 10 supplier calls run)
		dxr, dyr, dzr, dok := generateRandomDirectionWithinRadians(r, 0.0, breezeSlidePosAwayH, breezeSlidePosAwayV, 0, dirX, dirZ, breezePosAwayMaxRadian)
		if !dok {
			continue
		}
		posX := math.Floor(dxr + e.x)
		posY := math.Floor(dyr + e.y)
		posZ := math.Floor(dzr + e.z)
		const walkTargetValue = 0.0 // getWalkTargetValue == 0.0f for a base PathfinderMob (jar-verified)
		if walkTargetValue > bestWeight {
			bestWeight = walkTargetValue
			bx, by, bz = posX+0.5, posY, posZ+0.5 // Vec3.atBottomCenterOf(best)
			have = true
		}
	}
	if !have {
		return 0, 0, 0, false
	}
	return bx, by, bz, true
}

// breezeHasLineOfSight ports BreezeUtil.hasLineOfSight(breeze, vec): false if the candidate is farther
// than max(50.0, FOLLOW_RANGE); else clip a COLLIDER ray from the Breeze to the candidate and return true
// iff it MISSES. clipBlocksCollider returns hit=true when blocked, so LOS is !hit. Cite
// BreezeUtil.hasLineOfSight + getMaxLineOfSightTestRange.
func (t *TickLoop) breezeHasLineOfSight(e *Entity, x, y, z float64) bool {
	maxRange := math.Max(breezeLoSExtraRange, e.getAttributeValue(attribute.FollowRange))
	if sqrDist(e.x, e.y, e.z, x, y, z) > maxRange*maxRange {
		return false // vec.distanceTo(breezePos) > maxRange -> no LOS
	}
	return !t.clipBlocksCollider(e.x, e.y, e.z, x, y, z)
}

// breezeRandomPointBehindTarget ports BreezeUtil.randomPointBehindTarget(target, rng): a point ~4-8 blocks
// behind the target (target head yaw + 180), jittered by a gaussian. DRAW ORDER (Breeze stream):
// nextGaussian() (yaw jitter), then nextFloat() (distance lerp). Returns the absolute world point.
//
//	VERIFIED javap: yaw = target.yHeadRot + 180.0f + (float)nextGaussian * 90.0f / 2.0f;
//	dist = Mth.lerp(nextFloat, 4.0f, 8.0f); dir = Vec3.directionFromRotation(0.0f, yaw).scale(dist);
//	return target.position().add(dir).
func breezeRandomPointBehindTarget(e *Entity, target *tickPlayer) (x, y, z float64) {
	r := mobRandom(e)
	// A player head yaw is its facing yaw (v1 stand-in for LivingEntity.yHeadRot). ALL float32 arithmetic.
	yaw := target.headYaw + 180.0 + float32(r.nextGaussian())*float32(breezeBehindTargetYaw)/2.0
	dist := float32(breezeBehindDistLo) + r.nextFloat()*(float32(breezeBehindDistHi)-float32(breezeBehindDistLo))
	dirX, dirY, dirZ := breezeDirectionFromRotation(0.0, yaw)
	return target.x + float64(dirX)*float64(dist), target.y + float64(dirY)*float64(dist), target.z + float64(dirZ)*float64(dist)
}

// breezeRandomPointInMiddleCircle ports Slide.randomPointInMiddleCircle(breeze, target): step from the
// Breeze toward the target into the middle-circle band. DRAW: ONE nextDouble() (the radial lerp).
//
//	VERIFIED javap: toTarget = target.position() - breeze.position();
//	dist = toTarget.length() - Mth.lerp(nextDouble(), 8.0, 4.0);
//	step = toTarget.normalize().multiply(dist,dist,dist); return breeze.position().add(step).
func breezeRandomPointInMiddleCircle(e *Entity, target *tickPlayer) (x, y, z float64) {
	r := mobRandom(e)
	tx := target.x - e.x
	ty := target.y - e.y
	tz := target.z - e.z
	length := math.Sqrt(tx*tx + ty*ty + tz*tz)
	dist := length - lerp(r.nextDouble(), breezeMiddleCircleHi, breezeMiddleCircleLo)
	nx, ny, nz := normalizeVec3(tx, ty, tz)
	return e.x + nx*dist, e.y + ny*dist, e.z + nz*dist
}

// breezeDirectionFromRotation ports Vec3.directionFromRotation(pitch, yaw) (both degrees). ALL float32
// arithmetic (the jar operates on floats then widens). Cite Vec3.directionFromRotation(float, float).
//
//	VERIFIED javap: h = Mth.cos((-yaw*0.017453292f) - pi); i = Mth.sin((-yaw*0.017453292f) - pi);
//	j = -Mth.cos(-pitch*0.017453292f); k = Mth.sin(-pitch*0.017453292f); return new Vec3(i*j, k, h*j).
func breezeDirectionFromRotation(pitch, yaw float32) (x, y, z float32) {
	yr := -yaw*float32(breezeDegToRad) - float32(math.Pi)
	h := mthCos(float64(yr))
	i := mthSin(float64(yr))
	pr := -pitch * float32(breezeDegToRad)
	j := -mthCos(float64(pr))
	k := mthSin(float64(pr))
	return i * j, k, h * j
}
