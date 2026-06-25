package placement

import (
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestInSquareDrawsXThenZ pins the JAR-exact draw order: nextInt(16) on X FIRST,
// then on Z. A seeded LCG's first two NextIntN(16) draws must land on x then z.
func TestInSquareDrawsXThenZ(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	p := BlockPos{X: 100, Y: 70, Z: 200}

	// Reference: the same seeded LCG, draws in x-then-z order.
	ref := levelgen.NewLegacyRandomSource(123)
	wantX := int(ref.NextIntN(16)) + p.X
	wantZ := int(ref.NextIntN(16)) + p.Z

	rng := newCounter(123)
	got := InSquare{}.getPositions(ctx, rng, p)
	if len(got) != 1 {
		t.Fatalf("in_square emitted %d positions, want 1", len(got))
	}
	q := got[0]
	if q.X != wantX || q.Z != wantZ {
		t.Fatalf("in_square = (%d,%d), want (%d,%d) [x drawn first]", q.X, q.Z, wantX, wantZ)
	}
	if q.Y != p.Y {
		t.Fatalf("in_square Y = %d, want unchanged %d", q.Y, p.Y)
	}
	if rng.draws != 2 {
		t.Fatalf("in_square drew %d, want 2 (x then z)", rng.draws)
	}

	// Swapping the reference order would diverge — guard against a z-then-x port.
	ref2 := levelgen.NewLegacyRandomSource(123)
	swapZ := int(ref2.NextIntN(16)) + p.Z
	if q.Z == swapZ && wantX != swapZ {
		t.Fatalf("in_square Z matched the FIRST draw — z was drawn before x (wrong order)")
	}
}

// TestHeightmapProjection projects to the configured heightmap top; empty at/below minY.
func TestHeightmapProjection(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	ctx.setHeight(MotionBlocking, 10, 20, 75)
	rng := newCounter(1)

	got := Heightmap{heightmap: MotionBlocking}.getPositions(ctx, rng, BlockPos{X: 10, Y: 0, Z: 20})
	if len(got) != 1 || got[0] != (BlockPos{X: 10, Y: 75, Z: 20}) {
		t.Fatalf("heightmap projection = %v, want [{10 75 20}]", got)
	}

	// y <= minY -> empty. Set a column whose height equals minY.
	ctx.setHeight(MotionBlocking, 11, 21, -64)
	if got := (Heightmap{heightmap: MotionBlocking}).getPositions(ctx, rng, BlockPos{X: 11, Y: 0, Z: 21}); len(got) != 0 {
		t.Fatalf("heightmap at minY = %v, want empty", got)
	}
	if rng.draws != 0 {
		t.Fatalf("heightmap drew %d, want 0", rng.draws)
	}
}

// TestCountCopies: count draws the IntProvider then emits N copies.
func TestCountCopies(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	p := BlockPos{X: 5, Y: 6, Z: 7}

	// A constant count(3): 0 draws, 3 copies.
	c := newCount(&intProvider{kind: intConstant, value: 3})
	rng := newCounter(1)
	got := c.getPositions(ctx, rng, p)
	if len(got) != 3 {
		t.Fatalf("count(constant 3) emitted %d, want 3", len(got))
	}
	for i, q := range got {
		if q != p {
			t.Fatalf("count copy %d = %v, want %v", i, q, p)
		}
	}
	if rng.draws != 0 {
		t.Fatalf("count(constant) drew %d, want 0", rng.draws)
	}

	// A uniform count: exactly one draw, then that many copies.
	ref := levelgen.NewLegacyRandomSource(50)
	n := 1 + int(ref.NextIntN(int32(4-1+1)))
	cu := newCount(&intProvider{kind: intUniform, minVal: 1, maxVal: 4})
	rng2 := newCounter(50)
	gotu := cu.getPositions(ctx, rng2, p)
	if len(gotu) != n {
		t.Fatalf("count(uniform) emitted %d, want %d", len(gotu), n)
	}
	if rng2.draws != 1 {
		t.Fatalf("count(uniform) drew %d, want 1 (before the copies)", rng2.draws)
	}
}

// TestRarityOneFloat: rarity_filter consumes exactly one NextFloat per input pos.
func TestRarityOneFloat(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	p := BlockPos{X: 0, Y: 0, Z: 0}

	ref := levelgen.NewLegacyRandomSource(8)
	want := ref.NextFloat() < 1.0/float32(32)

	rf := newRarityFilter(32)
	rng := newCounter(8)
	got := rf.getPositions(ctx, rng, p)
	keep := len(got) == 1
	if keep != want {
		t.Fatalf("rarity_filter keep = %v, want %v", keep, want)
	}
	if rng.draws != 1 {
		t.Fatalf("rarity_filter drew %d, want exactly 1 NextFloat", rng.draws)
	}
}

// TestBiomeFilter: keep iff the allowed-predicate accepts the biome at the pos.
func TestBiomeFilter(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	const plains, desert = biome.Type(1), biome.Type(2)
	ctx.biomes[[3]int{10, 70, 10}] = plains
	ctx.biomes[[3]int{20, 70, 20}] = desert

	bf := newBiomeFilter(func(b biome.Type) bool { return b == plains })
	rng := newCounter(1)

	if got := bf.getPositions(ctx, rng, BlockPos{X: 10, Y: 70, Z: 10}); len(got) != 1 {
		t.Fatalf("biome filter at plains: got %v, want kept", got)
	}
	if got := bf.getPositions(ctx, rng, BlockPos{X: 20, Y: 70, Z: 20}); len(got) != 0 {
		t.Fatalf("biome filter at desert: got %v, want dropped", got)
	}
	if rng.draws != 0 {
		t.Fatalf("biome filter drew %d, want 0", rng.draws)
	}

	// Nil predicate is permissive.
	if got := newBiomeFilter(nil).getPositions(ctx, rng, BlockPos{X: 0, Y: 0, Z: 0}); len(got) != 1 {
		t.Fatalf("nil-predicate biome filter dropped a pos")
	}
}

// TestHeightRange: replace Y with the HeightProvider sample, keep X/Z.
func TestHeightRange(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	p := BlockPos{X: 8, Y: 0, Z: 9}

	// uniform absolute[136]..below_top[0]: lo=136, hi=319.
	ref := levelgen.NewLegacyRandomSource(77)
	wantY := 136 + int(ref.NextIntN(int32(319-136+1)))

	hr := HeightRange{height: heightProvider{
		kind: heightUniform,
		min:  verticalAnchor{kind: anchorAbsolute, offset: 136},
		max:  verticalAnchor{kind: anchorBelowTop, offset: 0},
	}}
	rng := newCounter(77)
	got := hr.getPositions(ctx, rng, p)
	if len(got) != 1 {
		t.Fatalf("height_range emitted %d, want 1", len(got))
	}
	q := got[0]
	if q.X != p.X || q.Z != p.Z {
		t.Fatalf("height_range moved X/Z: %v, want X=%d Z=%d", q, p.X, p.Z)
	}
	if q.Y != wantY {
		t.Fatalf("height_range Y = %d, want %d", q.Y, wantY)
	}
	if rng.draws != 1 {
		t.Fatalf("height_range drew %d, want 1", rng.draws)
	}
}

// TestSurfaceWaterDepthFilter: keep iff (WORLD_SURFACE - OCEAN_FLOOR) <= maxWaterDepth.
func TestSurfaceWaterDepthFilter(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	rng := newCounter(1)

	// Dry land: worldSurface == oceanFloor -> depth 0 <= 0 -> keep.
	ctx.setHeight(WorldSurface, 0, 0, 64)
	ctx.setHeight(OceanFloor, 0, 0, 64)
	swd := newSurfaceWaterDepthFilter(0)
	if got := swd.getPositions(ctx, rng, BlockPos{X: 0, Y: 64, Z: 0}); len(got) != 1 {
		t.Fatalf("water depth 0 with maxDepth 0: got %v, want kept", got)
	}

	// 3-deep water: worldSurface 67, oceanFloor 64 -> depth 3 > 0 -> drop.
	ctx.setHeight(WorldSurface, 1, 1, 67)
	ctx.setHeight(OceanFloor, 1, 1, 64)
	if got := swd.getPositions(ctx, rng, BlockPos{X: 1, Y: 64, Z: 1}); len(got) != 0 {
		t.Fatalf("water depth 3 with maxDepth 0: got %v, want dropped", got)
	}

	// Same 3-deep water but maxDepth 4 -> keep.
	swd4 := newSurfaceWaterDepthFilter(4)
	if got := swd4.getPositions(ctx, rng, BlockPos{X: 1, Y: 64, Z: 1}); len(got) != 1 {
		t.Fatalf("water depth 3 with maxDepth 4: got %v, want kept", got)
	}
	if rng.draws != 0 {
		t.Fatalf("surface_water_depth_filter drew %d, want 0", rng.draws)
	}
}

// TestBindModifier exercises the JSON binder for each ported type + the loud error.
func TestBindModifier(t *testing.T) {
	deps := ModifierDeps{}
	cases := []struct {
		typ string
		raw string
	}{
		{"minecraft:in_square", `{}`},
		{"minecraft:heightmap", `{"heightmap":"OCEAN_FLOOR_WG"}`},
		{"minecraft:count", `{"count":30}`},
		{"minecraft:count", `{"count":{"type":"minecraft:weighted_list","distribution":[{"data":0,"weight":19},{"data":1,"weight":1}]}}`},
		{"minecraft:rarity_filter", `{"chance":7}`},
		{"minecraft:biome", `{}`},
		{"minecraft:height_range", `{"height":{"type":"minecraft:uniform","min_inclusive":{"absolute":136},"max_inclusive":{"below_top":0}}}`},
		{"minecraft:surface_water_depth_filter", `{"max_water_depth":0}`},
		{"minecraft:random_offset", `{"xz_spread":{"type":"minecraft:trapezoid","min":-7,"max":7,"plateau":0},"y_spread":{"type":"minecraft:trapezoid","min":-3,"max":3,"plateau":0}}`},
		{"minecraft:block_predicate_filter", `{"predicate":{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}}`},
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

	// Unported type errors loudly (T-11-05), never silently nil.
	if _, err := BindModifier("minecraft:noise_based_count", []byte(`{}`), deps); err == nil {
		t.Fatalf("BindModifier(unported) returned nil error, want loud failure")
	}
}

var _ levelgen.RandomSource = (*drawCounter)(nil)

// TestRandomOffsetXYZOrder pins the JAR draw order: xz_spread for X, y_spread for Y,
// xz_spread for Z (3 draws, x then y then z). Using simple uniform spreads so the
// draws are individually attributable.
func TestRandomOffsetXYZOrder(t *testing.T) {
	// xz_spread = uniform[10,10] (a 1-wide range still draws once: nextInt(1)=0 -> 10);
	// y_spread = uniform[20,20]. Distinct constants make the axis assignment visible.
	raw := []byte(`{"xz_spread":{"type":"minecraft:uniform","min_inclusive":10,"max_inclusive":10},"y_spread":{"type":"minecraft:uniform","min_inclusive":20,"max_inclusive":20}}`)
	m, err := BindModifier("minecraft:random_offset", raw, ModifierDeps{})
	if err != nil {
		t.Fatalf("BindModifier random_offset: %v", err)
	}
	ctx := newFakeContext(-64, 384)
	rng := newCounter(7)
	p := BlockPos{X: 100, Y: 64, Z: 200}
	got := m.getPositions(ctx, rng, p)
	if len(got) != 1 {
		t.Fatalf("random_offset emitted %d positions, want 1", len(got))
	}
	want := BlockPos{X: 110, Y: 84, Z: 210}
	if got[0] != want {
		t.Fatalf("random_offset xyz = %v, want %v", got[0], want)
	}
	if rng.draws != 3 {
		t.Fatalf("random_offset consumed %d draws, want 3 (x,y,z)", rng.draws)
	}
}

// TestRandomOffsetTrapezoid pins the REAL flower_default spreads (xz trapezoid
// {-7,7,0}, y trapezoid {-3,3,0}) over a fixed origin + seed, with the post-draw rng
// state fingerprinted — the determinism contract (T-12-02).
func TestRandomOffsetTrapezoid(t *testing.T) {
	raw := []byte(`{
		"xz_spread":{"type":"minecraft:trapezoid","min":-7,"max":7,"plateau":0},
		"y_spread":{"type":"minecraft:trapezoid","min":-3,"max":3,"plateau":0}
	}`)
	m, err := BindModifier("minecraft:random_offset", raw, ModifierDeps{})
	if err != nil {
		t.Fatalf("BindModifier random_offset trapezoid: %v", err)
	}
	ctx := newFakeContext(-64, 384)
	rng := levelgen.NewLegacyRandomSource(2024)
	p := BlockPos{X: 100, Y: 64, Z: 200}
	got := m.getPositions(ctx, rng, p)
	// Oracle (hand-traced): dx=2, dy=0, dz=5 -> (102,64,205).
	want := BlockPos{X: 102, Y: 64, Z: 205}
	if got[0] != want {
		t.Fatalf("random_offset trapezoid = %v, want %v", got[0], want)
	}
	// Post-draw fingerprint: the trapezoid samples consumed 6 nextInt draws total
	// (2 per trapezoid x3); the next raw int must match the oracle (404437318).
	if fp := rng.NextInt(); fp != 404437318 {
		t.Fatalf("random_offset post-draw fingerprint = %d, want 404437318", fp)
	}
}

// TestBlockPredicateFilterKeepsDrops: block_predicate_filter keeps p iff the bound
// predicate holds. matching_block_tag air keeps over air, drops over stone.
func TestBlockPredicateFilterKeepsDrops(t *testing.T) {
	raw := []byte(`{"predicate":{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}}`)
	m, err := BindModifier("minecraft:block_predicate_filter", raw, ModifierDeps{})
	if err != nil {
		t.Fatalf("BindModifier block_predicate_filter: %v", err)
	}
	ctx := newFakeContext(-64, 384)
	rng := newCounter(1)
	p := BlockPos{X: 3, Y: 10, Z: 7}

	// Air position -> keep.
	if got := m.getPositions(ctx, rng, p); len(got) != 1 || got[0] != p {
		t.Fatalf("block_predicate_filter air: got %v, want [%v]", got, p)
	}
	// Stone position -> drop.
	ctx.blocks[[3]int{3, 10, 7}] = stateOf(t, block.Stone{})
	if got := m.getPositions(ctx, rng, p); len(got) != 0 {
		t.Fatalf("block_predicate_filter over stone: got %v, want empty", got)
	}
	// The filter draws 0 rng (predicates are positional).
	if rng.draws != 0 {
		t.Fatalf("block_predicate_filter consumed %d rng draws, want 0", rng.draws)
	}
}
