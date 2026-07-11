package router

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// legacy_random_source_test.go - regression lock + analytical golden for the
// useLegacyRandomSource branch of RandomState (W-C1).
//
// THE BUG: NewRandomState used to hardcode Xoroshiro(seed).forkPositional() and ignore
// NoiseGeneratorSettings.useLegacyRandomSource. The nether (and the end, and any
// pre-1.18-style settings) carry legacy_random_source:true and MUST seed every noise /
// aquifer / ore / surface random from a LegacyRandomSource-backed positional factory
// (WorldgenRandom.Algorithm.LEGACY), not Xoroshiro. Seeding from the wrong algorithm
// draws a different bit-stream, so the whole density graph - and thus the terrain -
// diverges from vanilla.
//
// THE FIX: NewRandomState now branches on the flag, exactly mirroring
// NoiseGeneratorSettings.getRandomSource() (useLegacyRandomSource ? LEGACY : XOROSHIRO)
// feeding RandomState.<init> (getRandomSource().newInstance(seed).forkPositional()).
//
// HOW THE GOLDEN WAS OBTAINED - analytical, from the ported algorithm (no jar runtime
// needed): vanilla is deterministic, so the value a legacy-seeded NormalNoise produces
// is fully determined by the already-verified ported primitives in package levelgen
// (LegacyRandomSource / legacyPositionalFactory, each checked constant-for-constant
// against the 26.2 bytecode) plus the ported NormalNoise. We reconstruct the vanilla
// wiring INDEPENDENTLY here (base=NewLegacyRandomSource(seed); factory=ForkPositional();
// rs=factory.FromHashOf("minecraft:temperature"); NewNormalNoise(rs,firstOctave,amps))
// and assert the RandomState built by the FIXED NewRandomState(seed, true) produces the
// byte-identical NormalNoise output. If NewRandomState still used Xoroshiro, this
// independent legacy reconstruction would not match. The companion assertions lock in
// that legacy output != xoroshiro output for the same seed (the actual regression), so
// a future re-hardcode of either algorithm fails loudly.

// legacyGoldenNoiseID is a shared climate noise the router seeds via
// factory.fromHashOf(id); temperature is present for both the overworld and nether
// routers, making it a stable probe for the seeding algorithm.
const legacyGoldenNoiseID = "minecraft:temperature"

// loadNoiseParams reads the embedded DATA noise params (firstOctave/amplitudes) the
// same way RandomState.NormalNoise does, so the independent reconstruction uses the
// identical inputs.
func loadNoiseParams(t *testing.T, id string) (int, []float64) {
	t.Helper()
	raw, err := data.Noise(id)
	if err != nil {
		t.Fatalf("data.Noise(%q): %v", id, err)
	}
	var p struct {
		FirstOctave int       `json:"firstOctave"`
		Amplitudes  []float64 `json:"amplitudes"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("parse noise params %q: %v", id, err)
	}
	return p.FirstOctave, p.Amplitudes
}

// TestNewRandomStateBranchesOnLegacyFlag is the core routing proof: for the SAME seed,
// the legacy branch and the xoroshiro branch must seed DIFFERENT noise (different
// algorithm => different bit-stream). Before the fix both calls returned Xoroshiro, so
// these would have been equal.
func TestNewRandomStateBranchesOnLegacyFlag(t *testing.T) {
	const seed = int64(0x0123456789ABCDEF)

	legacyRS := NewRandomState(seed, true)
	xoroRS := NewRandomState(seed, false)

	legacyNoise, err := legacyRS.NormalNoise(legacyGoldenNoiseID)
	if err != nil {
		t.Fatalf("legacy NormalNoise: %v", err)
	}
	xoroNoise, err := xoroRS.NormalNoise(legacyGoldenNoiseID)
	if err != nil {
		t.Fatalf("xoroshiro NormalNoise: %v", err)
	}

	pts := [][3]float64{{0, 0, 0}, {123.5, -40, 77.25}, {-1000, 60, 2000}, {17, 3, -9}}
	allEqual := true
	for _, p := range pts {
		if legacyNoise.GetValue(p[0], p[1], p[2]) != xoroNoise.GetValue(p[0], p[1], p[2]) {
			allEqual = false
			break
		}
	}
	if allEqual {
		t.Fatalf("legacy and xoroshiro RandomState produced identical noise at every sampled point - "+
			"NewRandomState is not branching on useLegacyRandomSource (seed=%d)", seed)
	}
}

// TestLegacyRandomStateMatchesIndependentReconstruction is the analytical golden: the
// FIXED legacy RandomState must reproduce a noise wired independently from the ported
// levelgen legacy primitives (proving the router genuinely uses LegacyRandomSource, and
// pinning the exact numeric output so any drift in the primitives is caught).
func TestLegacyRandomStateMatchesIndependentReconstruction(t *testing.T) {
	const seed = int64(0x0123456789ABCDEF)

	firstOctave, amps := loadNoiseParams(t, legacyGoldenNoiseID)
	base := levelgen.NewLegacyRandomSource(seed) // Algorithm.LEGACY.newInstance(seed)
	factory := base.ForkPositional()             // RandomState.random field
	rs := factory.FromHashOf(legacyGoldenNoiseID)
	want := synth.NewNormalNoise(rs, firstOctave, amps)

	got, err := NewRandomState(seed, true).NormalNoise(legacyGoldenNoiseID)
	if err != nil {
		t.Fatalf("legacy NormalNoise via NewRandomState: %v", err)
	}

	for _, p := range [][3]float64{{0, 0, 0}, {123.5, -40, 77.25}, {-1000, 60, 2000}, {17, 3, -9}} {
		w := want.GetValue(p[0], p[1], p[2])
		g := got.GetValue(p[0], p[1], p[2])
		if w != g {
			t.Fatalf("legacy RandomState noise mismatch at %v: NewRandomState=%v want(independent legacy)=%v - "+
				"the router is not seeding from LegacyRandomSource", p, g, w)
		}
	}

	xbase := levelgen.NewXoroshiro(seed)
	xrs := xbase.ForkPositional().FromHashOf(legacyGoldenNoiseID)
	xwant := synth.NewNormalNoise(xrs, firstOctave, amps)
	differs := false
	for _, p := range [][3]float64{{0, 0, 0}, {123.5, -40, 77.25}, {-1000, 60, 2000}, {17, 3, -9}} {
		if xwant.GetValue(p[0], p[1], p[2]) != want.GetValue(p[0], p[1], p[2]) {
			differs = true
			break
		}
	}
	if !differs {
		t.Fatalf("legacy and xoroshiro noise identical for seed=%d - golden is not discriminating the algorithms", seed)
	}
}

// TestNetherRouterUsesLegacyRandomSource proves the end-to-end wiring: the nether's
// parsed NoiseGeneratorSettings carries legacy_random_source:true, and the assembled
// nether Router's RandomState therefore matches the independent legacy reconstruction
// (NOT the xoroshiro one). This is the production path the bug lived on.
func TestNetherRouterUsesLegacyRandomSource(t *testing.T) {
	const seed = int64(424242)

	r, err := NewRouterFor(seed, "minecraft:nether")
	if err != nil {
		t.Fatalf("NewRouterFor(nether): %v", err)
	}
	if !r.Settings.LegacyRandomSource {
		t.Fatalf("nether NoiseGeneratorSettings.LegacyRandomSource = false, want true " +
			"(data/noise_settings/nether.json must carry legacy_random_source:true)")
	}

	firstOctave, amps := loadNoiseParams(t, legacyGoldenNoiseID)

	legacyRS := levelgen.NewLegacyRandomSource(seed).ForkPositional().FromHashOf(legacyGoldenNoiseID)
	wantLegacy := synth.NewNormalNoise(legacyRS, firstOctave, amps)
	xoroRS := levelgen.NewXoroshiro(seed).ForkPositional().FromHashOf(legacyGoldenNoiseID)
	wrongXoro := synth.NewNormalNoise(xoroRS, firstOctave, amps)

	got, err := r.Random.NormalNoise(legacyGoldenNoiseID)
	if err != nil {
		t.Fatalf("nether router NormalNoise: %v", err)
	}

	matchesLegacy, matchesXoro := true, true
	for _, p := range [][3]float64{{0, 0, 0}, {50, 40, -30}, {-777, 8, 1234}} {
		g := got.GetValue(p[0], p[1], p[2])
		if g != wantLegacy.GetValue(p[0], p[1], p[2]) {
			matchesLegacy = false
		}
		if g != wrongXoro.GetValue(p[0], p[1], p[2]) {
			matchesXoro = false
		}
	}
	if !matchesLegacy {
		t.Fatalf("nether router RandomState does NOT match the legacy reconstruction - the legacy flag is not honored")
	}
	if matchesXoro {
		t.Fatalf("nether router RandomState matches the XOROSHIRO reconstruction - the legacy flag is being ignored (the original bug)")
	}
}

// TestBlendedNoiseLegacyDimUsesLegacyRandomSource value-locks the wrapNew BlendedNoise seeding
// for a LEGACY dimension. RandomState$1NoiseWiringHelper.wrapNew branches on val$useLegacyInit:
// a legacy dim (nether/end) seeds the old_blended_noise from new LegacyRandomSource(seed) (=
// newLegacyInstance(0L)), NOT the positional random.fromHashOf("minecraft:terrain"). The prior
// port unconditionally used fromHashOf("terrain"), so nether/end terrain diverged from vanilla.
//
// The golden is analytical: BlendedNoise is deterministic, so a legacy-seeded BlendedNoise is
// fully determined by the ported LegacyRandomSource + BlendedNoise primitives. We reconstruct the
// vanilla wiring INDEPENDENTLY (rs = NewLegacyRandomSource(seed); NewBlendedNoise(rs, scales...))
// and assert the FIXED RandomState(seed, true).BlendedNoise matches byte-identically, and that the
// XOROSHIRO fromHashOf("terrain") path (the old behavior) does NOT match -- the actual regression.
func TestBlendedNoiseLegacyDimUsesLegacyRandomSource(t *testing.T) {
	const seed = int64(0x0123456789ABCDEF)
	// Representative old_blended_noise scales (overworld base_3d_noise fields; the seeding path is
	// scale-independent -- the RandomSource is consumed before any scale is applied).
	const xzScale, yScale, xzFactor, yFactor, smear = 0.25, 0.125, 80.0, 160.0, 8.0

	// Independent legacy reconstruction: wrapNew useLegacyInit branch = newLegacyInstance(0L).
	wantRS := levelgen.NewLegacyRandomSource(seed + 0)
	want := synth.NewBlendedNoise(wantRS, xzScale, yScale, xzFactor, yFactor, smear)

	got, err := NewRandomState(seed, true).BlendedNoise(xzScale, yScale, xzFactor, yFactor, smear)
	if err != nil {
		t.Fatalf("legacy BlendedNoise via NewRandomState: %v", err)
	}

	// The XOROSHIRO fromHashOf("minecraft:terrain") path = the OLD (buggy) seeding for any dim.
	xoroRS := levelgen.NewXoroshiro(seed).ForkPositional().FromHashOf("minecraft:terrain")
	wrongXoro := synth.NewBlendedNoise(xoroRS, xzScale, yScale, xzFactor, yFactor, smear)

	pts := [][3]int{{0, 0, 0}, {123, -40, 77}, {-1000, 60, 2000}, {17, 3, -9}}
	matchesLegacy, matchesXoro := true, true
	for _, q := range pts {
		g := got.Compute(q[0], q[1], q[2])
		if g != want.Compute(q[0], q[1], q[2]) {
			matchesLegacy = false
		}
		if g != wrongXoro.Compute(q[0], q[1], q[2]) {
			matchesXoro = false
		}
	}
	if !matchesLegacy {
		t.Fatalf("legacy BlendedNoise does NOT match the LegacyRandomSource(seed) reconstruction - "+
			"wrapNew's useLegacyInit branch is not honored (seed=%d)", seed)
	}
	if matchesXoro {
		t.Fatalf("legacy BlendedNoise matches the xoroshiro fromHashOf(\"terrain\") path - "+
			"the legacy dimension is still seeded the old (buggy) way (seed=%d)", seed)
	}
}

// TestOverworldBlendedNoiseUsesTerrainHash guards the OTHER branch: a MODERN dimension
// (legacy_random_source:false) must still seed the BlendedNoise from
// random.fromHashOf("minecraft:terrain"), NOT LegacyRandomSource. This is the overworld path and
// must stay byte-identical (the legacy fix must not regress it).
func TestOverworldBlendedNoiseUsesTerrainHash(t *testing.T) {
	const seed = int64(0x0123456789ABCDEF)
	const xzScale, yScale, xzFactor, yFactor, smear = 0.25, 0.125, 80.0, 160.0, 8.0

	wantRS := levelgen.NewXoroshiro(seed).ForkPositional().FromHashOf("minecraft:terrain")
	want := synth.NewBlendedNoise(wantRS, xzScale, yScale, xzFactor, yFactor, smear)

	got, err := NewRandomState(seed, false).BlendedNoise(xzScale, yScale, xzFactor, yFactor, smear)
	if err != nil {
		t.Fatalf("overworld BlendedNoise via NewRandomState: %v", err)
	}
	for _, q := range [][3]int{{0, 0, 0}, {123, -40, 77}, {-1000, 60, 2000}} {
		if got.Compute(q[0], q[1], q[2]) != want.Compute(q[0], q[1], q[2]) {
			t.Fatalf("overworld BlendedNoise mismatch at %v - the modern fromHashOf(terrain) path regressed", q)
		}
	}
}

// TestNetherBiomeNoisesUseLegacyNetherBiomePath value-locks visitNoise's special-casing of the two
// legacy nether biome climate noises. RandomState$1NoiseWiringHelper.visitNoise wires
// Noises.TEMPERATURE_NETHER ("nether/temperature") as createLegacyNetherBiome(newLegacyInstance(0L))
// and Noises.VEGETATION_NETHER ("nether/vegetation") as createLegacyNetherBiome(newLegacyInstance(1L)):
// a raw LegacyRandomSource(seed+0|+1) built via the LEGACY nether biome Perlin path (NOT the modern
// positional fromHashOf(id) path). The prior port routed them through the generic positional path,
// so nether biome placement diverged from vanilla.
func TestNetherBiomeNoisesUseLegacyNetherBiomePath(t *testing.T) {
	const seed = int64(424242)
	rs := NewRandomState(seed, true) // nether is a legacy dim

	cases := []struct {
		id   string
		salt int64
	}{
		{"minecraft:nether/temperature", 0},
		{"minecraft:nether/vegetation", 1},
	}
	for _, c := range cases {
		firstOctave, amps := loadNoiseParams(t, c.id)

		// Independent reconstruction: createLegacyNetherBiome(LegacyRandomSource(seed+salt), params).
		wantRng := levelgen.NewLegacyRandomSource(seed + c.salt)
		want := synth.NewNormalNoiseLegacyNetherBiome(wantRng, firstOctave, amps)

		// The WRONG (old) path: the generic positional fromHashOf(id) via NewNormalNoise.
		wrongRng := levelgen.NewLegacyRandomSource(seed).ForkPositional().FromHashOf(c.id)
		wrong := synth.NewNormalNoise(wrongRng, firstOctave, amps)

		got, err := rs.NormalNoise(c.id)
		if err != nil {
			t.Fatalf("NormalNoise(%q): %v", c.id, err)
		}

		matchesWant, matchesWrong := true, true
		for _, q := range [][3]float64{{0, 0, 0}, {50, 40, -30}, {-777, 8, 1234}, {12.5, -3, 9.25}} {
			g := got.GetValue(q[0], q[1], q[2])
			if g != want.GetValue(q[0], q[1], q[2]) {
				matchesWant = false
			}
			if g != wrong.GetValue(q[0], q[1], q[2]) {
				matchesWrong = false
			}
		}
		if !matchesWant {
			t.Fatalf("%s: does NOT match createLegacyNetherBiome(LegacyRandomSource(seed+%d)) - "+
				"visitNoise's legacy nether-biome path is not wired", c.id, c.salt)
		}
		if matchesWrong {
			t.Fatalf("%s: matches the generic positional fromHashOf path - the legacy nether-biome "+
				"special-case is not applied (the original bug)", c.id)
		}
	}

	// The +0 and +1 salts must produce DIFFERENT noise (temperature != vegetation seeding).
	temp, _ := rs.NormalNoise("minecraft:nether/temperature")
	veg, _ := rs.NormalNoise("minecraft:nether/vegetation")
	if temp.GetValue(1, 2, 3) == veg.GetValue(1, 2, 3) && temp.GetValue(-9, 4, 7) == veg.GetValue(-9, 4, 7) {
		t.Fatalf("nether/temperature and nether/vegetation seeded identically - the +0/+1 salt is not applied")
	}
}
