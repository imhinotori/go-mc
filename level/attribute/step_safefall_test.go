package attribute

import (
	"math"
	"testing"
)

// TestStepAndSafeFall_RegistrationDefaults pins the EXACT (default, min, max) the two newly-added
// RangedAttributes are registered with in Attributes.<clinit>, read directly from the jar bytecode
// (javap net.minecraft.world.entity.ai.attributes.Attributes, this session):
//
//	step_height        = new RangedAttribute(..., 0.6d,  0.0d,    10.0d)
//	safe_fall_distance = new RangedAttribute(..., 3.0d, -1024.0d, 1024.0d)
//
// A drift in any constant is caught here (bit-exact compare on the default).
func TestStepAndSafeFall_RegistrationDefaults(t *testing.T) {
	cases := []struct {
		attr     *Attribute
		def      float64
		min, max float64
	}{
		{StepHeight, 0.6, 0.0, 10.0},
		{SafeFallDistance, 3.0, -1024.0, 1024.0},
	}
	for _, c := range cases {
		if got := c.attr.DefaultValue(); got != c.def || math.Float64bits(got) != math.Float64bits(c.def) {
			t.Errorf("%s default = %v (bits %#x), want %v (bits %#x)",
				c.attr.Name(), got, math.Float64bits(got), c.def, math.Float64bits(c.def))
		}
		if c.attr.ranged == nil {
			t.Fatalf("%s: expected a ranged envelope", c.attr.Name())
		}
		if c.attr.ranged.min != c.min {
			t.Errorf("%s min = %v, want %v", c.attr.Name(), c.attr.ranged.min, c.min)
		}
		if c.attr.ranged.max != c.max {
			t.Errorf("%s max = %v, want %v", c.attr.Name(), c.attr.ranged.max, c.max)
		}
	}
}

// TestLivingBase_CarriesStepAndSafeFall confirms createLivingAttributes adds STEP_HEIGHT (0.6) and
// SAFE_FALL_DISTANCE (3.0) to EVERY living entity at their registration defaults (exactly as vanilla's
// LivingEntity.createLivingAttributes does). Checked via a plain living creature that carries no
// override (pig), a monster (zombie), and the base living fallback (salmon; axolotl is now a ported type).
func TestLivingBase_CarriesStepAndSafeFall(t *testing.T) {
	for _, name := range []string{"pig", "zombie", "salmon"} {
		m := NewMapForEntity(name)
		if m == nil {
			t.Fatalf("%s: no attribute map", name)
		}
		if !m.HasAttribute(StepHeight.Name()) {
			t.Errorf("%s missing step_height (createLivingAttributes must add it)", name)
		}
		if !m.HasAttribute(SafeFallDistance.Name()) {
			t.Errorf("%s missing safe_fall_distance (createLivingAttributes must add it)", name)
		}
		if got := m.GetValue(StepHeight.Name()); got != 0.6 {
			t.Errorf("%s step_height = %v, want 0.6 (living default)", name, got)
		}
		if got := m.GetValue(SafeFallDistance.Name()); got != 3.0 {
			t.Errorf("%s safe_fall_distance = %v, want 3.0 (living default)", name, got)
		}
	}
}

// TestEnderman_StepHeightOverride pins EnderMan.createAttributes .add(STEP_HEIGHT, 1.0) (javap: Monster
// base + MAX_HEALTH 40, MOVEMENT_SPEED 0.3, ATTACK_DAMAGE 7, FOLLOW_RANGE 64, STEP_HEIGHT dconst_1 ==
// 1.0). The enderman auto-steps a full block, so its STEP_HEIGHT diverges from the 0.6 living default.
func TestEnderman_StepHeightOverride(t *testing.T) {
	m := NewMapForEntity("enderman")
	if m == nil {
		t.Fatal("enderman: no attribute map")
	}
	if got := m.GetValue(StepHeight.Name()); got != 1.0 || math.Float64bits(got) != math.Float64bits(1.0) {
		t.Errorf("enderman step_height = %v, want 1.0 (EnderMan.createAttributes override)", got)
	}
	// safe_fall_distance is NOT overridden by the enderman -> stays the living default 3.0.
	if got := m.GetValue(SafeFallDistance.Name()); got != 3.0 {
		t.Errorf("enderman safe_fall_distance = %v, want 3.0 (no override)", got)
	}
}

// TestFox_SafeFallDistanceOverride pins Fox.createAttributes .add(SAFE_FALL_DISTANCE, 5.0) (javap:
// Animal base + MOVEMENT_SPEED 0.3, MAX_HEALTH 10, ATTACK_DAMAGE 2, SAFE_FALL_DISTANCE ldc2_w 5.0d,
// FOLLOW_RANGE 32). A fox survives a taller fall, so its SAFE_FALL_DISTANCE diverges from the 3.0
// living default.
func TestFox_SafeFallDistanceOverride(t *testing.T) {
	m := NewMapForEntity("fox")
	if m == nil {
		t.Fatal("fox: no attribute map")
	}
	if got := m.GetValue(SafeFallDistance.Name()); got != 5.0 || math.Float64bits(got) != math.Float64bits(5.0) {
		t.Errorf("fox safe_fall_distance = %v, want 5.0 (Fox.createAttributes override)", got)
	}
	// step_height is NOT overridden by the fox -> stays the living default 0.6.
	if got := m.GetValue(StepHeight.Name()); got != 0.6 {
		t.Errorf("fox step_height = %v, want 0.6 (no override)", got)
	}
}
