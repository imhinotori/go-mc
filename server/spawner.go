package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// spawner.go — AI-03: a faithful-but-minimal port of net.minecraft.world.level.NaturalSpawner
// (resolved Open Question 3). PORTED (the STANDING MANDATE, idiomatic non-1:1 Go, never a GPL
// paste) from the unobfuscated 26.2 jar via
//   javap -p -c -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.NaturalSpawner
//   javap -p -c -classpath temp/cache/26.2-inner.jar net.minecraft.world.entity.SpawnPlacementTypes$1
// (this session). The vanilla shape we port the V1 SUBSET of:
//
//   NaturalSpawner.createState(spawnableChunkCount, entities, chunkGetter, mobCapCalc):
//     ranges ALL loaded entities and tallies a per-MobCategory live count (an
//     Object2IntOpenHashMap), plus a PotentialCalculator density field. -> v1 ports the
//     per-category COUNT (countByCategory) from the tick-owned store; the density/potential
//     field + LocalMobCapCalculator per-player distance weighting are DEFERRED (documented).
//
//   NaturalSpawner.getFilteredSpawningCategories(state, spawnFriendly, spawnEnemy):
//     keeps a category iff its live count < maxInstancesPerChunk * state.spawnableChunkCount
//     (the cap gate — the load-bearing anti-flood, Pitfall 3). -> v1 ports this gate for
//     CREATURE: cap = categoryCreature.maxInstancesPerChunk() * spawnableChunkCount.
//
//   NaturalSpawner.spawnForChunk / spawnCategoryForChunk / getRandomPosWithin:
//     pick a random (x,z) within the chunk, find a Y from the heightmap, then
//     isValidEmptySpawnBlock + SpawnPlacements.checkSpawnRules + isValidPositionForMob, and
//     place the mob. -> v1 picks an eligible loaded column near a player, scans a column for a
//     standable Y (the ON_GROUND placement check below), and adds ONE Pig under cap.
//     The biome spawn lists (MobSpawnSettings / WeightedList), per-position density check
//     (isRightDistanceToPlayerAndSpawnPoint MIN_SPAWN_DISTANCE=24), structure spawns
//     (isInNetherFortressBounds), and the creature-probability roll are DEFERRED (documented).
//
//   SpawnPlacementTypes$1 (ON_GROUND).isSpawnPositionOk(level, pos, type) — the bytecode this
//   session translates to:
//       if type != null && !worldBorder.isWithinBounds(pos): return false   // no border in v1
//       below = pos.below(); belowState = level.getBlockState(below)
//       if !belowState.isValidSpawn(level, below, type): return false        // SOLID surface below
//       return isValidEmptySpawnBlock(pos, type) && isValidEmptySpawnBlock(pos.above(), type)
//   and NaturalSpawner.isValidEmptySpawnBlock — "not a full collision cube, not a signal
//   source, no fluid, not PREVENT_MOB_SPAWNING_INSIDE, not block-dangerous". -> v1 collapses
//   both to: a SOLID block at (x, y-1, z) AND clear air at (x, y, z) and (x, y+1, z) (the mob's
//   ~2-block height), read through the EXISTING blockSolidAt. The light-level rule (CREATURE
//   wants brightness > 0 on the heightmap surface) is RELAXED for v1 (no light engine yet) and
//   documented as deferred.
//
// SINGLE-OWNER (TICK-05): countByCategory ranges the tick-owned entityStore and naturalSpawn
// calls entityStore.add — both on the tick goroutine, no off-tick mutation, no new goroutine,
// no xsync/ants/conc. The candidate scan is structured (snapshot-free, pure over the
// tick-owned world read) so Phase 8 / OPT-03 can move it off-tick later, mirroring the A*
// snapshot discipline — but in this plan it runs INLINE.

const (
	// spawnDistanceChunk ports NaturalSpawner.SPAWN_DISTANCE_CHUNK (javap -constants: 8): the
	// Chebyshev radius in chunk columns around a player within which natural spawns are
	// eligible. (SPAWN_DISTANCE_BLOCK = 128 = 8*16 is the block-space equivalent; MIN_SPAWN
	// _DISTANCE = 24 blocks is the per-position minimum, deferred with the density model.)
	spawnDistanceChunk = 8

	// spawnInterval is the v1 spawn-attempt cadence (the THROTTLE). Vanilla runs the spawner
	// every tick but most attempts no-op under the cap + creature-probability roll; v1 instead
	// runs naturalSpawn every spawnInterval ticks (a simple gametime % N) and makes ONE bounded
	// placement attempt, so the per-tick cost stays negligible. 20 ticks ≈ once per second.
	spawnInterval = 20

	// spawnScanYRange bounds how many world-Y a column scan probes for a standable block around
	// a reference surface, so an attempt is O(constant) — never a full -64..319 column walk.
	// v1 references the players' feet Y; ±spawnScanYRange covers a few blocks up/down.
	spawnScanYRange = 8
)

// countByCategory ranges the tick-owned entityStore and tallies the live mob count per
// MobCategory (ported from NaturalSpawner.createState's per-category tally). This is the
// load-bearing cap-accounting (Pitfall 3): the count is recomputed from the AUTHORITATIVE
// store every cycle, so a stale/uncounted mob can neither flood nor starve the world. Runs on
// the tick goroutine over tick-owned state (TICK-05).
func (t *TickLoop) countByCategory() map[mobCategory]int {
	counts := make(map[mobCategory]int)
	if t.entities == nil {
		return counts
	}
	for _, e := range t.entities.byID {
		counts[categoryOf(e.typ)]++
	}
	return counts
}

// spawnableColumns ports the "loaded chunks within SPAWN_DISTANCE of a player" eligibility set
// (NaturalSpawner only spawns in chunks near players — the chunkGetter walks the player-loaded
// columns). It returns the distinct loaded columns within spawnDistanceChunk of ANY player, in
// a deterministic order (sorted by the player iteration then the dx/dz scan) so a v1 attempt is
// reproducible for tests. A column is "loaded" iff world.Get(col) reports Ready. With no world
// or no players the set is empty (nothing spawns) — exactly vanilla's "no players, no natural
// spawns". The count of returned columns is the spawnableChunkCount that scales the cap.
func (t *TickLoop) spawnableColumns() []level.ChunkPos {
	if t.world == nil || len(t.players) == 0 {
		return nil
	}
	seen := make(map[level.ChunkPos]bool)
	var cols []level.ChunkPos
	for _, p := range t.players {
		if p == nil {
			continue
		}
		center := columnOf(p.x, p.z)
		for dx := -spawnDistanceChunk; dx <= spawnDistanceChunk; dx++ {
			for dz := -spawnDistanceChunk; dz <= spawnDistanceChunk; dz++ {
				col := level.ChunkPos{center[0] + int32(dx), center[1] + int32(dz)}
				if seen[col] {
					continue
				}
				if _, ok := t.world.Get(col); !ok {
					continue // not Ready: vanilla only spawns in loaded columns
				}
				seen[col] = true
				cols = append(cols, col)
			}
		}
	}
	return cols
}

// findStandableY ports the SpawnPlacementTypes ON_GROUND check + isValidEmptySpawnBlock for one
// (x,z) column: scan a bounded Y window for the FIRST world-Y where the block below is solid
// (the isValidSpawn surface) and the spawn block itself plus the block above are clear air (the
// mob's ~2-block height clearance). Returns the feet Y and true on success, or (0,false) if no
// standable block exists in the window. Reads through the EXISTING blockSolidAt (the same
// world read physics uses), so placement can never disagree with collision. The vanilla light
// requirement is RELAXED for v1 (documented). refY is the search center (the player's feet Y).
func (t *TickLoop) findStandableY(x, z, refY int) (int, bool) {
	// Scan downward from refY+range to refY-range so a mob lands on the highest valid surface
	// near the reference (closest to a vanilla heightmap top-non-colliding pick).
	for y := refY + spawnScanYRange; y >= refY-spawnScanYRange; y-- {
		below := t.blockSolidAt(x, y-1, z) // ON_GROUND: solid spawnable surface beneath the feet
		feet := t.blockSolidAt(x, y, z)    // isValidEmptySpawnBlock(pos): the feet block is clear
		head := t.blockSolidAt(x, y+1, z)  // isValidEmptySpawnBlock(pos.above()): head clearance
		if below && !feet && !head {
			return y, true
		}
	}
	return 0, false
}

// naturalSpawn is the faithful-but-minimal NaturalSpawner cycle (ported, v1 subset). It:
//  1. computes the spawnable columns near players + the spawnableChunkCount,
//  2. counts the live CREATURE mobs from the tick-owned store (countByCategory),
//  3. if CREATURE is UNDER its cap (maxInstancesPerChunk * spawnableChunkCount — the Pitfall 3
//     anti-flood gate), scans the spawnable columns for the first valid ON_GROUND standable
//     block and adds ONE Pig there via entityStore.add, with a real mobAI (newPigAI) attached
//     so tickAI's serverAiStep drives it (the tracker then spawns it on clients via the
//     unchanged AddEntity).
//
// It attempts ONE placement per call (the THROTTLE keeps the per-tick cost bounded). When the
// cap is reached, or there is no valid block / no spawnable column, it is a no-op — no flood,
// no starvation. Tick-owned (TICK-05). Deferred vs vanilla (all documented): the full
// multi-category density model + PotentialCalculator, the LocalMobCapCalculator per-player
// distance weighting, the per-position MIN_SPAWN_DISTANCE check, biome spawn lists + the
// creature-probability roll, structure spawns, and light-level rules.
func (t *TickLoop) naturalSpawn() {
	if t.entities == nil || t.world == nil {
		return
	}
	cols := t.spawnableColumns()
	if len(cols) == 0 {
		return // no loaded columns near a player: nothing to populate
	}

	// The CREATURE cap scales with the spawnable-chunk count, exactly as vanilla derives it
	// from state.spawnableChunkCount * maxInstancesPerChunk (getFilteredSpawningCategories).
	cap := categoryCreature.maxInstancesPerChunk() * len(cols)
	live := t.countByCategory()[categoryCreature]
	if live >= cap {
		return // AT or OVER cap: the anti-flood gate (Pitfall 3) — attempt no spawn
	}

	// Reference Y for the column scan: a player's feet (the surface a near-player spawn sits on).
	refY := t.spawnRefY()

	// Walk the eligible columns deterministically; place at the first valid ON_GROUND block.
	for _, col := range cols {
		// Probe the column-center block coords (a single candidate per column for v1; vanilla
		// rolls a random (x,z) within the chunk — deferred with the full per-chunk attempt loop).
		bx := int(col[0])*16 + 8
		bz := int(col[1])*16 + 8
		y, ok := t.findStandableY(bx, bz, refY)
		if !ok {
			continue
		}
		pig := NewEntity(t.idAlloc.AllocID(), entity.Pig, float64(bx)+0.5, float64(y), float64(bz)+0.5)
		pig.ai = newPigAI() // the real ported AI: tickAI's serverAiStep drives wander + A* nav
		t.entities.add(pig) // the unchanged tracker spawns it on clients next tick (AddEntity)
		return              // one placement per cycle (the throttle)
	}
}

// spawnRefY returns the world-Y the column scan centers on — the first player's feet, or the
// dimension floor surface when no player position is available. Tick-owned read.
func (t *TickLoop) spawnRefY() int {
	for _, p := range t.players {
		if p != nil {
			return floorI(p.y)
		}
	}
	return dimMinY + 1
}

// (compile guard) — keep the pk import wired for the world block reads naturalSpawn relies on
// via blockSolidAt -> world.GetBlock(pk.Position, …); a future refactor that inlines a read
// here uses pk.Position directly.
var _ = pk.Position{}
