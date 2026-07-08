package server

// slime.go -- the hostile Slime (net.minecraft.world.entity.monster.cubemob.Slime), a 1:1 port from the
// unobfuscated 26.2 jar. Slime extends AbstractCubeMob (the shared cube base), reusing the cube-mob
// move-control + the three cube goals + the size machine + split-on-death that landed for the SulfurCube
// (ai_goals_sulfur_cube.go) and MagmaCube (magma_cube.go). An entity is exactly one of SulfurCube /
// MagmaCube / Slime, never two, so the shared cube fields never collide. This file supplies only the
// Slime-specific overrides.
//
// VANILLA (verified javap Slime + AbstractCubeMob + DefaultAttributes this session):
//   Slime DefaultAttributes: Monster.createMonsterAttributes().build() (NO MOVEMENT_SPEED add).
//   AbstractCubeMob.setSize: clamp(1,127); ID_SIZE; refreshDimensions; MAX_HEALTH base size*size;
//     MOVEMENT_SPEED base 0.2f+0.1f*size; if(updateHealth) setHealth(getMaxHealth()).
//   Slime.setSize: super; ATTACK_DAMAGE base = getSize(); xpReward = getSize(). NO ARMOR (MagmaCube-only).
//   getJumpDelay: inherited nextInt(20)+10 (NO 4x). getAttackDamage: (float) attr ATTACK_DAMAGE (== size,
//     NO +2.0). isDealsDamage: !isTiny() && isEffectiveAi() (isTiny==getSize()<=1 -> a size-1 slime deals
//     NO damage). remove: size>1 && dead -> getSplitCount() (2+nextInt(3)) half-size slimes; setUpSplitCube
//     setSize(halfSize,true)+snapTo(...,nextFloat()*360,0), Slime does NOT setBaby. Slime.canBeABaby false;
//     no causeFallDamage override (a slime takes normal fall damage). Mob.getBaseExperienceReward = xpReward
//     (== size). Loot: a TINY (size 1) slime drops slime_ball uniform[0,2] gated on cube_mob.size==1.
//
// v1 STUBS (cited): squish/land particles + sounds + ID_SIZE client metadata; IronGolem @3 targeting (no
// golem population); isEffectiveAi() const-true (a live server-controlled slime).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// Slime constants (VERIFIED javap Slime + AbstractCubeMob this session).
const (
	slimeSpawnSize     int32   = 2   // debug spawn size (a size-2 slime splits visibly)
	slimeTargetYWindow float64 = 4.0 // addTargetingGoals selector: abs(dy) <= 4.0
)

// slimeMaxHealth is AbstractCubeMob.setcubeMobHealth(size): MAX_HEALTH base = size*size (Slime does NOT
// override it, unlike the SulfurCube 4*size). size 2 -> 4, size 1 -> 1, size 4 -> 16.
func slimeMaxHealth(size int32) float64 { return float64(size * size) }

// setSlimeSize is the port of AbstractCubeMob.setSize + Slime.setSize overrides: clamp + record size,
// refresh AABB dims (base x size), apply MAX_HEALTH (size*size), MOVEMENT_SPEED (0.2+0.1*size),
// ATTACK_DAMAGE (size), and record xpReward (size). NO ARMOR (MagmaCube-only); NO setBaby (SulfurCube-only).
func setSlimeSize(e *Entity, size int32, updateHealth bool) {
	actualSize := size
	if actualSize < cubeMinSize {
		actualSize = cubeMinSize
	}
	if actualSize > cubeMaxSize {
		actualSize = cubeMaxSize
	}
	e.cubeSize = actualSize
	e.width = e.adultWidth * float64(actualSize)
	e.height = e.adultHeight * float64(actualSize)
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.MaxHealth.Name()); inst != nil {
			inst.SetBaseValue(slimeMaxHealth(actualSize)) // MAX_HEALTH base = size*size
		}
		if inst := e.attributes.GetInstance(attribute.MovementSpeed.Name()); inst != nil {
			inst.SetBaseValue(cubeMovementSpeed(actualSize)) // MOVEMENT_SPEED base = 0.2 + 0.1*size (shared)
		}
		if inst := e.attributes.GetInstance(attribute.AttackDamage.Name()); inst != nil {
			inst.SetBaseValue(float64(actualSize)) // Slime.setSize: ATTACK_DAMAGE base = getSize()
		}
	}
	if updateHealth {
		e.health = float32(e.getAttributeValue(attribute.MaxHealth))
	}
	e.slimeXpReward = actualSize // Slime.setSize: this.xpReward = getSize()
}

// spawnSlime creates a hostile Slime at (x,y,z) at the given size and adds it to the owner region store.
// setSlimeSize(...,true) seeds health/dims/speed/attack/xpReward. Cite Slime + registerGoals.
func (t *TickLoop) spawnSlime(x, y, z float64, size int32) *Entity {
	m := NewEntity(t.idAlloc.AllocID(), entity.Slime, x, y, z)
	m.isSlime = true
	m.ai = buildSlimeAI()
	reseedMobAI(m.ai, m.id)
	setSlimeSize(m, size, true) // health/dims/speed/attack/xpReward for the size
	m.cubeWantMove = -1         // Operation.WAIT until a goal arms it (the WAIT sentinel)
	owner := t.regionForEntity(m)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(m)
	return m
}

// buildSlimeAI builds a slime mobAI: the SAME three Go-native cube goals AbstractCubeMob.registerGoals
// adds (@1 float, @4 random-direction, @5 keep-on-jumping). The @2 CubeMobAttackGoal is folded into
// slimeAiStep. Identical to buildMagmaCubeAI (the cube goals are per-cube identical).
func buildSlimeAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.navigation.canFloat = true
	m.goals.addGoal(1, newCubeMobFloatGoal())
	m.goals.addGoal(4, newCubeMobRandomDirectionGoal())
	m.goals.addGoal(5, newCubeMobKeepOnJumpingGoal())
	return m
}

// slimeIsDealsDamage is AbstractCubeMob.isDealsDamage(): !isTiny() && isEffectiveAi(). isTiny() ==
// getSize()<=1, so a size-1 slime is tiny -> NO damage. isEffectiveAi() is const-true. Slime does NOT
// override isDealsDamage (unlike MagmaCube always-true).
func slimeIsDealsDamage(e *Entity) bool {
	const isEffectiveAi = true // a live server-controlled slime (no rider/no-ai stub)
	return e.cubeSize > 1 && isEffectiveAi
}

// slimeTarget reads the slime current attack-target player, or nil. Tick-owned.
func (t *TickLoop) slimeTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// slimeAcquireNearestPlayer ports Slime @1 NearestAttackableTargetGoal(Player, selector): nearest live
// player within FOLLOW_RANGE (16.0) whose Y is within +/-4.0 (selector abs(dy)<=4). NO RNG.
func (t *TickLoop) slimeAcquireNearestPlayer(e *Entity) {
	if e.ai == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 16.0 (createMobAttributes)
	rangeSqr := followRange * followRange
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p == nil || p.dead ||
			distanceToSqrPlayer(p, e) > rangeSqr ||
			math.Abs(p.y-e.y) > slimeTargetYWindow {
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
		if math.Abs(p.y-e.y) > slimeTargetYWindow {
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

// slimeAttackGoalTick ports AbstractCubeMob CubeMobAttackGoal.tick: face the target and set the
// move-control direction to the cube yaw with isAggressive = isDealsDamage(). A size>1 slime hops
// AGGRESSIVELY (jumpDelay/3); a tiny slime faces + hops but NOT aggressively.
func (t *TickLoop) slimeAttackGoalTick(e *Entity) {
	target := t.slimeTarget(e)
	if target == nil {
		return
	}
	dx := target.x - e.x
	dz := target.z - e.z
	e.cubeMoveYRot = float32(-math.Atan2(dx, dz) * vexDegPerRad) // lookAt(target,10,10) -> face yaw
	cubeSetDirection(e, e.cubeMoveYRot, slimeIsDealsDamage(e))   // setDirection(getYRot(), isDealsDamage())
}

// slimeGetAttackDamage is AbstractCubeMob.getAttackDamage(): (float) getAttributeValue(ATTACK_DAMAGE)
// (== size). Slime does NOT override it (NO +2.0 -- MagmaCube-only). size 2 -> 2.0; size 4 -> 4.0.
func slimeGetAttackDamage(e *Entity) float32 {
	return float32(e.getAttributeValue(attribute.AttackDamage))
}

// slimeTouchPlayer ports AbstractCubeMob.playerTouch -> dealDamage: if isDealsDamage() (a size>1 slime)
// AND within melee range with LoS, hurt for getAttackDamage() (== size). A tiny (size-1) slime does NOT
// deal damage. NO RNG.
func (t *TickLoop) slimeTouchPlayer(e *Entity, target *tickPlayer) {
	if !slimeIsDealsDamage(e) { // playerTouch: if (isDealsDamage()) dealDamage(player)
		return
	}
	reach := e.width/2 + playerWidth/2 + 0.6 // isWithinMeleeAttackRange ~ summed half-widths + 0.6 slack
	if distanceToSqrPlayer(target, e) > reach*reach {
		return
	}
	if !t.sensingHasLineOfSight(e, target) { // hasLineOfSight(target)
		return
	}
	dmg := slimeGetAttackDamage(e)     // getAttackDamage() == ATTACK_DAMAGE (size)
	src := damageSourceMobAttack(e.id) // damageSources().mobAttack(this)
	t.applyDamage(target, src, dmg)    // target.hurtServer(level, src, f) -- the PLAYER path
}

// slimeAiStep is the Slime per-tick drive: acquire nearest player, the CubeMobAttackGoal (face + hop), the
// SHARED cubeMoveControlTick (Slime uses the BASE getJumpDelay nextInt(20)+10 -- exactly what
// cubeMoveControlTick draws), then the touch damage. Called from tickAI (typ == entity.Slime.ID) AFTER
// serverAiStep. Squish/land is client anim (deferred).
func (t *TickLoop) slimeAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	e.cubeWasOnGround = e.onGround
	t.slimeAcquireNearestPlayer(e)
	t.slimeAttackGoalTick(e)
	t.cubeMoveControlTick(e) // the SHARED base tick (base getJumpDelay == nextInt(20)+10, no 4x)
	if target := t.slimeTarget(e); target != nil {
		t.slimeTouchPlayer(e, target)
	}
}

// slimeSplitOnRemove is the port of AbstractCubeMob.remove - the split. A slime of size > 1 removed while
// dead spawns getSplitCount() (2 + nextInt(3) == 2..4) half-size slimes at the (i%2, i/2) offset grid,
// each setSize(halfSize, true) + snapTo the offset with a random yaw. Slime does NOT setBaby. Called from
// tickDeath JUST BEFORE the store removal, per-type-gated on typ==entity.Slime.ID.
func (t *TickLoop) slimeSplitOnRemove(e *Entity) {
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
		child := NewEntity(t.idAlloc.AllocID(), entity.Slime, e.x+xd, e.y+0.5, e.z+zd)
		child.isSlime = true
		child.ai = buildSlimeAI()
		reseedMobAI(child.ai, child.id)
		setSlimeSize(child, halfSize, true)          // health/dims/speed/attack/xpReward for the new size
		child.cubeWantMove = -1                      // Operation.WAIT until a goal arms it
		child.yaw = mobRandom(e).nextFloat() * 360.0 // snapTo(..., random.nextFloat()*360, 0)
		child.headYaw = child.yaw
		owner.entities.add(child)
	}
}
