package world

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
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

// TestStructurePipelineNoBlocksYet: the structure pipeline is WIRED but INERT — the
// emitted chunk bytes are IDENTICAL whether placeStructures runs with the inert
// NoopStartGenerator OR with a FAKE generator that emits real starts. Because the PLACE
// hook is EMPTY this plan (14-02 fills it), even a non-empty StartGenerator writes ZERO
// blocks — the pipeline exists and runs (STARTS/REFERENCES populate the cache) but no
// geometry lands. This is the byte-stability proof for STRUCT-01.
func TestStructurePipelineNoBlocksYet(t *testing.T) {
	center := level.ChunkPos{1, 1}

	// Baseline: the production NoiseGenerator (inert NoopStartGenerator).
	g1 := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	var base bytes.Buffer
	if _, err := decorateChunkVia(g1, center).WriteTo(&base); err != nil {
		t.Fatalf("baseline WriteTo: %v", err)
	}

	// A second generator whose StartGenerator EMITS a wide start covering C — but the
	// PLACE hook is empty, so it must write nothing and produce identical bytes.
	g2 := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	g2.structGen = &fixedStartGenerator{
		owner: center,
		bbox:  structure.BoundingBox{MinX: 16, MinY: 60, MinZ: 16, MaxX: 31, MaxY: 90, MaxZ: 31},
	}
	var withStarts bytes.Buffer
	if _, err := decorateChunkVia(g2, center).WriteTo(&withStarts); err != nil {
		t.Fatalf("with-starts WriteTo: %v", err)
	}

	if !bytes.Equal(base.Bytes(), withStarts.Bytes()) {
		t.Fatalf("structure pipeline placed blocks (bytes differ by %d): the PLACE hook must be EMPTY this plan",
			len(base.Bytes())-len(withStarts.Bytes()))
	}

	// And the cache WAS populated for the emitting generator (proving the pipeline ran,
	// it just did not place).
	if _, ok := g2.structCache.StartsCachedFor(center); !ok {
		t.Fatalf("the structure pipeline did not run (no cached starts) for the emitting generator")
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
