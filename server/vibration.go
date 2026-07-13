package server

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	wardenVibrationRadius      = 16
	wardenVibrationCooldown    = 40
	wardenRecentProjectileTTL  = 100
	wardenProjectileFirstAnger = 10
	wardenProjectileCloseDist  = 30.0
)

const wardenEyeHeightForPos = 2.9 * 0.85

type vibrationData struct {
	hasCurrent  bool
	travelTicks int
	event       gameEventID
	sourceID    int32
	projOwnerID int32
	srcX        float64
	srcY        float64
	srcZ        float64
	distance    float32

	hasCandidate        bool
	candEvent           gameEventID
	candSourceID        int32
	candProjOwnerID     int32
	candX, candY, candZ float64
	candDistance        float32
	candGameTime        int64
}

// vibrationScheduleCandidate ports VibrationSystem.Listener.scheduleVibration: the vanilla selector
// accepts a candidate when none is pending, OR replaces the pending candidate only when it was
// offered in the same t.gametime and the new event is strictly BETTER (smaller distance, or equal
// distance with strictly higher vibrationFrequencyOf(sculkGameEvent(event))). A pending candidate
// from an earlier tick is sticky -- a later-tick event never overwrites it (vanilla records
// gameTime at offer via VibrationSystem.Data.updateCandidateToGameTime). All three listener paths
// (vibrationHandleGameEvent for the warden, sensorHandleGameEvent, shriekerHandleGameEvent) route
// through this helper instead of unconditional last-event-wins assignment. CITE
// VibrationSystem.Listener.scheduleVibration + VibrationSystem.Data.updateCandidateToGameTime.
func (t *TickLoop) vibrationScheduleCandidate(data *vibrationData, event gameEventID, sourceID, projOwnerID int32, x, y, z float64, dist float32) {
	if !data.hasCandidate {
		data.hasCandidate = true
		data.candEvent = event
		data.candSourceID = sourceID
		data.candProjOwnerID = projOwnerID
		data.candX, data.candY, data.candZ = x, y, z
		data.candDistance = dist
		data.candGameTime = t.gametime
		return
	}
	if data.candGameTime != t.gametime {
		return
	}
	if !vibrationCandidateBetter(dist, event, data.candDistance, data.candEvent) {
		return
	}
	data.candEvent = event
	data.candSourceID = sourceID
	data.candProjOwnerID = projOwnerID
	data.candX, data.candY, data.candZ = x, y, z
	data.candDistance = dist
}

// vibrationCandidateBetter is the VibrationSystem.Listener.scheduleVibration same-tick preference:
// strictly smaller distance wins, or equal distance with strictly higher vibrationFrequencyOf.
func vibrationCandidateBetter(newDist float32, newEvent gameEventID, oldDist float32, oldEvent gameEventID) bool {
	if newDist < oldDist {
		return true
	}
	if newDist > oldDist {
		return false
	}
	return vibrationFrequencyOf(sculkGameEvent(newEvent)) > vibrationFrequencyOf(sculkGameEvent(oldEvent))
}

// vibrationHandleGameEvent ports VibrationSystem.Listener.handleGameEvent: the busy/valid/receivable/
// occlusion gate then scheduleVibration. Only the WARDEN listener path is live. CITE
// VibrationSystem.Listener.handleGameEvent.
func (t *TickLoop) vibrationHandleGameEvent(ln *vibrationListener, event gameEventID, x, y, z float64, ctx gameEventContext) {
	e := ln.owner
	if e == nil || e.warden == nil {
		return
	}
	ws := e.warden
	if ws.vibration == nil {
		ws.vibration = &vibrationData{}
	}
	data := ws.vibration
	if data.hasCurrent {
		return
	}
	if !wardenCanListen(event) {
		return
	}
	lx := e.x
	ly := e.y + wardenEyeHeightForPos
	lz := e.z
	if !t.wardenCanReceiveVibration(e, ctx) {
		return
	}
	if t.vibrationOccluded(x, y, z, lx, ly, lz) {
		return
	}
	dx := x - lx
	dy := y - ly
	dz := z - lz
	dist := float32(math.Sqrt(dx*dx + dy*dy + dz*dz))
	t.vibrationScheduleCandidate(data, event, ctx.sourceEntityID, ctx.projectileOwnerID, x, y, z, dist)
}

// wardenCanListen ports GameEventTags.WARDEN_CAN_LISTEN membership. v1 uses the frequency table
// (frequency != 0) as the proxy. CITE GameEventTags.WARDEN_CAN_LISTEN.
func wardenCanListen(event gameEventID) bool {
	return vibrationFrequencyOf(sculkGameEvent(event)) != 0
}

// wardenCanReceiveVibration ports Warden.VibrationUser.canReceiveVibration: not dead, no VIBRATION_
// COOLDOWN memory, not digging/emerging, within world border (unbounded default), source targetable.
// CITE Warden.VibrationUser.canReceiveVibration.
func (t *TickLoop) wardenCanReceiveVibration(e *Entity, ctx gameEventContext) bool {
	ws := e.warden
	if ws == nil {
		return false
	}
	if e.dead || e.health <= 0 {
		return false
	}
	if ws.vibrationCooldown > 0 {
		return false
	}
	if ws.digging || ws.emergeTicks > 0 {
		return false
	}
	return true
}

// vibrationOccludesSignalsTag is BlockTags.OCCLUDES_VIBRATION_SIGNALS (BlockTags.create
// "occludes_vibration_signals"): the wool set that blocks a vibration line. CITE
// BlockTags.OCCLUDES_VIBRATION_SIGNALS + VibrationSystem.Listener.lambda$isOccluded$0.
const vibrationOccludesSignalsTag = "occludes_vibration_signals"

// vibrationRelativeEpsilon is the 1.0E-5 nudge VibrationSystem.Listener.isOccluded applies to the source
// block center along each Direction before casting the ray (Vec3.relative(dir, 9.999999747378752E-6)).
const vibrationRelativeEpsilon = 9.999999747378752e-6

// vibrationDirections are the 6 Direction.values() unit steps (DOWN, UP, NORTH, SOUTH, WEST, EAST). The
// isOccluded scan nudges the source center by 1E-5 along each and casts a ray to the listener block; the
// enum order is immaterial to the boolean result (a signal is occluded only if ALL six rays are blocked).
var vibrationDirections = [6][3]float64{
	{0, -1, 0}, // DOWN
	{0, 1, 0},  // UP
	{0, 0, -1}, // NORTH
	{0, 0, 1},  // SOUTH
	{-1, 0, 0}, // WEST
	{1, 0, 0},  // EAST
}

// vibrationOccluded ports VibrationSystem.Listener.isOccluded(Level, Vec3 from, Vec3 to): snap both
// endpoints to their block centers (Mth.floor + 0.5), then for each of the six Directions nudge the source
// center 1E-5 toward that face and Level.isBlockInLine a ray to the listener center testing membership in
// OCCLUDES_VIBRATION_SIGNALS. If ANY of the six rays reaches the listener WITHOUT hitting an occluding
// block (the ray is NOT a BLOCK hit) the signal gets through -> return false. Only when ALL six rays are
// blocked is the signal occluded -> return true.
//
//	[VERIFIED javap VibrationSystem$Listener.isOccluded: blockFrom/blockTo = Vec3(floor+0.5); for
//	 Direction.values(): p = blockFrom.relative(dir, 1E-5); if isBlockInLine(ClipBlockStateContext(p,
//	 blockTo, is OCCLUDES_VIBRATION_SIGNALS)).getType() != BLOCK -> return false; end -> return true.]
func (t *TickLoop) vibrationOccluded(sx, sy, sz, lx, ly, lz float64) bool {
	if t.world() == nil {
		return false
	}
	fromX := float64(floorI(sx)) + 0.5
	fromY := float64(floorI(sy)) + 0.5
	fromZ := float64(floorI(sz)) + 0.5
	toX := float64(floorI(lx)) + 0.5
	toY := float64(floorI(ly)) + 0.5
	toZ := float64(floorI(lz)) + 0.5
	for _, d := range vibrationDirections {
		px := fromX + d[0]*vibrationRelativeEpsilon
		py := fromY + d[1]*vibrationRelativeEpsilon
		pz := fromZ + d[2]*vibrationRelativeEpsilon
		if !t.vibrationBlockInLine(px, py, pz, toX, toY, toZ) {
			return false // this ray is clear -> the vibration is NOT occluded
		}
	}
	return true // all six rays blocked -> occluded
}

// vibrationBlockInLine ports Level.isBlockInLine(ClipBlockStateContext) for the vibration-occlusion
// predicate: BlockGetter.traverseBlocks the segment (from->to) and, per traversed cell, test its block
// STATE against OCCLUDES_VIBRATION_SIGNALS. Returns true (a BLOCK hit) at the FIRST cell whose block is in
// the tag; false if the whole segment reaches the listener without one. Uses the SAME DDA as
// clipBlocksCollider (BlockGetter.traverseBlocks) -- the per-cell test is a pure state-tag predicate (the
// isBlockInLine lambda does NO shape clip). CITE BlockGetter.isBlockInLine + lambda$isBlockInLine$0.
func (t *TickLoop) vibrationBlockInLine(fx, fy, fz, tx, ty, tz float64) bool {
	if fx == tx && fy == ty && fz == tz {
		return false // traverseBlocks: from.equals(to) -> miss supplier
	}
	const nudge = -1.0e-7
	sx := mthLerpD(nudge, tx, fx)
	sy := mthLerpD(nudge, ty, fy)
	sz := mthLerpD(nudge, tz, fz)
	ex := mthLerpD(nudge, fx, tx)
	ey := mthLerpD(nudge, fy, ty)
	ez := mthLerpD(nudge, fz, tz)

	cx := floorI(ex)
	cy := floorI(ey)
	cz := floorI(ez)
	if t.vibrationCellOccludes(cx, cy, cz) {
		return true
	}

	dx := sx - ex
	dy := sy - ey
	dz := sz - ez
	stepX := mthSignD(dx)
	stepY := mthSignD(dy)
	stepZ := mthSignD(dz)

	tDeltaX := math.MaxFloat64
	tDeltaY := math.MaxFloat64
	tDeltaZ := math.MaxFloat64
	if stepX != 0 {
		tDeltaX = float64(stepX) / dx
	}
	if stepY != 0 {
		tDeltaY = float64(stepY) / dy
	}
	if stepZ != 0 {
		tDeltaZ = float64(stepZ) / dz
	}
	tMaxX := tDeltaX * boundaryFrac(stepX, ex)
	tMaxY := tDeltaY * boundaryFrac(stepY, ey)
	tMaxZ := tDeltaZ * boundaryFrac(stepZ, ez)

	for tMaxX <= 1.0 || tMaxY <= 1.0 || tMaxZ <= 1.0 {
		if tMaxX < tMaxY {
			if tMaxX < tMaxZ {
				cx += stepX
				tMaxX += tDeltaX
			} else {
				cz += stepZ
				tMaxZ += tDeltaZ
			}
		} else if tMaxY < tMaxZ {
			cy += stepY
			tMaxY += tDeltaY
		} else {
			cz += stepZ
			tMaxZ += tDeltaZ
		}
		if t.vibrationCellOccludes(cx, cy, cz) {
			return true
		}
	}
	return false
}

// vibrationCellOccludes is the isBlockInLine per-cell predicate: the block at (bx,by,bz) is in
// OCCLUDES_VIBRATION_SIGNALS (wool). An unloaded cell has no block -> not occluding. CITE
// lambda$isBlockInLine$0 (isTargetBlock().test(state)).
func (t *TickLoop) vibrationCellOccludes(bx, by, bz int) bool {
	sid, ok := t.world().GetBlock(pk.Position{X: bx, Y: by, Z: bz}, dimMinY)
	if !ok {
		return false
	}
	return blockInTag(sid, vibrationOccludesSignalsTag)
}

// tickWardenVibration ports VibrationSystem.Ticker.tick for a warden: decrement the VIBRATION_COOLDOWN +
// RECENT_PROJECTILE memory expiries, then run the Data state machine -- select a pending candidate
// (travelTicks = Mth.floor(distance)) or decrement the in-flight travel time and, at <= 0, DELIVER via
// onReceiveVibration. Called once per warden tick from wardenAiStep. TRAVEL DELAY == floor(distance).
// CITE VibrationSystem.Ticker.tick.
func (t *TickLoop) tickWardenVibration(e *Entity) {
	ws := e.warden
	if ws == nil {
		return
	}
	if ws.vibrationCooldown > 0 {
		ws.vibrationCooldown--
	}
	if ws.recentProjectileTicks > 0 {
		ws.recentProjectileTicks--
	}
	data := ws.vibration
	if data == nil {
		return
	}
	if !data.hasCurrent {
		if data.hasCandidate {
			t.vibrationPromoteCandidate(data)
		} else {
			return
		}
	}
	data.travelTicks--
	if data.travelTicks > 0 {
		return
	}
	t.wardenOnReceiveVibration(e, data)
	data.hasCurrent = false
}

// wardenOnReceiveVibration ports Warden.VibrationUser.onReceiveVibration: arm VIBRATION_COOLDOWN 40, then
// the anger increments -- +35 for a direct entity source, +10 for the FIRST projectile / +35 for a
// subsequent one (RECENT_PROJECTILE memory), arming RECENT_PROJECTILE 100 on every projectile vibration
// (outside the closerThan gate, per the javap). CITE Warden.VibrationUser.onReceiveVibration.
func (t *TickLoop) wardenOnReceiveVibration(e *Entity, data *vibrationData) {
	ws := e.warden
	if ws == nil {
		return
	}
	if e.dead || e.health <= 0 {
		return
	}
	ws.vibrationCooldown = wardenVibrationCooldown
	sourceID := data.sourceID
	projOwnerID := data.projOwnerID
	if projOwnerID != 0 {
		if t.wardenCloserThanID(e, projOwnerID, wardenProjectileCloseDist) {
			if ws.recentProjectileTicks > 0 {
				t.wardenIncreaseAngerAt(e, projOwnerID, wardenDefaultAnger)
			} else {
				t.wardenIncreaseAngerAt(e, projOwnerID, wardenProjectileFirstAnger)
			}
		}
		ws.recentProjectileTicks = wardenRecentProjectileTTL
	} else if sourceID != 0 {
		t.wardenIncreaseAngerAt(e, sourceID, wardenDefaultAnger)
	}
}

// wardenCloserThanID ports Warden.closerThan(entity, distance) for the projectile-owner gate
// (closerThan(arg5, 30.0)). Resolves the id to a player. CITE Entity.closerThan(Entity, double).
func (t *TickLoop) wardenCloserThanID(e *Entity, id int32, dist float64) bool {
	p := t.playerByEntityID(id)
	if p == nil {
		return false
	}
	dx := p.x - e.x
	dy := p.y - e.y
	dz := p.z - e.z
	return dx*dx+dy*dy+dz*dz < dist*dist
}

// wardenVibrationListenerPos is the EntityPositionSource sample (feet + eye height), exposed for tests.
func wardenVibrationListenerPos(e *Entity) (float64, float64, float64) {
	return e.x, e.y + wardenEyeHeightForPos, e.z
}

// blockPosContaining ports BlockPos.containing(Vec3): floor each coordinate. CITE BlockPos.containing.
func blockPosContaining(x, y, z float64) pk.Position {
	return pk.Position{X: mthFloor(x), Y: mthFloor(y), Z: mthFloor(z)}
}
