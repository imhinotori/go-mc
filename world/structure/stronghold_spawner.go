package structure

// stronghold_spawner.go — STRUCT-POLISH-02 Task 2: the stronghold silverfish SPAWNER.
//
// PortalRoom.postProcess places a SPAWNER block set to spawn SILVERFISH (a mob_spawner
// block-entity, NOT a live entity — Pitfall 5). The hasPlacedSpawner one-shot guard prevents a
// re-pass / reload double-placement (Pitfall 6). This is distinct from the swamp-hut witch/cat,
// which ARE live SpawnRequests — the silverfish is purely a block + its block-entity, so it
// never crosses the off-tick->tick entity-store seam.
//
// Source: javap StrongholdPieces$PortalRoom.postProcess (the hasPlacedSpawner block:
// getWorldPos(5,3,6); box.isInside; hasPlacedSpawner=true; setBlock(SPAWNER, 2);
// getBlockEntity instanceof SpawnerBlockEntity; setEntityId(SILVERFISH, random)).

// placeSilverfishSpawner ports PortalRoom.postProcess's spawner tail: when getWorldPos(5,3,6)
// is inside the chunk's writable box, it places the SPAWNER block + a mob_spawner block-entity
// set to silverfish (SetSpawner — a BLOCK, NOT a live entity, Pitfall 5).
//
// THE ONE-SHOT GUARD IN SULFUR'S PER-CHUNK MODEL (Pitfall 2 + Pitfall 6): the jar's mutable
// `hasPlacedSpawner` flips true at placement because vanilla writes the whole structure in ONE
// WorldGenLevel pass. Sulfur instead RE-RUNS PostProcess once per overlapping chunk on the SAME
// piece instance (place.go placeInChunk) with a re-derivable rng + the box.isInside clip — so a
// mutable in-gen guard would WRONGLY block the owning chunk's pass on a second run (and break the
// fingerprint/cross-chunk determinism tests). The box.isInside clip ALONE makes placement
// idempotent + chunk-correct within a gen (only the chunk owning (5,3,6) writes it, once per pass
// with identical bytes — exactly like createChest's unconditional-then-clip discipline).
//
// The hasPlacedSpawner guard is therefore a RELOAD skip, NOT an in-gen flag: it is set true only
// when a placed portal room is SERIALIZED (SavePiece), read back at Load, and a reloaded piece
// whose guard is true skips the placement (so a recomputed-then-reloaded start does not re-place
// the spawner — belt-and-suspenders alongside the region-hit bypass, which already skips
// PostProcess entirely for a saved chunk). On a FRESH gen the guard is false and placement runs
// idempotently. This mirrors the persisted-guard reload property (20-03) without the per-chunk
// re-run breakage a mutable in-gen flag would cause.
//
// Source: javap StrongholdPieces$PortalRoom.postProcess (the hasPlacedSpawner block) — the
// observable placement (a silverfish spawner block at world (5,3,6)) is identical; only the
// guard's lifecycle is adapted to Sulfur's per-chunk re-run (an OPTIMIZATION-equivalent that
// preserves the byte-identical placed result).
func (r *StrongholdPortalRoom) placeSilverfishSpawner(view WorldGenView, box BoundingBox) {
	if r.hasPlacedSpawner {
		return // a RELOADED piece (guard restored from NBT) skips — no double placement (Pitfall 6)
	}
	wx := r.getWorldX(5, 6)
	wy := r.getWorldY(3)
	wz := r.getWorldZ(5, 6)
	if !box.IsInside(wx, wy, wz) {
		return // out of THIS chunk's box -> the spawner belongs to a different chunk's pass
	}
	// setBlock(SPAWNER, 2): the spawner block (placeBlock applies the orientation transform +
	// the box clip, same as the jar's setBlock at the world pos). Idempotent across per-chunk
	// re-runs (the same byte at the same pos) — the in-gen guard is NOT set here (see above).
	r.placeBlock(view, spawnerStateID, 5, 3, 6, box)
	// getBlockEntity instanceof SpawnerBlockEntity -> setEntityId(SILVERFISH, random): the
	// mob_spawner BE carrying SpawnData.entity.id = "minecraft:silverfish" (SetSpawner). The
	// entityID is written at the SAME world pos the SPAWNER block occupies.
	view.SetSpawner(wx, wy, wz, "minecraft:silverfish")
}
