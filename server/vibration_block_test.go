package server

// vibration_block_test.go -- behaviour pins for the BLOCK-POSITION game-event listener bus
// (vibration_block.go, 1:1 javap VibrationSystem.Listener + Ticker + the sculk sensor/shrieker
// VibrationUsers). Verifies: (a) a STEP emitted N blocks from a sculk sensor schedules a candidate,
// travels floor(distance) ticks, then activates the sensor with POWER =
// getRedstoneStrengthForDistance(blockDistance, radius); (b) an emit beyond the listener radius
// schedules nothing; (c) a shrieker does NOT listen to STEP over the bus (SHRIEKER_CAN_LISTEN excludes
// it) but a direct step advances its warning level; (d) the game_event tag membership query.

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestGameEventTagMembership pins the GameEventInTag query used by isValidVibration: STEP is in
// VIBRATIONS (sensor default) but NOT in SHRIEKER_CAN_LISTEN; sculk_sensor_tendrils_clicking IS in
// SHRIEKER_CAN_LISTEN. CITE GameEventTags.VIBRATIONS / SHRIEKER_CAN_LISTEN.
func TestGameEventTagMembership(t *testing.T) {
	if !gameEventInTag(geStep, gameEventTagVibrations) {
		t.Fatal("step should be in #minecraft:vibrations")
	}
	if !gameEventInTag(geBlockPlace, gameEventTagVibrations) {
		t.Fatal("block_place should be in #minecraft:vibrations")
	}
	if gameEventInTag(geStep, gameEventTagShriekerCanListen) {
		t.Fatal("step should NOT be in #minecraft:shrieker_can_listen")
	}
	if !gameEventInTag("sculk_sensor_tendrils_clicking", gameEventTagShriekerCanListen) {
		t.Fatal("sculk_sensor_tendrils_clicking should be in #minecraft:shrieker_can_listen")
	}
}

// TestSensorBusVibrationActivatesAfterDelay: a STEP emitted 3 blocks from a sculk sensor (within its
// radius 8) schedules a candidate on the sensor listener; the Ticker.tick ordering promotes the
// candidate AND decrements travelTicks on the same tick (1:1 VibrationSystem.Ticker.tick), so
// floor(distance)==3 decrements across 3 ticker calls deliver it, and the sensor activates with
// POWER = getRedstoneStrengthForDistance(3, 8) == 10.
func TestSensorBusVibrationActivatesAfterDelay(t *testing.T) {
	loop, mgr := newSculkLoop()
	sensorPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(sensorPos, sculkSensorState(), dimMinY)
	loop.resolveSculkSensor(sensorPos)

	// Emit STEP 3 blocks east of the sensor: block (11,64,8) center (11.5, 64.5, 8.5); the sensor center
	// is (8.5, 64.5, 8.5). Vec3 distance == 3.0, floor == 3. A player id as the source.
	const stepper int32 = 555
	loop.players = append(loop.players, &tickPlayer{x: 11.5, y: 64, z: 8.5, entityID: stepper})
	loop.gameEventAt(geStep, pk.Position{X: 11, Y: 64, Z: 8}, gameEventContext{sourceEntityID: stepper})

	be := loop.sculkSensors[sensorPos]
	if be == nil || be.vibration == nil || !be.vibration.hasCandidate {
		t.Fatal("STEP in range did not schedule a candidate on the sensor listener")
	}

	// First Ticker tick promotes the candidate (travelTicks set to floor(3.0) = 3) and decrements
	// it to 2 on the same tick; the sensor is not yet active.
	loop.tickBlockVibrationListeners()
	if tt := be.vibration.travelTicks; tt != 2 {
		t.Fatalf("travelTicks = %d, want 2 (promote sets travelTicks to 3, then immediate decrement to 2)", tt)
	}
	after, _ := mgr.GetBlock(sensorPos, dimMinY)
	if ph, _ := block.SculkSensorPhaseOf(after); ph == block.SculkSensorPhaseActive {
		t.Fatal("sensor activated before the travel delay elapsed")
	}

	// Drain the remaining travel: 2 -> 1 -> deliver on the tick that brings it to 0.
	loop.tickBlockVibrationListeners() // 2 -> 1
	loop.tickBlockVibrationListeners() // 1 -> 0 -> deliver + activate

	after, _ = mgr.GetBlock(sensorPos, dimMinY)
	ph, ok := block.SculkSensorPhaseOf(after)
	if !ok || ph != block.SculkSensorPhaseActive {
		t.Fatalf("sensor phase after travel = %v (ok=%v), want ACTIVE", ph, ok)
	}
	// POWER = getRedstoneStrengthForDistance(blockDistance=3, radius=8) = max(1, 15 - floor(15/8*3)) = 10.
	if p := block.SculkSensorPower(after); p != 10 {
		t.Fatalf("sensor POWER = %d, want 10 (getRedstoneStrengthForDistance(3, 8))", p)
	}
	// Comparator analog output = STEP frequency (1) while ACTIVE.
	if be.lastVibrationFrequency != 1 {
		t.Fatalf("lastVibrationFrequency = %d, want 1 (STEP)", be.lastVibrationFrequency)
	}
	// The current vibration cleared after delivery.
	if be.vibration.hasCurrent {
		t.Fatal("current vibration not cleared after delivery")
	}
}

// TestSensorBusVibrationOutOfRange: a STEP emitted 12 blocks from the sensor (radius 8) schedules
// nothing -- the getPostableListenerPosition distSqr gate rejects it before handleGameEvent.
func TestSensorBusVibrationOutOfRange(t *testing.T) {
	loop, mgr := newSculkLoop()
	sensorPos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(sensorPos, sculkSensorState(), dimMinY)
	loop.resolveSculkSensor(sensorPos)

	const stepper int32 = 556
	loop.players = append(loop.players, &tickPlayer{x: 20.5, y: 64, z: 8.5, entityID: stepper})
	// 12 blocks east -- distSqr 144 > 8^2 = 64, so the sensor is out of range.
	loop.gameEventAt(geStep, pk.Position{X: 20, Y: 64, Z: 8}, gameEventContext{sourceEntityID: stepper})

	be := loop.sculkSensors[sensorPos]
	if be != nil && be.vibration != nil && be.vibration.hasCandidate {
		t.Fatal("an out-of-range STEP scheduled a candidate (range gate failed)")
	}
	after, _ := mgr.GetBlock(sensorPos, dimMinY)
	if ph, _ := block.SculkSensorPhaseOf(after); ph == block.SculkSensorPhaseActive {
		t.Fatal("an out-of-range STEP activated the sensor")
	}
}

// TestShriekerBusIgnoresStepButDirectStepWarns: a shrieker's getListenableEvents is SHRIEKER_CAN_LISTEN,
// which does NOT contain STEP, so a STEP emitted next to a shrieker over the bus schedules no candidate.
// The player STILL advances the warning level via the DIRECT SculkShriekerBlock.stepOn path
// (tickSculkShriekers), which bypasses the vibration bus. CITE SculkShriekerBlock.stepOn / SHRIEKER_CAN_LISTEN.
func TestShriekerBusIgnoresStepButDirectStepWarns(t *testing.T) {
	loop, mgr := newSculkLoop()
	// newSculkLoop defaults levelDifficulty to NORMAL (canRespond needs !PEACEFUL).
	shPos := pk.Position{X: 8, Y: 64, Z: 8}
	// A CAN_SUMMON shrieker (canRespond needs CAN_SUMMON && !PEACEFUL && SPAWN_WARDENS).
	canSummon := block.ToStateID[block.SculkShrieker{CanSummon: true, Shrieking: false, Waterlogged: false}]
	mgr.SetBlock(shPos, canSummon, dimMinY)
	loop.resolveSculkShrieker(shPos)

	const stepper int32 = 557
	p := &tickPlayer{x: 8.5, y: 64, z: 8.5, entityID: stepper}
	loop.players = append(loop.players, p)

	// A STEP emitted at the shrieker cell over the bus: SHRIEKER_CAN_LISTEN excludes step -> no candidate.
	loop.gameEventAt(geStep, shPos, gameEventContext{sourceEntityID: stepper})
	be := loop.sculkShriekers[shPos]
	if be != nil && be.vibration != nil && be.vibration.hasCandidate {
		t.Fatal("shrieker scheduled a STEP candidate (SHRIEKER_CAN_LISTEN should exclude step)")
	}

	// The DIRECT step scan advances the warning level (the load-bearing shrieker path).
	loop.tickSculkShriekers()
	if wl := loop.resolveWardenTracker(stepper).warningLevel; wl != 1 {
		t.Fatalf("warning level after a direct step = %d, want 1", wl)
	}
}
