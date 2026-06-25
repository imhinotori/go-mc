package structure

import "github.com/imhinotori/sulfur/level"

// BoundingBox is the ported net.minecraft.world.level.levelgen.structure.BoundingBox:
// an inclusive integer AABB in WORLD block coords. A structure's BBox is the union
// (Encapsulate) of its piece bboxes; the REFERENCES scan tests whether it Intersects
// a chunk's writable column. Inclusive on every face (min..max), matching the jar.
//
// Source: javap -c net.minecraft.world.level.levelgen.structure.BoundingBox.
type BoundingBox struct {
	MinX, MinY, MinZ int
	MaxX, MaxY, MaxZ int
}

// IsInside ports BoundingBox.isInside(Vec3i): inclusive containment of a block pos.
func (b BoundingBox) IsInside(wx, wy, wz int) bool {
	return wx >= b.MinX && wx <= b.MaxX &&
		wy >= b.MinY && wy <= b.MaxY &&
		wz >= b.MinZ && wz <= b.MaxZ
}

// IntersectsXZ ports BoundingBox.intersects(int x1, int z1, int x2, int z2): the 2D
// XZ-projection overlap test (Y is ignored). The jar form is
// maxX >= x1 && minX <= x2 && maxZ >= z1 && minZ <= z2. This is the test the
// REFERENCES scan uses against a chunk's 16x16 column (a structure reaches a chunk iff
// its XZ footprint overlaps the chunk's column).
func (b BoundingBox) IntersectsXZ(x1, z1, x2, z2 int) bool {
	return b.MaxX >= x1 && b.MinX <= x2 && b.MaxZ >= z1 && b.MinZ <= z2
}

// Intersects ports BoundingBox.intersects(BoundingBox): full 3D AABB overlap.
func (b BoundingBox) Intersects(o BoundingBox) bool {
	return b.MaxX >= o.MinX && b.MinX <= o.MaxX &&
		b.MaxY >= o.MinY && b.MinY <= o.MaxY &&
		b.MaxZ >= o.MinZ && b.MinZ <= o.MaxZ
}

// Encapsulate ports BoundingBox.encapsulate(BoundingBox): the smallest box covering
// both (per-axis min/max). 14-02's piece-tree assembly unions piece bboxes via this
// to compute the StructureStart's overall BBox.
func (b BoundingBox) Encapsulate(o BoundingBox) BoundingBox {
	return BoundingBox{
		MinX: min(b.MinX, o.MinX), MinY: min(b.MinY, o.MinY), MinZ: min(b.MinZ, o.MinZ),
		MaxX: max(b.MaxX, o.MaxX), MaxY: max(b.MaxY, o.MaxY), MaxZ: max(b.MaxZ, o.MaxZ),
	}
}

// IsEmpty reports the zero-value box (no pieces). An empty StructureStart carries an
// empty BBox and is treated as "no structure here".
func (b BoundingBox) IsEmpty() bool {
	return b == BoundingBox{}
}

// WritableArea returns the 16x16xH world-block column of chunk pos over the full
// vertical [minY, minY+height) range — the area into which a structure may write
// blocks for that chunk (jar getWritableArea / the createStructures clip box). The
// PLACE pass (14-02) clips each piece to this box so a cross-chunk structure only
// edits THIS chunk's column. Max faces are inclusive (minBlockX+15, minY+height-1).
func WritableArea(pos level.ChunkPos, minY, height int) BoundingBox {
	minBlockX := int(pos[0]) * 16
	minBlockZ := int(pos[1]) * 16
	return BoundingBox{
		MinX: minBlockX, MinY: minY, MinZ: minBlockZ,
		MaxX: minBlockX + 15, MaxY: minY + height - 1, MaxZ: minBlockZ + 15,
	}
}

// Piece is the structure-piece interface (defined in piece.go, extended by 14-02 with
// PostProcess). 14-01 declared a bbox-only placeholder here; 14-02 moves the canonical
// declaration to piece.go and adds the PostProcess geometry-writing method.

// StructureStart is the ported net.minecraft.world.level.levelgen.structure.StructureStart:
// a structure DECISION owned by ChunkPos — the piece list (filled by 14-02), the union
// bounding box (BBox), and the reference count (how many chunks' REFERENCES name this
// start). An EMPTY start (no pieces, empty BBox) is the "no structure here" sentinel.
//
// The decision is PURE over (worldSeed, ChunkPos): the same inputs yield the same
// start, so the cache that holds it is a MEMOIZATION, not shared mutable state.
type StructureStart struct {
	// Structure is the structure id this start places (e.g. "minecraft:desert_pyramid").
	// 14-02 sets it; empty for the no-op start.
	Structure string
	// ChunkPos is the owning chunk (the start's anchor).
	ChunkPos level.ChunkPos
	// Pieces is the assembled piece tree (14-02). Empty here.
	Pieces []Piece
	// BBox is the union of the piece bboxes (Encapsulate over Pieces). Empty when
	// Pieces is empty.
	BBox BoundingBox
	// References is the count of chunks whose REFERENCES list names this start
	// (createReferences increments it in vanilla; tracked for parity + 14-02).
	References int
}

// IsValid ports StructureStart.isValid: a start is valid iff it has at least one
// piece. An empty start (the STRUCT-01 no-op generator's output) is invalid and is
// skipped by the REFERENCES scan and the PLACE gather.
func (s *StructureStart) IsValid() bool {
	return s != nil && len(s.Pieces) > 0
}

// RecomputeBBox sets BBox to the union of the piece bboxes (the assembly step 14-02
// calls after building the piece tree). An empty piece list yields the empty box.
func (s *StructureStart) RecomputeBBox() {
	if len(s.Pieces) == 0 {
		s.BBox = BoundingBox{}
		return
	}
	bb := s.Pieces[0].BoundingBox()
	for _, p := range s.Pieces[1:] {
		bb = bb.Encapsulate(p.BoundingBox())
	}
	s.BBox = bb
}
