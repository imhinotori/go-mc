package router

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen/density"
)

// Test 1: NewRouter parses overworld.json into the settings struct.
func TestParseNoiseSettings(t *testing.T) {
	r, err := NewRouter(12345)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	s := r.Settings
	if s.SeaLevel != 63 {
		t.Errorf("sea_level=%d want 63", s.SeaLevel)
	}
	if s.DefaultBlock != "minecraft:stone" {
		t.Errorf("default_block=%q want minecraft:stone", s.DefaultBlock)
	}
	if s.DefaultFluid != "minecraft:water" {
		t.Errorf("default_fluid=%q want minecraft:water", s.DefaultFluid)
	}
	if s.Noise.Height != 384 || s.Noise.MinY != -64 || s.Noise.SizeHorizontal != 1 || s.Noise.SizeVertical != 2 {
		t.Errorf("noise dims = %+v want {384,-64,1,2}", s.Noise)
	}
	if s.LegacyRandomSource {
		t.Errorf("legacy_random_source = true, want false")
	}
	if !s.AquifersEnabled {
		t.Errorf("aquifers_enabled = false, want true")
	}
	if !s.OreVeinsEnabled {
		t.Errorf("ore_veins_enabled = false, want true")
	}
}

// Test 2: RandomState seeds noises deterministically (Pitfall 1 + 7).
func TestRandomStateSeedsNoises(t *testing.T) {
	const seed = int64(987654321)
	// false = Xoroshiro (the overworld algorithm); this test asserts noise-seeding
	// determinism, unrelated to the legacy branch.
	a := NewRandomState(seed, false)
	b := NewRandomState(seed, false)
	na, err := a.NormalNoise("minecraft:temperature")
	if err != nil {
		t.Fatalf("seed temperature noise (a): %v", err)
	}
	nb, err := b.NormalNoise("minecraft:temperature")
	if err != nil {
		t.Fatalf("seed temperature noise (b): %v", err)
	}
	for _, p := range [][3]float64{{0, 0, 0}, {123.5, -40, 77.25}, {-1000, 60, 2000}} {
		va := na.GetValue(p[0], p[1], p[2])
		vb := nb.GetValue(p[0], p[1], p[2])
		if va != vb {
			t.Errorf("same seed produced different temperature samples at %v: %v != %v", p, va, vb)
		}
	}
	// A different noise id must produce a different sample (the name is carried into
	// the seed — Pitfall 1).
	other, _ := a.NormalNoise("minecraft:vegetation")
	if other.GetValue(0, 0, 0) == na.GetValue(0, 0, 0) {
		t.Errorf("temperature and vegetation noises sampled identical at origin — name not carried into seed")
	}
}

// Test 3: NewRouter yields an evaluable, deterministic final_density with caves
// (negative density underground).
func TestFinalDensityEvaluable(t *testing.T) {
	const seed = int64(424242)
	r1, err := NewRouter(seed)
	if err != nil {
		t.Fatalf("NewRouter r1: %v", err)
	}
	r2, err := NewRouter(seed)
	if err != nil {
		t.Fatalf("NewRouter r2: %v", err)
	}
	fd1 := r1.NoiseRouter.FinalDensity
	fd2 := r2.NoiseRouter.FinalDensity

	// finite + deterministic across two NewRouter calls.
	for _, c := range []density.Context{{X: 0, Y: 0, Z: 0}, {X: 64, Y: 100, Z: 64}, {X: -200, Y: -40, Z: 350}} {
		v1 := fd1.Compute(c)
		v2 := fd2.Compute(c)
		if math.IsNaN(v1) || math.IsInf(v1, 0) {
			t.Errorf("final_density non-finite at %v: %v", c, v1)
		}
		if v1 != v2 {
			t.Errorf("final_density non-deterministic at %v: %v != %v", c, v1, v2)
		}
	}

	// Sign varies across y: deep underground is solid (positive), high up is air
	// (negative). Sample a vertical column and assert both signs appear.
	sawPos, sawNeg := false, false
	for y := -60; y <= 200; y += 4 {
		v := fd1.Compute(density.Context{X: 16, Y: y, Z: 16})
		if v > 0 {
			sawPos = true
		}
		if v < 0 {
			sawNeg = true
		}
	}
	if !sawPos || !sawNeg {
		t.Errorf("final_density column should span solid(+) and air(-): sawPos=%v sawNeg=%v", sawPos, sawNeg)
	}

	// Caves: there exist (x,y,z) UNDERGROUND (below the high air band) where
	// final_density is negative — the noise cave branches carve solid rock. Scan a
	// shallow-underground volume for at least one carved (negative) cell.
	sawCave := false
	for x := 0; x < 64 && !sawCave; x += 4 {
		for z := 0; z < 64 && !sawCave; z += 4 {
			for y := -40; y <= 40; y += 4 {
				if fd1.Compute(density.Context{X: x, Y: y, Z: z}) < 0 {
					sawCave = true
					break
				}
			}
		}
	}
	if !sawCave {
		t.Errorf("expected at least one negative-density (carved) cell underground — caves not contributing")
	}
}

// Test 4: NewRouter binds ALL 15 named noise_router functions — each evaluable +
// deterministic. Any unported node type fails HERE (T-9-07), not at runtime.
func TestParseFullGraphEndToEnd(t *testing.T) {
	const seed = int64(555)
	r1, err := NewRouter(seed)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	r2, err := NewRouter(seed)
	if err != nil {
		t.Fatalf("NewRouter (2): %v", err)
	}

	c := density.Context{X: 48, Y: 24, Z: -112}
	for _, name := range routerFunctionNames {
		f1, ok := r1.NoiseRouter.Function(name)
		if !ok || f1 == nil {
			t.Fatalf("router function %q not bound", name)
		}
		f2, _ := r2.NoiseRouter.Function(name)
		v1 := f1.Compute(c)
		v2 := f2.Compute(c)
		if math.IsNaN(v1) || math.IsInf(v1, 0) {
			t.Errorf("router function %q non-finite at %v: %v", name, c, v1)
		}
		if v1 != v2 {
			t.Errorf("router function %q non-deterministic at %v: %v != %v", name, c, v1, v2)
		}
	}
	if len(r1.NoiseRouter.byName) != 15 {
		t.Errorf("expected 15 bound router functions, got %d", len(r1.NoiseRouter.byName))
	}
}

// Test 5: preliminary_surface_level (the find_top_surface node) parses AND evaluates —
// it MUST parse (09-07's surface rules consume it).
func TestParsePreliminarySurfaceLevel(t *testing.T) {
	r, err := NewRouter(99)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	psl := r.NoiseRouter.PreliminarySurfaceLevel
	if psl == nil {
		t.Fatalf("preliminary_surface_level not bound")
	}
	// It evaluates to a finite surface Y within the world column bounds.
	for _, c := range []density.Context{{X: 0, Y: 0, Z: 0}, {X: 256, Y: 0, Z: -256}} {
		v := psl.Compute(c)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("preliminary_surface_level non-finite at %v: %v", c, v)
		}
		// The find_top_surface scan returns a Y clamped to >= lower_bound (-64).
		if v < -64 {
			t.Errorf("preliminary_surface_level=%v below world floor at %v", v, c)
		}
	}
}
