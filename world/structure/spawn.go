package structure

// spawn.go — STRUCT-POLISH-02: the off-tick SpawnRequest carrier (the worldgen->tick seam).
//
// THE OFF-TICK -> TICK HAZARD (TICK-05 / Pitfall 5): worldgen runs OFF the tick (an immutable
// world.ChunkResult assembled on the worker scheduler goroutine), but the entity store is
// TICK-OWNED (server/entity_store.go). A structure piece deciding "spawn a witch here" during
// PostProcess CANNOT touch the store off-thread. Instead it RECORDS a SpawnRequest (a plain
// data value — entity-type id + world position + the one-shot finalize flag) through the
// WorldGenView (RecordSpawn); the recorder buffers the request into the chunk's ChunkResult;
// and the TICK drains ChunkResult.Spawns onto the store (server/structure_spawn.go), where
// GAMEPLAY-01's tracker broadcasts AddEntity for free (the same store-add path spawnBlockDrop
// rides). The worker NEVER mutates the store — the request is a value, single-owner, mirroring
// ChunkResult's immutable handoff discipline. The -race gate (TestStructureSpawnSeam) proves it.
//
// NOT a SpawnRequest: the stronghold silverfish is a SPAWNER BLOCK (a mob_spawner block-entity
// set to silverfish), NOT a live entity — it never reaches the store and is placed in the chunk
// via SetBlock + the spawner block-entity (stronghold_spawner.go). Only the swamp-hut witch+cat
// and the village template entities (villager/cat) are live SpawnRequests.

// SpawnRequest is the immutable off-tick carrier for ONE structure inhabitant: the entity-type
// id (e.g. "minecraft:witch"), the world spawn position (block-center floats, matching the jar's
// snapTo(x+0.5, y, z+0.5)), and PersistenceRequired (the jar's setPersistenceRequired() — a
// structure-spawned mob never despawns). It is a VALUE (no live entity pointer) so it crosses
// the off-tick->tick boundary with the same single-owner discipline as ChunkResult.Chunk.
//
// The entity-type id is the registry STRING (not a numeric id) so the structure package need
// not import data/entity (which lives above it); the tick-side drain resolves the string to a
// data/entity.Entity record (server/structure_spawn.go). This mirrors how the chest BE carries
// the loot-table id string rather than a resolved table.
//
// Source: the jar's per-piece spawn tail — SwampHutPiece.postProcess (WITCH.create +
// setPersistenceRequired + snapTo + finalizeSpawn) / StructureTemplate.placeEntities
// (createEntityIgnoreException over the template entityInfoList).
type SpawnRequest struct {
	// EntityType is the entity registry id (e.g. "minecraft:witch", "minecraft:cat",
	// "minecraft:villager"). The tick-side drain resolves it to a data/entity record.
	EntityType string
	// X, Y, Z is the world spawn position. The jar's spawn tail uses snapTo(blockX+0.5, blockY,
	// blockZ+0.5) for the hardcoded witch/cat; the template path uses the transformed Vec3 pos.
	X, Y, Z float64
	// PersistenceRequired ports setPersistenceRequired(): a structure inhabitant never despawns.
	// All structure spawns set it (witch/cat hardcoded; villagers via NoAI/PersistenceRequired in
	// vanilla finalize) — carried so the tick can set it on the built entity once that flag is real.
	PersistenceRequired bool
}
