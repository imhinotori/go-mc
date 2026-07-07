package server

// spawner_block.go - the MONSTER SPAWNER block-entity (SPAWNER-01): a 1:1 port of
// net.minecraft.world.level.BaseSpawner and SpawnerBlockEntity over the 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p this session). The mob spawner is a per-tick DRIVE
// (t.spawners keyed by world position, the conduit/beacon twin):
//   - every server tick runs isNearPlayer (requiredPlayerRange, default 16),
//   - counts spawnDelay DOWN one per tick; on -1 (freshly loaded) it rerolls immediately,
//   - on spawnDelay == 0 it runs the spawnCount (default 4) burst: each spawn draws a random
//     position within spawnRange (default 4), enforces maxNearbyEntities (default 6), spawns via
//     spawnVanillaMob, then rerolls delay into [minSpawnDelay(200), maxSpawnDelay(800)].
//
// VERIFIED javap constants (BaseSpawner ctor bytecode): spawnDelay 20, minSpawnDelay 200,
// maxSpawnDelay 800, spawnCount 4, maxNearbyEntities 6, requiredPlayerRange 16, spawnRange 4.
//
// Per-spawn RNG draw order (lambda serverTick position supplier, EXACT): x uses two nextDouble,
// y uses one nextInt(3), z uses two nextDouble; then one nextFloat for the yaw (snapTo). The delay
// reroll: if maxSpawnDelay <= minSpawnDelay set minSpawnDelay (no draw), else minSpawnDelay +
// nextInt(maxSpawnDelay - minSpawnDelay).
//
// SINGLE-OWNER (TICK-05): t.spawners is written on placement and read/mutated only on the tick
// goroutine. RNG uses the OWNING region seeded levelRandom (Level.random analogue), never a mob
// per-entity stream, so the pig oracle is unperturbed.
//
// PIG ORACLE: a spawner only ticks if a spawner block EXISTS near a player. The oracle world has
// NO spawner block, so tickSpawners iterates an empty map and serverTick never runs - byte-identical.

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// BaseSpawner default constants (BaseSpawner ctor bytecode, VERIFIED javap -c -p).
const (
	spawnerDefaultDelay         = 20   // DEFAULT_SPAWN_DELAY (bipush 20 -> spawnDelay)
	spawnerDefaultMinDelay      = 200  // DEFAULT_MIN_SPAWN_DELAY (sipush 200)
	spawnerDefaultMaxDelay      = 800  // DEFAULT_MAX_SPAWN_DELAY (sipush 800)
	spawnerDefaultSpawnCount    = 4    // DEFAULT_SPAWN_COUNT (iconst_4)
	spawnerDefaultMaxNearby     = 6    // DEFAULT_MAX_NEARBY_ENTITIES (bipush 6)
	spawnerDefaultRequiredRange = 16   // DEFAULT_REQUIRED_PLAYER_RANGE (bipush 16)
	spawnerDefaultSpawnRange    = 4    // DEFAULT_SPAWN_RANGE (iconst_4)
	spawnerSpawnAnimLevelEvent  = 2004 // serverTick level.levelEvent(2004, pos, 0) mob-spawn event
)

// spawnerBE is the tick-owned state of one mob spawner block-entity - the Go analogue of BaseSpawner
// narrowed to the fields serverTick/delay read/write. The client-only spin animation floats, the
// displayEntity render mob, and the WeightedList of SpawnData spawnPotentials are modeled as a single
// mobName (the entity type to spawn); a multi-entry weighted SpawnPotentials list is a documented
// deferral (the fixed-SpawnData path draws no extra RNG, matching a placed spawner).
type spawnerBE struct {
	spawnDelay          int // BaseSpawner.spawnDelay - per-tick countdown; -1 forces an immediate reroll
	minSpawnDelay       int // BaseSpawner.minSpawnDelay (default 200)
	maxSpawnDelay       int // BaseSpawner.maxSpawnDelay (default 800)
	spawnCount          int // BaseSpawner.spawnCount (default 4) - per-burst spawn attempts
	maxNearbyEntities   int // BaseSpawner.maxNearbyEntities (default 6) - per-position cap
	requiredPlayerRange int // BaseSpawner.requiredPlayerRange (default 16) - isNearPlayer radius
	spawnRange          int // BaseSpawner.spawnRange (default 4) - random-position + cap-AABB spread

	// mobName is the getOrCreateNextSpawnData entity-type-to-spawn, reduced to the spawnVanillaMob
	// name (the SpawnData.getEntityToSpawn id NBT string). Empty is an unconfigured spawner (spawns
	// nothing until set). serverTick no-ops the burst when mobName is empty.
	mobName string
}

// newSpawnerBE builds a spawnerBE with the exact BaseSpawner ctor defaults. mobName is the entity to
// spawn (empty for an unconfigured spawner).
func newSpawnerBE(mobName string) *spawnerBE {
	return &spawnerBE{
		spawnDelay:          spawnerDefaultDelay,
		minSpawnDelay:       spawnerDefaultMinDelay,
		maxSpawnDelay:       spawnerDefaultMaxDelay,
		spawnCount:          spawnerDefaultSpawnCount,
		maxNearbyEntities:   spawnerDefaultMaxNearby,
		requiredPlayerRange: spawnerDefaultRequiredRange,
		spawnRange:          spawnerDefaultSpawnRange,
		mobName:             mobName,
	}
}

// spawnerIsNearPlayer ports BaseSpawner.isNearPlayer(level, pos) == level.hasNearbyAlivePlayer(
// x+0.5, y+0.5, z+0.5, requiredPlayerRange): true iff some non-spectator, alive player is within
// requiredPlayerRange blocks of the spawner CENTER. EntityGetter.hasNearbyAlivePlayer bytecode: for
// each player pass NO_SPECTATORS + LIVING_ENTITY_STILL_ALIVE, then distanceToSqr(x,y,z); return true
// if range < 0 or distSq < range*range. v1 players are always non-spectator + alive, so the two
// predicate filters are no-ops here; the distance test is the load-bearing gate.
//
// 1:1 net.minecraft.world.level.BaseSpawner.isNearPlayer + EntityGetter.hasNearbyAlivePlayer
func (t *TickLoop) spawnerIsNearPlayer(pos pk.Position, be *spawnerBE) bool {
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	rng := float64(be.requiredPlayerRange)
	for _, p := range t.players {
		if p == nil {
			continue
		}
		dx := p.x - cx
		dy := p.y - cy
		dz := p.z - cz
		distSq := dx*dx + dy*dy + dz*dz
		// range < 0 (any distance) or distSq < range*range. The default 16 is positive.
		if rng < 0 || distSq < rng*rng {
			return true
		}
	}
	return false
}

// spawnerServerTick ports BaseSpawner.serverTick(serverLevel, pos) EXACTLY: the isNearPlayer gate,
// the -1 immediate-reroll, the countdown, then the spawnCount burst (each a random-position spawn
// under the maxNearbyEntities cap) and the trailing delay reroll. Runs on the tick goroutine. The
// isSpawnerBlockEnabled() gamerule defaults TRUE in v1 (no gamerule store yet - a cited constant that
// becomes a real read when gamerules land), so the gate reduces to isNearPlayer.
//
// 1:1 net.minecraft.world.level.BaseSpawner.serverTick
func (t *TickLoop) spawnerServerTick(pos pk.Position, be *spawnerBE) {
	// if (!isNearPlayer(level, pos) || !level.isSpawnerBlockEnabled()) return;
	if !t.spawnerIsNearPlayer(pos, be) {
		return
	}

	// if (spawnDelay == -1) delay(level, pos);
	if be.spawnDelay == -1 {
		t.spawnerDelay(pos, be)
	}

	// if (spawnDelay > 0) { spawnDelay--; return; }
	if be.spawnDelay > 0 {
		be.spawnDelay--
		return
	}

	// boolean spawned = false; RandomSource random = level.getRandom();
	spawned := false
	r := t.cur()
	if r == nil || r.levelRandom == nil {
		// No seeded level RNG (a bare test loop): cannot draw a faithful position/yaw. Reroll so
		// spawnDelay leaves 0 (else it would spin at 0 every tick) and return.
		t.spawnerDelay(pos, be)
		return
	}
	rng := r.levelRandom

	// SpawnData spawnData = getOrCreateNextSpawnData(...): a fixed single SpawnData (mobName). The
	// WeightedList.getRandom draw is a no-op for a one-entry (or cached) list, so no RNG is consumed
	// here. An empty mobName means no configured entity: the burst no-ops (but the countdown/reroll
	// still runs so it does not spin).
	if be.mobName == "" {
		t.spawnerDelay(pos, be)
		return
	}

	// for (int i = 0; i < spawnCount; i++) { ... }
	for i := 0; i < be.spawnCount; i++ {
		// The random spawn position (lambda serverTick position supplier). The Pos NBT is absent for a
		// live spawner, so orElseGet draws it. Draw order is EXACT: x two nextDouble, y one nextInt(3),
		// z two nextDouble.
		//   x = pos.getX() + (nextDouble() - nextDouble()) * spawnRange + 0.5
		x := float64(pos.X) + (rng.NextDouble()-rng.NextDouble())*float64(be.spawnRange) + 0.5
		//   y = pos.getY() + nextInt(3) - 1
		y := float64(pos.Y) + float64(rng.NextIntN(3)) - 1
		//   z = pos.getZ() + (nextDouble() - nextDouble()) * spawnRange + 0.5
		z := float64(pos.Z) + (rng.NextDouble()-rng.NextDouble())*float64(be.spawnRange) + 0.5

		bx := floorI(x)
		by := floorI(y)
		bz := floorI(z)

		// if (!level.noCollision(type.getSpawnAABB(x, y, z))) continue; - the spawn box must be clear of
		// solid collision. v1 collapses noCollision(spawnAABB) to the mob ~2-block footprint being clear
		// air (feet + head not solid), read through the SAME blockSolidAt physics/natural-spawner use.
		// The full AABB/type-dimension noCollision is a documented narrowing.
		if t.blockSolidAt(bx, by, bz) || t.blockSolidAt(bx, by+1, bz) {
			continue
		}

		// checkSpawnRules / customSpawnRules / PEACEFUL-difficulty guard: DEFERRED subset (documented). A
		// placed vanilla spawner ignores the light/biome spawn rules for the default (customSpawnRules is
		// empty; checkSpawnRules is consulted only when customSpawnRules is present); the PEACEFUL
		// hostile-skip needs a difficulty store (not wired - cited constant). So v1 always proceeds to the
		// spawn, matching a default-configured spawner on a non-peaceful world. CITE BaseSpawner.

		// Entity entity = EntityType.loadEntityRecursive(...): spawn the declared mob at the position via
		// the reused spawn primitive spawnVanillaMob, which builds the entity with its declared 1:1 AI
		// (the finalizeSpawn analogue for declared mobs) + reseeds the per-entity RNG by id. A nil result
		// (missing declaration) mirrors: if (entity == null) { delay; return; }.
		entity := t.spawnVanillaMob(be.mobName, x, y, z)
		if entity == nil {
			t.spawnerDelay(pos, be)
			return
		}

		// int nearby = level.getEntities(forExactClass(entity), AABB(pos).inflate(spawnRange),
		//     NO_SPECTATORS).size(); if (nearby >= maxNearbyEntities) { delay(level, pos); return; }
		// The CAP gate. Vanilla runs getEntities BEFORE tryAddFreshEntity, so the freshly-loaded entity
		// is not yet in the level and is not counted. v1 spawnVanillaMob adds eagerly, so exclude the
		// just-spawned entity from the count to match the vanilla pre-existing-count semantics.
		nearby := t.spawnerCountNearby(pos, be, entity)
		if nearby >= be.maxNearbyEntities {
			// Over the cap: undo the just-added mob (vanilla never added it), reroll, stop the burst.
			entity.dead = true
			r.entities.remove(entity.id)
			t.spawnerDelay(pos, be)
			return
		}

		// entity.snapTo(entity.getX(), entity.getY(), entity.getZ(), random.nextFloat() * 360, 0): the
		// YAW draw (one nextFloat). Position unchanged (already at x,y,z), so only yaw/headYaw/pitch update.
		yaw := rng.NextFloat() * 360.0
		entity.yaw = yaw
		entity.headYaw = yaw
		entity.pitch = 0

		// if (mob) mob.finalizeSpawn(...): the declared-mob AI built by spawnVanillaMob IS the
		// finalizeSpawn analogue (buildAIFromDecl over the declared 1:1 goals). No extra call needed.
		// tryAddFreshEntityWithPassengers(entity): the add already happened inside spawnVanillaMob (the
		// store add is the Go analogue), so this always succeeds in v1; the failure branch is unreachable.

		// level.levelEvent(2004, pos, 0) + gameEvent(ENTITY_PLACE) + mob.spawnAnim(): client/gameevent
		// cosmetic, cite-deferred no-op seam (like the dispenser/brewing-stand levelEvent seams). The
		// observable gameplay (the mob now live at the position with its AI) is what this port lands.
		t.spawnerLevelEvent(pos, spawnerSpawnAnimLevelEvent)

		spawned = true
	}

	// if (spawned) delay(level, pos): reroll after a burst that placed at least one mob.
	if spawned {
		t.spawnerDelay(pos, be)
	}
}

// spawnerCountNearby ports serverTick maxNearbyEntities cap query: level.getEntities(
// forExactClass(entity), new AABB(pos).inflate(spawnRange), NO_SPECTATORS).size(). It counts live
// entities of the SAME exact type as spawned whose hitbox intersects the spawner cube inflated by
// spawnRange on all faces, EXCLUDING the just-spawned entity (vanilla queries the level before the
// entity is added via tryAddFreshEntity; v1 spawn primitive adds eagerly, so we subtract it here).
// Reads the region entity store chunk buckets around the spawner (bounded broad phase). Tick-owned.
//
// 1:1 net.minecraft.world.level.BaseSpawner.serverTick (the getEntities size cap check)
func (t *TickLoop) spawnerCountNearby(pos pk.Position, be *spawnerBE, spawned *Entity) int {
	r := t.cur()
	if r == nil || r.entities == nil {
		return 0
	}
	// new AABB(pos, pos+1).inflate(spawnRange): [x-range, x+1+range] on each axis.
	rng := float64(be.spawnRange)
	minX := float64(pos.X) - rng
	minY := float64(pos.Y) - rng
	minZ := float64(pos.Z) - rng
	maxX := float64(pos.X) + 1 + rng
	maxY := float64(pos.Y) + 1 + rng
	maxZ := float64(pos.Z) + 1 + rng

	// The inflated AABB spans at most a couple chunks each way (spawnRange default 4 is 9-wide); a
	// 1-chunk-radius broad phase around the spawner column covers it, then the exact AABB intersect.
	count := 0
	for _, e := range r.entities.near(float64(pos.X), float64(pos.Z), 1) {
		if e == nil || e == spawned {
			continue // exclude the just-spawned mob (vanilla counts the pre-existing level)
		}
		if e.dead {
			continue // NO_SPECTATORS + a live-entity filter: a dead mob is not counted
		}
		if e.typ != spawned.typ {
			continue // forExactClass(entity): only the SAME exact entity type is counted
		}
		hw := e.width / 2
		eMinX, eMaxX := e.x-hw, e.x+hw
		eMinY, eMaxY := e.y, e.y+e.height
		eMinZ, eMaxZ := e.z-hw, e.z+hw
		if minX < eMaxX && maxX > eMinX && minY < eMaxY && maxY > eMinY && minZ < eMaxZ && maxZ > eMinZ {
			count++
		}
	}
	return count
}

// spawnerDelay ports BaseSpawner.delay(level, pos): reroll spawnDelay into [minSpawnDelay,
// maxSpawnDelay] using the level RNG, then (for a fixed SpawnData) re-pick the next spawn data (a
// no-op draw for a one-entry list) and broadcastEvent(1) (cite-deferred client sync). The reroll:
// if maxSpawnDelay <= minSpawnDelay set minSpawnDelay, else minSpawnDelay +
// random.nextInt(maxSpawnDelay - minSpawnDelay).
//
// 1:1 net.minecraft.world.level.BaseSpawner.delay
func (t *TickLoop) spawnerDelay(pos pk.Position, be *spawnerBE) {
	// RandomSource random = level.random (the SAME level RNG serverTick draws) - the OWNING region
	// seeded levelRandom. When no seeded RNG exists (a bare test loop), the reroll falls back to
	// minSpawnDelay (the maxSpawnDelay <= minSpawnDelay branch value), a valid in-range delay.
	if be.maxSpawnDelay <= be.minSpawnDelay {
		be.spawnDelay = be.minSpawnDelay
	} else {
		r := t.cur()
		if r == nil || r.levelRandom == nil {
			be.spawnDelay = be.minSpawnDelay // no RNG: the deterministic lower-bound in-range delay
		} else {
			be.spawnDelay = be.minSpawnDelay + int(r.levelRandom.NextIntN(int32(be.maxSpawnDelay-be.minSpawnDelay)))
		}
	}
	// spawnPotentials.getRandom(random).ifPresent(setNextSpawnData): no-op for a fixed single SpawnData
	// (no extra RNG draw). broadcastEvent(level, pos, 1): a client render sync - cite-deferred (no BE
	// broadcast seam in v1). The observable state (the rerolled spawnDelay) is what this lands.
	_ = pos
}

// spawnerLevelEvent is the cited faithful no-op seam for serverTick level.levelEvent(2004, pos, 0)
// (the mob-spawn particle/anim client event) + the implied gameEvent(ENTITY_PLACE) + mob.spawnAnim().
// v1 has no ClientboundLevelEvent broadcast plumbing in the block-entity path yet (the dispenser/
// brewing-stand levelEvent seams are the same cited no-op). CITE BaseSpawner.serverTick levelEvent(2004).
func (t *TickLoop) spawnerLevelEvent(_ pk.Position, _ int) {}

// tickSpawners ticks every live mob-spawner block-entity once per tick (the ServerLevel-side
// blockEntityTicker fan-out for SpawnerBlockEntity.serverTick -> BaseSpawner.serverTick). Called from
// tickWorld (the tickConduits twin). Each spawner reads the block at its position; if it is no longer
// a spawner (broken/replaced/unloaded), the block-entity is dropped. A nil world leaves spawners
// un-ticked (tests may drive spawnerServerTick directly). Tick-owned (SPAWNER-01).
func (t *TickLoop) tickSpawners() {
	if len(t.spawners) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, be := range t.spawners {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isSpawnerBlock(state) {
			delete(t.spawners, pos)
			continue
		}
		t.spawnerServerTick(pos, be)
	}
}

// resolveSpawner returns the tick-owned spawnerBE for pos, creating one with the BaseSpawner defaults
// on first access - the analogue of a freshly-placed spawner default SpawnerBlockEntity. mobName is the
// entity to configure (empty for an unconfigured spawner). Returns nil only when pos is not a spawner
// block (or the world is unloaded). Tick-owned (t.spawners, the t.conduits twin).
func (t *TickLoop) resolveSpawner(pos pk.Position, mobName string) *spawnerBE {
	if t.spawners == nil {
		t.spawners = make(map[pk.Position]*spawnerBE)
	}
	if be, ok := t.spawners[pos]; ok {
		return be
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isSpawnerBlock(state) {
		return nil
	}
	be := newSpawnerBE(mobName)
	t.spawners[pos] = be
	return be
}
