package server

// projectile_shoot.go -- the shared launch-vector math for a PLAYER-shot projectile: a 1:1 port of
// net.minecraft.world.entity.projectile.Projectile.shootFromRotation / shoot / getMovementToShoot,
// decompiled from temp/cache/26.2-inner.jar (javap -c -p this session). Every player spawn site
// (BowItem/CrossbowItem -> AbstractArrow, TridentItem -> ThrownTrident, SnowballItem/EggItem/
// EnderpearlItem/ExperienceBottleItem -> ThrowableItemProjectile, WindChargeItem -> WindCharge) drives
// its launch through Projectile.spawnProjectileFromRotation, which does shootFromRotation(shooter,
// xRot, yRot+angleOffset, 0, velocity, inaccuracy). This file ports that method-for-method so the
// per-axis triangle inaccuracy draws (RNG-order-critical) and the owner velocity-inheritance are exact.
//
// getMovementToShoot(x,y,z, velocity, inaccuracy):   [VERIFIED javap offsets 0-74]
//	new Vec3(x,y,z).normalize()
//	  .add(random.triangle(0.0, 0.0172275*inaccuracy),   // draw 1 (x)
//	       random.triangle(0.0, 0.0172275*inaccuracy),   // draw 2 (y)
//	       random.triangle(0.0, 0.0172275*inaccuracy))   // draw 3 (z)
//	  .scale(velocity)
// shoot(x,y,z, velocity, inaccuracy): setDeltaMovement(getMovementToShoot(...)).   [offsets 0-17]
// shootFromRotation(shooter, xRot, yRot, angle, velocity, inaccuracy):   [offsets 0-113]
//	view = (-sin((yRot+angle)*DEG)*cos(xRot*DEG), -sin(xRot*DEG), cos((yRot+angle)*DEG)*cos(xRot*DEG))
//	shoot(view.x, view.y, view.z, velocity, inaccuracy)
//	km = shooter.getKnownMovement()
//	setDeltaMovement(getDeltaMovement().add(km.x, shooter.onGround()? 0.0 : km.y, km.z))
//
// The triangle draws come from the PROJECTILE's OWN per-entity RandomSource (Projectile.random), NOT the
// shooter's -- so a pig (which fires no projectile) never touches this path and the pinned oracle stream
// is unperturbed.

import "math"

// projectileInaccuracyFactor is Projectile.getMovementToShoot's 0.0172275 spread-per-inaccuracy constant
// (ldc2_w 0.0172275d at offset 19/36/53). The per-axis spread == 0.0172275 * inaccuracy.
const projectileInaccuracyFactor = 0.0172275

// shootVectorFromRotation ports Projectile.shootFromRotation's launch-vector computation for a player
// shooter: build the look vector from (yaw, pitch) with the angle offset, run shoot() (normalize + the
// three triangle inaccuracy draws + scale by velocity) using the projectile's OWN rng, then inherit the
// shooter's known movement (x always; y only when the shooter is NOT on the ground; z always). Returns
// the initial deltaMovement (vx, vy, vz). Cite Projectile.shootFromRotation + shoot + getMovementToShoot.
func shootVectorFromRotation(rng *entityRandom, yaw, pitch, angleOffset float32, velocity, inaccuracy float64, ownerVX, ownerVY, ownerVZ float64, ownerOnGround bool) (vx, vy, vz float64) {
	// view vector: identical to playerViewVector but with the yaw angle offset added first (the xp bottle
	// uses angle -20, the rest use 0). Uses the Mth.sin/cos float32 idiom the rest of the port uses.
	yr := (yaw + angleOffset) * degToRad
	xr := pitch * degToRad
	yCos := float32(math.Cos(float64(yr)))
	ySin := float32(math.Sin(float64(yr)))
	xCos := float32(math.Cos(float64(xr)))
	xSin := float32(math.Sin(float64(xr)))
	lx := float64(-ySin * xCos)
	ly := float64(-xSin)
	lz := float64(yCos * xCos)

	// shoot(lx,ly,lz, velocity, inaccuracy) == getMovementToShoot: normalize, add the three triangle draws
	// (x,y,z order), scale by velocity.
	mag := math.Sqrt(lx*lx + ly*ly + lz*lz)
	if mag != 0 {
		lx, ly, lz = lx/mag, ly/mag, lz/mag
	}
	spread := projectileInaccuracyFactor * inaccuracy
	lx += arrowTriangle(rng, 0.0, spread) // draw 1 (x)
	ly += arrowTriangle(rng, 0.0, spread) // draw 2 (y)
	lz += arrowTriangle(rng, 0.0, spread) // draw 3 (z)
	vx = lx * velocity
	vy = ly * velocity
	vz = lz * velocity

	// getKnownMovement() inherit: add owner x, owner z always; owner y only when NOT on ground.
	vx += ownerVX
	if !ownerOnGround {
		vy += ownerVY
	}
	vz += ownerVZ
	return vx, vy, vz
}

// playerKnownMovement is the LivingEntity.getKnownMovement() seam for a player shooter. On a dedicated
// server the player's real per-tick movement delta (the client-authoritative walk/run velocity) is not
// yet tracked (attack_dispatch notes the same knownMovement gate is deferred), so this returns the
// vanilla-consistent (0,0,0) for a still shooter -- structured to become a real read once the player
// movement-delta subsystem lands. The server-applied knockback residue on playerEntity is deliberately
// NOT inherited (it is not getKnownMovement). Cite LivingEntity.getKnownMovement.
func (t *TickLoop) playerKnownMovement(_ *tickPlayer) (mx, my, mz float64) {
	return 0, 0, 0
}
