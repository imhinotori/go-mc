package server

// velocity_clamp_test.go - B-M3 gate: the LivingEntity.aiStep small-velocity clamp. Vanilla
// net.minecraft.world.entity.LivingEntity.aiStep zeroes each deltaMovement axis whose absolute
// value is below 0.003 (javap offsets 92-199; the non-player branch clamps x/z INDEPENDENTLY at
// 0.003 and the common y clamp is also 0.003). This runs early in aiStep, before applyInput and
// the serverAiStep goal/navigation body, so downstream physics integrates the clamped velocity.
//
// These tests drive the REAL clamp through mobAI.serverAiStep on a lone pig (no jump armed, no
// pushable neighbours), so the jump slot and pushNearbyEntities are velocity no-ops and the only
// thing that touches e.vx/vy/vz before the assertions is the clamp under test.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// newClampPig builds a bare AI pig wired for a serverAiStep drive, mirroring the minimal setup in
// TestServerAiStepOrder (a zero-value mobAI with no goals: navigation.tick / targetSelector.tick /
// jump slot / pushNearbyEntities are all no-ops on a lone, targetless mob).
func newClampPig() (*TickLoop, *Entity, *mobAI) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 0, 64, 0)
	m := &mobAI{}
	e.ai = m
	return loop, e, m
}

// TestVelocityClampZeroesSubThresholdAxes: dm=(0.001, 0.002, 0.5) -> (0, 0, 0.5). The x and y
// components are below 0.003 and must be zeroed; the z component (0.5 >> 0.003) must be kept.
func TestVelocityClampZeroesSubThresholdAxes(t *testing.T) {
	loop, e, m := newClampPig()
	e.vx, e.vy, e.vz = 0.001, 0.002, 0.5

	m.serverAiStep(loop, e)

	if e.vx != 0 {
		t.Fatalf("vx=0.001 (<0.003) must be clamped to 0, got %v", e.vx)
	}
	if e.vy != 0 {
		t.Fatalf("vy=0.002 (<0.003) must be clamped to 0, got %v", e.vy)
	}
	if e.vz != 0.5 {
		t.Fatalf("vz=0.5 (>=0.003) must be kept, got %v", e.vz)
	}
}

// TestVelocityClampKeepsAboveThreshold: every axis above the epsilon is unchanged.
func TestVelocityClampKeepsAboveThreshold(t *testing.T) {
	loop, e, m := newClampPig()
	e.vx, e.vy, e.vz = 0.01, -0.02, 0.03

	m.serverAiStep(loop, e)

	if e.vx != 0.01 {
		t.Fatalf("vx=0.01 (>0.003) must be kept, got %v", e.vx)
	}
	if e.vy != -0.02 {
		t.Fatalf("vy=-0.02 (abs>0.003) must be kept, got %v", e.vy)
	}
	if e.vz != 0.03 {
		t.Fatalf("vz=0.03 (>0.003) must be kept, got %v", e.vz)
	}
}

// TestVelocityClampPerAxisIndependent: each axis is clamped on its OWN magnitude, not jointly. A
// mob with one large axis and two tiny ones keeps only the large one (the non-player independent
// x/z branch, distinct from the player horizontalDistanceSqr joint zeroing).
func TestVelocityClampPerAxisIndependent(t *testing.T) {
	// Large x, tiny z: x kept, z zeroed (independent, NOT the player joint x/z rule).
	loop, e, m := newClampPig()
	e.vx, e.vy, e.vz = 0.5, 0, 0.001
	m.serverAiStep(loop, e)
	if e.vx != 0.5 {
		t.Fatalf("vx=0.5 must be kept, got %v", e.vx)
	}
	if e.vz != 0 {
		t.Fatalf("vz=0.001 (<0.003) must be clamped to 0 independently of vx, got %v", e.vz)
	}

	// Large z, tiny x: z kept, x zeroed.
	loop, e, m = newClampPig()
	e.vx, e.vy, e.vz = 0.001, 0, 0.5
	m.serverAiStep(loop, e)
	if e.vx != 0 {
		t.Fatalf("vx=0.001 (<0.003) must be clamped to 0 independently of vz, got %v", e.vx)
	}
	if e.vz != 0.5 {
		t.Fatalf("vz=0.5 must be kept, got %v", e.vz)
	}
}

// TestVelocityClampEpsilonBoundary: the compare is `Math.abs(dm) < 0.003` (dcmpg ifge), so EXACTLY
// 0.003 is NOT clamped (kept) and a value just below it IS clamped. Assert both the exact-boundary
// keep and the just-below-boundary zero, per axis.
func TestVelocityClampEpsilonBoundary(t *testing.T) {
	// Exactly 0.003 on every axis: abs < 0.003 is false -> all kept.
	loop, e, m := newClampPig()
	e.vx, e.vy, e.vz = 0.003, 0.003, 0.003
	m.serverAiStep(loop, e)
	if e.vx != 0.003 || e.vy != 0.003 || e.vz != 0.003 {
		t.Fatalf("exactly 0.003 must NOT be clamped (abs<0.003 is false); got (%v,%v,%v)", e.vx, e.vy, e.vz)
	}

	// Just below 0.003 on every axis: abs < 0.003 is true -> all zeroed.
	loop, e, m = newClampPig()
	e.vx, e.vy, e.vz = 0.0029999999, -0.0029999999, 0.0029999999
	m.serverAiStep(loop, e)
	if e.vx != 0 || e.vy != 0 || e.vz != 0 {
		t.Fatalf("just below 0.003 must be clamped to 0 on every axis; got (%v,%v,%v)", e.vx, e.vy, e.vz)
	}
}
