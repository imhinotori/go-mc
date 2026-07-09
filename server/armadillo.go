// armadillo.go -- ARMADILLO (net.minecraft.world.entity.animal.armadillo.Armadillo), 1:1 port from the
// unobfuscated 26.2 jar. Armadillo is a savanna Animal that ROLLS UP into a ball (its SIGNATURE) when a
// threat is nearby (UNDEAD mob, the mob that last hurt it, or a sprinting/riding player within an inflated
// 7x2x7 box), stays scared while the threat lingers, then UNROLLS; and it periodically SHEDS a scute
// (customServerAiStep scuteTime countdown). Additive + per-type-gated behind e.isArmadillo (false for
// every other entity; the pig oracle stays byte-identical).
//
// VANILLA (verified javap this task):
//   createAttributes: Animal.createAnimalAttributes + MAX_HEALTH 12.0 + MOVEMENT_SPEED 0.14.
//   ArmadilloState enum: IDLE(threatened=false animDur=0) ROLLING(true 10) SCARED(true 50) UNROLLING(true
//     30). isScared() = state != IDLE. shouldSwitchToScaredState(): ROLLING AND inStateTicks > 10.
//   isScaredBy(living): box.inflate(7,2,7).intersects(target.box) AND (UNDEAD OR lastHurtBy==target OR
//     (Player: not-spectator AND (sprinting OR passenger))).
//   rollUp(): if isScared return; stopInPlace; resetLove; switchToState(ROLLING).
//   rollOut(): if not scared return; switchToState(IDLE).
//   customServerAiStep: brain.tick + updateActivity; if isAlive AND --scuteTime<=0 AND shouldDropLoot ->
//     dropFromGiftLootTable(ARMADILLO_SHED); scuteTime = pickNextScuteDropTime().
//   pickNextScuteDropTime(): nextInt(20*60*5) + 20*60*5 == nextInt(6000)+6000. tick(): ++inStateTicks.
//
// LANDED: the 2 attributes, the ArmadilloState machine + anim durations + inStateTicks, rollUp/rollOut
// with the isScared guards + ROLLING->SCARED promotion, the isScaredBy player-threat predicate, the
// per-tick threat SCAN (armadilloAiStep, the ArmadilloBallUp analogue), and the scute-shed countdown. RNG
// on the armadillo OWN stream only.
//
// v1 STUBS (cited): the scute ITEM drop (ARMADILLO_SHED loot) is DEFERRED behind the loot/item subsystem
// -- the countdown + reset are faithful. The UNDEAD-mob / last-hurt-by threat feed (mob-vs-mob targeting),
// brushOffScute, the Brain graph, animation states + sounds are cited deferrals. baby uses AgeableMob dims.

package server

import (
	"github.com/imhinotori/sulfur/data/entity"
)

// Armadillo state ids (ARMADILLO_STATE ordinal; VERIFIED javap ArmadilloState enum order).
const (
	armadilloStateIdle      = 0 // IDLE       threatened=false animDur=0
	armadilloStateRolling   = 1 // ROLLING    threatened=true  animDur=10
	armadilloStateScared    = 2 // SCARED     threatened=true  animDur=50
	armadilloStateUnrolling = 3 // UNROLLING  threatened=true  animDur=30
)

// Armadillo constants (VERIFIED javap this task).
const (
	armadilloMaxHealth     = 12.0 // createAttributes MAX_HEALTH 12.0
	armadilloMovementSpeed = 0.14 // createAttributes MOVEMENT_SPEED 0.14

	armadilloRollingAnimDur   = 10 // ROLLING.animationDuration (enum init bipush 10)
	armadilloScaredAnimDur    = 50 // SCARED.animationDuration (enum init bipush 50)
	armadilloUnrollingAnimDur = 30 // UNROLLING.animationDuration (enum init bipush 30)

	armadilloScareInflateXZ = 7.0 // isScaredBy inflate x/z (ldc2_w 7.0d)
	armadilloScareInflateY  = 2.0 // isScaredBy inflate y (ldc2_w 2.0d)

	armadilloScuteDropWindow = 20 * 60 * 5 // pickNextScuteDropTime: nextInt(6000)+6000

	armadilloStrollSpeed  = 1.0
	armadilloBreedSpeed   = 1.0
	armadilloTemptSpeed   = 1.25
	armadilloFollowSpeed  = 1.25
	armadilloFoodTag      = "armadillo_food" // ARMADILLO_FOOD (SPIDER_EYE): the tempt predicate
	armadilloLookDistance = 8.0
)

// newArmadilloAI builds the Armadillo bounded passive AI (BRAIN reduced to armadilloAiStep). Mirrors
// newGoatAI shape. Cite Armadillo.makeBrain.
func newArmadilloAI() *mobAI {
	m := &mobAI{}
	m.rng = newEntityRandom(defaultEntityRandomSeed)
	m.wantSpeedMod = 1.0
	m.navigation.speed = m.wantSpeedMod * armadilloMovementSpeed
	m.navigation.canFloat = true
	applyAnimalPathfindingMalus(m)
	m.goals.addGoal(0, newFloatGoal())
	m.goals.addGoal(1, newPanicGoal(panicSpeedModifier))
	m.goals.addGoal(3, newBreedGoal(armadilloBreedSpeed))
	m.goals.addGoal(4, newTemptGoal(armadilloTemptSpeed, func(id int32) bool { return itemInTag(id, armadilloFoodTag) }, false, nil))
	m.goals.addGoal(5, newFollowParentGoal(armadilloFollowSpeed))
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(armadilloStrollSpeed))
	m.goals.addGoal(7, newLookAtPlayerGoal(armadilloLookDistance))
	m.goals.addGoal(8, newRandomLookAroundGoal())
	return m
}

// spawnArmadillo creates an Armadillo with the jar attributes + passive AI. State starts IDLE; scuteTime
// starts at pickNextScuteDropTime. Cite Armadillo.createAttributes + ctor + pickNextScuteDropTime.
func (t *TickLoop) spawnArmadillo(x, y, z float64, baby bool) *Entity {
	a := NewEntity(t.idAlloc.AllocID(), entity.Armadillo, x, y, z)
	a.isArmadillo = true
	a.armadilloState = armadilloStateIdle
	if baby {
		a.breedAge = babyStartAge
		a.refreshDimensions()
	}
	initSpawnHealth(a) // setHealth(getMaxHealth()) -> 12.0
	a.ai = newArmadilloAI()
	reseedMobAI(a.ai, a.id)
	a.armadilloScuteTime = armadilloPickNextScuteDropTime(a)
	owner := t.regionForEntity(a)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(a)
	return a
}

// armadilloPickNextScuteDropTime ports pickNextScuteDropTime(): nextInt(6000)+6000 on the OWN rng.
func armadilloPickNextScuteDropTime(e *Entity) int {
	return mobRandom(e).nextInt(armadilloScuteDropWindow) + armadilloScuteDropWindow
}

// armadilloIsScared ports isScared(): state != IDLE.
func armadilloIsScared(e *Entity) bool {
	return e.armadilloState != armadilloStateIdle
}

// armadilloAnimDuration returns ArmadilloState.animationDuration() (VERIFIED javap enum init).
func armadilloAnimDuration(state int) int {
	switch state {
	case armadilloStateRolling:
		return armadilloRollingAnimDur
	case armadilloStateScared:
		return armadilloScaredAnimDur
	case armadilloStateUnrolling:
		return armadilloUnrollingAnimDur
	default:
		return 0
	}
}

// armadilloSwitchToState ports switchToState(state): set state + reset inStateTicks (onSyncedDataUpdated).
func (t *TickLoop) armadilloSwitchToState(e *Entity, state int) {
	e.armadilloState = state
	e.armadilloInStateTicks = 0
}

// armadilloRollUp ports rollUp(): no-op if already scared; else stop + switchToState(ROLLING).
func (t *TickLoop) armadilloRollUp(e *Entity) {
	if armadilloIsScared(e) {
		return
	}
	if e.ai != nil {
		e.ai.hasTarget = false // stopInPlace: halt pathing
	}
	t.armadilloSwitchToState(e, armadilloStateRolling)
}

// armadilloRollOut ports rollOut(): no-op if not scared; else switchToState(IDLE).
func (t *TickLoop) armadilloRollOut(e *Entity) {
	if !armadilloIsScared(e) {
		return
	}
	t.armadilloSwitchToState(e, armadilloStateIdle)
}

// armadilloIsScaredBy ports isScaredBy(living) PLAYER path: inflated-box intersect AND not-spectator AND
// (sprinting OR passenger). Cite Armadillo.isScaredBy.
func (t *TickLoop) armadilloIsScaredBy(e *Entity, p *tickPlayer) bool {
	if p == nil || p.dead {
		return false
	}
	box := e.AABB()
	minX := box.Lower[0] - armadilloScareInflateXZ
	minY := box.Lower[1] - armadilloScareInflateY
	minZ := box.Lower[2] - armadilloScareInflateXZ
	maxX := box.Upper[0] + armadilloScareInflateXZ
	maxY := box.Upper[1] + armadilloScareInflateY
	maxZ := box.Upper[2] + armadilloScareInflateXZ
	phw := playerWidth / 2
	pMinX, pMaxX := p.x-phw, p.x+phw
	pMinY, pMaxY := p.y, p.y+playerHeight
	pMinZ, pMaxZ := p.z-phw, p.z+phw
	if !(minX < pMaxX && maxX > pMinX && minY < pMaxY && maxY > pMinY && minZ < pMaxZ && maxZ > pMinZ) {
		return false
	}
	if p.gameMode == gameModeSpectator {
		return false
	}
	return p.sprinting || p.vehicleID != 0
}

// armadilloDangerMemoryTicks is the DANGER_DETECTED_RECENTLY expiry Armadillo.onSyncedDataUpdated sets
// (setMemoryWithExpiry(DANGER_DETECTED_RECENTLY, true, 80L)) whenever a threat is scared-by. The
// ArmadilloBallUp state machine reads the remaining time (getTimeUntilExpiry) which counts down from 80.
//
//	[VERIFIED javap Armadillo: getBrain().setMemoryWithExpiry(DANGER_DETECTED_RECENTLY, TRUE, 80L).]
const armadilloDangerMemoryTicks = 80

// armadilloCanStayRolledUp ports Armadillo.canStayRolledUp(): !isPanicking() && !isInLiquid() &&
// !isLeashed() && !isPassenger() && !isVehicle(). A rolled-up armadillo is FORCED to unroll (the
// ArmadilloBallUp.stop rollOut) when any of these hold. isLeashed is a cited const-false (no leash
// subsystem in v1). Cite Armadillo.canStayRolledUp.
//
//	[VERIFIED javap Armadillo.canStayRolledUp: !isPanicking() && !isInLiquid() && !isLeashed() &&
//	 !isPassenger() && !isVehicle().]
func (t *TickLoop) armadilloCanStayRolledUp(e *Entity) bool {
	if t.isPanicking(e) { // isPanicking()
		return false
	}
	if t.entityInWater(e) { // isInLiquid() -> the v1 water read (no lava-fluid distinction here; Armadillo
		return false // never survives lava anyway). Cite Entity.isInLiquid.
	}
	// isLeashed(): cited const-false (no leash subsystem in v1).
	if e.vehicle != 0 { // isPassenger()
		return false
	}
	if len(e.passengers) > 0 { // isVehicle()
		return false
	}
	return true
}

// armadilloAiStep ports Armadillo.tick tail + customServerAiStep + the ArmadilloBallUp threat scan.
// Cite Armadillo.tick + customServerAiStep + ArmadilloAi.ArmadilloBallUp.
func (t *TickLoop) armadilloAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	e.armadilloInStateTicks++ // Armadillo.tick(): ++inStateTicks

	// Threat scan (the ArmadilloAi danger sensor + the isScaredBy player-threat predicate). A live threat
	// (re)sets the DANGER_DETECTED_RECENTLY memory to its 80-tick expiry (Armadillo.onSyncedDataUpdated /
	// the sensor's setMemoryWithExpiry(..., 80L)); otherwise the memory decays one tick toward 0
	// (Brain.tick expiry countdown). We model getTimeUntilExpiry(DANGER_DETECTED_RECENTLY) as
	// armadilloDangerExpiry. Cite ArmadilloAi danger sensor + Armadillo.onSyncedDataUpdated.
	threat := false
	for _, pl := range t.players {
		if t.armadilloIsScaredBy(e, pl) {
			threat = true
			break
		}
	}
	if threat {
		e.armadilloDangerExpiry = armadilloDangerMemoryTicks // setMemoryWithExpiry(..., 80L)
	} else if e.armadilloDangerExpiry > 0 {
		e.armadilloDangerExpiry-- // Brain.tick: the memory expiry counts down
	}

	// ArmadilloBallUp.checkExtraStartConditions == onGround(): a grounded armadillo with danger present
	// rolls up (the goal starts -> Armadillo.rollUp). Gated on onGround so an airborne armadillo does not
	// ball up mid-fall. Cite ArmadilloAi.ArmadilloBallUp.checkExtraStartConditions + start (rollUp).
	if threat && e.onGround {
		t.armadilloRollUp(e)
	}

	// ArmadilloBallUp.tick state machine (the danger-memory-driven transitions):
	timeUntilExpiry := e.armadilloDangerExpiry
	switch e.armadilloState {
	case armadilloStateRolling:
		// shouldSwitchToScaredState(): state==ROLLING && inStateTicks > ROLLING.animationDuration (10).
		if e.armadilloInStateTicks > int64(armadilloAnimDuration(armadilloStateRolling)) {
			t.armadilloSwitchToState(e, armadilloStateScared)
		}
	case armadilloStateScared:
		// SCARED: when the danger memory is about to lapse (getTimeUntilExpiry < UNROLLING.animationDuration
		// == 30) begin unrolling. This is the vanilla ArmadilloBallUp.tick SCARED branch (the peek-timer +
		// broadcastEntityEvent(64) peek is a cited client-visual deferral; the state transition is the
		// gameplay). Cite ArmadilloAi.ArmadilloBallUp.tick.
		if timeUntilExpiry < armadilloAnimDuration(armadilloStateUnrolling) {
			t.armadilloSwitchToState(e, armadilloStateUnrolling)
		}
	case armadilloStateUnrolling:
		// UNROLLING: a fresh danger spike (getTimeUntilExpiry > UNROLLING.animationDuration == 30) re-scares
		// the armadillo (back to SCARED). Cite ArmadilloBallUp.tick UNROLLING branch.
		if timeUntilExpiry > armadilloAnimDuration(armadilloStateUnrolling) {
			t.armadilloSwitchToState(e, armadilloStateScared)
		}
	}

	// ArmadilloBallUp.stop (the goal's exit): the BallUp behavior REQUIRES the DANGER_DETECTED_RECENTLY
	// memory (VALUE_PRESENT). When the memory fully lapses (getTimeUntilExpiry hits 0 -> the Brain erases
	// it -> hasRequiredMemories fails), the behavior stops and runs: if (!canStayRolledUp()) rollOut().
	// So a threatened armadillo unrolls to IDLE once the 80-tick danger memory has decayed away AND
	// nothing forces it to stay balled. canStayRolledUp TRUE (a panicking/liquid/ridden armadillo) is the
	// FORCE-UNROLL case handled separately below (a live threat keeps the memory topped up, so a
	// canStayRolledUp==false armadillo only unrolls here once genuinely safe). Cite ArmadilloBallUp.stop +
	// the DANGER_DETECTED_RECENTLY required-memory ctor + Armadillo.canStayRolledUp.
	if armadilloIsScared(e) && e.armadilloDangerExpiry <= 0 {
		t.armadilloRollOut(e)
	}
	// FORCE-UNROLL (!canStayRolledUp): a panicking / in-liquid / passenger / vehicle armadillo cannot stay
	// balled -- the goal.stop rollOut fires even while a danger memory lingers (the behavior is EVICTED by
	// the higher-priority ArmadilloPanic, which then runs stop -> rollOut). Model it directly: a scared
	// armadillo that cannot stay rolled up unrolls now. Cite ArmadilloBallUp.stop (!canStayRolledUp ->
	// rollOut) + Armadillo.canStayRolledUp (panic/liquid/leashed/passenger/vehicle).
	if armadilloIsScared(e) && !t.armadilloCanStayRolledUp(e) {
		t.armadilloRollOut(e)
	}
	e.armadilloScuteTime-- // customServerAiStep: --scuteTime
	if e.armadilloScuteTime <= 0 {
		// dropFromGiftLootTable(ARMADILLO_SHED) [DEFERRED loot]; reset.
		e.armadilloScuteTime = armadilloPickNextScuteDropTime(e)
	}
}
