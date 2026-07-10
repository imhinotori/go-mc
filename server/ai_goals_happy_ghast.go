package server

// ai_goals_happy_ghast.go — the HappyGhast FLIGHT subsystem, PORTED 1:1 from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p / CFR this task). The Happy Ghast is a HOVERING flyer: it does
// NOT fall (HappyGhast.travel uses travelFlying, which applies NO gravity). Its wander is driven by the
// classic Ghast goals it inherits via registerGoals @5 Ghast.RandomFloatAroundGoal(this, 16) + the
// Ghast.GhastMoveControl move controller — both host-native here (they drive velocity via the moveControl,
// NOT the ground A* pathfinder, so they cannot go through the .star nav seam).
//
// PORTED classes/methods (all javap/CFR-cited):
//   - HappyGhast.travel(Vec3): airSpeed = (float)getAttributeValue(FLYING_SPEED) * 5.0f / 3.0f;
//     travelFlying(input, airSpeed, airSpeed, airSpeed).
//   - LivingEntity.travelFlying AIR branch: moveRelative(air, input); move(deltaMovement); deltaMovement
//     *= 0.91f. (NO gravity.) For an unmounted ghast input is ~0 so moveRelative adds ~nothing; the motion
//     is the moveControl deltaMovement kick + the 0.91 drift.
//   - Ghast.GhastMoveControl.tick: if shouldBeStopped then WAIT/stopInPlace (v1: never stopped, cited); if
//     operation != MOVE_TO return; if (floatDuration-- <= 0){ floatDuration += nextInt(5)+2; travel =
//     wanted-pos; if canReach(travel) deltaMovement += travel.normalize().scale(getAttributeValue(
//     FLYING_SPEED)*5.0/3.0) else WAIT }.
//   - Ghast.RandomFloatAroundGoal(mob, 16): canUse = not hasWanted OR dist-sq of wanted <1.0 OR >3600.0;
//     start = getSuitableFlyToPosition(mob, 16) then setWantedPosition(x,y,z, 1.0); canContinueToUse=false.
//   - getSuitableFlyToPosition: 64 attempts of chooseRandomPosition = center + (nextFloat()*2-1)*16 per
//     axis (x,y,z draw order); keep the first isGoodTarget (air block with an open neighbor within
//     distanceToBlocks); else the LAST raw candidate; then the MOTION_BLOCKING heightmap DOWN-clamp.
//
// v1 STUBS (cited): (1) shouldBeStopped (isOnStillTimeout / ride-still) is always false (ride + still-timeout
// deferred, .star header), so the ghast is never force-stopped. (2) GhastMoveControl.canReach's full careful
// AABB block-traversal (HAPPY_GHAST_AVOIDS tag + fluid checks) is v1-reduced to a destination-block air/solid
// reach check — the ghast steers around solids but does not yet honor the avoid-tag; the RNG DRAW ORDER (the
// nextInt(5)+2 cadence + the getSuitableFlyToPosition nextFloat candidates) is preserved EXACTLY for lockstep.
// (3) isGoodTarget uses isSolidAt (air == !solid) for BlockState.isAir, and the heightmap clamp uses a column
// solid-scan — same observable "aim at open air, do not dive into terrain" behavior.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// ghastFloatAroundDistanceToBlocks is the Ghast.RandomFloatAroundGoal ctor arg the HappyGhast passes:
// registerGoals @5 new Ghast.RandomFloatAroundGoal(this, 16) (bipush 16). Bounds the open-neighbor scan.
const ghastFloatAroundDistanceToBlocks = 16

// ghastFloatAroundRadius is chooseRandomPosition's per-axis spread: center + (nextFloat()*2-1)*16.0.
const ghastFloatAroundRadius = 16.0

// ghastRepathMinDistSqr / ghastRepathMaxDistSqr are RandomFloatAroundGoal.canUse's re-roll bounds: re-pick
// a fly-to point when the current wanted is <1.0 (arrived) or >3600.0 (60 blocks, drifted) away.
const (
	ghastRepathMinDistSqr = 1.0
	ghastRepathMaxDistSqr = 3600.0
)

// ghastFlyingDrag is travelFlying's AIR deltaMovement scale (deltaMovement *= 0.91f) — the hover drift decay.
const ghastFlyingDrag = 0.91

// happyGhastFlyingSpeedFactor is the GhastMoveControl.tick / HappyGhast.travel accel multiplier:
// getAttributeValue(FLYING_SPEED) * 5.0 / 3.0.
const happyGhastFlyingSpeedFactor = 5.0 / 3.0

// happyGhastAiStep is the per-type hook (sibling of creeperAiStep/endermanAiStep) that drives the HappyGhast
// FLIGHT: RandomFloatAroundGoal (pick a fly-to point) + GhastMoveControl.tick (accelerate deltaMovement
// toward it). Called from tickAI for a live happy ghast (typ == entity.HappyGhast.ID), AFTER serverAiStep.
// The 0.91 flying drag + the NO-gravity integration happen in tickPhysics (gated on the same type).
//
// GOAL-REGISTRATION STUB (cited, HappyGhast.registerGoals verified javap this task): the jar registers
// @3 new HappyGhast.HappyGhastFloatGoal(this) (a FloatGoal: swim-UP out of water) and @4 new TemptGoal.
// ForNonPathfinders(this, 1.0, HAPPY_GHAST_TEMPT_ITEMS predicate, canScare=false, closeEnough=7.0)
// BEFORE the @5 RandomFloatAroundGoal(16) this function inlines. The happy ghast here is driven by THIS
// per-type hook, NOT a goalSelector (spawnDeclaredMob wires no GoalSelector to the ghast -- see ghast.go
// "NO goalSelector"), so there is no goal slot to add HappyGhastFloatGoal @3 / TemptGoal @4 into without
// re-architecting the ghast onto ai_mob.go's goalSelector (which would also perturb the flight RNG order).
// The floatGoal + temptGoal PORTS exist (ai_goals_float.go / ai_goals_passive.go, ctors newFloatGoal /
// newTemptGoal(1.0, HAPPY_GHAST_TEMPT_ITEMS, false, ...)) so this wires in verbatim once the ghast gains a
// goalSelector; leaving a cited stub rather than fabricating a half-wired goal. Cite HappyGhast.registerGoals
// (@3 HappyGhastFloatGoal, @4 TemptGoal.ForNonPathfinders(1.0, HAPPY_GHAST_TEMPT_ITEMS, false, 7.0)).
func (t *TickLoop) happyGhastAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	// RIDDEN suppression (passenger.go): when a controlling passenger steers the ghast (getControlling
	// Passenger != 0), the CLIENT owns the motion (client-authoritative vehicle movement via Serverbound
	// MoveVehicle), so the server-side RandomFloatAroundGoal + GhastMoveControl must NOT run — vanilla
	// routes a ridden mob through travelRidden instead of the moveControl-driven serverAiStep, and
	// GhastMoveControl.shouldBeStopped returns true while ridden (isOnStillTimeout). Skipping the fly-to
	// re-roll + the deltaMovement kick here is that shouldBeStopped==true branch: the ghast holds its
	// wanted state and the client's steer (handleMoveVehicle) is the sole motion. Cite Ghast$GhastMove
	// Control.tick (shouldBeStopped -> stopInPlace) + HappyGhast.getControllingPassenger.
	if t.getControllingPassenger(e) != 0 {
		return
	}
	// RandomFloatAroundGoal: re-roll the fly-to target when there is none, or the current one is reached
	// (<1-sq) or drifted too far (>3600-sq). This is the goal's canUse; when true it runs start(). This
	// runs for EVERY happy ghast (registerGoals adds RandomFloatAroundGoal@5 un-gated on age — VERIFIED
	// CFR HappyGhast.registerGoals) — it is the goalSelector slot of Mob.serverAiStep.
	if t.ghastRandomFloatAroundCanUse(e) {
		t.ghastRandomFloatAroundStart(e)
	}
	// customServerAiStep: for the BABY, the Brain runs HERE — AFTER the goalSelector set the classic
	// wanted point and BEFORE the move-control tick consumes it (VERIFIED Mob.serverAiStep order:
	// goalSelector.tick -> navigation.tick -> customServerAiStep -> controls/moveControl.tick). The brain
	// MoveToTargetSink overwrites ghastWanted* with the walk-target the baby behaviors chose, so the baby
	// is brain-steered while the adult keeps the classic RandomFloatAroundGoal wanted. Adult: no-op.
	t.happyGhastBabyBrainTick(e)
	// GhastMoveControl.tick shouldBeStopped branch: HappyGhast wires shouldBeStopped = isOnStillTimeout()
	// (VERIFIED HappyGhast.registerGoals -> new Ghast.GhastMoveControl(this, ..., this::isOnStillTimeout)).
	// When true the moveControl sets operation=WAIT + stopInPlace() and RETURNS BEFORE the floatDuration
	// nextInt(5) draw (bytecode offset 36 return, ahead of the getRandom().nextInt(5) at 63) -- so the
	// frozen ghast holds position and draws NOTHING from the kick this tick. The RandomFloatAroundGoal
	// above STILL ran (its canUse/start draws are the goalSelector slot, independent of shouldBeStopped),
	// preserving the RNG draw order exactly. Cite Ghast$GhastMoveControl.tick + HappyGhast.isOnStillTimeout.
	if happyGhastIsOnStillTimeout(e) {
		e.ghastHasWanted = false // operation := WAIT (moveControl no longer MOVE_TO)
		// stopInPlace(): setDeltaMovement(0,0,0) -- the freeze (navigation.stop + zero input are ground-nav
		// no-ops for the flyer). Cite Mob.stopInPlace.
		e.vx, e.vy, e.vz = 0, 0, 0
		return
	}
	// GhastMoveControl.tick: accelerate deltaMovement toward the (final) wanted point on the floatDuration
	// cadence — the controls slot, AFTER customServerAiStep.
	t.ghastMoveControlTick(e)
}

// ghastRandomFloatAroundCanUse ports Ghast.RandomFloatAroundGoal.canUse: true if the moveControl has no
// wanted position, or the current wanted's squared distance from the ghast is <1.0 or >3600.0. NO RNG.
func (t *TickLoop) ghastRandomFloatAroundCanUse(e *Entity) bool {
	if !e.ghastHasWanted {
		return true
	}
	xd := e.ghastWantedX - e.x
	yd := e.ghastWantedY - e.y
	zd := e.ghastWantedZ - e.z
	dd := xd*xd + yd*yd + zd*zd
	return dd < ghastRepathMinDistSqr || dd > ghastRepathMaxDistSqr
}

// ghastRandomFloatAroundStart ports Ghast.RandomFloatAroundGoal.start: result = getSuitableFlyToPosition(
// mob, 16); moveControl.setWantedPosition(result.x, result.y, result.z, 1.0).
func (t *TickLoop) ghastRandomFloatAroundStart(e *Entity) {
	x, y, z := t.ghastGetSuitableFlyToPosition(e, ghastFloatAroundDistanceToBlocks)
	e.ghastWantedX, e.ghastWantedY, e.ghastWantedZ = x, y, z
	e.ghastHasWanted = true
}

// ghastGetSuitableFlyToPosition ports Ghast.RandomFloatAroundGoal.getSuitableFlyToPosition: up to 64
// attempts of chooseRandomPosition (center + (nextFloat()*2-1)*16 per axis, x/y/z draw order), returning the
// first isGoodTarget; else the LAST raw candidate; then the MOTION_BLOCKING heightmap DOWN-clamp. The RNG
// draw order is preserved EXACTLY (3 nextFloat per attempt; vanilla stops drawing on the good-target return).
func (t *TickLoop) ghastGetSuitableFlyToPosition(e *Entity, distanceToBlocks int) (float64, float64, float64) {
	r := mobRandom(e)
	var rx, ry, rz float64
	found := false
	for i := 0; i < 64; i++ {
		// chooseRandomPosition: center + (nextFloat()*2-1)*16.0 per axis. DRAW ORDER x, y, z.
		cx := e.x + float64(r.nextFloat()*2.0-1.0)*ghastFloatAroundRadius
		cy := e.y + float64(r.nextFloat()*2.0-1.0)*ghastFloatAroundRadius
		cz := e.z + float64(r.nextFloat()*2.0-1.0)*ghastFloatAroundRadius
		// (chooseRandomPositionWithRestriction's hasHome/isWithinHome gate: v1 has no home-restriction, so
		// the candidate is never rejected here — matches an un-homed ghast.)
		rx, ry, rz = cx, cy, cz // remember the last candidate (vanilla keeps `result` across the loop)
		if t.ghastIsGoodTarget(cx, cy, cz, distanceToBlocks) {
			found = true
			break // vanilla returns immediately on the first good target (no further draws)
		}
	}
	_ = found
	// MOTION_BLOCKING heightmap DOWN-clamp: if the terrain top at (rx,rz) is below ry (and above minY), the
	// target dips below the mob — result.y = mobY - |mobY - ry|. v1 uses a column solid-scan for the top.
	topY := t.ghastMotionBlockingTop(int(math.Floor(rx)), int(math.Floor(rz)))
	if float64(topY) < ry && topY > dimMinY {
		ry = e.y - math.Abs(e.y-ry)
	}
	return rx, ry, rz
}

// ghastIsGoodTarget ports Ghast.RandomFloatAroundGoal.isGoodTarget: distanceToBlocks<=0 → true; else the
// block must be air AND have at least one non-air neighbor within distanceToBlocks in some Direction (aim at
// open air near a surface, not deep sky or inside terrain). v1: air == !isSolidAt (cited stub).
func (t *TickLoop) ghastIsGoodTarget(x, y, z float64, distanceToBlocks int) bool {
	if distanceToBlocks <= 0 {
		return true
	}
	if t.world() == nil {
		return true // world-less tests: accept (no terrain to check against)
	}
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	if t.isSolidAt(pk.Position{X: bx, Y: by, Z: bz}) {
		return false // must be air (isAir → !solid)
	}
	// 6 Directions × [1, distanceToBlocks): a solid neighbor makes this a good target.
	dirs := [6][3]int{{0, 1, 0}, {0, -1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for _, d := range dirs {
		for i := 1; i < distanceToBlocks; i++ {
			if t.isSolidAt(pk.Position{X: bx + d[0]*i, Y: by + d[1]*i, Z: bz + d[2]*i}) {
				return true
			}
		}
	}
	return false
}

// ghastMotionBlockingTop scans DOWN from the top for the first solid block at column (x,z) — the v1
// substitute for Level.getHeight(MOTION_BLOCKING, x, z). Returns dimMinY if the column is all air.
func (t *TickLoop) ghastMotionBlockingTop(x, z int) int {
	if t.world() == nil {
		return dimMinY
	}
	// Overworld build ceiling: dimMinY(-64) + 384 height == 320 (the highest block Y). Scan DOWN from there.
	const ghastColumnTopY = dimMinY + 384
	for cy := ghastColumnTopY; cy > dimMinY; cy-- {
		if t.isSolidAt(pk.Position{X: x, Y: cy, Z: z}) {
			return cy
		}
	}
	return dimMinY
}

// ghastMoveControlTick ports Ghast.GhastMoveControl.tick. shouldBeStopped is the v1 always-false stub (no
// ride/still-timeout). While a wanted point is set (operation MOVE_TO analogue = ghastHasWanted), on the
// floatDuration cadence (-- <= 0 → += nextInt(5)+2) it kicks deltaMovement toward the wanted point by
// travel.normalize() * FLYING_SPEED*5/3, or clears the want (WAIT) if it cannot reach.
func (t *TickLoop) ghastMoveControlTick(e *Entity) {
	if !e.ghastHasWanted {
		return // operation != MOVE_TO
	}
	e.ghastFloatDuration--
	if e.ghastFloatDuration > 0 {
		return
	}
	// floatDuration += nextInt(5) + 2. DRAW: one nextInt(5) per accel kick.
	e.ghastFloatDuration += int32(mobRandom(e).nextInt(5) + 2)
	tx := e.ghastWantedX - e.x
	ty := e.ghastWantedY - e.y
	tz := e.ghastWantedZ - e.z
	if !t.ghastCanReach(e, tx, ty, tz) {
		e.ghastHasWanted = false // operation = WAIT
		return
	}
	// travel.normalize().scale(FLYING_SPEED * 5.0/3.0) added to deltaMovement.
	length := math.Sqrt(tx*tx + ty*ty + tz*tz)
	if length < 1.0e-4 {
		return // Vec3.normalize of a ~zero vector is zero (guarded); no kick this tick
	}
	scale := e.getAttributeValue(attribute.FlyingSpeed) * happyGhastFlyingSpeedFactor
	e.vx += (tx / length) * scale
	e.vy += (ty / length) * scale
	e.vz += (tz / length) * scale
}

// ghastCanReach is the v1-reduced port of GhastMoveControl.canReach: vanilla sweeps the AABB along `travel`
// checking each intersected block is traversable (careful mode also rejects the HAPPY_GHAST_AVOIDS tag +
// impassable fluids). v1 reduces this to a destination-block air check: the ghast will not kick toward a
// point buried in a solid (so it steers around terrain), but does not yet honor the avoid-tag (cited).
func (t *TickLoop) ghastCanReach(e *Entity, tx, ty, tz float64) bool {
	if t.world() == nil {
		return true // world-less tests: always reachable
	}
	dx := int(math.Floor(e.x + tx))
	dy := int(math.Floor(e.y + ty))
	dz := int(math.Floor(e.z + tz))
	return !t.isSolidAt(pk.Position{X: dx, Y: dy, Z: dz})
}

// happyGhastIsFlyer reports whether an entity is a happy ghast (the tickPhysics gravity gate reads this to
// take the travelFlying NO-gravity + 0.91-drift branch instead of the ground gravity+friction branch). Cite
// HappyGhast.travel → travelFlying (no gravity).
func happyGhastIsFlyer(e *Entity) bool {
	return e.typ == entity.HappyGhast.ID
}

// happyGhastBabyScale is HappyGhast.BABY_SCALE (public static final float 0.2375f) — the age scale a baby
// happy ghast (a "ghastling") renders at. Cite HappyGhast.BABY_SCALE.
const happyGhastBabyScale = 0.2375

// happyGhastAgeScale ports HappyGhast.getAgeScale(): isBaby() ? 0.2375f : 1.0f (MAX_SCALE 1.0f). The scale
// feeds the entity dimensions (BABY_DIMENSIONS = base.scale(0.2375).withEyeHeight(0.46875)) + the client
// render size. Cite HappyGhast.getAgeScale.
func happyGhastAgeScale(e *Entity) float32 {
	if e.isBaby() {
		return happyGhastBabyScale
	}
	return 1.0 // MAX_SCALE
}

// --- HAPPY GHAST STILL-TIMEOUT STATE MACHINE -----------------------------------------------------
//
// Ported 1:1 from HappyGhast (javap this task). A happy ghast becomes "on still timeout" whenever a
// non-riding player stands on top of it: it FREEZES (holds position, is not steerable while ridden) for
// MAX_STILL_TIMEOUT (10) ticks past the last such moment. The state:
//   serverStillTimeout (int, max 10): counts DOWN; forced to 10 by scanPlayerAboveGhast.
//   STAYS_STILL (synched bool): syncStayStillFlag sets it to (serverStillTimeout > 0).
//   isOnStillTimeout(): staysStill() || serverStillTimeout > 0.
// Cite HappyGhast.tick / setServerStillTimeout / syncStayStillFlag / staysStill / isOnStillTimeout /
// scanPlayerAboveGhast / aiStep.

const (
	// happyGhastMaxStillTimeout is HappyGhast.MAX_STILL_TIMEOUT (10): the value scanPlayerAboveGhast forces
	// serverStillTimeout to, and the effective clamp (it only ever counts down from 10). Verified javap.
	happyGhastMaxStillTimeout = 10
	// happyGhastStillLoadGrace is HappyGhast.STILL_TIMEOUT_ON_LOAD_GRACE_PERIOD (60): tick() only decrements
	// serverStillTimeout once tickCount > 60, so a ghast loaded WITH a still_timeout holds it 60 ticks.
	happyGhastStillLoadGrace = 60
	// happyGhastScanTopEpsilon is scanPlayerAboveGhast's maxY inset: 9.999999747378752E-6 (a float 1e-5),
	// the AABB(minX-1, maxY - 1e-5, minZ-1, maxX+1, maxY + ysize/2, maxZ+1) top slab. Verified javap (ldc2_w).
	happyGhastScanTopEpsilon = 9.999999747378752e-6
)

// happyGhastStaysStill ports HappyGhast.staysStill(): reads the STAYS_STILL synched entity data. In v1 that
// synched bool is the ghastStaysStill field (kept in lockstep by happyGhastSyncStayStillFlag). Cite staysStill.
func happyGhastStaysStill(e *Entity) bool { return e.ghastStaysStill }

// happyGhastIsOnStillTimeout ports HappyGhast.isOnStillTimeout(): staysStill() || serverStillTimeout > 0.
// The keystone read: while true the harness ride is NOT steerable (getControllingPassenger) and the flight
// goals are stopped (GhastMoveControl.shouldBeStopped). Cite HappyGhast.isOnStillTimeout.
func happyGhastIsOnStillTimeout(e *Entity) bool {
	return happyGhastStaysStill(e) || e.ghastServerStillTimeout > 0
}

// happyGhastSyncStayStillFlag ports HappyGhast.syncStayStillFlag(): STAYS_STILL := (serverStillTimeout > 0).
// v1 keeps the ghastStaysStill field as the synched-data mirror; the SynchedEntityData.set broadcast is the
// client-visual side (cite-deferred like DATA_IS_CHARGING). Cite HappyGhast.syncStayStillFlag.
func happyGhastSyncStayStillFlag(e *Entity) {
	e.ghastStaysStill = e.ghastServerStillTimeout > 0
}

// happyGhastSetServerStillTimeout ports HappyGhast.setServerStillTimeout(int): assigns serverStillTimeout
// then syncs the STAYS_STILL flag. The 0->positive transition ALSO fires a ClientboundEntityPositionSync
// Packet to trackers (so the freeze is not fought by the quantized delta) -- that packet is the client-
// visual side, cite-deferred; the observable state (the value + the STAYS_STILL flag) is exact. Cite
// HappyGhast.setServerStillTimeout (the `serverStillTimeout <= 0 && value > 0` position-sync guard).
func happyGhastSetServerStillTimeout(e *Entity, value int32) {
	// The `if (serverStillTimeout <= 0 && value > 0) { syncPacketPositionCodec(...); sendToTracking(...) }`
	// position-resync on the 0->positive edge: client-visual (cite-deferred). The value + flag below is exact.
	e.ghastServerStillTimeout = value
	happyGhastSyncStayStillFlag(e)
}

// happyGhastScanPlayerAboveGhast ports HappyGhast.scanPlayerAboveGhast(): true iff a non-spectator player
// whose ROOT VEHICLE is not itself a happy ghast stands within the top slab above the ghast's bounding box:
//   box = getBoundingBox(); slab = AABB(minX-1, maxY - 1e-5, minZ-1, maxX+1, maxY + ysize/2, maxZ+1);
//   for each level player: if isSpectator continue; root = getRootVehicle(); if root instanceof HappyGhast
//   continue; if slab.contains(root.position()) return true. RNG-free. Cite HappyGhast.scanPlayerAboveGhast.
func (t *TickLoop) happyGhastScanPlayerAboveGhast(e *Entity) bool {
	// getBoundingBox(): centered on (x, y..y+height), width e.width.
	hw := e.width / 2.0
	minX, maxX := e.x-hw, e.x+hw
	minZ, maxZ := e.z-hw, e.z+hw
	maxY := e.y + float64(e.height)
	ysize := float64(e.height)
	// slab = AABB(minX-1, maxY - 1e-5, minZ-1, maxX+1, maxY + ysize/2, maxZ+1).
	sMinX, sMinY, sMinZ := minX-1.0, maxY-happyGhastScanTopEpsilon, minZ-1.0
	sMaxX, sMaxY, sMaxZ := maxX+1.0, maxY+ysize/2.0, maxZ+1.0
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if isSpectatorMode(p) { // Player.isSpectator() -> skip
			continue
		}
		// getRootVehicle() instanceof HappyGhast -> skip (a player RIDING a happy ghast does not freeze it).
		if t.happyGhastPlayerRootIsHappyGhast(p) {
			continue
		}
		// AABB.contains(position()) is a HALF-OPEN check: minX <= x < maxX (etc). Vanilla AABB.contains.
		if p.x >= sMinX && p.x < sMaxX && p.y >= sMinY && p.y < sMaxY && p.z >= sMinZ && p.z < sMaxZ {
			return true
		}
	}
	return false
}

// happyGhastPlayerRootIsHappyGhast reports whether the player's root vehicle is a happy ghast (getRootVehicle()
// instanceof HappyGhast). v1 walks the single-level vehicleID chain (a player rides at most one vehicle, and a
// happy ghast is never itself a passenger). Cite Entity.getRootVehicle.
func (t *TickLoop) happyGhastPlayerRootIsHappyGhast(p *tickPlayer) bool {
	if p.vehicleID == 0 {
		return false
	}
	owner := t.cur()
	if owner == nil || owner.entities == nil {
		return false
	}
	v, ok := owner.entities.get(p.vehicleID)
	if !ok || v == nil {
		return false
	}
	return v.typ == entity.HappyGhast.ID
}

// happyGhastStillTimeoutTick ports HappyGhast.tick()'s still-timeout block + aiStep's precise-position set,
// run once per server tick for a live happy ghast (BEFORE happyGhastAiStep, so isOnStillTimeout gates the
// flight this tick). tick(): if serverStillTimeout > 0 { if tickCount > 60 serverStillTimeout--;
// setServerStillTimeout(serverStillTimeout) } ; if scanPlayerAboveGhast() setServerStillTimeout(10).
// aiStep(): setRequiresPrecisePosition(isOnStillTimeout()). RNG-free. Cite HappyGhast.tick + aiStep.
func (t *TickLoop) happyGhastStillTimeoutTick(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	e.ghastTickCount++ // Entity.tick: tickCount++
	// tick(): the serverStillTimeout decrement (with the on-load 60-tick grace) + the re-sync.
	if e.ghastServerStillTimeout > 0 {
		if e.ghastTickCount > happyGhastStillLoadGrace {
			e.ghastServerStillTimeout--
		}
		happyGhastSetServerStillTimeout(e, e.ghastServerStillTimeout)
	}
	// tick(): a player standing on top FORCES the timeout back to MAX_STILL_TIMEOUT (10).
	if t.happyGhastScanPlayerAboveGhast(e) {
		happyGhastSetServerStillTimeout(e, happyGhastMaxStillTimeout)
	}
	// aiStep(): setRequiresPrecisePosition(isOnStillTimeout()). The packet side is cite-deferred; the flag
	// is exact so a future tracker read honors it.
	e.ghastRequiresPrecisePosition = happyGhastIsOnStillTimeout(e)
}

// --- HAPPY GHAST NBT (still_timeout) -------------------------------------------------------------
//
// HappyGhast.addAdditionalSaveData(out): out.putInt("still_timeout", serverStillTimeout).
// HappyGhast.readAdditionalSaveData(in): setServerStillTimeout(in.getIntOr("still_timeout", 0)).
// (Verified javap this task.) The server has no live per-mob entity-NBT round-trip wired yet (the
// saveEntities/loadEntities region path exists but no caller builds a save.Entities from a live *Entity),
// so these are the faithful, unit-testable put/get helpers that the entity-NBT path calls once it lands --
// NOT a value baked away. Cite HappyGhast.addAdditionalSaveData / readAdditionalSaveData.

// happyGhastSaveStillTimeout ports HappyGhast.addAdditionalSaveData: writes serverStillTimeout under the
// "still_timeout" key. Cite HappyGhast.addAdditionalSaveData (putInt).
func happyGhastSaveStillTimeout(e *Entity, out map[string]int32) {
	out["still_timeout"] = e.ghastServerStillTimeout // putInt("still_timeout", serverStillTimeout)
}

// happyGhastLoadStillTimeout ports HappyGhast.readAdditionalSaveData: reads "still_timeout" (default 0) and
// funnels it through setServerStillTimeout so the STAYS_STILL flag re-syncs on load. Cite
// HappyGhast.readAdditionalSaveData (getIntOr("still_timeout", 0) -> setServerStillTimeout).
func happyGhastLoadStillTimeout(e *Entity, in map[string]int32) {
	v, ok := in["still_timeout"] // getIntOr("still_timeout", 0)
	if !ok {
		v = 0
	}
	happyGhastSetServerStillTimeout(e, v)
}
