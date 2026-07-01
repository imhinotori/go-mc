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
func (t *TickLoop) happyGhastAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	// RandomFloatAroundGoal: re-roll the fly-to target when there is none, or the current one is reached
	// (<1-sq) or drifted too far (>3600-sq). This is the goal's canUse; when true it runs start().
	if t.ghastRandomFloatAroundCanUse(e) {
		t.ghastRandomFloatAroundStart(e)
	}
	// GhastMoveControl.tick: accelerate deltaMovement toward the wanted point on the floatDuration cadence.
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
