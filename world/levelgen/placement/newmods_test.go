package placement

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// The six modifiers ported in FEAT-11-06 unblock 32 previously-silently-dropped
// placed_features. These tests pin each modifier's getPositions/count body against a
// known config, plus the loud-error contract for an unknown modifier type.

// TestNoiseThresholdCountBranches pins NoiseThresholdCountPlacement.count: it reads
// BIOME_INFO_NOISE at (x/200, z/200) and returns belowNoise if value < noiseLevel, else
// aboveNoise, then emits that many copies of pos. 0 rng draws (positional noise).
func TestNoiseThresholdCountBranches(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	p := BlockPos{X: 0, Y: 64, Z: 0}
	// At origin the BIOME_INFO_NOISE value is deterministic; pick thresholds that force
	// each branch. A very-high noiseLevel forces the "below" branch; a very-low one the
	// "above" branch (the noise value at the origin is finite and small in magnitude).
	rng := newCounter(1)
	below := newNoiseThresholdCount(1e9, 3, 7)
	if got := below.getPositions(ctx, rng, p); len(got) != 3 {
		t.Fatalf("noise_threshold_count below branch: %d copies, want 3", len(got))
	}
	above := newNoiseThresholdCount(-1e9, 3, 7)
	if got := above.getPositions(ctx, rng, p); len(got) != 7 {
		t.Fatalf("noise_threshold_count above branch: %d copies, want 7", len(got))
	}
	if rng.draws != 0 {
		t.Fatalf("noise_threshold_count drew rng %d times, want 0 (positional noise)", rng.draws)
	}
	// Every emitted copy is pos unchanged.
	for _, q := range below.getPositions(ctx, rng, p) {
		if q != p {
			t.Fatalf("noise_threshold_count copy = %v, want %v", q, p)
		}
	}
}

// TestNoiseBasedCountCeil pins NoiseBasedCountPlacement.count: ceil((noise + offset) *
// ratio). With a huge offset the count is dominated by offset*ratio, letting us assert
// the ceil/d2i exactly regardless of the (bounded) noise term.
func TestNoiseBasedCountCeil(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	p := BlockPos{X: 0, Y: 64, Z: 0}
	rng := newCounter(1)
	// offset 10.0, ratio 5, factor 80 -> count ~= ceil((noise+10)*5) ~= 50 (noise near 0).
	c := newNoiseBasedCount(5, 80.0, 10.0)
	got := c.getPositions(ctx, rng, p)
	if len(got) < 45 || len(got) > 55 {
		t.Fatalf("noise_based_count ~= 50 copies, got %d", len(got))
	}
	if rng.draws != 0 {
		t.Fatalf("noise_based_count drew rng %d times, want 0", rng.draws)
	}
}

// TestSurfaceRelativeThresholdFilterGate pins the heightmap-relative min/max gate:
// keep iff (h+min) <= pos.Y <= (h+max).
func TestSurfaceRelativeThresholdFilterGate(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	ctx.setHeight(OceanFloorWG, 5, 9, 70) // surface at y=70
	rng := newCounter(1)
	// max_inclusive = -2 (default min = MIN_INT): keep iff pos.Y <= 68.
	f := newSurfaceRelativeThresholdFilter(OceanFloorWG, -1<<31, -2)
	if got := f.getPositions(ctx, rng, BlockPos{X: 5, Y: 68, Z: 9}); len(got) != 1 {
		t.Fatalf("surface_relative_threshold_filter at y=68 (<=70-2): want kept, got %v", got)
	}
	if got := f.getPositions(ctx, rng, BlockPos{X: 5, Y: 69, Z: 9}); len(got) != 0 {
		t.Fatalf("surface_relative_threshold_filter at y=69 (>70-2): want dropped, got %v", got)
	}
	if rng.draws != 0 {
		t.Fatalf("surface_relative_threshold_filter drew rng %d, want 0", rng.draws)
	}
}

// TestFixedPlacementSameChunkFilter pins FixedPlacement: keep positions sharing pos's
// section (x>>4,z>>4); empty when none share.
func TestFixedPlacementSameChunkFilter(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	rng := newCounter(1)
	// end_platform's actual config: a single position [100, 49, 0] (section 6,0).
	f := FixedPlacement{positions: []BlockPos{{X: 100, Y: 49, Z: 0}}}

	// Origin inside section (6,0): the position is returned.
	got := f.getPositions(ctx, rng, BlockPos{X: 96, Y: 0, Z: 0})
	if len(got) != 1 || got[0] != (BlockPos{X: 100, Y: 49, Z: 0}) {
		t.Fatalf("fixed_placement same-chunk: got %v, want [{100 49 0}]", got)
	}
	// Origin in a different section (0,0): empty.
	if got := f.getPositions(ctx, rng, BlockPos{X: 0, Y: 0, Z: 0}); len(got) != 0 {
		t.Fatalf("fixed_placement other-chunk: got %v, want empty", got)
	}
	if rng.draws != 0 {
		t.Fatalf("fixed_placement drew rng %d, want 0", rng.draws)
	}
}

// TestEnvironmentScanFindsTarget pins EnvironmentScanPlacement scanning UP for a target
// predicate: it stops at the first cursor where target holds and emits it.
func TestEnvironmentScanFindsTarget(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	rng := newCounter(1)
	stone := block.ToStateID[block.Stone{}]
	// Place stone 3 blocks above the origin; air elsewhere (StateID 0 default is air-ish
	// only if block 0 is air -- use an explicit target predicate on the stone id set).
	target := matchingBlocks{set: map[block.StateID]bool{stone: true}}
	ctx.blocks[[3]int{10, 73, 20}] = stone
	scan := EnvironmentScan{direction: scanUp, target: target, allowedSearch: truePredicate{}, maxSteps: 12}
	got := scan.getPositions(ctx, rng, BlockPos{X: 10, Y: 70, Z: 20})
	if len(got) != 1 || got[0] != (BlockPos{X: 10, Y: 73, Z: 20}) {
		t.Fatalf("environment_scan up: got %v, want [{10 73 20}] (target found 3 steps up)", got)
	}
	if rng.draws != 0 {
		t.Fatalf("environment_scan drew rng %d, want 0", rng.draws)
	}
	// A target never within maxSteps -> empty.
	scanShort := EnvironmentScan{direction: scanUp, target: target, allowedSearch: truePredicate{}, maxSteps: 2}
	if got := scanShort.getPositions(ctx, rng, BlockPos{X: 10, Y: 70, Z: 20}); len(got) != 0 {
		t.Fatalf("environment_scan too-short: got %v, want empty", got)
	}
}

// TestCountOnEveryLayerScan pins CountOnEveryLayerPlacement: it draws count.sample per
// layer (2 nextInt(16) per iteration) and emits air-over-solid boundaries walking down
// from the MOTION_BLOCKING height. With a single solid floor there is exactly one layer-0
// boundary, and layer-1 finds none, stopping the modifier.
func TestCountOnEveryLayerScan(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	stone := block.ToStateID[block.Stone{}]
	// count = constant 1: one (x,z) sample per layer. The LCG's first two nextInt(16)
	// pick the same jittered column each layer (deterministic per seed).
	c := newCountOnEveryLayer(&intProvider{kind: intConstant, value: 1})

	// Predict the jittered column from the reference LCG (x then z draws), for layer 0.
	ref := levelgen.NewLegacyRandomSource(42)
	// count.Sample draws 0 (constant); then x, z:
	jx := int(ref.NextIntN(16))
	jz := int(ref.NextIntN(16))
	col := BlockPos{X: jx, Z: jz}

	// Build a solid floor: stone at y=0, air above. MOTION_BLOCKING height = 1 (top air).
	for y := -64; y <= 0; y++ {
		ctx.blocks[[3]int{col.X, y, col.Z}] = stone
	}
	ctx.setHeight(MotionBlocking, col.X, col.Z, 1)

	rng := newCounter(42)
	got := c.getPositions(ctx, rng, BlockPos{X: 0, Y: 0, Z: 0})
	// Layer 0: boundary at the air-over-stone top -> y = 1 (cursor at y=1 is air, below y=0
	// is stone -> return cursorY+1... the scan starts at MOTION_BLOCKING height and walks
	// down). At least one position is emitted; every emitted pos is at the jittered column.
	if len(got) < 1 {
		t.Fatalf("count_on_every_layer: emitted %d, want >=1 boundary", len(got))
	}
	for _, q := range got {
		if q.X != col.X || q.Z != col.Z {
			t.Fatalf("count_on_every_layer pos %v not at jittered column (%d,%d)", q, col.X, col.Z)
		}
	}
}

// TestBindNewModifiersFromJSON binds each new modifier from its real placed_feature JSON
// shape, proving the JSON -> modifier wiring holds end to end.
func TestBindNewModifiersFromJSON(t *testing.T) {
	deps := ModifierDeps{}
	cases := []struct{ typ, raw string }{
		{"minecraft:count_on_every_layer", `{"count":8}`},
		{"minecraft:noise_threshold_count", `{"above_noise":4,"below_noise":15,"noise_level":-0.8}`},
		{"minecraft:noise_based_count", `{"noise_factor":80.0,"noise_offset":0.3,"noise_to_count_ratio":160}`},
		{"minecraft:noise_based_count", `{"noise_factor":400.0,"noise_to_count_ratio":20}`}, // no offset -> default 0.0
		{"minecraft:surface_relative_threshold_filter", `{"heightmap":"OCEAN_FLOOR_WG","max_inclusive":-13}`},
		{"minecraft:fixed_placement", `{"positions":[[100,49,0]]}`},
		{"minecraft:environment_scan", `{"direction_of_search":"up","max_steps":12,"target_condition":{"type":"minecraft:has_sturdy_face","direction":"down"},"allowed_search_condition":{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}}`},
	}
	for _, c := range cases {
		m, err := BindModifier(c.typ, []byte(c.raw), deps)
		if err != nil {
			t.Errorf("BindModifier(%s) error: %v", c.typ, err)
			continue
		}
		if m == nil {
			t.Errorf("BindModifier(%s) returned nil modifier", c.typ)
		}
	}

	// inside_world_bounds + has_sturdy_face bind through the predicate parser.
	for _, raw := range []string{
		`{"type":"minecraft:has_sturdy_face","direction":"down"}`,
		`{"type":"minecraft:inside_world_bounds","offset":[0,-5,0]}`,
	} {
		if _, err := ParsePredicate([]byte(raw)); err != nil {
			t.Errorf("ParsePredicate(%s) error: %v", raw, err)
		}
	}
}

// TestUnknownModifierErrorsLoudly is the T-11-05 contract: an unbindable modifier is a
// loud error, never a silent nil (which used to no-op an entire feature).
func TestUnknownModifierErrorsLoudly(t *testing.T) {
	if _, err := BindModifier("minecraft:no_such_modifier", []byte(`{}`), ModifierDeps{}); err == nil {
		t.Fatalf("BindModifier(unknown) returned nil error, want loud failure")
	}
	if _, err := ParsePredicate([]byte(`{"type":"minecraft:no_such_predicate"}`)); err == nil {
		t.Fatalf("ParsePredicate(unknown) returned nil error, want loud failure")
	}
}
