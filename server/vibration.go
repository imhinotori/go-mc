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
	data.hasCandidate = true
	data.candEvent = event
	data.candSourceID = ctx.sourceEntityID
	data.candProjOwnerID = ctx.projectileOwnerID
	data.candX, data.candY, data.candZ = x, y, z
	data.candDistance = dist
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

// vibrationOccluded ports VibrationSystem.isOccluded. v1 stub: never occluded (block-property clip is a
// follow-up; SNEAK sensitivity is at the EMITTER). CITE VibrationSystem.isOccluded.
func (t *TickLoop) vibrationOccluded(sx, sy, sz, lx, ly, lz float64) bool {
	return false
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
			data.hasCurrent = true
			data.event = data.candEvent
			data.sourceID = data.candSourceID
			data.projOwnerID = data.candProjOwnerID
			data.srcX, data.srcY, data.srcZ = data.candX, data.candY, data.candZ
			data.distance = data.candDistance
			data.travelTicks = mthFloor(float64(data.candDistance))
			data.hasCandidate = false
		}
		return
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
