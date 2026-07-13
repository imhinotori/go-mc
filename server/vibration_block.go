package server

// vibration_block.go -- the BLOCK-POSITION game-event LISTENER path: a 1:1 port of the
// net.minecraft.world.level.gameevent.vibrations.VibrationSystem.Listener state machine for the two
// block listeners (SculkSensorBlockEntity + SculkShriekerBlockEntity), driven off the block-keyed
// registries (t.sculkSensors / t.sculkShriekers, the per-block GameEventListenerRegistry analogue).
// Over the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p).
//
// VANILLA (verified javap this task):
//   GameEventDispatcher.post: iterate sections in [pos-r, pos+r] (r = notificationRadius, DEFAULT 16);
//     per section GameEventListenerRegistry, gate on distSqr(listenerBlockPos, eventBlockPos) <=
//     getListenerRadius squared, then BY_DISTANCE-deliver handleGameEvent(level, event, ctx, sourceVec3).
//   VibrationSystem.Listener.handleGameEvent: currentVibration non-null -> false; not isValidVibration ->
//     false; not canReceiveVibration -> false; isOccluded(source, listener) -> false; else
//     scheduleVibration (addCandidate).
//   VibrationSystem.Ticker.tick: no current -> select candidate (travelTimeInTicks =
//     calculateTravelTimeInTicks(distance) = Mth.floor(distance)); current and travel>0 -> decrement; at
//     travel<=0 -> receiveVibration -> onReceiveVibration(level, sourceBlockPos, event, entity,
//     projectileOwner, distanceBetweenInBlocks(sourceBlockPos, listenerBlockPos)); setCurrentVibration(null).
//
// v1 MODEL: the block-keyed registries ARE the per-block registry (sensor/shrieker BEs keyed by pos).
// gameEvent(...) (game_event.go) now walks them alongside the entity listeners. Each BE holds its
// in-flight VibrationSystem.Data (be.vibration, the SAME candidate/current struct the warden uses).
// tickBlockVibrationListeners runs Ticker.tick per BE once per tick.
//
// TICK DISCIPLINE: the tick goroutine owns t.sculkSensors / t.sculkShriekers and every gameEvent(...)
// emit (all posts come from tick-owned paths: block place/break, door toggle, item use, growth
// random-tick, enderman carry, etc.), and the Ticker runs in the block-entity tick phase. No region
// fan-out posts a game event, so no cross-goroutine boundary is crossed; a future async emitter MUST
// marshal its post back onto the tick goroutine before touching this registry.
//
// RNG: the block-listener path draws NOTHING -- handleGameEvent, the Ticker, and onReceiveVibration are
// pure (calculateTravelTimeInTicks = Mth.floor; no nextInt/nextFloat). Verified javap. So a sensor/
// shrieker vibration never perturbs any RNG stream (the pig oracle is unaffected).

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// GameEventTags used by the two block listeners for getListenableEvents / isValidVibration. CITE
// GameEventTags.VIBRATIONS (sensor default, 56 events) / SHRIEKER_CAN_LISTEN (shrieker override, only
// sculk_sensor_tendrils_clicking); BlockTags.DAMPENS_VIBRATIONS (affected-state reject).
const (
	gameEventTagVibrations        = "vibrations"
	gameEventTagShriekerCanListen = "shrieker_can_listen"
	blockTagDampensVibrations     = "dampens_vibrations"
)

// sculkShriekerListenerRadius is SculkShriekerBlockEntity.VibrationUser.getListenerRadius (bipush 8).
const sculkShriekerListenerRadius = 8

// walkBlockVibrationListeners is the GameEventDispatcher.post body for the block-keyed registries: for
// every sculk sensor / shrieker whose block position is within its listener radius of the event, run
// its VibrationSystem.Listener.handleGameEvent. Called from gameEvent(...) after the entity walk.
// Tick-owned. Distance gate = EuclideanGameEventListenerRegistry.getPostableListenerPosition:
// distSqr(listenerBlockPos, eventBlockPos) <= listenerRadius squared (section box is the broad phase).
func (t *TickLoop) walkBlockVibrationListeners(event gameEventID, x, y, z float64, ctx gameEventContext) {
	ebx := floorI(x)
	eby := floorI(y)
	ebz := floorI(z)
	for pos, be := range t.sculkSensors {
		r := sculkSensorListenerRadiusAt(t, pos)
		if blockDistSqr(pos.X, pos.Y, pos.Z, ebx, eby, ebz) > float64(r*r) {
			continue
		}
		t.sensorHandleGameEvent(pos, be, event, x, y, z, ctx)
	}
	for pos, be := range t.sculkShriekers {
		if blockDistSqr(pos.X, pos.Y, pos.Z, ebx, eby, ebz) > float64(sculkShriekerListenerRadius*sculkShriekerListenerRadius) {
			continue
		}
		t.shriekerHandleGameEvent(pos, be, event, x, y, z, ctx)
	}
}

// sculkSensorListenerRadiusAt resolves the sensor variant getListenerRadius (8 plain / 16 calibrated)
// from the world state at pos. A stale/unloaded pos falls back to the plain radius. CITE
// SculkSensorBlockEntity.VibrationUser.getListenerRadius / CalibratedSculkSensorBlockEntity.
func sculkSensorListenerRadiusAt(t *TickLoop, pos pk.Position) int {
	if t.world() == nil {
		return sculkSensorListenerRadius
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if ok && block.IsCalibratedSculkSensor(state) {
		return calibratedSensorListenerRadius
	}
	return sculkSensorListenerRadius
}

// blockDistSqr ports BlockPos.distSqr(Vec3i): the integer-center squared distance (as doubles).
func blockDistSqr(ax, ay, az, bx, by, bz int) float64 {
	dx := float64(ax - bx)
	dy := float64(ay - by)
	dz := float64(az - bz)
	return dx*dx + dy*dy + dz*dz
}

// distanceBetweenInBlocks ports VibrationSystem.Listener.distanceBetweenInBlocks(BlockPos, BlockPos):
// (float) Math.sqrt(a.distSqr(b)). CITE VibrationSystem.Listener.distanceBetweenInBlocks.
func distanceBetweenInBlocks(ax, ay, az, bx, by, bz int) float32 {
	return float32(math.Sqrt(blockDistSqr(ax, ay, az, bx, by, bz)))
}

// vibrationIsValidVibration ports VibrationSystem.User.isValidVibration (interface default): the event
// must be in getListenableEvents(); a source entity must not be spectator / carefully-stepping an
// IGNORE_VIBRATIONS_SNEAKING event / dampening; the affected block state must not be DAMPENS_VIBRATIONS.
// In v1 the source-entity spectator/careful/dampen flags are cited-default false (no such flag is
// threaded into gameEventContext yet), so this reduces to the listenable-tag check + the affected-state
// DAMPENS_VIBRATIONS reject. CITE VibrationSystem.User.isValidVibration.
func vibrationIsValidVibration(event gameEventID, listenableTag string, ctx gameEventContext) bool {
	if !gameEventInTag(event, listenableTag) {
		return false
	}
	if ctx.affectedState != 0 {
		if blockInTag(block.StateID(ctx.affectedState), blockTagDampensVibrations) {
			return false
		}
	}
	return true
}

// sensorHandleGameEvent ports VibrationSystem.Listener.handleGameEvent for a sculk sensor: busy / valid
// / receivable / occlusion gate, then scheduleVibration (candidate distance = sourceVec3 distanceTo
// listenerVec3). CITE VibrationSystem.Listener.handleGameEvent.
func (t *TickLoop) sensorHandleGameEvent(pos pk.Position, be *sculkSensorBE, event gameEventID, x, y, z float64, ctx gameEventContext) {
	if be.vibration == nil {
		be.vibration = &vibrationData{}
	}
	data := be.vibration
	if data.hasCurrent {
		return
	}
	if !vibrationIsValidVibration(event, gameEventTagVibrations, ctx) {
		return
	}
	lx := float64(pos.X) + 0.5
	ly := float64(pos.Y) + 0.5
	lz := float64(pos.Z) + 0.5
	if !t.sensorCanReceiveVibration(pos, event, x, y, z) {
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

// sensorCanReceiveVibration ports SculkSensorBlockEntity.VibrationUser.canReceiveVibration: if the source
// block position equals this sensor block and the event is BLOCK_DESTROY or BLOCK_PLACE -> false; else
// getGameEventFrequency(event) != 0 and SculkSensorBlock.canActivate(state). CITE.
func (t *TickLoop) sensorCanReceiveVibration(pos pk.Position, event gameEventID, x, y, z float64) bool {
	if floorI(x) == pos.X && floorI(y) == pos.Y && floorI(z) == pos.Z {
		if event == geBlockDestroy || event == geBlockPlace {
			return false
		}
	}
	if vibrationFrequencyOf(sculkGameEvent(event)) == 0 {
		return false
	}
	if t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsAnySculkSensor(state) {
		return false
	}
	return sculkSensorCanActivate(state)
}

// shriekerHandleGameEvent ports VibrationSystem.Listener.handleGameEvent for a sculk shrieker. Its
// getListenableEvents == GameEventTags.SHRIEKER_CAN_LISTEN (only sculk_sensor_tendrils_clicking). CITE
// VibrationSystem.Listener.handleGameEvent + SculkShriekerBlockEntity.VibrationUser.
func (t *TickLoop) shriekerHandleGameEvent(pos pk.Position, be *sculkShriekerBE, event gameEventID, x, y, z float64, ctx gameEventContext) {
	if be.vibration == nil {
		be.vibration = &vibrationData{}
	}
	data := be.vibration
	if data.hasCurrent {
		return
	}
	if !vibrationIsValidVibration(event, gameEventTagShriekerCanListen, ctx) {
		return
	}
	lx := float64(pos.X) + 0.5
	ly := float64(pos.Y) + 0.5
	lz := float64(pos.Z) + 0.5
	if !t.shriekerCanReceiveVibration(pos, ctx) {
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

// shriekerCanReceiveVibration ports SculkShriekerBlockEntity.VibrationUser.canReceiveVibration:
// not SHRIEKING and tryGetPlayer(sourceEntity) != null (only a PLAYER-sourced vibration triggers). CITE.
func (t *TickLoop) shriekerCanReceiveVibration(pos pk.Position, ctx gameEventContext) bool {
	if t.world() == nil {
		return false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsSculkShrieker(state) {
		return false
	}
	if block.SculkShriekerShrieking(state) {
		return false
	}
	return t.vibrationSourcePlayer(ctx) != nil
}

// vibrationSourcePlayer ports SculkShriekerBlockEntity.tryGetPlayer(Entity): a ServerPlayer source, or a
// projectile ServerPlayer owner. Resolves ctx (projectileOwnerID preferred, else sourceEntityID) to a
// live tickPlayer. CITE SculkShriekerBlockEntity.tryGetPlayer.
func (t *TickLoop) vibrationSourcePlayer(ctx gameEventContext) *tickPlayer {
	if ctx.projectileOwnerID != 0 {
		if p := t.playerByEntityID(ctx.projectileOwnerID); p != nil {
			return p
		}
	}
	if ctx.sourceEntityID != 0 {
		if p := t.playerByEntityID(ctx.sourceEntityID); p != nil {
			return p
		}
	}
	return nil
}

// tickBlockVibrationListeners runs VibrationSystem.Ticker.tick over every sculk sensor / shrieker with
// in-flight vibration data: select a pending candidate (travelTicks = floor(distance)), decrement the
// current travel time, and on <= 0 deliver via the BE onReceiveVibration. Called once per tick from the
// block-entity tick phase (after the entity pass). Tick-owned. CITE VibrationSystem.Ticker.tick.
func (t *TickLoop) tickBlockVibrationListeners() {
	if t.world() == nil {
		return
	}
	for pos, be := range t.sculkSensors {
		if be.vibration == nil {
			continue
		}
		t.tickSensorVibration(pos, be)
	}
	for pos, be := range t.sculkShriekers {
		if be.vibration == nil {
			continue
		}
		t.tickShriekerVibration(pos, be)
	}
}

// tickSensorVibration ports VibrationSystem.Ticker.tick for a sculk sensor. CITE.
func (t *TickLoop) tickSensorVibration(pos pk.Position, be *sculkSensorBE) {
	data := be.vibration
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
	t.sensorOnReceiveVibration(pos, be, data)
	data.hasCurrent = false
}

// tickShriekerVibration ports VibrationSystem.Ticker.tick for a sculk shrieker. CITE.
func (t *TickLoop) tickShriekerVibration(pos pk.Position, be *sculkShriekerBE) {
	data := be.vibration
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
	t.shriekerOnReceiveVibration(pos, be, data)
	data.hasCurrent = false
}

// vibrationPromoteCandidate is trySelectAndScheduleVibration: move the pending candidate into the current
// vibration and set travelTicks = Mth.floor(distance) (the calculateTravelTimeInTicks default). Same
// promotion the warden Ticker performs. CITE VibrationSystem.Ticker.trySelectAndScheduleVibration.
func (t *TickLoop) vibrationPromoteCandidate(data *vibrationData) {
	data.hasCurrent = true
	data.event = data.candEvent
	data.sourceID = data.candSourceID
	data.projOwnerID = data.candProjOwnerID
	data.srcX, data.srcY, data.srcZ = data.candX, data.candY, data.candZ
	data.distance = data.candDistance
	data.travelTicks = mthFloor(float64(data.candDistance))
	data.hasCandidate = false
}

// sensorOnReceiveVibration ports SculkSensorBlockEntity.VibrationUser.onReceiveVibration: if
// SculkSensorBlock.canActivate(state), lastVibrationFrequency = getGameEventFrequency(event), power =
// getRedstoneStrengthForDistance(distanceBetweenInBlocks(sourceBlockPos, listenerBlockPos), radius),
// SculkSensorBlock.activate(pos, state, power, frequency). The distance is the BLOCK-to-block distance
// recomputed at delivery (Ticker.receiveVibration), NOT the stored Vec3 distance. CITE.
func (t *TickLoop) sensorOnReceiveVibration(pos pk.Position, be *sculkSensorBE, data *vibrationData) {
	if t.world() == nil {
		return
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsAnySculkSensor(state) {
		return
	}
	if !sculkSensorCanActivate(state) {
		return
	}
	freq := vibrationFrequencyOf(sculkGameEvent(data.event))
	be.lastVibrationFrequency = freq
	sbx := floorI(data.srcX)
	sby := floorI(data.srcY)
	sbz := floorI(data.srcZ)
	dist := distanceBetweenInBlocks(sbx, sby, sbz, pos.X, pos.Y, pos.Z)
	radius := sculkSensorListenerRadius
	if block.IsCalibratedSculkSensor(state) {
		radius = calibratedSensorListenerRadius
	}
	power := vibrationRedstoneStrengthForDistance(dist, radius)
	t.sculkSensorActivate(pos, state, power, freq)
}

// shriekerOnReceiveVibration ports SculkShriekerBlockEntity.VibrationUser.onReceiveVibration:
// tryShriek(level, tryGetPlayer(projectileOwner else sourceEntity)). The delayed listen path drives the
// same tryShriek the direct stepOn path uses, advancing the warning-level machine. CITE.
func (t *TickLoop) shriekerOnReceiveVibration(pos pk.Position, be *sculkShriekerBE, data *vibrationData) {
	if t.world() == nil {
		return
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsSculkShrieker(state) {
		return
	}
	p := t.vibrationSourcePlayer(gameEventContext{sourceEntityID: data.sourceID, projectileOwnerID: data.projOwnerID})
	if p == nil {
		return
	}
	t.sculkShriekerTryShriek(pos, state, be, p)
}
