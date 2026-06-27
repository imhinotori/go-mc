package attribute

import (
	"math"
	"testing"
)

// --- calculateValue fold golden tests -----------------------------------------------------------
//
// These pin the EXACT vanilla AttributeInstance.calculateValue fold:
//   d1 = base + sum(ADD_VALUE)
//   d3 = d1 + sum_over(ADD_MULTIPLIED_BASE: d1 * amount)        // d1 FROZEN after ADD_VALUE
//   d3 = d3 * product_over(ADD_MULTIPLIED_TOTAL: 1.0 + amount)  // compounds the running total
//   return sanitizeValue(d3)
// with hand-computed expected values, so a regression in the fold order or the d1-freeze is caught.

func newTestInstance(base float64, mods ...AttributeModifier) *AttributeInstance {
	// A wide-open RangedAttribute so sanitizeValue does not clamp the golden values (we test clamp
	// separately). default==base so the instance starts at base, then we override to be explicit.
	a := NewRangedAttribute("test", 0.0, -1.0e9, 1.0e9)
	inst := newAttributeInstance(a)
	inst.SetBaseValue(base)
	for _, m := range mods {
		inst.AddTransientModifier(m)
	}
	return inst
}

func TestCalculateValue_NoModifiers(t *testing.T) {
	got := newTestInstance(20.0).Value()
	if got != 20.0 {
		t.Fatalf("base-only fold = %v, want 20.0", got)
	}
}

func TestCalculateValue_AddValueOnly(t *testing.T) {
	// base 10 + ADD_VALUE(+5) + ADD_VALUE(-2) = 13
	got := newTestInstance(10.0,
		AttributeModifier{ID: "a", Amount: 5.0, Operation: AddValue},
		AttributeModifier{ID: "b", Amount: -2.0, Operation: AddValue},
	).Value()
	if got != 13.0 {
		t.Fatalf("ADD_VALUE fold = %v, want 13.0", got)
	}
}

func TestCalculateValue_AddMultipliedBaseUsesFrozenD1(t *testing.T) {
	// THE load-bearing case: two ADD_MULTIPLIED_BASE modifiers each multiply the SAME frozen d1
	// (the post-ADD_VALUE base), NOT a compounding total.
	//   d1 = 10 + 2 = 12                                    (ADD_VALUE +2)
	//   d3 = 12
	//   d3 += 12 * 0.5 = 12 + 6  = 18                       (ADD_MULTIPLIED_BASE 0.5 uses d1=12)
	//   d3 += 12 * 0.5 = 18 + 6  = 24                       (second 0.5 ALSO uses d1=12, not 18)
	// If a buggy port used the running total, the second would add 18*0.5=9 -> 27. So 24 vs 27
	// distinguishes the correct freeze.
	got := newTestInstance(10.0,
		AttributeModifier{ID: "add", Amount: 2.0, Operation: AddValue},
		AttributeModifier{ID: "mb1", Amount: 0.5, Operation: AddMultipliedBase},
		AttributeModifier{ID: "mb2", Amount: 0.5, Operation: AddMultipliedBase},
	).Value()
	if got != 24.0 {
		t.Fatalf("ADD_MULTIPLIED_BASE frozen-d1 fold = %v, want 24.0 (a buggy compounding port yields 27.0)", got)
	}
}

func TestCalculateValue_AddMultipliedTotalCompounds(t *testing.T) {
	// ADD_MULTIPLIED_TOTAL compounds the running d3 by (1+amount):
	//   d1 = 10
	//   d3 = 10
	//   d3 *= 1 + 0.5 = 15
	//   d3 *= 1 + 1.0 = 30
	got := newTestInstance(10.0,
		AttributeModifier{ID: "mt1", Amount: 0.5, Operation: AddMultipliedTotal},
		AttributeModifier{ID: "mt2", Amount: 1.0, Operation: AddMultipliedTotal},
	).Value()
	if got != 30.0 {
		t.Fatalf("ADD_MULTIPLIED_TOTAL fold = %v, want 30.0", got)
	}
}

func TestCalculateValue_FullThreeOperationOrder(t *testing.T) {
	// All three operations together, exercising the full pipeline ordering:
	//   d1 = 8 + 4 = 12                              (ADD_VALUE +4)
	//   d3 = 12
	//   d3 += 12 * 0.25 = 12 + 3 = 15                (ADD_MULTIPLIED_BASE 0.25, frozen d1=12)
	//   d3 *= 1 + 0.5 = 22.5                         (ADD_MULTIPLIED_TOTAL 0.5)
	got := newTestInstance(8.0,
		AttributeModifier{ID: "v", Amount: 4.0, Operation: AddValue},
		AttributeModifier{ID: "b", Amount: 0.25, Operation: AddMultipliedBase},
		AttributeModifier{ID: "tt", Amount: 0.5, Operation: AddMultipliedTotal},
	).Value()
	if got != 22.5 {
		t.Fatalf("full three-op fold = %v, want 22.5", got)
	}
}

// --- sanitizeValue / Mth.clamp tests ------------------------------------------------------------

func TestSanitizeValue_ClampHighLow(t *testing.T) {
	a := NewRangedAttribute("clamped", 1.0, 1.0, 10.0)
	cases := []struct {
		in, want float64
	}{
		{in: 5.0, want: 5.0},   // within range: unchanged
		{in: 0.0, want: 1.0},   // below min: -> min (strict < test)
		{in: 1.0, want: 1.0},   // exactly min: unchanged
		{in: 10.0, want: 10.0}, // exactly max: unchanged (Math.min(10,10)=10)
		{in: 99.0, want: 10.0}, // above max: -> max via Math.min
		{in: -50, want: 1.0},   // far below: -> min
	}
	for _, c := range cases {
		if got := a.sanitizeValue(c.in); got != c.want {
			t.Errorf("sanitizeValue(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSanitizeValue_NaNCollapsesToMin(t *testing.T) {
	// RangedAttribute.sanitizeValue: Double.isNaN(value) -> return minValue (the NaN guard is FIRST).
	a := NewRangedAttribute("nan", 5.0, 2.0, 8.0)
	got := a.sanitizeValue(math.NaN())
	if got != 2.0 {
		t.Fatalf("sanitizeValue(NaN) = %v, want 2.0 (min)", got)
	}
}

func TestSanitizeValue_BaseAttributeIdentity(t *testing.T) {
	// A bare (non-ranged) Attribute.sanitizeValue is the identity.
	a := NewAttribute("identity", 0.0)
	for _, v := range []float64{-1e9, 0, 42.5, 1e9} {
		if got := a.sanitizeValue(v); got != v {
			t.Errorf("base sanitizeValue(%v) = %v, want identity", v, got)
		}
	}
	// And NaN passes through unchanged (no ranged guard).
	if got := a.sanitizeValue(math.NaN()); !math.IsNaN(got) {
		t.Errorf("base sanitizeValue(NaN) = %v, want NaN passthrough", got)
	}
}

func TestCalculateValue_AppliesSanitizeClamp(t *testing.T) {
	// The fold tail runs sanitizeValue: a modifier driving the value past max is clamped.
	a := NewRangedAttribute("hp", 20.0, 1.0, 30.0)
	inst := newAttributeInstance(a)
	inst.SetBaseValue(20.0)
	inst.AddTransientModifier(AttributeModifier{ID: "huge", Amount: 100.0, Operation: AddValue})
	// d3 = 120 -> clamp to max 30.0
	if got := inst.Value(); got != 30.0 {
		t.Fatalf("clamped fold = %v, want 30.0 (max)", got)
	}
}

// --- RangedAttribute ctor invariants ------------------------------------------------------------

func TestNewRangedAttribute_PanicsOnBadBounds(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected panic, got none", name)
			}
		}()
		fn()
	}
	mustPanic("min>max", func() { NewRangedAttribute("x", 0, 10, 5) })
	mustPanic("default<min", func() { NewRangedAttribute("x", -1, 0, 10) })
	mustPanic("default>max", func() { NewRangedAttribute("x", 11, 0, 10) })
}
