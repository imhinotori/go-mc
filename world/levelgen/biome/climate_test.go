package biome

import (
	"math/rand"
	"testing"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// testSeed is a fixed seed so every test builds the same deterministic router + boxes.
const testSeed = int64(0x5EED_1234)

// newSource is a shared helper: a Router from testSeed + the multi-noise biome source.
func newSource(t *testing.T) *MultiNoiseBiomeSource {
	t.Helper()
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	src, err := NewMultiNoiseBiomeSource(r)
	if err != nil {
		t.Fatalf("NewMultiNoiseBiomeSource: %v", err)
	}
	return src
}

// constFn is a tiny fixed-value density.Function for the quantization unit test.
type constFn float64

func (c constFn) Compute(density.Context) float64 { return float64(c) }
func (c constFn) MinValue() float64               { return float64(c) }
func (c constFn) MaxValue() float64               { return float64(c) }

// TestClimateTargetPoint asserts the 6-D TargetPoint is the quantized (×10000, truncated)
// sample of the six climate functions — the ported Climate.target/quantizeCoord math.
func TestClimateTargetPoint(t *testing.T) {
	// distinct constants so a field swap would be visible.
	s := Sampler{
		Temperature:     constFn(0.5),
		Humidity:        constFn(-0.25),
		Continentalness: constFn(0.123),
		Erosion:         constFn(-0.9),
		Depth:           constFn(0.0),
		Weirdness:       constFn(0.4567),
	}
	got := s.sample(10, 20, 30) // quart coords; constFn ignores position

	want := TargetPoint{
		Temperature:     quantizeCoord(0.5),
		Humidity:        quantizeCoord(-0.25),
		Continentalness: quantizeCoord(0.123),
		Erosion:         quantizeCoord(-0.9),
		Depth:           quantizeCoord(0.0),
		Weirdness:       quantizeCoord(0.4567),
	}
	if got != want {
		t.Fatalf("target point mismatch:\n got %+v\nwant %+v", got, want)
	}
	// quantizeCoord(0.5) must be exactly 5000 (0.5 * 10000), proving the ×10000 factor.
	if got.Temperature != 5000 {
		t.Fatalf("quantizeCoord(0.5) = %d, want 5000", got.Temperature)
	}
}

// TestBiomeParametersParse asserts the embedded biome_parameters.json parses into the box
// list, every box resolves to a real biome, and the overworld staples are present.
func TestBiomeParametersParse(t *testing.T) {
	src := newSource(t)
	boxes := src.Params().Boxes()
	if len(boxes) < 1000 {
		t.Fatalf("expected the full overworld box list (thousands), got %d", len(boxes))
	}

	// Confirm a few well-known overworld biomes are referenced by at least one box.
	seen := map[levelbiome.Type]bool{}
	for i := range boxes {
		seen[boxes[i].Biome] = true
	}
	for _, name := range []string{"minecraft:plains", "minecraft:desert", "minecraft:badlands"} {
		var bt levelbiome.Type
		if err := bt.UnmarshalText([]byte(name)); err != nil {
			t.Fatalf("biome %q not in registry: %v", name, err)
		}
		if !seen[bt] {
			t.Errorf("expected at least one box selecting %s", name)
		}
	}
}

// TestNearestBiomeVaries asserts getBiome over a spread of positions returns MORE THAN
// ONE distinct biome — real multi-noise diversity, not a uniform plains slab.
func TestNearestBiomeVaries(t *testing.T) {
	src := newSource(t)

	distinct := map[levelbiome.Type]int{}
	// Sample a wide grid at sea level so several climate regions are crossed.
	for x := -4000; x <= 4000; x += 250 {
		for z := -4000; z <= 4000; z += 250 {
			bt := src.GetBiome(x, 64, z)
			distinct[bt]++
		}
	}
	if len(distinct) < 2 {
		t.Fatalf("multi-noise source produced only %d distinct biome(s) over the grid; expected variety", len(distinct))
	}
	t.Logf("distinct biomes over grid: %d", len(distinct))
}

// TestBiomeDeterministic asserts two sources from the same seed return identical biomes
// at the same coords (Pitfall 7: no map iteration, no leaky RNG).
func TestBiomeDeterministic(t *testing.T) {
	a := newSource(t)
	b := newSource(t)
	for _, p := range [][3]int{{0, 64, 0}, {1234, 70, -567}, {-3000, 64, 2500}, {800, 64, 800}} {
		ba := a.GetBiome(p[0], p[1], p[2])
		bb := b.GetBiome(p[0], p[1], p[2])
		if ba != bb {
			t.Fatalf("non-deterministic biome at %v: %s vs %s", p, ba, bb)
		}
	}
}

// TestRTreeMatchesLinearScan is the SAFETY GATE for the Climate RTree port: for a large set
// of pseudo-random target points spanning the quantized climate space (and the boundary
// values), the RTree search (findValue) must return the EXACT same biome as the linear
// brute-force scan (findValueLinear). A faster-but-wrong biome lookup would silently change
// the generated world, so this asserts the RTree is a pure throughput swap — behavior-identical
// nearest box, identical first-match-wins tiebreak. Run against the real ~7594-box overworld
// list so the tree is deep enough to exercise pruning across many subtree levels.
func TestRTreeMatchesLinearScan(t *testing.T) {
	src := newSource(t)
	pl := src.Params()
	if len(pl.Boxes()) < 1000 {
		t.Fatalf("expected the full overworld box list, got %d", len(pl.Boxes()))
	}

	// Deterministic RNG so a failure is reproducible. The quantized climate coords live in
	// roughly [-2*10000, 2*10000] (climate floats in ~[-2,2] times the 10000 factor); sample
	// a wider band plus the extreme boundaries so out-of-all-boxes targets are exercised too.
	rng := rand.New(rand.NewSource(0xA11CE))
	randCoord := func() int64 {
		// span ~[-30000, 30000] to cover inside, near-edge, and far-outside targets.
		return int64(rng.Intn(60001) - 30000)
	}

	const iterations = 50000
	for i := 0; i < iterations; i++ {
		tp := TargetPoint{
			Temperature:     randCoord(),
			Humidity:        randCoord(),
			Continentalness: randCoord(),
			Erosion:         randCoord(),
			Depth:           randCoord(),
			Weirdness:       randCoord(),
		}
		want, okWant := pl.findValueLinear(tp)
		got, okGot := pl.findValue(tp)
		if okWant != okGot || want != got {
			t.Fatalf("RTree/linear mismatch at %+v: rtree=(%s,%v) linear=(%s,%v)",
				tp, got, okGot, want, okWant)
		}
	}

	// Also pin a handful of extreme/edge targets explicitly (all-min, all-max, all-zero).
	edges := []TargetPoint{
		{},
		{Temperature: 30000, Humidity: 30000, Continentalness: 30000, Erosion: 30000, Depth: 30000, Weirdness: 30000},
		{Temperature: -30000, Humidity: -30000, Continentalness: -30000, Erosion: -30000, Depth: -30000, Weirdness: -30000},
		{Temperature: 1, Humidity: -1, Continentalness: 9999, Erosion: -9999, Depth: 5000, Weirdness: -5000},
	}
	for _, tp := range edges {
		want, okWant := pl.findValueLinear(tp)
		got, okGot := pl.findValue(tp)
		if okWant != okGot || want != got {
			t.Fatalf("RTree/linear mismatch at edge %+v: rtree=(%s,%v) linear=(%s,%v)",
				tp, got, okGot, want, okWant)
		}
	}
}

// TestFitnessNearestAndTiebreak unit-tests the ported fitness/distance + the first-match-
// wins tiebreak directly (independent of the router), so a distance-math regression is
// caught even if the data happens to mask it.
func TestFitnessNearestAndTiebreak(t *testing.T) {
	var plains, desert levelbiome.Type
	if err := plains.UnmarshalText([]byte("minecraft:plains")); err != nil {
		t.Fatal(err)
	}
	if err := desert.UnmarshalText([]byte("minecraft:desert")); err != nil {
		t.Fatal(err)
	}

	// Two boxes that both contain the target exactly (fitness 0) — the EARLIER wins.
	full := Parameter{Min: -10000, Max: 10000}
	a := ParameterPoint{full, full, full, full, full, full, 0, plains}
	b := ParameterPoint{full, full, full, full, full, full, 0, desert}
	list := NewParameterList([]ParameterPoint{a, b})

	t0 := TargetPoint{} // all zeros, inside both
	if a.fitness(t0) != 0 || b.fitness(t0) != 0 {
		t.Fatalf("expected both boxes fitness 0, got %d / %d", a.fitness(t0), b.fitness(t0))
	}
	got, ok := list.findValue(t0)
	if !ok || got != plains {
		t.Fatalf("tie should resolve to the earlier box (plains); got %s ok=%v", got, ok)
	}

	// A target outside box A but inside box B: the closer box (B) wins.
	near := Parameter{Min: 0, Max: 100}     // box A: tight near 0
	far := Parameter{Min: 9000, Max: 10000} // box B: tight near 9500
	a2 := ParameterPoint{near, full, full, full, full, full, 0, plains}
	b2 := ParameterPoint{far, full, full, full, full, full, 0, desert}
	list2 := NewParameterList([]ParameterPoint{a2, b2})
	tNearB := TargetPoint{Temperature: 9500} // inside B's temperature span
	got2, _ := list2.findValue(tNearB)
	if got2 != desert {
		t.Fatalf("target inside box B's span should select desert, got %s", got2)
	}
}

// gradientFn is a density.Function whose value increases linearly with X (block coords),
// used to force the climate spawn search away from origin toward a preferred continentalness.
type gradientFn struct{ base, scale float64 }

func (g gradientFn) Compute(c density.Context) float64 { return g.base + float64(c.X)*g.scale }
func (g gradientFn) MinValue() float64                 { return -2 }
func (g gradientFn) MaxValue() float64                 { return 2 }

// TestFindSpawnPositionDeterministic proves the climate spawn search is pure (same sampler
// -> same result) and returns a well-formed position. CITE: Climate$Sampler.findSpawnPosition.
func TestFindSpawnPositionDeterministic(t *testing.T) {
	src := newSource(t)
	tgt := OverworldSpawnTarget()
	a := src.Sampler().FindSpawnPosition(tgt)
	b := src.Sampler().FindSpawnPosition(tgt)
	if a != b {
		t.Fatalf("FindSpawnPosition not deterministic: %+v vs %+v", a, b)
	}
}

// TestFindSpawnPositionMovesOffOrigin proves the search actually steers off (0,0) toward a
// lower-fitness column: with a continentalness gradient that is a poor match at origin but a
// good match away from it, the spiral must return a non-origin position. This is the bug-5
// fix (initial spawn is the climate spawn CHUNK, not hardcoded 0,0).
func TestFindSpawnPositionMovesOffOrigin(t *testing.T) {
	// Continentalness climbs from -1 at origin toward inland (~0.2..1) as X grows; the
	// spawnTarget prefers inland continentalness, so the best column is far from origin.
	s := Sampler{
		Temperature:     constFn(0.0),
		Humidity:        constFn(0.0),
		Continentalness: gradientFn{base: -1.0, scale: 1.0 / 1024.0}, // ocean(-1) at origin -> inland as X grows
		Erosion:         constFn(0.0),
		Depth:           constFn(0.0),
		Weirdness:       constFn(0.5), // inside point-2 weirdness span [0.16,1.0]
	}
	sp := s.FindSpawnPosition(OverworldSpawnTarget())
	if sp.X == 0 && sp.Z == 0 {
		t.Fatalf("FindSpawnPosition stuck at origin despite an off-origin climate optimum: %+v", sp)
	}
}
