package feature

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// flowerPlainJSON is the real flower_plain to_place envelope (the overworld plains flower
// picker) verbatim from world/levelgen/data/configured_feature/flower_plain.json. Parsing this
// used to panic ("unported ... noise_threshold_provider") and crash terrain decoration.
const flowerPlainJSON = `{
  "type": "minecraft:noise_threshold_provider",
  "default_state": {"Name": "minecraft:dandelion"},
  "high_chance": 0.33333334,
  "high_states": [
    {"Name": "minecraft:poppy"}, {"Name": "minecraft:azure_bluet"},
    {"Name": "minecraft:oxeye_daisy"}, {"Name": "minecraft:cornflower"}
  ],
  "low_states": [
    {"Name": "minecraft:orange_tulip"}, {"Name": "minecraft:red_tulip"},
    {"Name": "minecraft:pink_tulip"}, {"Name": "minecraft:white_tulip"}
  ],
  "noise": {"amplitudes": [1.0], "firstOctave": 0},
  "scale": 0.005,
  "seed": 2345,
  "threshold": -0.8
}`

// TestNoiseThresholdProviderParses is the regression gate for the "Loading terrain" panic: the
// real flower_plain provider must parse without error (it used to hit the deferred default).
func TestNoiseThresholdProviderParses(t *testing.T) {
	p, err := ParseProvider(json.RawMessage(flowerPlainJSON))
	if err != nil {
		t.Fatalf("flower_plain noise_threshold_provider failed to parse: %v", err)
	}
	if _, ok := p.(NoiseThresholdProvider); !ok {
		t.Fatalf("parsed provider is %T, want NoiseThresholdProvider", p)
	}
}

// TestNoiseThresholdProviderDrawOrder pins the JAR-exact draw order of getState across all
// three branches by constructing a provider with a KNOWN-sign noise (via a huge/zero scale so
// the sampled value is deterministic per position) and an oracle-checked rng consumption.
//
// Branches (NoiseThresholdProvider.getState):
//
//	d < threshold        -> lowStates[nextInt(len)]           (1 int draw, no float draw)
//	d >= threshold, roll  -> nextFloat() < highChance ? highStates[nextInt(len)] : defaultState
func TestNoiseThresholdProviderDrawOrder(t *testing.T) {
	low := []block.StateID{stateOf(t, block.OrangeTulip{}), stateOf(t, block.RedTulip{})}
	high := []block.StateID{stateOf(t, block.Poppy{}), stateOf(t, block.Cornflower{})}
	def := stateOf(t, block.Dandelion{})

	// A noise seeded like the data; we do not assert WHICH sign, only that the draw order for
	// whichever branch is taken matches an oracle replaying the same primitives in the same order.
	rngSeed := int64(2345)
	noiseRng := levelgen.NewWorldgenRandom(rngSeed)
	nz := synth.NewNormalNoise(noiseRng, 0, []float64{1.0})

	p := NoiseThresholdProvider{
		noise: nz, scale: 0.005, threshold: -0.8, highChance: 0.33333334,
		defaultState: def, lowStates: low, highStates: high,
	}

	// Replay against an oracle at a fixed position. We recompute the branch the SAME way the
	// provider does (noise sample vs threshold using the float32-narrowed scale widened via f2d),
	// then assert the provider's rng consumption equals the oracle's for that branch -- the
	// determinism contract.
	const x, y, z = 100, 64, 200
	s := float64(p.scale) // f2d over the float32-narrowed scale (matches the jar's getfield scale:F; f2d)
	d := nz.GetValue(float64(x)*s, float64(y)*s, float64(z)*s)

	prov := levelgen.NewLegacyRandomSource(9)
	oracle := levelgen.NewLegacyRandomSource(9)
	_ = p.GetState(prov, x, y, z)

	if d < float64(p.threshold) {
		_ = oracle.NextIntN(int32(len(low))) // low branch: one int draw
	} else {
		f := oracle.NextFloat() // high branch: one float draw...
		if f < p.highChance {
			_ = oracle.NextIntN(int32(len(high))) // ...plus one int draw when the roll passes
		}
	}
	if prov.NextInt() != oracle.NextInt() {
		t.Fatalf("NoiseThresholdProvider draw order diverged from oracle (branch d=%.4f threshold=%.4f)", d, p.threshold)
	}
}

// TestNoiseThresholdProviderFloat32ScalePrecision closes the audited 26.2 NoiseThresholdProvider
// precision gap. Vanilla stores `scale` as `float` (javap NoiseBasedStateProvider.scale:F) and
// widens to double via f2d at the noise call (javap NoiseThresholdProvider.getState: getfield
// scale:F, f2d, getNoiseValue). A scale value not exactly representable in float32 (e.g. 0.005
// from the real flower_plain config) must be NARROWED to float32 BEFORE the float64 widening,
// otherwise the noise sample diverges and the threshold/branch split shifts.
//
// This regression pins the narrowing-before-widening contract by:
//  1. asserting the float32 narrowing of 0.005 actually changes the value (premise),
//  2. showing the float32-widened noise sample differs from the raw float64 sample,
//  3. locating a witness position where the two samples straddle the threshold,
//  4. asserting the provider returns the float32-widened branch's state rather than the
//     observably different state selected by the raw-float64 branch.
func TestNoiseThresholdProviderFloat32ScalePrecision(t *testing.T) {
	// (1) Premise: 0.005 is not exactly representable as float32 -- the narrowing must
	// produce a different value than the raw float64. The delta is small (~1.1e-10), but
	// after multiplying by an int coordinate it grows linearly, and the witness search
	// below amplifies any noise sample divergence to a threshold branch flip.
	const raw = 0.005
	f32 := float32(raw)
	if raw == float64(f32) {
		t.Fatalf("test premise broken: 0.005 must not round-trip through float32 (raw=%v f32=%v widened=%v)",
			raw, f32, float64(f32))
	}
	if math.Abs(float64(f32)-raw) < 1e-12 {
		t.Fatalf("test premise: float32 narrowing of 0.005 must be visibly > 1e-12 (got delta=%.3e)",
			float64(f32)-raw)
	}

	// Use the flower_plain seed (2345) -- this is the exact seed the in-game provider uses,
	// so the test exercises the same noise path as the live config. We search positions
	// where the noise crosses the threshold; with this seed and threshold=0.0 the float32
	// narrowing (~1.1e-10 per unit position) reliably flips the branch inside ±2^16.
	noiseRng := levelgen.NewWorldgenRandom(2345)
	nz := synth.NewNormalNoise(noiseRng, 0, []float64{1.0})

	low := []block.StateID{stateOf(t, block.OrangeTulip{}), stateOf(t, block.RedTulip{})}
	high := []block.StateID{stateOf(t, block.Poppy{}), stateOf(t, block.Cornflower{})}
	def := stateOf(t, block.Dandelion{})

	// (2) Build the provider with the float32-narrowed scale. The provider's noise call uses
	// s = float64(p.scale) -- the float32-narrowed value widened to double (matches the jar).
	// threshold=0.0 puts the comparison on the noise zero-crossings where the per-position
	// slope is large enough that the ~1e-10 narrowing delta reliably flips the branch.
	p := NoiseThresholdProvider{
		noise: nz, scale: f32, threshold: 0.0, highChance: 0.33333334,
		defaultState: def, lowStates: low, highStates: high,
	}
	sWid := float64(p.scale) // f2d over float32-narrowed scale (matches the jar's getfield scale:F; f2d)
	sRaw := raw              // the un-narrowed float64 scale a "bug" would use

	// (3) Find a witness position where the float32-widened and raw-float64 noise samples
	// straddle the threshold. At such a position the provider (using the float32-narrowed
	// scale) takes one branch and a "raw float64" implementation would take the other --
	// observable via the RNG consumption pattern.
	const maxProbe = 1 << 18
	thresh := float64(p.threshold)
	witness := 0
	witnessFound := false
	for tx := -maxProbe; tx <= maxProbe; tx++ {
		dWid := nz.GetValue(float64(tx)*sWid, 0, 0)
		dRaw := nz.GetValue(float64(tx)*sRaw, 0, 0)
		if (dWid < thresh) != (dRaw < thresh) {
			witness = tx
			witnessFound = true
			break
		}
	}
	if !witnessFound {
		t.Skipf("could not find a witness position where float32 narrowing crosses threshold %.4f "+
			"(searched %d..%d) -- precision gap is too small to cross at this threshold/seed",
			thresh, -maxProbe, maxProbe)
	}

	// (4) At the witness position, compute both samples and verify the provider's branch
	// (RNG consumption) matches the float32-widened oracle, NOT a raw-float64 oracle.
	dWid := nz.GetValue(float64(witness)*sWid, 0, 0)
	dRaw := nz.GetValue(float64(witness)*sRaw, 0, 0)
	if (dWid < thresh) == (dRaw < thresh) {
		t.Fatalf("witness position %d no longer straddles threshold after re-eval: dWid=%.6f dRaw=%.6f thresh=%.6f",
			witness, dWid, dRaw, thresh)
	}

	// Compare the returned state, not only the post-call RNG fingerprint: for this seed a
	// low-branch nextInt and a failed high-branch nextFloat each consume one primitive draw,
	// so their fingerprints can coincide even though their observable states differ.
	providerRNG := levelgen.NewLegacyRandomSource(7)
	widRNG := levelgen.NewLegacyRandomSource(7)
	rawRNG := levelgen.NewLegacyRandomSource(7)
	got := p.GetState(providerRNG, witness, 0, 0)

	wantWid := def
	if dWid < thresh {
		wantWid = low[int(widRNG.NextIntN(int32(len(low))))]
	} else if widRNG.NextFloat() < p.highChance {
		wantWid = high[int(widRNG.NextIntN(int32(len(high))))]
	}
	wantRaw := def
	if dRaw < thresh {
		wantRaw = low[int(rawRNG.NextIntN(int32(len(low))))]
	} else if rawRNG.NextFloat() < p.highChance {
		wantRaw = high[int(rawRNG.NextIntN(int32(len(high))))]
	}
	if wantWid == wantRaw {
		t.Fatalf("witness %d did not produce distinct observable states: widened=%v raw=%v", witness, wantWid, wantRaw)
	}
	if got != wantWid {
		t.Fatalf("provider state at witness %d = %v, want float32-widened state %v (raw float64 would return %v)", witness, got, wantWid, wantRaw)
	}
	t.Logf("witness=%d dWid=%.10f dRaw=%.10f delta=%.3e widened=%v raw=%v",
		witness, dWid, dRaw, math.Abs(dWid-dRaw), wantWid, wantRaw)
}

// pileHayJSON is the real pile_hay state_provider (village hay bales) verbatim from
// world/levelgen/data/configured_feature/pile_hay.json.
const pileHayJSON = `{
  "type": "minecraft:rotated_block_provider",
  "state": {"Name": "minecraft:hay_block", "Properties": {"axis": "y"}}
}`

// TestRotatedBlockProviderParsesAndRolls: the real pile_hay provider parses (regression gate)
// and getState consumes exactly ONE nextInt(3) selecting the axis in declaration order {x,y,z}.
func TestRotatedBlockProviderParsesAndRolls(t *testing.T) {
	p, err := ParseProvider(json.RawMessage(pileHayJSON))
	if err != nil {
		t.Fatalf("pile_hay rotated_block_provider failed to parse: %v", err)
	}
	rp, ok := p.(RotatedBlockProvider)
	if !ok {
		t.Fatalf("parsed provider is %T, want RotatedBlockProvider", p)
	}

	// The axis is VALUES[nextInt(3)] over {x,y,z}: assert the provider's state matches
	// hay_block with the oracle-selected axis, and that exactly one int draw was consumed.
	prov := levelgen.NewLegacyRandomSource(5)
	oracle := levelgen.NewLegacyRandomSource(5)
	got := rp.GetState(prov, 0, 0, 0)

	axisIdx := int(oracle.NextIntN(3))
	wantAxis := rotatedAxisValues[axisIdx]
	want, err := resolveBlockState(blockStateJSON{Name: "minecraft:hay_block", Properties: map[string]string{"axis": wantAxis}})
	if err != nil {
		t.Fatalf("resolve hay_block[axis=%s]: %v", wantAxis, err)
	}
	if got != want {
		t.Fatalf("RotatedBlockProvider gave state %v want hay_block[axis=%s] (%v)", got, wantAxis, want)
	}
	if prov.NextInt() != oracle.NextInt() {
		t.Fatalf("RotatedBlockProvider did not consume exactly one nextInt(3)")
	}
}
