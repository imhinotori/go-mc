package server

// entity_persist.go -- SUB-PERSIST (Part C): in-chunk (per-column) entity persistence, wiring the
// previously-DEAD saveEntities/loadEntities region path (persistence.go) to a live snapshot/restore of
// the tick-owned entityStore. Vanilla writes entities to a SEPARATE region (EntityStorage ->
// entities/r.x.z.mca, ENTITIES_TAG "Entities"), NOT the legacy in-chunk Entities list -- this is that
// modern path (the codebase already had the region IO; it just had no caller building a save.Entities
// from a live *Entity). v1 persists the entity kinds the codebase round-trips: dropped ItemEntity
// (Item/Age/PickupDelay) and generic mobs (id/Pos/Motion/Rotation/UUID/Health).
//
// CITED JAR (26.2-inner.jar):
//   - net.minecraft.world.level.chunk.storage.EntityStorage.storeEntities / loadEntities: the per-
//     column ChunkEntities <-> entities-region mapping (ENTITIES_TAG "Entities").
//   - net.minecraft.world.entity.Entity.save / saveWithoutId: id (EntityType.CODEC), Pos, Motion,
//     Rotation, OnGround, UUID, PortalCooldown, ...
//   - net.minecraft.world.entity.LivingEntity.addAdditionalSaveData: Health.
//   - net.minecraft.world.entity.item.ItemEntity.addAdditionalSaveData: Item, Age, PickupDelay.
//
// RNG SAFETY (pig oracle): the LOAD path (diskToEntity) reconstructs from saved data ONLY -- it never
// calls attribute.FinalizeSpawn (a fresh-spawn draw off the shared levelRandom) and never draws the
// ItemEntity toss velocities (it restores the saved Motion). So respawning saved entities on chunk
// load perturbs NEITHER the shared levelRandom stream NOR the per-mob AI stream (seeded from the
// entity id). initSpawnHealth is called only for a legacy record with no saved Health (a bare field
// write, RNG-free). The dogfood pig oracle uses no chunk streaming, so this path is off its trace.
//
// DOUBLE-SPAWN GUARD: the load side is driven by a one-shot SavedEntities list on the ChunkResult,
// consumed exactly once when the column becomes Ready (drainSavedEntities), mirroring how
// drainStructureSpawns consumes res.Spawns. A column loaded twice carries the saved list only on the
// first (generation/region) load; a re-Insert of an already-live column carries an empty list.

import (
	"sync"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

var (
	entityByNameOnce sync.Once
	entityByName     map[string]entity.Entity
)

func resolveEntityByName(name string) (entity.Entity, bool) {
	entityByNameOnce.Do(func() {
		entityByName = make(map[string]entity.Entity, len(entity.ByID))
		for _, e := range entity.ByID {
			entityByName[e.Name] = *e
		}
	})
	n := name
	if i := indexByte(n, ':'); i >= 0 {
		n = n[i+1:]
	}
	e, ok := entityByName[n]
	return e, ok
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func entityToDisk(e *Entity) (save.Entities, bool) {
	if e == nil || e.dead {
		return save.Entities{}, false
	}
	if e.typ == entity.Player.ID {
		return save.Entities{}, false
	}
	rec := save.Entities{
		ID:             "minecraft:" + entityTypeName(e.typ),
		Pos:            [3]float64{e.x, e.y, e.z},
		Motion:         [3]float64{e.vx, e.vy, e.vz},
		Rotation:       [2]float32{e.yaw, e.pitch},
		UUID:           uuidToInts(e.uuid),
		OnGround:       e.onGround,
		PortalCooldown: 0,
		Health:         e.health,
	}
	if e.isItem {
		rec.Age = int16(e.age)
		rec.PickupDelay = int16(e.pickupDelay)
		item := save.ItemStackDisk{
			ID:    itemName(int32(e.itemStack.ItemID)),
			Count: int32(e.itemStack.Count),
		}
		rec.Item = &item
	}
	return rec, true
}

func diskToEntity(t *TickLoop, rec save.Entities) (*Entity, bool) {
	typeRec, ok := resolveEntityByName(rec.ID)
	if !ok {
		return nil, false
	}
	id := t.idAlloc.AllocID()
	if typeRec.ID == entity.Item.ID {
		stack := component.SlotData{}
		if rec.Item != nil {
			stack.ItemID = pk.VarInt(itemNameToID(rec.Item.ID))
			stack.Count = pk.VarInt(rec.Item.Count)
		}
		ie := NewEntity(id, entity.Item, rec.Pos[0], rec.Pos[1], rec.Pos[2])
		ie.isItem = true
		ie.itemStack = stack
		ie.vx, ie.vy, ie.vz = rec.Motion[0], rec.Motion[1], rec.Motion[2]
		ie.yaw, ie.pitch = rec.Rotation[0], rec.Rotation[1]
		ie.onGround = rec.OnGround
		ie.age = int(rec.Age)
		ie.pickupDelay = int(rec.PickupDelay)
		ie.metadata = encodeItemMetadata(stack)
		return ie, true
	}
	e := NewEntity(id, typeRec, rec.Pos[0], rec.Pos[1], rec.Pos[2])
	e.vx, e.vy, e.vz = rec.Motion[0], rec.Motion[1], rec.Motion[2]
	e.yaw, e.pitch = rec.Rotation[0], rec.Rotation[1]
	e.headYaw = rec.Rotation[0]
	e.onGround = rec.OnGround
	if rec.Health > 0 {
		e.health = rec.Health
	} else {
		initSpawnHealth(e)
	}
	return e, true
}

func (t *TickLoop) snapshotColumnEntities(pos level.ChunkPos) []save.Entities {
	r := t.regionForColumn(pos)
	if r == nil || r.entities == nil {
		return nil
	}
	bucket := r.entities.buckets[pos]
	if len(bucket) == 0 {
		return nil
	}
	out := make([]save.Entities, 0, len(bucket))
	for _, e := range bucket {
		if rec, ok := entityToDisk(e); ok {
			out = append(out, rec)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uuidToInts(u [16]byte) [4]int32 {
	var out [4]int32
	for i := 0; i < 4; i++ {
		out[i] = int32(uint32(u[i*4])<<24 | uint32(u[i*4+1])<<16 | uint32(u[i*4+2])<<8 | uint32(u[i*4+3]))
	}
	return out
}

// drainSavedEntities respawns the persisted entities for a column when it becomes Ready (SUB-PERSIST,
// Part C load side), reading the parallel entities/r.x.z.mca region on the OWNER and reconstructing
// each save.Entities into a live *Entity that is added to the owning region store (the tracker then
// broadcasts AddEntity next tick, exactly like drainStructureSpawns). It is called from
// chunkReady.applyTo inside the withRegion block for the loaded column, so cur() is the owning region.
//
// DOUBLE-SPAWN GUARD: t.entitiesLoaded[pos] is set on the first drain; a subsequent Insert of the same
// column (reload / regen) short-circuits, so a column loaded twice never duplicates its saved entities.
// A "" persistDir (tests / ephemeral) is a no-op. A miss (never-saved cell) or an IO error is skipped
// (logged), and the column is still marked loaded so a bad cell is not retried every Insert.
//
// RNG SAFETY: diskToEntity does NOT draw the shared levelRandom (no FinalizeSpawn) nor the item toss
// velocities (it restores saved Motion), so this respawn perturbs no RNG stream (pig oracle safe).
func (t *TickLoop) drainSavedEntities(pos level.ChunkPos) {
	if t.persistDir == "" {
		return
	}
	if t.entitiesLoaded == nil {
		t.entitiesLoaded = make(map[level.ChunkPos]bool)
	}
	if t.entitiesLoaded[pos] {
		return // already drained for this column: never double-spawn
	}
	t.entitiesLoaded[pos] = true

	ents, ok, err := loadEntities(t.persistDir, pos)
	if err != nil {
		udebug("chunksave", "entity load %v: %v", pos, err)
		return
	}
	if !ok || len(ents) == 0 {
		return // never-saved cell / empty: nothing to respawn
	}
	for _, rec := range ents {
		e, ok := diskToEntity(t, rec)
		if !ok {
			continue // unknown/unported entity type: skip (build-data robustness)
		}
		t.cur().entities.add(e)
	}
}
