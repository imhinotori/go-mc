package structure

import (
	"testing"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level"
)

// village test fixtures: a fixed seed whose plains village lands at the ALGORITHM-DERIVED
// anchor chunk (15,2) — verified via PotentialStructureChunk(salt 10387312, spacing 34,
// separation 8), NOT eyeballed — spanning multiple chunks above sea level. A flat surface
// stub (y=72) + a constant plains biome make the placement router-independent + deterministic
// (the same oracle pattern 14-02/15-03 used).
const (
	villageSeed     = int64(0)
	villageAnchorCx = 15
	villageAnchorCz = 2
	villageSurfaceY = 72
)

// flatVillageSampler is the router-independent surface stub (a constant Y, like the desert
// pyramid test's desertSurfaceSampler) — so the village fingerprint depends ONLY on (seed,pos),
// not on the live noise router.
type flatVillageSampler struct{}

func (flatVillageSampler) SampleSurfaceY(int, int) int { return villageSurfaceY }

// villageBiomeType resolves a biome id to its runtime Type (the desert_pyramid_test pattern).
func villageBiomeType(t *testing.T, id string) levelbiome.Type {
	t.Helper()
	for i := 0; i < 1024; i++ {
		if levelbiome.Type(i).String() == id {
			return levelbiome.Type(i)
		}
	}
	t.Fatalf("no biome type for %q", id)
	return 0
}

// plainsBiomeAt is the constant-plains biome stub (a plains village is allowed everywhere here).
func plainsBiomeAt(t *testing.T) BiomeAt {
	bt := villageBiomeType(t, "minecraft:plains")
	return func(int, int, int) levelbiome.Type { return bt }
}

// newVillageGen builds the generator (panics-as-test-fatal on a build-data error).
func newVillageGen(t *testing.T) StartGenerator {
	t.Helper()
	g, err := NewVillageStartGen()
	if err != nil {
		t.Fatalf("NewVillageStartGen: %v", err)
	}
	return g
}

// TestVillagePlacementConstants pins the verified villages.json structure_set constants (salt
// 10387312 / spacing 34 / separation 8) — the placement gate is the half of "vanilla positions".
// NewVillageStartGen FAILS LOUD if villages.json disagrees, so its success already verifies them;
// this test re-asserts via the placement struct for an explicit regression pin.
func TestVillagePlacementConstants(t *testing.T) {
	set, err := LoadStructureSet("minecraft:villages")
	if err != nil {
		t.Fatalf("LoadStructureSet(villages): %v", err)
	}
	if set.Placement.Salt != 10387312 {
		t.Errorf("village salt = %d; want 10387312", set.Placement.Salt)
	}
	if set.Placement.Spacing != 34 {
		t.Errorf("village spacing = %d; want 34", set.Placement.Spacing)
	}
	if set.Placement.Separation != 8 {
		t.Errorf("village separation = %d; want 8", set.Placement.Separation)
	}
	if len(set.Structures) != 5 {
		t.Errorf("villages set has %d structures; want 5 (the biome variants)", len(set.Structures))
	}
}

// TestVillageLandsAtAlgorithmChunk pins that the fixed seed lands a plains village at the
// ALGORITHM-DERIVED anchor chunk (15,2) — the chunk that IS its own region's potential start
// chunk (PotentialStructureChunk), NOT an eyeballed value. The start is valid + multi-piece.
func TestVillageLandsAtAlgorithmChunk(t *testing.T) {
	// The anchor MUST be the algorithm's potential structure chunk (the gate decision).
	p := RandomSpreadStructurePlacement{Spacing: 34, Separation: 8, Salt: 10387312, SpreadType: SpreadLinear}
	if !p.IsStructureChunk(villageSeed, villageAnchorCx, villageAnchorCz) {
		t.Fatalf("anchor (%d,%d) is not the algorithm's structure chunk for seed %d", villageAnchorCx, villageAnchorCz, villageSeed)
	}

	g := newVillageGen(t)
	pos := level.ChunkPos{villageAnchorCx, villageAnchorCz}
	starts := g.GenerateStarts(villageSeed, pos, flatVillageSampler{}, plainsBiomeAt(t))
	if len(starts) != 1 {
		t.Fatalf("GenerateStarts returned %d starts; want 1 village", len(starts))
	}
	st := starts[0]
	if !st.IsValid() {
		t.Fatalf("village start is invalid (no pieces)")
	}
	if st.Structure != "minecraft:village_plains" {
		t.Errorf("village structure = %q; want minecraft:village_plains", st.Structure)
	}
	if len(st.Pieces) < 2 {
		t.Errorf("village has %d pieces; want a multi-piece graph", len(st.Pieces))
	}
}

// TestVillageSpansMultipleChunks pins that the village's bbox spans >=2 chunks on each axis
// (the ±8 REFERENCES scan is what finds it from neighbor chunks) — a single-chunk village would
// be a degenerate assembly.
func TestVillageSpansMultipleChunks(t *testing.T) {
	g := newVillageGen(t)
	pos := level.ChunkPos{villageAnchorCx, villageAnchorCz}
	starts := g.GenerateStarts(villageSeed, pos, flatVillageSampler{}, plainsBiomeAt(t))
	if len(starts) != 1 {
		t.Fatalf("GenerateStarts returned %d starts; want 1", len(starts))
	}
	bb := starts[0].BBox
	spanCx := (bb.MaxX >> 4) - (bb.MinX >> 4) + 1
	spanCz := (bb.MaxZ >> 4) - (bb.MinZ >> 4) + 1
	if spanCx < 2 || spanCz < 2 {
		t.Errorf("village spans %dx%d chunks; want >=2 on each axis (bbox %+v)", spanCx, spanCz, bb)
	}
}

// TestVillageBiomeGateLoadBearing proves the biome gate is LOAD-BEARING (NO accept-by-default):
// the same structure chunk with an OUT-OF-BIOME origin (a biome in NO village's has_structure
// set, e.g. the ocean) produces NO village start. Dropping the gate would place a village in
// the ocean.
func TestVillageBiomeGateLoadBearing(t *testing.T) {
	g := newVillageGen(t)
	pos := level.ChunkPos{villageAnchorCx, villageAnchorCz}

	// An ocean biome is in NO village variant's has_structure set -> no village.
	oceanType := villageBiomeType(t, "minecraft:ocean")
	oceanBiomeAt := func(int, int, int) levelbiome.Type { return oceanType }

	starts := g.GenerateStarts(villageSeed, pos, flatVillageSampler{}, oceanBiomeAt)
	if len(starts) != 0 {
		t.Fatalf("out-of-biome (ocean) origin produced %d village starts; want 0 (accept-by-default leak)", len(starts))
	}
}

// TestVillageDeterministic proves the village is PURE over (seed,pos): two assemblies of the
// same start produce byte-identical piece graphs (the fingerprint of every piece bbox +
// structure). The piece RNG (setLargeFeatureSeed) is re-derivable, so this is the determinism
// contract (T-16-05).
func TestVillageDeterministic(t *testing.T) {
	g := newVillageGen(t)
	pos := level.ChunkPos{villageAnchorCx, villageAnchorCz}

	assemble := func() *StructureStart {
		starts := g.GenerateStarts(villageSeed, pos, flatVillageSampler{}, plainsBiomeAt(t))
		if len(starts) != 1 {
			t.Fatalf("GenerateStarts returned %d starts; want 1", len(starts))
		}
		return starts[0]
	}
	a := assemble()
	b := assemble()
	if a.Structure != b.Structure {
		t.Fatalf("two assemblies chose different variants: %q vs %q", a.Structure, b.Structure)
	}
	if len(a.Pieces) != len(b.Pieces) {
		t.Fatalf("two assemblies produced different piece counts: %d vs %d", len(a.Pieces), len(b.Pieces))
	}
	if a.BBox != b.BBox {
		t.Fatalf("two assemblies produced different bboxes: %+v vs %+v", a.BBox, b.BBox)
	}
	for i := range a.Pieces {
		if a.Pieces[i].BoundingBox() != b.Pieces[i].BoundingBox() {
			t.Fatalf("piece %d bbox diverges: %+v vs %+v (not pure over (seed,pos))",
				i, a.Pieces[i].BoundingBox(), b.Pieces[i].BoundingBox())
		}
	}
}

// TestVillageCrossChunkIdempotent proves the cross-chunk union: placing the village piece-by-
// piece into TWO adjacent chunks' writable boxes writes each chunk's slice ONLY, and the union
// equals the whole placement (placeInChunk idempotent, no double-write — Pitfall #2 / T-16-05).
func TestVillageCrossChunkIdempotent(t *testing.T) {
	g := newVillageGen(t)
	pos := level.ChunkPos{villageAnchorCx, villageAnchorCz}
	starts := g.GenerateStarts(villageSeed, pos, flatVillageSampler{}, plainsBiomeAt(t))
	if len(starts) != 1 {
		t.Fatalf("GenerateStarts returned %d starts; want 1", len(starts))
	}
	start := starts[0]

	// Place the WHOLE village against an unclipped box (the completeness oracle).
	whole := newMapView()
	bigVillageBox := BoundingBox{MinX: -1 << 20, MinY: -64, MinZ: -1 << 20, MaxX: 1 << 20, MaxY: 320, MaxZ: 1 << 20}
	placeInChunk(start, whole, bigVillageBox, pos, villageSeed)

	// Now place chunk-by-chunk over the village's whole chunk footprint + UNION them; each
	// chunk's write must land ONLY inside its own column.
	bb := start.BBox
	minCx, maxCx := bb.MinX>>4, bb.MaxX>>4
	minCz, maxCz := bb.MinZ>>4, bb.MaxZ>>4
	union := newMapView()
	for cx := minCx; cx <= maxCx; cx++ {
		for cz := minCz; cz <= maxCz; cz++ {
			c := level.ChunkPos{int32(cx), int32(cz)}
			box := WritableArea(c, -64, 384)
			single := newMapView()
			placeInChunk(start, single, box, c, villageSeed)
			for ppos, st := range single.blocks {
				if ppos[0]>>4 != cx || ppos[2]>>4 != cz {
					t.Fatalf("placeInChunk(%v) leaked a write to chunk (%d,%d)", c, ppos[0]>>4, ppos[2]>>4)
				}
				union.blocks[ppos] = st
			}
		}
	}

	// The union must equal the whole placement exactly (idempotent, no double-write / truncation).
	if len(union.blocks) != len(whole.blocks) {
		t.Fatalf("cross-chunk union wrote %d blocks; whole wrote %d (truncation or double-write)", len(union.blocks), len(whole.blocks))
	}
	for ppos, st := range whole.blocks {
		if union.blocks[ppos] != st {
			t.Fatalf("cross-chunk union diverges at %v: %v vs whole %v", ppos, union.blocks[ppos], st)
		}
	}
}
