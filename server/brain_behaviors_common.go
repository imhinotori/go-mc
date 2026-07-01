package server

// brain_behaviors_common.go — the concrete behaviors HappyGhastAi registers, ported 1:1 from the jar
// (temp/cache/26.2-inner.jar). Each is a *behavior / *oneShot / *gateBehavior configured with the vanilla
// entryCondition + overridable hooks. Cite the class per constructor.
//
// THE MOVEMENT SEAM (flyer bridge): the baby happy ghast is a FLYER — its motion is the Ghast MoveControl
// deltaMovement kick (ai_goals_happy_ghast.go ghastMoveControlTick), NOT the ground A* nav. So the brain's
// MoveToTargetSink bridges a WALK_TARGET into the ghast move-control wanted point (ghastWanted*), and the
// existing per-tick ghastMoveControlTick accelerates the ghast toward it. This is the minimal faithful
// primitive (per scope: "build the minimal faithful piece or cite it"): vanilla's MoveToTargetSink calls
// navigation.createPath/moveTo — for a flying mob that is the FlyingPathNavigation; here it is the ghast
// hover drive, which produces the same observable "steer toward the walk target" behavior. The full A*
// flying path (canReach cooldown, partial-step DefaultRandomPos fallback) is cited-reduced.

import (
	"math"

	"github.com/imhinotori/sulfur/level/attribute"
)

// newSwim ports net.minecraft.world.entity.ai.behavior.Swim<T>(chance): CORE behavior, empty entry
// condition; runs while shouldSwim (in deep water / lava); tick jumps on nextFloat()<chance.
//
//	[VERIFIED CFR Swim: super(ImmutableMap.of()); checkExtraStartConditions=canStillUse=shouldSwim(body)
//	 = (isInWater && fluidHeight(WATER)>fluidJumpThreshold) || isInLava; tick: if random.nextFloat()<chance
//	 jumpControl.jump().]
func newSwim(chance float32) *behavior {
	b := newBehavior(nil, behaviorDuration, behaviorDuration)
	b.checkExtraStart = func(t *TickLoop, e *Entity) bool { return happyGhastShouldSwim(t, e) }
	b.canStillUse = func(t *TickLoop, e *Entity, _ int64) bool { return happyGhastShouldSwim(t, e) }
	b.tick = func(t *TickLoop, e *Entity, _ int64) {
		if mobRandom(e).nextFloat() < chance {
			e.setJumping(true) // jumpControl.jump() analogue — the swim impulse (MOB-SUB-04 seam)
		}
	}
	return b
}

// happyGhastShouldSwim ports Swim.shouldSwim. v1 has no in-water/lava state for the flying ghast (it
// hovers above ground), so this is a cited const-false structured to become a real fluid read (isInWater/
// isInLava) when the ghast fluid state lands — the behavior stays STOPPED (never jumps), which is the
// correct observable for a hovering ghast that is not in fluid.
//
//	[VERIFIED CFR Swim.shouldSwim: (isInWater && getFluidHeight(WATER) > getFluidJumpThreshold()) ||
//	 isInLava. The ghast's in-fluid state is not yet modeled (cited); a hovering ghast is not in fluid.]
func happyGhastShouldSwim(t *TickLoop, e *Entity) bool {
	_ = t
	_ = e
	return false
}

// newAnimalPanic ports net.minecraft.world.entity.ai.behavior.AnimalPanic<E>(speed, flyHeight=0). CORE
// behavior, duration 100..120, entryCondition {IS_PANICKING REGISTERED, HURT_BY REGISTERED}. Starts when
// HURT_BY is a panic-causing damage OR IS_PANICKING already set; canStillUse always true (runs the full
// duration); start sets IS_PANICKING + clears WALK_TARGET; tick, while nav is done, sets a WALK_TARGET to
// a random flee pos; stop erases IS_PANICKING.
//
//	[VERIFIED CFR AnimalPanic: super(Map.of(IS_PANICKING REGISTERED, HURT_BY REGISTERED), 100, 120);
//	 checkExtraStartConditions: HURT_BY.map(d->d.is(PANIC_CAUSES)).orElse(false) || hasMemoryValue(
//	 IS_PANICKING); canStillUse=true; start: setMemory(IS_PANICKING,true); eraseMemory(WALK_TARGET);
//	 navigation.stop(); tick: if navigation.isDone() && (pos=getPanicPos)!=null setMemory(WALK_TARGET,
//	 new WalkTarget(pos, speed, 0)); stop: eraseMemory(IS_PANICKING). The (speed,flyHeight) ctor uses
//	 AirAndWaterRandomPos.getPos for the flee position.]
func newAnimalPanic(speed float32) *behavior {
	b := newBehavior([]memoryCondition{
		{key: memIsPanicking, status: memRegistered},
		{key: memHurtBy, status: memRegistered},
	}, 100, 120)
	b.checkExtraStart = func(t *TickLoop, e *Entity) bool {
		// HURT_BY present (any hurt is panic-causing in v1 — the PANIC_CAUSES tag check is cited-reduced
		// to "was hurt", since the damage-type tag on the stored HURT_BY memory is not yet modeled) OR
		// IS_PANICKING already set.
		return e.brain.hasMemoryValue(memHurtBy) || e.brain.hasMemoryValue(memIsPanicking)
	}
	b.canStillUse = func(t *TickLoop, e *Entity, _ int64) bool { return true }
	b.start = func(t *TickLoop, e *Entity, _ int64) {
		e.brain.setMemory(memIsPanicking, true)
		e.brain.eraseMemory(memWalkTarget)
		happyGhastNavStop(e)
	}
	b.tick = func(t *TickLoop, e *Entity, _ int64) {
		if !happyGhastNavDone(e) {
			return
		}
		px, py, pz, ok := happyGhastAirRandomPos(t, e)
		if !ok {
			return
		}
		e.brain.setMemory(memWalkTarget, newWalkTarget(blockPosTracker(px, py, pz), speed, 0))
	}
	b.stop = func(t *TickLoop, e *Entity, _ int64) { e.brain.eraseMemory(memIsPanicking) }
	return b
}

// newLookAtTargetSink ports net.minecraft.world.entity.ai.behavior.LookAtTargetSink(min,max). CORE, entry
// {LOOK_TARGET VALUE_PRESENT}; canStillUse while the look target is visible; tick aims the look control at
// the target; stop erases LOOK_TARGET.
//
//	[VERIFIED CFR LookAtTargetSink: super(ImmutableMap.of(LOOK_TARGET, VALUE_PRESENT), min, max);
//	 canStillUse: getMemory(LOOK_TARGET).filter(pos->pos.isVisibleBy(body)).isPresent(); stop:
//	 eraseMemory(LOOK_TARGET); tick: getMemory(LOOK_TARGET).ifPresent(target->getLookControl().setLookAt(
//	 target.currentPosition())).]
func newLookAtTargetSink(minDur, maxDur int) *behavior {
	b := newBehavior([]memoryCondition{{key: memLookTarget, status: memValuePresent}}, minDur, maxDur)
	b.canStillUse = func(t *TickLoop, e *Entity, _ int64) bool {
		_, ok := e.brain.getMemoryTarget(memLookTarget)
		// isVisibleBy is cited-reduced to "the target memory is present" (the NEAREST_VISIBLE_LIVING_
		// ENTITIES visibility cache + LOS raycast is deferred); a present look target is visible enough
		// to keep aiming at — the observable "face the target" is preserved.
		return ok
	}
	b.stop = func(t *TickLoop, e *Entity, _ int64) { e.brain.eraseMemory(memLookTarget) }
	b.tick = func(t *TickLoop, e *Entity, _ int64) {
		pt, ok := e.brain.getMemoryTarget(memLookTarget)
		if !ok {
			return
		}
		lx, ly, lz := pt.currentPosition(t)
		happyGhastLookAt(e, lx, ly, lz)
	}
	return b
}

// newCountDownCooldownTicks ports net.minecraft.world.entity.ai.behavior.CountDownCooldownTicks(mem):
// CORE, entry {mem VALUE_PRESENT}; never times out; canStillUse while the cooldown int > 0; tick
// decrements it; stop erases it.
//
//	[VERIFIED CFR CountDownCooldownTicks: super(ImmutableMap.of(cooldownTicks, VALUE_PRESENT)); timedOut
//	 =false; canStillUse: mem.isPresent() && mem.get()>0; tick: setMemory(mem, mem.get()-1); stop:
//	 eraseMemory(mem).]
func newCountDownCooldownTicks(mem memoryKey) *behavior {
	b := newBehavior([]memoryCondition{{key: mem, status: memValuePresent}}, behaviorDuration, behaviorDuration)
	b.timedOut = func(_ int64) bool { return false }
	b.canStillUse = func(t *TickLoop, e *Entity, _ int64) bool {
		n, ok := e.brain.getMemoryInt(mem)
		return ok && n > 0
	}
	b.tick = func(t *TickLoop, e *Entity, _ int64) {
		n, ok := e.brain.getMemoryInt(mem)
		if !ok {
			return
		}
		e.brain.setMemory(mem, n-1)
	}
	b.stop = func(t *TickLoop, e *Entity, _ int64) { e.brain.eraseMemory(mem) }
	return b
}

// newMoveToTargetSink ports net.minecraft.world.entity.ai.behavior.MoveToTargetSink(minTimeout,maxTimeout).
// CORE, entry {CANT_REACH_WALK_TARGET_SINCE REGISTERED, PATH VALUE_ABSENT, WALK_TARGET VALUE_PRESENT}.
// FLYER BRIDGE: instead of navigation.createPath/moveTo (the FlyingPathNavigation), it points the ghast
// move-control at the walk target (ghastWanted*) and lets ghastMoveControlTick drive the hover. reachedTarget
// (distManhattan <= closeEnoughDist) is honored so it stops on arrival; the A* path/cooldown machinery is
// cited-reduced (a flyer steers by move-control, not a ground path).
//
//	[VERIFIED CFR MoveToTargetSink: super(ImmutableMap.of(CANT_REACH_WALK_TARGET_SINCE REGISTERED, PATH
//	 VALUE_ABSENT, WALK_TARGET VALUE_PRESENT), minTimeout, maxTimeout); checkExtraStartConditions: if
//	 !reachedTarget && tryComputePath -> true, else eraseMemory(WALK_TARGET); canStillUse: !navigation
//	 .isDone() && walkTarget present && !reachedTarget; start: navigation.moveTo(path, speed); stop:
//	 navigation.stop(); eraseMemory(WALK_TARGET); eraseMemory(PATH); tick: re-path if the target moved
//	 >2 blocks. reachedTarget: walkTarget.target.currentBlockPosition().distManhattan(body) <=
//	 walkTarget.closeEnoughDist.]
func newMoveToTargetSink(minTimeout, maxTimeout int) *behavior {
	b := newBehavior([]memoryCondition{
		{key: memCantReachWalkTargetSince, status: memRegistered},
		{key: memPath, status: memValueAbsent},
		{key: memWalkTarget, status: memValuePresent},
	}, minTimeout, maxTimeout)
	b.checkExtraStart = func(t *TickLoop, e *Entity) bool {
		wt, ok := e.brain.getMemoryWalkTarget(memWalkTarget)
		if !ok {
			return false
		}
		if happyGhastReachedTarget(t, e, wt) {
			e.brain.eraseMemory(memWalkTarget)
			e.brain.eraseMemory(memCantReachWalkTargetSince)
			return false
		}
		// tryComputePath (flyer): commit the walk target to the ghast move-control.
		happyGhastMoveTo(t, e, wt)
		e.brain.eraseMemory(memCantReachWalkTargetSince)
		return true
	}
	b.canStillUse = func(t *TickLoop, e *Entity, _ int64) bool {
		wt, ok := e.brain.getMemoryWalkTarget(memWalkTarget)
		if !ok {
			return false
		}
		return !happyGhastNavDone(e) && !happyGhastReachedTarget(t, e, wt)
	}
	b.start = func(t *TickLoop, e *Entity, _ int64) {
		// navigation.moveTo(path, speed) analogue: (re)commit the wanted point.
		if wt, ok := e.brain.getMemoryWalkTarget(memWalkTarget); ok {
			happyGhastMoveTo(t, e, wt)
		}
	}
	b.stop = func(t *TickLoop, e *Entity, _ int64) {
		happyGhastNavStop(e)
		e.brain.eraseMemory(memWalkTarget)
		e.brain.eraseMemory(memPath)
	}
	b.tick = func(t *TickLoop, e *Entity, _ int64) {
		// Re-point at the (possibly moved) target each tick (vanilla re-paths when the target moves >2
		// blocks; the flyer re-commit is cheap and idempotent, so re-point every tick — same observable).
		if wt, ok := e.brain.getMemoryWalkTarget(memWalkTarget); ok {
			happyGhastMoveTo(t, e, wt)
		}
	}
	return b
}

// newFollowTemptation ports net.minecraft.world.entity.ai.behavior.FollowTemptation(speed, closeEnough,
// lookInEyes). IDLE, entry {LOOK_TARGET REGISTERED, WALK_TARGET REGISTERED, TEMPTATION_COOLDOWN_TICKS
// VALUE_ABSENT, IS_TEMPTED VALUE_ABSENT, TEMPTING_PLAYER VALUE_PRESENT, BREED_TARGET VALUE_ABSENT,
// IS_PANICKING VALUE_ABSENT}. Never times out; canStillUse while a tempting player exists and not
// breeding/panicking; start sets IS_TEMPTED; tick aims LOOK_TARGET at the player and sets/erases WALK_TARGET
// by distance; stop sets the 100-tick cooldown + clears IS_TEMPTED/WALK_TARGET/LOOK_TARGET.
//
//	[VERIFIED CFR FollowTemptation: entryCondition per above; timedOut=false; canStillUse: getTemptingPlayer
//	 .isPresent() && !hasMemoryValue(BREED_TARGET) && !hasMemoryValue(IS_PANICKING); start: setMemory(
//	 IS_TEMPTED,true); stop: setMemory(TEMPTATION_COOLDOWN_TICKS,100); erase IS_TEMPTED/WALK_TARGET/
//	 LOOK_TARGET; tick: setMemory(LOOK_TARGET, new EntityTracker(player,true)); if distSqr<closeEnough^2
//	 erase WALK_TARGET else setMemory(WALK_TARGET, new WalkTarget(new EntityTracker(player,eyes,eyes),
//	 speed, 2)).]
func newFollowTemptation(speed float32, closeEnough float64, lookInEyes bool) *behavior {
	b := newBehavior([]memoryCondition{
		{key: memLookTarget, status: memRegistered},
		{key: memWalkTarget, status: memRegistered},
		{key: memTemptationCooldownTicks, status: memValueAbsent},
		{key: memIsTempted, status: memValueAbsent},
		{key: memTemptingPlayer, status: memValuePresent},
		{key: memBreedTarget, status: memValueAbsent},
		{key: memIsPanicking, status: memValueAbsent},
	}, behaviorDuration, behaviorDuration)
	b.timedOut = func(_ int64) bool { return false }
	b.canStillUse = func(t *TickLoop, e *Entity, _ int64) bool {
		_, ok := e.brain.getMemoryEntityID(memTemptingPlayer)
		return ok && !e.brain.hasMemoryValue(memBreedTarget) && !e.brain.hasMemoryValue(memIsPanicking)
	}
	b.start = func(t *TickLoop, e *Entity, _ int64) { e.brain.setMemory(memIsTempted, true) }
	b.stop = func(t *TickLoop, e *Entity, _ int64) {
		e.brain.setMemory(memTemptationCooldownTicks, 100)
		e.brain.eraseMemory(memIsTempted)
		e.brain.eraseMemory(memWalkTarget)
		e.brain.eraseMemory(memLookTarget)
	}
	b.tick = func(t *TickLoop, e *Entity, _ int64) {
		pid, ok := e.brain.getMemoryEntityID(memTemptingPlayer)
		if !ok {
			return
		}
		e.brain.setMemory(memLookTarget, newEntityTracker(pid, true, false))
		px, py, pz, _, okp := t.resolveTargetPos(pid)
		if okp {
			dx, dy, dz := px-e.x, py-e.y, pz-e.z
			if dx*dx+dy*dy+dz*dz < closeEnough*closeEnough {
				e.brain.eraseMemory(memWalkTarget)
				return
			}
		}
		e.brain.setMemory(memWalkTarget, newWalkTarget(newEntityTracker(pid, lookInEyes, lookInEyes), speed, 2))
	}
	return b
}

// newBabyFollowAdult ports BabyFollowAdult.create(range, speed, nearestType, targetEye) — an OneShot with
// group {present(nearestType), registered(LOOK_TARGET), absent(WALK_TARGET)}. The trigger: if the body is
// a baby AND within (max+1) but NOT within min of the nearest adult, set WALK_TARGET (closeEnough=min-1) +
// LOOK_TARGET at the adult; else false.
//
//	[VERIFIED CFR BabyFollowAdult.create: group present(nearestVisibleType), registered(LOOK_TARGET),
//	 absent(WALK_TARGET); trigger: if !body.isBaby() return false; adult=get(nearestAdult); if
//	 body.closerThan(adult, max+1) && !body.closerThan(adult, min) { walkTarget.set(new WalkTarget(new
//	 EntityTracker(adult, targetEye, targetEye), speed, min-1)); lookTarget.set(new EntityTracker(adult,
//	 true, targetEye)); return true; } return false.]
func newBabyFollowAdult(followMin, followMax int, speed float32, nearestType memoryKey, targetEye bool) *oneShot {
	cond := []memoryCondition{
		{key: nearestType, status: memValuePresent},
		{key: memLookTarget, status: memRegistered},
		{key: memWalkTarget, status: memValueAbsent},
	}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		if !e.isBaby() {
			return false
		}
		adultID, ok := e.brain.getMemoryEntityID(nearestType)
		if !ok {
			return false
		}
		ax, ay, az, _, okp := t.resolveTargetPos(adultID)
		if !okp {
			return false
		}
		if !happyGhastCloserThan(e, ax, ay, az, float64(followMax+1)) || happyGhastCloserThan(e, ax, ay, az, float64(followMin)) {
			return false
		}
		e.brain.setMemory(memWalkTarget, newWalkTarget(newEntityTracker(adultID, targetEye, targetEye), speed, followMin-1))
		e.brain.setMemory(memLookTarget, newEntityTracker(adultID, true, targetEye))
		return true
	})
}

// newRandomStrollFly ports RandomStroll.fly(speed): an OneShot with group {absent(WALK_TARGET)}; the
// trigger sets WALK_TARGET to a random fly pos (getTargetFlyPos = AirAndWaterRandomPos.getPos(body,10,7,
// -2, viewX, viewZ, PI/2)) or erases it when null. canRun=true.
//
//	[VERIFIED CFR RandomStroll.fly -> strollFlyOrSwim(speed, body->getTargetFlyPos(body,10,7), b->true):
//	 group absent(WALK_TARGET); trigger: pos=Optional.ofNullable(fetchTargetPos.apply(body));
//	 walkTarget.setOrErase(pos.map(p->new WalkTarget(p, speed, 0))); return true. getTargetFlyPos:
//	 AirAndWaterRandomPos.getPos(body, 10, 7, -2, viewVector.x, viewVector.z, 1.5707963705062866).]
func newRandomStrollFly(speed float32) *oneShot {
	cond := []memoryCondition{{key: memWalkTarget, status: memValueAbsent}}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		px, py, pz, ok := happyGhastAirRandomPos(t, e)
		e.brain.setMemoryOrErase(memWalkTarget, newWalkTarget(blockPosTracker(px, py, pz), speed, 0), ok)
		return true
	})
}

// newSetWalkTargetFromLookTarget ports SetWalkTargetFromLookTarget.create(speed, closeEnough): an OneShot
// with group {absent(WALK_TARGET), present(LOOK_TARGET)}; the trigger sets WALK_TARGET to the look target
// (the same PositionTracker) at (speed, closeEnough). canSetWalkTarget defaults true.
//
//	[VERIFIED CFR SetWalkTargetFromLookTarget.create: group absent(WALK_TARGET), present(LOOK_TARGET);
//	 trigger: if !canSetWalkTargetPredicate.test(body) return false; walkTarget.set(new WalkTarget(get(
//	 lookTarget), speed, closeEnough)); return true.]
func newSetWalkTargetFromLookTarget(speed float32, closeEnough int) *oneShot {
	cond := []memoryCondition{
		{key: memWalkTarget, status: memValueAbsent},
		{key: memLookTarget, status: memValuePresent},
	}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		pt, ok := e.brain.getMemoryTarget(memLookTarget)
		if !ok {
			return false
		}
		e.brain.setMemory(memWalkTarget, newWalkTarget(pt, speed, closeEnough))
		return true
	})
}

// --- flyer movement/look/pos helpers (the vanilla nav/look primitives, reduced to the ghast hover) -----

// happyGhastMoveTo bridges a WALK_TARGET to the ghast move-control (ghastWanted*): resolve the target
// current world position and commit it as the wanted point. ghastMoveControlTick (ai_goals_happy_ghast.go)
// then accelerates the ghast toward it — the flyer analogue of navigation.moveTo(path, speed).
func happyGhastMoveTo(t *TickLoop, e *Entity, wt walkTarget) {
	x, y, z := wt.target.currentPosition(t)
	e.ghastWantedX, e.ghastWantedY, e.ghastWantedZ = x, y, z
	e.ghastHasWanted = true
}

// happyGhastNavStop ports navigation.stop() for the flyer: clear the wanted point (WAIT).
func happyGhastNavStop(e *Entity) { e.ghastHasWanted = false }

// happyGhastNavDone ports navigation.isDone() for the flyer: no wanted point committed.
func happyGhastNavDone(e *Entity) bool { return !e.ghastHasWanted }

// happyGhastReachedTarget ports MoveToTargetSink.reachedTarget: distManhattan(target.currentBlockPosition(),
// body.blockPosition()) <= walkTarget.closeEnoughDist.
//
//	[VERIFIED CFR MoveToTargetSink.reachedTarget: distManhattan(walkTarget.target.currentBlockPosition(),
//	 body.blockPosition()) <= walkTarget.getCloseEnoughDist().]
func happyGhastReachedTarget(t *TickLoop, e *Entity, wt walkTarget) bool {
	tx, ty, tz := wt.target.currentPosition(t)
	return distManhattanBlocks(tx, ty, tz, e.x, e.y, e.z) <= wt.closeEnoughDist
}

// happyGhastCloserThan ports Entity.closerThan(entity, dist): distanceToSqr < dist*dist (center distance
// for point targets — the ghast follow band is coarse enough that the center metric is observably identical).
//
//	[VERIFIED CFR Entity.closerThan: distanceToSqr(entity) < dist*dist.]
func happyGhastCloserThan(e *Entity, x, y, z, dist float64) bool {
	dx, dy, dz := x-e.x, y-e.y, z-e.z
	return dx*dx+dy*dy+dz*dz < dist*dist
}

// happyGhastLookAt ports LookControl.setLookAt(Vec3): aim the head/body yaw at the point (the same
// headYaw/yaw write the classic look goals use, ai_goals_passive.go). Pitch is left to physics/render.
func happyGhastLookAt(e *Entity, x, y, z float64) {
	yaw := yawTowardDeg(x-e.x, z-e.z)
	e.headYaw = yaw
	e.yaw = yaw
}

// happyGhastAirRandomPos ports AirAndWaterRandomPos.getPos for the ghast, REUSING the classic ghast
// fly-to selection (ghastGetSuitableFlyToPosition) — the SAME open-air chooser the adult ghast uses, so
// the baby brain stroll/panic targets are jar-faithful fly-to points (and draw the same per-mob RNG).
// ok=false only when world-less (a test with no terrain) -> setOrErase clears the walk target (vanilla
// null fetchTargetPos -> erase).
//
//	[VERIFIED CFR RandomStroll.getTargetFlyPos / AnimalPanic(flyHeight): AirAndWaterRandomPos.getPos picks
//	 an air position near the mob; ghastGetSuitableFlyToPosition is the ported air-position chooser.]
func happyGhastAirRandomPos(t *TickLoop, e *Entity) (x, y, z float64, ok bool) {
	if t.world() == nil {
		return 0, 0, 0, false
	}
	x, y, z = t.ghastGetSuitableFlyToPosition(e, ghastFloatAroundDistanceToBlocks)
	return x, y, z, true
}

// happyGhastAttrTemptRange reads TEMPT_RANGE (the TemptingSensor targeting range, jar default 10.0).
func happyGhastAttrTemptRange(e *Entity) float64 { return e.getAttributeValue(attribute.TemptRange) }

// happyGhastAttrFollowRange reads FOLLOW_RANGE (the NearestLivingEntity/adult sensor inflate radius).
func happyGhastAttrFollowRange(e *Entity) float64 { return e.getAttributeValue(attribute.FollowRange) }

// keep the math import used (currentPosition math lives in brain.go; a future edit may reference it here).
var _ = math.Abs
