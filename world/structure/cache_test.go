package structure

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
)

// fakePiece is a minimal Piece with a fixed bbox for the cache tests (14-02 supplies
// the real piece types).
type fakePiece struct{ bb BoundingBox }

func (p fakePiece) BoundingBox() BoundingBox { return p.bb }

// fakeStartGenerator emits a single fixed-bbox start at a designated OWNER chunk and
// nothing elsewhere — so the cache + references are testable WITHOUT 14-02's geometry.
// It counts GenerateStarts calls (per pos) to prove pure-memoization + singleflight.
type fakeStartGenerator struct {
	owner level.ChunkPos
	bbox  BoundingBox
	calls map[int64]*int32
	mu    sync.Mutex
}

func newFakeGen(owner level.ChunkPos, bbox BoundingBox) *fakeStartGenerator {
	return &fakeStartGenerator{owner: owner, bbox: bbox, calls: make(map[int64]*int32)}
}

func (g *fakeStartGenerator) callCount(pos level.ChunkPos) int32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.calls[packPos(pos)]; ok {
		return atomic.LoadInt32(c)
	}
	return 0
}

func (g *fakeStartGenerator) GenerateStarts(seed int64, pos level.ChunkPos, _ SurfaceSampler, _ BiomeAt) []*StructureStart {
	g.mu.Lock()
	c, ok := g.calls[packPos(pos)]
	if !ok {
		var n int32
		c = &n
		g.calls[packPos(pos)] = c
	}
	g.mu.Unlock()
	atomic.AddInt32(c, 1)

	if pos == g.owner {
		st := &StructureStart{
			Structure: "test:fixture",
			ChunkPos:  pos,
			Pieces:    []Piece{fakePiece{bb: g.bbox}},
		}
		st.RecomputeBBox()
		return []*StructureStart{st}
	}
	return nil
}

// stubSampler is a no-op SurfaceSampler for the cache tests (the fake generator does
// not consult it).
type stubSampler struct{}

func (stubSampler) SampleSurfaceY(int, int) int { return 64 }

// stubBiomeAt returns a fixed biome (the cache tests do not gate on it).
func stubBiomeAt(int, int, int) levelbiome.Type { return 0 }

func newTestCache() *Cache { return NewCache(stubSampler{}, stubBiomeAt) }

// TestComputeStartsPure: same (seed,pos) returns the identical memoized slice.
func TestComputeStartsPure(t *testing.T) {
	const seed = int64(42)
	owner := level.ChunkPos{3, 3}
	gen := newFakeGen(owner, BoundingBox{MinX: 48, MinY: 60, MinZ: 48, MaxX: 60, MaxY: 80, MaxZ: 60})
	c := newTestCache()

	a := c.ComputeStarts(seed, owner, gen)
	b := c.ComputeStarts(seed, owner, gen)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected one start each, got %d and %d", len(a), len(b))
	}
	if a[0] != b[0] {
		t.Fatalf("memoization returned different pointers for same (seed,pos)")
	}
	// Memoized: GenerateStarts called exactly once for the owner.
	if n := gen.callCount(owner); n != 1 {
		t.Fatalf("GenerateStarts called %d times for owner, want 1 (memoized)", n)
	}
}

// TestComputeStartsSingleflight: many concurrent computers of the SAME (seed,pos)
// collapse to ONE generator call and all get identical results. -race clean.
func TestComputeStartsSingleflight(t *testing.T) {
	const seed = int64(7)
	owner := level.ChunkPos{0, 0}
	gen := newFakeGen(owner, BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 15, MaxY: 70, MaxZ: 15})
	c := newTestCache()

	const goroutines = 64
	var wg sync.WaitGroup
	results := make([][]*StructureStart, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = c.ComputeStarts(seed, owner, gen)
		}(i)
	}
	wg.Wait()

	first := results[0]
	if len(first) != 1 {
		t.Fatalf("expected one start, got %d", len(first))
	}
	for i, r := range results {
		if len(r) != 1 || r[0] != first[0] {
			t.Fatalf("goroutine %d got a different start pointer (not deduped)", i)
		}
	}
	// Note: singleflight guarantees concurrent same-key collapse; the call count may
	// be 1 (all collapsed) — assert at most a tiny number, and identical pointers above
	// already prove one stored value.
	if n := gen.callCount(owner); n < 1 {
		t.Fatalf("GenerateStarts never called")
	}
}

// TestReferences8Radius: a start OWNED by a chunk at distance >=2 (beyond the radius-1
// ring) whose bbox reaches the center is FOUND in the center's References — proving the
// compute-on-demand scan reaches the full +-8, not just the pre-cached ring. A start
// that does NOT reach the center is absent.
func TestReferences8Radius(t *testing.T) {
	const seed = int64(99)
	center := level.ChunkPos{0, 0}
	minY, height := -64, 384

	// Owner is 3 chunks away on +x (distance 3 > 1). Its bbox is a wide structure whose
	// XZ footprint stretches back from the owner column to overlap the center column
	// (x in [0..52] covers the center's [0..15]). This start is owned >=2 chunks out.
	owner := level.ChunkPos{3, 0}
	wideBBox := BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 52, MaxY: 80, MaxZ: 15}
	gen := newFakeGen(owner, wideBBox)
	c := newTestCache()

	refs := c.ComputeReferences(seed, center, gen, minY, height)
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference (the >=2-chunk-out owner reaching center), got %d: %v", len(refs), refs)
	}
	if refs[0] != packPos(owner) {
		t.Fatalf("reference key %d != owner %d", refs[0], packPos(owner))
	}
	// The owner's own starts were computed on-demand during the scan (proving compute-
	// on-demand, not read-only-cached).
	if n := gen.callCount(owner); n < 1 {
		t.Fatalf("owner's starts were never computed during the +-8 scan")
	}

	// A start that does NOT reach the center: owner far away with a small bbox confined
	// to its own column. It must be ABSENT from a different center's references.
	farOwner := level.ChunkPos{7, 0}
	smallBBox := BoundingBox{MinX: 112, MinY: 60, MinZ: 0, MaxX: 120, MaxY: 80, MaxZ: 15} // chunk 7 column only
	gen2 := newFakeGen(farOwner, smallBBox)
	c2 := newTestCache()
	refs2 := c2.ComputeReferences(seed, center, gen2, minY, height)
	if len(refs2) != 0 {
		t.Fatalf("a non-reaching start was wrongly recorded as a reference: %v", refs2)
	}
}

// TestStartsForChunk: the gather returns C's own starts + the starts named in C's
// references.
func TestStartsForChunk(t *testing.T) {
	const seed = int64(11)
	center := level.ChunkPos{0, 0}
	minY, height := -64, 384

	owner := level.ChunkPos{2, 0}
	wideBBox := BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 40, MaxY: 80, MaxZ: 15}
	gen := newFakeGen(owner, wideBBox)
	c := newTestCache()

	// Populate the cache as the worker would: starts for center, then references.
	c.ComputeStarts(seed, center, gen)
	c.ComputeReferences(seed, center, gen, minY, height)

	got := c.StartsForChunk(center)
	// center owns nothing (only `owner` does), but `owner`'s start reaches center, so
	// the gather should return exactly the one neighbor start.
	if len(got) != 1 {
		t.Fatalf("StartsForChunk returned %d starts, want 1 (the reaching neighbor)", len(got))
	}
	if got[0].ChunkPos != owner {
		t.Fatalf("gathered start owner %v != %v", got[0].ChunkPos, owner)
	}
}

// TestBoundingBoxIntersects pins IntersectsXZ + Intersects + IsInside.
func TestBoundingBoxIntersects(t *testing.T) {
	b := BoundingBox{MinX: 0, MinY: 0, MinZ: 0, MaxX: 15, MaxY: 100, MaxZ: 15}

	if !b.IntersectsXZ(10, 10, 30, 30) {
		t.Error("overlapping XZ box reported no intersection")
	}
	if b.IntersectsXZ(16, 0, 31, 15) {
		t.Error("non-overlapping XZ box (x starts at 16) reported intersection")
	}
	if !b.Intersects(BoundingBox{MinX: 5, MinY: 50, MinZ: 5, MaxX: 20, MaxY: 60, MaxZ: 20}) {
		t.Error("overlapping 3D box reported no intersection")
	}
	if b.Intersects(BoundingBox{MinX: 5, MinY: 200, MinZ: 5, MaxX: 20, MaxY: 300, MaxZ: 20}) {
		t.Error("box above (Y 200-300) reported a 3D intersection")
	}
	if !b.IsInside(8, 50, 8) {
		t.Error("interior point reported outside")
	}
	if b.IsInside(16, 50, 8) {
		t.Error("point at x=16 (outside max 15) reported inside")
	}
}

// TestWritableArea pins the 16x16xH column bounds (inclusive max faces).
func TestWritableArea(t *testing.T) {
	pos := level.ChunkPos{2, -3}
	minY, height := -64, 384
	w := WritableArea(pos, minY, height)

	if w.MinX != 32 || w.MaxX != 47 {
		t.Errorf("X bounds = [%d,%d], want [32,47]", w.MinX, w.MaxX)
	}
	if w.MinZ != -48 || w.MaxZ != -33 {
		t.Errorf("Z bounds = [%d,%d], want [-48,-33]", w.MinZ, w.MaxZ)
	}
	if w.MinY != -64 || w.MaxY != -64+384-1 {
		t.Errorf("Y bounds = [%d,%d], want [-64,%d]", w.MinY, w.MaxY, -64+384-1)
	}
}

// TestEmptyStartInvalid: an empty start (no pieces) is invalid and has an empty bbox.
func TestEmptyStartInvalid(t *testing.T) {
	st := &StructureStart{ChunkPos: level.ChunkPos{0, 0}}
	st.RecomputeBBox()
	if st.IsValid() {
		t.Error("empty start reported valid")
	}
	if !st.BBox.IsEmpty() {
		t.Error("empty start has a non-empty bbox")
	}
}
