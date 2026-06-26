package server

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// attack_dispatch.go — GAMEPLAY-04 (the PvP attack half). applyInput (subtick.go) routes a
// drained ServerboundAttack here; this resolves the NAMED target to a tickPlayer, reach-gates
// it server-side, and applies a SERVER-supplied damage amount into the existing, tested
// applyDamage->die flow (combat.go). The packet only NAMES a target — the client never claims
// a damage amount (T-6-05 / V4); the server is authoritative.
//
// JAR-VERIFIED WIRE LAYOUT (javap -c -p net.minecraft.network.protocol.game.ServerboundAttackPacket
// from temp/cache/26.2-inner.jar this session):
//
//	ServerboundAttackPacket = record(int entityId)
//	STREAM_CODEC = StreamCodec.composite(ByteBufCodecs.VAR_INT, …)  // a SINGLE VarInt entityId
//
// This is a 26.2 SHIFT the ≤1.21 docs miss: the old ServerboundInteractPacket carried an
// {INTERACT, ATTACK, INTERACT_AT} Action enum and ATTACK lived inside it. In 26.2 ATTACK was
// SPLIT into its own ServerboundAttackPacket whose entire body is the target entity id, while
// ServerboundInteractPacket is now ONLY the right-click interact
// (entityId + InteractionHand + Vec3 location + Boolean usingSecondaryAction — NO Action enum,
// jar-verified). So the entity ATTACK is carried by ServerboundAttack, and ServerboundInteract
// is NOT an attack — handleInteract treats it as a non-damaging interaction no-op for v1
// (right-click-on-entity actions are a later concern).

// attackReach is the server-authoritative max distance (blocks, player-center to player-center)
// at which a melee attack can land. Vanilla survival entity-interaction reach is ~3.0 blocks
// (Attributes.ENTITY_INTERACTION_RANGE base = 3.0 in the jar); a small slack absorbs the
// position jitter between the client's hit-frame and the server's tick-sampled position so a
// legitimate just-in-range hit is not falsely rejected. A target beyond this is a silent no-op
// (the reach gate, mirroring withinReach for blocks — T-6-01). Kept distinct from blockReach
// (6.0) because entity reach is shorter than block reach in vanilla.
const attackReach = 3.0 + 0.5

// ============================================================================================
// MELEE ATTACK 1:1 PORT (Plan 17-11) — LITERAL PORT of
// net.minecraft.world.entity.player.Player.attack(Entity), verified against
// temp/cache/26.2-inner.jar via `javap -c -p` this session. No GPL source is pasted; the
// structure, ordering, float casts and exact constants are IDENTICAL to the bytecode.
//
// degToRad is Player.attack's `0.017453292F` (the float π/180 used to convert the yaw to radians
// for the knockback direction). Ported verbatim as the same single-precision constant.
const degToRad float32 = 0.017453292

// attackStrengthScaleArg is the `0.5F` adjustTicks argument in Player.attack's
// `getAttackStrengthScale(0.5F)` — the half-tick lookahead so the scale reaches ~full at the
// expected delay boundary. Ported verbatim.
const attackStrengthScaleArg float32 = 0.5

// handleAttack resolves a ServerboundAttack on-tick: the entry point for the Player.attack(Entity)
// port. It decodes defensively (a Scan error is a silent no-op — never a panic, T-6-04 / V5),
// resolves the named target entity id to its owning tickPlayer (the 17-01 reverse lookup), rejects
// a nil/self/out-of-reach target silently, then runs the full vanilla melee sequence: base damage
// from the ATTACK_DAMAGE attribute, the attack-strength cooldown ramp, the sprint-knockback sound
// gate, the critical-hit ×1.5, the hurtServer i-frame-gated damage application, knockback, the
// sweep attack, and food exhaustion. Runs on the tick goroutine over tick-owned state (TICK-05).
//
// Faithful bytecode trace of Player.attack (the v1-relevant slice; world/item/enchant/projectile
// branches noted as stubs):
//
//	if (cannotAttack(target)) return;                              // v1: only reach/self guard below
//	float damage = (float) getAttributeValue(ATTACK_DAMAGE);      // (autoSpinAttack branch: stub false)
//	float scale = getAttackStrengthScale(0.5F);
//	float enchBonus = (getEnchantedDamage(...) - damage) * scale; // v1: getEnchantedDamage == damage -> 0
//	damage *= baseDamageScaleFactor();                            // 0.2F + scale*scale*0.8F
//	onAttack();                                                   // stub
//	if (deflectProjectile(target)) return;                        // stub false
//	if (damage > 0.0F || enchBonus > 0.0F) {
//	    boolean fullStrength = scale > 0.9F;                      // crit gate + sweep gate
//	    boolean sprintKb = isSprinting() && fullStrength;         // sprint knockback (+sound)
//	    damage += weapon.getItem().getAttackDamageBonus(...);     // v1: 0 (bare hand)
//	    boolean crit = fullStrength && canCriticalAttack(target);
//	    if (crit) damage *= 1.5F;
//	    float total = damage + enchBonus;                         // enchBonus 0 in v1
//	    boolean sweep = isSweepAttack(fullStrength, crit, sprintKb);
//	    float targetHealth = (target instanceof LivingEntity) ? target.getHealth() : 0;
//	    Vec3 targetDelta = target.getDeltaMovement();
//	    boolean hurt = target.hurtOrSimulate(source, total);     // -> LivingEntity.hurtServer
//	    if (hurt) {
//	        causeExtraKnockback(target, getKnockback(target, source) + (sprintKb ? 0.5F : 0),
//	                            targetDelta, source, total, true);
//	        if (sweep) doSweepAttack(target, damage, source, scale);
//	        // attackVisualEffects / setLastHurtMob / itemAttackInteraction / damageStatsAndHearts: stubs
//	        causeFoodExhaustion(0.1F);
//	    }
//	}
func (t *TickLoop) handleAttack(p *tickPlayer, pkt pk.Packet) {
	var targetID pk.VarInt
	if err := pkt.Scan(&targetID); err != nil {
		return // malformed/short payload: no-op, never panic (defensive decode)
	}

	victim := t.lookupPlayerByEntityID(int32(targetID))
	if victim == nil || victim == p {
		return // forged/unknown target, or a self-attack: cannotAttack -> silent no-op
	}

	// Server-authoritative reach gate (T-6-01): reject an out-of-range target silently. This is the
	// v1 stand-in for the client-side reach check inside cannotAttack; the client cannot melee
	// across the map even if it names a valid entity id.
	if !t.withinAttackReach(p, victim) {
		return
	}

	// base damage = (float) getAttributeValue(ATTACK_DAMAGE). The autoSpinAttack branch (riptide
	// trident) is a constant-false stub in v1 — players never auto-spin, so the ATTACK_DAMAGE path
	// is the faithful one. The d2f narrowing cast is applied here exactly where the bytecode does.
	damage := float32(p.getAttributeValue(attrAttackDamage))

	// float scale = getAttackStrengthScale(0.5F) — the cooldown ramp position (0..1). Read with the
	// CURRENT attackStrengthTicker (BEFORE the swing reset below), exactly as vanilla: attack()
	// computes the scale, and ServerPlayer.swing() — sent alongside the attack — resets the ticker.
	// Sulfur routes the attack here and ServerboundSwing is a no-op, so the swing reset is performed
	// at the END of this handler (resetAttackStrengthTicker), AFTER the scale is read.
	scale := p.getAttackStrengthScale(attackStrengthScaleArg)

	// enchBonus = (getEnchantedDamage(target, damage, source) - damage) * scale. v1 has no
	// enchantments, so getEnchantedDamage returns damage unchanged -> the difference is 0 ->
	// enchBonus is 0. Kept as an explicit term so an enchantment port slots in here unchanged.
	const enchantedDamage = float32(0.0) // getEnchantedDamage(...) - damage == 0 in v1
	enchBonus := enchantedDamage * scale

	// damage *= baseDamageScaleFactor() == 0.2F + scale*scale*0.8F — the attack-strength damage
	// ramp: a just-attacked swing (scale~0) deals ~0.2x, a fully recharged swing (scale==1) 1.0x.
	damage *= p.baseDamageScaleFactor()

	// onAttack() / deflectProjectile(target): stubs (no statistics, no projectile deflection in v1).

	// ServerPlayer.swing() reset: the swing that accompanies this attack resets the attack-strength
	// ticker to 0 so the NEXT swing starts from the 0.2x floor. Vanilla does this on the swing
	// packet (sent with the attack); since the scale was already read above, performing it here —
	// before the early-return branches — matches vanilla's "every swing resets the ticker" (a swing
	// happens even when the attack deals no damage). resetAttackStrengthTicker is idempotent and
	// tick-owned.
	p.resetAttackStrengthTicker()

	// `if (damage > 0.0F || enchBonus > 0.0F)` — skip the whole hit if there is nothing to deal.
	if !(damage > 0.0 || enchBonus > 0.0) {
		// PLAYER_ATTACK_NODAMAGE sound: server-side no-op (sounds not modeled). postPiercingAttack: stub.
		return
	}

	// boolean fullStrength = scale > 0.9F — gates both the crit and the sweep.
	fullStrength := scale > 0.9

	// boolean sprintKb = isSprinting() && fullStrength — the sprint-knockback branch (also plays
	// PLAYER_ATTACK_KNOCKBACK, a server-side no-op here). p.sprinting is a faithful stub (false in
	// v1 until a sprint-flag decode lands), so sprintKb is false today.
	sprintKb := p.sprinting && fullStrength

	// damage += weapon.getItem().getAttackDamageBonus(...): bare-hand is 0 in v1 (no weapon item),
	// so the add is a no-op — kept as a documented stub for when items arrive.

	// boolean crit = fullStrength && canCriticalAttack(target); if (crit) damage *= 1.5F.
	crit := fullStrength && t.canCriticalAttack(p, victim)
	if crit {
		damage *= 1.5
	}

	// float total = damage + enchBonus (enchBonus 0 in v1) — the amount passed to hurtServer.
	total := damage + enchBonus

	// boolean sweep = isSweepAttack(fullStrength, crit, sprintKb).
	sweep := t.isSweepAttack(p, fullStrength, crit, sprintKb)

	// target.hurtOrSimulate(source, total) -> LivingEntity.hurtServer: the i-frame-gated damage
	// application. applyDamage IS the hurtServer port (combat.go): it enforces invulnerableTime,
	// applies armor/absorption via actuallyHurt, sends SetHealth, and drives die() if lethal. It
	// returns whether damage actually landed (false during the i-frame window with no greater hit),
	// which gates the knockback/sweep/exhaustion tail exactly as vanilla's `if (hurt)` does.
	hurt := t.applyAttackDamage(victim, total)
	if !hurt {
		return // hurtOrSimulate returned false (i-frame window absorbed it): no knockback/sweep/exhaustion
	}

	// causeExtraKnockback(target, getKnockback(target, source) + (sprintKb ? 0.5F : 0.0F),
	//                     targetDelta, source, total, true).
	extraKb := t.getKnockback(victim)
	if sprintKb {
		extraKb += 0.5
	}
	t.causeExtraKnockback(p, victim, extraKb)

	// if (sweep) doSweepAttack(target, damage, source, scale).
	if sweep {
		t.doSweepAttack(p, victim, damage, scale)
	}

	// attackVisualEffects / setLastHurtMob / itemAttackInteraction / damageStatsAndHearts: v1 stubs
	// (no crit particles, no mob-attribution, no item-on-hit, no stats yet).

	// causeFoodExhaustion(0.1F) — the attack costs hunger. Ported below; v1 applies it to the
	// attacker's food/saturation faithfully.
	t.causeFoodExhaustion(p, 0.1)

	// postPiercingAttack(): stub (no multi-hit piercing weapon in v1).
}

// applyAttackDamage is the hurtOrSimulate(source, amount) -> LivingEntity.hurtServer bridge used by
// the attack path: it must return whether damage ACTUALLY landed so the knockback/sweep/exhaustion
// tail is gated exactly as vanilla's `if (hurt)`. applyDamage (the hurtServer port) is void in
// Sulfur, so this wrapper snapshots the i-frame state to decide the boolean the same way hurtServer
// computes its return value:
//   - a dead target: hurtServer returns false (isDeadOrDying guard);
//   - inside the upper i-frame window ((float) invulnerableTime > 10.0F) with a non-greater hit:
//     hurtServer returns false (the `if (amount <= lastHurt) return false` branch);
//   - otherwise damage lands and hurtServer returns true.
// The decision is computed BEFORE calling applyDamage (which mutates invulnerableTime/lastHurt), so
// it reflects the same pre-hit state vanilla branches on.
func (t *TickLoop) applyAttackDamage(victim *tickPlayer, amount float32) bool {
	if victim.dead {
		return false // isDeadOrDying() -> hurtServer returns false
	}
	// Mirror hurtServer's `if (amount < 0.0F) amount = 0.0F;` for the gate decision (applyDamage
	// re-applies it internally; here it only affects the <= lastHurt comparison).
	if amount < 0.0 {
		amount = 0.0
	}
	landed := true
	if float32(victim.invulnerableTime) > hurtCooldownConst {
		// Inside the upper grace window: only a STRICTLY greater hit lands (the excess). A
		// non-greater hit is fully absorbed -> hurtServer returns false.
		landed = amount > victim.lastHurt
	}
	t.applyDamage(victim, amount)
	return landed
}

// getAttackStrengthScale is the port of LivingEntity.getAttackStrengthScale(float adjustTicks):
//
//	return Mth.clamp((attackStrengthTicker + adjustTicks) / getCurrentItemAttackStrengthDelay(),
//	                 0.0F, 1.0F);
//
// It is the cooldown ramp position: 0 just after an attack (attackStrengthTicker reset to 0),
// climbing to 1 once attackStrengthTicker + adjustTicks reaches the delay. The int ticker is
// widened to float (i2f) before the add, exactly as the bytecode does.
func (p *tickPlayer) getAttackStrengthScale(adjustTicks float32) float32 {
	return mthClampF((float32(p.attackStrengthTicker)+adjustTicks)/p.getCurrentItemAttackStrengthDelay(), 0.0, 1.0)
}

// getCurrentItemAttackStrengthDelay is the port of Player.getCurrentItemAttackStrengthDelay():
//
//	return (float) (1.0 / getAttributeValue(ATTACK_SPEED) * 20.0);
//
// The whole expression is computed in double precision then narrowed to float (d2f), matching the
// bytecode (dconst_1 ... ddiv ldc2_w 20.0d dmul d2f). With ATTACK_SPEED base 4.0 this is
// (1.0/4.0)*20.0 = 5.0 ticks — the bare-hand attack cooldown.
func (p *tickPlayer) getCurrentItemAttackStrengthDelay() float32 {
	return float32(1.0 / p.getAttributeValue(attrAttackSpeed) * 20.0)
}

// baseDamageScaleFactor is the port of Player.baseDamageScaleFactor():
//
//	float scale = getAttackStrengthScale(0.5F);
//	return 0.2F + scale * scale * 0.8F;
//
// The attack-strength damage multiplier: 0.2 (just attacked) ramping to 1.0 (fully recharged).
func (p *tickPlayer) baseDamageScaleFactor() float32 {
	scale := p.getAttackStrengthScale(attackStrengthScaleArg)
	return 0.2 + scale*scale*0.8
}

// canCriticalAttack is the port of Player.canCriticalAttack(Entity):
//
//	return fallDistance > 0.0 && !onGround() && !onClimbable() && !isInWater()
//	       && !isMobilityRestricted() && !isPassenger()
//	       && target instanceof LivingEntity && !isSprinting();
//
// In v1 the meaningful, tracked conditions are fallDistance>0, !onGround, !isInWater (water via the
// 17-02 AABB check) and target-is-a-player (all players are LivingEntity). onClimbable /
// isMobilityRestricted / isPassenger are faithful constant-false stubs (no ladders/mounts/restraint
// state yet) — they widen the crit window only in their absence, and are documented so a future
// port closes them with no formula change. isSprinting() is the p.sprinting stub (false in v1).
func (t *TickLoop) canCriticalAttack(p *tickPlayer, victim *tickPlayer) bool {
	const onClimbable = false        // no ladder/vine climb state in v1
	const isMobilityRestricted = false // no use-item/sleep restraint state in v1
	const isPassenger = false        // no mounts in v1
	const targetIsLivingEntity = true // a victim tickPlayer is always a LivingEntity
	return p.fallDistance > 0.0 &&
		!p.onGround &&
		!onClimbable &&
		!t.playerInWater(p) &&
		!isMobilityRestricted &&
		!isPassenger &&
		targetIsLivingEntity &&
		!p.sprinting
}

// isSweepAttack is the port of Player.isSweepAttack(boolean fullStrength, boolean crit, boolean
// sprintKb):
//
//	if (fullStrength && !crit && !sprintKb && onGround()) {
//	    double moveSqr = getKnownMovement().horizontalDistanceSqr();
//	    double threshold = (double) getSpeed() * 2.5;
//	    if (moveSqr < Mth.square(threshold))
//	        return getItemInHand(MAIN_HAND).is(ItemTags.SWORDS);
//	}
//	return false;
//
// v1 has no sword items (the SWORDS-tag check is a constant-false stub: a bare hand is never a
// sword), so isSweepAttack is always false today — the FULL gate structure is preserved (the
// fullStrength/crit/sprintKb/onGround/movement-threshold checks) so a sword-item port flips only the
// final tag check. getKnownMovement/getSpeed are not yet modeled, so the movement-threshold branch
// is documented but unreached (the sword stub short-circuits to false first).
func (t *TickLoop) isSweepAttack(p *tickPlayer, fullStrength, crit, sprintKb bool) bool {
	const holdingSword = false // no sword items in v1: getItemInHand(MAIN_HAND).is(SWORDS) == false
	if fullStrength && !crit && !sprintKb && p.onGround {
		// The movement-threshold gate (moveSqr < (getSpeed()*2.5)^2) precedes the sword check in
		// vanilla; with no velocity/speed model wired it cannot be evaluated faithfully, and the
		// sword stub is false regardless, so the whole branch resolves to false. Documented here so
		// the gate slots in once knownMovement/speed exist.
		return holdingSword
	}
	return false
}

// getKnockback is the port of LivingEntity.getKnockback(Entity, DamageSource):
//
//	float f = (float) getAttributeValue(ATTACK_KNOCKBACK);
//	// (ServerLevel enchant modifyKnockback branch -> v1: f unchanged, no enchantments)
//	return f / 2.0F;
//
// ATTACK_KNOCKBACK base is 0.0 for a player, so this returns 0.0 in v1 — the base knockback is
// entirely from the sprint bonus and the knockback() impulse. The /2.0F and the d2f cast are
// ported verbatim so a future ATTACK_KNOCKBACK modifier (e.g. the Knockback enchant) reads through.
func (t *TickLoop) getKnockback(victim *tickPlayer) float32 {
	f := float32(victim.getAttributeValue(attrAttackKnockback))
	// EnchantmentHelper.modifyKnockback: v1 has no enchantments -> f unchanged.
	return f / 2.0
}

// causeExtraKnockback is the port of the LivingEntity-target path of
// Player.causeExtraKnockback(Entity, float strength, Vec3 targetDelta, DamageSource, float damage,
// boolean alwaysApplyKnockback):
//
//	if (strength > 0.0F) {
//	    // target instanceof LivingEntity:
//	    ((LivingEntity) target).knockback((double) strength,
//	        (double) Mth.sin(getYRot() * 0.017453292F),
//	        (double) -Mth.cos(getYRot() * 0.017453292F),
//	        source, damage);
//	    // (attacker delta-movement *0.6/1/0.6 and setSprinting(false): the ATTACKER's recoil)
//	}
//	// (ServerPlayer target: send SetEntityMotion, reset hurtMarked)
//
// The knockback DIRECTION is the ATTACKER's yaw: dx = sin(yaw·π/180), dz = -cos(yaw·π/180) — i.e.
// the target is pushed away along the direction the attacker faces. The attacker-recoil delta and
// the setSprinting(false) are documented stubs (no attacker delta-movement model / sprint flag in
// v1). The strength>0 guard is preserved: with ATTACK_KNOCKBACK 0 and no sprint, strength is 0 and
// NO knockback is applied — exactly vanilla.
func (t *TickLoop) causeExtraKnockback(attacker, victim *tickPlayer, strength float32) {
	if strength > 0.0 {
		dx := float64(float32(math.Sin(float64(attacker.yaw * degToRad))))
		dz := float64(-float32(math.Cos(float64(attacker.yaw * degToRad))))
		t.knockback(victim, float64(strength), dx, dz)
	}
	// Attacker recoil (deltaMovement *0.6/1/0.6) + setSprinting(false): v1 stubs (no attacker
	// velocity model / sprint flag). The ServerPlayer-target SetEntityMotion send happens inside
	// knockback() below (where the victim's velocity actually changes), so the victim's client sees
	// the impulse.
}

// knockback is the port of LivingEntity.knockback(double strength, double dx, double dz,
// DamageSource, float damage) -> knockback(double, double, double, DamageSource, float, boolean
// false). The 6-arg core:
//
//	strength *= 1.0 - getAttributeValue(KNOCKBACK_RESISTANCE);
//	if (strength <= 0.0) return;
//	Vec3 cur = getDeltaMovement();
//	// (degenerate dx²+dz² < 1e-5 random jitter loop: skipped — our dx/dz are unit-ish from yaw)
//	Vec3 impulse = new Vec3(dx, 0.0, dz).normalize().scale(strength);
//	setDeltaMovement(
//	    cur.x / 2.0 - impulse.x,
//	    onGround() ? Math.min(0.4, cur.y / 2.0 + strength) : cur.y,
//	    cur.z / 2.0 - impulse.z);
//
// KNOCKBACK_RESISTANCE base is 0.0 for a player, so strength is unscaled in v1. The vertical
// component lifts the target (the familiar knockback "pop") when onGround. The victim's velocity is
// stored on its playerEntity (the store Entity the tracker syncs) and a SetEntityMotion is sent to
// the victim's own client so it sees the impulse (vanilla's ServerPlayer SetEntityMotion send).
func (t *TickLoop) knockback(victim *tickPlayer, strength, dx, dz float64) {
	strength *= 1.0 - victim.getAttributeValue(attrKnockbackResistance)
	if strength <= 0.0 {
		return
	}

	// cur = getDeltaMovement(). The victim's velocity lives on its store Entity (playerEntity); if
	// it is not yet wired (mid-registration), treat current velocity as zero — the impulse still
	// applies and is sent to the client.
	var curX, curY, curZ float64
	if victim.playerEntity != nil {
		curX, curY, curZ = victim.playerEntity.vx, victim.playerEntity.vy, victim.playerEntity.vz
	}

	// impulse = Vec3(dx, 0, dz).normalize().scale(strength). The horizontal direction is normalized
	// (so a yaw-derived (sin, -cos) of unit length stays unit) then scaled by strength. The
	// degenerate dx²+dz² < 1e-5 random-jitter loop is omitted: our (dx, dz) come from the attacker
	// yaw and are never both ~0 in the gated strength>0 melee path.
	ix, iz := normalizeHoriz(dx, dz)
	impulseX := ix * strength
	impulseZ := iz * strength

	newX := curX/2.0 - impulseX
	var newY float64
	if victim.onGround {
		newY = math.Min(0.4, curY/2.0+strength)
	} else {
		newY = curY
	}
	newZ := curZ/2.0 - impulseZ

	if victim.playerEntity != nil {
		victim.playerEntity.vx = newX
		victim.playerEntity.vy = newY
		victim.playerEntity.vz = newZ
		// ServerPlayer knockback: vanilla sends ClientboundSetEntityMotion to the knocked player so
		// its client applies the impulse (the player is the authority for its own motion, but the
		// server-driven knockback must be pushed). Send it to the victim's own client.
		if victim.client != nil {
			victim.client.Send(encodeSetEntityMotion(victim.playerEntity))
		}
	}
}

// normalizeHoriz mirrors Vec3.normalize() restricted to the horizontal plane for the knockback
// impulse: it returns (dx, dz) scaled to unit length, or (0, 0) for a (near-)zero vector (matching
// Vec3.normalize's `d < 1.0E-4 ? ZERO : this.scale(1/d)`). The y component is 0 by construction
// here (the impulse Vec3 is (dx, 0, dz)), so only the horizontal magnitude matters.
func normalizeHoriz(dx, dz float64) (float64, float64) {
	d := math.Sqrt(dx*dx + dz*dz)
	if d < 1.0e-4 {
		return 0, 0
	}
	return dx / d, dz / d
}

// doSweepAttack is the port of Player.doSweepAttack(Entity target, float damage, DamageSource,
// float scale). It scans for additional LivingEntity targets within a 1-block-inflated box around
// the primary target and deals the sweep damage to each in a 3-block range, then knocks them back.
//
// Faithful bytecode trace (the v1-relevant slice):
//
//	float sweepDamage = 1.0F + (float) getAttributeValue(SWEEPING_DAMAGE_RATIO) * damage;
//	for (LivingEntity e : level.getEntitiesOfClass(LivingEntity,
//	        target.getBoundingBox().inflate(1.0, 0.25, 1.0))) {
//	    if (e == this || e == target) continue;
//	    if (isAlliedTo(e)) continue;
//	    if (e instanceof ArmorStand && ((ArmorStand) e).isMarker()) continue;
//	    if (distanceToSqr(e) < 9.0) {
//	        float d = getEnchantedDamage(e, sweepDamage, source) * scale;  // v1: getEnchantedDamage == sweepDamage
//	        if (e.hurtServer(serverLevel, source, d)) {
//	            e.knockback(0.4, sin(getYRot()·π/180), -cos(getYRot()·π/180), source, d);
//	        }
//	    }
//	}
//
// SWEEPING_DAMAGE_RATIO base is 0.0 in v1 (no sweeping-edge enchant), so sweepDamage == 1.0F. The
// per-target distanceToSqr < 9.0 (3-block radius) and the inflate(1.0, 0.25, 1.0) box are ported
// verbatim. The sweep targets are other PLAYERS in range (v1's LivingEntity population); each is
// hurt via applyDamage (the hurtServer port) and knocked back with the 0.4 vertical sweep impulse.
func (t *TickLoop) doSweepAttack(attacker, primary *tickPlayer, damage, scale float32) {
	// sweepDamage = 1.0F + SWEEPING_DAMAGE_RATIO * damage. With ratio 0.0, sweepDamage == 1.0.
	sweepDamage := 1.0 + float32(attacker.getAttributeValue(attrSweepingDamageRatio))*damage

	for _, e := range t.players {
		if e == attacker || e == primary {
			continue // never sweep self or the already-struck primary target
		}
		if e.dead {
			continue // a corpse is not a sweep target
		}
		// isAlliedTo(e): v1 has no teams -> always false (no ally to skip). ArmorStand-marker
		// skip: v1 has no armor stands. Both are documented stubs — every other player is a target.

		// The inflate(1.0, 0.25, 1.0) box around the primary plus distanceToSqr(e) < 9.0 collapse
		// to: the candidate must be within the 3-block sweep radius of the ATTACKER (vanilla uses
		// distanceToSqr from `this`). Sulfur has no AABB getEntitiesOfClass for players, so we scan
		// the player list and apply the exact distanceToSqr < 9.0 gate from the attacker — the same
		// set vanilla's box-then-distance filter yields for the player population.
		dx := e.x - attacker.x
		dy := e.y - attacker.y
		dz := e.z - attacker.z
		if dx*dx+dy*dy+dz*dz >= 9.0 {
			continue
		}

		// float d = getEnchantedDamage(e, sweepDamage, source) * scale. v1: getEnchantedDamage
		// returns sweepDamage unchanged -> d = sweepDamage * scale.
		d := sweepDamage * scale

		// e.hurtServer(...) -> applyAttackDamage so the per-target knockback is gated on the hit
		// landing (vanilla's `if (e.hurtServer(...))`).
		if t.applyAttackDamage(e, d) {
			// e.knockback(0.4, sin(yaw·π/180), -cos(yaw·π/180), source, d) — the lighter 0.4 sweep
			// impulse along the attacker's facing.
			kdx := float64(float32(math.Sin(float64(attacker.yaw * degToRad))))
			kdz := float64(-float32(math.Cos(float64(attacker.yaw * degToRad))))
			t.knockback(e, 0.4, kdx, kdz)
		}
	}
}

// causeFoodExhaustion is the port of net.minecraft.world.entity.player.Player.causeFoodExhaustion(
// float exhaustion):
//
//	if (abilities.invulnerable) return;
//	if (!level.isClientSide()) foodData.addExhaustion(exhaustion);
//
// Plan 17-19 makes the accumulation REAL: FoodData now has an exhaustion accumulator
// (tickPlayer.exhaustion / addExhaustion in food.go), so this routes the exhaustion into it. The
// invulnerable guard is preserved (a CITED stub == false, creative/invuln abilities not wired in
// v1). The server is NEVER client-side, so the `!level.isClientSide()` guard is always true — the
// addExhaustion always runs. This is the single entry point for ALL exhaustion sources: the melee
// attack (causeFoodExhaustion(0.1) in handleAttack), the actuallyHurt damage tail (combat.go), and
// the movement ladder (checkMovementStatistics in food.go).
func (t *TickLoop) causeFoodExhaustion(p *tickPlayer, exhaustion float32) {
	const invulnerable = false // Player.abilities.invulnerable: creative/invuln not wired in v1 (CITED stub)
	if invulnerable {
		return
	}
	// !level.isClientSide() is always true on the server: foodData.addExhaustion(exhaustion).
	p.addExhaustion(exhaustion)
}

// withinAttackReach reports whether the victim is close enough to the attacker for a melee
// hit. Euclidean distance between the two players' positions, compared against attackReach.
// Squared-distance comparison avoids a sqrt. The server-authoritative entity reach gate
// (T-6-01) — mirrors withinReach (block edits) but with the shorter entity-interaction reach.
func (t *TickLoop) withinAttackReach(attacker, victim *tickPlayer) bool {
	dx := victim.x - attacker.x
	dy := victim.y - attacker.y
	dz := victim.z - attacker.z
	return dx*dx+dy*dy+dz*dz <= attackReach*attackReach
}

// handleInteract resolves a ServerboundInteract on-tick. In 26.2 this packet is the RIGHT-CLICK
// entity interaction (entityId + InteractionHand + Vec3 location + Boolean usingSecondaryAction
// — jar-verified, NO Action enum; ATTACK is a separate ServerboundAttack handled by
// handleAttack). v1 has no entity right-click behavior (mounting, trading, leashing, etc.), so
// this is a defensive-decode no-op: it never deals damage (only ServerboundAttack does). Kept
// as an explicit handler so the wire is consumed deliberately rather than via the default drop,
// and so a future plan can attach interaction behavior here. Decoding is best-effort; a
// malformed payload is simply ignored.
func (t *TickLoop) handleInteract(p *tickPlayer, pkt pk.Packet) {
	// No-op for v1. Intentionally does NOT damage: per the jar split, ServerboundInteract is
	// the right-click interaction, never the attack (T-6-05 — only the server-driven attack
	// path deals damage). Left as a named seam for future entity-interaction behavior.
	_ = p
	_ = pkt
}
