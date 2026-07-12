package server

// sculk_sensor_be.go - the SCULK SENSOR + CALIBRATED SCULK SENSOR block-entity + block behavior: a
// 1:1 port of net.minecraft.world.level.block.SculkSensorBlock.{stepOn,activate,deactivate,tick,
// canActivate,getPhase,getActiveTicks,getSignal-via-getAnalogOutputSignal} + the SculkSensorBlockEntity
// .VibrationUser.{canReceiveVibration,onReceiveVibration,getListenerRadius} + VibrationSystem.
// getGameEventFrequency + getRedstoneStrengthForDistance, plus CalibratedSculkSensorBlock overrides
// (getActiveTicks=10, listener radius 16, the FACING back-signal frequency filter). Over the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR + javap -c).
//
// PHASE MACHINE (SculkSensorPhase INACTIVE -> ACTIVE -> COOLDOWN -> INACTIVE):
//   - canActivate(state): getPhase == INACTIVE.
//   - activate(entity, level, pos, state, power, frequency): setBlock(PHASE=ACTIVE, POWER=power, 3);
//       scheduleTick(pos, block, getActiveTicks()); updateNeighbours; tryResonateVibration; gameEvent
//       (SCULK_SENSOR_TENDRILS_CLICKING); (sound deferred). getActiveTicks: 30 (sensor) / 10 (calibrated).
//   - tick(state, level, pos): if PHASE==ACTIVE -> deactivate(level, pos, state);
//       else if PHASE==COOLDOWN -> setBlock(PHASE=INACTIVE, 3); (stop-clicking sound deferred).
//   - deactivate(level, pos, state): setBlock(PHASE=COOLDOWN, POWER=0, 3); scheduleTick(pos, block, 10);
//       updateNeighbours.
//
// REDSTONE OUTPUT:
//   - getSignal (weak, all faces) = POWER; getDirectSignal = POWER only out UP.
//   - hasAnalogOutputSignal = true; getAnalogOutputSignal = (PHASE==ACTIVE) ? lastVibrationFrequency : 0
//       (the comparator reads the FREQUENCY, not the power).
//
// VIBRATION (the observable STEP source): a mob/player standing on a sculk sensor emits GameEvent.STEP;
// the VibrationUser receives it (canReceiveVibration: not a self BLOCK_PLACE/DESTROY, frequency!=0,
// canActivate), sets lastVibrationFrequency = getGameEventFrequency(STEP)=1, and calls
// SculkSensorBlock.activate(power=getRedstoneStrengthForDistance(dist,radius), frequency=1). The full
// delay-based VibrationSystem.Ticker travel-time (Data/Ticker) is DEFERRED (cited): the STEP-on-block
// direct delivery is the load-bearing observable (a sculk sensor a player walks on lights up + outputs
// redstone), matching vanilla's stepOn forceScheduleVibration for a distance-0 same-block vibration.
// CITE: SculkSensorBlock.stepOn (forceScheduleVibration on STEP) + VibrationSystem.Listener.
//
// RNG: the sensor draws NOTHING deterministically observable (the activate/deactivate sounds that draw
// nextFloat are deferred), so it never perturbs any RNG stream.

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Sensor constants (SculkSensorBlock / CalibratedSculkSensorBlock, VERIFIED javap).
const (
	sculkSensorActiveTicks         = 30 // SculkSensorBlock.getActiveTicks (bipush 30)
	calibratedSensorActiveTicks    = 10 // CalibratedSculkSensorBlock.getActiveTicks (bipush 10)
	sculkSensorCooldownTicks       = 10 // deactivate -> scheduleTick(pos, block, 10)
	sculkSensorListenerRadius      = 8  // SculkSensorBlockEntity.VibrationUser.getListenerRadius (bipush 8)
	calibratedSensorListenerRadius = 16 // CalibratedSculkSensorBlockEntity.VibrationUser.getListenerRadius (bipush 16)
)

const sculkSensorTickType blockTickType = "minecraft:sculk_sensor"
const calibratedSculkSensorTickType blockTickType = "minecraft:calibrated_sculk_sensor"

// sculkSensorBE is the tick-owned state of one SculkSensorBlockEntity: the last vibration frequency
// (the comparator analog output while ACTIVE). The VibrationSystem.Data (the in-flight delayed
// vibration) is DEFERRED (the STEP source is delivered directly), so only lastVibrationFrequency
// persists. CITE: SculkSensorBlockEntity.lastVibrationFrequency.
type sculkSensorBE struct {
	lastVibrationFrequency int
	// vibration is the in-flight VibrationSystem.Data (the receiving -> delay -> signal state machine)
	// for the sensor's VibrationSystem.Listener. nil until the first candidate is scheduled. The delayed
	// path (a game event elsewhere in range travels floor(distance) ticks then activates the sensor) is
	// LIVE via the block-position listener bus (vibration_block.go); the direct same-block STEP path
	// (tickSculkSensors) remains for a distance-0 STEP. CITE: SculkSensorBlockEntity.getVibrationData /
	// VibrationSystem$Data.
	vibration *vibrationData
}

// isSculkSensorBlock is the block-identity gate (either sensor variant) used by the tick loop +
// createBlockEntityOnPlace.
func isSculkSensorBlock(state block.StateID) bool {
	return block.IsAnySculkSensor(state)
}

// resolveSculkSensor returns the tick-owned sculkSensorBE for pos, creating an EMPTY one on first
// access. Returns nil when pos is not a sensor (or unloaded). Tick-owned.
func (t *TickLoop) resolveSculkSensor(pos pk.Position) *sculkSensorBE {
	if t.sculkSensors == nil {
		t.sculkSensors = make(map[pk.Position]*sculkSensorBE)
	}
	if s, ok := t.sculkSensors[pos]; ok {
		return s
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsAnySculkSensor(state) {
		return nil
	}
	s := &sculkSensorBE{}
	t.sculkSensors[pos] = s
	return s
}

// tickSculkSensors is the per-tick STEP-vibration scan: for every player/entity, if it is standing in
// a sculk sensor's cell and the sensor canActivate, deliver a STEP vibration (the direct-delivery
// model, see file header). Mirrors the pressure-plate per-tick entity scan (tickPressurePlates). The
// WARDEN self-exclusion (stepOn ignores a Warden) is a no-op in v1 (no Warden entity). Tick-owned.
func (t *TickLoop) tickSculkSensors() {
	if t.world() == nil {
		return
	}
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		t.sculkSensorStepOnAt(blockPosOf(p.x, p.y, p.z))
	}
	if t.cur() != nil {
		for _, e := range t.cur().entities.all() {
			if e == nil || e.dead {
				continue
			}
			t.sculkSensorStepOnAt(blockPosOf(e.x, e.y, e.z))
		}
	}
}

// sculkSensorStepOnAt ports SculkSensorBlock.stepOn for the entity standing at pos: if the block is a
// sculk sensor that canActivate (and, for the VibrationUser gate, the STEP frequency is non-zero),
// deliver the STEP vibration. The forceScheduleVibration -> onReceiveVibration -> activate chain is
// collapsed into a direct activate (a STEP on the sensor's own block is a distance-0 vibration; vanilla
// forceScheduleVibration delivers it next tick, the same observable). CITE: SculkSensorBlock.stepOn +
// VibrationUser.onReceiveVibration.
func (t *TickLoop) sculkSensorStepOnAt(pos pk.Position) {
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsAnySculkSensor(state) {
		return
	}
	// canActivate(state): getPhase == INACTIVE.
	if !sculkSensorCanActivate(state) {
		return
	}
	// VibrationUser.canReceiveVibration for STEP: STEP is not BLOCK_PLACE/DESTROY, and
	// getGameEventFrequency(STEP)=1 != 0, and canActivate (checked above) -> receivable.
	freq := vibrationFrequencyOf(gameEventStep)
	if freq == 0 {
		return
	}
	// CALIBRATED back-signal frequency filter: if the calibrated sensor's back input signal is non-zero
	// and != this event's frequency, reject. CITE: CalibratedSculkSensorBlockEntity.VibrationUser
	// .canReceiveVibration -> getBackSignal.
	if block.IsCalibratedSculkSensor(state) {
		if back := t.calibratedSensorBackSignal(pos, state); back != 0 && back != freq {
			return
		}
	}
	be := t.resolveSculkSensor(pos)
	if be == nil {
		return
	}
	// onReceiveVibration: setLastVibrationFrequency(freq); power = getRedstoneStrengthForDistance(
	// distance=0, radius); activate(entity, level, pos, state, power, freq).
	be.lastVibrationFrequency = freq
	radius := sculkSensorListenerRadius
	if block.IsCalibratedSculkSensor(state) {
		radius = calibratedSensorListenerRadius
	}
	power := vibrationRedstoneStrengthForDistance(0.0, radius)
	t.sculkSensorActivate(pos, state, power, freq)
}

// sculkSensorCanActivate ports SculkSensorBlock.canActivate: getPhase == INACTIVE. CITE.
func sculkSensorCanActivate(state block.StateID) bool {
	phase, ok := block.SculkSensorPhaseOf(state)
	return ok && phase == block.SculkSensorPhaseInactive
}

// sculkSensorActiveTicksFor ports getActiveTicks: 30 for a plain sensor, 10 for calibrated. CITE.
func sculkSensorActiveTicksFor(state block.StateID) int {
	if block.IsCalibratedSculkSensor(state) {
		return calibratedSensorActiveTicks
	}
	return sculkSensorActiveTicks
}

// sculkSensorTickTypeFor is the scheduled-tick block id for the sensor variant (each schedules under
// its own block id, so the tickBlock stale guard routes correctly).
func sculkSensorTickTypeFor(state block.StateID) blockTickType {
	if block.IsCalibratedSculkSensor(state) {
		return calibratedSculkSensorTickType
	}
	return sculkSensorTickType
}

// sculkSensorActivate ports SculkSensorBlock.activate: setBlock(PHASE=ACTIVE, POWER=power, flag 3);
// scheduleTick(pos, block, getActiveTicks); updateNeighbours(pos + pos.below). tryResonateVibration +
// the SCULK_SENSOR_TENDRILS_CLICKING gameEvent + the click sound are deferred (no resonance network /
// game-event bus / BE sound seam in v1). CITE: SculkSensorBlock.activate.
func (t *TickLoop) sculkSensorActivate(pos pk.Position, state block.StateID, power, frequency int) {
	if t.world() == nil {
		return
	}
	newState, ok := block.SculkSensorWithPhaseAndPower(state, block.SculkSensorPhaseActive, power)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, newState, dimMinY) {
		t.broadcastBlockUpdate(pos, newState)
	}
	t.scheduleBlockTick(pos, sculkSensorTickTypeFor(newState), sculkSensorActiveTicksFor(newState))
	t.sculkSensorUpdateNeighbours(pos)
	// tryResonateVibration(entity, level, pos, frequency): the calibrated-resonance network is DEFERRED
	// (no adjacent-sensor resonance seam). gameEvent(SCULK_SENSOR_TENDRILS_CLICKING): no game-event bus
	// (cited). Both are cosmetic/secondary to the redstone output + phase machine.
	_ = frequency
}

// sculkSensorDeactivate ports SculkSensorBlock.deactivate: setBlock(PHASE=COOLDOWN, POWER=0, flag 3);
// scheduleTick(pos, block, 10); updateNeighbours. CITE: SculkSensorBlock.deactivate.
func (t *TickLoop) sculkSensorDeactivate(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	newState, ok := block.SculkSensorWithPhaseAndPower(state, block.SculkSensorPhaseCooldown, 0)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, newState, dimMinY) {
		t.broadcastBlockUpdate(pos, newState)
	}
	t.scheduleBlockTick(pos, sculkSensorTickTypeFor(newState), sculkSensorCooldownTicks)
	t.sculkSensorUpdateNeighbours(pos)
}

// sculkSensorTick ports SculkSensorBlock.tick: PHASE==ACTIVE -> deactivate; else PHASE==COOLDOWN ->
// setBlock(PHASE=INACTIVE, flag 3) (the stop-clicking sound is deferred). Dispatched from the
// scheduled-block-tick drain. CITE: SculkSensorBlock.tick.
func (t *TickLoop) sculkSensorTick(state block.StateID, pos pk.Position) {
	phase, ok := block.SculkSensorPhaseOf(state)
	if !ok {
		return
	}
	if phase == block.SculkSensorPhaseActive {
		t.sculkSensorDeactivate(pos, state)
		return
	}
	if phase == block.SculkSensorPhaseCooldown {
		newState, ok := block.SculkSensorWithPhaseAndPower(state, block.SculkSensorPhaseInactive, block.SculkSensorPower(state))
		if !ok {
			return
		}
		if t.world().SetBlock(pos, newState, dimMinY) {
			t.broadcastBlockUpdate(pos, newState)
		}
	}
}

// sculkSensorUpdateNeighbours ports SculkSensorBlock.updateNeighbours: updateNeighborsAt(pos) +
// updateNeighborsAt(pos.below). Wired through the redstone propagation entry point so the sensor's
// power change wakes the wire graph. CITE: SculkSensorBlock.updateNeighbours.
func (t *TickLoop) sculkSensorUpdateNeighbours(pos pk.Position) {
	t.onRedstoneEdit(pos)
	t.onRedstoneEdit(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
}

// calibratedSensorBackSignal ports CalibratedSculkSensorBlockEntity.VibrationUser.getBackSignal:
// level.getSignal(pos.relative(FACING.opposite), FACING.opposite). The block behind the calibrated
// sensor (opposite its FACING) supplies the frequency filter. CITE: CalibratedSculkSensorBlock
// .getBackSignal.
func (t *TickLoop) calibratedSensorBackSignal(pos pk.Position, state block.StateID) int {
	facing, ok := block.CalibratedSculkSensorFacing(state)
	if !ok {
		return 0
	}
	opp := sculkOppositeDir(facing)
	off := sculkDirOffset(opp)
	behind := pk.Position{X: pos.X + off.dx, Y: pos.Y + off.dy, Z: pos.Z + off.dz}
	return t.getWeakSignal(behind, opp)
}
