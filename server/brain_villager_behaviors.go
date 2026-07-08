package server

// brain_villager_behaviors.go -- the per-Activity movement behaviors the villager WORK/MEET/REST/PANIC
// packages register, ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p
// this session). These are the LOAD-BEARING relocation behaviors of the schedule: WORK/MEET/REST each
// walk the villager to its claimed POI (job-site / meeting bell / bed via HOME), and PANIC flees the
// threat. Cite: VillagerGoalPackages.get{Work,Meet,Rest,Panic}Package, SetWalkTargetFromBlockMemory,
// SetWalkTargetAwayFrom, VillagerPanicTrigger, VillagerCalmDown, UpdateActivityFromSchedule.
//
// DEFERRED (cited): workstation WORK actions (WorkAtPoi, StrollToPoi/StrollToPoiList, SetLookAndInteract),
// MEET social behaviors (SocializeAtBell, InteractWith child-play, trade chains), REST SleepInBed pose
// (mob sleep-pose seam not built) + InsideBrownianWalk/GoToClosestVillage/SetClosestHomeAsWalkTarget
// fallbacks, RingBell/ResetRaidStatus (raid coupling), and the DefaultRandomPos partial-step +
// JitteredLinearRetry reachability backoff (async nav). The landed slice is walk-to-POI + flee movement.

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// newSetWalkTargetFromBlockMemory ports SetWalkTargetFromBlockMemory.create(mem, speed, closeEnough,
// maxDistanceFromPoi, maxRetries) -- the WORK/MEET/REST walk-to-POI behavior. entry group:
// registered(CANT_REACH_WALK_TARGET_SINCE), absent(WALK_TARGET), present(mem). The trigger reads the
// GlobalPos in mem; a cross-dimension POI is released+erased; else a WALK_TARGET is set toward the POI.
//
//	[VERIFIED javap SetWalkTargetFromBlockMemory.create: group(registered(CANT_REACH_WALK_TARGET_SINCE),
//	 absent(WALK_TARGET), present(mem)); trigger: gp=get(mem); if gp.dimension != level.dimension OR
//	 (CANT_REACH present && now-cantReach > maxRetries) -> releasePoi(mem)+erase(mem); else if
//	 distManhattan(gp.pos, body) > maxDistanceFromPoi -> WALK_TARGET = DefaultRandomPos.getPosTowards
//	 partial step; else WALK_TARGET = new WalkTarget(new BlockPosTracker(gp.pos), speed, closeEnough). The
//	 DefaultRandomPos partial-step + cant-reach retry backoff are cite-reduced (async nav): the walk target
//	 is set straight to the POI at (speed, closeEnough) -- the same walk-to-the-POI observable when reachable.]
func newSetWalkTargetFromBlockMemory(mem memoryKey, speed float32, closeEnough int) *oneShot {
	cond := []memoryCondition{
		{key: memCantReachWalkTargetSince, status: memRegistered},
		{key: memWalkTarget, status: memValueAbsent},
		{key: mem, status: memValuePresent},
	}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		v, ok := e.brain.getMemory(mem)
		if !ok {
			return false
		}
		pos, ok := v.(pk.Position)
		if !ok {
			return false
		}
		// dimension check is cite-reduced: v1 stores a bare block pos (one overworld region per villager),
		// so a same-dimension POI always matches; the cross-dimension releasePoi path is deferred with the
		// GlobalPos.dimension field. Set the walk target toward the POI block center (Vec3.atCenterOf).
		e.brain.eraseMemory(memCantReachWalkTargetSince)
		center := blockPosTracker(float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5)
		e.brain.setMemory(memWalkTarget, newWalkTarget(center, speed, closeEnough))
		return true
	})
}

// newSetWalkTargetAwayFromEntity ports SetWalkTargetAwayFrom.entity(mem, speed, desiredDistance,
// retainTarget) -- the PANIC flee-on-hurt behavior. entry group: registered(WALK_TARGET), present(mem).
// The trigger reads the threat entity id from mem; if the body is within desiredDistance it sets a
// WALK_TARGET at a point AWAY from the threat.
//
//	[VERIFIED javap SetWalkTargetAwayFrom.entity -> create(mem, speed, dist, retainTarget, Entity::position):
//	 group(registered(WALK_TARGET), present(mem)); trigger: target=posGetter(get(mem)); if !retainTarget &&
//	 body NOT within dist -> false; pos=LandRandomPos.getPosAway(body,16,7,target); if pos==null -> false;
//	 WALK_TARGET = new WalkTarget(pos, speed, 0); return true. The getPosAway cone-scatter chooser is
//	 cite-reduced to a straight 16-block step opposite the threat -- the same flee-away observable; the
//	 DefaultRandomPos scatter is deferred with the async nav.]
func newSetWalkTargetAwayFromEntity(mem memoryKey, speed float32, desiredDistance int, retainTarget bool) *oneShot {
	cond := []memoryCondition{
		{key: memWalkTarget, status: memRegistered},
		{key: mem, status: memValuePresent},
	}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		id, ok := e.brain.getMemoryEntityID(mem)
		if !ok {
			return false
		}
		tx, ty, tz, _, okp := t.resolveTargetPos(id)
		if !okp {
			return false
		}
		dx, dy, dz := tx-e.x, ty-e.y, tz-e.z
		distSqr := dx*dx + dy*dy + dz*dz
		dd := float64(desiredDistance)
		if !retainTarget && distSqr >= dd*dd {
			return false // already far enough from the threat
		}
		away := villagerAwayStep(e.x, e.y, e.z, tx, tz, 16.0)
		e.brain.setMemory(memWalkTarget, newWalkTarget(blockPosTracker(away[0], away[1], away[2]), speed, 0))
		return true
	})
}

// villagerAwayStep returns a point dist blocks from (x,y,z) directly opposite the threat at (txx,tzz).
// The vanilla LandRandomPos.getPosAway scatters within a cone; the straight opposite step is the reduced
// deterministic form (same flee direction). y is kept at the body y (ground flee).
func villagerAwayStep(x, y, z, txx, tzz, dist float64) [3]float64 {
	ax, az := x-txx, z-tzz
	mag := ax*ax + az*az
	if mag <= 1e-9 {
		return [3]float64{x + dist, y, z} // degenerate: threat on top of us -> step +X
	}
	inv := dist / math.Sqrt(mag)
	return [3]float64{x + ax*inv, y, z + az*inv}
}

// newVillagerPanicTrigger ports net.minecraft.world.entity.ai.behavior.VillagerPanicTrigger: a CORE
// Behavior (empty entryCondition) that, while the villager is hurt or has a nearby hostile, forces the
// PANIC activity. checkExtraStartConditions = isHurt || hasHostile; start (when not already in PANIC)
// erases PATH/WALK_TARGET/LOOK_TARGET/BREED_TARGET/INTERACTION_TARGET and setActiveActivityIfPossible(PANIC).
//
//	[VERIFIED javap VillagerPanicTrigger: super(ImmutableMap.of()); checkExtraStartConditions: isHurt(body)
//	 || hasHostile(body); isHurt = brain.hasMemoryValue(HURT_BY); hasHostile = brain.hasMemoryValue(
//	 NEAREST_HOSTILE). start: if (isHurt || hasHostile) { if (!brain.isActive(PANIC)) { erase PATH,
//	 WALK_TARGET, LOOK_TARGET, BREED_TARGET, INTERACTION_TARGET; brain.setActiveActivityIfPossible(PANIC); }
//	 spawnGolemIfNeeded; }. spawnGolemIfNeeded is cite-deferred (iron-golem village defense not built).]
func newVillagerPanicTrigger() *behavior {
	b := newBehavior(nil, behaviorDuration, behaviorDuration)
	b.checkExtraStart = func(t *TickLoop, e *Entity) bool {
		return villagerIsHurt(e) || villagerHasHostile(e)
	}
	b.start = func(t *TickLoop, e *Entity, _ int64) {
		if !(villagerIsHurt(e) || villagerHasHostile(e)) {
			return
		}
		if !e.brain.isActive(activityPanic) {
			e.brain.eraseMemory(memPath)
			e.brain.eraseMemory(memWalkTarget)
			e.brain.eraseMemory(memLookTarget)
			e.brain.eraseMemory(memBreedTarget)
			e.brain.eraseMemory(memInteractionTarget)
			e.brain.setActiveActivityIfPossible(activityPanic)
		}
		// spawnGolemIfNeeded(level, gameTime, 3) -- cite-deferred (iron-golem village defense).
	}
	return b
}

// villagerIsHurt ports VillagerPanicTrigger.isHurt: brain.hasMemoryValue(HURT_BY).
func villagerIsHurt(e *Entity) bool { return e.brain.hasMemoryValue(memHurtBy) }

// villagerHasHostile ports VillagerPanicTrigger.hasHostile: brain.hasMemoryValue(NEAREST_HOSTILE).
func villagerHasHostile(e *Entity) bool { return e.brain.hasMemoryValue(memNearestHostile) }

// newVillagerCalmDown ports net.minecraft.world.entity.ai.behavior.VillagerCalmDown: a PANIC-package
// OneShot (group registered(HURT_BY, HURT_BY_ENTITY, NEAREST_HOSTILE)) that, once the villager is no
// longer hurt and has no hostile, erases HURT_BY + HURT_BY_ENTITY and re-runs updateActivityFromSchedule
// (reverting from PANIC back to the scheduled activity).
//
//	[VERIFIED javap VillagerCalmDown.create: group(registered(HURT_BY), registered(HURT_BY_ENTITY),
//	 registered(NEAREST_HOSTILE)); trigger: notHurt = HURT_BY absent && HURT_BY_ENTITY absent; noHostile =
//	 NEAREST_HOSTILE.filter(closerThan) absent; if (notHurt && noHostile) { hurtBy.erase(); hurtByEntity
//	 .erase(); brain.updateActivityFromSchedule(system, gameTime, position); return true; } return false.
//	 The NEAREST_HOSTILE distance filter is the hostile sensor range, reduced here to memory-absent (the
//	 hostile sensor writes NEAREST_HOSTILE only for in-range hostiles).]
func newVillagerCalmDown() *oneShot {
	cond := []memoryCondition{
		{key: memHurtBy, status: memRegistered},
		{key: memHurtByEntity, status: memRegistered},
		{key: memNearestHostile, status: memRegistered},
	}
	return newOneShot(cond, func(t *TickLoop, e *Entity, _ int64) bool {
		notHurt := !e.brain.hasMemoryValue(memHurtBy) && !e.brain.hasMemoryValue(memHurtByEntity)
		noHostile := !e.brain.hasMemoryValue(memNearestHostile)
		if !(notHurt && noHostile) {
			return false
		}
		e.brain.eraseMemory(memHurtBy)
		e.brain.eraseMemory(memHurtByEntity)
		e.brain.updateActivityFromSchedule(t.villagerDayTime())
		return true
	})
}

// newUpdateActivityFromSchedule ports UpdateActivityFromSchedule.create(): the priority-99 OneShot each of
// the WORK/MEET/REST/IDLE packages holds. Empty group; the trigger drives brain.updateActivityFromSchedule
// with the day-time and returns true, keeping the active Activity in sync with the schedule.
//
//	[VERIFIED javap UpdateActivityFromSchedule.create: point((level, body, ts) -> { body.getBrain()
//	 .updateActivityFromSchedule(level.environmentAttributes(), level.getGameTime(), body.position());
//	 return true; }).]
func newUpdateActivityFromSchedule() *oneShot {
	return newOneShot(nil, func(t *TickLoop, e *Entity, _ int64) bool {
		e.brain.updateActivityFromSchedule(t.villagerDayTime())
		return true
	})
}

// villagerDayTime returns the overworld-clock day-time the schedule sampler consumes: the raw gametime
// (the sampler wraps it via floorMod over the 24000-tick period, so passing gametime is identical to
// passing gametime % 24000 -- the same day-time proxy fire.go uses). VERIFIED overworld ClockManager
// totalTicks advances 1/tick == the day-time counter.
func (t *TickLoop) villagerDayTime() int64 { return t.GameTime() }
