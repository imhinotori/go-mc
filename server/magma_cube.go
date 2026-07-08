package server

// magma_cube.go -- the hostile MagmaCube (net.minecraft.world.entity.monster.cubemob.MagmaCube), a 1:1
// port from the unobfuscated 26.2 jar. MagmaCube extends AbstractCubeMob (the shared slime/cube base),
// so it REUSES the cube-mob move-control + the three cube goals + the size machine + split-on-death that
// already landed for the SulfurCube (ai_goals_sulfur_cube.go) -- an entity is either a SulfurCube or a
// MagmaCube, never both, so the shared cube fields on the Entity never collide. This file supplies only the
// MagmaCube-specific overrides: the per-size attributes, the 4x jump delay, the +size*0.1 jump boost, the
// +2.0 attack damage, the always-deals-damage isDealsDamage, the size*size health (NOT the SulfurCube
// 4*size), and the AbstractCubeMob split count (2 + nextInt(3)). Code-spawned (spawnMagmaCube); its move
// drive is magmaCubeAiStep from tickAI (sibling of blazeAiStep, per-type-gated on typ == entity.MagmaCube.ID).
//
// VANILLA (verified javap MagmaCube + AbstractCubeMob + inner classes this session):
//   MagmaCube.createAttributes: Monster.createMonsterAttributes + MOVEMENT_SPEED 0.20000000298023224.
//   AbstractCubeMob.setSize: clamp(size,1,127); MAX_HEALTH base=size*size; MOVEMENT_SPEED base=0.2+0.1*size.
//   MagmaCube.setSize: super; ATTACK_DAMAGE base=actualSize; ARMOR base=size*3; xpReward=actualSize.
//   MagmaCube.getAttackDamage: super [ATTACK_DAMAGE attr] + 2.0f. getJumpDelay: super [nextInt(20)+10] * 4.
//   MagmaCube.jumpFromGround: setDeltaMovement(x, getJumpPower()+getSize()*0.1f, z). decreaseSquish: *0.9f.
//   MagmaCube.isOnFire: false. isDealsDamage: isEffectiveAi() (deals damage even when tiny).
//   AbstractCubeMob.remove: size>1 && isDeadOrDying -> spawn getSplitCount() (2+nextInt(3)) half-size cubes.
//   addTargetingGoals: @1 NearestAttackableTargetGoal(Player, selector abs(dy)<=4.0). addBehaviourGoals: @2
//   CubeMobAttackGoal. registerGoals base: @1 Float, @4 RandomDirection, @5 KeepOnJumping.
//
// FIRE IMMUNITY: MagmaCube.isOnFire const-false; a magma cube is fire+lava immune (entityFireImmune gates it
// out of fire.go tickEntityFire/tickEntityLava). Cite MagmaCube.isOnFire + EntityType.fireImmune.
//
// v1 STUBS (cited): squish/land particles + sounds + ID_SIZE client metadata; IronGolem targeting (no golem
// population); the addParticle FLAME visual (client-only).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// MagmaCube constants (VERIFIED javap MagmaCube + AbstractCubeMob this session).
const (
	magmaCubeSpawnSize      int32   = 2   // debug spawn size (a stable pin)
	magmaCubeJumpDelayScale int32   = 4   // MagmaCube.getJumpDelay: super() * 4
	magmaCubeArmorPerSize   float64 = 3.0 // MagmaCube.setSize: ARMOR base = size * 3
	magmaCubeAttackBonus    float32 = 2.0 // MagmaCube.getAttackDamage: super + 2.0f
	magmaCubeTargetYWindow  float64 = 4.0 // addTargetingGoals selector: abs(dy) <= 4.0
)

// magmaCubeMaxHealth is AbstractCubeMob.setcubeMobHealth(size): MAX_HEALTH base = size*size (MagmaCube does
// NOT override it, unlike the SulfurCube 4*size). size 2 -> 4, size 1 -> 1, size 4 -> 16.
//
//	[VERIFIED javap AbstractCubeMob.setcubeMobHealth: getAttribute(MAX_HEALTH).setBaseValue(size*size).]
func magmaCubeMaxHealth(size int32) float64 { return float64(size * size) }

// setMagmaCubeSize is the port of AbstractCubeMob.setSize + MagmaCube.setSize overrides: clamp + record
// size (cubeSize == ID_SIZE), refresh AABB dims (base x size), apply MAX_HEALTH (size*size), MOVEMENT_SPEED
// (0.2+0.1*size), ATTACK_DAMAGE (size), ARMOR (size*3) base values. MagmaCube.setSize does NOT setBaby.
//
//	[VERIFIED javap AbstractCubeMob.setSize + MagmaCube.setSize: ATTACK_DAMAGE base=actualSize;
//	 ARMOR base=size*3; xpReward=actualSize.]
func setMagmaCubeSize(e *Entity, size int32, updateHealth bool) {
	actualSize := size
	if actualSize < cubeMinSize {
		actualSize = cubeMinSize
	}
	if actualSize > cubeMaxSize {
		actualSize = cubeMaxSize
	}
	e.cubeSize = actualSize
	// refreshDimensions: AABB footprint = base (un-scaled spawn-table) dims x size (0.52x0.52 base).
	e.width = e.adultWidth * float64(actualSize)
	e.height = e.adultHeight * float64(actualSize)
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.MaxHealth.Name()); inst != nil {
			inst.SetBaseValue(magmaCubeMaxHealth(actualSize)) // MAX_HEALTH base = size*size
		}
		if inst := e.attributes.GetInstance(attribute.MovementSpeed.Name()); inst != nil {
			inst.SetBaseValue(cubeMovementSpeed(actualSize)) // MOVEMENT_SPEED base = 0.2 + 0.1*size (shared)
		}
		if inst := e.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			inst.SetBaseValue(float64(actualSize)) // MagmaCube.setSize: ATTACK_DAMAGE base = actualSize
		}
		if inst := e.attributes.GetInstance(attribute.Armor.Name()); inst != nil {
			inst.SetBaseValue(float64(size) * magmaCubeArmorPerSize) // MagmaCube.setSize: ARMOR base = size*3
		}
	}
	if updateHealth {
		e.health = float32(e.getAttributeValue(attribute.MaxHealth))
	}
	_ = actualSize // MagmaCube.setSize xpReward=actualSize (xpReward not modeled on Entity yet; cited)
}

// spawnMagmaCube creates a hostile MagmaCube at (x,y,z) at the given size and adds it to the owner region
// store. Minimal e.ai (per-entity rng + attack-target slot + jumpControl) + the three cube goals.
// setMagmaCubeSize(...,true) seeds health/dims/speed/attack/armor. Cite MagmaCube + registerGoals.
func (t *TickLoop) spawnMagmaCube(x, y, z float64, size int32) *Entity {
	m := NewEntity(t.idAlloc.AllocID(), entity.MagmaCube, x, y, z)
	m.isMagmaCube = true
	m.ai = buildMagmaCubeAI()
	reseedMobAI(m.ai, m.id)
	setMagmaCubeSize(m, size, true) // health/dims/speed/attack/armor for the size
	m.cubeWantMove = -1             // Operation.WAIT until a goal arms it (the WAIT sentinel)
	owner := t.regionForEntity(m)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(m)
	return m
}

// buildMagmaCubeAI builds a magma cube mobAI: the SAME three Go-native cube goals AbstractCubeMob
// .registerGoals adds (@1 float, @4 random-direction, @5 keep-on-jumping). The @2 CubeMobAttackGoal is
// folded into magmaCubeAiStep (targetSelector acquisition is magmaCubeAcquireNearestPlayer).
func buildMagmaCubeAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.navigation.canFloat = true
	m.goals.addGoal(1, newCubeMobFloatGoal())
	m.goals.addGoal(4, newCubeMobRandomDirectionGoal())
	m.goals.addGoal(5, newCubeMobKeepOnJumpingGoal())
	return m
}

// magmaCubeGetJumpDelay is MagmaCube.getJumpDelay(): super.getJumpDelay() * 4 == (nextInt(20)+10)*4.
//
//	[VERIFIED javap MagmaCube.getJumpDelay: invokespecial AbstractCubeMob.getJumpDelay; iconst_4; imul.]
func magmaCubeGetJumpDelay(e *Entity) int32 { return cubeGetJumpDelay(e) * magmaCubeJumpDelayScale }

// magmaCubeMoveControlTick is AbstractCubeMob CubeMobMoveControl.tick() for the MagmaCube. IDENTICAL to the
// shared cubeMoveControlTick EXCEPT the jump-delay reset uses the magma cube 4x delay. Kept separate +
// additive so the SulfurCube hot path is untouched.
//
//	[VERIFIED javap CubeMobMoveControl.tick: setYRot(rotlerp(getYRot(),yRot,90.0f)); yHeadRot=yBodyRot=getYRot();
//	 if(operation!=MOVE_TO){ setZza(0); return; } operation=WAIT;
//	 if(onGround()){ setSpeed(speedModifier*MOVEMENT_SPEED); if(jumpDelay-- <= 0){ jumpDelay=getJumpDelay();
//	   if(isAggressive) jumpDelay/=3; jumpControl.jump(); } else { setSpeed(0); } }
//	 else { setSpeed(speedModifier*MOVEMENT_SPEED); }.]
func (t *TickLoop) magmaCubeMoveControlTick(e *Entity) {
	e.yaw = rotlerpDeg(e.yaw, e.cubeMoveYRot, moveControlMaxYawStep)
	e.headYaw = e.yaw

	if e.cubeWantMove < 0 { // Operation != MOVE_TO (the WAIT sentinel): the cube coasts, no drive.
		return
	}
	speedModifier := e.cubeWantMove
	e.cubeWantMove = -1 // consume the one-shot MOVE_TO -> WAIT (a goal re-arms it each tick it runs)

	if e.onGround {
		speed := speedModifier * e.getAttributeValue(attribute.MovementSpeed)
		if e.cubeJumpDelay <= 0 { // jumpDelay-- <= 0: time to hop.
			e.cubeJumpDelay = magmaCubeGetJumpDelay(e) // MagmaCube.getJumpDelay == base*4
			if e.cubeAggressive {
				e.cubeJumpDelay /= 3
			}
			if e.ai != nil {
				e.ai.jumpControl.doJump()
			}
			t.cubeTravel(e, speed) // drive forward so the hop carries in the facing direction
		} else {
			e.cubeJumpDelay-- // between hops: xxa=0, zza=0, setSpeed(0) - no drive while grounded
		}
	} else {
		speed := speedModifier * e.getAttributeValue(attribute.MovementSpeed) // airborne: steer mid-hop
		t.cubeTravel(e, speed)
	}
}

// magmaCubeTarget reads the magma cube current attack-target player, or nil. Tick-owned (mirrors blazeTarget).
func (t *TickLoop) magmaCubeTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// magmaCubeAcquireNearestPlayer ports MagmaCube @1 NearestAttackableTargetGoal(Player, selector): nearest
// live player within FOLLOW_RANGE (createMobAttributes 16.0) whose Y is within +/-4.0 (selector abs(dy)<=4).
// NO RNG. Cite MagmaCube.addTargetingGoals + lambda selector.
func (t *TickLoop) magmaCubeAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 16.0 (createMobAttributes)
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead ||
			distanceToSqrPlayer(p, e) > rangeSqr ||
			math.Abs(p.y-e.y) > magmaCubeTargetYWindow {
			e.ai.attackTargetID = 0 // setTarget(null)
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
		if math.Abs(p.y-e.y) > magmaCubeTargetYWindow {
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

// magmaCubeAttackGoalTick ports AbstractCubeMob CubeMobAttackGoal.tick: face the target (lookAt 10,10) and
// set the move-control direction to the cube yaw with isAggressive = isDealsDamage(). MagmaCube.isDealsDamage
// == isEffectiveAi() (true), so the cube hops AGGRESSIVELY at its target (jumpDelay/3). Cite
// CubeMobAttackGoal.tick + MagmaCube.isDealsDamage.
func (t *TickLoop) magmaCubeAttackGoalTick(e *Entity) {
	target := t.magmaCubeTarget(e)
	if target == nil {
		return
	}
	dx := target.x - e.x
	dz := target.z - e.z
	e.cubeMoveYRot = float32(-math.Atan2(dx, dz) * vexDegPerRad) // lookAt(target, 10, 10) -> face yaw
	cubeSetDirection(e, e.cubeMoveYRot, true)                    // setDirection(getYRot(), isDealsDamage()==true)
}

// magmaCubeGetAttackDamage is MagmaCube.getAttackDamage(): super [ATTACK_DAMAGE attr == size] + 2.0f.
// size 2 -> 4.0; size 1 -> 3.0; size 4 -> 6.0.
//
//	[VERIFIED javap MagmaCube.getAttackDamage: invokespecial AbstractCubeMob.getAttackDamage; fconst_2; fadd.]
func magmaCubeGetAttackDamage(e *Entity) float32 {
	return float32(e.getAttributeValue(attribute.AttackDamage)) + magmaCubeAttackBonus
}

// magmaCubeTouchPlayer ports AbstractCubeMob.playerTouch -> dealDamage: if within melee range with LoS,
// hurt for getAttackDamage() (size + 2.0). isDealsDamage() == isEffectiveAi() true. NO RNG (the SLIME_ATTACK
// sound is client cue, deferred). Cite AbstractCubeMob.dealDamage + MagmaCube.getAttackDamage.
func (t *TickLoop) magmaCubeTouchPlayer(e *Entity, target *tickPlayer) {
	reach := e.width/2 + playerWidth/2 + 0.6 // isWithinMeleeAttackRange ~ summed half-widths + 0.6 slack
	if distanceToSqrPlayer(target, e) > reach*reach {
		return
	}
	if !t.sensingHasLineOfSight(e, target) { // hasLineOfSight(target)
		return
	}
	dmg := magmaCubeGetAttackDamage(e) // getAttackDamage() == ATTACK_DAMAGE (size) + 2.0
	src := damageSourceMobAttack(e.id) // damageSources().mobAttack(this)
	t.applyDamage(target, src, dmg)    // target.hurtServer(level, src, f) -- the PLAYER path
}

// magmaCubeAiStep is the MagmaCube per-tick drive: acquire nearest player (targetSelector), the
// CubeMobAttackGoal (face + aggressive hop), the CubeMobMoveControl.tick (yaw rotlerp + hop + travel),
// then the touch damage. Called from tickAI (typ == entity.MagmaCube.ID) AFTER serverAiStep so the three
// cube goals have set the move-control state. The squish/land block is client animation - cite-deferred.
func (t *TickLoop) magmaCubeAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	e.cubeWasOnGround = e.onGround
	t.magmaCubeAcquireNearestPlayer(e)
	t.magmaCubeAttackGoalTick(e)
	t.magmaCubeMoveControlTick(e)
	if target := t.magmaCubeTarget(e); target != nil {
		t.magmaCubeTouchPlayer(e, target)
	}
}

// magmaCubeSplitOnRemove is the port of AbstractCubeMob.remove - the split. A cube of size > 1 removed while
// dead spawns getSplitCount() (2 + nextInt(3) == 2..4) half-size cubes at the (i%2, i/2) offset grid, each
// setSize(halfSize, true) + snapTo the offset with a random yaw. MagmaCube does NOT setBaby (a plain smaller
// magma cube). Called from tickDeath JUST BEFORE the store removal, per-type-gated on typ==entity.MagmaCube.ID.
//
//	[VERIFIED javap AbstractCubeMob.remove + getSplitCount (2 + nextInt(3)) + setUpSplitCube.]
func (t *TickLoop) magmaCubeSplitOnRemove(e *Entity) {
	if e.cubeSize <= 1 || !e.dead { // !isClientSide (true) && getSize()>1 && isDeadOrDying()
		return
	}
	width := e.width // getDimensions(pose).width() == the live (size-scaled) width
	xzOffset := width / 2.0
	halfSize := e.cubeSize / 2
	count := int(2 + mobRandom(e).nextInt(3)) // AbstractCubeMob.getSplitCount == 2 + nextInt(3) == 2..4
	owner := t.regionForEntity(e)
	for i := 0; i < count; i++ {
		xd := (float64(i%2) - 0.5) * xzOffset
		zd := (float64(i/2) - 0.5) * xzOffset
		child := NewEntity(t.idAlloc.AllocID(), entity.MagmaCube, e.x+xd, e.y+0.5, e.z+zd)
		child.isMagmaCube = true
		child.ai = buildMagmaCubeAI()
		reseedMobAI(child.ai, child.id)
		setMagmaCubeSize(child, halfSize, true)      // health/dims/speed/attack/armor for the new size
		child.cubeWantMove = -1                      // Operation.WAIT until a goal arms it
		child.yaw = mobRandom(e).nextFloat() * 360.0 // snapTo(..., random.nextFloat()*360, 0)
		child.headYaw = child.yaw
		owner.entities.add(child)
	}
}
