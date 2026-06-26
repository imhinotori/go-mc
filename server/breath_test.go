package server

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// breath_test.go (Plan 17-13) gates the 1:1 air-supply / drowning port (breath.go) and the
// movement-accept fluid wire (moveWithFluidPhysics). The constants under test are the jar-verified
// vanilla values: maxAirSupply 300, decrement 1/tick underwater, refill 4/tick above, drowning at
// air<=-20 dealing 2.0 DROWN damage. Reuses the newFluidLoop / setWater / fluidTestPlayer harness
// from fluid_test.go (same package).

// breathPlayer makes a survival tickPlayer at (x,y,z) with a full bubble bar and full health, the
// registration default. A capturing client is attached so the drowning path's applyDamage ->
// actuallyHurt SetHealth send has a sink (it never touches a real socket). Eye height (1.62) lands
// the eyes in the block at floor(y+1.62).
func breathPlayer(x, y, z float64) *tickPlayer {
	return &tickPlayer{x: x, y: y, z: z, airSupply: maxAirSupply, health: maxHealth, client: captureClient(64)}
}

// TestEyeInWaterGatesOnEyes: air drains only when the EYES are submerged (isEyeInFluid), not when
// merely the feet are wet. A player whose feet are in water but whose eye block (y+1.62) is air is
// NOT eye-in-water.
func TestEyeInWaterGatesOnEyes(t *testing.T) {
	loop, mgr := newFluidLoop()
	// A single water block at the eye level of a player standing at y=64 (eye at 65.62 -> block 65).
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)

	submerged := breathPlayer(8.5, 64.0, 8.5) // eye at 65.62 -> water block 65
	if !loop.eyeInWater(submerged) {
		t.Fatalf("player with eyes in the water block should be eye-in-water")
	}

	// Feet-only: water at y=64 (feet) but air at the eye block 65.
	loop2, mgr2 := newFluidLoop()
	setWater(mgr2, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	feetOnly := breathPlayer(8.5, 64.0, 8.5) // eye at 65.62 -> air block 65
	if loop2.eyeInWater(feetOnly) {
		t.Fatalf("player with only feet in water should NOT be eye-in-water (air drains on eyes)")
	}
}

// TestAirDecrementsUnderwater: each tick with eyes submerged drops airSupply by exactly 1
// (decreaseAirSupply for a bare player == air-1).
func TestAirDecrementsUnderwater(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)
	p := breathPlayer(8.5, 64.0, 8.5)
	loop.players = append(loop.players, p)

	loop.tickBreath()
	if p.airSupply != maxAirSupply-1 {
		t.Fatalf("air after 1 underwater tick = %d, want %d (decrement by 1)", p.airSupply, maxAirSupply-1)
	}
	loop.tickBreath()
	if p.airSupply != maxAirSupply-2 {
		t.Fatalf("air after 2 underwater ticks = %d, want %d", p.airSupply, maxAirSupply-2)
	}
}

// TestAirRefillsAboveWater: out of water, air climbs by 4/tick (increaseAirSupply) clamped to
// maxAirSupply, and a full bar stays full.
func TestAirRefillsAboveWater(t *testing.T) {
	loop, _ := newFluidLoop() // no water anywhere
	p := breathPlayer(8.5, 64.0, 8.5)
	p.airSupply = 100
	loop.players = append(loop.players, p)

	loop.tickBreath()
	if p.airSupply != 104 {
		t.Fatalf("air after 1 dry tick = %d, want 104 (refill by 4)", p.airSupply)
	}

	// Near the cap: 298 + 4 = 302 -> clamped to 300.
	p.airSupply = 298
	loop.tickBreath()
	if p.airSupply != maxAirSupply {
		t.Fatalf("air refill clamp = %d, want %d (min(air+4, max))", p.airSupply, maxAirSupply)
	}

	// Full bar stays full (the `air < max` guard skips the refill).
	p.airSupply = maxAirSupply
	loop.tickBreath()
	if p.airSupply != maxAirSupply {
		t.Fatalf("full bar changed = %d, want %d (no-op when air==max)", p.airSupply, maxAirSupply)
	}
}

// TestDrowningDamageAtThreshold: when underwater drains air to the vanilla threshold (air<=-20),
// the tick resets air to 0 and deals exactly 2.0 DROWN damage through applyDamage.
func TestDrowningDamageAtThreshold(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)
	p := breathPlayer(8.5, 64.0, 8.5)
	// One tick below threshold so decreaseAirSupply (air-1) crosses to exactly -20.
	p.airSupply = drowningThreshold + 1 // -19 -> after decrement -20 (shouldTakeDrowningDamage true)
	loop.players = append(loop.players, p)

	loop.tickBreath()
	if p.airSupply != 0 {
		t.Fatalf("air after drowning tick = %d, want 0 (reset on drown)", p.airSupply)
	}
	if !floatNear(float64(p.health), float64(maxHealth-drownDamage), 1e-6) {
		t.Fatalf("health after drowning = %v, want %v (2.0 DROWN damage)", p.health, maxHealth-drownDamage)
	}
}

// TestNoDrownDamageAboveThreshold: at air==-19 the decrement reaches -20 and drowns; at air just
// above (so decrement lands above -20) NO damage is dealt and air keeps draining by 1.
func TestNoDrownDamageAboveThreshold(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)
	p := breathPlayer(8.5, 64.0, 8.5)
	p.airSupply = -10 // -> -11 after decrement, still > -20, no damage
	loop.players = append(loop.players, p)

	loop.tickBreath()
	if p.airSupply != -11 {
		t.Fatalf("air = %d, want -11 (plain decrement, no drown yet)", p.airSupply)
	}
	if !floatNear(float64(p.health), float64(maxHealth), 1e-6) {
		t.Fatalf("health = %v, want %v (no damage above threshold)", p.health, maxHealth)
	}
}

// TestAirRefillsOnLeavingWater: a player who drained underwater then surfaces refills from wherever
// the bar was (the dry branch), NOT a reset — vanilla's leaving-water path is increaseAirSupply.
func TestAirRefillsOnLeavingWater(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)
	p := breathPlayer(8.5, 64.0, 8.5)
	// Start with a partly-drained bar (well below max) so the +4 refill is observable and not
	// clamped: simulate a player who has been underwater a while.
	p.airSupply = 200
	loop.players = append(loop.players, p)

	// One more tick underwater drains by 1.
	loop.tickBreath()
	drained := p.airSupply
	if drained != 199 {
		t.Fatalf("air after underwater tick = %d, want 199 (decrement by 1)", drained)
	}

	// Surface: move the player far from any water so the eye block is air.
	p.x, p.z = 2.5, 2.5
	loop.tickBreath()
	if p.airSupply != drained+airRefillPerTick {
		t.Fatalf("air after surfacing = %d, want %d (refill by 4 from drained value)", p.airSupply, drained+airRefillPerTick)
	}
}

// TestMoveWithFluidPhysicsWire: the subtick movement-accept wire (moveWithFluidPhysics) applies
// 0.8 horizontal slowdown + 0.014 buoyancy to the ACCEPTED delta in water, and is the identity on
// dry land (so the dry movement path is byte-for-byte the old collide-only behavior).
func TestMoveWithFluidPhysicsWire(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	// In water: current at (8.5,64,8.5); collided target one block east + sinking 0.5.
	wet := fluidTestPlayer(8.5, 64.0, 8.5)
	nx, ny, nz := loop.moveWithFluidPhysics(wet, 9.5, 63.5, 8.5)
	// Horizontal delta 1.0 * 0.8 = 0.8 -> x = 8.5 + 0.8 = 9.3.
	if !floatNear(nx, 9.3, 1e-9) {
		t.Fatalf("in-water accepted x = %v, want 9.3 (delta*0.8)", nx)
	}
	if !floatNear(nz, 8.5, 1e-9) {
		t.Fatalf("in-water accepted z = %v, want 8.5 (no z movement)", nz)
	}
	// Vertical delta -0.5 + 0.014 buoyancy = -0.486 -> y = 64 - 0.486 = 63.514.
	if !floatNear(ny, 63.514, 1e-9) {
		t.Fatalf("in-water accepted y = %v, want 63.514 (delta+0.014 buoyancy)", ny)
	}

	// Dry land: identity — the wire returns the collided target verbatim.
	dry := fluidTestPlayer(2.5, 64.0, 2.5)
	dx, dy, dz := loop.moveWithFluidPhysics(dry, 3.5, 64.0, 2.5)
	if !floatNear(dx, 3.5, 1e-9) || !floatNear(dy, 64.0, 1e-9) || !floatNear(dz, 2.5, 1e-9) {
		t.Fatalf("dry wire not identity: got (%v,%v,%v), want (3.5,64.0,2.5)", dx, dy, dz)
	}
}
