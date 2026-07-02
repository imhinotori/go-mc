package server

// vex.go -- the Vex entity (net.minecraft.world.entity.monster.Vex), a 1:1 port from the unobfuscated
// 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). The Vex is a FLYING Monster the Evoker's
// SUMMON_VEX spell (Evoker$EvokerSummonSpellGoal.performSpellCasting) spawns (setOwner/setBoundOrigin/
// setLimitedLife). It is a CODE-SPAWNED living mob (spawnVex), NOT a plugin-declared mob: it gets a minimal
// e.ai (a per-entity seeded rng + the attack-target slot) but NO goalSelector -- its whole behavior is the
// per-type vexAiStep driven from tickAI (the sibling of creeperAiStep / happyGhastAiStep / sulfurCubeAiStep,
// the established per-type code-driven AI seam). It flies via a direct delta-movement integration (Vex.tick
// sets noPhysics + noGravity, so the vex moves purely by its deltaMovement each tick).
//
// VANILLA (verified CFR Vex + its inner classes):
//
//	Vex.tick(): noPhysics=true; super.tick(); noPhysics=false; setNoGravity(true);
//	    if (hasLimitedLife && --limitedLifeTicks <= 0) { limitedLifeTicks = 20; hurt(starve(), 1.0); }
//	createAttributes: Monster.createMonsterAttributes().add(MAX_HEALTH, 14.0).add(ATTACK_DAMAGE, 4.0).
//	xpReward = 3. FLAG_IS_CHARGING = 1.
//	registerGoals: 0 FloatGoal; 4 VexChargeAttackGoal; 8 VexRandomMoveGoal; 9/10 LookAtPlayerGoal;
//	    target 1 HurtByTargetGoal(Raider); 2 VexCopyOwnerTargetGoal; 3 NearestAttackableTargetGoal(Player).
//
//	VexMoveControl.tick(): if (operation != MOVE_TO) return;
//	    Vec3 delta = new Vec3(wantedX-getX(), wantedY-getY(), wantedZ-getZ()); double len = delta.length();
//	    if (len < getBoundingBox().getSize()) { operation = WAIT; setDeltaMovement(getDeltaMovement()*0.5); }
//	    else { setDeltaMovement(getDeltaMovement() + delta.scale(speedModifier*0.05/len));
//	           if (getTarget()==null) yaw = -atan2(dx,dz)*57.295776; else yaw from target dx/dz; yBodyRot=yaw; }
//
//	VexChargeAttackGoal.canUse(): target alive && !moveControl.hasWanted() && random.nextInt(reducedTick
//	    Delay(7))==0 && distanceToSqr(target) > 4.0. start(): setWantedPosition(target.getEyePosition, 1.0);
//	    setIsCharging(true); playSound(VEX_CHARGE). canContinueToUse(): hasWanted && isCharging && target
//	    alive. tick(): if bb.intersects(target.bb) { doHurtTarget(target); setIsCharging(false); }
//	    else if (distanceToSqr(target) < 9.0) setWantedPosition(target.getEyePosition, 1.0).
//
//	VexRandomMoveGoal.canUse(): !moveControl.hasWanted() && random.nextInt(reducedTickDelay(7))==0.
//	    tick(): origin = boundOrigin!=null ? boundOrigin : blockPosition(); for (attempt<3) {
//	      pos = origin.offset(nextInt(15)-7, nextInt(11)-5, nextInt(15)-7); if (!isEmptyBlock(pos)) continue;
//	      setWantedPosition(pos.x+0.5, pos.y+0.5, pos.z+0.5, 0.25); if (target!=null) break;
//	      lookControl.setLookAt(...); break; }
//
//	VexCopyOwnerTargetGoal.canUse(): owner instanceof Targeting && owner.getTarget()!=null && canAttack(...).
//	    start(): setTarget(owner.getTarget()).
//
// The client-only flap counters (FLAP_DEGREES_PER_TICK / TICKS_PER_FLAP / isFlapping) are pure render and
// are NOT ported. LookAtPlayerGoal (9/10) is a LOOK-only visual (cite-deferred; the charge/move-control
// already set yaw). HurtByTargetGoal (target 1) retaliates against a Raider attacker -- there is no vex-vs-
// raider friendly fire in v1, so it is a structural no-op (the vex targets players via copy-owner + nearest).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Vex constants (VERIFIED CFR Vex + inner classes).
const (
	vexStarveResetTicks   = 20   // Vex.tick: on limited-life expiry, limitedLifeTicks = 20 (re-arm)
	vexStarveDamage       = 1.0  // Vex.tick: hurt(starve(), 1.0F)
	vexMoveAccelPerLen    = 0.05 // VexMoveControl: deltaMovement += delta.scale(speedModifier * 0.05 / len)
	vexArriveDamp         = 0.5  // VexMoveControl arrival: setDeltaMovement(getDeltaMovement().scale(0.5))
	vexChargeSpeed        = 1.0  // VexChargeAttackGoal: setWantedPosition(eye, 1.0) speedModifier
	vexRandomMoveSpeed    = 0.25 // VexRandomMoveGoal: setWantedPosition(pos, 0.25) speedModifier
	vexChargeCadence      = 7    // canUse: random.nextInt(reducedTickDelay(7)) == 0
	vexRandomMoveCadence  = 7    // canUse: random.nextInt(reducedTickDelay(7)) == 0
	vexChargeMinDistSqr   = 4.0  // VexChargeAttackGoal.canUse: distanceToSqr(target) > 4.0
	vexChargeReacquireSqr = 9.0  // VexChargeAttackGoal.tick: distanceToSqr(target) < 9.0 -> re-aim
	vexDegPerRad          = 57.295776
	// vexRandomMoveHorizSpan / vexRandomMoveVertSpan are the VexRandomMoveGoal offset ranges: nextInt(15)-7
	// (horizontal, +/-7) and nextInt(11)-5 (vertical, +/-5).
	vexRandomMoveHorizSpan = 15
	vexRandomMoveHorizBias = 7
	vexRandomMoveVertSpan  = 11
	vexRandomMoveVertBias  = 5
	vexRandomMoveAttempts  = 3
	// vexNearestTargetRange is NearestAttackableTargetGoal's default follow-range read (Attributes.FOLLOW_
	// RANGE) -- Monster follow range 16.0. The vex acquires the nearest player within this range.
	vexNearestTargetRange = 16.0
)

// spawnVex creates a Vex at (x,y,z) owned by the summoning evoker, with the bound origin and limited life
// the SUMMON_VEX spell computed, and adds it to the owner region's store (the tracker broadcasts AddEntity
// next tick). It attaches a MINIMAL e.ai (a per-entity seeded rng + the attack-target slot) so mobRandom(e)
// gives a per-vex deterministic stream and vexAiStep can store its target -- but registers NO goals (the
// behavior is the code-driven vexAiStep). initSpawnHealth seeds health from the folded MaxHealth (14.0).
// Cite Evoker$EvokerSummonSpellGoal.performSpellCasting (VEX.create + snapTo + setOwner + setBoundOrigin +
// setLimitedLife + addFreshEntity).
func (t *TickLoop) spawnVex(ownerID int32, x, y, z float64, boundX, boundY, boundZ int, limitedLifeTicks int) *Entity {
	v := NewEntity(t.idAlloc.AllocID(), entity.Vex, x, y, z)
	v.isVex = true
	v.vexOwnerID = ownerID
	v.vexBoundOriginX, v.vexBoundOriginY, v.vexBoundOriginZ = boundX, boundY, boundZ
	v.vexHasBoundOrigin = true
	v.vexHasLimitedLife = true
	v.vexLimitedLifeTicks = int32(limitedLifeTicks)

	// LivingEntity.<init> setHealth(getMaxHealth()): the vex spawns at its folded MAX_HEALTH (14.0). Read
	// AFTER NewEntity seeded the attribute map from the vex supplier.
	initSpawnHealth(v)

	// Minimal AI: the per-entity rng (mobRandom) + the attack-target slot. NO goalSelector is registered --
	// serverAiStep over an empty goalSelector/navigation is a safe no-op (no RNG), and the vex's behavior is
	// the per-type vexAiStep below. reseedMobAI derives the per-vex deterministic seed from the entity id.
	v.ai = &mobAI{}
	reseedMobAI(v.ai, v.id)

	owner := t.regionForEntity(v)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(v)
	return v
}

// vexAiStep ports Vex.tick + the vex goals + VexMoveControl.tick for ONE vex, driven per-type from tickAI
// (gated on typ == entity.Vex.ID, AFTER serverAiStep -- the empty goalSelector no-op). It runs in vanilla
// order: the Vex.tick limited-life starve, then the targetSelector (copy-owner-target -> nearest-player),
// then the goals (charge-attack -> random-move), then the move-control flight integration. NO RNG draw
// touches any other entity's stream (all draws are on the vex's OWN per-entity rng). Cite Vex + inner goals.
func (t *TickLoop) vexAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return // a dead vex does not fly (its corpse counts down via tickDeath)
	}

	// --- Vex.tick: setNoGravity(true) (implicit -- the vex never applies gravity here) + the limited-life
	// starve. `if (hasLimitedLife && --limitedLifeTicks <= 0) { limitedLifeTicks = 20; hurt(starve, 1.0); }`.
	if e.vexHasLimitedLife {
		e.vexLimitedLifeTicks--
		if e.vexLimitedLifeTicks <= 0 {
			e.vexLimitedLifeTicks = vexStarveResetTicks
			t.applyDamageEntity(e, damageSourceOf(damageTypeStarve), vexStarveDamage)
			if e.dead || e.health <= 0 {
				return // the starve was lethal -- stop (tickDeath removes the corpse)
			}
		}
	}

	// --- targetSelector (Mob.serverAiStep order: target goals before action goals). ---
	// @2 VexCopyOwnerTargetGoal: copy the owner (evoker)'s target if the vex has none. @3 NearestAttackable
	// TargetGoal(Player): else acquire the nearest live player within follow range. Both SET e.ai.attackTargetID.
	t.vexCopyOwnerTarget(e)
	t.vexAcquireNearestPlayer(e)

	// --- goals (action goals, priority order: charge@4 before random-move@8). Charge preempts random-move
	// via the MOVE flag (once charging holds a wanted position, random-move's !hasWanted() gate is false). ---
	if !t.vexChargeAttack(e) {
		t.vexRandomMove(e)
	}

	// --- VexMoveControl.tick: fly toward the wanted position (integrate deltaMovement), then step the vex. ---
	t.vexMoveControlTick(e)
	t.vexTravel(e)
}

// vexTarget reads the vex's current attack-target player (the Mob.getTarget() analogue via e.ai.attackTargetID),
// or nil. Tick-owned read.
func (t *TickLoop) vexTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// vexCopyOwnerTarget ports Vex$VexCopyOwnerTargetGoal: if the vex has no target and its owner (the evoker)
// has a live combat target, copy it. In v1 the owner's target is a player (the evoker's NearestAttackable
// TargetGoal target); the copyOwnerTargeting conditions (forNonCombat, ignoreLineOfSight, ignoreInvisibility)
// reduce to "the target player is alive". NO RNG. Cite Vex$VexCopyOwnerTargetGoal.canUse/start.
func (t *TickLoop) vexCopyOwnerTarget(e *Entity) {
	if e.ai == nil {
		return
	}
	if e.ai.attackTargetID != 0 {
		// The vex already has a target; canAttack still lets copy-owner refresh it to the owner's target in
		// vanilla (start unconditionally setTarget(owner.getTarget()) when canUse). But canUse ALSO fires only
		// when the owner has a target -- so a vex with a target keeps it unless the owner has a (new) one.
	}
	owner, ok := t.cur().entities.get(e.vexOwnerID)
	if !ok || owner == nil {
		return // owner gone (the evoker died) -- nothing to copy
	}
	// owner instanceof Targeting && owner.getTarget() != null: the evoker's target (a player id).
	if owner.ai == nil || owner.ai.attackTargetID == 0 {
		return
	}
	ownerTarget := t.playerByEntityID(owner.ai.attackTargetID)
	if ownerTarget == nil || ownerTarget.dead {
		return // canAttack(target, conditions): the target must be alive
	}
	e.ai.attackTargetID = owner.ai.attackTargetID // setTarget(owner.getTarget())
}

// vexAcquireNearestPlayer ports Vex's @3 NearestAttackableTargetGoal(Player): when the vex has no target,
// acquire the nearest live player within follow range (16.0). The full goal has an unseen-memory + a target
// conditions test; v1 reduces to "nearest live player within range" (the enderman/other-mob NearestAttackable
// pattern). NO RNG. Cite NearestAttackableTargetGoal.
func (t *TickLoop) vexAcquireNearestPlayer(e *Entity) {
	if e.ai == nil || e.ai.attackTargetID != 0 {
		return // already has a target (copy-owner or a retained acquisition) -- do not override
	}
	var best *tickPlayer
	bestSq := vexNearestTargetRange * vexNearestTargetRange
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
		e.ai.attackTargetID = best.entityID // Mob.setTarget(nearest)
	}
}

// vexChargeAttack ports Vex$VexChargeAttackGoal (canUse/start/canContinueToUse/tick folded into one per-tick
// step). It returns TRUE while the charge is active (holding the MOVE flag), so the caller skips random-move.
// canUse: target alive && !hasWanted && nextInt(reducedTickDelay(7))==0 && distanceToSqr(target) > 4.0.
// While charging: on bb-intersection doHurtTarget + stop charging; else if within 9.0 re-aim at the target's
// eye. Cite Vex$VexChargeAttackGoal.
func (t *TickLoop) vexChargeAttack(e *Entity) bool {
	target := t.vexTarget(e)

	if e.vexCharging {
		// canContinueToUse: hasWanted && isCharging && target alive. A lost target / cleared want ends it.
		if target == nil || !e.vexHasWant {
			e.vexCharging = false // stop(): setIsCharging(false)
			return false
		}
		// tick: if the vex's bb intersects the target's bb -> bite; else re-aim if within 9.0.
		if t.vexIntersectsPlayer(e, target) {
			t.vexDoHurtTarget(e, target)
			e.vexCharging = false // setIsCharging(false)
			return true
		}
		if distanceToSqrPlayer(target, e) < vexChargeReacquireSqr {
			ex, ey, ez := target.x, target.y+playerStandingEyeHeight, target.z // getEyePosition()
			e.vexWantX, e.vexWantY, e.vexWantZ = ex, ey, ez
			e.vexWantSpeed = vexChargeSpeed
			e.vexHasWant = true
		}
		return true
	}

	// canUse: needs a live target, no active wanted position, the 1-in-reducedTickDelay(7) roll, and dist>4.
	if target == nil || e.vexHasWant {
		return false
	}
	// The RNG roll (on the vex's OWN stream) -- drawn each tick the guard is reached, exactly like vanilla.
	if mobRandom(e).nextInt(reducedTickDelay(vexChargeCadence)) != 0 {
		return false
	}
	if distanceToSqrPlayer(target, e) <= vexChargeMinDistSqr {
		return false // distanceToSqr(target) > 4.0 required
	}
	// start(): setWantedPosition(target.getEyePosition(), 1.0); setIsCharging(true); playSound(VEX_CHARGE).
	ex, ey, ez := target.x, target.y+playerStandingEyeHeight, target.z
	e.vexWantX, e.vexWantY, e.vexWantZ = ex, ey, ez
	e.vexWantSpeed = vexChargeSpeed
	e.vexHasWant = true
	e.vexCharging = true
	return true
}

// vexRandomMove ports Vex$VexRandomMoveGoal: when no wanted position is active, roll the 1-in-reducedTickDelay
// (7) cadence and (on a hit) pick an empty block within +/-(7,5,7) of the bound origin (or the vex's current
// block) to wander to at speed 0.25. Up to 3 attempts to find an empty cell. NO target-look here (the LOOK is
// a cite-deferred visual). Cite Vex$VexRandomMoveGoal.canUse/tick.
func (t *TickLoop) vexRandomMove(e *Entity) {
	if e.vexHasWant {
		return // !moveControl.hasWanted() gate
	}
	r := mobRandom(e)
	if r.nextInt(reducedTickDelay(vexRandomMoveCadence)) != 0 {
		return
	}
	// origin = boundOrigin != null ? boundOrigin : blockPosition().
	ox, oy, oz := e.vexBoundOriginX, e.vexBoundOriginY, e.vexBoundOriginZ
	if !e.vexHasBoundOrigin {
		ox = int(math.Floor(e.x))
		oy = int(math.Floor(e.y))
		oz = int(math.Floor(e.z))
	}
	for attempt := 0; attempt < vexRandomMoveAttempts; attempt++ {
		// pos = origin.offset(nextInt(15)-7, nextInt(11)-5, nextInt(15)-7). The 3 draws fire IN ORDER every
		// attempt (matching the jar's per-iteration offset draws).
		px := ox + r.nextInt(vexRandomMoveHorizSpan) - vexRandomMoveHorizBias
		py := oy + r.nextInt(vexRandomMoveVertSpan) - vexRandomMoveVertBias
		pz := oz + r.nextInt(vexRandomMoveHorizSpan) - vexRandomMoveHorizBias
		if !t.vexIsEmptyBlock(px, py, pz) {
			continue // !isEmptyBlock(pos) -> try again
		}
		// setWantedPosition(px+0.5, py+0.5, pz+0.5, 0.25).
		e.vexWantX = float64(px) + 0.5
		e.vexWantY = float64(py) + 0.5
		e.vexWantZ = float64(pz) + 0.5
		e.vexWantSpeed = vexRandomMoveSpeed
		e.vexHasWant = true
		// if (getTarget() != null) break; else lookControl.setLookAt(...); break. Either way ONE commit ends
		// the loop -- the LOOK is a cite-deferred visual, so both branches just break after the commit.
		break
	}
}

// vexMoveControlTick ports Vex$VexMoveControl.tick: if a wanted position is active (Operation.MOVE_TO), steer
// the deltaMovement toward it. On arrival (delta length < boundingBox.getSize()) it damps the velocity by 0.5
// and clears the want (Operation.WAIT); else it accelerates toward the target and sets the flight yaw. Cite
// Vex$VexMoveControl.tick.
func (t *TickLoop) vexMoveControlTick(e *Entity) {
	if !e.vexHasWant {
		return // operation != MOVE_TO
	}
	dx := e.vexWantX - e.x
	dy := e.vexWantY - e.y
	dz := e.vexWantZ - e.z
	length := math.Sqrt(dx*dx + dy*dy + dz*dz) // Vec3.length()

	// getBoundingBox().getSize() == (xsize + ysize + zsize)/3.0 (AABB.getSize).
	size := (e.width + e.height + e.width) / 3.0
	if length < size {
		// Arrived: WAIT + damp the velocity by 0.5. Clearing hasWant is the Operation.WAIT (hasWanted()==false).
		e.vexHasWant = false
		e.vx *= vexArriveDamp
		e.vy *= vexArriveDamp
		e.vz *= vexArriveDamp
		return
	}

	// setDeltaMovement(getDeltaMovement() + delta.scale(speedModifier * 0.05 / len)).
	scale := e.vexWantSpeed * vexMoveAccelPerLen / length
	e.vx += dx * scale
	e.vy += dy * scale
	e.vz += dz * scale

	// Yaw: face the target if targeting, else the flight heading. yaw = -atan2(dx,dz)*57.295776; yBodyRot=yaw.
	if target := t.vexTarget(e); target == nil {
		e.yaw = float32(-math.Atan2(e.vx, e.vz) * vexDegPerRad)
	} else {
		tx := target.x - e.x
		tz := target.z - e.z
		e.yaw = float32(-math.Atan2(tx, tz) * vexDegPerRad)
	}
	e.headYaw = e.yaw // yBodyRot = getYRot()
}

// vexTravel integrates the vex's deltaMovement into its position (Vex.tick's noPhysics + noGravity flight:
// the vex moves purely by its deltaMovement each tick, with no collision and no gravity). It steps the vex
// via entities.move (which re-buckets so the tracker broadcasts its motion). This is the v1 stand-in for
// LivingEntity.travel with a flying (noGravity) mob -- the observable "the vex flies straight toward its
// wanted position". Cite Vex.tick (noPhysics=true; setNoGravity(true)).
func (t *TickLoop) vexTravel(e *Entity) {
	if e.vx == 0 && e.vy == 0 && e.vz == 0 {
		return
	}
	t.cur().entities.move(e, e.x+e.vx, e.y+e.vy, e.z+e.vz)
}

// vexDoHurtTarget ports Vex(Mob).doHurtTarget for a PLAYER victim: deal ATTACK_DAMAGE (4.0 for a vex) to the
// target via the player hurt path. The source is mob_attack attributed to the vex id (no weapon). Cite
// Mob.doHurtTarget + the vex's ATTACK_DAMAGE 4.0.
func (t *TickLoop) vexDoHurtTarget(e *Entity, target *tickPlayer) {
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // (float) getAttributeValue(ATTACK_DAMAGE)
	src := damageSourceMobAttack(e.id)                          // getWeaponItem().getDamageSource(this) -> mob_attack
	t.applyDamage(target, src, dmg)
}

// vexIntersectsPlayer reports whether the vex's feet-anchored AABB (width x height) intersects the player's
// collision box (playerWidth x playerHeight). VexChargeAttackGoal.tick uses getBoundingBox().intersects(
// target.getBoundingBox()). Cite Vex$VexChargeAttackGoal.tick.
func (t *TickLoop) vexIntersectsPlayer(e *Entity, p *tickPlayer) bool {
	hw := e.width / 2
	loX, hiX := e.x-hw, e.x+hw
	loY, hiY := e.y, e.y+e.height
	loZ, hiZ := e.z-hw, e.z+hw
	return boxIntersectsPlayer(p, loX, loY, loZ, hiX, hiY, hiZ)
}

// vexIsEmptyBlock ports Level.isEmptyBlock(BlockPos) for the random-move target test: the cell is air (no
// solid, no fluid). v1 reuses isSolidAt's inverse -- isEmptyBlock == !isSolidAt (air or water reads as
// empty). The vex wanders to an air cell to fly there. Cite Vex$VexRandomMoveGoal.tick (isEmptyBlock).
func (t *TickLoop) vexIsEmptyBlock(x, y, z int) bool {
	return !t.isSolidAt(pk.Position{X: x, Y: y, Z: z})
}
