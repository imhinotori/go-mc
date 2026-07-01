package server

import "github.com/imhinotori/sulfur/level/attribute"

// mob_fall_damage.go — LIVE-DEBUG B (the "mobs take no fall damage" fix): the *Entity sibling of the
// player fall-damage path (fall_damage.go). The player path (tickFallDamage) accumulated fallDistance
// and called causeFallDamage ONLY for tickPlayers; a mob (*Entity) accumulated no fallDistance and
// never called causeFallDamage, so a mob that fell from height took zero damage. This wires the
// vanilla Entity.checkFallDamage + LivingEntity.causeFallDamage chain into the mob physics tick
// (tickPhysics), routing the computed damage through applyDamageEntity (the Phase-29 keystone) and
// REUSING the already-ported, jar-verified calculateFallDamage free function (fall_damage.go).
//
// LITERAL 1:1 PORT OF VANILLA JAVA 26.2 (protocol 776) — verified method-for-method against
// temp/cache/26.2-inner.jar via `javap -c -p` this session. The two vanilla methods (the SAME ones
// the player path ports — fall_damage.go's header documents the bytecode in full):
//
//	net.minecraft.world.entity.Entity.checkFallDamage(double deltaY, boolean onGround, BlockState, BlockPos):
//	  if (!isInWater() && deltaY < 0.0) this.fallDistance -= (double)(float) deltaY;   // d2f THEN f2d
//	  if (onGround) {
//	    if (this.fallDistance > 0.0) Block.fallOn(...) [-> causeFallDamage(fallDistance, 1.0F, FALL)];
//	    this.resetFallDistance();
//	  }
//
//	net.minecraft.world.entity.LivingEntity.causeFallDamage(double d, float mul, DamageSource src):
//	  // (non-impulse else path: d passes through unchanged)
//	  int i = calculateFallDamage(d, mul);
//	  if (i > 0) { playSound(getFallDamageSound(i)); playBlockFallSound(); hurt(src, (float) i); return true; }
//	  return false;
//
// MAPPING TO THE MOB PATH:
//   - deltaY: vanilla's deltaY is deltaMovement.y (the vertical velocity about to be integrated).
//     Sulfur's tickPhysics integrates e.vy via moveEntity, so e.vy IS deltaMovement.y at the
//     checkFallDamage call site — accumulateMobFallDistance is called with e.vy BEFORE moveEntity,
//     exactly as vanilla checks fall damage on the pre-move deltaMovement.y.
//   - isInWater(): t.mobInWater(e) (the Phase-30 predicate). A mob descending in water adds NO
//     fallDistance, so a mob that falls INTO water takes no fall damage (the vanilla water guard).
//   - onGround: e.onGround, freshly set by moveEntity (it lands a mob whose downward motion was
//     clamped by a solid block this tick), so landMobFallDamage is called AFTER moveEntity.
//   - causeFallDamage's calculateFallDamage(d, 1.0) REUSES the player path's free function (the math
//     is IDENTICAL — LivingEntity.calculateFallDamage is shared by player and mob; the player port
//     already cited the SAFE_FALL_DISTANCE 3.0 / FALL_DAMAGE_MULTIPLIER 1.0 / Mth.floor ops).
//   - hurt(src, (float) i): t.applyDamageEntity(e, damageSourceOf(damageTypeFall), float32(i)) — the
//     Phase-29 keystone mob hurt pipeline (combat_mob.go). FALL routes through it normally (it is a
//     bypasses_armor source, which actuallyHurtEntity's armor branch reads — NOT an i-frame bypass).
//   - Block.fallOn's default forwards damageMultiplier = 1.0 (per-block fallOn multipliers — hay
//     bales etc. — are deferred, exactly as the player path defers them). The fall SOUNDS
//     (getFallDamageSound / playBlockFallSound) are deferred too (no per-damage fall-sound table is
//     ported; the player path skips sounds the same way) — cited, structured to slot in later.
//
// RNG-FREE: the whole chain is pure float/int math + the fluid predicate read, so it draws no random
// and cannot perturb the pig oracle's per-mob RNG stream (PITFALLS Pitfall 5). The dead-mob corpse is
// frozen in tickPhysics (the `if e.dead { continue }` guard) so a corpse never accumulates or lands
// fall damage — the live-mob gate holds.

// resetFallDistanceEntity mirrors net.minecraft.world.entity.Entity.resetFallDistance() for a mob
// (bytecode: dconst_0 putfield fallDistance) — it zeroes the accumulated descent. Defined as a method
// so the call sites read like the vanilla chain (checkFallDamage's landing reset + the water reset).
func (e *Entity) resetFallDistanceEntity() {
	e.fallDistance = 0
}

// accumulateMobFallDistance is the descent-accumulation half of Entity.checkFallDamage for a mob:
//
//	if (!isInWater() && deltaY < 0.0) this.fallDistance -= (double)(float) deltaY;
//
// deltaY is e.vy (vanilla's deltaMovement.y) read BEFORE moveEntity integrates it. inWater is
// t.mobInWater(e) (the !isInWater() guard) — a mob descending in water adds no fallDistance, so a
// fall INTO water deals no fall damage. The d2f-then-f2d narrowing-then-widening cast is ported
// VERBATIM as float64(float32(deltaY)) — it is part of vanilla's numeric behavior and is NOT skipped
// (the SAME cast the player path's checkFallDamage performs, fall_damage.go:193).
func (t *TickLoop) accumulateMobFallDistance(e *Entity, deltaY float64, inWater bool) {
	if !inWater && deltaY < 0.0 {
		// d2f then f2d: the (float) narrowing cast widened back to double, ported verbatim.
		e.fallDistance -= float64(float32(deltaY))
	}
}

// landMobFallDamage is the landing half of Entity.checkFallDamage for a mob:
//
//	if (onGround) {
//	    if (this.fallDistance > 0.0) causeFallDamage(this.fallDistance, 1.0F, DamageSource.FALL);
//	    this.resetFallDistance();
//	}
//
// Called AFTER moveEntity, where e.onGround is freshly set (moveEntity lands a mob whose downward
// motion was clamped by a solid block this tick). The vanilla `Entity.updateFluidInteraction` water
// reset (resetFallDistance every tick in water, mirroring fall_damage.go:163 for the player) is also
// applied here BEFORE the landing branch: a mob standing in water has its accumulated distance zeroed
// every tick with no damage — so a mob that was falling and entered water on the SAME tick it landed
// takes no fall damage. inWater is t.mobInWater(e).
func (t *TickLoop) landMobFallDamage(e *Entity, inWater bool) {
	// Entity.updateFluidInteraction water reset: zero any pre-water fall distance, with NO damage, on
	// every in-water tick (the player path does this in tickFallDamage, fall_damage.go:163-165). This
	// runs before the landing branch so a fall into water deals 0 damage even on the landing tick.
	if inWater {
		e.resetFallDistanceEntity()
	}

	if e.onGround {
		if e.fallDistance > 0.0 {
			t.causeFallDamageEntity(e, e.fallDistance, 1.0)
		}
		e.resetFallDistanceEntity()
	}
}

// causeFallDamageEntity mirrors the ELSE (non-impulse) path of
// LivingEntity.causeFallDamage(double d, float damageMultiplier, DamageSource src) for a mob:
//
//	int i = calculateFallDamage(d, damageMultiplier);
//	if (i > 0) { /* sounds (deferred) */ this.hurt(DamageSource.FALL, (float) i); return true; }
//	return false;
//
// d passes through unchanged (the currentImpulse/wind-charge branch is not modeled — a mob falling
// normally is not ignoring fall damage from an impulse, so the else path is the faithful one, exactly
// as the player port leaves it). calculateFallDamage(d, 1.0) is the REUSED player free function
// (fall_damage.go:120) — LivingEntity.calculateFallDamage is shared by player and mob, so the mob path
// calls the SAME jar-verified function (SAFE_FALL_DISTANCE 3.0, FALL_DAMAGE_MULTIPLIER 1.0, Mth.floor).
// hurt(FALL, (float) i) is t.applyDamageEntity(e, damageSourceOf(damageTypeFall), float32(i)) — the
// Phase-29 keystone. Returns whether damage was dealt (mirrors the method's boolean result).
func (t *TickLoop) causeFallDamageEntity(e *Entity, d float64, damageMultiplier float64) bool {
	// getAttributeValue(SAFE_FALL_DISTANCE): the live per-mob attribute read — 3.0 for a plain living
	// entity (createLivingAttributes default), 5.0 for a Fox (Fox.createAttributes override). Read at
	// the vanilla LivingEntity.calculateFallPower call site so each mob's own safe-fall threshold
	// applies (a fox survives a taller fall than a pig).
	safeFallDistance := e.getAttributeValue(attribute.SafeFallDistance)
	i := calculateFallDamage(d, damageMultiplier, safeFallDistance)
	if i > 0 {
		// hurt(DamageSource.FALL, (float) i): route the fall damage through the keystone mob hurt
		// pipeline. damageSourceOf(damageTypeFall) is the environmental FALL source (no attacker), the
		// port of DamageSources.fall(). The fall SOUNDS (getFallDamageSound / playBlockFallSound) are
		// deferred — no per-damage fall-sound table is ported (the player path skips sounds the same way).
		t.applyDamageEntity(e, damageSourceOf(damageTypeFall), float32(i))
		return true
	}
	return false
}
