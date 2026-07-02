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
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world"
)

// snapSectionLocal mirrors world/manager.go's sectionLocal (the single authoritative mapping,
// package-private there): sec = (y-minY)>>4; local = (y&15)<<8 | (z&15)<<4 | (x&15). Replicated here
// (not exported from world) to read a cached chunk's sections directly in snapshotRegion without a
// per-cell ChunkManager lookup. Negative-safe: Go's >> is arithmetic and y&15 is correct for
// negatives ((-1)&15==15), so no extra correction is needed.
func snapSectionLocal(x, y, z, minY int) (sec, local int) {
	sec = (y - minY) >> 4
	local = (y&15)<<8 | (z&15)<<4 | (x & 15)
	return
}

// pathRegion is the ported PathNavigationRegion: an IMMUTABLE solidity snapshot of the block
// box [minX..maxX]×[minY..maxY]×[minZ..maxZ]. solid is a dense bitset indexed by the packed
// (x,y,z) offset within the box (idx below). After snapshotRegion (or a test's set calls) the
// region is treated as immutable — only the builder writes it, and computePath reads it. A
// flat []bool keeps the read branch-free and cache-friendly (the box is small: ~50³ worst case
// for a follow range of 24, well within a tick-built scratch buffer).
//
// pathFluid is the per-cell fluid classification the snapshot stores alongside solidity, so the
// pure A* (node_evaluator.go getPathType) can classify WATER/LAVA nodes off-tick WITHOUT a live
// world read. It mirrors the getPathTypeFromState fluid precedence (LAVA before WATER). pathFluidNone is
// the common case (air or a non-fluid block). Classified ON the tick in snapshotRegion via the SAME
// waterLevelOf/lavaLevelOf reads fluid.go uses, so the snapshot and the live world never disagree.
type pathFluid uint8

const (
	pathFluidNone pathFluid = iota
	pathFluidWater
	pathFluidLava
)

type pathRegion struct {
	minX, minY, minZ int
	maxX, maxY, maxZ int
	dx, dy, dz       int // span = max-min+1 per axis (precomputed for the index)
	solid            []bool
	// fluid is the per-cell fluid classification (fluidNone/fluidWater/fluidLava), index-aligned with
	// solid (same idx). Built by snapshotRegion; immutable thereafter. A cell may be BOTH a fluid and
	// non-solid (water/lava are non-solid), so this is a SEPARATE array, not folded into solid.
	fluid []pathFluid
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
		fluid: make([]pathFluid, dx*dy*dz),
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

// setFluid writes a cell fluid classification (builder/test only — immutable thereafter).
func (r *pathRegion) setFluid(x, y, z int, f pathFluid) {
	if !r.inBox(x, y, z) {
		return
	}
	r.fluid[r.idx(x, y, z)] = f
}

// fluidAt reports the fluid classification (fluidNone/fluidWater/fluidLava) at (x,y,z). OUT-OF-BOX is
// fluidNone (the edge is treated as a solid barrier by solidAt; a void cell carries no fluid). Reads
// only the immutable snapshot — the getPathTypeFromState fluid precedence in node_evaluator.getPathType.
func (r *pathRegion) fluidAt(x, y, z int) pathFluid {
	if !r.inBox(x, y, z) {
		return pathFluidNone
	}
	return r.fluid[r.idx(x, y, z)]
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
	// PERF (Phase-8 snapshot, hot-path fix): the snapshot cube is ~(2*size+span)^3 cells (~74k for a
	// followRange-16 stroll). The naive per-cell w.GetBlock did a full ChunkManager column map-lookup
	// (m.Get -> mapaccess1) PER BLOCK — a CPU profile put this at ~85% of the tick with a handful of
	// mobs pathing. The cube spans only ~9 columns, so resolve the *level.Chunk ONCE PER (x,z) column
	// and read every y of that column from the cached chunk's sections directly. This is a pure perf
	// rewrite — the produced solid/air bitmap is byte-identical to the per-cell path (same GetBlock +
	// IsAir per cell, just without re-doing the column lookup) — the only permitted deviation.
	// Iterate column-major (x,z outer) so the cached chunk + section index are reused across the y run.
	for x := r.minX; x <= r.maxX; x++ {
		for z := r.minZ; z <= r.maxZ; z++ {
			col := level.ChunkPos{int32(x >> 4), int32(z >> 4)}
			ch, loaded := w.Get(col)
			if !loaded {
				continue // unloaded column: leave the cells false (air) — same as GetBlock ok==false
			}
			for y := r.minY; y <= r.maxY; y++ {
				sec, local := snapSectionLocal(x, y, z, dimMinY)
				if sec < 0 || sec >= len(ch.Sections) {
					continue // y outside the section range: air (same as GetBlock ok==false)
				}
				s := ch.Sections[sec].GetBlock(local)
				r.set(x, y, z, !block.IsAir(s)) // solid? (the copy — never a live alias)
				// Fluid classification (getPathTypeFromState precedence: LAVA before WATER), read via the
				// SAME waterLevelOf/lavaLevelOf fluid.go uses so the snapshot matches the live world. A
				// non-fluid block stays fluidNone. This is the copy the pure A* classifies WATER/LAVA from.
				if _, isLava := lavaLevelOf(s); isLava {
					r.setFluid(x, y, z, pathFluidLava)
				} else if _, isWater := waterLevelOf(s); isWater {
					r.setFluid(x, y, z, pathFluidWater)
				}
			}
		}
	}
	return r // immutable from here — the seam value the Phase-8 worker consumes
}
