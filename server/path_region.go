package server

// path_region.go — AI-02: the ported PathNavigationRegion — the IMMUTABLE block-solidity
// snapshot that is the request->snapshot->result boundary (THE Phase-8 hinge).
//
// PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, never a GPL paste) from the unobfuscated
// 26.2 jar (javap, this session):
//
//   net.minecraft.world.level.PathNavigationRegion
//     - ctor (Level, BlockPos min, BlockPos max): COPIES `ChunkAccess[][] chunks` for the
//       bounded region and serves getBlockState/getFluidState over that COPY. The PathFinder
//       reads ONLY this object — never the live Level. javap-confirmed fields:
//         protected final int centerX, centerZ;
//         protected final ChunkAccess[][] chunks;       // the copied region
//         protected boolean allEmpty;
//     - GroundPathNavigation.createPath builds it as `new PathNavigationRegion(level,
//       blockPos.offset(-(int)(followRange+accuracy), …), blockPos.offset(+…))` — a cube around
//       the target sized by the mob's follow range. (javap createPath: `size = followRange +
//       accuracy; offset(-size,-size,-size) .. offset(+size,+size,+size)`.)
//
// THE SEAM CONTRACT (07-RESEARCH Pitfall 1): vanilla's PathFinder.findPath never touches the
// live Level — it reads the copied PathNavigationRegion. Porting that shape is the single
// discipline that makes Phase-8 (OPT-01) a swap of the EXECUTOR, not a rewrite. snapshotRegion
// COPIES world solidity ON the tick; computePath (pathfinder.go) then reads ONLY the copy.
//
// v1 SCOPE: the copy reduces each block to one bit — solid (a unit-cube collider) or not — via
// level.block.IsAir over world.ChunkManager.GetBlock, exactly the solidity primitive physics
// uses (blockSolidAt). Full BlockState fluid/shape data is deferred with the water/lava/fence
// malus (A5) — v1 superflat is flat stone, so a solidity bitset is faithful.

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// pathRegion is the ported PathNavigationRegion: an IMMUTABLE solidity snapshot of the block
// box [minX..maxX]×[minY..maxY]×[minZ..maxZ]. solid is a dense bitset indexed by the packed
// (x,y,z) offset within the box (idx below). After snapshotRegion (or a test's set calls) the
// region is treated as immutable — only the builder writes it, and computePath reads it. A
// flat []bool keeps the read branch-free and cache-friendly (the box is small: ~50³ worst case
// for a follow range of 24, well within a tick-built scratch buffer).
type pathRegion struct {
	minX, minY, minZ int
	maxX, maxY, maxZ int
	dx, dy, dz       int // span = max-min+1 per axis (precomputed for the index)
	solid            []bool
}

// newPathRegion allocates an all-air region over the inclusive box. The builder
// (snapshotRegion) or a test fills it via set; nothing else writes it afterward.
func newPathRegion(minX, minY, minZ, maxX, maxY, maxZ int) *pathRegion {
	dx := maxX - minX + 1
	dy := maxY - minY + 1
	dz := maxZ - minZ + 1
	if dx < 1 || dy < 1 || dz < 1 {
		// Degenerate box (target == start at a clamped edge): a 1×1×1 region so the index is
		// always valid and the start node is in range.
		dx, dy, dz = 1, 1, 1
		maxX, maxY, maxZ = minX, minY, minZ
	}
	return &pathRegion{
		minX: minX, minY: minY, minZ: minZ,
		maxX: maxX, maxY: maxY, maxZ: maxZ,
		dx: dx, dy: dy, dz: dz,
		solid: make([]bool, dx*dy*dz),
	}
}

// inBox reports whether (x,y,z) lies within the snapshot's bounding box.
func (r *pathRegion) inBox(x, y, z int) bool {
	return x >= r.minX && x <= r.maxX &&
		y >= r.minY && y <= r.maxY &&
		z >= r.minZ && z <= r.maxZ
}

// idx packs (x,y,z) into the solid bitset index. Caller guarantees inBox.
func (r *pathRegion) idx(x, y, z int) int {
	lx, ly, lz := x-r.minX, y-r.minY, z-r.minZ
	return (lx*r.dy+ly)*r.dz + lz
}

// set writes a cell's solidity (builder/test only — the region is immutable thereafter).
func (r *pathRegion) set(x, y, z int, solid bool) {
	if !r.inBox(x, y, z) {
		return
	}
	r.solid[r.idx(x, y, z)] = solid
}

// solidAt reports whether the cell at (x,y,z) is a solid (impassable) block in the snapshot.
// OUT-OF-BOX is treated as SOLID — vanilla's PathNavigationRegion returns a void/barrier-like
// state outside the copied chunks, and treating the edge as solid keeps the A* from walking off
// the snapshot into uncopied space (a node beyond the box is never standable-into). This is the
// documented edge policy: the box is sized to comfortably contain a valid path, so clamping the
// frontier at the boundary only forbids leaving the snapshot, never a real reachable cell.
func (r *pathRegion) solidAt(x, y, z int) bool {
	if !r.inBox(x, y, z) {
		return true
	}
	return r.solid[r.idx(x, y, z)]
}

// snapshotRegion builds the immutable pathRegion ON the tick by COPYING world solidity over the
// bounding box around (mob -> target), mirroring GroundPathNavigation.createPath's
// `new PathNavigationRegion(level, blockPos.offset(-size…), blockPos.offset(+size…))`. size is
// the mob's follow range padded by the reach accuracy (vanilla: followRange + accuracy). It
// reads world.ChunkManager.GetBlock (the SAME read physics uses) and stores `ok && !IsAir` —
// a solid cell — so the A* sees exactly the colliders physics does. Built on the tick (reads
// tick-owned chunks); IMMUTABLE from here, so the Phase-8 off-tick worker consumes exactly this
// value with no race (Pitfall 1).
//
// The box is centered on the TARGET (as vanilla does — createPath offsets from the target's
// blockPos), expanded to also cover the mob's current column so the start node is always in the
// snapshot.
func snapshotRegion(w *world.ChunkManager, e *Entity, tx, ty, tz int, followRange, accuracy int) *pathRegion {
	size := followRange + accuracy
	sx, sy, sz := floorI(e.x), floorI(e.y), floorI(e.z)

	minX := min(tx, sx) - size
	minY := min(ty, sy) - size
	minZ := min(tz, sz) - size
	maxX := max(tx, sx) + size
	maxY := max(ty, sy) + size
	maxZ := max(tz, sz) + size

	r := newPathRegion(minX, minY, minZ, maxX, maxY, maxZ)
	if w == nil {
		return r // world-less: an all-air region (degenerate, but never panics)
	}
	for x := r.minX; x <= r.maxX; x++ {
		for y := r.minY; y <= r.maxY; y++ {
			for z := r.minZ; z <= r.maxZ; z++ {
				s, ok := w.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY)
				r.set(x, y, z, ok && !block.IsAir(s)) // solid? (the copy — never a live alias)
			}
		}
	}
	return r // immutable from here — the seam value the Phase-8 worker consumes
}
