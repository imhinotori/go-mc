package server

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
)

// fall_damage.go — GAMEPLAY-04 (the environmental half). OVERWRITES the 17-01 stub. The call
// site (tick_phases.go tickEntities -> t.tickFallDamage()) and the tickPlayer fall-damage
// fields (fallDistance/wasOnGround/lastY, declared in tick.go) are owned by 17-01 and NOT
// touched here — this plan edits ONLY this file. Damage routes through the existing, tested
// applyDamage->die flow (combat.go).
//
// ============================================================================================
// LITERAL 1:1 PORT OF VANILLA JAVA 26.2 (protocol 776) — re-verified method-for-method against
// temp/cache/26.2-inner.jar via `javap -c -p` this session. This is a STRUCTURE-PRESERVING port:
// each vanilla method is mirrored by a dedicated Go function with the IDENTICAL numeric ops, in
// the same order, so the port is auditable line-for-line against the bytecode. No GPL source is
// pasted — the algorithm is re-expressed in Go — but the structure and arithmetic are identical.
//
// The four vanilla methods, with the bytecode evidence that fixes each numeric op:
//
//	net.minecraft.world.entity.Entity.checkFallDamage(double deltaY, boolean onGround, BlockState, BlockPos)
//	  bytecode:  isInWater ifne 25 | dload_1 dconst_0 dcmpg ifge 25 |
//	             getfield fallDistance | dload_1 d2f f2d dsub | putfield fallDistance |
//	             iload_3 ifeq 102 | getfield fallDistance dconst_0 dcmpl ifle 98 |
//	             Block.fallOn(...) [-> causeFallDamage(fallDistance, 1.0F, FALL)] |
//	             resetFallDistance()
//	  => if (!isInWater() && deltaY < 0.0) fallDistance -= (double)(float) deltaY;   // d2f THEN f2d
//	     if (onGround) { if (fallDistance > 0.0) causeFallDamage(fallDistance, 1.0F, FALL); resetFallDistance(); }
//	  The `d2f f2d` pair is the (float) narrowing-then-widening cast — ported EXACTLY as
//	  float64(float32(deltaY)); it is part of vanilla's numeric behavior and is NOT skipped.
//
//	net.minecraft.world.entity.LivingEntity.causeFallDamage(double d, float mul, DamageSource src)
//	  bytecode:  isIgnoringFallDamageFromCurrentImpulse ifeq 58 | <impulse branch> |
//	             58: dload_1 dstore_5  (else: d passes through unchanged) |
//	             Entity.causeFallDamage(d, mul, src) -> istore_7  (passenger propagation; false for a player) |
//	             calculateFallDamage(d, mul) -> istore_8 (i) |
//	             iload_8 ifle 117 | <sounds> | hurt(src, (float) i) i2f | iconst_1 ireturn |
//	             117: iload_7 ireturn
//	  => normal landing takes the ELSE path (d unchanged); if (i > 0) hurt(src, (float) i).
//	  The currentImpulse/wind-charge branch is intentionally not modeled — a player landing
//	  normally is NOT ignoring fall damage from an impulse, so the `else` path is the faithful one.
//
//	net.minecraft.world.entity.LivingEntity.calculateFallDamage(double d, float mul)
//	  bytecode:  EntityTypeTags.FALL_DAMAGE_IMMUNE is(...) ifeq 12 | iconst_0 ireturn |
//	             calculateFallPower(d) -> dstore_4 |
//	             dload_4 fload_3 f2d dmul | getAttributeValue(FALL_DAMAGE_MULTIPLIER) dmul |
//	             Mth.floor(...) ireturn
//	  => if (FALL_DAMAGE_IMMUNE) return 0;
//	     return Mth.floor(calculateFallPower(d) * (double)mul * getAttributeValue(FALL_DAMAGE_MULTIPLIER));
//
//	net.minecraft.world.entity.LivingEntity.calculateFallPower(double d)
//	  bytecode:  dload_1 ldc2_w 1.0E-6 dadd | getAttributeValue(SAFE_FALL_DISTANCE) dsub | dreturn
//	  => return (d + 1.0E-6) - getAttributeValue(SAFE_FALL_DISTANCE);
//
//	net.minecraft.util.Mth.floor(double)
//	  bytecode:  Math.floor(d) d2i ireturn  => (int) Math.floor(d).  Ported as int(math.Floor(d)).
//
// ATTRIBUTE BASE VALUES (verified in net.minecraft.world.entity.ai.attributes.Attributes.<clinit>):
//   SAFE_FALL_DISTANCE    = RangedAttribute("safe_fall_distance",    3.0, -1024.0, 1024.0) -> base 3.0
//   FALL_DAMAGE_MULTIPLIER = RangedAttribute("fall_damage_multiplier", 1.0,     0.0,  100.0) -> base 1.0
// SAFE_FALL_DISTANCE is now a LIVE per-entity attribute read: calculateFallPower / calculateFallDamage
// take the caller's getAttributeValue(SAFE_FALL_DISTANCE) as a parameter (player: playerAttributes
// attrSafeFallDistance base 3.0; mob: Entity.getAttributeValue(attribute.SafeFallDistance) — 3.0 for a
// plain living entity, 5.0 for a Fox, etc.). FALL_DAMAGE_MULTIPLIER stays a named constant equal to its
// jar base 1.0 (its per-entity attribute is not yet in the v1 set — cited, structured to slot in the
// same way when its consumer needs a non-default value).
//
// WATER GUARD (17-08 — IS vanilla). Vanilla negates ALL fall damage in water via TWO cooperating
// guards, both reproduced here using the 17-02 in-water check (fluid_physics.go:playerInWater):
//   (a) Entity.checkFallDamage accumulates ONLY when `!isInWater()` — descent in water adds no
//       fall distance; and
//   (b) Entity.updateFluidInteraction (run every tick, independent of the landing edge) calls
//       resetFallDistance() whenever the entity is in water — so any fall distance accumulated
//       BEFORE entering the water is zeroed the instant the player touches water, before the
//       onGround landing edge could apply damage.
// ============================================================================================

// Attribute base values from the 26.2 jar (Attributes.<clinit>, RangedAttribute defaults).
// These stand in for getAttributeValue(...) until an attribute system exists; keeping them as
// explicit factors preserves the literal vanilla product power*mul*fallDamageMultiplier.
const (
	// fallDamageMultiplierAttr == getAttributeValue(Attributes.FALL_DAMAGE_MULTIPLIER), base 1.0. Kept
	// as a named constant equal to the jar base (its per-entity attribute is not yet in the v1 set).
	fallDamageMultiplierAttr = 1.0
	// fallDamageEpsilon mirrors calculateFallPower's literal 1.0E-6 addend (ldc2_w 1.0E-6d).
	fallDamageEpsilon = 1.0e-6
)

// mthFloor mirrors net.minecraft.util.Mth.floor(double): `(int) Math.floor(d)` (bytecode:
// Math.floor d2i). Used so calculateFallDamage's flooring is the exact vanilla operation.
func mthFloor(d float64) int {
	return int(math.Floor(d))
}

// resetFallDistance mirrors net.minecraft.world.entity.Entity.resetFallDistance() (bytecode:
// dconst_0 putfield fallDistance) — it sets fallDistance to 0. Defined as a method so the call
// sites read like the vanilla chain (checkFallDamage's reset, updateFluidInteraction's water reset).
func (p *tickPlayer) resetFallDistance() {
	p.fallDistance = 0
}

// calculateFallPower mirrors LivingEntity.calculateFallPower(double d):
//
//	return (d + 1.0E-6) - getAttributeValue(SAFE_FALL_DISTANCE);
//
// safeFallDistance is the caller's getAttributeValue(Attributes.SAFE_FALL_DISTANCE) — a live per-entity
// read (base 3.0; a Fox carries 5.0), passed at the exact bytecode read site.
func calculateFallPower(d float64, safeFallDistance float64) float64 {
	return (d + fallDamageEpsilon) - safeFallDistance
}

// calculateFallDamage mirrors LivingEntity.calculateFallDamage(double d, float damageMultiplier):
//
//	if (getType() in EntityTypeTags.FALL_DAMAGE_IMMUNE) return 0;
//	return Mth.floor(calculateFallPower(d) * (double)damageMultiplier
//	                 * getAttributeValue(FALL_DAMAGE_MULTIPLIER));
//
// The FALL_DAMAGE_IMMUNE guard is kept as a constant-false branch: players are NOT in the
// FALL_DAMAGE_IMMUNE tag, so vanilla's `is(...)` returns false and falls through to the formula.
// Modeling it as `if fallDamageImmune { return 0 }` preserves the method's structure so a future
// per-entity tag lookup slots in here unchanged. The product keeps power, damageMultiplier and
// the FALL_DAMAGE_MULTIPLIER attribute as three explicit factors, exactly as the bytecode's two
// `dmul`s do.
func calculateFallDamage(d float64, damageMultiplier float64, safeFallDistance float64) int {
	const fallDamageImmune = false // players are not in the EntityTypeTags.FALL_DAMAGE_IMMUNE tag
	if fallDamageImmune {
		return 0
	}
	power := calculateFallPower(d, safeFallDistance)
	return mthFloor(power * damageMultiplier * fallDamageMultiplierAttr)
}

// doCheckFallDamage mirrors Entity.doCheckFallDamage(double dx, double dy, double dz, boolean onGround)
// - the EXACT vanilla call site is ServerGamePacketListenerImpl.handleMovePlayer, which runs it ONCE
// PER MOVEMENT PACKET with dy = (this packet's y) - (the previous packet's y) and onGround = THAT
// packet's onGround flag (bytecode 1146-1186: setOnGroundWithMovement then doCheckFallDamage(dx,dy,dz,
// packet.isOnGround)). Sulfur mirrors that per-packet placement: applyInput calls this from the two
// position-changing ServerboundMovePlayer* variants, passing dy = ny - oldY computed against the
// pre-move y (the identical per-packet delta detectJumpExhaustion already uses).
//
// PER-PACKET is load-bearing, not cosmetic. Vanilla's fallDistance accumulation AND its landing reset
// happen on every packet, so a player stepping down over uneven terrain gets fallDistance RESET on
// each grounded packet and never accumulates a spurious multi-step fall. The previous per-TICK
// collapse (deltaY = p.y - p.lastY, one checkFallDamage per tick over the LAST surviving onGround)
// dropped the intermediate grounded resets when several packets batched into one tick - so a series
// of harmless small hops accumulated into one lethal-looking fall (the "too sensitive") and only
// discharged when a tick finally ended grounded (the "arrives late"). Running the check per packet
// removes both divergences and is the literal vanilla placement.
//
// Entity.doCheckFallDamage's touchingUnloadedChunk early-out is not modeled (the player's own column
// is loaded by definition here). isInWater is sampled per packet at the just-updated position, so the
// !isInWater() accumulation guard is faithful. Runs on the tick goroutine (applyInput is drained on
// the owner in resolveSubtickInputs) - no locking.
func (t *TickLoop) doCheckFallDamage(p *tickPlayer, dy float64, onGround bool) {
	if p == nil || p.dead {
		return
	}
	inWater := t.playerInWater(p)
	t.checkFallDamage(p, dy, onGround, inWater)
}

// tickFallDamage is the per-tick Entity.updateFluidInteraction water reset (GAMEPLAY-04 / 17-08).
// The fall-distance ACCUMULATION + landing damage moved to the per-packet doCheckFallDamage (the
// vanilla handleMovePlayer placement); what remains here is the tick-scoped guard that mirrors
// Entity.updateFluidInteraction: vanilla calls resetFallDistance() every tick the entity is in water,
// zeroing any distance accumulated before entering the water so a fall INTO water deals no damage.
// This runs in tickEntities (the 17-01-wired call site; signature unchanged), BEFORE the next tick's
// input resolution, exactly as vanilla runs updateFluidInteraction in the entity tick - so by the
// time a submerged landing packet is processed next tick, fallDistance is already 0.
//
// The wasOnGround/lastY bookkeeping is retained for other readers (fall-flag combat gates, ultradebug
// vY) and kept current for dead/nil players so a respawn does not inherit a stale baseline.
func (t *TickLoop) tickFallDamage() {
	for _, p := range t.players {
		if p == nil || p.dead {
			if p != nil {
				p.wasOnGround = p.onGround
				p.lastY = p.y
			}
			continue
		}

		// Entity.updateFluidInteraction water reset (17-08): zero any fall distance while in water, no
		// damage. Reuses the 17-02 AABB water-intersection check (fluid_physics.go) - NOT reimplemented.
		if t.playerInWater(p) {
			p.resetFallDistance()
		}

		// Bookkeeping for the fall-flag combat gates / ultradebug vY (they read lastY/wasOnGround). The
		// per-packet doCheckFallDamage owns the accumulation; these fields are no longer its baseline.
		p.wasOnGround = p.onGround
		p.lastY = p.y
	}
}

// tickBelowWorld ports Entity.checkBelowWorld for a player: `if (getY() < level.getMinY() - 64)
// onBelowWorld()`, and LivingEntity.onBelowWorld = `hurt(fellOutOfWorld(), 4.0F)`. Below the void
// threshold the player takes 4.0 out_of_world damage per tick until it dies. out_of_world is a
// bypasses_invulnerability source, so it kills even a creative player (matching vanilla — you fall
// forever in creative only because the client stops you; server-authoritatively the void still bites).
// The threshold uses the player's own dimension minY (dimMinYFor). Sibling of tickFallDamage/tickBreath;
// runs in tickEntities. Cite Entity.checkBelowWorld / LivingEntity.onBelowWorld.
func (t *TickLoop) tickBelowWorld() {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if p.y < float64(dimMinYFor(playerDimOr(p))-64) {
			t.applyDamage(p, damageSourceOf(damageTypeOutOfWorld), 4.0)
		}
	}
}

// tickLavaPlayers ports the lava-in-block effects (LavaFluid.entityInside) for a player: while the
// player is in lava, ignite it for 15s (lavaIgnite) and deal 4.0 lava damage/tick (lavaHurt), and halve
// fallDistance (baseTick `isInLava -> fallDistance *= 0.5`). Sibling of tickBelowWorld; runs in
// tickEntities. The 4.0 damage is fully faithful; the player fire flag is set on the player's store
// Entity so trackers render flames (the ongoing player fire-BURN after leaving lava is a pre-existing
// v1 deferral — there is no player-side fire tick — but the lava damage while submerged is complete).
// Cite Entity.lavaIgnite / Entity.lavaHurt / Entity.baseTick.
func (t *TickLoop) tickLavaPlayers() {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if !t.playerInLava(p) {
			continue
		}
		if p.playerEntity != nil {
			t.igniteForSeconds(p.playerEntity, 15.0) // lavaIgnite: set the flame flag on the store entity
		}
		t.applyDamage(p, damageSourceOf(damageTypeLava), 4.0) // lavaHurt: hurt(lava, 4.0)
		p.fallDistance *= 0.5                                 // baseTick: fallDistance halved in lava
	}
}

// checkFallDamage mirrors Entity.checkFallDamage(double deltaY, boolean onGround, BlockState, BlockPos):
//
//	if (!isInWater() && deltaY < 0.0) this.fallDistance -= (double)(float) deltaY;
//	if (onGround) {
//	    if (this.fallDistance > 0.0) causeFallDamage(this.fallDistance, 1.0F, DamageSource.FALL);
//	    this.resetFallDistance();
//	}
//
// inWater is t.playerInWater(p) (== !isInWater() guard). The BlockState/BlockPos args and the
// HIT_GROUND game event are cosmetic/world-side and omitted; Block.fallOn's default forwards
// damageMultiplier = 1.0 to causeFallDamage, which is passed literally here.
func (t *TickLoop) checkFallDamage(p *tickPlayer, deltaY float64, onGround bool, inWater bool) {
	if !inWater && deltaY < 0.0 {
		// d2f then f2d: the (float) narrowing cast widened back to double, ported verbatim.
		p.fallDistance -= float64(float32(deltaY))
	}
	if onGround {
		if p.fallDistance > 0.0 {
			// Entity.checkFallDamage -> the landing Block.fallOn dispatch picks the damageMultiplier:
			// most blocks default 1.0 (Entity.fallOn), HayBlock 0.2, BedBlock 0.5, SlimeBlock 0.0 (a
			// non-sneaking bounce cancels all fall damage). causeFallDamage is then called with it.
			t.causeFallDamage(p, p.fallDistance, t.fallOnMultiplierAt(p))
		}
		p.resetFallDistance()
	}
}

// fallOnMultiplierAt returns the Block.fallOn damageMultiplier for the block the player just landed on
// (getBlockStateOn: the block at floor(y - 0.2), the surface the feet rest on). HayBlock.fallOn scales by
// 0.2f, BedBlock.fallOn by 0.5, SlimeBlock.fallOn cancels damage (0.0) unless the entity isSuppressingBounce
// (a sneaking player -- v1 has no sneak decode, so a slime landing is the non-suppressing 0.0 common case,
// the sneak-restores-damage guard being the cited deferral). Every other block is the Entity.fallOn default
// 1.0. Cite HayBlock.fallOn (0.2f) + BedBlock.fallOn (*0.5) + SlimeBlock.fallOn (0.0f, !isSuppressingBounce).
func (t *TickLoop) fallOnMultiplierAt(p *tickPlayer) float64 {
	if t.world() == nil {
		return 1.0
	}
	// getBlockStateOn: the block just below the feet (y - 0.2 floored -> the supporting surface).
	bx := floorI(p.x)
	by := floorI(p.y - 0.2)
	bz := floorI(p.z)
	sid := t.blockStateAt(bx, by, bz)
	if int(sid) < 0 || int(sid) >= len(block.StateList) {
		return 1.0
	}
	switch block.StateList[sid].ID() {
	case "minecraft:hay_block":
		return 0.2 // HayBlock.fallOn: causeFallDamage(d, 0.2f, fall)
	case "minecraft:slime_block":
		return 0.0 // SlimeBlock.fallOn: !isSuppressingBounce -> causeFallDamage(d, 0.0f, fall)
	}
	if blockInTag(sid, blockTagBeds) {
		return 0.5 // BedBlock.fallOn: super.fallOn(..., d * 0.5) -- any colored bed (#minecraft:beds)
	}
	return 1.0
}

// causeFallDamage mirrors the ELSE (non-impulse) path of
// LivingEntity.causeFallDamage(double d, float damageMultiplier, DamageSource src):
//
//	int i = calculateFallDamage(d, damageMultiplier);
//	if (i > 0) { /* sounds */ this.hurt(src, (float) i); return true; }
//	return false;
//
// d passes through unchanged (the impulse branch is not modeled). hurt(src, (float)i) is Sulfur's
// applyDamage(p, float32(i)) — the same server-authoritative path attacks use. Sounds are skipped
// server-side. Returns whether damage was dealt (mirrors the method's boolean result).
func (t *TickLoop) causeFallDamage(p *tickPlayer, d float64, damageMultiplier float64) bool {
	// getAttributeValue(SAFE_FALL_DISTANCE): the live per-player attribute read (base 3.0), the value
	// LivingEntity.calculateFallPower subtracts. Read at the vanilla call site so a future modifier
	// (feather-falling-style effect) composes automatically.
	safeFallDistance := p.getAttributeValue(attrSafeFallDistance)
	i := calculateFallDamage(d, damageMultiplier, safeFallDistance)
	if i > 0 {
		t.applyDamage(p, damageSourceOf(damageTypeFall), float32(i))
		return true
	}
	return false
}
