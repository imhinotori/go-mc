package synth

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// The golden values below are PINNED from the real 26.2 jar: a Java probe
// (net.minecraft.world.level.levelgen.synth.SimplexNoise / PerlinSimplexNoise,
// run against temp/cache/26.2-inner.jar + fastutil/guava/joml) constructed the
// exact Biome noise instances (WorldgenRandom(LegacyRandomSource(seed))) and
// printed getValue outputs with %.17g. They lock the port bit-exactly.

const simplexEps = 1e-12

func approxS(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > simplexEps {
		t.Fatalf("%s = %.17g, want %.17g (diff %.3g)", name, got, want, got-want)
	}
}

// newBiomeSimplex builds a SimplexNoise seeded exactly like Biome's TEMPERATURE_NOISE
// zero octave: WorldgenRandom(LegacyRandomSource(1234)).
func newBiomeSimplex(seed int64) *SimplexNoise {
	return NewSimplexNoise(levelgen.NewWorldgenRandom(seed))
}

func TestSimplexNoiseSeeding(t *testing.T) {
	sn := newBiomeSimplex(1234)
	// Offsets = nextDouble()*256 from the seeded legacy source — exact goldens.
	approxS(t, "xo", sn.OffsetX(), 165.52503303447696)
	approxS(t, "yo", sn.OffsetY(), 243.54757399536433)
	approxS(t, "zo", sn.OffsetZ(), 219.54264571054935)
}

func TestSimplexNoise2D(t *testing.T) {
	sn := newBiomeSimplex(1234)
	approxS(t, "sn2d(0,0)", sn.GetValue2D(0, 0), 0.0)
	approxS(t, "sn2d(1.5,2.5)", sn.GetValue2D(1.5, 2.5), 0.090054382117035830)
	approxS(t, "sn2d(10.1,-5.2)", sn.GetValue2D(10.1, -5.2), 0.67526567716882720)
}

func TestSimplexNoise3D(t *testing.T) {
	sn := newBiomeSimplex(1234)
	approxS(t, "sn3d(0.5,0.5,0.5)", sn.GetValue3D(0.5, 0.5, 0.5), 0.0)
	approxS(t, "sn3d(10.1,-5.2,7.3)", sn.GetValue3D(10.1, -5.2, 7.3), 0.33635764437860094)
}

func TestSimplexNoiseDeterminism(t *testing.T) {
	a := newBiomeSimplex(99)
	b := newBiomeSimplex(99)
	for _, c := range [][3]float64{{0, 0, 0}, {3.3, 4.4, 5.5}, {-12.1, 8.8, 0.2}} {
		if x, y := a.GetValue3D(c[0], c[1], c[2]), b.GetValue3D(c[0], c[1], c[2]); x != y {
			t.Fatalf("SimplexNoise 3D determinism at %v: %v != %v", c, x, y)
		}
		if x, y := a.GetValue2D(c[0], c[1]), b.GetValue2D(c[0], c[1]); x != y {
			t.Fatalf("SimplexNoise 2D determinism at %v: %v != %v", c[:2], x, y)
		}
	}
}

func TestPerlinSimplexTemperatureNoise(t *testing.T) {
	// TEMPERATURE_NOISE = PerlinSimplexNoise(WorldgenRandom(LegacyRandomSource(1234)), of(0)).
	tn := NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(1234), []int{0})
	approxS(t, "TN(0,0,false)", tn.GetValue(0, 0, false), 0.0)
	approxS(t, "TN(12.5,-7.25,false)", tn.GetValue(12.5, -7.25, false), 0.31056928987772464)
	approxS(t, "TN(100,200,true)", tn.GetValue(100.0, 200.0, true), -0.13544434607885433)
}

func TestPerlinSimplexFrozenNoise(t *testing.T) {
	// FROZEN_TEMPERATURE_NOISE = PerlinSimplexNoise(WorldgenRandom(LegacyRandomSource(3456)), of(-2,-1,0)).
	fn := NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(3456), []int{-2, -1, 0})
	approxS(t, "FN(0,0,false)", fn.GetValue(0, 0, false), 0.0)
	approxS(t, "FN(5,10,false)", fn.GetValue(5.0, 10.0, false), -0.16632234644061125)
	approxS(t, "FN(3.15,-9.85,true)", fn.GetValue(3.15, -9.85, true), 0.024909956673906330)
}

func TestPerlinSimplexBiomeInfoNoise(t *testing.T) {
	// BIOME_INFO_NOISE = PerlinSimplexNoise(WorldgenRandom(LegacyRandomSource(2345)), of(0)).
	bn := NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(2345), []int{0})
	approxS(t, "BN(0,0,false)", bn.GetValue(0, 0, false), 0.0)
	approxS(t, "BN(6.4,-3.2,false)", bn.GetValue(6.4, -3.2, false), 0.15870631048007225)
}

func TestPerlinSimplexOctaveLayout(t *testing.T) {
	// FROZEN uses octaves {-2,-1,0}: lowFreq=2, highFreq=0, octaves=3. All three
	// noiseLevels must be active (none nil), because contains(0/-1/-2) are all true.
	fn := NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(3456), []int{-2, -1, 0})
	if got := len(fn.noiseLevels); got != 3 {
		t.Fatalf("FROZEN octave count = %d, want 3", got)
	}
	for i, n := range fn.noiseLevels {
		if n == nil {
			t.Fatalf("FROZEN noiseLevels[%d] is nil, want active", i)
		}
	}
	// TEMPERATURE uses octaves {0}: single active level.
	tn := NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(1234), []int{0})
	if got := len(tn.noiseLevels); got != 1 || tn.noiseLevels[0] == nil {
		t.Fatalf("TEMPERATURE octave layout wrong: len=%d", len(tn.noiseLevels))
	}
	// Octave-set de-dup + sort: {0,-1,-2,-1} must build the same as {-2,-1,0}.
	dup := NewPerlinSimplexNoise(levelgen.NewWorldgenRandom(3456), []int{0, -1, -2, -1})
	for _, c := range [][2]float64{{0, 0}, {5, 10}, {3.15, -9.85}} {
		if x, y := dup.GetValue(c[0], c[1], false), fn.GetValue(c[0], c[1], false); x != y {
			t.Fatalf("octave-set de-dup mismatch at %v: %v != %v", c, x, y)
		}
	}
}
