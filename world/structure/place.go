package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// placeInChunk ports net.minecraft.world.level.levelgen.structure.StructureStart.
// placeInChunk: for a start touching chunk C (writableBox), iterate ALL of the start's
// pieces and, for each piece whose bbox INTERSECTS the writable box, call PostProcess with
// that box — which writes blocks CLIPPED to C's 16x16 slice via placeBlock's IsInside guard.
//
// A multi-chunk structure is placed ONCE PER OVERLAPPING CHUNK, each call writing only that
// chunk's slice (Pitfall #2). It is IDEMPOTENT: the piece RNG is re-derivable from
// (worldSeed, startChunkX, startChunkZ) via the already-ported SetLargeFeatureSeed, and the
// writes are position-clipped — so placing the same start from C's pass and from N's pass
// produces identical, non-duplicated blocks.
//
// AfterPlace (the desert-pyramid suspicious-sand / terrain-beard) is STUBBED (v3 deferral,
// documented): scattered temples set no terrain beard, and the suspicious-sand/archaeology
// loot is a v3 subsystem — the VISIBLE pyramid (sandstone, mosaic, TNT, chest blocks) is
// fully placed by PostProcess. The stub is kept for the Phase-16 jigsaw that DOES beard.
//
// Source: javap -c / CFR StructureStart.placeInChunk (the intersect-then-postProcess loop).
func placeInChunk(start *StructureStart, view WorldGenView, writableBox BoundingBox, chunkPos level.ChunkPos, worldSeed int64) {
	if start == nil || len(start.Pieces) == 0 {
		return
	}
	for _, piece := range start.Pieces {
		if !piece.BoundingBox().Intersects(writableBox) {
			continue
		}
		// The piece RNG is re-derived per-placement from the OWNING chunk (idempotent: the
		// same start placed from any overlapping chunk seeds the identical stream). This is
		// GenerationContext.makeRandom = WorldgenRandom.setLargeFeatureSeed(seed, cx, cz).
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(worldSeed, int(start.ChunkPos[0]), int(start.ChunkPos[1]))
		piece.PostProcess(view, writableBox, chunkPos, rng)
	}
	// afterPlace(...) — STUB (v3 deferral): suspicious-sand + terrain beard, documented above.
}

// PlaceStructures is the PLACE-pass driver that fills 14-01's empty placeStructures hook:
// gather the starts touching chunk C (C's OWN starts + the neighbor starts named in C's
// REFERENCES, via StartsForChunk) and placeInChunk each into C's writable column. Called
// from world.Decorate at the END of decoration (after applyBiomeDecoration — structures
// overwrite terrain + features). Runs on the scheduler goroutine (single-owner).
//
// The cache is already populated by ComputeStarts(C) + ComputeReferences(C) earlier in the
// same Decorate. Writing through the WorldGenView (the 3x3 Neighborhood) means out-of-3x3
// slices are dropped by the Neighborhood AND out-of-writable-box cells are dropped by
// placeBlock — so C receives exactly its own 16x16xH slice of every overlapping structure.
func (c *Cache) PlaceStructures(view WorldGenView, center level.ChunkPos, worldSeed int64, minY, height int) {
	writableBox := WritableArea(center, minY, height)
	for _, start := range c.StartsForChunk(center) {
		placeInChunk(start, view, writableBox, center, worldSeed)
	}
}
