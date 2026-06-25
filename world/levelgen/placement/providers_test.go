package placement

import (
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// fakeContext is an in-memory PlacementContext for the draw-order tests: a flat
// per-(x,z) heightmap (one value per type), a fixed minY/height, and an optional
// block/biome grid. It performs ZERO rng draws — the determinism contract is purely
// in the modifiers + providers.
type fakeContext struct {
	minY, height int
	heights      map[HeightmapType]map[[2]int]int
	blocks       map[[3]int]block.StateID
	biomes       map[[3]int]biome.Type
}

func newFakeContext(minY, height int) *fakeContext {
	return &fakeContext{
		minY:    minY,
		height:  height,
		heights: map[HeightmapType]map[[2]int]int{},
		blocks:  map[[3]int]block.StateID{},
		biomes:  map[[3]int]biome.Type{},
	}
}

func (c *fakeContext) setHeight(t HeightmapType, x, z, y int) {
	m := c.heights[t]
	if m == nil {
		m = map[[2]int]int{}
		c.heights[t] = m
	}
	m[[2]int{x, z}] = y
}

func (c *fakeContext) GetHeight(t HeightmapType, x, z int) int {
	if m := c.heights[t]; m != nil {
		if y, ok := m[[2]int{x, z}]; ok {
			return y
		}
	}
	return c.minY // default: terrain at floor
}

func (c *fakeContext) MinY() int   { return c.minY }
func (c *fakeContext) Height() int { return c.height }

func (c *fakeContext) GetBlock(x, y, z int) block.StateID { return c.blocks[[3]int{x, y, z}] }
func (c *fakeContext) BiomeAt(x, y, z int) biome.Type     { return c.biomes[[3]int{x, y, z}] }

// drawCounter wraps a RandomSource and tallies every primitive draw so a test can
// assert the JAR-exact draw count, not just the output. It mirrors the underlying
// LegacyRandomSource bit-for-bit (it only intercepts the call surface).
type drawCounter struct {
	inner levelgen.RandomSource
	draws int
}

func (d *drawCounter) NextLong() int64        { d.draws++; return d.inner.NextLong() }
func (d *drawCounter) NextInt() int32         { d.draws++; return d.inner.NextInt() }
func (d *drawCounter) NextIntN(b int32) int32 { d.draws++; return d.inner.NextIntN(b) }
func (d *drawCounter) NextDouble() float64    { d.draws++; return d.inner.NextDouble() }
func (d *drawCounter) NextFloat() float32     { d.draws++; return d.inner.NextFloat() }
func (d *drawCounter) NextBoolean() bool      { d.draws++; return d.inner.NextBoolean() }
func (d *drawCounter) ConsumeCount(n int)     { d.draws += n; d.inner.ConsumeCount(n) }
func (d *drawCounter) Fork() levelgen.RandomSource {
	d.draws++
	return d.inner.Fork()
}
func (d *drawCounter) ForkPositional() levelgen.PositionalRandomFactory {
	d.draws++
	return d.inner.ForkPositional()
}

func newCounter(seed int64) *drawCounter {
	return &drawCounter{inner: levelgen.NewLegacyRandomSource(seed)}
}

// ---- PlacementFilter base ----

type alwaysPlace struct{ b bool }

func (a alwaysPlace) shouldPlace(_ PlacementContext, _ levelgen.RandomSource, _ BlockPos) bool {
	return a.b
}

func TestFilterKeepsAndDrops(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	rng := newCounter(1)
	p := BlockPos{X: 3, Y: 10, Z: 7}

	keep := placementFilter{predicate: alwaysPlace{true}}
	got := keep.getPositions(ctx, rng, p)
	if len(got) != 1 || got[0] != p {
		t.Fatalf("filter keep: got %v, want [%v]", got, p)
	}

	drop := placementFilter{predicate: alwaysPlace{false}}
	if got := drop.getPositions(ctx, rng, p); len(got) != 0 {
		t.Fatalf("filter drop: got %v, want empty", got)
	}
	if rng.draws != 0 {
		t.Fatalf("filter base drew rng %d times via the constant predicate, want 0", rng.draws)
	}
}

// ---- RepeatingPlacement base ----

type fixedCount struct{ n int }

func (f fixedCount) count(_ levelgen.RandomSource, _ BlockPos) int { return f.n }

func TestRepeatingCopies(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	rng := newCounter(1)
	p := BlockPos{X: 1, Y: 2, Z: 3}

	rep := repeatingPlacement{counter: fixedCount{n: 4}}
	got := rep.getPositions(ctx, rng, p)
	if len(got) != 4 {
		t.Fatalf("repeating: got %d copies, want 4", len(got))
	}
	for i, q := range got {
		if q != p {
			t.Fatalf("repeating copy %d = %v, want %v", i, q, p)
		}
	}
	// Zero count emits nothing.
	if got := (repeatingPlacement{counter: fixedCount{n: 0}}).getPositions(ctx, rng, p); len(got) != 0 {
		t.Fatalf("repeating n=0: got %v, want empty", got)
	}
}

// ---- VerticalAnchor.resolveY ----

func TestAnchorResolveY(t *testing.T) {
	const minY, genDepth = -64, 384
	cases := []struct {
		a    verticalAnchor
		want int
	}{
		{verticalAnchor{kind: anchorAbsolute, offset: 136}, 136},
		{verticalAnchor{kind: anchorAboveBottom, offset: 0}, -64},
		{verticalAnchor{kind: anchorAboveBottom, offset: 8}, -56},
		// below_top(0) = (384-1) + (-64) - 0 = 319.
		{verticalAnchor{kind: anchorBelowTop, offset: 0}, 319},
		{verticalAnchor{kind: anchorBelowTop, offset: 8}, 311},
	}
	for _, c := range cases {
		if got := c.a.resolveY(minY, genDepth); got != c.want {
			t.Errorf("resolveY(%+v) = %d, want %d", c.a, got, c.want)
		}
	}
}

// ---- IntProvider.Sample draw counts ----

func TestProviderConstantNoDraw(t *testing.T) {
	rng := newCounter(42)
	ip := &intProvider{kind: intConstant, value: 30}
	if got := ip.Sample(rng); got != 30 {
		t.Fatalf("constant.Sample = %d, want 30", got)
	}
	if rng.draws != 0 {
		t.Fatalf("constant drew %d, want 0", rng.draws)
	}
}

func TestProviderUniformOneDraw(t *testing.T) {
	// UniformInt.sample = min + nextInt(max-min+1); verify the value matches a hand
	// computation off the same seeded LCG, and exactly one draw is consumed.
	ref := levelgen.NewLegacyRandomSource(7)
	wantVal := 5 + int(ref.NextIntN(int32(10-5+1)))

	rng := newCounter(7)
	ip := &intProvider{kind: intUniform, minVal: 5, maxVal: 10}
	if got := ip.Sample(rng); got != wantVal {
		t.Fatalf("uniform.Sample = %d, want %d", got, wantVal)
	}
	if rng.draws != 1 {
		t.Fatalf("uniform drew %d, want 1", rng.draws)
	}
}

func TestProviderBiasedToBottomTwoDraws(t *testing.T) {
	// BiasedToBottomInt.sample = min + nextInt(nextInt(max-min+1)+1): inner first.
	ref := levelgen.NewLegacyRandomSource(99)
	inner := int(ref.NextIntN(int32(8-2+1))) + 1
	wantVal := 2 + int(ref.NextIntN(int32(inner)))

	rng := newCounter(99)
	ip := &intProvider{kind: intBiasedToBottom, minVal: 2, maxVal: 8}
	if got := ip.Sample(rng); got != wantVal {
		t.Fatalf("biased_to_bottom.Sample = %d, want %d", got, wantVal)
	}
	if rng.draws != 2 {
		t.Fatalf("biased_to_bottom drew %d, want 2", rng.draws)
	}
}

func TestProviderClampedUsesSourceDraws(t *testing.T) {
	// ClampedInt(uniform[-3,1], 0, 1): source is uniform (1 draw), clamp to [0,1].
	ref := levelgen.NewLegacyRandomSource(3)
	raw := -3 + int(ref.NextIntN(int32(1-(-3)+1)))
	want := raw
	if want < 0 {
		want = 0
	}
	if want > 1 {
		want = 1
	}

	rng := newCounter(3)
	ip := &intProvider{
		kind:   intClamped,
		minVal: 0, maxVal: 1,
		source: &intProvider{kind: intUniform, minVal: -3, maxVal: 1},
	}
	if got := ip.Sample(rng); got != want {
		t.Fatalf("clamped.Sample = %d, want %d", got, want)
	}
	if rng.draws != 1 {
		t.Fatalf("clamped drew %d, want 1 (the inner uniform)", rng.draws)
	}
}

func TestProviderWeightedListPickThenSample(t *testing.T) {
	// weighted_list over two CONSTANT data values (0 weight 19, 1 weight 1): the
	// pick consumes nextInt(totalWeight=20) (1 draw); constants then draw 0.
	ref := levelgen.NewLegacyRandomSource(5)
	pick := int(ref.NextIntN(20))
	want := 0
	if pick >= 19 {
		want = 1
	}

	rng := newCounter(5)
	ip := &intProvider{
		kind:        intWeightedList,
		totalWeight: 20,
		entries: []weightedIntEntry{
			{provider: &intProvider{kind: intConstant, value: 0}, weight: 19},
			{provider: &intProvider{kind: intConstant, value: 1}, weight: 1},
		},
	}
	if got := ip.Sample(rng); got != want {
		t.Fatalf("weighted_list.Sample = %d, want %d (pick=%d)", got, want, pick)
	}
	if rng.draws != 1 {
		t.Fatalf("weighted_list (constant entries) drew %d, want 1 (the pick)", rng.draws)
	}
}

// ---- HeightProvider.sample ----

func TestProviderUniformHeightOneDraw(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	ref := levelgen.NewLegacyRandomSource(11)
	// uniform absolute[0]..absolute[10]: lo=0,hi=10 -> randomBetweenInclusive.
	want := 0 + int(ref.NextIntN(int32(10-0+1)))

	rng := newCounter(11)
	hp := heightProvider{
		kind: heightUniform,
		min:  verticalAnchor{kind: anchorAbsolute, offset: 0},
		max:  verticalAnchor{kind: anchorAbsolute, offset: 10},
	}
	if got := hp.sample(rng, ctx); got != want {
		t.Fatalf("uniform height.sample = %d, want %d", got, want)
	}
	if rng.draws != 1 {
		t.Fatalf("uniform height drew %d, want 1", rng.draws)
	}
}

func TestProviderUniformHeightEmptyRangeNoDraw(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	rng := newCounter(11)
	// lo=10 > hi=5 -> returns lo, NO draw.
	hp := heightProvider{
		kind: heightUniform,
		min:  verticalAnchor{kind: anchorAbsolute, offset: 10},
		max:  verticalAnchor{kind: anchorAbsolute, offset: 5},
	}
	if got := hp.sample(rng, ctx); got != 10 {
		t.Fatalf("empty-range uniform height = %d, want 10 (lo)", got)
	}
	if rng.draws != 0 {
		t.Fatalf("empty-range uniform height drew %d, want 0", rng.draws)
	}
}

func TestProviderBelowTopUsesGenTop(t *testing.T) {
	ctx := newFakeContext(-64, 384)
	rng := newCounter(13)
	// uniform absolute[136]..below_top[0]: lo=136, hi=(384-1)+(-64)-0=319.
	ref := levelgen.NewLegacyRandomSource(13)
	want := 136 + int(ref.NextIntN(int32(319-136+1)))
	hp := heightProvider{
		kind: heightUniform,
		min:  verticalAnchor{kind: anchorAbsolute, offset: 136},
		max:  verticalAnchor{kind: anchorBelowTop, offset: 0},
	}
	if got := hp.sample(rng, ctx); got != want {
		t.Fatalf("below_top uniform height = %d, want %d", got, want)
	}
}
