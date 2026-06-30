package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/component"
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
	if victim == nil {
		// DUAL-RESOLVE (Phase-29, MOB-SUB-01): the named id is not a player. It may be a Go-native MOB
		// owned by some region — route to the Wave-2 applyDamageEntity hurt pipeline (same-region
		// synchronous, cross-region via the owner barrier-queue). A non-player, non-mob id falls through
		// to a silent no-op. handleMobAttack mirrors the player path below exactly (same reach gate, same
		// ATTACK_DAMAGE × scale × crit math, the same hurtOrSimulate boolean tail).
		t.handleMobAttack(p, int32(targetID))
		return
	}
	if victim == p {
		return // a self-attack: cannotAttack -> silent no-op
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
	hurt := t.applyAttackDamage(victim, p.entityID, total)
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

// handleMobAttack is the MOB-victim arm of the handleAttack dual-resolve (Phase-29, MOB-SUB-01): a
// player hit a Go-native mob (the named id is not a player). It is a FAITHFUL MIRROR of the player
// branch of handleAttack — the SAME Player.attack(Entity) bytecode trace — differing ONLY in (a) the
// victim is an *Entity resolved through its OWNING region (owningRegion, NEVER cur(): the cross-region
// victim must never fall to region 0 — Pitfall 2 / T-29-02), (b) the reach gate measures player→mob
// distance, and (c) the hurtOrSimulate application routes to applyDamageEntity (the LivingEntity
// .hurtServer port) either SYNCHRONOUSLY (same region — the fast path) or via the barrier-queue
// (cross region — queueDamageIntent drained by applyCrossRegionDamage).
//
// Because Player.attack calls target.hurtOrSimulate(source, total) for ANY LivingEntity target (javap:
// bytecode 242 → LivingEntity.hurtServer at 184), the SAME ATTACK_DAMAGE × strength-scale × crit math
// applies to a mob target as to a player target — so the damage computation is shared verbatim with the
// player branch via the inlined sequence below (kept identical so a divergence is impossible).
func (t *TickLoop) handleMobAttack(p *tickPlayer, targetID int32) {
	// Resolve the victim through its OWNING region (re-resolve by id, O(regionCount), nil if gone). NEVER
	// cur() — a cross-region victim resolved through cur() would silently fall to region 0 (the trap the
	// strictRegion gate guards). A forged/despawned id yields a nil owner -> a silent no-op (T-29-01).
	ownerRegion := t.owningRegion(targetID)
	if ownerRegion == nil {
		return
	}
	mob, ok := ownerRegion.entities.get(targetID)
	if !ok {
		return // owningRegion confirmed it, but stay defensive
	}

	// Server-authoritative reach gate (T-29-01): reject an out-of-reach mob silently. A client cannot
	// melee a mob across the map even if it names a valid mob id — the SAME entity-interaction reach the
	// player branch enforces (attackReach), measured player-center to mob-position.
	if !t.withinAttackReachEntity(p, mob) {
		return
	}

	// --- The SHARED Player.attack(Entity) damage math (IDENTICAL to the player branch above) -----------
	// base damage = (float) getAttributeValue(ATTACK_DAMAGE) (the d2f narrowing cast).
	damage := float32(p.getAttributeValue(attrAttackDamage))
	// float scale = getAttackStrengthScale(0.5F) — read BEFORE the swing reset.
	scale := p.getAttackStrengthScale(attackStrengthScaleArg)
	// enchBonus = (getEnchantedDamage - damage) * scale == 0 in v1 (no enchantments).
	const enchantedDamage = float32(0.0)
	enchBonus := enchantedDamage * scale
	// damage *= baseDamageScaleFactor() == 0.2F + scale*scale*0.8F.
	damage *= p.baseDamageScaleFactor()
	// ServerPlayer.swing() resets the attack-strength ticker (every swing, even a no-damage one) — done
	// after the scale read, before the early returns, exactly as the player branch does.
	p.resetAttackStrengthTicker()
	// `if (damage > 0.0F || enchBonus > 0.0F)` — skip if nothing to deal.
	if !(damage > 0.0 || enchBonus > 0.0) {
		return
	}
	// boolean fullStrength = scale > 0.9F; boolean sprintKb = isSprinting() && fullStrength. sprintKb
	// feeds the knockback strength (+0.5) and the sweep gate in the player branch; the mob knockback +
	// sweep tail is the cited follow-on below, so it is computed for the faithful trace but not yet
	// consumed for a mob victim.
	fullStrength := scale > 0.9
	sprintKb := p.sprinting && fullStrength
	_ = sprintKb // consumed by the deferred mob-knockback/sweep tail (cited below)
	// boolean crit = fullStrength && canCriticalAttack(target); if (crit) damage *= 1.5F. The mob is a
	// LivingEntity (targetIsLivingEntity true, like a player victim), so canCriticalAttackEntity reuses
	// the SAME attacker-side conditions as canCriticalAttack (none depend on the victim beyond the
	// always-true LivingEntity check).
	crit := fullStrength && t.canCriticalAttackEntity(p)
	if crit {
		damage *= 1.5
	}
	// float total = damage + enchBonus (enchBonus 0 in v1).
	total := damage + enchBonus

	// src = DamageSources.playerAttack(player) — type player_attack, causingEntity = the attacker id
	// (javap DamageSources.playerAttack: DamageTypes.PLAYER_ATTACK + the player). The genuine ported
	// source so PanicGoal (P31) reads its tag and wolf anger (P36) its attacker.
	src := damageSourcePlayerAttack(p.entityID)

	// ROUTING (the FIRST true cross-region write — Pitfall 2). Resolve the ATTACKER's region by its
	// column (NEVER cur() — handleAttack runs on the dispatch goroutine with NO region registered, where
	// cur() would either fall to region 0 or, with strictRegion armed, PANIC). A same-region hit applies
	// SYNCHRONOUSLY (the fast path — dispatch runs on the coordinator BEFORE the fan-out, so the owner's
	// store is quiescent); a cross-region hit is queued on the ATTACKER's region tagged to the owner and
	// drained at the coordinator barrier (applyCrossRegionDamage).
	attackerRegion := t.regionForColumn(columnOf(p.x, p.z))
	if ownerRegion == attackerRegion {
		// SAME-region fast path: apply inline via the hurtOrSimulate bridge (gates the knockback tail on
		// the hit landing, exactly as the player branch gates on `if (hurt)`).
		if !t.applyMobAttackDamage(mob, src, total) {
			return // hurtOrSimulate returned false (i-frame window absorbed it): no knockback tail
		}
	} else {
		// CROSS-region: queue on the attacker's region (the actor's own slice — A3), tagged to the owner.
		// The hit is applied at the barrier; the knockback tail (which would mutate the cross-region
		// mob's store mid-tick) is INTENTIONALLY not run from the dispatch goroutine for a cross-region
		// victim — knockback of a cross-region mob is a barrier concern, deferred (cited stub below).
		t.queueDamageIntent(attackerRegion, ownerRegion.id, damageIntent{victimID: mob.id, src: src, amount: total})
		return
	}

	// causeExtraKnockback / doSweepAttack tail for a SAME-region mob victim: knockback of a mob is the
	// LivingEntity.knockback port over the mob's vx/vy/vz. The player-victim knockback (causeExtraKnockback)
	// is typed on *tickPlayer; a mob-target knockback is a thin sibling. For this plan the routing +
	// damage are the deliverable (MOB-SUB-01); the mob-knockback impulse + sweep-over-mobs are a cited
	// follow-on (no consumer reads a mob's post-hit velocity yet — the tracker syncs position, and the
	// mob hurt/death pipeline drives the gameplay). The hit LANDED (applyMobAttackDamage returned true),
	// damage + lastDamageSource + the on_damage emit all fired in applyDamageEntity. causeFoodExhaustion
	// for the attacker still applies (the attack costs the player hunger regardless of the victim type).

	// setLastHurtMob(target): the OWNER-SIDE attack bookkeeping (the formerly-stubbed setLastHurtMob from
	// the player branch, line 205) — record the mob this player just hit + the gameTime stamp. A tamed
	// wolf's OwnerHurtTargetGoal reads its OWNER's getLastHurtMob()/Timestamp() to retaliate against what
	// its owner is fighting (MOB-NEUT-01, Phase 36-01). Set ONLY on a landed same-region hit (the `if
	// (hurt)` tail, exactly where Player.attack calls setLastHurtMob). The cross-region victim returned
	// early above; recording the owner-side ref for a cross-region hit is the same barrier follow-on the
	// knockback tail defers (cited). PURE field writes, NO RNG — a non-wolf-owning player never feeds a
	// wolf goal, so this cannot perturb any oracle stream.
	//	[VERIFIED javap Player.attack: on the hurt branch, setLastHurtMob(target) -> lastHurtMob = target;
	//	 lastHurtMobTimestamp = tickCount.]
	p.lastHurtMob = mob.id
	p.lastHurtMobTimestamp = int32(t.gametime)

	t.causeFoodExhaustion(p, 0.1)
}

// canCriticalAttackEntity is canCriticalAttack for a MOB target — the port of Player.canCriticalAttack(
// Entity) where `target instanceof LivingEntity` is always true for a mob (a Pig is a LivingEntity), so
// it reduces to the attacker-side conditions, IDENTICAL to canCriticalAttack's player-victim form. Kept
// as a sibling (rather than overloading canCriticalAttack) so the mob path is explicit and the player
// path is untouched.
func (t *TickLoop) canCriticalAttackEntity(p *tickPlayer) bool {
	const onClimbable = false          // no ladder/vine climb state in v1
	const isMobilityRestricted = false // no use-item/sleep restraint state in v1
	const isPassenger = false          // no mounts in v1
	const targetIsLivingEntity = true  // a mob (Pig/...) is always a LivingEntity
	return p.fallDistance > 0.0 &&
		!p.onGround &&
		!onClimbable &&
		!t.playerInWater(p) &&
		!isMobilityRestricted &&
		!isPassenger &&
		targetIsLivingEntity &&
		!p.sprinting
}

// withinAttackReachEntity is withinAttackReach for a MOB target: the server-authoritative entity reach
// gate (T-29-01), squared-distance from the player position to the mob position vs attackReach. Mirrors
// withinAttackReach (player-vs-player) but the victim is an *Entity. A mob beyond reach is a silent
// no-op — a client cannot melee a mob across the map.
func (t *TickLoop) withinAttackReachEntity(attacker *tickPlayer, mob *Entity) bool {
	dx := mob.x - attacker.x
	dy := mob.y - attacker.y
	dz := mob.z - attacker.z
	return dx*dx+dy*dy+dz*dz <= attackReach*attackReach
}

// applyMobAttackDamage is the hurtOrSimulate(source, amount) -> LivingEntity.hurtServer bridge for a MOB
// victim — the *Entity sibling of applyAttackDamage. It returns whether damage ACTUALLY landed so the
// caller gates the knockback tail exactly as vanilla's `if (hurt)`. applyDamageEntity (the mob hurtServer
// port) is void, so this snapshots the i-frame state to decide the boolean the same way hurtServer
// computes its return value (BEFORE applyDamageEntity mutates invulnerableTime/lastHurt):
//   - a dead mob (health<=0): hurtServer returns false (isDeadOrDying guard);
//   - inside the upper i-frame window ((float) invulnerableTime > 10.0F) with a non-greater hit:
//     hurtServer returns false (the `if (amount <= lastHurt) return false` branch);
//   - otherwise damage lands and hurtServer returns true.
// src is the genuine ported DamageSource (player_attack with the real attacker id); applyDamageEntity
// records it as the mob's lastDamageSource (MOB-SUB-02).
func (t *TickLoop) applyMobAttackDamage(mob *Entity, src damageSource, amount float32) bool {
	if mob.health <= 0 {
		return false // isDeadOrDying() -> hurtServer returns false
	}
	// Mirror hurtServer's `if (amount < 0.0F) amount = 0.0F;` for the gate decision.
	if amount < 0.0 {
		amount = 0.0
	}
	landed := true
	if float32(mob.invulnerableTime) > hurtCooldownConst {
		// Inside the upper grace window: only a STRICTLY greater hit lands (the excess).
		landed = amount > mob.lastHurt
	}
	t.applyDamageEntity(mob, src, amount)
	return landed
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
func (t *TickLoop) applyAttackDamage(victim *tickPlayer, attackerID int32, amount float32) bool {
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
	// The attack carries DamageSources.playerAttack(attacker) (type PLAYER_ATTACK, causingEntity =
	// the attacker) — the genuine source so the victim's ClientboundDamageEvent flashes the right
	// direction and the on-hit consumers read the real attacker id.
	t.applyDamage(victim, damageSourcePlayerAttack(attackerID), amount)
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
		if t.applyAttackDamage(e, attacker.entityID, d) {
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
// handleAttack). MOB-SUB-09 (Plan 33-02) wires the FEED path here: a player right-clicking a pig
// with pig_food (Animal.mobInteract) feeds it — an ADULT falls in love (setInLove + hearts), a BABY
// ages up faster, both consuming 1 held item. It still NEVER deals damage (only ServerboundAttack
// does). The target is resolved through its owning region (cross-region feeds are dropped per the v5
// same-region cut); a malformed payload / forged or non-mob id is a silent no-op (defensive decode).
func (t *TickLoop) handleInteract(p *tickPlayer, pkt pk.Packet) {
	// DECODE the ServerboundInteract. Wire layout (javap ServerboundInteractPacket.STREAM_CODEC,
	// composite of): VarInt entityId ; InteractionHand (VarInt enum) ; Vec3 location (3 doubles) ;
	// Boolean usingSecondaryAction. We only need the target entityId for the feed path (the held item
	// is read server-side from the player's selected hand, exactly as the TemptGoal held-read does);
	// the trailing fields are decoded defensively to consume the frame cleanly and are not used. A
	// malformed/short payload is a silent no-op (the existing defensive-decode discipline).
	var targetID pk.VarInt
	if err := pkt.Scan(&targetID); err != nil {
		return // short payload: no entity id -> ignore (never panic)
	}

	// RESOLVE the target mob through its OWNING region (re-resolve by id; nil if gone/forged), exactly
	// as handleMobAttack does — NEVER cur() (the dispatch goroutine has no region registered; cur()
	// would fall to region 0 or panic under strictRegion). A non-mob / despawned / forged id -> a silent
	// no-op. The feed path only touches mobs, so a player or unknown id falls through harmlessly.
	ownerRegion := t.owningRegion(int32(targetID))
	if ownerRegion == nil {
		return
	}
	mob, ok := ownerRegion.entities.get(int32(targetID))
	if !ok {
		return
	}

	// CROSS-REGION discipline (T-33-03): handleInteract runs on the player's dispatch path. If the mob
	// is owned by a DIFFERENT region than the player's column, do NOT mutate that foreign region's mob
	// inline (the same Pitfall-2 hazard handleMobAttack guards with its owner-resolve/queue split). v5
	// takes the accepted "same-region cut" deviation here (mirroring the documented same-region breeding
	// cut): a cross-region feed is dropped rather than queued — the player simply re-interacts after the
	// mob/player settle into one region. The common case (a player feeding a pig beside them) is always
	// same-region. CITE region_transfer.go queueDamageIntent as the full-fidelity barrier path a later
	// plan can adopt if cross-region feeding becomes load-bearing.
	playerRegion := t.regionForColumn(columnOf(p.x, p.z))
	if ownerRegion != playerRegion {
		return // cross-region feed: dropped (accepted v5 same-region cut), never a foreign inline mutation
	}

	// MOB-PASS-02 (Phase 34): the Sheep SHEAR path runs BEFORE the feed path (Sheep.mobInteract tries
//	the shears branch ahead of super.mobInteract == Animal.mobInteract feed). trySheepShear returns true
//	when the held item is shears (consuming the interact — whether it sheared or the sheep was not ready),
//	so handleInteract does NOT fall through to feed; it returns false ONLY when the held item is not shears,
//	falling through to tryFeedAnimal. Sheep-gated (typ == entity.Sheep.ID) so it is a zero-cost no-op for a
//	pig/cow/chicken — the pig oracle stream is unperturbed. The cow's tryMilkCow gate (34-01, wave 2) slots
//	in this SAME spot; both are wave-ordered after this wave-1 plan, so no parallel edit to this file.
	if mob.typ == entity.Sheep.ID && t.trySheepShear(p, mob) {
		return // the shear (or the not-ready consume) handled the interact
	}
	// MOB-PASS-01 (Phase 34, Plan 34-01): the Cow MILK path runs BEFORE the feed path
	// (AbstractCow.mobInteract tries the empty-bucket branch ahead of super.mobInteract == Animal
	// .mobInteract feed). tryMilkCow returns true ONLY when the held item is an empty BUCKET on an
	// ADULT cow (it milked); a non-bucket or a baby returns false and falls through to tryFeedAnimal
	// (super.mobInteract). Cow-gated (typ == entity.Cow.ID), so it is a zero-cost no-op for a
	// pig/sheep/chicken — the pig oracle stream is unperturbed. This slots into the SAME spot the
	// sheep shear gate (wave-1) uses; both are additive, mob-gated, and wave-ordered after 34-00.
	if mob.typ == entity.Cow.ID && t.tryMilkCow(p, mob) {
		return // the milk handled the interact
	}
	t.tryFeedAnimal(p, mob)
}

// tryFeedAnimal is the port of net.minecraft.world.entity.animal.Animal.mobInteract's FEED branch for
// a pig (Pig.isFood = stack.is(ItemTags.PIG_FOOD)). Verbatim from the javap'd bytecode
// (33-JARNOTES.md:92-115):
//
//	ItemStack stack = player.getItemInHand(hand);
//	if (isFood(stack)) {
//	    int age = getAge();
//	    if (player instanceof ServerPlayer && age == 0 && canFallInLove()) {  // ADULT, not already in love
//	        usePlayerItem(player, hand, stack);     // consume 1 (survival)
//	        setInLove(serverPlayer);                // inLove = 600 + broadcastEntityEvent(this, 18)
//	        playEatingSound();                      // PIG: the base Animal.playEatingSound is a no-op
//	        return SUCCESS_SERVER;
//	    }
//	    if (canAgeUp()) {                           // BABY (age<0; isAgeLocked is a v1 const-false stub)
//	        usePlayerItem(player, hand, stack);
//	        ageUp(getSpeedUpSecondsWhenFeeding(-age), true);  // grow toward adult faster
//	        playEatingSound();                      // PIG: no-op
//	        return SUCCESS;
//	    }
//	}
//
// ORDER is load-bearing and preserved: the ADULT-love branch is tried FIRST, then the BABY age-up
// branch. The server is authoritative — the held item is read server-side (the player's selected hand,
// the TemptGoal precedent), never trusted from the packet. usePlayerItem == shrinkHeldItem (stack.
// consume(1); pig_food carrots carry no USE_REMAINDER). setInLove's heart broadcast (Level.
// broadcastEntityEvent(this, 18)) is realized via broadcastHearts. playEatingSound() for a PIG is the
// empty base Animal.playEatingSound (Pig does NOT override it — javap-confirmed `return`), so feeding a
// pig emits NO sound; the call is preserved as a documented no-op so a sound-overriding animal (Phase
// 34) slots in here. This runs ONLY on a real ServerboundInteract — the oracle pig is never fed, so
// the whole path is dormant on it (the byte-identical gate).
//	[VERIFIED javap Animal.mobInteract (offsets 0-119): isFood; getAge; (ServerPlayer && age==0 &&
//	 canFallInLove) -> usePlayerItem + setInLove + playEatingSound + SUCCESS_SERVER; canAgeUp ->
//	 usePlayerItem + ageUp(getSpeedUpSecondsWhenFeeding(-age), true) + playEatingSound + SUCCESS.
//	 Mob.usePlayerItem -> stack.consume(1, player). AgeableMob.canAgeUp == isBaby() && !isAgeLocked().
//	 Pig has NO playEatingSound override; Animal.playEatingSound == return (no-op).]
func (t *TickLoop) tryFeedAnimal(p *tickPlayer, mob *Entity) {
	inv := ensureInventory(p)

	// player.getItemInHand(hand): read the player's selected hand. Sulfur reads the MAIN-hand selected
	// hotbar slot (heldWindowSlot) — the established held-item read (TemptGoal.shouldFollow precedent).
	// An empty hand (slotIsEmpty) is never pig_food (ItemStack.EMPTY fails isFood), so it falls through.
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) {
		return
	}
	itemID := int32(held.ItemID)

	// Pig.isFood = stack.is(ItemTags.PIG_FOOD) — the Phase-32 tag read (the same predicate the @4
	// pig_food TemptGoal uses). A non-food item is a silent no-op (no consume, no love).
	if !itemInTag(itemID, "pig_food") {
		return
	}

	age := mob.breedAge

	// ADULT branch (tried FIRST): age == 0 && canFallInLove() (player is always a ServerPlayer here).
	if age == 0 && mob.canFallInLove() {
		t.shrinkHeldItem(p, inv) // usePlayerItem -> stack.consume(1): survival shrink by 1
		mob.setInLove()          // inLove = 600
		t.broadcastHearts(mob)   // setInLove's broadcastEntityEvent(this, 18): the client heart burst
		// playEatingSound(): the base Animal.playEatingSound is a no-op for a pig (Pig has no override).
		return
	}

	// BABY branch: canAgeUp() == isBaby() (isAgeLocked is a v1 const-false stub) == age < 0.
	if mob.isBaby() {
		t.shrinkHeldItem(p, inv)
		// ageUp(getSpeedUpSecondsWhenFeeding(-age)): grow the fed baby toward adulthood faster. -age is
		// positive (a baby's breedAge is negative). If the grow-up crosses 0 (the baby becomes an adult
		// THIS feed), perform the same 0-crossing side effect tickMobAging's onGrewUp does — restore the
		// adult AABB + broadcast DATA_BABY_ID=false — so a fed-to-adulthood baby renders full-size at once.
		wasBaby := mob.isBaby()
		mob.ageUp(getSpeedUpSecondsWhenFeeding(-age))
		if wasBaby && !mob.isBaby() {
			t.onGrewUp(mob)
		}
		// playEatingSound(): no-op for a pig (see above).
		return
	}
	// Neither branch (an adult already in love, or on cooldown): no consume, no effect — falls through
	// to super.mobInteract in vanilla (no further v1 behavior).
}

// tryMilkCow ports net.minecraft.world.entity.animal.cow.AbstractCow.mobInteract VERBATIM
// (34-JARNOTES.md:134-147 — NO RNG):
//
//	@Override mobInteract(Player player, InteractionHand hand) {
//	    ItemStack itemStack = player.getItemInHand(hand);
//	    if (itemStack.is(Items.BUCKET) && !this.isBaby()) {
//	        player.playSound(SoundEvents.COW_MILK, 1.0f, 1.0f);
//	        ItemStack r = ItemUtils.createFilledResult(itemStack, player, Items.MILK_BUCKET.getDefaultInstance());
//	        player.setItemInHand(hand, r);
//	        return InteractionResult.SUCCESS;
//	    }
//	    return super.mobInteract(player, hand);   // the feed/breed path (Animal.mobInteract)
//	}
//
// Returns true iff the milk branch fired (held item is an empty BUCKET && the cow is an ADULT); a
// non-bucket or a baby returns false so handleInteract falls through to tryFeedAnimal (super.mobInteract).
// The held item is read SERVER-side (the player's selected hand, the tryFeedAnimal/TemptGoal precedent),
// never trusted from the Interact payload (T-34-02). COW_MILK == soundid 449; Cow (Animal) getSoundSource
// == NEUTRAL. The sound is emitted via the established entity-attached broadcastToTrackers/encodeSoundEntity
// model (the chicken egg-lay + combat hurt/death sounds use the same seam) — player.playSound on the
// server side reaches the tracking players. NO RNG: COW_MILK plays at a fixed 1.0/1.0 (no voice-pitch
// jitter), so this draws ZERO from any stream.
//	[VERIFIED javap AbstractCow.mobInteract: is(Items.BUCKET) && !isBaby() -> playSound(COW_MILK,1,1) +
//	 ItemUtils.createFilledResult(stack, player, MILK_BUCKET.getDefaultInstance()) + setItemInHand + SUCCESS;
//	 else super.mobInteract. Items.BUCKET == "bucket" (1040), Items.MILK_BUCKET == "milk_bucket" (1046).]
func (t *TickLoop) tryMilkCow(p *tickPlayer, mob *Entity) bool {
	inv := ensureInventory(p)

	// player.getItemInHand(hand): the player's selected MAIN-hand hotbar slot (the established held read).
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if slotIsEmpty(held) {
		return false // empty hand: ItemStack.EMPTY fails is(BUCKET) -> super.mobInteract (feed)
	}

	// itemStack.is(Items.BUCKET): the held item must be an empty bucket (1040). A water/lava/milk bucket
	// or any other item fails the gate -> super.mobInteract (the bucket id is exact, no tag).
	if int32(held.ItemID) != int32(item.Bucket.ID) {
		return false
	}

	// !this.isBaby(): only an ADULT cow milks. A baby falls through to the feed path (super.mobInteract).
	if mob.isBaby() {
		return false
	}

	// player.playSound(SoundEvents.COW_MILK, 1.0f, 1.0f): emit the entity.cow.milk sound (449) on the
	// NEUTRAL category at a fixed 1.0/1.0 (NO RNG). Broadcast to the cow's trackers (the entity-attached
	// sound seam the chicken egg-lay + mob hurt/death use).
	t.broadcastToTrackers(mob.id, encodeSoundEntity(449, soundSourceNeutral, mob.id, 1.0, 1.0, 0))

	// ItemStack r = ItemUtils.createFilledResult(itemStack, player, MILK_BUCKET.getDefaultInstance());
	// player.setItemInHand(hand, r): replace the hand with the createFilledResult return value.
	r := t.createFilledResult(p, inv, held, component.SlotData{Count: 1, ItemID: pk.VarInt(item.MilkBucket.ID)})
	inv.set(heldWindowSlot(inv.heldSlot), r)

	// The held-slot change (the bucket consume + the possible milk_bucket replace) plus the inventory.add
	// from createFilledResult are synchronized to the client with an authoritative full content re-send
	// (createFilledResult's add path may have touched arbitrary slots).
	t.sendContent(p)
	return true
}

// createFilledResult ports net.minecraft.world.item.ItemUtils.createFilledResult(emptyStack, player,
// filledStack, grow=true) VERBATIM (the 3-arg overload passes grow=true; javap ItemUtils.createFilledResult
// this session). It is the survival item-swap helper: consume one of the empty stack and yield the filled
// stack, replacing the hand if the empty stack emptied, else depositing the filled stack into the inventory.
// Returns the ItemStack that becomes the new hand contents (the empty stack if it is still non-empty, else
// the filled stack). MUTATES the player inventory (the inventory.add path) but NOT the passed empty slot —
// the caller writes the return value into the hand.
//
//	createFilledResult(ItemStack emptyStack, Player player, ItemStack filledStack, boolean grow):
//	    boolean creative = player.hasInfiniteMaterials();
//	    if (grow && creative) {                              // creative: keep the bucket, add milk if absent
//	        if (!player.getInventory().contains(filledStack)) player.getInventory().add(filledStack);
//	        return emptyStack;
//	    }
//	    emptyStack.consume(1, player);                       // SURVIVAL: shrink the bucket by 1
//	    if (emptyStack.isEmpty()) return filledStack;        // the bucket emptied -> the hand becomes milk
//	    if (!player.getInventory().add(filledStack)) player.drop(filledStack, false);  // else deposit/drop milk
//	    return emptyStack;                                   // and the hand keeps the remaining bucket(s)
//
// v1 is always survival here (a milking player has finite materials; the creative path is a documented
// no-op branch — Player.hasInfiniteMaterials is a v1 const-false stub, mirroring shrinkHeldItem's caller
// creative guard). The `player.drop` (inventory-full) fallback is realized as a dropped ItemEntity at the
// player, but v1's inventoryAdd into a 46-slot player inventory effectively never fills for a single milk
// bucket — the drop branch is the cited fallback (no item is ever silently lost).
//	[VERIFIED javap ItemUtils.createFilledResult(Lnet/minecraft/world/item/ItemStack;Lnet/minecraft/world/
//	 entity/player/Player;Lnet/minecraft/world/item/ItemStack;Z): hasInfiniteMaterials; (grow && creative)
//	 -> contains/add + return emptyStack; else consume(1, player); isEmpty -> return filledStack; add ->
//	 (!add) drop(filled, false); return emptyStack. The 3-arg overload calls it with grow=true (iconst_1).]
func (t *TickLoop) createFilledResult(p *tickPlayer, inv *Inventory, emptyStack, filledStack component.SlotData) component.SlotData {
	// SURVIVAL (v1: hasInfiniteMaterials is const-false): emptyStack.consume(1, player) — shrink by 1.
	emptyStack.Count--
	if emptyStack.Count <= 0 {
		// emptyStack.isEmpty(): the bucket emptied -> the hand becomes the filled (milk) stack.
		return filledStack
	}
	// The bucket stack is still non-empty: deposit the milk into the inventory (player.getInventory().add).
	// inventoryAdd mutates the stack to the leftover; a full inventory's leftover is the cited drop fallback.
	add := filledStack
	if !t.inventoryAdd(p, inv, &add) {
		// player.drop(filledStack, false): inventory full -> drop the milk at the player. v1 never fills for
		// a single milk bucket; this is the cited faithful fallback (no item silently lost).
		t.playerDrop(p, add, false)
	}
	// return emptyStack: the hand keeps the remaining bucket(s).
	return emptyStack
}
