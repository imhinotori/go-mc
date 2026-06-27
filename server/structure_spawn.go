package server

import (
	"strings"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/world"
)

// structure_spawn.go — STRUCT-POLISH-02: the TICK-side drain of ChunkResult.Spawns.
//
// THE OFF-TICK -> TICK SEAM (TICK-05 / Pitfall 5): worldgen records structure-inhabitant
// SpawnRequests OFF the tick (the worker scheduler goroutine, world/structure/spawn.go +
// world/neighborhood.go RecordSpawn), carries them on the IMMUTABLE world.ChunkResult.Spawns,
// and the TICK drains them HERE — on the owner goroutine, inside chunkReady.applyTo (tick.go) —
// allocating an id, building the Entity, and entityStore.add(e). The worker NEVER touches the
// store; this is the ONLY mutation that crosses the boundary for a structure spawn, and it runs
// on the owner, so the seam is -race clean by construction (the same discipline as the chunk
// Insert it sits beside). GAMEPLAY-01's tracker (tracker.go) then broadcasts ClientboundAddEntity
// for the new mob on the next tick WITHOUT any new tracker code — the witch/cat/villager rides
// the same store-add path a dropped Item does (spawnBlockDrop / NewItemEntity).
//
// The silverfish is NOT here: the stronghold places a mob_spawner BLOCK-ENTITY (Pitfall 5), so
// it never becomes a SpawnRequest and never reaches the store — it is a block in the chunk.

// drainStructureSpawns adds each structure-inhabitant SpawnRequest from an immutable ChunkResult
// to the tick-owned entity store. Tick-owned (TICK-05): called ONLY from chunkReady.applyTo on
// the owner goroutine. An unresolvable entity-type id (a garbled region tag / an as-yet-unported
// entity) is a no-op SKIP — never a panic (the region-NBT robustness contract; the worldgen
// path only ever records the faithful witch/cat/villager ids). Each spawn draws a fresh id from
// the shared EntityIDAllocator and is built via NewEntity, so the tracker broadcasts it next tick.
func (t *TickLoop) drainStructureSpawns(res world.ChunkResult) {
	for _, req := range res.Spawns {
		rec, ok := resolveSpawnEntity(req.EntityType)
		if !ok {
			continue // unknown/unported entity id -> skip (never panic — build-data robustness)
		}
		// NewEntity copies the type's wire id + AABB dims; the spawn is north-facing + stationary
		// (the jar's snapTo sets yaw/pitch 0). finalizeSpawn attribute/variant init is the cited
		// stub below — a structure mob spawns at its vanilla DEFAULT state (no variant/profession
		// read yet), structured so finalizeSpawn becomes a real read when the attribute subsystem
		// lands. PersistenceRequired (the jar's setPersistenceRequired) is carried for the same
		// reason — the entity never despawns once that flag is a real read.
		e := NewEntity(t.idAlloc.AllocID(), rec, req.X, req.Y, req.Z)
		// CITED STUB (CLAUDE.md): finalizeSpawn(ServerLevelAccessor, DifficultyInstance, STRUCTURE,
		// null) initializes mob attributes + variants (cat variant, witch held item) + sets
		// NoAI/persistence. The attribute/effect/variant subsystems are not built yet, so the mob
		// spawns at its vanilla-default state (the value finalizeSpawn would leave for a default
		// difficulty with no SpawnGroupData) — equal to the vanilla default, NOT baked away: when
		// the attribute subsystem lands, finalizeSpawn becomes a real per-type call here.
		// Source: Mob.finalizeSpawn / SwampHutPiece.postProcess (WITCH/CAT) / StructureTemplate
		// .placeEntities (createEntityIgnoreException).
		_ = req.PersistenceRequired
		t.entities.add(e) // the ONLY off-tick-boundary store mutation; tracker broadcasts AddEntity
	}
}

// structureSpawnTypes maps the bare entity name (the SpawnRequest id minus the "minecraft:"
// namespace) to its data/entity record. Scoped to the entities the faithful structure-spawn
// paths emit: the swamp-hut witch+cat and the village villagers+cat. An id not in the map
// resolves to (Entity{}, false) -> the drain skips it (a garbled tag / a v3 mob). Kept as an
// explicit small map (not a global name->Entity index, which data/entity does not generate) so
// the resolvable set is the exact faithful inhabitant list and an unexpected id fails closed.
var structureSpawnTypes = map[string]entity.Entity{
	"witch":    entity.Witch,
	"cat":      entity.Cat,
	"villager": entity.Villager,
}

// resolveSpawnEntity resolves a SpawnRequest entity-type id (e.g. "minecraft:witch" or a bare
// "witch") to its data/entity record. Strips the namespace before the map lookup. Returns
// (record, true) for a known structure inhabitant, (zero, false) otherwise (the drain skips it).
func resolveSpawnEntity(id string) (entity.Entity, bool) {
	name := id
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	rec, ok := structureSpawnTypes[name]
	return rec, ok
}
