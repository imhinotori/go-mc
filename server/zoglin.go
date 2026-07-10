package server

// zoglin.go -- the Zoglin (net.minecraft.world.entity.monster.Zoglin implements HoglinBase), a 1:1 port from
// the unobfuscated 26.2 jar. A Zoglin is what a Hoglin BECOMES after too long outside the nether (the
// TERMINAL undead form -- it NEVER converts back or further). Unlike the neutral hoglin/piglin, a Zoglin is
// INDISCRIMINATELY HOSTILE: its brain attacks ANY LivingEntity (players AND all mobs) except other zoglins
// and creepers. On a landed hit it FLINGS the target upward (the shared HoglinBase.hurtAndThrowTarget
// knock-up toss). Code-spawned (spawnZoglin) OR produced in-place by hoglinFinishConversion; its per-tick
// drive is zoglinAiStep from tickAI (per-type-gated on typ == entity.Zoglin.ID).
//
// VANILLA (verified javap Zoglin + HoglinBase this session):
//   createAttributes = Monster.createMonsterAttributes + MAX_HEALTH 40.0 + MOVEMENT_SPEED 0.30000001192092896
//     + KNOCKBACK_RESISTANCE 0.6000000238418579 + ATTACK_KNOCKBACK 1.0 + ATTACK_DAMAGE 6.0.
//   setBaby(true): getAttribute(ATTACK_DAMAGE).setBaseValue(0.5) -- a baby zoglin hits for 0.5. isAdult() =
//     !isBaby(). BABY_DIMENSIONS scalable(0.75, 0.85).
//   doHurtTarget(level, entity): if !(entity instanceof LivingEntity) return false; attackAnimationRemaining
//     Ticks = 10; broadcastEntityEvent(this, (byte)4); makeSound(ZOGLIN_ATTACK); return
//     HoglinBase.hurtAndThrowTarget(level, this, le).
//   findNearestValidAttackTarget (brain): accepts any LivingEntity that is !is(ZOGLIN) AND !is(CREEPER) AND
//     Sensor.isEntityAttackable -- INDISCRIMINATE hostility (players + all mobs, excluding zoglin/creeper).
//   hurtServer retaliation: if canAttack(le) AND not-much-further-than-current -> setAttackTarget(le)
//     (ATTACK_TARGET memory, 200-tick expiry).
//   aiStep(): if attackAnimationRemainingTicks > 0 --. customServerAiStep: brain.tick; updateActivity (no
//     conversion, no anger, no timeInOverworld -- TERMINAL). canBeLeashed(): true.
//   HoglinBase.hurtAndThrowTarget(level, attacker, target): f = ATTACK_DAMAGE; f2 = (!isBaby AND (int)f>0) ?
//     f/2 + level.getRandom().nextInt((int)f) : f; flag = target.hurtServer(mobAttack, f2); if flag AND
//     !isBaby throwTarget(attacker, target); return flag.
//   HoglinBase.throwTarget(attacker, target): d6 = ATTACK_KNOCKBACK(attacker) - KNOCKBACK_RESISTANCE(target);
//     if d6 <= 0 return; d8 = target.x - attacker.x; d10 = target.z - attacker.z; f13 = nextInt(21)-10;
//     d14 = d6 * (nextFloat()*0.5 + 0.2); vec = Vec3(d8,0,d10).normalize().scale(d14).yRot(f13);
//     d17 = d6 * nextFloat() * 0.5; target.push(vec.x, d17, vec.z); target.hurtMarked = true.
//
// NOTE: vanilla HoglinBase.throwTarget/hurtAndThrowTarget draw on attacker.level().getRandom() (the ServerLevel
// random), NOT the mob own stream. The existing hoglin.go port draws on mobRandom(e); this Zoglin port shares
// that hoglin.go helper (hoglinHurtAndThrowTarget/hoglinThrowTarget) verbatim to stay in lockstep with the
// hoglin -- the formula + draw ORDER match; the stream identity is the SAME cited deviation the hoglin carries.
//
// v1 STUBS (cited): the BRAIN (ZoglinAi sensors/activities: the nearest-attackable-target memory, the
// MeleeAttack + RunSometimes behaviors, updateActivity FIGHT/IDLE + playAngrySound) is the goal-style melee
// layered over the faithful attributes + hurtAndThrowTarget; the attack-animation ticks + ZOGLIN_ATTACK
// sound are client cues. Observable gameplay (attributes, INDISCRIMINATE hostility toward players AND mobs
// except zoglin/creeper, the adult/baby damage split, the knock-up toss, TERMINAL/no-conversion) is EXACT.

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Zoglin constants (VERIFIED javap Zoglin this session).
const (
	zoglinMaxHealth           = 40.0                // MAX_HEALTH 40.0
	zoglinMovementSpeed       = 0.30000001192092896 // MOVEMENT_SPEED
	zoglinKnockbackResistance = 0.6000000238418579  // KNOCKBACK_RESISTANCE
	zoglinAttackKnockback     = 1.0                 // ATTACK_KNOCKBACK
	zoglinAttackDamage        = 6.0                 // adult ATTACK_DAMAGE
	zoglinBabyAttackDamage    = 0.5                 // baby ATTACK_DAMAGE (setBaby sets base 0.5)
	zoglinAttackAnimTicks     = 10                  // attackAnimationRemainingTicks on a hit
	// zoglinAttackTargetDuration is the ATTACK_TARGET memory expiry the Zoglin retaliation sets:
	// Zoglin.setAttackTarget -> brain.setMemoryWithExpiry(ATTACK_TARGET, le, 200L). Verified javap.
	zoglinAttackTargetDuration = 200 // setMemoryWithExpiry(ATTACK_TARGET, ..., 200L)
	// zoglinRetaliateMuchFurther is the BehaviorUtils.isOtherTargetMuchFurtherAwayThanCurrentAttackTarget
	// distance passed by Zoglin.hurtServer: 4.0 (the new attacker latches unless it is >4.0 blocks farther
	// than the current attack target). Verified javap Zoglin.hurtServer (ldc2_w 4.0d).
	zoglinRetaliateMuchFurther = 4.0
	// zoglinMeleeCooldownAdult / zoglinMeleeCooldownBaby are the MeleeAttack.create(cooldownBetweenAttacks)
	// intervals the Zoglin brain registers: MeleeAttack.create(40) under triggerIf(isAdult) and
	// MeleeAttack.create(15) for the baby. Each swing sets ATTACK_COOLING_DOWN for that many ticks, so the
	// effective per-swing interval is 40 (adult) / 15 (baby). Verified javap Zoglin brain (bipush 40 / 15
	// -> MeleeAttack.create(I)). This REPLACES the shared meleeAttackResetCooldown(20) for the zoglin.
	zoglinMeleeCooldownAdult = 40 // triggerIf(isAdult) MeleeAttack.create(40)
	zoglinMeleeCooldownBaby  = 15 // MeleeAttack.create(15)
)

// spawnZoglin creates a Zoglin at (x,y,z) with the jar attributes and adds it to the owner region store. It
// is TERMINAL (no conversion). Minimal e.ai; NO goalSelector (behavior is the code-driven zoglinAiStep, like
// spawnHoglin). initSpawnHealth seeds MAX_HEALTH 40.0. baby sets the 0.5 ATTACK_DAMAGE base. Cite
// Zoglin.createAttributes + setBaby.
func (t *TickLoop) spawnZoglin(x, y, z float64, baby bool) *Entity {
	zg := NewEntity(t.idAlloc.AllocID(), entity.Zoglin, x, y, z)
	zg.isZoglin = true
	if baby {
		zg.breedAge = babyStartAge
	}
	setZoglinAgeAttack(zg)
	initSpawnHealth(zg)
	zg.ai = &mobAI{}
	reseedMobAI(zg.ai, zg.id)
	owner := t.regionForEntity(zg)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return zg
	}
	owner.entities.add(zg)
	return zg
}

// setZoglinAgeAttack ports Zoglin.setBaby: baby -> ATTACK_DAMAGE base 0.5, dims scaled; adult -> 6.0. NO RNG.
// Cite Zoglin.setBaby + getDefaultDimensions.
func setZoglinAgeAttack(e *Entity) {
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			if e.isBaby() {
				inst.SetBaseValue(zoglinBabyAttackDamage)
			} else {
				inst.SetBaseValue(zoglinAttackDamage)
			}
		}
	}
	if e.isBaby() {
		e.width = e.adultWidth * babyDimensionScale
		e.height = e.adultHeight * babyDimensionScale
	} else {
		e.width = e.adultWidth
		e.height = e.adultHeight
	}
}

// zoglinValidTarget ports the Zoglin brain findNearestValidAttackTarget predicate: a candidate mob is
// attackable iff it is NOT a zoglin and NOT a creeper (Sensor.isEntityAttackable is the alive/present check
// the caller already does). The INDISCRIMINATE hostility keystone -- every other living entity is fair game.
// Cite Zoglin.findNearestValidAttackTarget.
func zoglinValidTarget(typ entity.ID) bool {
	return typ != entity.Zoglin.ID && typ != entity.Creeper.ID
}

// zoglinAcquireNearestTarget ports the brain nearest-attackable-target scan: pick the NEAREST valid target
// among ALL players AND ALL mobs (except zoglins/creepers) within FOLLOW_RANGE -- INDISCRIMINATE hostility.
// A dead/removed/out-of-range/now-invalid current target is dropped, then the whole set is rescanned. NO RNG.
// Cite Zoglin.findNearestValidAttackTarget + ZoglinAi.
func (t *TickLoop) zoglinAcquireNearestTarget(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange)
	rangeSqr := followRange * followRange
	// RETALIATION LATCH (Zoglin.hurtServer -> setMemoryWithExpiry(ATTACK_TARGET, le, 200L)): while the
	// latch is unexpired AND its target is still a live/present combat target, HOLD it -- do NOT re-scan
	// for a nearer one and do NOT drop it for leaving FOLLOW_RANGE (the memory expiry, not a distance
	// check, ends a latched grudge). The memory get() drops the target when it is no longer a present
	// LivingEntity. Cite Zoglin.setAttackTarget + Brain ATTACK_TARGET expiry semantics.
	if e.zoglinAttackTargetExpiry != 0 {
		if t.gametime >= e.zoglinAttackTargetExpiry {
			// The ATTACK_TARGET memory lapsed after 200 ticks: it clears WITH its value (the brain's
			// StartAttacking then re-picks the nearest valid target on the fresh scan below). Clearing the
			// id here is what lets a nearer target win once the grudge ends. Cite Brain expiry (memory + value).
			e.zoglinAttackTargetExpiry = 0
			e.ai.attackTargetID = 0
		} else if e.ai.attackTargetID != 0 && t.zoglinTargetPresent(e, e.ai.attackTargetID) {
			return // latched grudge still active; keep the current target
		} else {
			// the latched target vanished (dead/removed) -> the memory clears with it.
			e.zoglinAttackTargetExpiry = 0
			e.ai.attackTargetID = 0
		}
	}
	// Drop a stale current target.
	if e.ai.attackTargetID != 0 {
		if !t.zoglinTargetStillValid(e, e.ai.attackTargetID, rangeSqr) {
			e.ai.attackTargetID = 0
		} else {
			return
		}
	}
	var bestID int32
	bestSq := rangeSqr
	// Players (indiscriminate: a zoglin attacks players).
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		dsq := distanceToSqrPlayer(p, e)
		if dsq <= bestSq {
			bestSq = dsq
			bestID = p.entityID
		}
	}
	// Mobs (indiscriminate: any living entity except zoglin/creeper), from the zoglin owner region store.
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner != nil && owner.entities != nil {
		for _, other := range owner.entities.all() {
			if other == nil || other.id == e.id || other.dead || !other.isAlive() {
				continue
			}
			if !zoglinValidTarget(other.typ) {
				continue // !is(ZOGLIN) AND !is(CREEPER)
			}
			dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
			dsq := dx*dx + dy*dy + dz*dz
			if dsq <= bestSq {
				bestSq = dsq
				bestID = other.id
			}
		}
	}
	if bestID != 0 {
		e.ai.attackTargetID = bestID
	}
}

// zoglinTargetStillValid reports whether the current target id resolves to a live, in-range, still-valid
// combat target (a present player, or a mob that is not a zoglin/creeper). The canAttack + range keep-check.
func (t *TickLoop) zoglinTargetStillValid(e *Entity, id int32, rangeSqr float64) bool {
	if p := t.playerByEntityID(id); p != nil {
		return !p.dead && distanceToSqrPlayer(p, e) <= rangeSqr
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return false
	}
	other, ok := owner.entities.get(id)
	if !ok || other == nil || other.dead || !other.isAlive() || !zoglinValidTarget(other.typ) {
		return false
	}
	dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
	return dx*dx+dy*dy+dz*dz <= rangeSqr
}

// zoglinDoHurtTarget ports Zoglin.doHurtTarget: attackAnimationRemainingTicks = 10, broadcast the swing,
// then HoglinBase.hurtAndThrowTarget. The victim may be a PLAYER or a MOB (indiscriminate) -- the player
// path reuses the hoglin.go hurtAndThrowTarget verbatim (same math + draw order); the mob path uses the
// entity-victim hurt + the shared throw math. Cite Zoglin.doHurtTarget + HoglinBase.hurtAndThrowTarget.
func (t *TickLoop) zoglinDoHurtTarget(e *Entity, targetID int32) {
	e.hoglinAttackAnimTicks = zoglinAttackAnimTicks // attackAnimationRemainingTicks = 10 (shared field)
	// PLAYER victim: reuse the hoglin player hurtAndThrowTarget (broadcasts swing + throw internally).
	if p := t.playerByEntityID(targetID); p != nil {
		t.hoglinHurtAndThrowTarget(e, p)
		return
	}
	// MOB victim (indiscriminate hostility): the entity hurt path + the shared knock-up toss.
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return
	}
	other, ok := owner.entities.get(targetID)
	if !ok || other == nil {
		return
	}
	t.broadcastMobSwing(e)
	t.zoglinHurtAndThrowTargetEntity(e, other)
}

// zoglinHurtAndThrowTargetEntity ports HoglinBase.hurtAndThrowTarget for a MOB victim: compute the damage
// roll (adult: f/2 + nextInt((int)f); baby: f), hurt the mob, then -- on a landed hit and if not a baby --
// throwTargetEntity (the knock-up toss). The RNG is on the zoglin OWN stream (the SAME cited stream-identity
// deviation the hoglin port carries). Cite HoglinBase.hurtAndThrowTarget.
func (t *TickLoop) zoglinHurtAndThrowTargetEntity(e, victim *Entity) {
	f := float32(e.getAttributeValue(attribute.AttackDamage))
	var dmg float32
	if !e.isBaby() && int(f) > 0 {
		dmg = f/2.0 + float32(mobRandom(e).nextInt(int(f)))
	} else {
		dmg = f
	}
	src := damageSourceMobAttack(e.id)
	hurt := !victim.dead && victim.isAlive()
	if hurt && float32(victim.invulnerableTime) > hurtCooldownConst {
		amt := dmg
		if amt < 0 {
			amt = 0
		}
		hurt = amt > victim.lastHurt
	}
	t.applyDamageEntity(victim, src, dmg)
	if !hurt {
		return
	}
	if !e.isBaby() {
		t.zoglinThrowTargetEntity(e, victim)
	}
}

// zoglinThrowTargetEntity ports HoglinBase.throwTarget for a MOB victim: delta = ATTACK_KNOCKBACK(attacker) -
// KNOCKBACK_RESISTANCE(victim); if delta <= 0 return. Horizontal vector attacker->victim, normalized, scaled
// by delta*(nextFloat()*0.5+0.2), yRot by (nextInt(21)-10) radians; vertical delta*nextFloat()*0.5. push
// (add to deltaMovement) via pushEntityImpulse. RNG order EXACT: nextInt(21), nextFloat, nextFloat -- the
// SAME order/formula as hoglinThrowTarget. Cite HoglinBase.throwTarget.
func (t *TickLoop) zoglinThrowTargetEntity(e, victim *Entity) {
	knockback := e.getAttributeValue(attribute.AttackKnockback)
	resistance := victim.getAttributeValue(attribute.KnockbackResistance)
	delta := knockback - resistance
	if delta <= 0.0 {
		return
	}
	dx := victim.x - e.x
	dz := victim.z - e.z
	rng := mobRandom(e)
	yRotRad := float32(rng.nextInt(21) - 10)
	horiz := delta * float64(rng.nextFloat()*0.5+0.2)
	nx, ny, nz := normalizeVec3(dx, 0.0, dz)
	sx, _, sz := nx*horiz, ny*horiz, nz*horiz
	cos := float64(mthCosf(yRotRad))
	sin := float64(mthSinf(yRotRad))
	hx := sx*cos + sz*sin
	hz := sz*cos - sx*sin
	vy := delta * float64(rng.nextFloat()) * 0.5
	pushEntityImpulse(victim, hx, vy, hz)
}

// zoglinAiStep is the Zoglin per-tick drive (ports aiStep + customServerAiStep): decrement the attack-anim
// ticks, acquire the nearest INDISCRIMINATE target (players + mobs except zoglin/creeper), then the goal-
// style melee that FLINGS the target. TERMINAL -- no conversion, no anger. Gated in tickAI on typ ==
// entity.Zoglin.ID, AFTER serverAiStep. RNG only on the mob OWN stream (the damage roll + throw). Cite
// Zoglin.aiStep + doHurtTarget + customServerAiStep.
func (t *TickLoop) zoglinAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.hoglinAttackAnimTicks > 0 {
		e.hoglinAttackAnimTicks-- // aiStep: if attackAnimationRemainingTicks>0 --
	}
	t.zoglinAcquireNearestTarget(e)
	if e.ai != nil && e.ai.attackTargetID != 0 {
		targetID := e.ai.attackTargetID
		if e.meleeCooldown > 0 {
			e.meleeCooldown--
		} else if t.zoglinWithinMeleeRange(e, targetID) {
			// MeleeAttack.create(cooldownBetweenAttacks): the swing sets ATTACK_COOLING_DOWN for the per-age
			// interval (40 adult / 15 baby) -- NOT the shared MeleeAttackGoal.resetAttackCooldown(20).
			// Cite Zoglin brain (triggerIf(isAdult) MeleeAttack.create(40) / MeleeAttack.create(15)).
			if e.isBaby() {
				e.meleeCooldown = zoglinMeleeCooldownBaby
			} else {
				e.meleeCooldown = zoglinMeleeCooldownAdult
			}
			t.zoglinDoHurtTarget(e, targetID)
		}
	} else if e.meleeCooldown > 0 {
		e.meleeCooldown--
	}
}

// zoglinWithinMeleeRange reports whether the target (player or mob) is within the zoglin melee reach. Player
// targets use the shared isWithinMeleeAttackRange (the inflated attack box); mob targets use the equivalent
// center-distance check against the same default reach + the combined half-widths.
func (t *TickLoop) zoglinWithinMeleeRange(e *Entity, targetID int32) bool {
	if p := t.playerByEntityID(targetID); p != nil {
		return isWithinMeleeAttackRange(e, p) && t.sensingHasLineOfSight(e, p)
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return false
	}
	other, ok := owner.entities.get(targetID)
	if !ok || other == nil {
		return false
	}
	reach := defaultAttackReach
	hw := e.width/2 + reach + other.width/2
	dx := other.x - e.x
	dz := other.z - e.z
	if dx*dx+dz*dz > hw*hw {
		return false
	}
	// Vertical band: [y - reach/2, y + height + reach/2] must overlap the victim's feet..head.
	return other.y <= e.y+e.height+reach/2 && other.y+other.height >= e.y-reach/2
}

// zoglinTargetPresent reports whether the id resolves to a still-PRESENT combat target -- a live player,
// or a live mob that is not a zoglin/creeper. This is the ATTACK_TARGET memory get() presence check
// (LivingEntity present + valid), WITHOUT the FOLLOW_RANGE distance gate: a latched (retaliated) target
// is held by its 200-tick memory expiry, not by distance. Cite Zoglin ATTACK_TARGET memory semantics.
func (t *TickLoop) zoglinTargetPresent(e *Entity, id int32) bool {
	if p := t.playerByEntityID(id); p != nil {
		return !p.dead
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return false
	}
	other, ok := owner.entities.get(id)
	if !ok || other == nil || other.dead || !other.isAlive() {
		return false
	}
	return zoglinValidTarget(other.typ)
}

// zoglinCanAttack ports Mob.canAttack(LivingEntity) as used by Zoglin.hurtServer's retaliation gate:
// `!le.is(GHAST) && LivingEntity.canAttack(le)` -- the attacker must not be a ghast AND must be a valid,
// live combat target (canBeSeenAsEnemy: alive/present). v1 reduces LivingEntity.canAttack to "the
// attacker is a present, live entity" (the same present-target proxy the acquire scan uses); the GHAST
// exclusion is a genuine type read. Cite Mob.canAttack (le.is(GHAST) short-circuit -> super.canAttack).
func (t *TickLoop) zoglinCanAttack(e *Entity, attackerID int32) bool {
	if p := t.playerByEntityID(attackerID); p != nil {
		return !p.dead // a player is never a ghast; canAttack(player) reduces to alive/present
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return false
	}
	other, ok := owner.entities.get(attackerID)
	if !ok || other == nil || other.dead || !other.isAlive() {
		return false
	}
	if other.typ == entity.Ghast.ID || other.typ == entity.HappyGhast.ID {
		return false // Mob.canAttack: le.is(GHAST) -> false
	}
	return true
}

// zoglinPosDistSqr returns the squared position distance from the zoglin to an entity id (player or mob),
// or (0, false) if the id does not resolve -- the BehaviorUtils.distanceToSqr(position) helper.
func (t *TickLoop) zoglinPosDistSqr(e *Entity, id int32) (float64, bool) {
	if p := t.playerByEntityID(id); p != nil {
		return distanceToSqrPlayer(p, e), true
	}
	owner := t.regionForEntity(e)
	if owner == nil {
		owner = t.cur()
	}
	if owner == nil || owner.entities == nil {
		return 0, false
	}
	other, ok := owner.entities.get(id)
	if !ok || other == nil {
		return 0, false
	}
	dx, dy, dz := other.x-e.x, other.y-e.y, other.z-e.z
	return dx*dx + dy*dy + dz*dz, true
}

// zoglinIsOtherTargetMuchFurther ports BehaviorUtils.isOtherTargetMuchFurtherAwayThanCurrentAttackTarget(
// this, other, d): if there is NO current ATTACK_TARGET memory -> false (the new attacker always latches);
// else `otherDistSqr > currentDistSqr + d*d`. Verified javap BehaviorUtils (Optional.isEmpty -> false;
// else other.distanceToSqr > current.distanceToSqr + d*d). The "current ATTACK_TARGET" here is the zoglin's
// live attack-target id (the brain's ATTACK_TARGET memory analogue). Cite BehaviorUtils.
func (t *TickLoop) zoglinIsOtherTargetMuchFurther(e *Entity, otherID int32, d float64) bool {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return false // Optional.isEmpty() -> return false
	}
	curSq, ok := t.zoglinPosDistSqr(e, e.ai.attackTargetID)
	if !ok {
		return false // the current target vanished -> no "current" to compare against
	}
	otherSq, ok := t.zoglinPosDistSqr(e, otherID)
	if !ok {
		return false
	}
	return otherSq > curSq+d*d
}

// zoglinSetAttackTarget ports Zoglin.setAttackTarget(LivingEntity): eraseMemory(CANT_REACH_WALK_TARGET_SINCE)
// then setMemoryWithExpiry(ATTACK_TARGET, le, 200L) -- a retaliation latch that survives 200 ticks even if
// the attacker leaves FOLLOW_RANGE. In v1 the ATTACK_TARGET memory is e.ai.attackTargetID + the expiry
// gametime stamp (CANT_REACH_WALK_TARGET_SINCE is a pathfinding memory with no v1 reader -> cited no-op).
// Cite Zoglin.setAttackTarget.
func (t *TickLoop) zoglinSetAttackTarget(e *Entity, le int32) {
	if e.ai == nil {
		return
	}
	// eraseMemory(CANT_REACH_WALK_TARGET_SINCE): pathfinding give-up memory, no v1 reader (cited no-op).
	e.ai.attackTargetID = le                                                 // setMemory(ATTACK_TARGET, le)
	e.zoglinAttackTargetExpiry = t.gametime + int64(zoglinAttackTargetDuration) // ...WithExpiry(200L)
}

// zoglinHurtServerRetaliate ports Zoglin.hurtServer's retaliation tail (run after a landed hit): if the
// causing entity is a LivingEntity, and canAttack(le), and it is NOT much-further-away (>4.0) than the
// current attack target, setAttackTarget(le) -- the 200-tick grudge latch. RNG-free. It is a per-type
// post-hurt hook (the sibling of endermanHurtTeleport / silverfishNotifyHurt), gated on isZoglin and run
// after the shared applyDamageEntity hit landed. Cite Zoglin.hurtServer (bytecode 41-68).
func (t *TickLoop) zoglinHurtServerRetaliate(e *Entity, src damageSource) {
	// getEntity() instanceof LivingEntity: v1's src.attacker != 0 is the living-attacker proxy (a player
	// or a mob; an environmental hit leaves it 0).
	if src.attacker == 0 {
		return
	}
	if !t.zoglinCanAttack(e, src.attacker) {
		return // canAttack(le) == false
	}
	if t.zoglinIsOtherTargetMuchFurther(e, src.attacker, zoglinRetaliateMuchFurther) {
		return // the attacker is >4.0 farther than the current target -> keep the current target
	}
	t.zoglinSetAttackTarget(e, src.attacker)
}
