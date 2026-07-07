package block

// voxelshape.go — the 1:1 Go port of net.minecraft.world.phys.shapes.VoxelShape's collision
// surface (26.2 jar), backed by the extracted per-state discrete grids in collision_shapes.go
// (tools/java/GenBlockCollisionShapes.java → tools/gen_block_collision.go).
//
// A vanilla VoxelShape is a DiscreteVoxelShape grid (per-axis slice-boundary coordinate lists
// plus a full/empty bit per cell). The collision engine consumes it through exactly three
// operations, all ported here method-for-method from bytecode:
//
//   - VoxelShape.collide(Axis, AABB, double) → collideX(AxisCycle, AABB, double): clip ONE
//     component of a motion delta against the shape (Collide below).
//   - VoxelShape.findIndex(Axis, double): Mth.binarySearch over the coordinate list
//     (findIndex below — the exact binary search, not a floor shortcut: in the collision
//     path shapes are always world-MOVED (VoxelShape.move → ArrayVoxelShape with
//     OffsetDoubleList), and ArrayVoxelShape uses the base-class binary-search findIndex.
//     The offset is applied per-access here, mirroring OffsetDoubleList.getDouble).
//   - DiscreteVoxelShape.isFullWide: the full-cell test (isFullAxes below).
//
// World placement: vanilla moves each block's shape to its BlockPos (shape.move(pos)) before
// colliding. Rather than allocating a moved copy per block per tick, the block offset
// (ox,oy,oz) is a parameter of every query and added exactly where OffsetDoubleList would
// add it — the double arithmetic (coord + offset) is the identical operation, so results are
// bit-identical to colliding a moved shape.
import "math"

// shapeEps is Shapes.EPSILON (1.0E-7) — the epsilon every collide/index computation uses.
// CITE: javap net.minecraft.world.phys.shapes.Shapes — EPSILON ldc2_w 1.0E-7 throughout
// VoxelShape.collideX and BlockCollisions.
const shapeEps = 1.0e-7

// Axis indices mirror net.minecraft.core.Direction$Axis ordinals (X=0, Y=1, Z=2).
const (
	AxisX = 0
	AxisY = 1
	AxisZ = 2
)

// Box is a plain double-precision AABB (net.minecraft.world.phys.AABB's six fields). The
// collision engine passes entity boxes through it; level/block owns the type so the shape
// engine has no dependency on server internals.
type Box struct {
	MinX, MinY, MinZ float64
	MaxX, MaxY, MaxZ float64
}

// Intersects reports AABB.intersects(minX..maxZ) — STRICT inequalities on every axis (a
// shared face does not intersect). CITE: javap net.minecraft.world.phys.AABB.intersects
// (DDDDDD): minX < maxX2 && maxX > minX2 && ... with dcmpg/dcmpl strict compares.
func (b Box) Intersects(minX, minY, minZ, maxX, maxY, maxZ float64) bool {
	return b.MinX < maxX && b.MaxX > minX &&
		b.MinY < maxY && b.MaxY > minY &&
		b.MinZ < maxZ && b.MaxZ > minZ
}

// VoxelShape is the extracted discrete grid of one vanilla collision shape (block-local
// coordinates; world placement is the per-call offset). Fields are populated by the
// generated collision_shapes.go table.
type VoxelShape struct {
	// Coords[axis] is getCoords(axis): the size+1 slice-boundary coordinates along that
	// axis (ArrayVoxelShape's DoubleArrayList / CubeVoxelShape's FractionalDoubleList —
	// identical double values either way, extracted verbatim from the jar runtime).
	Coords [3][]float64
	// Full is the DiscreteVoxelShape bit set: cell (x,y,z) is full iff bit
	// (x*ySize+y)*zSize+z — BitSetDiscreteVoxelShape.getIndex order.
	Full []uint64
	// Block reports identity-equality with Shapes.block() in the jar runtime. The
	// BlockCollisions iterator selects a strict-AABB fast path on IDENTITY (not geometry):
	// `if (shape == Shapes.block())`. CITE: javap BlockCollisions.computeNext if_acmpne.
	Block bool
	// Empty is DiscreteVoxelShape.isEmpty() (no full cells) — air, plants, water, …
	Empty bool
}

// IsEmpty mirrors VoxelShape.isEmpty(): a shape with no full cells never collides.
func (s *VoxelShape) IsEmpty() bool { return s.Empty }

// size is DiscreteVoxelShape.getSize(axis): the cell count along an axis (one less than the
// boundary-coordinate count).
func (s *VoxelShape) size(axis int) int { return len(s.Coords[axis]) - 1 }

// coord is VoxelShape.get(axis, i) for the UNMOVED shape; callers add the world offset
// exactly where OffsetDoubleList would.
func (s *VoxelShape) coord(axis, i int) float64 { return s.Coords[axis][i] }

// findIndex is VoxelShape.findIndex(axis, position) for the world-moved shape:
// Mth.binarySearch(0, size+1, i -> position < get(axis,i)) - 1, where get(axis,i) of the
// moved shape is coord+off (OffsetDoubleList.getDouble). Returns the largest index i with
// coord[i]+off <= position (-1 if position precedes coord[0]+off; size if it is at/past the
// last boundary). CITE: javap VoxelShape.findIndex + lambda$findIndex$0 (dcmpg ifge — a
// strict `position < get`), Mth.binarySearch (first-true binary search).
func (s *VoxelShape) findIndex(axis int, off, position float64) int {
	coords := s.Coords[axis]
	// Mth.binarySearch(min=0, max=len(coords), pred) — first index where pred is true.
	lo, rng := 0, len(coords)
	for rng > 0 {
		half := rng / 2
		mid := lo + half
		if position < coords[mid]+off {
			rng = half
		} else {
			lo = mid + 1
			rng -= half + 1
		}
	}
	return lo - 1
}

// isFullAxes is DiscreteVoxelShape.isFullWide translated out of AxisCycle form: (a,b,c) are
// the world axes the (ia,ib,ic) indices refer to; the cycled index triple is mapped back to
// (x,y,z) and tested against the bit set (with the same defensive bounds check isFullWide
// performs). CITE: javap DiscreteVoxelShape.isFullWide(AxisCycle,III) → cycles the indices
// back to XYZ then bounds-checks and reads the BitSet.
func (s *VoxelShape) isFullAxes(a, b, c, ia, ib, ic int) bool {
	var idx [3]int
	idx[a], idx[b], idx[c] = ia, ib, ic
	x, y, z := idx[0], idx[1], idx[2]
	sx, sy, sz := s.size(0), s.size(1), s.size(2)
	if x < 0 || y < 0 || z < 0 || x >= sx || y >= sy || z >= sz {
		return false
	}
	bit := (x*sy+y)*sz + z
	return s.Full[bit>>6]&(1<<uint(bit&63)) != 0
}

// Collide clips one component of a motion delta against this shape placed at world offset
// (ox,oy,oz), returning the clamped delta. It is the 1:1 port of VoxelShape.collide(Axis,
// AABB, double) → collideX(AxisCycle.between(axis, X), box, desired):
//
//   - axis a is the motion axis; b,c = (a+1)%3, (a+2)%3 — exactly the axisY/axisZ the
//     inverse AxisCycle produces in collideX (verified for all three axes: X→(X,Y,Z),
//     Y→(Y,Z,X), Z→(Z,X,Y)).
//   - The box's cross-section index range on b/c uses the ±EPSILON-shrunk edges
//     (findIndex(min+ε) .. findIndex(max-ε)), clamped to the grid.
//   - Moving positive: scan slices STRICTLY beyond the box's leading edge slice
//     (findIndex(maxA-ε)+1 ..); the FIRST slice containing any full overlapping cell
//     decides: d = sliceMinCoord - maxA; clamp desired=min(desired,d) only when
//     d >= -EPSILON (an already-overlapping shape does not clip), then return. Negative is
//     the mirror (slice max face vs minA, d <= EPSILON, max-clamp).
//
// CITE: javap net.minecraft.world.phys.shapes.VoxelShape.collideX — structure and every
// epsilon/compare mirrored 1:1 (the b/c scan order does not affect the result: every full
// cell in the first colliding slice yields the same face coordinate).
func (s *VoxelShape) Collide(axis int, ox, oy, oz float64, box Box, desired float64) float64 {
	if s.IsEmpty() {
		return desired
	}
	if math.Abs(desired) < shapeEps {
		return 0
	}
	off := [3]float64{ox, oy, oz}
	bmin := [3]float64{box.MinX, box.MinY, box.MinZ}
	bmax := [3]float64{box.MaxX, box.MaxY, box.MaxZ}
	a, b, c := axis, (axis+1)%3, (axis+2)%3

	maxA := bmax[a]
	minA := bmin[a]
	minIdxA := s.findIndex(a, off[a], minA+shapeEps)
	maxIdxA := s.findIndex(a, off[a], maxA-shapeEps)
	minB := max(0, s.findIndex(b, off[b], bmin[b]+shapeEps))
	maxB := min(s.size(b), s.findIndex(b, off[b], bmax[b]-shapeEps)+1)
	minC := max(0, s.findIndex(c, off[c], bmin[c]+shapeEps))
	maxC := min(s.size(c), s.findIndex(c, off[c], bmax[c]-shapeEps)+1)
	sizeA := s.size(a)

	if desired > 0 {
		for ia := maxIdxA + 1; ia < sizeA; ia++ {
			for ib := minB; ib < maxB; ib++ {
				for ic := minC; ic < maxC; ic++ {
					if s.isFullAxes(a, b, c, ia, ib, ic) {
						d := s.coord(a, ia) + off[a] - maxA
						if d >= -shapeEps {
							desired = math.Min(desired, d)
						}
						return desired
					}
				}
			}
		}
	} else if desired < 0 {
		for ia := minIdxA - 1; ia >= 0; ia-- {
			for ib := minB; ib < maxB; ib++ {
				for ic := minC; ic < maxC; ic++ {
					if s.isFullAxes(a, b, c, ia, ib, ic) {
						d := s.coord(a, ia+1) + off[a] - minA
						if d <= shapeEps {
							desired = math.Max(desired, d)
						}
						return desired
					}
				}
			}
		}
	}
	return desired
}

// IntersectsBox reports whether this shape, placed at (ox,oy,oz), has interior overlap with
// the box — the candidate filter BlockCollisions.computeNext applies to NON-full-cube shapes
// via Shapes.joinIsNotEmpty(shape.move(pos), Shapes.create(box), BooleanOp.AND). The merged-
// grid AND is nonempty iff some full cell of the shape overlaps the box with positive width
// on all three axes AFTER the IndirectMerger collapses boundary coordinates closer than
// EPSILON — i.e. a graze thinner than EPSILON does not count. This port tests each full cell
// directly with that epsilon rule (overlap width >= EPSILON on every axis); it is the
// documented approximation of the IndexMerger machinery, exact except exactly AT the 1e-7
// knife's edge of the merger's dedupe compare, and it only FILTERS candidates — Collide
// itself is epsilon-exact, so a filtered-in non-constraining shape cannot change a result.
// CITE: javap BlockCollisions.computeNext (joinIsNotEmpty(..., AND)); IndirectMerger.<init>
// (the >= 1.0E-7 coordinate dedupe).
func (s *VoxelShape) IntersectsBox(ox, oy, oz float64, box Box) bool {
	if s.IsEmpty() {
		return false
	}
	bmin := [3]float64{box.MinX, box.MinY, box.MinZ}
	bmax := [3]float64{box.MaxX, box.MaxY, box.MaxZ}
	off := [3]float64{ox, oy, oz}
	sx, sy, sz := s.size(0), s.size(1), s.size(2)
	for x := 0; x < sx; x++ {
		for y := 0; y < sy; y++ {
			for z := 0; z < sz; z++ {
				bit := (x*sy+y)*sz + z
				if s.Full[bit>>6]&(1<<uint(bit&63)) == 0 {
					continue
				}
				idx := [3]int{x, y, z}
				ok := true
				for a := 0; a < 3; a++ {
					lo := s.coord(a, idx[a]) + off[a]
					hi := s.coord(a, idx[a]+1) + off[a]
					if math.Min(hi, bmax[a])-math.Max(lo, bmin[a]) < shapeEps {
						ok = false
						break
					}
				}
				if ok {
					return true
				}
			}
		}
	}
	return false
}

// ForEachCoordY calls fn with every Y slice-boundary coordinate of the world-placed shape,
// in ascending order — VoxelShape.getCoords(Direction.Axis.Y) iteration as consumed by
// Entity.collectCandidateStepUpHeights. CITE: javap Entity.collectCandidateStepUpHeights —
// iterates shape.getCoords(Y) per collider.
func (s *VoxelShape) ForEachCoordY(oy float64, fn func(y float64) bool) {
	for _, c := range s.Coords[AxisY] {
		if !fn(c + oy) {
			return
		}
	}
}
