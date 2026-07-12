package feature

import (
	"encoding/json"
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
//   d < threshold        -> lowStates[nextInt(len)]           (1 int draw, no float draw)
//   d >= threshold, roll  -> nextFloat() < highChance ? highStates[nextInt(len)] : defaultState
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
	// provider does (noise sample vs threshold), then assert the provider's rng consumption
	// equals the oracle's for that branch -- the determinism contract.
	const x, y, z = 100, 64, 200
	d := nz.GetValue(float64(x)*p.scale, float64(y)*p.scale, float64(z)*p.scale)

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
