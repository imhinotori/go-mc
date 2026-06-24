package synth

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// Golden samples below are derived by tracing the EXACT math read from the
// 26.2 jar bytecode (javap -c) for ImprovedNoise/PerlinNoise/NormalNoise/
// BlendedNoise, seeded from the bit-exact Tier-A Xoroshiro (random.go, itself
// golden-tested). They are exact-vanilla-DERIVABLE goldens (the noise math is
// fully deterministic given the seeded gradient tables), locked so any drift in
// the port surfaces immediately. Where a value is statistical (mean/range), it
// is documented as a property test rather than a single golden.

const eps = 1e-12

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Fatalf("%s = %.17g, want %.17g (diff %.3g)", name, got, want, got-want)
	}
}

func TestImprovedNoise(t *testing.T) {
	im := NewImprovedNoise(levelgen.NewXoroshiro(0))
	// Offsets are nextDouble()*256 from the seeded source — exact goldens.
	approx(t, "xo", im.OffsetX(), 42.174385605015516)
	approx(t, "yo", im.OffsetY(), 204.73490662467498)
	approx(t, "zo", im.OffsetZ(), 64.306224355231024)

	approx(t, "noise(0,0,0)", im.Noise(0, 0, 0), 0.2848659343182478)
	approx(t, "noise(1.5,2.5,3.5)", im.Noise(1.5, 2.5, 3.5), -0.016983226652707795)
	approx(t, "noise(10.1,-5.2,7.3)", im.Noise(10.1, -5.2, 7.3), 0.50169286363585408)

	// Determinism: two constructions from the same seed agree.
	a := NewImprovedNoise(levelgen.NewXoroshiro(99))
	b := NewImprovedNoise(levelgen.NewXoroshiro(99))
	for _, c := range [][3]float64{{0, 0, 0}, {3.3, 4.4, 5.5}, {-12.1, 8.8, 0.2}} {
		if x, y := a.Noise(c[0], c[1], c[2]), b.Noise(c[0], c[1], c[2]); x != y {
			t.Fatalf("ImprovedNoise determinism at %v: %v != %v", c, x, y)
		}
	}
	// A single-octave improved-Perlin sample is bounded by ~1 (gradient*offset).
	if math.Abs(im.Noise(123.4, -56.7, 89.0)) > 1.5 {
		t.Fatalf("ImprovedNoise out of expected bound")
	}
}

func TestPerlinNoiseOctaves(t *testing.T) {
	// firstOctave -6, amplitudes [1,1,1,0,0,1,1] (5 active of 7 octaves).
	amps := []float64{1, 1, 1, 0, 0, 1, 1}
	pn := NewPerlinNoise(levelgen.NewXoroshiro(0), -6, amps)
	approx(t, "perlin maxValue", pn.MaxValue(), 1.811023622047244)
	approx(t, "perlin getValue(1,2,3)", pn.GetValue(1, 2, 3), -0.019700907628429735)
	approx(t, "perlin getValue(100.5,-30.2,55.7)", pn.GetValue(100.5, -30.2, 55.7), 0.24213787746311882)

	// Negative firstOctave (temperature-like): -10, [1.5,0,1,0,0,0].
	tn := NewPerlinNoise(levelgen.NewXoroshiro(0), -10, []float64{1.5, 0, 1, 0, 0, 0})
	approx(t, "perlin(fo=-10) maxValue", tn.MaxValue(), 1.7777777777777777)
	approx(t, "perlin(fo=-10) getValue(7,7,7)", tn.GetValue(7, 7, 7), -0.11657199383468747)

	// maxValue must bound every sample (the octave-formula sanity, Pitfall 2).
	for _, c := range [][3]float64{{0, 0, 0}, {1000, -500, 250}, {-7.7, 8.8, -9.9}} {
		if v := math.Abs(pn.GetValue(c[0], c[1], c[2])); v > pn.MaxValue()+eps {
			t.Fatalf("perlin |getValue%v|=%v exceeds maxValue %v", c, v, pn.MaxValue())
		}
	}

	// Determinism across two constructions.
	p1 := NewPerlinNoise(levelgen.NewXoroshiro(7), -8, []float64{1, 1, 1, 1})
	p2 := NewPerlinNoise(levelgen.NewXoroshiro(7), -8, []float64{1, 1, 1, 1})
	if p1.GetValue(5, 6, 7) != p2.GetValue(5, 6, 7) {
		t.Fatalf("perlin determinism mismatch")
	}
}

func TestNormalNoise(t *testing.T) {
	// firstOctave -7, amplitudes [1,1] — exact goldens for the seeded math.
	nn := NewNormalNoise(levelgen.NewXoroshiro(0), -7, []float64{1, 1})
	approx(t, "normal maxValue", nn.MaxValue(), 4.4444444444444438)
	approx(t, "normal getValue(0,0,0)", nn.GetValue(0, 0, 0), -0.068396257085230935)
	approx(t, "normal getValue(12.3,45.6,78.9)", nn.GetValue(12.3, 45.6, 78.9), 0.17930576055837893)

	// Temperature-style params from a different seed.
	nt := NewNormalNoise(levelgen.NewXoroshiro(12345), -10, []float64{1.5, 0, 1, 0, 0, 0})
	approx(t, "normal(temp) maxValue", nt.MaxValue(), 4.4444444444444446)
	approx(t, "normal(temp) getValue(64,0,64)", nt.GetValue(64, 0, 64), 0.6772443782153541)

	// Property: over a grid the mean is near 0 and all samples lie within maxValue
	// (the NormalNoise normalization — INPUT_FACTOR/expectedDeviation/valueFactor).
	var sum, mn, mx float64
	mn, mx = math.Inf(1), math.Inf(-1)
	n := 0
	for x := 0; x < 40; x++ {
		for z := 0; z < 40; z++ {
			v := nn.GetValue(float64(x)*3.1, 0, float64(z)*3.1)
			sum += v
			n++
			mn, mx = math.Min(mn, v), math.Max(mx, v)
		}
	}
	if mean := sum / float64(n); math.Abs(mean) > 0.15 {
		t.Fatalf("normal sample mean %.4f not near 0", mean)
	}
	if mn < -nn.MaxValue() || mx > nn.MaxValue() {
		t.Fatalf("normal samples [%.4f,%.4f] exceed maxValue %.4f", mn, mx, nn.MaxValue())
	}

	// Determinism across two constructions.
	a := NewNormalNoise(levelgen.NewXoroshiro(55), -9, []float64{1, 1, 1})
	b := NewNormalNoise(levelgen.NewXoroshiro(55), -9, []float64{1, 1, 1})
	if a.GetValue(1.1, 2.2, 3.3) != b.GetValue(1.1, 2.2, 3.3) {
		t.Fatalf("normal determinism mismatch")
	}
}

func TestNormalNoiseFromPositionalFactory(t *testing.T) {
	// The density graph (Wave 2) seeds each named noise via the positional
	// factory's FromHashOf. Same name+seed -> identical noise (Pitfall 7).
	seedFac := func() levelgen.PositionalRandomFactory {
		return levelgen.NewXoroshiro(0xCAFE).ForkPositional()
	}
	a := NewNormalNoise(seedFac().FromHashOf("minecraft:temperature"), -10, []float64{1.5, 0, 1, 0, 0, 0})
	b := NewNormalNoise(seedFac().FromHashOf("minecraft:temperature"), -10, []float64{1.5, 0, 1, 0, 0, 0})
	for _, c := range [][3]float64{{0, 0, 0}, {64, 0, 64}, {-128, 32, 256}} {
		if x, y := a.GetValue(c[0], c[1], c[2]), b.GetValue(c[0], c[1], c[2]); x != y {
			t.Fatalf("normal-from-factory determinism at %v: %v != %v", c, x, y)
		}
	}
	// A different noise name diverges.
	c := NewNormalNoise(seedFac().FromHashOf("minecraft:vegetation"), -10, []float64{1.5, 0, 1, 0, 0, 0})
	d := NewNormalNoise(seedFac().FromHashOf("minecraft:temperature"), -10, []float64{1.5, 0, 1, 0, 0, 0})
	if c.GetValue(64, 0, 64) == d.GetValue(64, 0, 64) {
		t.Fatalf("distinct noise names produced identical sample")
	}
}

func TestBlendedNoise(t *testing.T) {
	// base_3d_noise scales: xz_scale 0.25, y_scale 0.125, xz_factor 80,
	// y_factor 160, smear_scale_multiplier 8. Exact goldens.
	bn := NewBlendedNoise(levelgen.NewXoroshiro(0), 0.25, 0.125, 80, 160, 8)
	approx(t, "blended compute(0,0,0)", bn.Compute(0, 0, 0), 0.052837270865629352)
	approx(t, "blended compute(16,64,16)", bn.Compute(16, 64, 16), -0.13770376144966415)
	approx(t, "blended compute(-100,32,200)", bn.Compute(-100, 32, 200), 0.40465381632917613)

	// Determinism across two constructions.
	a := NewBlendedNoise(levelgen.NewXoroshiro(3), 0.25, 0.125, 80, 160, 8)
	b := NewBlendedNoise(levelgen.NewXoroshiro(3), 0.25, 0.125, 80, 160, 8)
	if a.Compute(5, 10, 15) != b.Compute(5, 10, 15) {
		t.Fatalf("blended determinism mismatch")
	}

	// Output is bounded (the /128 final scale + clampedLerp keep it small).
	for _, c := range [][3]int{{0, 0, 0}, {200, 100, -300}, {-1000, 50, 1000}} {
		if v := math.Abs(bn.Compute(c[0], c[1], c[2])); v > 5 {
			t.Fatalf("blended |compute%v|=%v unexpectedly large", c, v)
		}
	}
}
