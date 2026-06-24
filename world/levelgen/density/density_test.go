package density

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// --- test helpers: a real data source + a deterministic noise binder ---

// testBinder seeds NormalNoise/BlendedNoise deterministically from a fixed factory and
// the real Wave-1 noise params, so the cave/router parse tests build + evaluate the
// SAME way RandomState does (Wave 2) without depending on router.go. This keeps Task-1
// tests self-contained while still exercising the real seeded primitives.
type testBinder struct {
	factory levelgen.PositionalRandomFactory
}

func newTestBinder(seed int64) *testBinder {
	return &testBinder{factory: levelgen.NewXoroshiro(seed).ForkPositional()}
}

func (b *testBinder) NormalNoise(id string) (*synth.NormalNoise, error) {
	raw, err := data.Noise(id)
	if err != nil {
		return nil, err
	}
	var p struct {
		FirstOctave int       `json:"firstOctave"`
		Amplitudes  []float64 `json:"amplitudes"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	rs := b.factory.FromHashOf(id)
	return synth.NewNormalNoise(rs, p.FirstOctave, p.Amplitudes), nil
}

func (b *testBinder) BlendedNoise(xz, y, xzf, yf, smear float64) (*synth.BlendedNoise, error) {
	rs := b.factory.FromHashOf("minecraft:terrain")
	return synth.NewBlendedNoise(rs, xz, y, xzf, yf, smear), nil
}

// parseInline parses a JSON literal with no ref source (constant/arithmetic trees).
func parseInline(t *testing.T, jsonStr string) Function {
	t.Helper()
	r := NewRegistry(nil, nil)
	fn, err := r.Parse(json.RawMessage(jsonStr))
	if err != nil {
		t.Fatalf("Parse(%s) error: %v", jsonStr, err)
	}
	return fn
}

const eps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) <= eps }

// Test 1: the Mapped transforms + add/mul/min/max/clamp compose correctly.
func TestUnaryAndArithmeticNodes(t *testing.T) {
	// abs(-3) = 3
	if v := parseInline(t, `{"type":"minecraft:abs","argument":-3.0}`).Compute(Context{}); !approx(v, 3) {
		t.Errorf("abs(-3)=%v want 3", v)
	}
	// square(4) = 16
	if v := parseInline(t, `{"type":"minecraft:square","argument":4.0}`).Compute(Context{}); !approx(v, 16) {
		t.Errorf("square(4)=%v want 16", v)
	}
	// cube(2) = 8
	if v := parseInline(t, `{"type":"minecraft:cube","argument":2.0}`).Compute(Context{}); !approx(v, 8) {
		t.Errorf("cube(2)=%v want 8", v)
	}
	// half_negative(-4) = -2 ; half_negative(4) = 4
	if v := parseInline(t, `{"type":"minecraft:half_negative","argument":-4.0}`).Compute(Context{}); !approx(v, -2) {
		t.Errorf("half_negative(-4)=%v want -2", v)
	}
	if v := parseInline(t, `{"type":"minecraft:half_negative","argument":4.0}`).Compute(Context{}); !approx(v, 4) {
		t.Errorf("half_negative(4)=%v want 4", v)
	}
	// quarter_negative(-4) = -1
	if v := parseInline(t, `{"type":"minecraft:quarter_negative","argument":-4.0}`).Compute(Context{}); !approx(v, -1) {
		t.Errorf("quarter_negative(-4)=%v want -1", v)
	}
	// invert(4) = 0.25  (1/x, NOT -x — bytecode-verified)
	if v := parseInline(t, `{"type":"minecraft:invert","argument":4.0}`).Compute(Context{}); !approx(v, 0.25) {
		t.Errorf("invert(4)=%v want 0.25 (1/x)", v)
	}
	// squeeze(1) = 1/2 - 1/24 = 0.4583...
	if v := parseInline(t, `{"type":"minecraft:squeeze","argument":1.0}`).Compute(Context{}); !approx(v, 0.5-1.0/24.0) {
		t.Errorf("squeeze(1)=%v want %v", v, 0.5-1.0/24.0)
	}
	// add(3,4)=7
	if v := parseInline(t, `{"type":"minecraft:add","argument1":3.0,"argument2":4.0}`).Compute(Context{}); !approx(v, 7) {
		t.Errorf("add(3,4)=%v want 7", v)
	}
	// mul(3,4)=12 ; mul(0,x)=0 short-circuit
	if v := parseInline(t, `{"type":"minecraft:mul","argument1":3.0,"argument2":4.0}`).Compute(Context{}); !approx(v, 12) {
		t.Errorf("mul(3,4)=%v want 12", v)
	}
	if v := parseInline(t, `{"type":"minecraft:mul","argument1":0.0,"argument2":4.0}`).Compute(Context{}); !approx(v, 0) {
		t.Errorf("mul(0,4)=%v want 0", v)
	}
	// min(3,4)=3 ; max(3,4)=4
	if v := parseInline(t, `{"type":"minecraft:min","argument1":3.0,"argument2":4.0}`).Compute(Context{}); !approx(v, 3) {
		t.Errorf("min(3,4)=%v want 3", v)
	}
	if v := parseInline(t, `{"type":"minecraft:max","argument1":3.0,"argument2":4.0}`).Compute(Context{}); !approx(v, 4) {
		t.Errorf("max(3,4)=%v want 4", v)
	}
	// clamp(input=10, min=-1, max=1) = 1 ; clamp(-10)=-1 ; clamp(0.5)=0.5
	c := parseInline(t, `{"type":"minecraft:clamp","input":10.0,"min":-1.0,"max":1.0}`)
	if v := c.Compute(Context{}); !approx(v, 1) {
		t.Errorf("clamp(10,-1,1)=%v want 1", v)
	}
	if c2 := parseInline(t, `{"type":"minecraft:clamp","input":-10.0,"min":-1.0,"max":1.0}`); !approx(c2.Compute(Context{}), -1) {
		t.Errorf("clamp(-10,-1,1) want -1")
	}
}

// Test 2: y_clamped_gradient ramps + range_choice/interval_select branch by range.
func TestYGradientAndRangeChoice(t *testing.T) {
	// y_clamped_gradient from (y=0 -> 0) to (y=10 -> 100): at y=5 -> 50, below -> 0, above -> 100.
	g := parseInline(t, `{"type":"minecraft:y_clamped_gradient","from_y":0,"to_y":10,"from_value":0.0,"to_value":100.0}`)
	if v := g.Compute(Context{Y: 5}); !approx(v, 50) {
		t.Errorf("ygrad(y=5)=%v want 50", v)
	}
	if v := g.Compute(Context{Y: -5}); !approx(v, 0) {
		t.Errorf("ygrad(y=-5)=%v want 0 (clamped)", v)
	}
	if v := g.Compute(Context{Y: 20}); !approx(v, 100) {
		t.Errorf("ygrad(y=20)=%v want 100 (clamped)", v)
	}

	// range_choice: input=y_grad; in [40,60) -> 1, else -> -1. At y=5 (grad=50, in) -> 1; y=9 (grad=90, out) -> -1.
	rc := parseInline(t, `{"type":"minecraft:range_choice","input":{"type":"minecraft:y_clamped_gradient","from_y":0,"to_y":10,"from_value":0.0,"to_value":100.0},"min_inclusive":40.0,"max_exclusive":60.0,"when_in_range":1.0,"when_out_of_range":-1.0}`)
	if v := rc.Compute(Context{Y: 5}); !approx(v, 1) {
		t.Errorf("range_choice(grad=50 in [40,60))=%v want 1", v)
	}
	if v := rc.Compute(Context{Y: 9}); !approx(v, -1) {
		t.Errorf("range_choice(grad=90 out)=%v want -1", v)
	}

	// interval_select: input=const; thresholds [0,1]; functions [10,20,30].
	// input<0 -> 10 ; input<1 -> 20 ; else -> 30.
	for _, tc := range []struct {
		in   string
		want float64
	}{
		{`-5.0`, 10}, {`0.5`, 20}, {`5.0`, 30},
	} {
		is := parseInline(t, `{"type":"minecraft:interval_select","input":`+tc.in+`,"thresholds":[0.0,1.0],"functions":[10.0,20.0,30.0]}`)
		if v := is.Compute(Context{}); !approx(v, tc.want) {
			t.Errorf("interval_select(input=%s)=%v want %v", tc.in, v, tc.want)
		}
	}
}

// Test 3: a spline node with known control points evaluates the cubic correctly.
func TestSplineCubic(t *testing.T) {
	// A spline over a y_clamped_gradient coordinate, two points: at loc 0 -> value 0
	// (deriv 1) and loc 10 -> value 10 (deriv 1). With matching slope-1 derivatives the
	// cubic is exactly the straight line value==coord across the interval; at the
	// endpoints linearExtend uses the same slope, so value==coord everywhere here.
	// coordinate = y_clamped_gradient that maps blockY directly to its value over a wide
	// range so coord == blockY for the tested range.
	j := `{"type":"minecraft:spline","spline":{
		"coordinate":{"type":"minecraft:y_clamped_gradient","from_y":-100,"to_y":100,"from_value":-100.0,"to_value":100.0},
		"points":[
			{"location":0.0,"value":0.0,"derivative":1.0},
			{"location":10.0,"value":10.0,"derivative":1.0}
		]}}`
	sp := parseInline(t, j)
	for _, y := range []int{-50, 0, 5, 10, 50} {
		coord := mthClampedMap(float64(y), -100, 100, -100, 100) // == y in range
		if v := sp.Compute(Context{Y: y}); !approx(v, coord) {
			t.Errorf("spline(y=%d) coord=%v -> %v, want %v (slope-1 line)", y, coord, v, coord)
		}
	}

	// A spline with a non-linear shape: points (0->0 deriv 0) (1->1 deriv 0). At the
	// midpoint t=0.5 the cubic with zero derivatives is a smoothstep: value = lerp(t,0,1)
	// + t(1-t)*lerp(t, m, n) where m=0-(1-0)=-1, n=-0+(1-0)=1 -> 0.5 + 0.25*lerp(0.5,-1,1)
	// = 0.5 + 0.25*0 = 0.5.
	j2 := `{"type":"minecraft:spline","spline":{
		"coordinate":{"type":"minecraft:y_clamped_gradient","from_y":0,"to_y":1,"from_value":0.0,"to_value":1.0},
		"points":[
			{"location":0.0,"value":0.0,"derivative":0.0},
			{"location":1.0,"value":1.0,"derivative":0.0}
		]}}`
	sp2 := parseInline(t, j2)
	// y between 0 and 1 isn't reachable with integer Y; instead test the analytic value
	// at coord=0.5 by driving a coordinate that yields 0.5. Use from_y 0 to_y 2 mapping
	// y=1 -> 0.5.
	j3 := `{"type":"minecraft:spline","spline":{
		"coordinate":{"type":"minecraft:y_clamped_gradient","from_y":0,"to_y":2,"from_value":0.0,"to_value":1.0},
		"points":[
			{"location":0.0,"value":0.0,"derivative":0.0},
			{"location":1.0,"value":1.0,"derivative":0.0}
		]}}`
	sp3 := parseInline(t, j3)
	_ = sp2
	if v := sp3.Compute(Context{Y: 1}); !approx(v, 0.5) {
		t.Errorf("smoothstep spline(coord=0.5)=%v want 0.5", v)
	}
}

// Test 4: Parse resolves a string ref via the registry (dedup), and parses the real
// embedded overworld/offset.json without error.
func TestParseResolvesStringRef(t *testing.T) {
	b := newTestBinder(42)
	r := NewRegistry(DataSourceFunc(data.DensityFunction), b)

	// Resolve the same ref twice -> the SAME cached instance (HolderHolder dedup).
	f1, err := r.Resolve("minecraft:overworld/continents")
	if err != nil {
		t.Fatalf("resolve continents: %v", err)
	}
	f2, err := r.Resolve("minecraft:overworld/continents")
	if err != nil {
		t.Fatalf("resolve continents 2: %v", err)
	}
	if f1 != f2 {
		t.Errorf("dedup failed: Resolve returned distinct instances for the same ref")
	}

	// Parse the real embedded overworld/offset.json (spline-heavy) without error.
	if _, err := r.Resolve("minecraft:overworld/offset"); err != nil {
		t.Fatalf("parse overworld/offset.json: %v", err)
	}
}

// Test 5: Parse the real embedded cave functions without error (the full-parity gate).
func TestParseCaveFunctions(t *testing.T) {
	b := newTestBinder(7)
	r := NewRegistry(DataSourceFunc(data.DensityFunction), b)
	caves := []string{
		"minecraft:overworld/caves/entrances",
		"minecraft:overworld/caves/noodle",
		"minecraft:overworld/caves/pillars",
		"minecraft:overworld/caves/spaghetti_2d",
	}
	for _, id := range caves {
		fn, err := r.Resolve(id)
		if err != nil {
			t.Fatalf("parse cave function %s: %v", id, err)
		}
		// And it evaluates to a finite value (caves are in the graph, evaluable).
		v := fn.Compute(Context{X: 100, Y: 30, Z: 100})
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("cave %s computed non-finite %v", id, v)
		}
	}
}

// Test 6: an interpolated node is preserved as a Marker (not flattened).
func TestInterpolatedMarkerPreserved(t *testing.T) {
	fn := parseInline(t, `{"type":"minecraft:interpolated","argument":5.0}`)
	mk, ok := fn.(Marked)
	if !ok {
		t.Fatalf("interpolated node is not a Marked (got %T) — Pitfall 3 violated", fn)
	}
	if mk.Kind() != MarkerInterpolated {
		t.Errorf("marker kind = %v want MarkerInterpolated", mk.Kind())
	}
	if v := mk.Wrapped().Compute(Context{}); !approx(v, 5) {
		t.Errorf("interpolated wrapped value = %v want 5", v)
	}
	// And it still computes through to the wrapped value.
	if v := fn.Compute(Context{}); !approx(v, 5) {
		t.Errorf("interpolated.Compute = %v want 5", v)
	}
}

// Test 7: Parse on an unsupported type errors clearly naming it (T-9-07).
func TestUnsupportedNodeErrors(t *testing.T) {
	r := NewRegistry(nil, nil)
	_, err := r.Parse(json.RawMessage(`{"type":"minecraft:end_islands"}`))
	if err == nil {
		t.Fatalf("expected error for unsupported node type end_islands, got nil")
	}
	if !contains(err.Error(), "end_islands") || !contains(err.Error(), "unsupported") {
		t.Errorf("error %q should name the unsupported type and say 'unsupported'", err.Error())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
