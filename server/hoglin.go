package server

// hoglin.go -- the Hoglin (net.minecraft.world.entity.monster.hoglin.Hoglin), a 1:1 port from the
// unobfuscated 26.2 jar. Hoglin is the nether beast that MELEES + FLINGS its target upward (the knock-up
// toss), converts to a Zoglin after > 300 ticks in a non-nether dimension (the zombification-off state),
// and comes in adult/baby forms. Vanilla drives it with a BRAIN; for a bounded port this is a GOAL-style
// melee (acquire nearest player + the shared MeleeAttackGoal cadence) layered over the faithful
// hurtAndThrowTarget + conversion timer -- the brain (StartAttacking/RunIf/panic-from-repellents) is the
// DEFERRED behavior layer. Code-spawned (spawnHoglin) with a minimal e.ai; its per-tick drive is
// hoglinAiStep from tickAI (sibling of blazeAiStep, per-type-gated on typ == entity.Hoglin.ID).
//
// VANILLA (verified javap Hoglin + HoglinBase this session):
//   Hoglin.createAttributes: Monster.createMonsterAttributes + MAX_HEALTH 40.0 + MOVEMENT_SPEED
//     0.30000001192092896 + KNOCKBACK_RESISTANCE 0.6000000238418579 + ATTACK_KNOCKBACK 1.0 + ATTACK_DAMAGE 6.0.
//   Hoglin ctor: xpReward 5.
//   Hoglin.ageBoundaryReached: baby -> xpReward 3, ATTACK_DAMAGE base 0.5; adult -> xpReward 5, ATTACK_DAMAGE 6.0.
//   Hoglin.doHurtTarget(level, entity): if(!(entity instanceof LivingEntity le)) return false;
//     attackAnimationRemainingTicks=10; broadcastEntityEvent(this,4); makeSound(HOGLIN_ATTACK);
//     HoglinAi.onHitTarget(this, le); return HoglinBase.hurtAndThrowTarget(level, this, le).
//   HoglinBase.hurtAndThrowTarget(level, attacker, target): f=(float)attacker.getAttributeValue(ATTACK_DAMAGE);
//     f2 = (!attacker.isBaby() && (int)f > 0) ? f/2.0f + nextInt((int)f) : f; flag = target.hurtServer(level,
//     mobAttack(attacker), f2); if(flag){ doPostAttackEffects; if(!attacker.isBaby()) throwTarget(attacker,target); } return flag.
//   HoglinBase.throwTarget(attacker, target): d6 = ATTACK_KNOCKBACK(attacker) - KNOCKBACK_RESISTANCE(target);
//     if(d6 <= 0) return; d8=target.getX()-attacker.getX(); d10=target.getZ()-attacker.getZ();
//     f13 = nextInt(21)-10; d14 = d6 * (nextFloat()*0.5f + 0.2f);
//     vec = new Vec3(d8,0,d10).normalize().scale(d14).yRot(f13);
//     d17 = d6 * nextFloat() * 0.5; target.push(vec.x, d17, vec.z); target.hurtMarked = true.
//   Hoglin.isConverting(): !isImmuneToZombification() && !isNoAi() && environmentAttributes.getValue(
//     PIGLINS_ZOMBIFY, position()) (true in overworld/end, false in the_nether).
//   Hoglin.customServerAiStep: brain.tick; HoglinAi.updateActivity; if(isConverting()){ ++timeInOverworld;
//     if(timeInOverworld > 300){ makeSound(HOGLIN_CONVERTED_TO_ZOMBIFIED); finishConversion(); } } else timeInOverworld=0.
//   Hoglin.finishConversion(): convertTo(EntityType.ZOGLIN, ...) (the new zoglin gets NAUSEA 200, client cue).
//   Hoglin.aiStep(): if(attackAnimationRemainingTicks > 0) --attackAnimationRemainingTicks; super.aiStep().
//   BABY: finalizeSpawn setBaby(true) with probability 0.2 (nextFloat() < 0.2f).
//
// v1 STUBS (cited): the BRAIN (HoglinAi sensors + activities: StartAttacking, MeleeAttack behavior, panic
// from warped_fungus / nether_portal / hoglin_repellents, avoid piglins, the breed/love goals) is the
// DEFERRED behavior layer -- this port supplies the goal-style nearest-target melee + the faithful
// hurtAndThrowTarget toss + the zoglin-conversion timer. The immune-to-zombification flag + the
// HOGLIN_CONVERTED_TO_ZOMBIFIED sound + the new-zoglin NAUSEA cue are deferred; the observable gameplay
// (attributes, melee damage roll, the knock-up fling, the > 300-tick non-nether -> Zoglin conversion,
// adult/baby damage) is EXACT.

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

const (
	hoglinMaxHealth           = 40.0
	hoglinMovementSpeed       = 0.30000001192092896
	hoglinKnockbackResistance = 0.6000000238418579
	hoglinAttackKnockback     = 1.0
	hoglinAttackDamage        = 6.0
	hoglinBabyAttackDamage    = 0.5
	hoglinConversionTime      = 300
	hoglinAttackAnimTicks     = 10
	hoglinMeleeCooldown       = meleeAttackResetCooldown
	hoglinBabySpawnChance     = 0.2
)

// spawnHoglin creates a Hoglin at (x,y,z) with the jar attributes and adds it to the owner region store.
// baby toggles the adult/baby age + ATTACK_DAMAGE (adult 6.0, baby 0.5) per Hoglin.ageBoundaryReached.
// Minimal e.ai; NO goalSelector (the BRAIN is deferred; behavior is the code-driven hoglinAiStep, like
// spawnBlaze). initSpawnHealth seeds health from MAX_HEALTH (40.0). Cite Hoglin.createAttributes +
// ageBoundaryReached.
func (t *TickLoop) spawnHoglin(x, y, z float64, baby bool, dim int) *Entity {
	h := NewEntity(t.idAlloc.AllocID(), entity.Hoglin, x, y, z)
	h.isHoglin = true
	h.hoglinDimension = dim // dimOverworld / dimNether -- gates the zoglin conversion (PIGLINS_ZOMBIFY)
	if baby {
		h.breedAge = babyStartAge // AgeableMob baby: age < 0
		setHoglinAgeAttack(h)     // ageBoundaryReached: baby ATTACK_DAMAGE = 0.5, dims halve
	} else {
		setHoglinAgeAttack(h) // ageBoundaryReached: adult ATTACK_DAMAGE = 6.0
	}
	initSpawnHealth(h) // setHealth(getMaxHealth()) -> 40.0
	h.ai = &mobAI{}
	reseedMobAI(h.ai, h.id)
	owner := t.regionForEntity(h)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(h)
	return h
}

// setHoglinAgeAttack ports Hoglin.ageBoundaryReached: a baby sets xpReward 3 + ATTACK_DAMAGE base 0.5; an
// adult sets xpReward 5 + ATTACK_DAMAGE base 6.0. Also refreshes the baby-halved / adult AABB dims (the
// shared getDefaultDimensions baby-scale). NO RNG. Cite Hoglin.ageBoundaryReached + getDefaultDimensions.
func setHoglinAgeAttack(e *Entity) {
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			if e.isBaby() {
				inst.SetBaseValue(hoglinBabyAttackDamage) // ldc2_w 0.5d
			} else {
				inst.SetBaseValue(hoglinAttackDamage) // ldc2_w 6.0d
			}
		}
	}
	// getDefaultDimensions: baby -> BABY_DIMENSIONS (adult scaled 0.5); adult -> the spawn-table dims.
	if e.isBaby() {
		e.width = e.adultWidth * babyDimensionScale
		e.height = e.adultHeight * babyDimensionScale
	} else {
		e.width = e.adultWidth
		e.height = e.adultHeight
	}
}

// hoglinTarget reads the current attack-target player, or nil. Tick-owned (mirrors blazeTarget).
func (t *TickLoop) hoglinTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// hoglinAcquireNearestPlayer approximates the Hoglin brain nearest-target: nearest live player within
// FOLLOW_RANGE (createMobAttributes 16.0). The full brain (StartAttacking sensors) is deferred; this is
// the bounded goal-style acquisition (like blazeAcquireNearestPlayer). NO RNG. Cite HoglinAi + the brain
// deferral note.
func (t *TickLoop) hoglinAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 16.0
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead || distanceToSqrPlayer(p, e) > rangeSqr {
			e.ai.attackTargetID = 0
		} else {
			return
		}
	}
	var best *tickPlayer
	bestSq := rangeSqr
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dsq := distanceToSqrPlayer(p, e)
		if dsq <= bestSq {
			bestSq = dsq
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID
	}
}

// hoglinDoHurtTarget ports Hoglin.doHurtTarget: set attackAnimationRemainingTicks 10, broadcast event 4,
// make the HOGLIN_ATTACK sound, HoglinAi.onHitTarget (deferred), then HoglinBase.hurtAndThrowTarget. The
// animation ticks + sound are client cues (deferred); the load-bearing part is hurtAndThrowTarget. Cite
// Hoglin.doHurtTarget + HoglinBase.hurtAndThrowTarget.
func (t *TickLoop) hoglinDoHurtTarget(e *Entity, target *tickPlayer) {
	e.hoglinAttackAnimTicks = hoglinAttackAnimTicks // attackAnimationRemainingTicks = 10
	t.broadcastMobSwing(e)                          // broadcastEntityEvent(this, 4) -- swing/attack animation
	t.hoglinHurtAndThrowTarget(e, target)           // HoglinBase.hurtAndThrowTarget(level, this, target)
}

// hoglinHurtAndThrowTarget ports HoglinBase.hurtAndThrowTarget(level, attacker, target): compute the
// attack damage (adult: f/2 + nextInt((int)f) where f = ATTACK_DAMAGE; baby: f as-is), hurt the target,
// then -- on a landed hit and if the attacker is NOT a baby -- throwTarget (the knock-up toss). The RNG
// (nextInt on the damage roll) is on the hoglin OWN stream. Cite HoglinBase.hurtAndThrowTarget.
func (t *TickLoop) hoglinHurtAndThrowTarget(e *Entity, target *tickPlayer) {
	f := float32(e.getAttributeValue(attribute.AttackDamage)) // (float) attacker.getAttributeValue(ATTACK_DAMAGE)
	var dmg float32
	if !e.isBaby() && int(f) > 0 {
		// f2 = f/2.0 + nextInt((int)f): the adult damage roll (a random bonus up to the base).
		dmg = f/2.0 + float32(mobRandom(e).nextInt(int(f)))
	} else {
		dmg = f // baby (or f<=0): the flat value
	}
	src := damageSourceMobAttack(e.id) // damageSources().mobAttack(attacker)
	// boolean flag = target.hurtServer(level, src, f2): snapshot the landed flag BEFORE applyDamage mutates.
	hurt := !target.dead
	if hurt && float32(target.invulnerableTime) > hurtCooldownConst {
		amt := dmg
		if amt < 0 {
			amt = 0
		}
		hurt = amt > target.lastHurt
	}
	t.applyDamage(target, src, dmg) // the PLAYER hurt path
	if !hurt {
		return
	}
	// EnchantmentHelper.doPostAttackEffects (deferred). if(flag && !attacker.isBaby()) throwTarget.
	if !e.isBaby() {
		t.hoglinThrowTarget(e, target) // HoglinBase.throwTarget(attacker, target) -- the knock-up toss
	}
}

// hoglinThrowTarget ports HoglinBase.throwTarget(attacker, target): the big knock-up fling. delta =
// ATTACK_KNOCKBACK(attacker) - KNOCKBACK_RESISTANCE(target); if delta <= 0 return. Build a horizontal
// vector from attacker->target, normalize, scale by delta*(nextFloat()*0.5+0.2), rotate yRot by
// (nextInt(21)-10) radians; the vertical is delta*nextFloat()*0.5. push(hx, vy, hz); hurtMarked = true.
// The RNG (2x nextFloat + 1x nextInt) is on the hoglin OWN stream, in the EXACT draw order. Cite
// HoglinBase.throwTarget.
func (t *TickLoop) hoglinThrowTarget(e *Entity, target *tickPlayer) {
	knockback := e.getAttributeValue(attribute.AttackKnockback)     // ATTACK_KNOCKBACK == 1.0
	resistance := target.getAttributeValue(attrKnockbackResistance) // target KNOCKBACK_RESISTANCE (player 0.0)
	delta := knockback - resistance                                 // d6
	if delta <= 0.0 {
		return
	}
	dx := target.x - e.x // target.getX() - attacker.getX()
	dz := target.z - e.z // target.getZ() - attacker.getZ()
	rng := mobRandom(e)
	// f13 = nextInt(21) - 10 (the yRot angle, in RADIANS, passed straight to Vec3.yRot(float)).
	yRotRad := float32(rng.nextInt(21) - 10)
	// d14 = delta * (nextFloat() * 0.5 + 0.2): the horizontal scale.
	horiz := delta * float64(rng.nextFloat()*0.5+0.2)
	// Vec3(dx, 0, dz).normalize().scale(horiz).yRot(f13):
	nx, ny, nz := normalizeVec3(dx, 0.0, dz)
	sx, _, sz := nx*horiz, ny*horiz, nz*horiz
	cos := float64(mthCosf(yRotRad))
	sin := float64(mthSinf(yRotRad))
	hx := sx*cos + sz*sin // Vec3.yRot: x' = x*cos + z*sin
	hz := sz*cos - sx*sin // Vec3.yRot: z' = z*cos - x*sin
	// d17 = delta * nextFloat() * 0.5: the vertical (knock-up) component.
	vy := delta * float64(rng.nextFloat()) * 0.5
	// target.push(hx, vy, hz): ADD to the player velocity (LivingEntity.push == addDeltaMovement), then
	// hurtMarked = true (send SetEntityMotion so the client applies the impulse).
	if target.playerEntity != nil {
		target.playerEntity.vx += hx
		target.playerEntity.vy += vy
		target.playerEntity.vz += hz
		if target.client != nil {
			target.client.Send(encodeSetEntityMotion(target.playerEntity)) // hurtMarked -> motion sync
		}
	}
}

// hoglinIsConverting ports Hoglin.isConverting(): !isImmuneToZombification() && !isNoAi() && the
// PIGLINS_ZOMBIFY environment attribute is true (i.e. NOT in the nether). v1 reads the region dimension:
// converting == NOT dimNether (overworld/end zombify piglins+hoglins). Cite Hoglin.isConverting +
// EnvironmentAttributes.PIGLINS_ZOMBIFY.
func (t *TickLoop) hoglinIsConverting(e *Entity) bool {
	// !isImmuneToZombification() && !isNoAi(): a spawned hoglin has AI and (v1) is never immune -> both true.
	// PIGLINS_ZOMBIFY: true everywhere EXCEPT the_nether. A hoglin outside the nether converts.
	return e.hoglinDimension != dimNether
}

// hoglinConversionTick ports Hoglin.customServerAiStep conversion block: if isConverting(), ++timeInOverworld
// and finishConversion (-> Zoglin) once it EXCEEDS 300 (if_icmple 300 -> convert when > 300); else reset
// timeInOverworld to 0. finishConversion converts to a Zoglin (the overworld hoglin -> zoglin). Cite
// Hoglin.customServerAiStep + finishConversion + CONVERSION_TIME (300).
func (t *TickLoop) hoglinConversionTick(e *Entity) {
	if t.hoglinIsConverting(e) {
		e.hoglinTimeInOverworld++ // ++timeInOverworld
		if e.hoglinTimeInOverworld > hoglinConversionTime {
			t.hoglinFinishConversion(e) // convertTo(ZOGLIN, ...)
		}
	} else {
		e.hoglinTimeInOverworld = 0 // reset when back in the nether
	}
}

// hoglinFinishConversion ports Hoglin.finishConversion: convertTo(EntityType.ZOGLIN, ...). v1 mutates the
// entity type in place (the store keeps the same id/pos/health), the minimal conversion the bounded port
// needs -- a Zoglin is the overworld hoglin. The resulting Zoglin is a REAL, functioning mob: the type flag
// flips to zoglin (so zoglinAiStep drives its INDISCRIMINATE hostility + knock-up toss instead of the
// hoglin melee), and setZoglinAgeAttack refreshes ATTACK_DAMAGE to the zoglin adult/baby value (the hoglin
// e.ai + attributes are carried in place). The NAUSEA-200 client cue on the new zoglin is deferred. Cite
// Hoglin.finishConversion + Zoglin.
func (t *TickLoop) hoglinFinishConversion(e *Entity) {
	e.typ = entity.Zoglin.ID
	e.isHoglin = false
	e.isZoglin = true
	e.hoglinTimeInOverworld = 0
	e.hoglinAttackAnimTicks = 0 // reset the shared attack-anim counter for the zoglin
	setZoglinAgeAttack(e)       // Zoglin adult ATTACK_DAMAGE 6.0 / baby 0.5 (== hoglin values, kept explicit)
	if e.ai == nil {
		e.ai = &mobAI{}
		reseedMobAI(e.ai, e.id)
	}
}

// hoglinAiStep is the Hoglin per-tick drive: decrement the attack-anim ticks (Hoglin.aiStep), acquire the
// nearest player + melee (the brain-deferred goal-style attack), then the zoglin-conversion timer
// (customServerAiStep). Gated in tickAI on typ == entity.Hoglin.ID, AFTER serverAiStep. RNG only on the
// hoglin OWN stream (the damage roll + throw). Cite Hoglin.aiStep + doHurtTarget + customServerAiStep.
func (t *TickLoop) hoglinAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.hoglinAttackAnimTicks > 0 {
		e.hoglinAttackAnimTicks-- // Hoglin.aiStep: if(attackAnimationRemainingTicks>0) --
	}
	t.hoglinAcquireNearestPlayer(e)
	if target := t.hoglinTarget(e); target != nil {
		if e.meleeCooldown > 0 {
			e.meleeCooldown--
		} else if isWithinMeleeAttackRange(e, target) && t.sensingHasLineOfSight(e, target) {
			e.meleeCooldown = hoglinMeleeCooldown
			t.hoglinDoHurtTarget(e, target)
		}
	} else if e.meleeCooldown > 0 {
		e.meleeCooldown--
	}
	t.hoglinConversionTick(e) // the zoglin-conversion timer (overworld > 300 ticks)
}
