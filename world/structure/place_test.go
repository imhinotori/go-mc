package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// spanPiece is a test Piece spanning x[0..31] (two chunks: (0,0) and (1,0)). Its PostProcess
// fills its whole bbox at y=70 with sandstone via generateBox — clipped by placeBlock to the
// passed writable box, so placing it against chunk (0,0)'s box writes ONLY x[0..15], and
// against (1,0)'s box ONLY x[16..31]. The union is the complete strip, no overlap.
type spanPiece struct {
	piece *StructurePiece
	sand  block.StateID
}

func newSpanPiece(sand block.StateID) spanPiece {
	p := &StructurePiece{bbox: BoundingBox{MinX: 0, MinY: 64, MinZ: 0, MaxX: 31, MaxY: 80, MaxZ: 15}}
	p.setOrientation(block.North, false)
	return spanPiece{piece: p, sand: sand}
}

func (s spanPiece) BoundingBox() BoundingBox { return s.piece.bbox }

func (s spanPiece) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, _ levelgen.RandomSource) {
	s.piece.generateBox(view, box, 0, 70, 0, 31, 70, 15, s.sand, s.sand, false)
}

// TestPlaceInChunkCrossChunk: a chunk-spanning piece placed from chunk (0,0)'s writable box
// writes ONLY (0,0)'s slice; placed from (1,0)'s box ONLY (1,0)'s slice; the UNION is the
// complete strip with NO overlap / double-write / truncation (Pitfall #2, idempotent).
func TestPlaceInChunkCrossChunk(t *testing.T) {
	sand := sandstoneID(t)
	piece := newSpanPiece(sand)
	start := &StructureStart{Structure: "test:span", ChunkPos: level.ChunkPos{0, 0}, Pieces: []Piece{piece}}
	start.RecomputeBBox()

	box0 := WritableArea(level.ChunkPos{0, 0}, 64, 32) // x[0..15]
	box1 := WritableArea(level.ChunkPos{1, 0}, 64, 32) // x[16..31]

	view := newMapView()
	placeInChunk(start, view, box0, level.ChunkPos{0, 0}, 0)

	// Only x[0..15] at y=70, z[0..15] should be written so far (16*16 = 256 cells).
	if view.writes != 256 {
		t.Fatalf("placeInChunk(box0) wrote %d cells, want 256 (chunk 0,0 slice only)", view.writes)
	}
	if !block.IsAir(view.GetBlock(16, 70, 0)) {
		t.Fatalf("placeInChunk(box0) leaked into chunk (1,0) at x=16")
	}
	if view.GetBlock(15, 70, 0) != sand {
		t.Fatalf("placeInChunk(box0) did not write the (0,0) edge at x=15")
	}

	// Now place the SAME start from chunk (1,0): writes ONLY x[16..31] (another 256 cells).
	placeInChunk(start, view, box1, level.ChunkPos{1, 0}, 0)
	if view.writes != 512 {
		t.Fatalf("after placeInChunk(box1) total writes = %d, want 512 (no overlap/double-write)", view.writes)
	}

	// The union must be the complete strip x[0..31] z[0..15] at y=70, all sandstone.
	for x := 0; x <= 31; x++ {
		for z := 0; z <= 15; z++ {
			if got := view.GetBlock(x, 70, z); got != sand {
				t.Fatalf("union cell (%d,70,%d) = %v, want sandstone (truncation/gap)", x, z, got)
			}
		}
	}
}

// TestPlaceInChunkIdempotent: placing the SAME start into the SAME chunk twice yields the
// SAME blocks (no double-write corruption) and re-deriving the RNG over (seed,ownerChunk)
// makes the placement order-independent.
func TestPlaceInChunkIdempotent(t *testing.T) {
	sand := sandstoneID(t)
	piece := newSpanPiece(sand)
	start := &StructureStart{Structure: "test:span", ChunkPos: level.ChunkPos{0, 0}, Pieces: []Piece{piece}}
	start.RecomputeBBox()
	box0 := WritableArea(level.ChunkPos{0, 0}, 64, 32)

	v1 := newMapView()
	placeInChunk(start, v1, box0, level.ChunkPos{0, 0}, 42)
	placeInChunk(start, v1, box0, level.ChunkPos{0, 0}, 42) // again

	v2 := newMapView()
	placeInChunk(start, v2, box0, level.ChunkPos{0, 0}, 42) // once

	if len(v1.blocks) != len(v2.blocks) {
		t.Fatalf("double placement changed the block set: %d vs %d", len(v1.blocks), len(v2.blocks))
	}
	for k, got := range v2.blocks {
		if v1.blocks[k] != got {
			t.Fatalf("double placement diverged at %v: %v vs %v", k, v1.blocks[k], got)
		}
	}
}

// TestPlaceInChunkSkipsNonIntersecting: a piece whose bbox does NOT intersect the chunk's
// writable box is NOT PostProcessed (no writes).
func TestPlaceInChunkSkipsNonIntersecting(t *testing.T) {
	sand := sandstoneID(t)
	piece := newSpanPiece(sand) // bbox x[0..31]
	start := &StructureStart{Structure: "test:span", ChunkPos: level.ChunkPos{0, 0}, Pieces: []Piece{piece}}
	start.RecomputeBBox()

	// Chunk (10,10): writable box x[160..175] — far from the piece bbox x[0..31].
	farBox := WritableArea(level.ChunkPos{10, 10}, 64, 32)
	view := newMapView()
	placeInChunk(start, view, farBox, level.ChunkPos{10, 10}, 0)

	if view.writes != 0 {
		t.Fatalf("placeInChunk wrote %d cells for a non-intersecting chunk, want 0", view.writes)
	}
}
