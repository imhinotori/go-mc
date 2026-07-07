package server

// collision.go — the VoxelShape collision engine: the 1:1 port of the vanilla movement
// collision call chain (26.2 jar, verified by javap this session):
//
//	Entity.move(MoverType, Vec3)
//	  └─ Entity.collide(Vec3)
//	       └─ collideBoundingBox(entity, vec, box, level, entityCollisions)
//	            ├─ collectCollidersIgnoringWorldBorder → Level.getBlockCollisions
//	            │    └─ BlockCollisions.computeNext (the per-block shape iterator)
//	            └─ collideWithShapes(vec, box, shapes)
//	                 └─ per axis in Direction.axisStepOrder: Shapes.collide(axis, box, shapes, d)
//	                      └─ VoxelShape.collide → collideX (level/block/voxelshape.go)
//
// The shapes come from the extracted per-state grids (level/block/collision_shapes.go), so
// slabs/stairs/fences(1.5)/walls/snow-layers/carpets collide with their REAL vanilla boxes.
//
// Deliberately not yet ported (phase 4 of the collision plan; call sites are marked):
//   - Level.getEntityCollisions (shulker/boat hard boxes) — the entityCollisions list is
//     always empty here.
//   - The world-border shape (WorldBorder.getCollisionShape when isInsideCloseToBorder).
//
// Everything runs on the tick goroutine over tick-owned chunk state (TICK-05), exactly like
// the old sweep it replaces.

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// vec3d is a plain (x,y,z) double triple — net.minecraft.world.phys.Vec3 for the collision
// path (no methods beyond what the port needs).
type vec3d struct{ x, y, z float64 }

func (v vec3d) get(axis int) float64 {
	switch axis {
	case block.AxisX:
		return v.x
	case block.AxisY:
		return v.y
	default:
		return v.z
	}
}

func (v vec3d) with(axis int, d float64) vec3d {
	switch axis {
	case block.AxisX:
		v.x = d
	case block.AxisY:
		v.y = d
	default:
		v.z = d
	}
	return v
}

func (v vec3d) lengthSqr() float64 { return v.x*v.x + v.y*v.y + v.z*v.z }

// placedShape is one world-positioned collider: a block-local VoxelShape plus its BlockPos
// offset — the allocation-free equivalent of vanilla's shape.move(pos).
type placedShape struct {
	s       *block.VoxelShape
	x, y, z float64
}

// boxMove is AABB.move(x,y,z).
func boxMove(b block.Box, x, y, z float64) block.Box {
	return block.Box{
		MinX: b.MinX + x, MinY: b.MinY + y, MinZ: b.MinZ + z,
		MaxX: b.MaxX + x, MaxY: b.MaxY + y, MaxZ: b.MaxZ + z,
	}
}

// expandTowards is AABB.expandTowards(x,y,z): extend the min edge for a negative component,
// the max edge for a positive one. CITE: javap net.minecraft.world.phys.AABB.expandTowards.
func expandTowards(b block.Box, x, y, z float64) block.Box {
	if x < 0 {
		b.MinX += x
	} else {
		b.MaxX += x
	}
	if y < 0 {
		b.MinY += y
	} else {
		b.MaxY += y
	}
	if z < 0 {
		b.MinZ += z
	} else {
		b.MaxZ += z
	}
	return b
}

// axisStepOrder is Direction.axisStepOrder(Vec3): Y first, then the horizontal axis with the
// LARGER |component| (ties → X). CITE: javap net.minecraft.core.Direction.axisStepOrder —
// |x| < |z| ? YZX : YXZ.
func axisStepOrder(m vec3d) [3]int {
	if math.Abs(m.x) < math.Abs(m.z) {
		return [3]int{block.AxisY, block.AxisZ, block.AxisX}
	}
	return [3]int{block.AxisY, block.AxisX, block.AxisZ}
}

// collectBlockCollisions is the BlockCollisions iterator (context-free CollisionContext):
// every block whose collision shape can intersect `box`, yielded as a placedShape. Port of
// net.minecraft.world.level.BlockCollisions.computeNext:
//
//   - Cursor bounds: floor(min-EPSILON)-1 .. floor(max+EPSILON)+1 per axis — the ±1 ring
//     admits neighbors whose shape exceeds their cell (fence 1.5).
//   - Ring gating by boundary count (Cursor3D.getNextType): 3 boundary axes (corner) —
//     skipped; 2 (edge) — only minecraft:moving_piston; 1 (face) — only states with
//     hasLargeCollisionShape().
//   - Shapes.block() IDENTITY fast path: strict AABB.intersects against the unit cell.
//   - Other shapes: skip when empty, else the joinIsNotEmpty(AND) overlap filter
//     (VoxelShape.IntersectsBox).
//
// An unloaded chunk contributes NO collision (vanilla getChunkForCollisions returns null and
// the position is skipped) — same policy the old blockSolidAt used.
func (t *TickLoop) collectBlockCollisions(box block.Box) []placedShape {
	if t.world() == nil {
		return nil
	}
	x0 := floorI(box.MinX-shapeEpsD) - 1
	x1 := floorI(box.MaxX+shapeEpsD) + 1
	y0 := floorI(box.MinY-shapeEpsD) - 1
	y1 := floorI(box.MaxY+shapeEpsD) + 1
	z0 := floorI(box.MinZ-shapeEpsD) - 1
	z1 := floorI(box.MaxZ+shapeEpsD) + 1

	var out []placedShape
	for bx := x0; bx <= x1; bx++ {
		bx0 := bx == x0 || bx == x1
		for by := y0; by <= y1; by++ {
			by0 := by == y0 || by == y1
			for bz := z0; bz <= z1; bz++ {
				bz0 := bz == z0 || bz == z1
				boundary := 0
				if bx0 {
					boundary++
				}
				if by0 {
					boundary++
				}
				if bz0 {
					boundary++
				}
				if boundary == 3 {
					continue // corner: never consulted (Cursor3D TYPE 3)
				}
				sid, ok := t.world().GetBlock(pk.Position{X: bx, Y: by, Z: bz}, dimMinY)
				if !ok {
					continue // unloaded / out of range: no collision (null chunk skip)
				}
				if boundary == 1 && !block.HasLargeCollisionShape(sid) {
					continue // face ring: only shapes that can exceed their cell
				}
				if boundary == 2 {
					if _, isMoving := block.StateList[sid].(block.MovingPiston); !isMoving {
						continue // edge ring: only minecraft:moving_piston
					}
				}
				s := block.CollisionShape(sid)
				if s.Block {
					// Shapes.block() identity fast path: strict unit-cell intersects.
					if box.Intersects(float64(bx), float64(by), float64(bz),
						float64(bx)+1, float64(by)+1, float64(bz)+1) {
						out = append(out, placedShape{s, float64(bx), float64(by), float64(bz)})
					}
					continue
				}
				if s.IsEmpty() {
					continue
				}
				if s.IntersectsBox(float64(bx), float64(by), float64(bz), box) {
					out = append(out, placedShape{s, float64(bx), float64(by), float64(bz)})
				}
			}
		}
	}
	return out
}

// shapeEpsD mirrors Shapes.EPSILON (1.0E-7) for the cursor-bound arithmetic here (the block
// package keeps its own copy for the shape engine).
const shapeEpsD = 1.0e-7

// shapesCollide is Shapes.collide(axis, box, shapes, desired): successive clamping through
// every shape, with the |desired| < EPSILON → 0 short-circuit tested BEFORE each shape
// (bytecode: the abs check is inside the iterator loop, so an empty list returns desired
// unchanged even when |desired| < EPSILON). CITE: javap Shapes.collide.
func shapesCollide(axis int, box block.Box, shapes []placedShape, desired float64) float64 {
	for _, ps := range shapes {
		if math.Abs(desired) < shapeEpsD {
			return 0
		}
		desired = ps.s.Collide(axis, ps.x, ps.y, ps.z, box, desired)
	}
	return desired
}

// collideWithShapes is Entity.collideWithShapes(Vec3, AABB, List<VoxelShape>): clip the
// motion one axis at a time in axisStepOrder, moving the box by the accumulated result
// before each axis. CITE: javap Entity.collideWithShapes — Vec3.ZERO accumulator,
// box.move(result) per axis, Shapes.collide, result.with(axis, clamped).
func collideWithShapes(m vec3d, box block.Box, shapes []placedShape) vec3d {
	if len(shapes) == 0 {
		return m
	}
	result := vec3d{}
	for _, axis := range axisStepOrder(m) {
		d := m.get(axis)
		if d == 0 {
			continue
		}
		clamped := shapesCollide(axis, boxMove(box, result.x, result.y, result.z), shapes, d)
		result = result.with(axis, clamped)
	}
	return result
}

// collideBoundingBox is Entity.collideBoundingBox(entity, vec, box, level, entityCollisions)
// with the (phase-4) entityCollisions list empty and the world-border collider not yet
// wired: collect the block shapes in the motion-expanded box, then clip per axis.
// CITE: javap Entity.collideBoundingBox → collectCollidersIgnoringWorldBorder(box.
// expandTowards(vec)) → collideWithShapes.
func (t *TickLoop) collideBoundingBox(m vec3d, box block.Box) vec3d {
	shapes := t.collectBlockCollisions(expandTowards(box, m.x, m.y, m.z))
	return collideWithShapes(m, box, shapes)
}

// collideMovement is Entity.collide(Vec3): the whole-motion resolution. Step-up
// (maxUpStep/collectCandidateStepUpHeights) is phase 3 of the collision plan and
// entity-entity hard collision phase 4 — this is the vec.lengthSqr()==0-guarded
// collideBoundingBox core. CITE: javap Entity.collide bytecode 0-43 (the pre-step-up part).
func (t *TickLoop) collideMovement(m vec3d, box block.Box) vec3d {
	if m.lengthSqr() == 0 {
		return m
	}
	return t.collideBoundingBox(m, box)
}

// mthEqual is Mth.equal(double, double): |b - a| < 9.999999747378752E-6 ((double)1.0E-5F) —
// the tolerance Entity.move uses for the horizontalCollision flags. CITE: javap
// net.minecraft.util.Mth.equal(DD).
func mthEqual(a, b float64) bool {
	return math.Abs(b-a) < 9.999999747378752e-6
}
