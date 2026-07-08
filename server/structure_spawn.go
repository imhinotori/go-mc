package server

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/plugin/host"
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
		// finalizeSpawn (REAL, no longer a cited-constant stub): NewEntity already attached the mob's
		// AttributeMap from its DefaultAttributes supplier (the per-type base values — Witch HP 26.0,
		// Cat HP 10.0 / speed 0.30000001192092896 / atk 3.0, Villager speed 0.5). Here we run the
		// ATTRIBUTE + RNG slice of Mob.finalizeSpawn on the tick-owned level random: the FOLLOW_RANGE
		// random-spawn bonus (triangle(0.0, 0.11485) — two nextDouble() draws) + the left-handed roll
		// (nextFloat() < 0.05F). The draw ORDER is the faithful Mob.finalizeSpawn order. The returned
		// leftHanded flag has no metadata consumer yet (no left-handed SynchedEntityData bit), so it is
		// applied to the entity field and CITED: the setLeftHanded metadata wire-out lands with mob
		// metadata. The draw is performed regardless so the level RNG stream stays vanilla-faithful.
		//
		// STILL CITED STUBS (not part of the attribute subsystem, deferred to their own subsystems):
		//   - VARIANT/PROFESSION: Cat variant (CatVariant registry roll) + Villager profession/type
		//     depend on the variant/biome registries + tags NOT yet extracted; they are left at the
		//     vanilla DEFAULT (the entity spawns variant-default), structured so a real variant roll
		//     slots in here when the registry lands. Source: Cat.finalizeSpawn (variant roll),
		//     Villager.finalizeSpawn (VillagerData type/profession).
		//   - NoAI / setPersistenceRequired: req.PersistenceRequired carries the persistence flag; the
		//     persistence/despawn consumer is not wired (mobs never despawn in v1 anyway), so it is
		//     read-and-held below pending the despawn subsystem.
		e.leftHanded = attribute.FinalizeSpawn(e.attributes, t.cur().levelRandom)
		// MOB-SUB-01 (CR-01): LivingEntity.<init> setHealth(getMaxHealth()) — initialize the structure
		// mob's health to its folded MaxHealth (Witch 26.0, Cat 10.0, Villager 20.0). FinalizeSpawn has
		// already seeded the attribute map, so initSpawnHealth reads the FINAL value. WITHOUT this every
		// structure-spawned mob is born at health 0 and is permanently invulnerable (applyDamageEntity's
		// `health <= 0` guard) — the keystone silently defeated for the structure-spawn path. Shared with
		// the declared + oracle spawn paths via the one helper (entity.go) so the gap cannot recur.
		initSpawnHealth(e)
		_ = req.PersistenceRequired
		// SHULKER (Task): a structure-spawned Sentry is a FULL shulker -- attach the shulker state (peek
		// closed + the +20 covered armor, attachFace DOWN, color none) so the End City turret opens + fires
		// like a /dbg shulker. The generic drain built the entity + attribute map; this adds the tick state.
		// Cite Shulker(EntityType, Level) ctor + setRawPeekAmount(0).
		if e.typ == entity.Shulker.ID {
			e.shulker = &shulkerState{peek: shulkerPeekClosed, attachFace: block.Down, color: shulkerNoColor}
			t.shulkerSetRawPeek(e, shulkerPeekClosed)
		}
		t.cur().entities.add(e) // the ONLY off-tick-boundary store mutation; tracker broadcasts AddEntity

		// PLUGIN-02 (Plan 22) on_entity_spawn seam: fire ONCE here, immediately after the actual
		// store add — the discrete spawn occurrence — NOT from tickAI's per-tick naturalSpawn scan.
		// One emit per spawned mob, independent of entity count. Nil-guarded; the payload carries the
		// entity id + wire type id + spawn position (truncated to ints) as plain frozen scalars.
		if t.plugins != nil {
			t.plugins.Emit(host.EventEntitySpawn, host.EntitySpawnEvent{
				EntityID: int(e.id),
				TypeID:   int(e.typ),
				X:        int(e.x),
				Y:        int(e.y),
				Z:        int(e.z),
			})
		}
	}
}

// levelRandomSeedUniquifier is the port of net.minecraft.world.level.levelgen.RandomSupport's
// seedUniquifier atomic — a process-global counter that, XORed with the wall clock, makes each
// RandomSource.create() seed unique even when two are created in the same nanosecond. Vanilla:
// `seedUniquifier = seedUniquifier * 1181783497276652981L; return seedUniquifier ^ nanoTime();`
// (the same Knuth multiplier java.util.Random uses). It is touched only via atomic CAS, so creating
// level randoms from multiple goroutines never races.
var levelRandomSeedUniquifier atomic.Int64

// seedUniquifierMul is the Knuth LCG multiplier RandomSupport.generateUniqueSeed uses
// (1181783497276652981L), preserved exactly.
const seedUniquifierMul = int64(1181783497276652981)

func init() {
	// RandomSupport seeds the uniquifier at 8682522807148012L initially.
	levelRandomSeedUniquifier.Store(8682522807148012)
}

// uniqueLevelRandomSeed is the port of RandomSupport.generateUniqueSeed(): a nondeterministic unique
// seed for a level's RandomSource, exactly as vanilla seeds Level.random (the level random is NOT
// world-seed-derived — only worldgen RNG is). `uniquifier = uniquifier * MUL; return uniquifier ^
// nanoTime();`, advanced via atomic CAS so concurrent creation is race-free.
func uniqueLevelRandomSeed() int64 {
	for {
		old := levelRandomSeedUniquifier.Load()
		next := old * seedUniquifierMul
		if levelRandomSeedUniquifier.CompareAndSwap(old, next) {
			return next ^ time.Now().UnixNano()
		}
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
	// SHULKER (Task): the End City "Sentry" data-marker inhabitant -- the box-turret hostile. Resolving
	// it here lets drainStructureSpawns build a live shulker; the shulker peek/attack state is attached
	// below (the drain special-cases it so a structure-spawned sentry is a FULLY functional shulker).
	"shulker": entity.Shulker,
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
