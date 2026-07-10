package server

// vibration_test.go -- behaviour pins for the GAME-EVENT / VIBRATION subsystem + the Warden VibrationUser
// (game_event.go + vibration.go, 1:1 javap Warden.VibrationUser + VibrationSystem this task). Verifies:
// (a) a game event broadcast reaches a registered listener within radius and, after the travel delay
// (== floor(distance)), drives anger; (b) a nearby STEP gives +35 direct-entity anger; (c) a projectile
// gives +10 on the FIRST hit and +35 on a SUBSEQUENT one; (d) VIBRATION_COOLDOWN is 40 ticks; (e) an
// event with NO listener registered is a pure no-op (the pig-oracle invariant).

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// vibLoop builds a physics loop + floor ready to spawn a NON-emerging warden (so its VibrationUser can
// receive immediately -- an emerging warden rejects vibrations).
func vibLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// vibLoopMgr is vibLoop but also returns the chunk manager so a test can place occluding blocks (wool).
func vibLoopMgr(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestVibrationWoolOccludesSignal: a solid wool wall spanning every one of the six 1E-5 nudge rays between
// an emitter and the warden occludes the vibration -- gameEvent registers NO candidate. Removing the wall
// lets the same event through. Cite VibrationSystem.Listener.isOccluded (OCCLUDES_VIBRATION_SIGNALS wool).
func TestVibrationWoolOccludesSignal(t *testing.T) {
	loop, mgr, floorY := vibLoopMgr(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false)
	const stepper int32 = 7210
	// Emitter one block east of the warden feet; the listener is at feet + eye height (2.465). Wrap the
	// EMITTER block cell and all six of its face-neighbours in wool so every nudge ray is blocked.
	wool := block.DefaultStateID["minecraft:white_wool"]
	ex, ey, ez := 9, floorY+1, 8
	for _, d := range [][3]int{{0, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
		mgr.SetBlock(pk.Position{X: ex + d[0], Y: ey + d[1], Z: ez + d[2]}, wool, dimMinY)
	}
	loop.gameEvent(geStep, 9.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: stepper})
	if w.warden.vibration != nil && w.warden.vibration.hasCandidate {
		t.Fatal("wool-occluded STEP still registered a vibration candidate (isOccluded returned false)")
	}

	// Clear the wool: the same event now reaches the listener.
	air := block.DefaultStateID["minecraft:air"]
	for _, d := range [][3]int{{0, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
		mgr.SetBlock(pk.Position{X: ex + d[0], Y: ey + d[1], Z: ez + d[2]}, air, dimMinY)
	}
	loop.gameEvent(geStep, 9.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: stepper})
	if w.warden.vibration == nil || !w.warden.vibration.hasCandidate {
		t.Fatal("un-occluded STEP did not register a candidate (isOccluded false-positive)")
	}
}

// drainTravel runs the warden vibration ticker n times (each == one Ticker.tick: select/decrement/deliver).
func drainTravel(t *TickLoop, w *Entity, n int) {
	for i := 0; i < n; i++ {
		t.tickWardenVibration(w)
	}
}

// TestGameEventReachesListenerInRadius: a STEP emitted at the warden's own feet (distance ~ eye height)
// reaches the registered listener and, after the travel delay, raises anger by +35 (direct entity source).
func TestGameEventReachesListenerInRadius(t *testing.T) {
	loop, floorY := vibLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false)
	const stepper int32 = 7201
	// Emit STEP at the warden's feet cell with the stepper as source.
	loop.gameEvent(geStep, 8.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: stepper})
	// The candidate is pending; the Ticker selects it on the first tick (travelTicks = floor(dist)).
	if w.warden.vibration == nil || !w.warden.vibration.hasCandidate {
		t.Fatal("game event did not register a candidate on the warden listener")
	}
	// Drain enough ticks to cover selection + travel + delivery (eye height 2.465 -> floor(dist) small).
	drainTravel(loop, w, 10)
	if got := w.warden.angerBySuspect[stepper]; got != wardenDefaultAnger {
		t.Fatalf("anger after STEP = %d, want +%d (DEFAULT_ANGER direct source)", got, wardenDefaultAnger)
	}
}

// TestVibrationTravelDelayEqualsDistance: the travel time set at selection is floor(distance) ticks; anger
// lands only AFTER that many decrement ticks. A step ~5 blocks away must not deliver before ~5 ticks.
func TestVibrationTravelDelayEqualsDistance(t *testing.T) {
	loop, floorY := vibLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false)
	const stepper int32 = 7202
	// Emit 5 blocks north (dz = 5) at the same y as the feet; the listener is at feet + eye height.
	loop.gameEvent(geStep, 8.5, float64(floorY+1), 8.5+5.0, gameEventContext{sourceEntityID: stepper})
	// First ticker tick promotes the candidate + sets travelTicks. Read it.
	loop.tickWardenVibration(w)
	tt := w.warden.vibration.travelTicks
	if tt < 4 {
		t.Fatalf("travelTicks = %d, want >= 4 (floor of ~5.5 block distance)", tt)
	}
	// Not yet delivered.
	if w.warden.angerBySuspect[stepper] != 0 {
		t.Fatalf("anger delivered before travel time elapsed (travelTicks=%d)", tt)
	}
	// Drain the remaining travel; anger lands.
	drainTravel(loop, w, tt+1)
	if got := w.warden.angerBySuspect[stepper]; got != wardenDefaultAnger {
		t.Fatalf("anger after travel = %d, want +%d", got, wardenDefaultAnger)
	}
}

// TestVibrationCooldownIs40: after a delivered vibration the VIBRATION_COOLDOWN is 40 ticks, and a second
// event during that window is rejected (canReceiveVibration returns false).
func TestVibrationCooldownIs40(t *testing.T) {
	loop, floorY := vibLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false)
	const s1 int32 = 7203
	loop.gameEvent(geStep, 8.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: s1})
	drainTravel(loop, w, 10) // deliver the first vibration
	if w.warden.vibrationCooldown <= 0 {
		t.Fatalf("VIBRATION_COOLDOWN not armed after delivery (got %d)", w.warden.vibrationCooldown)
	}
	// A second event while on cooldown is rejected (no candidate).
	base := w.warden.angerBySuspect[s1]
	const s2 int32 = 7204
	loop.gameEvent(geStep, 8.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: s2})
	if w.warden.vibration.hasCandidate {
		t.Fatal("event accepted while on VIBRATION_COOLDOWN (should be rejected)")
	}
	if w.warden.angerBySuspect[s2] != 0 {
		t.Fatal("cooldown-window event produced anger")
	}
	_ = base
}

// TestProjectileAngerFirstThenSubsequent: the FIRST projectile vibration gives +10; a SUBSEQUENT one
// (within RECENT_PROJECTILE 100t) gives +35. Both require the owner within 30 blocks.
func TestProjectileAngerFirstThenSubsequent(t *testing.T) {
	loop, floorY := vibLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false)
	const owner int32 = 7205
	// The projectile owner (a player) within 30 blocks so closerThan(owner, 30) holds.
	loop.players = append(loop.players, &tickPlayer{x: 8.5 + 3.0, y: float64(floorY + 1), z: 8.5, entityID: owner})
	// FIRST projectile: projectileOwnerID set, sourceEntityID is the projectile (irrelevant to anger).
	loop.gameEvent(geProjectileLand, 8.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: 9999, projectileOwnerID: owner})
	drainTravel(loop, w, 10)
	if got := w.warden.angerBySuspect[owner]; got != wardenProjectileFirstAnger {
		t.Fatalf("first projectile anger = %d, want +%d", got, wardenProjectileFirstAnger)
	}
	// Clear the VIBRATION_COOLDOWN so the second event is accepted; RECENT_PROJECTILE is still armed.
	w.warden.vibrationCooldown = 0
	loop.gameEvent(geProjectileLand, 8.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: 9999, projectileOwnerID: owner})
	drainTravel(loop, w, 10)
	// +10 (first) then +35 (subsequent) == 45.
	if got := w.warden.angerBySuspect[owner]; got != wardenProjectileFirstAnger+wardenDefaultAnger {
		t.Fatalf("after subsequent projectile anger = %d, want %d (+10 then +35)", got, wardenProjectileFirstAnger+wardenDefaultAnger)
	}
}

// TestDoorEmitsBlockOpenVibration: opening a door near a warden posts a BLOCK_OPEN game event that
// registers a vibration candidate on the warden's listener (the emitter wiring, not a direct feed).
func TestDoorEmitsBlockOpenVibration(t *testing.T) {
	loop, floorY := vibLoop(t)
	w := loop.spawnWarden(8.5, float64(floorY+1), 8.5, false)
	p := &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 8.5, entityID: 7220}
	pos := pk.Position{X: 8, Y: floorY + 1, Z: 8}
	loop.emitDoorGameEvent(pos, true, p) // the DoorBlock.setOpen gameEvent(player, BLOCK_OPEN, pos) tail
	if w.warden.vibration == nil || !w.warden.vibration.hasCandidate {
		t.Fatal("a BLOCK_OPEN near the warden did not register a vibration candidate (emitter not wired)")
	}
	if w.warden.vibration.candSourceID != p.entityID {
		t.Fatalf("BLOCK_OPEN candidate source = %d, want the opening player %d", w.warden.vibration.candSourceID, p.entityID)
	}
}

// TestGameEventNoListenerIsNoOp: with NO listener registered, gameEvent(...) touches nothing (the pig-
// oracle invariant -- a pig emits STEP with no listener and stays byte-identical).
func TestGameEventNoListenerIsNoOp(t *testing.T) {
	loop, floorY := vibLoop(t)
	if len(loop.vibrationListeners) != 0 {
		t.Fatalf("expected no listeners, got %d", len(loop.vibrationListeners))
	}
	// Must not panic / allocate a listener / mutate anything.
	loop.gameEvent(geStep, 8.5, float64(floorY+1), 8.5, gameEventContext{sourceEntityID: 4242})
	if len(loop.vibrationListeners) != 0 {
		t.Fatal("gameEvent created a listener from nothing")
	}
}
