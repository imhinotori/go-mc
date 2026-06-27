package world

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/world/structure"
)

// Neighborhood is a WorldGenLevel-like read/write view over a 3x3 of carved chunks,
// centered on `center`. It is the GEN2-02 decoration window: a feature reads/writes
// blocks relative to a center chunk but may touch into the 8 surrounding chunks
// (vanilla blockStateWriteRadius=1). Reads outside the 3x3 (or out of the Y range)
// return air; writes outside the 3x3 (or out of Y range) are DROPPED.
//
// It is used ONLY on the worker's single scheduler goroutine — single-owner, no
// locking. Every in-range SetBlock keeps the 3 WORLDGEN heightmaps (WORLD_SURFACE_WG /
// OCEAN_FLOOR_WG / MOTION_BLOCKING) live via level.HeightmapUpdate, so a later
// same-step feature sees an earlier placement.
//
// Phase 10's Decorate is a NO-OP body that never CALLS SetBlock — but the proxy + its
// heightmap-update wiring is built + correct so the feature phase (Phase 11+) inherits
// it. The 3 opaque predicates are built here from block.IsAir + a resolved water
// StateID (the only ready-made predicate is block.IsAir; there is no motion/fluid helper):
//
//	WORLD_SURFACE_WG opaque = NOT air
//	OCEAN_FLOOR_WG   opaque = NOT air AND NOT water   (motion-blocking, no fluid)
//	MOTION_BLOCKING  opaque = NOT air OR water        (blocks motion OR fluid)
type Neighborhood struct {
	center level.ChunkPos
	minY   int
	height int
	chunks map[int64]*level.Chunk // 9 entries: center + 8 neighbors, packed-pos keyed
	air    block.StateID
	water  block.StateID

	// spawns buffers the structure-inhabitant SpawnRequests recorded during the PLACE pass
	// (PostProcess -> RecordSpawn). It is the OFF-TICK half of the off-tick->tick seam
	// (TICK-05 / Pitfall 5): the worker only RECORDS here; placeStructures drains it onto the
	// emitted ChunkResult.Spawns, and the tick performs the only entity-store add. Owned by the
	// single scheduler goroutine (same as chunks), so the append is race-free by construction.
	// A defensive cap (spawnRequestCap) bounds the per-chunk count (T-20-12) — a faithful
	// structure emits a tiny fixed set (witch+cat, a handful of villagers), never thousands.
	spawns []structure.SpawnRequest
}

// spawnRequestCap bounds the per-chunk SpawnRequest count (T-20-12 DoS): a faithful structure
// emits a small fixed set of inhabitants (swamp hut = witch+cat; a village house = a few
// villagers), so a chunk legitimately records at most a few dozen. The cap backstops a
// malformed template entity list from ballooning the buffer; excess requests are dropped
// (never panic). 256 is far above any faithful per-chunk inhabitant count.
const spawnRequestCap = 256

// newNeighborhood builds the 3x3 view. chunks must hold the center + its 8 neighbors,
// keyed by packPos. minY/height come from the generator's Dims(). The air/water
// StateIDs are resolved once for the GetBlock fallback + the heightmap predicates.
func newNeighborhood(center level.ChunkPos, chunks map[int64]*level.Chunk, minY, height int) *Neighborhood {
	return &Neighborhood{
		center: center,
		minY:   minY,
		height: height,
		chunks: chunks,
		air:    block.ToStateID[block.Air{}],
		water:  block.ToStateID[block.Water{Level: 0}],
	}
}

// chunkAt maps a world (wx,wz) to its owning chunk in the 3x3, if present. cx=wx>>4,
// cz=wz>>4; the lookup uses the same packPos packing the staging map uses.
func (n *Neighborhood) chunkAt(wx, wz int) (*level.Chunk, bool) {
	cx, cz := wx>>4, wz>>4
	ch, ok := n.chunks[packPos(level.ChunkPos{int32(cx), int32(cz)})]
	return ch, ok
}

// localIndex is the y-major in-section local index used everywhere in the section
// layout: (wy&15)<<8 | (wz&15)<<4 | (wx&15).
func (n *Neighborhood) localIndex(wx, wy, wz int) int {
	return (wy&15)<<8 | (wz&15)<<4 | (wx & 15)
}

// GetBlock returns the block state at world (wx,wy,wz). Outside the 3x3 or out of the
// Y range it returns air (the WorldGenLevel "unloaded reads as air" semantics).
func (n *Neighborhood) GetBlock(wx, wy, wz int) block.StateID {
	ch, ok := n.chunkAt(wx, wz)
	if !ok {
		return n.air
	}
	sec := (wy - n.minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return n.air
	}
	return ch.Sections[sec].GetBlock(n.localIndex(wx, wy, wz))
}

// SetBlock writes a block state at world (wx,wy,wz). Writes outside the 3x3 (vanilla
// blockStateWriteRadius=1) or out of the Y range are DROPPED. An in-range write updates
// the target chunk's section AND keeps the 3 worldgen heightmaps live via
// level.HeightmapUpdate (per-type predicate + an opaqueAt closure over the chunk column).
//
// Phase 10's no-op Decorate never reaches this method; it is wired + correct for the
// feature phase.
func (n *Neighborhood) SetBlock(wx, wy, wz int, st block.StateID) {
	ch, ok := n.chunkAt(wx, wz)
	if !ok {
		return // outside the 3x3 -> dropped (blockStateWriteRadius=1)
	}
	sec := (wy - n.minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return // out of Y range -> dropped
	}
	ch.Sections[sec].SetBlock(n.localIndex(wx, wy, wz), st)

	lx, lz := wx&15, wz&15
	// opaqueColumnAt re-reads the EXISTING block at local (lx, y, lz) of this chunk for the
	// downward rescan when a surface block is removed. The closures below apply the same
	// per-type predicate to that existing state.
	opaqueColumnAt := func(y int) block.StateID {
		s := (y - n.minY) >> 4
		if s < 0 || s >= len(ch.Sections) {
			return n.air
		}
		return ch.Sections[s].GetBlock((y&15)<<8 | (lz&15)<<4 | (lx & 15))
	}

	if hm := ch.HeightMaps.WorldSurfaceWG; hm != nil {
		level.HeightmapUpdate(hm, lx, wy, lz, n.minY,
			n.worldSurfaceWG(st),
			func(y int) bool { return n.worldSurfaceWG(opaqueColumnAt(y)) })
	}
	if hm := ch.HeightMaps.OceanFloorWG; hm != nil {
		level.HeightmapUpdate(hm, lx, wy, lz, n.minY,
			n.oceanFloorWG(st),
			func(y int) bool { return n.oceanFloorWG(opaqueColumnAt(y)) })
	}
	if hm := ch.HeightMaps.MotionBlocking; hm != nil {
		level.HeightmapUpdate(hm, lx, wy, lz, n.minY,
			n.motionBlocking(st),
			func(y int) bool { return n.motionBlocking(opaqueColumnAt(y)) })
	}
}

// SetBlockEntity records a loot-bearing chest block entity at world (wx,wy,wz) into the
// owning chunk's BlockEntity list — the worldgen side of the WorldGenView.SetBlockEntity
// seam (20-02 Task 2). The BE carries the {LootTable, LootTableSeed} NBT the chest rolls
// LAZILY on first open (server/chest_loot.go unpackLootTable); NO items are written at gen
// (Pitfall 2). Out-of-window writes (outside the 3x3 or out of the Y range) are DROPPED, the
// same clip SetBlock applies. The NBT keys mirror RandomizableContainerBlockEntity:
// "LootTable" (string id) + "LootTableSeed" (long). Single-owner (the worker scheduler
// goroutine), so the append into the immutable-on-emit chunk is race-free by construction.
func (n *Neighborhood) SetBlockEntity(wx, wy, wz int, typ block.EntityType, lootTable string, lootSeed int64) {
	ch, ok := n.chunkAt(wx, wz)
	if !ok {
		return // outside the 3x3 -> dropped (blockStateWriteRadius=1)
	}
	if wy < n.minY || wy >= n.minY+n.height {
		return // out of Y range -> dropped
	}
	be := level.BlockEntity{
		Y:    int16(wy),
		Type: typ,
		Data: chestLootNBT(lootTable, lootSeed),
	}
	// PackXZ stores the LOCAL (0..15) x/z; a successful pack is required for the wire encode.
	if !be.PackXZ(wx&15, wz&15) {
		return // unpackable local coord (never for an in-window write) -> drop, never panic
	}
	ch.BlockEntity = append(ch.BlockEntity, be)
}

// SetSpawner records a mob_spawner BLOCK-ENTITY at world (wx,wy,wz) set to spawn entityID
// (the stronghold's silverfish). This is a BLOCK, not a live entity (Pitfall 5): the BE carries
// SpawnData.entity.id, mirroring BaseSpawner.setEntityId (jar). The SPAWNER block itself is
// placed by a preceding SetBlock; SetSpawner appends the mob_spawner BE. Out-of-window writes
// are dropped (same clip as SetBlock/SetBlockEntity). Single-owner (the scheduler goroutine).
func (n *Neighborhood) SetSpawner(wx, wy, wz int, entityID string) {
	ch, ok := n.chunkAt(wx, wz)
	if !ok {
		return // outside the 3x3 -> dropped
	}
	if wy < n.minY || wy >= n.minY+n.height {
		return // out of Y range -> dropped
	}
	be := level.BlockEntity{
		Y:    int16(wy),
		Type: block.EntityTypes["minecraft:mob_spawner"],
		Data: spawnerNBT(entityID),
	}
	if !be.PackXZ(wx&15, wz&15) {
		return
	}
	ch.BlockEntity = append(ch.BlockEntity, be)
}

// spawnerNBT builds the mob_spawner block-entity NBT carrying SpawnData.entity.id = entityID —
// the ONLY field BaseSpawner.setEntityId writes at gen (the Delay/MinSpawnDelay/SpawnCount/...
// fields are the spawner's runtime defaults, set when the spawner first ticks, never at gen).
// The shape is {SpawnData: {entity: {id: "<entityID>"}}}, mirroring BaseSpawner.save's
// "SpawnData" key + SpawnData's "entity" field + setEntityId's putString("id", ...). nbt.Marshal
// emits a full document ([0x0A][nameLen=0][payload]); BlockEntity.Data wants the bare compound
// payload, so the 3-byte root header is stripped (the chestLootNBT convention).
//
// Source: javap BaseSpawner.setEntityId (SpawnData.getEntityToSpawn().putString("id", key)) +
// BaseSpawner.save (the "SpawnData" tag) + SpawnData codec ("entity" field).
func spawnerNBT(entityID string) nbt.RawMessage {
	doc, err := nbt.Marshal(struct {
		SpawnData struct {
			Entity struct {
				ID string `nbt:"id"`
			} `nbt:"entity"`
		} `nbt:"SpawnData"`
	}{SpawnData: struct {
		Entity struct {
			ID string `nbt:"id"`
		} `nbt:"entity"`
	}{Entity: struct {
		ID string `nbt:"id"`
	}{ID: entityID}}})
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}
}

// RecordSpawn buffers a structure-inhabitant SpawnRequest for the tick to drain (TICK-05 /
// Pitfall 5): the worker NEVER spawns a live entity off-tick — it only RECORDS the request, and
// placeStructures forwards the buffer onto ChunkResult.Spawns where the tick performs the only
// store add. Single-owner (the scheduler goroutine). The per-chunk count is capped (T-20-12);
// a request beyond the cap is dropped (a faithful structure never reaches it).
func (n *Neighborhood) RecordSpawn(req structure.SpawnRequest) {
	if len(n.spawns) >= spawnRequestCap {
		return // T-20-12: bound the per-chunk spawn-request count (never panic)
	}
	n.spawns = append(n.spawns, req)
}

// Spawns returns the structure-inhabitant SpawnRequests recorded during the PLACE pass — the
// off-tick buffer placeStructures attaches to the emitted ChunkResult.Spawns. The returned
// slice aliases the buffer (read-only on the scheduler goroutine; the worker copies it into the
// immutable ChunkResult before emit). Empty when no structure recorded an inhabitant.
func (n *Neighborhood) Spawns() []structure.SpawnRequest { return n.spawns }

// chestLootNBT builds the chest block-entity NBT compound carrying only {LootTable,
// LootTableSeed} — the lazy-roll seam (no item list). The keys mirror
// RandomizableContainer.LOOT_TABLE_TAG ("LootTable") and LOOT_TABLE_SEED_TAG
// ("LootTableSeed") from the jar. nbt.Marshal emits a full document ([0x0A][nameLen=0]
// [payload]); BlockEntity.Data wants the bare compound payload, so the 3-byte root header is
// stripped (the same convention resolveIglooState uses for block-state Properties).
func chestLootNBT(lootTable string, lootSeed int64) nbt.RawMessage {
	doc, err := nbt.Marshal(struct {
		LootTable     string `nbt:"LootTable"`
		LootTableSeed int64  `nbt:"LootTableSeed"`
	}{LootTable: lootTable, LootTableSeed: lootSeed})
	if err != nil {
		// The input is two trivial fixed-type fields; a marshal error is a build bug, not a
		// runtime condition. Return an empty compound rather than panic on the worker goroutine.
		return nbt.RawMessage{Type: nbt.TagCompound}
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}
}

// The 3 worldgen-heightmap opaque predicates, built from block.IsAir + the resolved
// water StateID (mirroring surface/system.go's predicate logic — no ready-made
// motion/fluid helper exists):
//
//	WORLD_SURFACE_WG = NOT air
//	OCEAN_FLOOR_WG   = NOT air AND NOT water   (motion-blocking, no fluid)
//	MOTION_BLOCKING  = NOT air OR water        (blocks motion OR fluid)
func (n *Neighborhood) worldSurfaceWG(st block.StateID) bool { return !block.IsAir(st) }
func (n *Neighborhood) oceanFloorWG(st block.StateID) bool {
	return !block.IsAir(st) && st != n.water
}
func (n *Neighborhood) motionBlocking(st block.StateID) bool {
	return !block.IsAir(st) || st == n.water
}
