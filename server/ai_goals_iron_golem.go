package server

// ai_goals_iron_golem.go — IRON GOLEM (Task): the IronGolem.doHurtTarget override + the aiStep countdowns +
// the isPlayerCreated flag, PORTED 1:1 from the unobfuscated 26.2 jar (net.minecraft.world.entity.animal.
// golem.IronGolem, CFR + javap -c -p this session). The golem's doHurtTarget is a FULL OVERRIDE (a
// different damage shape from the shared Mob.doHurtTarget) — the range-roll damage (ad/2 + nextInt(ad)) plus
// the signature +0.4 vertical FLING on top of hurtServer's own horizontal knockback. The village/villager
// goals (MoveTowardsTarget/MoveBackToVillage/GolemRandomStrollInVillage-villager-leg/OfferFlower/
// DefendVillage) are cite-deferred in the .star header (no villager behavior / POI-toward nav yet).
//
//	[VERIFIED CFR IronGolem.doHurtTarget:
//	   this.attackAnimationTick = 10;
//	   level.broadcastEntityEvent(this, (byte)4);
//	   float ad = getAttackDamage();                                   // == getAttributeValue(ATTACK_DAMAGE) == 15
//	   float damage = (int)ad > 0 ? ad/2.0f + random.nextInt((int)ad) : ad;   // 15 -> 7.5 + nextInt(15)
//	   boolean hurt = target.hurtServer(mobAttack(this), damage);
//	   if (hurt) {
//	       double kbr = target instanceof LivingEntity le ? le.getAttributeValue(KNOCKBACK_RESISTANCE) : 0.0;
//	       double scale = Math.max(0.0, 1.0 - kbr);
//	       target.setDeltaMovement(target.getDeltaMovement().add(0.0, 0.4f*scale, 0.0));
//	       EnchantmentHelper.doPostAttackEffects(level, target, damageSource);   // v1 no enchants: no-op
//	   }
//	   this.playSound(IRON_GOLEM_ATTACK, 1.0f, 1.0f);                  // client sound: server no-op
//	   return hurt; ]

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// IronGolem constants (VERIFIED CFR IronGolem).
const (
	ironGolemAttackAnimationTicks = 10  // doHurtTarget: attackAnimationTick = 10
	ironGolemFlingVertical        = 0.4 // doHurtTarget: 0.4f vertical fling component (scaled by 1-kbr)
	ironGolemOfferFlowerTicks     = 400 // OfferFlowerGoal.OFFER_TICKS / handleEntityEvent(11) offerFlowerTick = 400
)

// IronGolem entity-event byte. handleEntityEvent(4) = the attack swing (attackAnimationTick = 10) — the
// SAME byte the ravager attack uses (entityEventRavagerAttack == 4), but the golem broadcasts it from its
// own doHurtTarget. (Events 11/34 = offer/retract flower — cite-deferred with OfferFlowerGoal.)
const entityEventIronGolemAttack byte = 4

// ironGolemDoHurtTarget ports IronGolem.doHurtTarget (a FULL override of Mob.doHurtTarget) for a PLAYER
// victim. Called from checkAndPerformAttack (ai_goals_attack.go) for a golem, INSTEAD of the shared
// doHurtTarget (never both). It sets the attack-animation tick + broadcasts event 4, rolls the range damage
// (ad/2 + nextInt(ad)) on the golem's OWN rng stream, applies it through the player hurt path (applyDamage,
// which also runs the standard horizontal dealDefaultKnockback), and — if the hit LANDED — adds the +0.4
// vertical fling scaled by (1 - target KNOCKBACK_RESISTANCE), sending the combined velocity to the player.
//
// RNG: EXACTLY ONE mobRandom(e).nextInt((int)ad) draw per landed swing, on the golem's per-entity stream
// (the pig oracle — never a golem — draws ZERO). The IRON_GOLEM_ATTACK sound is a client-side no-op.
//
// The mob-victim arm (a golem attacking a hostile MOB it acquired via iron_golem_hostile_target) shares the
// same across-the-board limitation as the wolf/fox: the melee goal (ai_goals_attack.go) resolves only PLAYER
// targets, so the golem's hostile-mob TARGET is acquired + set (structurally faithful) but the mob-vs-mob
// STRIKE is the cite-deferred mob-target-melee gap common to every neutral/hunting mob in v1.
func (t *TickLoop) ironGolemDoHurtTarget(e *Entity, target *tickPlayer) {
	// this.attackAnimationTick = 10; level.broadcastEntityEvent(this, (byte)4).
	e.ironGolemAttackAnimationTick = ironGolemAttackAnimationTicks
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventIronGolemAttack))

	// float ad = getAttackDamage() == (float) getAttributeValue(ATTACK_DAMAGE). The d2f narrowing cast at
	// the vanilla site.
	ad := float32(e.getAttributeValue(attribute.AttackDamage))
	// float damage = (int)ad > 0 ? ad/2.0f + nextInt((int)ad) : ad. With ATTACK_DAMAGE 15 -> (int)15 > 0 ->
	// 15/2 (== 7.5f) + nextInt(15) (== [0,14]) -> the [7.5, 21.5] range. The (int)ad truncation feeds BOTH
	// the >0 guard and the nextInt bound; ad/2.0f keeps the .5 (float division, NOT integer). A degenerate
	// ATTACK_DAMAGE == 0 would deal the flat ad (0) with NO draw — the guard preserves that exactly.
	var damage float32
	if int(ad) > 0 {
		damage = ad/2.0 + float32(mobRandom(e).nextInt(int(ad))) // ONE nextInt((int)ad) draw
	} else {
		damage = ad
	}

	// boolean hurt = target.hurtServer(mobAttack(this), damage). The victim is a PLAYER, so this routes
	// through applyDamage (combat.go) — the SAME hurtServer port the shared mob doHurtTarget uses. applyDamage
	// ALSO runs the standard horizontal dealDefaultKnockbackPlayer (that is part of hurtServer, not
	// doHurtTarget), so the golem's fling below stacks ON TOP of that horizontal pop. applyAttackableMobDamage
	// returns whether the hit LANDED (i-frame gate) so the fling is gated exactly as vanilla's `if (hurt)`.
	src := damageSourceMobAttack(e.id)
	hurt := t.applyGolemAttackDamage(target, src, damage)

	if hurt {
		// double scale = Math.max(0.0, 1.0 - target.getAttributeValue(KNOCKBACK_RESISTANCE)). A player's
		// KNOCKBACK_RESISTANCE base is 0.0, so scale is 1.0 (full fling) unless a modifier lowers it.
		kbr := target.getAttributeValue(attrKnockbackResistance)
		scale := math.Max(0.0, 1.0-kbr)
		// target.setDeltaMovement(target.getDeltaMovement().add(0, 0.4f*scale, 0)): add the PURE-VERTICAL
		// impulse to the player's velocity. 0.4f*scale is computed as the float 0.4f then widened (the jar
		// multiplies the float literal by the double scale -> a double add). The velocity lives on the
		// player's store Entity (playerEntity, what the tracker syncs); send the combined velocity to the
		// player's own client (ServerPlayer is the authority for its motion, so a server-driven impulse must
		// be pushed via ClientboundSetEntityMotion — the same send dealDefaultKnockbackPlayer uses).
		if target.playerEntity != nil {
			target.playerEntity.vy += float64(float32(ironGolemFlingVertical)) * scale
			if target.client != nil {
				target.client.Send(encodeSetEntityMotion(target.playerEntity))
			}
		}
		// EnchantmentHelper.doPostAttackEffects: v1 has no enchantments -> a cited no-op.
	}
	// this.playSound(IRON_GOLEM_ATTACK, 1.0f, 1.0f): a client-side sound, server no-op (cited).
}

// applyGolemAttackDamage is the hurtOrSimulate(source, amount) -> LivingEntity.hurtServer bridge for the
// golem's PLAYER victim — a thin sibling of applyAttackDamage (attack_dispatch.go) that carries the golem's
// mob_attack DamageSource (with the golem's attacker id) instead of a player_attack source. It returns
// whether damage ACTUALLY landed (the i-frame gate) so ironGolemDoHurtTarget gates the fling exactly as
// vanilla's `if (hurt)`. The decision is computed BEFORE applyDamage mutates invulnerableTime/lastHurt.
func (t *TickLoop) applyGolemAttackDamage(victim *tickPlayer, src damageSource, amount float32) bool {
	if victim.dead {
		return false // isDeadOrDying() -> hurtServer returns false
	}
	if amount < 0.0 {
		amount = 0.0 // hurtServer's `if (amount < 0.0F) amount = 0.0F` for the gate decision
	}
	landed := true
	if float32(victim.invulnerableTime) > hurtCooldownConst {
		// Inside the upper grace window: only a STRICTLY greater hit lands (the excess); a non-greater hit is
		// absorbed -> hurtServer returns false.
		landed = amount > victim.lastHurt
	}
	t.applyDamage(victim, src, amount) // the PLAYER hurt path (also runs dealDefaultKnockbackPlayer)
	return landed
}

// ironGolemAiStep ports IronGolem.aiStep (the server branch, a per-type hook — the sibling of ravagerAiStep/
// creeperAiStep), run AFTER serverAiStep, golem-gated. The jar:
//
//	super.aiStep();
//	if (attackAnimationTick > 0) --attackAnimationTick;
//	if (offerFlowerTick > 0)     --offerFlowerTick;
//	if (!level.isClientSide()) updatePersistentAnger((ServerLevel)level, true);  // NeutralMob anger expiry
//
// The two countdowns are pure decrements (no RNG). updatePersistentAnger is the NeutralMob anger book-
// keeping: the gametime-ENDPOINT anger model (combat_mob.go) auto-expires when gametime passes angerEndTime
// (the same W6-DISSOLVED shape the wolf uses), so there is NO per-tick decrement to run here — the anger is
// read live by isAngryAt against angerEndTime. Zero for every non-golem entity.
//
//	[VERIFIED CFR IronGolem.aiStep: super.aiStep(); attackAnimationTick-- (if>0); offerFlowerTick-- (if>0);
//	 !isClientSide -> updatePersistentAnger(level, true).]
func (t *TickLoop) ironGolemAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.ironGolemAttackAnimationTick > 0 {
		e.ironGolemAttackAnimationTick--
	}
	if e.ironGolemOfferFlowerTick > 0 {
		e.ironGolemOfferFlowerTick--
	}
	// updatePersistentAnger: the gametime-endpoint anger auto-expires (no per-tick work); isAngryAt reads it
	// live. The ResetUniversalAngerTargetGoal (targetSelector @4) is likewise dissolved into this model.
}

// isPlayerCreated ports IronGolem.isPlayerCreated: (DATA_FLAGS_ID & 1) != 0 — the bit set on a golem a
// player built (a pumpkin-topped iron frame) vs a village-spawned golem. A player-created golem never
// targets players (canAttack: isPlayerCreated && target.is(PLAYER) -> false). v1 has no golem-construction
// path yet, so DATA_FLAGS_ID starts 0 (village/dbg golems are NOT player-created) — the field + accessor are
// ported so a future construction path sets the bit + the canAttack gate reads through. Cite
// IronGolem.isPlayerCreated + IronGolem.setPlayerCreated (DATA_FLAGS_ID bit 0x01).
func ironGolemIsPlayerCreated(e *Entity) bool {
	return e.ironGolemPlayerCreated
}

// ironGolemSetPlayerCreated ports IronGolem.setPlayerCreated: set/clear the DATA_FLAGS_ID bit 0x01. Kept as
// the write sibling of ironGolemIsPlayerCreated so a golem-construction path flips it (no consumer sets it
// in v1 — village/dbg golems stay village golems). Cite IronGolem.setPlayerCreated.
func ironGolemSetPlayerCreated(e *Entity, value bool) {
	e.ironGolemPlayerCreated = value
}

// ironGolemCanAttack ports the IronGolem.canAttack guards the NearestAttackableTargetGoal<Player> respects:
//
//	if (isPlayerCreated() && target.is(PLAYER)) return false;   // a player-built golem never hunts players
//	if (target.is(CREEPER)) return false;                       // (the hostile-mob goal already excludes it)
//	return super.canAttack(target);
//
// The player-target goal (angry_player_target) is ALREADY anger-gated (an un-provoked golem never acquires a
// player), and a player-created golem is never angry at a player in v1 (no construction path sets the bit),
// so this guard is structurally present + faithful. Kept as the explicit accessor a future construction path
// wires into the target selector. Cite IronGolem.canAttack.
func ironGolemCanAttackPlayer(e *Entity) bool {
	// isPlayerCreated() && target.is(PLAYER) -> false: a player-built golem does not target players.
	return !ironGolemIsPlayerCreated(e)
}

// isIronGolem reports whether a wire type is the IronGolem (id 70). Used by the tick dispatch gate.
func isIronGolem(typ entity.ID) bool { return typ == entity.IronGolem.ID }
