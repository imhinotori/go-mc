package server

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// travel_lava_test.go (B-A5) gates the mob travelInLava movement branch: a mob whose AABB is in
// lava uses the vanilla thick-lava physics (0.5 drag + lava gravity) INSTEAD of dry-land physics.
// Every constant asserted here is javap-verified at the port site (fluid_physics.go
// travelInLavaVertical). These are PORT-EXACT velocity gates against the hand-computed vanilla
// LivingEntity.travelInLava sequence, not feel checks.
//
//	Vanilla net.minecraft.world.entity.LivingEntity.travelInLava (velocity ops, per javap):
//	  if isInShallowFluid(LAVA): dm.multiply(0.5, 0.800000011920929, 0.5); getFluidFallingAdjustedMovement(g, falling, dm)
//	  else:                      dm.scale(0.5)                       // uniform 0.5 all axes
//	  if g != 0:                 dm.add(0, -g/4.0, 0)                // the outer lava gravity (-0.02)

// TestLavaMovementDeep asserts the DEEP-lava velocity sequence (a lava SOURCE column: fluid height
// 8/9 == 0.889 > the pig's 0.4 jump threshold, so isInShallowFluid==false). Deep lava scales ALL
// three axes by 0.5 (vertical drag 0.5, NOT 0.8, and NO getFluidFallingAdjustedMovement), then adds
// the outer -baseGravity/4 == -0.02 lava gravity. Hand-computed from the jar sequence:
//
//	vx=0.5  -> *0.5 = 0.25
//	vz=0.3  -> *0.5 = 0.15
//	vy=-0.1 -> *0.5 = -0.05 ; then + (-0.08/4 == -0.02) = -0.07
func TestLavaMovementDeep(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0) // source -> amount 8 -> height 0.889 > 0.4 (deep)

	e := fluidTestMob(8.5, 64.0, 8.5)
	e.vx, e.vy, e.vz = 0.5, -0.1, 0.3

	// Guard the fixture: the pig in a source lava column must read DEEP (not shallow).
	if loop.isInShallowFluid(e, fluidLava) {
		t.Fatalf("fixture error: pig in lava SOURCE should be DEEP lava (height 0.889 > threshold 0.4)")
	}

	loop.travelInLavaVertical(e)

	wantVx, wantVy, wantVz := 0.25, -0.07, 0.15
	if !floatNear(e.vx, wantVx, 1e-9) || !floatNear(e.vy, wantVy, 1e-9) || !floatNear(e.vz, wantVz, 1e-9) {
		t.Fatalf("deep lava travel = (%v,%v,%v), want (%v,%v,%v) [0.5 all-axis drag + (-0.08/4) gravity]",
			e.vx, e.vy, e.vz, wantVx, wantVy, wantVz)
	}
}

// TestLavaMovementShallow asserts the SHALLOW-lava velocity sequence (flowing lava legacy 5 ->
// amount 3 -> height 3/9 == 0.333 <= the pig's 0.4 threshold, so isInShallowFluid==true). Shallow
// lava multiplies (0.5, 0.800000011920929, 0.5) then applies getFluidFallingAdjustedMovement (the
// reduced baseGravity/16 == 0.005 pull), then the outer -baseGravity/4 == -0.02 gravity. BOTH
// gravity terms. Hand-computed:
//
//	vx=0.5  -> *0.5 = 0.25
//	vz=0.3  -> *0.5 = 0.15
//	vy=-0.1 -> *0.800000011920929 = -0.0800000011920929 ; getFluidFallingAdjustedMovement:
//	          -0.0800000011920929 - 0.005 = -0.0850000011920929 (neutral guard OFF: the
//	          <0.003 clause fails) ; then + (-0.08/4 == -0.02) = -0.1050000011920929
func TestLavaMovementShallow(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 5) // legacy 5 -> amount 3 -> height 0.333 <= 0.4 (shallow)

	e := fluidTestMob(8.5, 64.0, 8.5)
	e.vx, e.vy, e.vz = 0.5, -0.1, 0.3

	if !loop.isInShallowFluid(e, fluidLava) {
		t.Fatalf("fixture error: pig in shallow flowing lava should read isInShallowFluid==true (height 0.333 <= 0.4)")
	}

	loop.travelInLavaVertical(e)

	wantVx, wantVy, wantVz := 0.25, -0.1050000011920929, 0.15
	if !floatNear(e.vx, wantVx, 1e-9) || !floatNear(e.vy, wantVy, 1e-9) || !floatNear(e.vz, wantVz, 1e-9) {
		t.Fatalf("shallow lava travel = (%v,%v,%v), want (%v,%v,%v) [(0.5,0.8,0.5) drag + reduced/16 + (-0.08/4)]",
			e.vx, e.vy, e.vz, wantVx, wantVy, wantVz)
	}
}

// TestFluidTravelLavaDragIsHalfNotWater is the divergence gate: lava horizontal drag is 0.5, NOT
// water's 0.8 (getWaterSlowDown) and NOT the dry 0.98 friction. A mob with pure horizontal velocity
// in a lava source must have its X/Z halved (deep-lava scale(0.5)); asserting the value is 0.5*vx
// and explicitly NOT 0.8*vx proves the lava branch (not the water branch or dry physics) ran.
func TestFluidTravelLavaDragIsHalfNotWater(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	e := fluidTestMob(8.5, 64.0, 8.5)
	e.vx, e.vy, e.vz = 1.0, 0.0, 1.0
	loop.travelInLavaVertical(e)

	if !floatNear(e.vx, 0.5, 1e-9) || !floatNear(e.vz, 0.5, 1e-9) {
		t.Fatalf("lava horizontal drag = (%v,%v), want (0.5,0.5) [lava 0.5 drag]", e.vx, e.vz)
	}
	if floatNear(e.vx, 0.8, 1e-9) {
		t.Fatalf("lava horizontal drag must NOT be 0.8 (that is water getWaterSlowDown), got vx=%v", e.vx)
	}
	if floatNear(e.vx, 0.98, 1e-9) {
		t.Fatalf("lava horizontal drag must NOT be 0.98 (that is dry air friction), got vx=%v", e.vx)
	}
}

// TestFluidTravelDryMobUnchangedByLavaBranch is the pig-oracle gate: the lava branch runs ONLY when
// the mob is in lava. This asserts travelInLavaVertical is never reached for a dry mob by driving the
// SAME dispatch decision the tickPhysics loop uses (mobInLava). A dry pig reads mobInLava==false, so
// the lava branch is skipped entirely and the mob takes the unchanged dry travelInAir path -- the
// dry oracle world (no lava) is byte-identical.
func TestFluidTravelDryMobUnchangedByLavaBranch(t *testing.T) {
	loop, mgr := newFluidLoop()
	setLava(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)

	// A DRY mob far from the lava: the tickPhysics gate (mobInLava) is the SAME predicate that
	// selects the lava branch, so a false read means travelInLavaVertical never runs on it.
	dry := fluidTestMob(2.5, 64.0, 2.5)
	if loop.mobInLava(dry) {
		t.Fatalf("dry mob should read mobInLava==false so the lava travel branch is never selected")
	}

	// And the wet mob DOES select the branch -- the gate is live, not dead.
	wet := fluidTestMob(8.5, 64.0, 8.5)
	if !loop.mobInLava(wet) {
		t.Fatalf("mob in lava should read mobInLava==true so the lava travel branch is selected")
	}
}
