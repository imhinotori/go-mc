package server

// ai_goals_sulfur_cube.go - MOB-CUBE (SulfurCube, a NEW 26.2 mob): the cube-mob AI + the size machine +
// split-on-death, PORTED 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this
// session). See the AbstractCubeMob (shared base) + SulfurCube (per-type overrides) citations inline.
//
// WHY GO-NATIVE (not a .star callback): the cube does NOT use PathNavigation - its CubeMobMoveControl
// drives movement DIRECTLY (rotlerp the yaw toward a chosen heading, jumpControl.jump() on a countdown,
// setSpeed = speedModifier x MOVEMENT_SPEED, and the travel physics moves it). So the move control + the
// three goals that feed it are Go-native (buildNativeGoal kinds cube_float / cube_random_direction /
// cube_keep_on_jumping); sulfurCubeAiStep runs the CubeMobMoveControl.tick AFTER serverAiStep (sibling of
// creeperAiStep/endermanAiStep, per-type-gated on typ == entity.SulfurCube.ID).
//
// CITE-DEFERRED (recorded, NEVER silently dropped - each needs an unbuilt subsystem, stubbed at vanilla
// default): SulfurCube.addBehaviourGoals SulfurCubeTemptGoal@2 (tempt/held-item) + SearchForItemsGoal@3
// (item-pickup); the whole body-item/archetype system (contact damage - isDealsDamage stays false without
// a body item; explosion-on-death EXPLOSIVE archetype power 3 fuse 120; knockback modifiers; buoyancy;
// shear/bucket/pickup) - all need item-pickup + block-entity + the SULFUR_CUBE_ARCHETYPE registry;
// hasEffect(LEVITATION) in RandomDirection.canUse (const-false, no mob-effect read); the squish/land
// particles + squish/jump sounds + the ID_SIZE client metadata (client visuals/wire, the cube still
// jumps + moves + splits + scales). The archetype VALUES are documented in the .star header.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

const (
	cubeMinSize          int32 = 1   // AbstractCubeMob.MIN_SIZE (setSize Mth.clamp lower bound)
	cubeMaxSize          int32 = 127 // AbstractCubeMob.MAX_SIZE (setSize Mth.clamp upper bound)
	sulfurCubeSpawnSize  int32 = 2   // SulfurCube.setSpawnSize adult size (baby -> 1)
	sulfurCubeSplitCount       = 2   // SulfurCube.getSplitCount (0 if primed - no body-item fuse in v1)
)

// babyStartAge (AgeableMob.BABY_START_AGE == -24000) is defined in ai_goals_breed.go and reused here for
// SulfurCube.setSize's size-1 -> setBaby and setUpSplitCube's setBaby(true) (both set the negative age).

// sulfurCubeMaxHealth is SulfurCube.setcubeMobHealth(size): MAX_HEALTH base = 4*size (SulfurCube OVERRIDES
// setcubeMobHealth; NOT AbstractCubeMob's size*size). size 2 -> 8, size 1 -> 4.
//
//	[VERIFIED CFR SulfurCube.setcubeMobHealth: getAttribute(MAX_HEALTH).setBaseValue(4 * actualSize).]
func sulfurCubeMaxHealth(size int32) float64 { return float64(4 * size) }

// cubeMovementSpeed is AbstractCubeMob.setSize MOVEMENT_SPEED base: 0.2f + 0.1f*size (float math then
// widened). size 2 -> 0.4, size 1 -> 0.3.
//
//	[VERIFIED CFR AbstractCubeMob.setSize: MOVEMENT_SPEED base = 0.2f + 0.1f*(float)actualSize.]
func cubeMovementSpeed(size int32) float64 { return float64(float32(0.2) + float32(0.1)*float32(size)) }

// setSulfurCubeSize is the port of AbstractCubeMob.setSize(size, updateHealth) + SulfurCube's overrides:
// clamp + record size (cubeSize == ID_SIZE), refresh AABB dims (base x size), apply MAX_HEALTH (4*size) +
// MOVEMENT_SPEED (0.2+0.1*size) base values, and SulfurCube.setSize's non-baby size-1 -> setBaby.
//
//	[VERIFIED CFR AbstractCubeMob.setSize: actualSize=Mth.clamp(size,1,127); set(ID_SIZE); reapplyPosition();
//	 refreshDimensions(); setcubeMobHealth(actualSize); MOVEMENT_SPEED base 0.2f+0.1f*actualSize;
//	 if(updateHealth) setHealth(getMaxHealth()). SulfurCube.setSize: super; if(updateHealth && size==1 &&
//	 !isBaby()) setBaby(true).]
func setSulfurCubeSize(e *Entity, size int32, updateHealth bool) {
	actualSize := size
	if actualSize < cubeMinSize {
		actualSize = cubeMinSize
	}
	if actualSize > cubeMaxSize {
		actualSize = cubeMaxSize
	}
	e.cubeSize = actualSize
	// refreshDimensions: AABB footprint = base (un-scaled spawn-table) dims x size (0.49x0.49 base).
	e.width = e.adultWidth * float64(actualSize)
	e.height = e.adultHeight * float64(actualSize)
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.MaxHealth.Name()); inst != nil {
			inst.SetBaseValue(sulfurCubeMaxHealth(actualSize)) // MAX_HEALTH base = 4*size
		}
		if inst := e.attributes.GetInstance(attribute.MovementSpeed.Name()); inst != nil {
			inst.SetBaseValue(cubeMovementSpeed(actualSize)) // MOVEMENT_SPEED base = 0.2 + 0.1*size
		}
	}
	if updateHealth {
		e.health = float32(e.getAttributeValue(attribute.MaxHealth))
	}
	// SulfurCube.setSize: the size-1 cube IS a baby (a split from size 2). Set the negative age (the
	// AgeableMob baby machine already lives on the Entity - breedAge < 0).
	if updateHealth && size == 1 && !e.isBaby() {
		e.breedAge = babyStartAge
	}
}

// initSulfurCubeSpawn applies SulfurCube.setSpawnSize at spawn: adult -> size 2, baby -> size 1. setSize
// (...,true) resets health to the size-scaled MaxHealth, superseding the shared initSpawnHealth base 20.0.
//
//	[VERIFIED CFR SulfurCube.setSpawnSize: if (isBaby()) setSize(1,true); else setSize(2,true).]
func initSulfurCubeSpawn(e *Entity) {
	if e.isBaby() {
		setSulfurCubeSize(e, 1, true)
	} else {
		setSulfurCubeSize(e, sulfurCubeSpawnSize, true)
	}
}

// The cube goals set the move-control state (cubeMoveYRot/cubeAggressive/cubeWantMove), which
// sulfurCubeAiStep consumes. Each defines canContinueToUse == canUse (baseGoal defaults true, so override).

// cubeMobFloatGoal is AbstractCubeMob$CubeMobFloatGoal - {JUMP,MOVE}, requiresUpdateEveryTick.
//
//	[VERIFIED CFR CubeMobFloatGoal: setFlags(JUMP,MOVE); setCanFloat(true); canUse:(isInWater||isInLava);
//	 requiresUpdateEveryTick:true; tick: if(nextFloat()<0.8f) jumpControl.jump(); setWantedMovement(1.2).]
type cubeMobFloatGoal struct{ baseGoal }

func newCubeMobFloatGoal() *cubeMobFloatGoal {
	return &cubeMobFloatGoal{baseGoal: newBaseGoal(flagJump | flagMove)}
}

func (g *cubeMobFloatGoal) requiresUpdateEveryTick() bool { return true }
func (g *cubeMobFloatGoal) canUse(t *TickLoop, e *Entity) bool {
	return t.mobInWater(e) || t.mobInLava(e)
}
func (g *cubeMobFloatGoal) canContinueToUse(t *TickLoop, e *Entity) bool { return g.canUse(t, e) }
func (g *cubeMobFloatGoal) tick(t *TickLoop, e *Entity) {
	if mobRandom(e).nextFloat() < 0.8 {
		if e.ai != nil {
			e.ai.jumpControl.doJump()
		}
	}
	cubeSetWantedMovement(e, 1.2)
}

// cubeMobRandomDirectionGoal is AbstractCubeMob$CubeMobRandomDirectionGoal - {LOOK}.
//
//	[VERIFIED CFR CubeMobRandomDirectionGoal: setFlags(LOOK); canUse: getTarget()==null &&
//	 (onGround||isInWater||isInLava||hasEffect(LEVITATION)); tick: if(--nextRandomizeTime<=0){
//	 nextRandomizeTime=adjustedTickDelay(40+nextInt(60)); chosenDegrees=nextInt(360); }
//	 setDirection(chosenDegrees,false).]
type cubeMobRandomDirectionGoal struct {
	baseGoal
	chosenDegrees     float32
	nextRandomizeTime int32
}

func newCubeMobRandomDirectionGoal() *cubeMobRandomDirectionGoal {
	return &cubeMobRandomDirectionGoal{baseGoal: newBaseGoal(flagLook)}
}

func (g *cubeMobRandomDirectionGoal) canUse(t *TickLoop, e *Entity) bool {
	// getTarget()==null: the base cube has no targetSelector (addTargetingGoals empty), so mobTarget is
	// always 0 for a v1 sulfur cube - this branch is always satisfied.
	if mobTarget(e) != 0 {
		return false
	}
	const hasLevitation = false // hasEffect(LEVITATION): const-false (no cube mob-effect read yet).
	return e.onGround || t.mobInWater(e) || t.mobInLava(e) || hasLevitation
}

func (g *cubeMobRandomDirectionGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

func (g *cubeMobRandomDirectionGoal) tick(t *TickLoop, e *Entity) {
	g.nextRandomizeTime--
	if g.nextRandomizeTime <= 0 {
		g.nextRandomizeTime = int32(40 + mobRandom(e).nextInt(60)) // adjustedTickDelay(x)==x on a server
		g.chosenDegrees = float32(mobRandom(e).nextInt(360))
	}
	cubeSetDirection(e, g.chosenDegrees, false)
}

// cubeMobKeepOnJumpingGoal is AbstractCubeMob$CubeMobKeepOnJumpingGoal - {JUMP,MOVE}.
//
//	[VERIFIED CFR CubeMobKeepOnJumpingGoal: setFlags(JUMP,MOVE); canUse: !isPassenger();
//	 tick: setWantedMovement(1.0).]
type cubeMobKeepOnJumpingGoal struct{ baseGoal }

func newCubeMobKeepOnJumpingGoal() *cubeMobKeepOnJumpingGoal {
	return &cubeMobKeepOnJumpingGoal{baseGoal: newBaseGoal(flagJump | flagMove)}
}

func (g *cubeMobKeepOnJumpingGoal) canUse(t *TickLoop, e *Entity) bool {
	const isPassenger = false // no mounts in v1 - the cube is never a passenger (the @5 keep-hop fallback).
	return !isPassenger
}

func (g *cubeMobKeepOnJumpingGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}
func (g *cubeMobKeepOnJumpingGoal) tick(t *TickLoop, e *Entity) { cubeSetWantedMovement(e, 1.0) }

// cubeSetDirection is CubeMobMoveControl.setDirection(yRot, isAggressive).
//
//	[VERIFIED CFR CubeMobMoveControl.setDirection: this.yRot=yRot; this.isAggressive=isAggressive.]
func cubeSetDirection(e *Entity, yRot float32, aggressive bool) {
	e.cubeMoveYRot = yRot
	e.cubeAggressive = aggressive
}

// cubeSetWantedMovement is CubeMobMoveControl.setWantedMovement(speedModifier): arm MOVE_TO this tick.
//
//	[VERIFIED CFR CubeMobMoveControl.setWantedMovement: speedModifier=x; operation=MOVE_TO.]
func cubeSetWantedMovement(e *Entity, speedModifier float64) { e.cubeWantMove = speedModifier }

// cubeMoveControlTick is AbstractCubeMob$CubeMobMoveControl.tick() (SulfurCube runs the base tick -
// SulfurCubeMobMoveControl.tick no-ops ONLY with a body item, which v1 never has). rotlerp yaw ->
// consume MOVE_TO -> on ground: setSpeed + jump-on-delay-0 (thirded if aggressive) + drive; in air: drive.
//
//	[VERIFIED CFR CubeMobMoveControl.tick: setYRot(rotlerp(getYRot(),yRot,90.0f)); yHeadRot=yBodyRot=getYRot();
//	 if(operation!=MOVE_TO){ setZza(0); return; } operation=WAIT;
//	 if(onGround()){ setSpeed(speedModifier*getAttributeValue(MOVEMENT_SPEED));
//	   if(jumpDelay-- <= 0){ jumpDelay=getJumpDelay(); if(isAggressive) jumpDelay/=3; jumpControl.jump();
//	     if(doPlayJumpSound()) playSound(...); }
//	   else { xxa=0; zza=0; setSpeed(0); } }
//	 else { setSpeed(speedModifier*getAttributeValue(MOVEMENT_SPEED)); }.]
func (t *TickLoop) cubeMoveControlTick(e *Entity) {
	e.yaw = rotlerpDeg(e.yaw, e.cubeMoveYRot, moveControlMaxYawStep)
	e.headYaw = e.yaw

	if e.cubeWantMove < 0 { // Operation != MOVE_TO (the WAIT sentinel): the cube coasts, no drive.
		return
	}
	speedModifier := e.cubeWantMove
	e.cubeWantMove = -1 // consume the one-shot MOVE_TO -> WAIT (a goal re-arms it each tick it runs)

	if e.onGround {
		speed := speedModifier * e.getAttributeValue(attribute.MovementSpeed)
		// `if (jumpDelay-- <= 0)`: TEST the PRE-decrement value against 0, then decrement (post-decrement
		// semantics). A fresh cube's jumpDelay is 0, so it hops on its first grounded move-tick (vanilla
		// inits jumpDelay to 0). The `--` is folded into the two branches: reset in the jump branch, plain
		// decrement in the else.
		if e.cubeJumpDelay <= 0 { // jumpDelay-- <= 0: time to hop.
			e.cubeJumpDelay = cubeGetJumpDelay(e)
			if e.cubeAggressive {
				e.cubeJumpDelay /= 3
			}
			if e.ai != nil {
				e.ai.jumpControl.doJump()
			}
			// doPlayJumpSound()/playSound(getJumpSound()): client cue - cite-deferred (no mob playSound).
			t.cubeTravel(e, speed) // drive forward so the hop carries in the facing direction
		} else {
			e.cubeJumpDelay-- // between hops: xxa=0, zza=0, setSpeed(0) - no drive while grounded
		}
	} else {
		speed := speedModifier * e.getAttributeValue(attribute.MovementSpeed) // airborne: steer mid-hop
		t.cubeTravel(e, speed)
	}
}

// cubeGetJumpDelay is AbstractCubeMob.getJumpDelay(): random.nextInt(20) + 10.
//
//	[VERIFIED CFR AbstractCubeMob.getJumpDelay: return random.nextInt(20) + 10.]
func cubeGetJumpDelay(e *Entity) int32 { return int32(mobRandom(e).nextInt(20) + 10) }

// cubeTravel applies the setSpeed input as the ground travel physics along the body yaw - the SAME
// frictionSpeed + moveRelative(forward) + friction chain navigation.go uses (LivingEntity.travelInAir /
// getFrictionInfluencedSpeed / Entity.moveRelative). speed is getSpeed (speedModifier x MOVEMENT_SPEED).
//
//	[VERIFIED CFR the travel chain cited in navigation.go: frictionSpeed=getSpeed*(0.216/blockFriction^3);
//	 deltaMovement += getInputVector((0,0,1)*frictionSpeed, yaw); move(deltaMovement); deltaMovement.xz *= 0.546.]
func (t *TickLoop) cubeTravel(e *Entity, speed float64) {
	const blockFriction = 0.6
	const airDrag = 0.91
	const friction = blockFriction * airDrag // 0.546
	frictionSpeed := speed * (0.21600002 / (blockFriction * blockFriction * blockFriction))
	yawRad := float64(e.yaw) * math.Pi / 180.0
	sinY := math.Sin(yawRad)
	cosY := math.Cos(yawRad)
	e.vx += -sinY * frictionSpeed
	e.vz += cosY * frictionSpeed
	t.moveEntity(e, e.vx, 0, e.vz)
	e.vx *= friction
	e.vz *= friction
}

// sulfurCubeAiStep is the cube's per-tick move-control drive: the AbstractCubeMob squish edge (wasOnGround)
// + the CubeMobMoveControl.tick. Called from tickAI for a live sulfur cube (typ == entity.SulfurCube.ID),
// AFTER serverAiStep so the three cube goals have set the move-control state this tick. The cube does NOT
// use navigation.tick (no goal sets a nav want), so its serverAiStep navigation pass is inert.
//
// AbstractCubeMob.tick's squish/land block (targetSquish edges + land particles/sound) is client animation
// - cite-deferred (no mob addParticle/playSound); the wasOnGround edge is tracked for a future squish/
// ID_SIZE client sync.
func (t *TickLoop) sulfurCubeAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	e.cubeWasOnGround = e.onGround
	t.cubeMoveControlTick(e)
}

// sulfurCubeSplitOnRemove is the port of AbstractCubeMob.remove(RemovalReason) - the split. A cube of size
// > 1 removed while dead spawns getSplitCount() (SulfurCube: 2) smaller cubes at half its size (halfSize =
// size/2), arranged in the (i%2, i/2) offset grid. Each split cube: setSize(halfSize, true) + setBaby(true)
// + snapTo the offset with a random yaw. Called from tickDeath JUST BEFORE the store removal, per-type-gated
// on typ == entity.SulfurCube.ID - the faithful "remove() splits" placement (tickDeath is where
// LivingEntity.remove runs at deathTime>=20).
//
//	[VERIFIED CFR AbstractCubeMob.remove: if(!isClientSide && getSize()>1 && isDeadOrDying()){
//	   width=getDimensions(pose).width(); xzOffset=width/2; halfSize=size/2; count=getSplitCount();
//	   for(i=0;i<count;i++){ xd=((i%2)-0.5f)*xzOffset; zd=((i/2)-0.5f)*xzOffset;
//	     convertTo(getType(), SPLIT_ON_DEATH, cube -> setUpSplitCube(cube, halfSize, xd, zd)); } }
//	 setUpSplitCube: cube.setSize(halfSize,true); cube.snapTo(x+xd, y+0.5, z+zd, random.nextFloat()*360, 0).
//	 SulfurCube.setUpSplitCube: super + cube.setBaby(true). SulfurCube.getSplitCount: isPrimed()?0:2.]
func (t *TickLoop) sulfurCubeSplitOnRemove(e *Entity) {
	if e.cubeSize <= 1 || !e.dead { // !isClientSide (true) && getSize()>1 && isDeadOrDying()
		return
	}
	width := e.width // getDimensions(pose).width() == the live (size-scaled) width
	xzOffset := width / 2.0
	halfSize := e.cubeSize / 2
	count := sulfurCubeSplitCount // SulfurCube.getSplitCount == 2 (0 if primed - no body-item fuse in v1)
	owner := t.regionForEntity(e)
	for i := 0; i < count; i++ {
		xd := (float64(i%2) - 0.5) * xzOffset
		zd := (float64(i/2) - 0.5) * xzOffset
		child := NewEntity(t.idAlloc.AllocID(), entity.SulfurCube, e.x+xd, e.y+0.5, e.z+zd)
		child.breedAge = babyStartAge // SulfurCube.setUpSplitCube -> setBaby(true) (split is a baby size-1)
		child.ai = buildSulfurCubeSplitAI()
		reseedMobAI(child.ai, child.id)
		setSulfurCubeSize(child, halfSize, true)     // health/dims/speed for the new size
		child.cubeWantMove = -1                      // Operation.WAIT until a goal arms it
		child.yaw = mobRandom(e).nextFloat() * 360.0 // snapTo(..., random.nextFloat()*360, 0)
		child.headYaw = child.yaw
		owner.entities.add(child)
	}
}

// buildSulfurCubeSplitAI builds a split cube's *mobAI: the SAME three Go-native cube goals a fresh cube gets
// (AbstractCubeMob.registerGoals base set). Built directly (the cube goals are Go-native + identical for
// every cube), mirroring buildAIFromDecl's cube-goal routing.
func buildSulfurCubeSplitAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.navigation.canFloat = true
	m.goals.addGoal(1, newCubeMobFloatGoal())
	m.goals.addGoal(4, newCubeMobRandomDirectionGoal())
	m.goals.addGoal(5, newCubeMobKeepOnJumpingGoal())
	return m
}
