package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// build3x3 makes a Neighborhood centered at `center` over 9 empty chunks (sized minY/height),
// for the decoration trace tests. The empty chunks have live worldgen heightmaps (EmptyChunk
// initializes them), so feature writes + reads work.
func build3x3(center [2]int, minY, height int) *Neighborhood {
	secs := height / 16
	chunks := make(map[int64]*level.Chunk, 9)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			cp := level.ChunkPos{int32(center[0] + dx), int32(center[1] + dz)}
			chunks[packPos(cp)] = level.EmptyChunk(secs)
		}
	}
	return newNeighborhood(level.ChunkPos{int32(center[0]), int32(center[1])}, chunks, minY, height)
}

// TestFeatureSeedTrace is the FEAT-02 acceptance: for a known seed + chunk, the per-feature
// (step, globalIndex, featureSeed) sequence applyBiomeDecoration drives matches a HAND-DERIVED
// expected — proving the SetFeatureSeed(decoSeed, globalIndex, step) discipline (block origin,
// global cross-biome index, sorted ascending, 10000*step). It uses a SYNTHETIC feature graph
// with controlled indices so the expected sequence is fully hand-computable, independent of the
// real embed.
func TestFeatureSeedTrace(t *testing.T) {
	const seed = int64(0x5EED1234)
	const minY, height = -64, 384
	center := [2]int{3, -5}

	// Two synthetic biomes sharing a feature, at two steps:
	//   biomeA step0 = [p0, p1]   step2 = [p2]
	//   biomeB step0 = [p1, p3]   step2 = [p2]   (p1 shared at step0, p2 shared at step2)
	// FeatureSorter step0 order: p0, p1, p3 (indices 0,1,2); step2: p2 (index 0).
	p0 := &feature.PlacedFeature{ID: "p0", Feature: &feature.ConfiguredFeature{Type: "noop"}}
	p1 := &feature.PlacedFeature{ID: "p1", Feature: &feature.ConfiguredFeature{Type: "noop"}}
	p2 := &feature.PlacedFeature{ID: "p2", Feature: &feature.ConfiguredFeature{Type: "noop"}}
	p3 := &feature.PlacedFeature{ID: "p3", Feature: &feature.ConfiguredFeature{Type: "noop"}}

	bA := make([][]*feature.PlacedFeature, feature.DecorationStepCount)
	bB := make([][]*feature.PlacedFeature, feature.DecorationStepCount)
	bA[0] = []*feature.PlacedFeature{p0, p1}
	bA[2] = []*feature.PlacedFeature{p2}
	bB[0] = []*feature.PlacedFeature{p1, p3}
	bB[2] = []*feature.PlacedFeature{p2}

	sorter, err := feature.BuildFeaturesPerStep([][][]*feature.PlacedFeature{bA, bB})
	if err != nil {
		t.Fatalf("BuildFeaturesPerStep: %v", err)
	}

	// Pick two distinct synthetic biome.Types to back the retained set.
	var biomeA, biomeB levelbiome.Type
	if err := biomeA.UnmarshalText([]byte("minecraft:plains")); err != nil {
		t.Fatalf("plains: %v", err)
	}
	if err := biomeB.UnmarshalText([]byte("minecraft:desert")); err != nil {
		t.Fatalf("desert: %v", err)
	}

	data := &decorationData{
		sorter: sorter,
		biomeFeatures: map[levelbiome.Type][][]*feature.PlacedFeature{
			biomeA: bA,
			biomeB: bB,
		},
		allowedBiomes: map[*feature.PlacedFeature]map[levelbiome.Type]bool{
			p0: {biomeA: true},
			p1: {biomeA: true, biomeB: true},
			p2: {biomeA: true, biomeB: true},
			p3: {biomeB: true},
		},
	}

	view := build3x3(center, minY, height)
	// The retained biome set is fixed to {biomeA, biomeB} for this synthetic test (we bypass
	// the noise biome source so the expected feature set is deterministic).
	biomes := []levelbiome.Type{biomeA, biomeB}
	ctx := newPlacementContext(view, minY, height, func(_, _, _ int) levelbiome.Type { return biomeA })

	var invocations []featureInvocation
	makePlacer := func(pf *feature.PlacedFeature) placement.PlacerFunc {
		var cf *feature.ConfiguredFeature
		if pf != nil {
			cf = pf.Feature
		}
		return newConfiguredPlacer(cf, view, data.registry, block.StateID(0), false, &invocations)
	}

	trace := &decorationTrace{}
	wg := levelgen.NewWorldgenRandom(seed)
	applyBiomeDecoration(view, biomes, data, ctx, wg, seed, makePlacer, trace)

	// --- Hand-derived oracle ---
	// decoSeed via the public helper (independent rng instance).
	oracleWG := levelgen.NewWorldgenRandom(seed)
	wantDeco := oracleWG.SetDecorationSeed(seed, center[0]*16, center[1]*16)

	// Expected ordered (step, globalIndex) sequence:
	//   step 0: indices {0(p0),1(p1),2(p3)} sorted -> 0,1,2
	//   step 2: index   {0(p2)}             sorted -> 0
	type si struct{ step, idx int }
	wantSeq := []si{{0, 0}, {0, 1}, {0, 2}, {2, 0}}

	if len(trace.entries) != len(wantSeq) {
		t.Fatalf("trace length = %d, want %d (%+v)", len(trace.entries), len(wantSeq), trace.entries)
	}
	for i, e := range trace.entries {
		w := wantSeq[i]
		if e.step != w.step || e.globalIndex != w.idx {
			t.Fatalf("entry %d = (step %d, idx %d), want (step %d, idx %d)", i, e.step, e.globalIndex, w.step, w.idx)
		}
		if e.decoSeed != wantDeco {
			t.Fatalf("entry %d decoSeed = %d, want %d", i, e.decoSeed, wantDeco)
		}
		// The determinism contract: featureSeed = decoSeed + globalIndex + 10000*step.
		wantFeat := wantDeco + int64(e.globalIndex) + int64(10000*e.step)
		if e.featureSeed != wantFeat {
			t.Fatalf("entry %d featureSeed = %d, want %d (decoSeed+idx+10000*step)", i, e.featureSeed, wantFeat)
		}
	}

	// And each feature was actually invoked (the no-op placer recorded it) at the chunk
	// block origin (no modifiers -> place at origin).
	if len(invocations) != len(wantSeq) {
		t.Fatalf("placer invoked %d times, want %d", len(invocations), len(wantSeq))
	}
	wantOriginX, wantOriginZ := center[0]*16, center[1]*16
	for i, inv := range invocations {
		if inv.pos.X != wantOriginX || inv.pos.Z != wantOriginZ {
			t.Fatalf("invocation %d at (%d,%d), want origin (%d,%d)", i, inv.pos.X, inv.pos.Z, wantOriginX, wantOriginZ)
		}
	}
}

// TestTraceOrderIndependent asserts the per-feature trace is independent of the biome
// iteration order in the retained set: decorating with {A,B} and {B,A} yields the IDENTICAL
// (step, idx, featureSeed) sequence (the sorted global index is order-free).
func TestTraceOrderIndependent(t *testing.T) {
	const seed = int64(0xABCDEF)
	const minY, height = -64, 384
	center := [2]int{0, 0}

	p0 := &feature.PlacedFeature{ID: "p0", Feature: &feature.ConfiguredFeature{Type: "noop"}}
	p1 := &feature.PlacedFeature{ID: "p1", Feature: &feature.ConfiguredFeature{Type: "noop"}}
	bA := make([][]*feature.PlacedFeature, feature.DecorationStepCount)
	bB := make([][]*feature.PlacedFeature, feature.DecorationStepCount)
	bA[0] = []*feature.PlacedFeature{p0, p1}
	bB[0] = []*feature.PlacedFeature{p0, p1}
	sorter, err := feature.BuildFeaturesPerStep([][][]*feature.PlacedFeature{bA, bB})
	if err != nil {
		t.Fatalf("sorter: %v", err)
	}
	var a, b levelbiome.Type
	_ = a.UnmarshalText([]byte("minecraft:plains"))
	_ = b.UnmarshalText([]byte("minecraft:desert"))
	data := &decorationData{
		sorter:        sorter,
		biomeFeatures: map[levelbiome.Type][][]*feature.PlacedFeature{a: bA, b: bB},
		allowedBiomes: map[*feature.PlacedFeature]map[levelbiome.Type]bool{
			p0: {a: true, b: true}, p1: {a: true, b: true},
		},
	}
	run := func(biomes []levelbiome.Type) []traceEntry {
		view := build3x3(center, minY, height)
		ctx := newPlacementContext(view, minY, height, func(_, _, _ int) levelbiome.Type { return a })
		mk := func(pf *feature.PlacedFeature) placement.PlacerFunc {
			return newConfiguredPlacer(pf.Feature, view, data.registry, block.StateID(0), false, nil)
		}
		tr := &decorationTrace{}
		applyBiomeDecoration(view, biomes, data, ctx, levelgen.NewWorldgenRandom(seed), seed, mk, tr)
		return tr.entries
	}
	t1 := run([]levelbiome.Type{a, b})
	t2 := run([]levelbiome.Type{b, a})
	if len(t1) != len(t2) {
		t.Fatalf("trace length differs by biome order: %d vs %d", len(t1), len(t2))
	}
	for i := range t1 {
		if t1[i] != t2[i] {
			t.Fatalf("trace entry %d differs by biome order: %+v vs %+v", i, t1[i], t2[i])
		}
	}
}

// TestSorterIntegration runs the REAL buildDecorationData (the full embedded roster + the
// FeatureSorter over all biomes) and asserts a placed_feature shared by two real overworld
// biomes (ore_dirt, in essentially every overworld biome's UNDERGROUND_ORES step) is
// DAG-deduped to ONE *PlacedFeature pointer AND mapped to ONE global per-step index — the
// cross-biome dedup the trace's index discipline depends on.
func TestSorterIntegration(t *testing.T) {
	data, err := buildDecorationData()
	if err != nil {
		t.Fatalf("buildDecorationData: %v", err)
	}

	var plains, forest levelbiome.Type
	if err := plains.UnmarshalText([]byte("minecraft:plains")); err != nil {
		t.Fatalf("plains: %v", err)
	}
	if err := forest.UnmarshalText([]byte("minecraft:forest")); err != nil {
		t.Fatalf("forest: %v", err)
	}

	findOreDirt := func(b levelbiome.Type) *feature.PlacedFeature {
		for _, stepList := range data.biomeFeatures[b] {
			for _, pf := range stepList {
				if pf != nil && pf.ID == "minecraft:ore_dirt" {
					return pf
				}
			}
		}
		return nil
	}
	a := findOreDirt(plains)
	c := findOreDirt(forest)
	if a == nil || c == nil {
		t.Fatalf("ore_dirt not found in plains(%v)/forest(%v) — embed changed?", a != nil, c != nil)
	}
	// DAG-dedup: the SAME pointer across both biomes (the Registry caches by id).
	if a != c {
		t.Fatalf("ore_dirt resolved to two distinct *PlacedFeature pointers across biomes (no DAG-dedup)")
	}
	// ONE global per-step index.
	ia, oka := data.sorter.Index(a)
	if !oka {
		t.Fatalf("ore_dirt has no global index in the FeatureSorter")
	}
	ic, _ := data.sorter.Index(c)
	if ia != ic {
		t.Fatalf("ore_dirt mapped to two global indices %d/%d (dedup failed)", ia, ic)
	}
}

// TestTestSetBlockFlows proves the single test-only feature type writes through the
// Neighborhood AND the write updates the live worldgen heightmap (the seam the real Phase-12
// bodies will use). It runs a synthetic feature of type "test_set_block" with no modifiers
// (so it places at the block origin) and asserts the block landed + the heightmap rose.
func TestTestSetBlockFlows(t *testing.T) {
	const seed = int64(1)
	const minY, height = -64, 384
	center := [2]int{0, 0}

	pf := &feature.PlacedFeature{ID: "t", Feature: &feature.ConfiguredFeature{Type: testSetBlockType}}
	steps := make([][]*feature.PlacedFeature, feature.DecorationStepCount)
	steps[0] = []*feature.PlacedFeature{pf}
	sorter, err := feature.BuildFeaturesPerStep([][][]*feature.PlacedFeature{steps})
	if err != nil {
		t.Fatalf("sorter: %v", err)
	}
	var pl levelbiome.Type
	_ = pl.UnmarshalText([]byte("minecraft:plains"))
	data := &decorationData{
		sorter:        sorter,
		biomeFeatures: map[levelbiome.Type][][]*feature.PlacedFeature{pl: steps},
		allowedBiomes: map[*feature.PlacedFeature]map[levelbiome.Type]bool{pf: {pl: true}},
	}

	view := build3x3(center, minY, height)
	ctx := newPlacementContext(view, minY, height, func(_, _, _ int) levelbiome.Type { return pl })
	stone := block.ToStateID[block.Stone{}]
	mk := func(p *feature.PlacedFeature) placement.PlacerFunc {
		return newConfiguredPlacer(p.Feature, view, data.registry, stone, true, nil)
	}
	applyBiomeDecoration(view, []levelbiome.Type{pl}, data, ctx, levelgen.NewWorldgenRandom(seed), seed, mk, nil)

	// The write lands at the block origin (0,0) of the center chunk, at y = origin.Y = 0.
	if got := view.GetBlock(0, 0, 0); got != stone {
		t.Fatalf("test_set_block did not write through the view: GetBlock(0,0,0)=%v want %v", got, stone)
	}
	// The live worldgen heightmap rose to cover the new block: WorldSurfaceWG at column (0,0)
	// reports first-air-above-top relative to minY, so its world Y must be > 0 (the placed block).
	ch, _ := view.chunkAt(0, 0)
	if ch == nil || ch.HeightMaps.WorldSurfaceWG == nil {
		t.Fatalf("center chunk missing WorldSurfaceWG heightmap")
	}
	if worldY := ch.HeightMaps.WorldSurfaceWG.Get(0) + minY; worldY <= 0 {
		t.Fatalf("worldgen heightmap not updated by the write: world Y = %d, want > 0", worldY)
	}
}

// TestFeatureBodiesProduceBlocks is the FINAL Phase-12 live-decoration proof: with ALL
// FEAT-03 bodies registered (ore/simple_block/random_patch from 12-02; selectors +
// pile/fallen/vegetation_patch from 12-03), running the REAL applyBiomeDecoration over a
// real overworld biome's full feature roster — on a stone-filled 3x3 — actually WRITES
// feature blocks (ore blobs replacing stone, and/or vegetation/cover above the surface)
// through the live pipeline, not just in unit tests. It proves the bodies are wired into
// the dispatch and place real blocks end-to-end.
func TestFeatureBodiesProduceBlocks(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}

	data, err := buildDecorationData()
	if err != nil {
		t.Fatalf("buildDecorationData: %v", err)
	}

	var plains levelbiome.Type
	if err := plains.UnmarshalText([]byte("minecraft:plains")); err != nil {
		t.Fatalf("plains: %v", err)
	}
	steps, ok := data.biomeFeatures[plains]
	if !ok {
		t.Fatalf("plains has no decoration features in the embed")
	}
	// Sanity: plains must reference at least one ore (UNDERGROUND_ORES) so the ore body
	// has stone to replace — the live-write we assert.
	foundOre := false
	for _, stepList := range steps {
		for _, pf := range stepList {
			if pf != nil && pf.Feature != nil && pf.Feature.Type == "ore" {
				foundOre = true
			}
		}
	}
	if !foundOre {
		t.Fatalf("plains references no ore feature — cannot prove a live ore write")
	}

	// A 3x3 of stone-filled chunks: a solid column from minY up to y=64 in every chunk,
	// so the ore body (which only replaces base-stone targets) and the heightmap-projected
	// surface features have real ground to act on.
	view := build3x3(center, minY, height)
	stone := block.ToStateID[block.Stone{}]
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			bx, bz := (center[0]+dx)*16, (center[1]+dz)*16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y < 64; y++ {
						view.SetBlock(bx+lx, y, bz+lz, stone)
					}
				}
			}
		}
	}

	ctx := newPlacementContext(view, minY, height, func(_, _, _ int) levelbiome.Type { return plains })
	mk := func(pf *feature.PlacedFeature) placement.PlacerFunc {
		var cf *feature.ConfiguredFeature
		if pf != nil {
			cf = pf.Feature
		}
		return newConfiguredPlacer(cf, view, data.registry, block.StateID(0), false, nil)
	}
	applyBiomeDecoration(view, []levelbiome.Type{plains}, data, ctx,
		levelgen.NewWorldgenRandom(0x12345), 0x12345, mk, nil)

	// Scan the center chunk's stone column: at least one cell must now be a NON-stone,
	// NON-air block — an ore blob the ore body wrote by replacing base stone (or another
	// feature block). If the bodies were not live (all no-ops), the column stays pure
	// stone and this fails.
	air := block.ToStateID[block.Air{}]
	changed := 0
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := minY; y < 64; y++ {
				st := view.GetBlock(lx, y, lz)
				if st != stone && st != air {
					changed++
				}
			}
		}
	}
	if changed == 0 {
		t.Fatalf("no feature blocks written by the live decoration — the FEAT-03 bodies are not placing through applyBiomeDecoration")
	}
	t.Logf("live decoration wrote %d non-stone feature blocks into the center chunk", changed)
}
