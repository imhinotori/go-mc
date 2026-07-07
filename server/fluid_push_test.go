package server

import (
	"math"
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// fluid_push_test.go gates B-A4 (fluid current push) and B-A9 (jumpOutOfFluid): the mob-generic
// ports of net.minecraft.world.entity.EntityFluidInteraction.update + Tracker.applyCurrentTo and
// net.minecraft.world.entity.LivingEntity.jumpOutOfFluid. Every assertion mirrors a jar method
// verified via javap -c -p on temp/cache/26.2-inner.jar (citations at each port site in fluid.go /
// fluid_physics.go). PORT-EXACT behavior gates: exact velocity vectors, not feel checks.

// The pig footprint (Width/Height 0.9). A pig at (8.5,64,8.5) has its AABB centered in the (8,64,8)
// cell (hw 0.45 -> 8.05..8.95), so a single-cell fluid setup is scanned by exactly that one cell.

// TestFluidPushCurrentAlongFlow: a mob in a water source whose only flowing neighbour is to +X gets
// pushed along the normalized flow (+X) scaled by the WATER motionScale 0.014. getFlow sums the
// gradient (heightDiff = ownHeight(8/9) - neighbourOwnHeight(4/9) > 0 toward the lower +X cell),
// normalizes to (1,0,0); Tracker.applyCurrentTo (non-Player) normalizes + scale(0.014). The three
// non-flow horizontal neighbours are solid so they contribute nothing (empty+wall -> skip). The
// source cell height (~0.888) exceeds the 0.4 shallow cutoff so there is NO height attenuation.
func TestFluidPushCurrentAlongFlow(t *testing.T) {
	loop, mgr := newFluidLoop()
	// Mob cell: a full water source. +X neighbour: flowing water (lower height) -> flow points +X.
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0) // source, amount 8, ownHeight 8/9
	setWater(mgr, pk.Position{X: 9, Y: 64, Z: 8}, 4) // flowing legacy 4 -> amount 4, ownHeight 4/9
	// The other three horizontal neighbours are solid walls (empty fluid + wall -> getFlow skips).
	setSolid(mgr, pk.Position{X: 7, Y: 64, Z: 8})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 7})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 9})

	e := fluidTestMob(8.5, 64.0, 8.5)
	loop.updateFluidCurrent(e, fluidWater, waterCurrentScale)

	// Expected: normalize(getFlow) == (1,0,0); * 0.014. vy gets 0 (flow has no Y). No min-boost
	// (0.014 > 0.0045). So exactly (0.014, 0, 0) added to a zero-velocity mob.
	const eps = 1e-9
	if math.Abs(e.vx-waterCurrentScale) > eps {
		t.Fatalf("vx after push = %v, want %v (normalized +X flow * 0.014)", e.vx, waterCurrentScale)
	}
	if math.Abs(e.vy) > eps {
		t.Fatalf("vy after push = %v, want 0 (flow has no vertical component here)", e.vy)
	}
	if math.Abs(e.vz) > eps {
		t.Fatalf("vz after push = %v, want 0", e.vz)
	}
	// Direction: strictly downstream (+X), matching the lower-neighbour side.
	if e.vx <= 0 {
		t.Fatalf("current must push toward the lower (+X) neighbour, got vx=%v", e.vx)
	}
}

// TestFluidPushDryEntityNoChange is the pig-oracle property (GATE both behind "in a fluid"): a mob
// on dry land has ZERO matching fluid cells, so updateFluidCurrent hits the count==0 skip and makes
// NO velocity change (no getFlow call, byte-identical to the pre-port dry path). jumpOutOfFluid is
// only called when the mob is in a fluid, so a dry mob never reaches it either.
func TestFluidPushDryEntityNoChange(t *testing.T) {
	loop, _ := newFluidLoop() // an all-air chunk: no fluid anywhere
	e := fluidTestMob(8.5, 64.0, 8.5)
	e.vx, e.vy, e.vz = 0.1, -0.2, 0.3 // arbitrary pre-existing velocity
	loop.updateFluidCurrent(e, fluidWater, waterCurrentScale)
	loop.updateFluidCurrent(e, fluidLava, lavaCurrentScaleOverworld)
	if e.vx != 0.1 || e.vy != -0.2 || e.vz != 0.3 {
		t.Fatalf("dry mob velocity changed: got (%v,%v,%v), want (0.1,-0.2,0.3) unchanged", e.vx, e.vy, e.vz)
	}
}

// TestFluidPushLavaScale: the same +X gradient in LAVA is scaled by the OVERWORLD lava motionScale
// (0.0023333333333333335), not the water 0.014. Confirms the per-fluid motionScale constant wiring.
func TestFluidPushLavaScale(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	setLava(mgr, pk.Position{X: 9, Y: 64, Z: 8}, 4)
	setSolid(mgr, pk.Position{X: 7, Y: 64, Z: 8})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 7})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 9})

	// Give the mob a horizontal velocity above the 0.003 min-boost band so the raw motionScale is
	// observed directly (a still mob would be boosted to the 0.0045 floor, since the lava scale
	// 0.0023333 is below it -- that boost is itself faithful, but here we want the raw scale).
	e := fluidTestMob(8.5, 64.0, 8.5)
	e.vx = 0.01 // > currentBoostAxisBand (0.003): the min-current boost is skipped
	loop.updateFluidCurrent(e, fluidLava, lavaCurrentScaleOverworld)

	const eps = 1e-12
	// vx started at 0.01; the push adds normalize(+X)*lavaScale = +lavaCurrentScaleOverworld.
	if math.Abs(e.vx-(0.01+lavaCurrentScaleOverworld)) > eps {
		t.Fatalf("lava push vx = %v, want %v (0.01 start + normalized +X * overworld lava scale)", e.vx, 0.01+lavaCurrentScaleOverworld)
	}
}

// TestJumpOutOfFluidHopsAtWall: a mob in water, pressed against a wall (horizontalCollision), whose
// up-and-forward box is open air, gets its vertical velocity set to the vanilla 0.3 hop impulse.
// jumpOutOfFluid: if horizontalCollision && isFree(dm.x, dm.y+0.6-getY()+oldY, dm.z) ->
// setDeltaMovement(dm.x, 0.3, dm.z). Here oldY == getY() (no move between), so the probe dy = 0.6.
func TestJumpOutOfFluidHopsAtWall(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0) // the water the mob is standing in

	e := fluidTestMob(8.5, 64.5, 8.5) // feet at 64.5 -> AABB minY 64 overlaps the water cell
	e.horizontalCollision = true      // pressed against a wall
	e.vx, e.vy, e.vz = 0.1, 0.0, 0.0  // moving +X, no vertical velocity yet
	oldY := e.y                       // the pre-move getY() captured by travelInFluid

	loop.jumpOutOfFluid(e, oldY)

	if e.vy != 0.30000001192092896 {
		t.Fatalf("jumpOutOfFluid should set vy to the 0.3 hop impulse, got %v", e.vy)
	}
	// dm.x / dm.z are preserved (setDeltaMovement(dm.x, 0.3, dm.z)).
	if e.vx != 0.1 || e.vz != 0.0 {
		t.Fatalf("jumpOutOfFluid must preserve horizontal velocity, got vx=%v vz=%v", e.vx, e.vz)
	}
}

// TestJumpOutOfFluidNoWallNoHop: without horizontalCollision (a mob swimming in open water, not
// pressed against a wall), jumpOutOfFluid returns immediately and leaves velocity untouched.
func TestJumpOutOfFluidNoWallNoHop(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	e := fluidTestMob(8.5, 64.5, 8.5)
	e.horizontalCollision = false // NOT against a wall
	e.vx, e.vy, e.vz = 0.1, -0.05, 0.0

	loop.jumpOutOfFluid(e, e.y)

	if e.vy != -0.05 {
		t.Fatalf("open-water mob must not hop: vy = %v, want -0.05 unchanged", e.vy)
	}
}

// TestJumpOutOfFluidBlockedByLiquidNoHop: even against a wall, if the up-and-forward box is NOT open
// air (it still contains liquid), isFree is false (containsAnyLiquid) and the mob does not hop. Here
// a tall water column fills the up-forward probe target, so mobIsFree returns false.
func TestJumpOutOfFluidBlockedByLiquidNoHop(t *testing.T) {
	loop, mgr := newFluidLoop()
	// A 3-tall water column at the +X neighbour so the up-and-forward probe box lands inside liquid.
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	setWater(mgr, pk.Position{X: 9, Y: 64, Z: 8}, 0)
	setWater(mgr, pk.Position{X: 9, Y: 65, Z: 8}, 0)
	setWater(mgr, pk.Position{X: 9, Y: 66, Z: 8}, 0)
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)
	setWater(mgr, pk.Position{X: 8, Y: 66, Z: 8}, 0)

	e := fluidTestMob(8.5, 64.5, 8.5)
	e.horizontalCollision = true
	e.vx, e.vy, e.vz = 0.6, 0.0, 0.0 // move strongly +X into the water column
	loop.jumpOutOfFluid(e, e.y)

	if e.vy == 0.30000001192092896 {
		t.Fatalf("mob must NOT hop into liquid: vy became the 0.3 impulse but the probe box holds water")
	}
}

// TestFlowGetFlowGradientDirection asserts getFlow points from the higher fluid toward the lower
// neighbour (the raw gradient, pre-normalize check via the sign): a source cell with a lower +X
// neighbour yields a strictly +X flow, and normalize keeps it a unit +X vector.
func TestFlowGetFlowGradientDirection(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0) // source
	setWater(mgr, pk.Position{X: 9, Y: 64, Z: 8}, 4) // lower +X
	setSolid(mgr, pk.Position{X: 7, Y: 64, Z: 8})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 7})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 9})

	pos := pk.Position{X: 8, Y: 64, Z: 8}
	flow := loop.getFlow(pos, loop.fluidAt(pos))
	const eps = 1e-9
	if math.Abs(flow.x-1.0) > eps || math.Abs(flow.y) > eps || math.Abs(flow.z) > eps {
		t.Fatalf("getFlow = (%v,%v,%v), want normalized (1,0,0) toward the lower +X neighbour", flow.x, flow.y, flow.z)
	}
}

// TestCurrentMinBoostFloor: a near-still mob in a very weak current (below the 0.0045 floor) gets the
// current re-normalized to exactly 0.0045 (Tracker.applyCurrentTo min-current boost). We force a weak
// current via the shallow-height attenuation: a thin (< 0.4 tall) fluid film scales the flow down so
// the scaled current length falls under 0.0045, triggering the re-normalize-to-floor branch.
func TestCurrentMinBoostFloor(t *testing.T) {
	loop, mgr := newFluidLoop()
	// A thin film: amount 1 (legacy 7) in the mob cell, lower amount... use a shallow source-vs-flow
	// gradient. Height = 1/9 ~ 0.111 < 0.4 -> flow scaled by 0.111 -> weak current.
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 6) // flowing, amount 2, ownHeight 2/9
	setWater(mgr, pk.Position{X: 9, Y: 64, Z: 8}, 7) // lower, amount 1, ownHeight 1/9
	setSolid(mgr, pk.Position{X: 7, Y: 64, Z: 8})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 7})
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 9})

	e := fluidTestMob(8.5, 64.0, 8.5)
	loop.updateFluidCurrent(e, fluidWater, waterCurrentScale)

	// The horizontal current magnitude must be at least the floor (the boost pins weak currents up).
	mag := math.Hypot(e.vx, e.vz)
	if mag+1e-12 < currentBoostFloor {
		t.Fatalf("weak-current min boost failed: horizontal magnitude %v < floor %v", mag, currentBoostFloor)
	}
	if e.vx <= 0 {
		t.Fatalf("boosted current must still point downstream (+X), got vx=%v", e.vx)
	}
}
