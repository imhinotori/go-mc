package level

// HeightmapUpdate is the incremental counterpart to the bulk heightmap recomputes
// (the once-per-chunk full column rescans in world/levelgen/surface). It mirrors
// net.minecraft.world.level.levelgen.Heightmap.update: after a single block is written
// at (lx, y, lz), it fixes ONLY that column's stored height in O(1) amortized — the
// firstAvailable-2 early-out, the opaque set-to-(y+1), and the non-opaque downward
// rescan — without rescanning the whole column the bulk recompute* does.
//
// Parameters:
//   - bs:       the per-column heightmap BitStorage (one of the 3 worldgen heightmaps).
//     Heights are stored RELATIVE to minY; the column index is lz<<4|lx.
//   - lx, y, lz: the LOCAL x/z (0..15) and WORLD y of the block that was just written.
//   - minY:     the chunk's world floor (heights are stored as worldY-minY).
//   - opaque:   the per-type opaque predicate result for the NEW state just written
//     (WORLD_SURFACE_WG = NOT_AIR; OCEAN_FLOOR_WG = motion-blocking-no-fluid;
//     MOTION_BLOCKING = blocks-motion-or-fluid). Each heightmap passes its own.
//   - opaqueAt: re-tests EXISTING blocks during the downward rescan when the exact
//     surface block was removed (the caller closes over the chunk + the same predicate).
//     It is only consulted on the removal path; an opaque write never calls it.
//
// Algorithm (jar Heightmap.update bytecode, transcribed in 10-RESEARCH §Standard Stack 2):
// the early-out branches at getFirstAvailable-2, the opaque path setHeight(y+1), and the
// MutableBlockPos downward scan on a non-opaque surface removal.
//
// It is pure and allocation-free (no chunk reference — the caller supplies the BitStorage
// and the opaqueAt closure), so it works against any of the 3 worldgen heightmaps. Phase
// 10's Decorate is a no-op, so this is built + unit-tested now but not yet CALLED in
// production; 10-03's Neighborhood proxy and the feature phase wire it on every worldgen
// block write so a later same-step feature sees an earlier placement.
func HeightmapUpdate(bs *BitStorage, lx, y, lz, minY int, opaque bool, opaqueAt func(y int) bool) {
	col := lz<<4 | lx
	firstAvail := bs.Get(col) + minY // current "first Y above surface"

	// Below surface-1 → this write cannot affect the column top (jar: if y <= firstAvailable-2 return).
	if y <= firstAvail-2 {
		return
	}

	if opaque {
		// A new opaque block at or above the current top raises the top to y+1.
		if y >= firstAvail {
			bs.Set(col, (y+1)-minY)
		}
		return
	}

	// Non-opaque (removal). Only the EXACT surface block matters: removing anything else
	// at/above the early-out boundary cannot lower the top.
	if firstAvail-1 == y {
		// Rescan downward for the next opaque block; its top becomes the new height.
		for ny := y - 1; ny >= minY; ny-- {
			if opaqueAt(ny) {
				bs.Set(col, (ny+1)-minY)
				return
			}
		}
		// Nothing opaque below → floor.
		bs.Set(col, 0)
	}
}
