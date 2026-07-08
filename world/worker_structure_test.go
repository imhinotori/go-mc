package world

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/structure"
)

// decorateChunkVia builds a fully-decorated center chunk by terrain-generating its 3x3
// neighborhood and running Decorate over it (the same concrete path decorateSingle uses),
// returning the center chunk so a test can inspect its blocks / serialize it. It exercises
// the STRUCT-01 placeStructures hook (STARTS + REFERENCES run inside Decorate).
func decorateChunkVia(g *NoiseGenerator, pos level.ChunkPos) *level.Chunk {
	return decorateSingle(g, pos)
}

// TestStartsCachedForNeighborhood: decorating C populates the STRUCT-01 cache with C's
// STARTS plus the STARTS of every cell in C's +-8 references scan (incl. a >=2-chunk-out
// cell), each pure over (seed,pos). This proves the two-pass seam runs inside Decorate and
// the references scan reaches the FULL radius (compute-on-demand), not just radius-1.
func TestStartsCachedForNeighborhood(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	center := level.ChunkPos{0, 0}

	_ = decorateChunkVia(g, center)

	// C's own STARTS must be cached (ComputeStarts ran in placeStructures).
	if _, ok := g.structCache.StartsCachedFor(center); !ok {
		t.Fatalf("center %v STARTS not cached after Decorate", center)
	}
	// Every cell in the +-8 scan must have had its STARTS computed on-demand — check a
	// FAR cell (distance 5 > 1) to prove the scan reached beyond the radius-1 ring.
	far := level.ChunkPos{5, -5}
	if _, ok := g.structCache.StartsCachedFor(far); !ok {
		t.Fatalf("far cell %v STARTS not computed during C's +-8 references scan", far)
	}
	// And the maximal +-8 corner.
	corner := level.ChunkPos{8, 8}
	if _, ok := g.structCache.StartsCachedFor(corner); !ok {
		t.Fatalf("corner cell %v (+-8) STARTS not computed during the scan", corner)
	}
	// References for C must be cached too (even if empty with the inert generator).
	if _, ok := g.structCache.ReferencesCachedFor(center); !ok {
		t.Fatalf("center %v REFERENCES not cached after Decorate", center)
	}
}

// TestReferencesPopulatedOnDecorate: with a FAKE StartGenerator that emits a wide start
// owned by a neighbor reaching into C, decorating C records that neighbor in C's
// references (the cross-chunk discovery the PLACE pass will gather). Drives the cache
// directly (the generator is structCache-scoped, not wired into NoiseGenerator) so the
// references mechanism is exercised without 14-02's geometry.
func TestReferencesPopulatedOnDecorate(t *testing.T) {
	center := level.ChunkPos{0, 0}
	owner := level.ChunkPos{2, 0} // distance 2 > 1: a real cross-chunk discovery
	wide := structure.BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 40, MaxY: 80, MaxZ: 15}

	c := structure.NewCache(stubSurfaceSampler{}, stubWorldBiomeAt)
	gen := &fixedStartGenerator{owner: owner, bbox: wide}

	refs := c.ComputeReferences(noiseGenSeed, center, gen, testMinY, testSecs*16)
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference (neighbor start reaching C), got %d", len(refs))
	}

	got := c.StartsForChunk(center)
	if len(got) != 1 || got[0].ChunkPos != owner {
		t.Fatalf("StartsForChunk did not gather the referenced neighbor start: %v", got)
	}
}

// TestStructurePipelineRunsForCoveringStart: the structure pipeline is WIRED and LIVE.
// The original STRUCT-01-era assertion (that emitting a start produced byte-IDENTICAL
// chunks because the PLACE hook was empty) is obsolete: the place pass now runs, and a
// chunk covered by a start serializes structure start/reference metadata into its NBT, so
// its bytes DIFFER from the inert baseline. The load-bearing contract this test now pins is
// that the pipeline actually RAN for the covering generator (the cache populated) -- which
// is the seam a structure-pipeline regression would break.
func TestStructurePipelineRunsForCoveringStart(t *testing.T) {
	center := level.ChunkPos{1, 1}

	// A generator whose StartGenerator emits a start covering C. The fixture piece's
	// PostProcess is a no-op (it places no geometry itself), so any byte difference from
	// the inert baseline is the structure start/reference metadata the chunk now carries --
	// exactly what should be present once the pipeline is live.
	g2 := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	g2.structGen = &fixedStartGenerator{
		owner: center,
		bbox:  structure.BoundingBox{MinX: 16, MinY: 60, MinZ: 16, MaxX: 31, MaxY: 90, MaxZ: 31},
	}
	var withStarts bytes.Buffer
	if _, err := decorateChunkVia(g2, center).WriteTo(&withStarts); err != nil {
		t.Fatalf("with-starts WriteTo: %v", err)
	}

	// The cache WAS populated for the emitting generator (proving the pipeline ran).
	if _, ok := g2.structCache.StartsCachedFor(center); !ok {
		t.Fatalf("the structure pipeline did not run (no cached starts) for the emitting generator")
	}
}

// desertPyramidSeed/Chunk: the world seed + owning chunk where the production NoiseGenerator
// (real router + real GetBiome) places a desert pyramid (derived from the placement algorithm
// + the desert-biome gate; see world/structure desert_pyramid_test). The pyramid's 21x21
// footprint spans chunk (-58,32) + its +x/+z neighbors.
const (
	desertPyramidSeed   = int64(38)
	desertPyramidChunkX = -58
	desertPyramidChunkZ = 32
)

// countSandstone counts the sandstone-family blocks in a chunk (the pyramid's dominant
// material) — a cheap "did a pyramid land here" probe over the real pipeline output.
func countSandstone(ch *level.Chunk) int {
	ids := map[block.StateID]bool{
		block.ToStateID[block.Sandstone{}]:         true,
		block.ToStateID[block.CutSandstone{}]:      true,
		block.ToStateID[block.ChiseledSandstone{}]: true,
	}
	n := 0
	for si := range ch.Sections {
		s := &ch.Sections[si]
		for i := 0; i < 4096; i++ {
			if ids[s.GetBlock(i)] {
				n++
			}
		}
	}
	return n
}

// TestDesertPyramidPlacesInPipeline: the FULL production pipeline (real router/biome/STARTS/
// REFERENCES/PLACE) places the seed-38 desert pyramid into its owning chunk — the decorated
// chunk holds a large sandstone mass that an adjacent non-structure chunk does not. Proves the
// PLACE hook is wired end-to-end (STRUCT-01 + STRUCT-02 shaken down on one real structure).
func TestDesertPyramidPlacesInPipeline(t *testing.T) {
	g := NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY)
	owner := decorateChunkVia(g, level.ChunkPos{desertPyramidChunkX, desertPyramidChunkZ})

	got := countSandstone(owner)
	if got < 200 {
		t.Fatalf("owning chunk (%d,%d) holds only %d sandstone blocks — the pyramid did not place via the pipeline", desertPyramidChunkX, desertPyramidChunkZ, got)
	}

	// A chunk far from the pyramid (and not a structure chunk) holds essentially no sandstone
	// shell — confirming the mass above is the pyramid, not ambient terrain.
	far := countSandstone(decorateChunkVia(NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY), level.ChunkPos{desertPyramidChunkX + 4, desertPyramidChunkZ + 4}))
	if far >= got {
		t.Fatalf("far chunk sandstone (%d) >= pyramid chunk (%d): the mass is not the pyramid", far, got)
	}
}

// TestDesertPyramidCrossChunkIdempotent: the seed-38 pyramid spans multiple chunks. Decorating
// the SAME owning chunk twice through the real pipeline yields byte-identical output (the
// re-derivable piece RNG + position-clipped writes make the placement order-independent and
// non-duplicating, Pitfall #2). Also decorate an overlapping neighbor and confirm it receives
// its own slice of the pyramid (sandstone present there too) without disturbing determinism.
func TestDesertPyramidCrossChunkIdempotent(t *testing.T) {
	owner := level.ChunkPos{desertPyramidChunkX, desertPyramidChunkZ}

	var a, b bytes.Buffer
	if _, err := decorateChunkVia(NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY), owner).WriteTo(&a); err != nil {
		t.Fatalf("decorate run A: %v", err)
	}
	if _, err := decorateChunkVia(NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY), owner).WriteTo(&b); err != nil {
		t.Fatalf("decorate run B: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("re-decorating the pyramid owner produced different bytes (%d vs %d) — placement not idempotent", a.Len(), b.Len())
	}

	// The +x neighbor (-57,32) overlaps the pyramid footprint (bbox x reaches -908, i.e. chunk
	// -57); it must receive its own sandstone slice via the REFERENCES+clip, not the owner's.
	neighbor := decorateChunkVia(NewNoiseGenerator(desertPyramidSeed, testSecs, testMinY), level.ChunkPos{desertPyramidChunkX + 1, desertPyramidChunkZ})
	if countSandstone(neighbor) == 0 {
		t.Fatalf("overlapping neighbor (%d,%d) received no pyramid slice — cross-chunk references+clip failed", desertPyramidChunkX+1, desertPyramidChunkZ)
	}
}

// --- test fixtures for the world-package structure tests ---

// stubSurfaceSampler is a no-op SurfaceSampler for the world-package cache tests.
type stubSurfaceSampler struct{}

func (stubSurfaceSampler) SampleSurfaceY(int, int) int { return 64 }

// stubWorldBiomeAt returns a fixed biome.
func stubWorldBiomeAt(int, int, int) levelbiome.Type { return 0 }

// fixedStartPiece is a Piece with a fixed bbox. PostProcess is a no-op (these fixtures only
// exercise the STARTS/REFERENCES cache, never the PLACE pass).
type fixedStartPiece struct{ bb structure.BoundingBox }

func (p fixedStartPiece) BoundingBox() structure.BoundingBox { return p.bb }
func (p fixedStartPiece) PostProcess(structure.WorldGenView, structure.BoundingBox, level.ChunkPos, levelgen.RandomSource) {
}

// fixedStartGenerator emits one fixed-bbox start at a designated owner chunk (for the
// references / no-blocks tests). It writes nothing itself — the PLACE pass (14-02) would.
type fixedStartGenerator struct {
	owner level.ChunkPos
	bbox  structure.BoundingBox
}

func (g *fixedStartGenerator) GenerateStarts(_ int64, pos level.ChunkPos, _ structure.SurfaceSampler, _ structure.BiomeAt) []*structure.StructureStart {
	if pos != g.owner {
		return nil
	}
	st := &structure.StructureStart{
		Structure: "test:fixture",
		ChunkPos:  pos,
		Pieces:    []structure.Piece{fixedStartPiece{bb: g.bbox}},
	}
	st.RecomputeBBox()
	return []*structure.StructureStart{st}
}

// TestStructureCacheOrderIndependent pins the order-independence the STRUCTURE pipeline
// OWNS: the STARTS + REFERENCES the cache holds for a center are PURE over (seed,pos), so
// computing them in different orders (and recomputing the same +-8 scan from scratch)
// yields the IDENTICAL starts (same owner, same bbox) and the IDENTICAL references list.
// This is the structure-seam determinism contract — distinct from the pre-existing
// feature-decoration boundary behavior (which the Superflat 5x5 reorder gate covers).
//
// Driving the cache directly (with a deterministic fake start at a fixed neighbor)
// isolates the pipeline from the feature decoration, so a structure-seam regression is
// caught precisely.
func TestStructureCacheOrderIndependent(t *testing.T) {
	owner := level.ChunkPos{3, 1} // a >=2-chunk-out owner inside the +-8 window
	bbox := structure.BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 60, MaxY: 80, MaxZ: 30}
	makeGen := func() *fixedStartGenerator { return &fixedStartGenerator{owner: owner, bbox: bbox} }

	// Centers to populate, in two different orders. Each cache run is independent.
	centers := []level.ChunkPos{{0, 0}, {1, 1}, {-1, 2}, {2, -1}}

	runCache := func(order []level.ChunkPos) *structure.Cache {
		c := structure.NewCache(stubSurfaceSampler{}, stubWorldBiomeAt)
		gen := makeGen()
		for _, ctr := range order {
			c.ComputeStarts(noiseGenSeed, ctr, gen)
			c.ComputeReferences(noiseGenSeed, ctr, gen, testMinY, testSecs*16)
		}
		return c
	}

	a := runCache(centers)
	rev := make([]level.ChunkPos, len(centers))
	for i := range centers {
		rev[i] = centers[len(centers)-1-i]
	}
	b := runCache(rev)

	// For every center, both runs must agree on the gathered starts (owner + bbox) and on
	// the references list — pure over (seed,pos), order cannot matter.
	for _, ctr := range centers {
		sa := a.StartsForChunk(ctr)
		sb := b.StartsForChunk(ctr)
		if len(sa) != len(sb) {
			t.Fatalf("center %v: start count %d != %d across orders", ctr, len(sa), len(sb))
		}
		for i := range sa {
			if sa[i].ChunkPos != sb[i].ChunkPos || sa[i].BBox != sb[i].BBox {
				t.Fatalf("center %v start %d differs across orders: %+v vs %+v", ctr, i, sa[i], sb[i])
			}
		}
		ra, oka := a.ReferencesCachedFor(ctr)
		rb, okb := b.ReferencesCachedFor(ctr)
		if oka != okb || len(ra) != len(rb) {
			t.Fatalf("center %v references differ across orders: %v vs %v", ctr, ra, rb)
		}
	}
}
