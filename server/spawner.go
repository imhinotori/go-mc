package server

import (
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

	// friendlySpawnInterval ports the ServerChunkCache.tickChunks spawnFriendly cadence:
	// spawnFriendly = (getGameTime() % 400L == 0) (javap: ldc2_w 400l; lrem; ifne). The FRIENDLY
	// categories (CREATURE, AMBIENT, AXOLOTLS, WATER_CREATURE, WATER_AMBIENT, UNDERGROUND_WATER_CREATURE
	// -- every isFriendly() category) are only offered to getFilteredSpawningCategories on this 400-tick
	// cycle; MONSTER (isFriendly()==false) is offered every tick. 400 is a multiple of spawnInterval(20),
	// so the (gametime%400==0) test still fires correctly given naturalSpawn is itself called only at
	// gametime%20==0. CITE: net.minecraft.server.level.ServerChunkCache.tickChunks.
	friendlySpawnInterval = 400

	// spawnScanYRange bounds how many world-Y a column scan probes for a standable block around
	// a reference surface, so an attempt is O(constant) — never a full -64..319 column walk.
	// v1 references the players' feet Y; ±spawnScanYRange covers a few blocks up/down.
	spawnScanYRange = 8

	// spawnMagicNumber ports NaturalSpawner.MAGIC_NUMBER = (int)Math.pow(17.0, 2.0) = 289. The
	// per-category mob cap is `maxInstancesPerChunk * spawnableChunkCount / MAGIC_NUMBER`
	// (NaturalSpawner$SpawnState.canSpawnForCategoryGlobal bytecode: imul then idiv by MAGIC_NUMBER).
	// WITHOUT this divisor the cap was maxInstancesPerChunk * spawnableChunkCount (~2890 creatures for
	// a radius-8 area) — the world flooded with mobs. With it the cap is ~10 (CREATURE max) for a
	// fully-loaded spawn area, matching vanilla. CITE: net.minecraft.world.level.NaturalSpawner.
	spawnMagicNumber = 289
)

// creatureCap is the vanilla per-category global cap for CREATURE:
// maxInstancesPerChunk * spawnableChunkCount / MAGIC_NUMBER (NaturalSpawner$SpawnState
// .canSpawnForCategoryGlobal). A live count >= this blocks further CREATURE spawns. The single
// source of the formula so the pre-submit gate (naturalSpawn) and the apply-time re-check
// (spawnCandidatesReady.applyTo) can never diverge.
func creatureCap(spawnableChunkCount int) int {
	return categoryCreature.maxInstancesPerChunk() * spawnableChunkCount / spawnMagicNumber
}

// monsterCap is the vanilla per-category global cap for MONSTER (Phase 35, SC#3):
// maxInstancesPerChunk(70) * spawnableChunkCount / MAGIC_NUMBER(289)
// (NaturalSpawner$SpawnState.canSpawnForCategoryGlobal — the SAME imul-then-idiv-by-MAGIC_NUMBER
// bytecode creatureCap ports, only the per-category max differs: 70 for MONSTER vs 10 for CREATURE).
// A live MONSTER count >= this blocks further hostile spawns. The /289 divisor is KEPT (without it
// the cap would be ~70*spawnableChunkCount ≈ tens of thousands of hostiles — a flood). The single
// source of the MONSTER formula so the pre-submit gate (naturalSpawn) and the apply-time re-check
// (spawnCandidatesReady.applyTo) can never diverge. CITE: net.minecraft.world.level.NaturalSpawner.
func monsterCap(spawnableChunkCount int) int {
	return categoryMonster.maxInstancesPerChunk() * spawnableChunkCount / spawnMagicNumber
}

// categorySpawnCap returns the per-category global cap for the given MobCategory (Phase 35-02). It is
// the single dispatch the apply-time re-check (spawnCandidatesReady.applyTo) uses so the CREATURE pass
// and the MONSTER pass each re-check their OWN cap. Both formulas are identical (maxInstancesPerChunk *
// count / MAGIC_NUMBER); the dispatch keeps the apply path category-driven instead of cap-fn-hardcoded.
func categorySpawnCap(cat mobCategory, spawnableChunkCount int) int {
	if cat == categoryMonster {
		return monsterCap(spawnableChunkCount)
	}
	return creatureCap(spawnableChunkCount)
}

// Day/night gametime windows for the daylight day/night proxy (35-CONTEXT SC#4). The vanilla day is
// 24000 ticks; hostiles are active from dusk (~13000) to dawn (~23000) -- the night portion of the
// day-time cycle. These bound the proxy the SPIDER daylight-flee gate (ai_goals_attack.go isBright)
// still uses for its getLightLevelDependentMagicValue()>=0.5 day/night read; the natural-spawn
// darkness gate no longer uses this proxy -- it reads the REAL light engine (isDarkEnoughToSpawn).
const (
	dayLengthTicks  = 24000 // vanilla day length (one full day/night cycle in ticks)
	nightStartTicks = 13000 // dusk: night begins
	nightEndTicks   = 23000 // dawn: night ends
)

// isNightByGametime is the day/night GAMETIME proxy: true during the [13000,23000) night portion of
// `t.gametime % dayLengthTicks`. It is NOT the spawn darkness gate (that is the real light-based
// isDarkEnoughToSpawn below). It stands in for the not-yet-built day/night SKY_LIGHT_LEVEL
// environment-attribute clock for the ONE remaining consumer that has no per-position light read:
// the Spider daylight-flee proxy (Spider$SpiderAttackGoal getLightLevelDependentMagicValue()>=0.5,
// ai_goals_attack.go isBright). gametime (tick.go) is a monotonic non-negative tick counter (++ once
// per consumed step, never decremented), so dayTime is always in [0,24000).
func (t *TickLoop) isNightByGametime() bool {
	dayTime := t.gametime % dayLengthTicks
	return dayTime >= nightStartTicks && dayTime < nightEndTicks
}

// Overworld DimensionType monster-spawn light parameters (data/minecraft/dimension_type/overworld.json:
// monster_spawn_block_light_limit: 0, monster_spawn_light_level: UniformInt{0..7}). These are the
// DimensionType.monsterSpawnBlockLightLimit() / monsterSpawnLightTest() reads Monster.isDarkEnoughToSpawn
// consults. v1 is single-dimension overworld; a future multi-dimension wiring threads the per-dimension
// values here (the nether uses blockLightLimit 15 -> the BLOCK-light branch is skipped, and its own
// monster_spawn_light_level). CITE: net.minecraft.world.level.dimension.DimensionType.
const (
	monsterSpawnBlockLightLimit = 0 // overworld: BLOCK light must be <= 0 (any block light blocks the spawn)
	monsterSpawnLightTestMin    = 0 // UniformInt min_inclusive
	monsterSpawnLightTestMax    = 7 // UniformInt max_inclusive
)

// isDarkEnoughToSpawn ports net.minecraft.world.entity.monster.Monster.isDarkEnoughToSpawn(level, pos,
// random) 1:1 over the REAL light engine (server/light.go, world/manager_light.go -- the LevelLightEngine
// ported in the light commits), replacing the former day/night gametime proxy. The bytecode this
// translates (javap -c -p net.minecraft.world.entity.monster.Monster):
//
//	if (level.getBrightness(SKY, pos) > random.nextInt(32)) return false;          // SKY short-circuit
//	int limit = dimensionType.monsterSpawnBlockLightLimit();
//	if (limit < 15 && level.getBrightness(BLOCK, pos) > limit) return false;        // BLOCK-light gate
//	int b = isThundering ? getMaxLocalRawBrightness(pos, 10) : getMaxLocalRawBrightness(pos);
//	return b <= dimensionType.monsterSpawnLightTest().sample(random);              // light-test sample
//
// RNG DRAW ORDER (crucial -- this runs on the spawn stream, cur().levelRandom, the Level.random
// analogue): the nextInt(32) SKY sample is drawn FIRST and UNCONDITIONALLY (exactly the jar: the
// getBrightness(SKY) short-circuit is `getBrightness(SKY,pos) > random.nextInt(32)`, so even a skylit
// cell that returns false HAS drawn the nextInt(32)). Only if the SKY and BLOCK gates BOTH pass is the
// monsterSpawnLightTest sample drawn -- UniformInt(0,7).sample == Mth.randomBetweenInclusive(r,0,7) ==
// r.nextInt(7-0+1)+0 == r.nextInt(8) (verified: UniformInt.sample -> Mth.randomBetweenInclusive
// bytecode). The two draws land at the vanilla isValidSpawnPostitionForType -> checkSpawnRules position
// inside spawnPackAt (natural_spawner.go), AFTER the biome pick/packSize-reset and BEFORE the yaw draw.
//
// getMaxLocalRawBrightness(pos) = getRawBrightness(pos, getSkyDarken()); getSkyDarken() is the level's
// ambient-darkness term (15 - SKY_LIGHT_LEVEL). The day/night SKY_LIGHT_LEVEL clock is a not-yet-built
// subsystem, so skyDarken stays at the vanilla DAY default (skyDarkenDay = 0, light.go), which makes a
// SURFACE cell (raw SKY light 15) read getMaxLocalRawBrightness == 15 > any nextInt(8) sample -> the
// gate returns false -> NO daytime surface hostile spawn (the reported bug's fix). A dark cave cell
// (SKY 0, BLOCK 0) reads 0 <= nextInt(8) frequently -> hostiles spawn underground, exactly as vanilla.
// [DEFERRED: getSkyDarken() day/night clock -> NIGHT surface spawns land when the environment-attribute
// time clock lands (the SAME skyDarkenDay deferral in light.go); the light read + RNG draws are 1:1 now.]
// CITE: net.minecraft.world.entity.monster.Monster.isDarkEnoughToSpawn;
// net.minecraft.world.level.dimension.DimensionType.monsterSpawnLightTest (UniformInt 0..7);
// net.minecraft.util.valueproviders.UniformInt.sample -> Mth.randomBetweenInclusive.
func (t *TickLoop) isDarkEnoughToSpawn(pos pk.Position) bool {
	r := t.cur().levelRandom
	// if (level.getBrightness(SKY, pos) > random.nextInt(32)) return false; -- the SKY short-circuit.
	// The nextInt(32) is ALWAYS drawn (it is on the RHS of the comparison), even when SKY is low.
	skyBrightness := t.getBrightnessSky(pos)
	if skyBrightness > int(r.NextIntN(32)) {
		return false
	}
	// if (limit < 15 && level.getBrightness(BLOCK, pos) > limit) return false; -- the BLOCK-light gate.
	// Overworld limit == 0, so any block light (> 0) at the position blocks the spawn. No RNG here.
	if monsterSpawnBlockLightLimit < 15 && t.getBrightnessBlock(pos) > monsterSpawnBlockLightLimit {
		return false
	}
	// int b = isThundering ? getMaxLocalRawBrightness(pos, 10) : getMaxLocalRawBrightness(pos);
	var b int
	if t.isThundering() {
		b = t.rawBrightness(pos, 10)
	} else {
		b = t.maxLocalRawBrightness(pos)
	}
	// return b <= monsterSpawnLightTest().sample(random); -- UniformInt(0,7).sample == nextInt(8).
	sample := int(r.NextIntN(monsterSpawnLightTestMax-monsterSpawnLightTestMin+1)) + monsterSpawnLightTestMin
	return b <= sample
}

// countByCategory ranges the tick-owned entityStore and tallies the live mob count per
// MobCategory (ported from NaturalSpawner.createState's per-category tally). This is the
// load-bearing cap-accounting (Pitfall 3): the count is recomputed from the AUTHORITATIVE
// store every cycle, so a stale/uncounted mob can neither flood nor starve the world. Runs on
// the tick goroutine over tick-owned state (TICK-05).
func (t *TickLoop) countByCategory() map[mobCategory]int {
	counts := make(map[mobCategory]int)
	if t.cur().entities == nil {
		return counts
	}
	for _, e := range t.cur().entities.all() {
		counts[categoryOf(e.typ)]++
	}
	return counts
}

// countByCategoryAcrossRegions tallies the live mob count per MobCategory across EVERY region's
// store (Phase-27 STEP-3, Pitfall 1: the cross-region spawn cap). It runs on the coordinator at the
// barrier (quiescent — every region joined), so reading multiple region stores is race-clean. It is
// the authoritative cap re-check spawnCandidatesReady.applyTo uses before adding a mob: a stale
// per-region snapshot can never over-spawn past the GLOBAL cap because the live count spans regions.
func (t *TickLoop) countByCategoryAcrossRegions() map[mobCategory]int {
	counts := make(map[mobCategory]int)
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.all() {
			counts[categoryOf(e.typ)]++
		}
	}
	return counts
}

// mobNearAcrossRegions reports whether any entity sits within rangeBlocks horizontal distance of
// (x,z) across EVERY region (Phase-27 STEP-3: the cross-region anti-piling guard the spawn apply
// uses). It runs at the quiescent barrier so reading multiple region stores is race-clean. It uses
// the cross-region broad phase (entitiesNearAcrossRegions) so the scan stays bounded to the
// candidate's column neighbourhood, not the whole world.
func (t *TickLoop) mobNearAcrossRegions(x, z, rangeBlocks float64) bool {
	r2 := rangeBlocks * rangeBlocks
	for _, e := range t.entitiesNearAcrossRegions(x, z, 1) {
		dx := e.x - x
		dz := e.z - z
		if dx*dx+dz*dz <= r2 {
			return true
		}
	}
	return false
}

// spawnableColumns ports the "loaded chunks within SPAWN_DISTANCE of a player" eligibility set
// (NaturalSpawner only spawns in chunks near players — the chunkGetter walks the player-loaded
// columns). It returns the distinct loaded columns within spawnDistanceChunk of ANY player, in
// a deterministic order (sorted by the player iteration then the dx/dz scan) so a v1 attempt is
// reproducible for tests. A column is "loaded" iff world.Get(col) reports Ready. With no world
// or no players the set is empty (nothing spawns) — exactly vanilla's "no players, no natural
// spawns". The count of returned columns is the spawnableChunkCount that scales the cap.
func (t *TickLoop) spawnableColumns() []level.ChunkPos {
	if t.world() == nil || len(t.players) == 0 {
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
				if _, ok := t.world().Get(col); !ok {
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

// spawnAttemptsPerCycle bounds how many random candidate (x,z) positions a single spawn cycle
// probes for a standable block. Vanilla NaturalSpawner picks a random chunk + getRandomPosWithin;
// v1 rolls this many random in-column positions and (off-tick) keeps the standable ones, so a
// cycle's scan is O(constant). The owner then places at most one (the throttle) from those.
const spawnAttemptsPerCycle = 8

// spawnCandidatePick is one random in-column position the OWNER rolls before submitting the
// off-tick scan. The (x,z) is chosen on the owner (a trivial rand); the EXPENSIVE part — whether
// that column has a standable ON_GROUND block — is what the off-tick worker computes over the
// solidity snapshot. Keeping the random pick on the owner means the snapshot copies only the
// exact candidate columns' Y-windows (a tight copy), not the whole spawnable area.
type spawnCandidatePick struct{ x, z int }

// spawnSnapshot is the IMMUTABLE solidity copy the off-tick spawn scan reads — the OPT-03 analogue
// of pathRegion (path_region.go). It mirrors the load-bearing seam discipline (07-RESEARCH Pitfall
// 1 / 08-RESEARCH Pitfall 1): the candidate columns' block solidity is COPIED on the OWNER before
// the scan is submitted, so the worker reads this frozen value and NEVER the live ChunkManager
// (which the tick mutates via tickChunks/SetBlock). It is keyed by the candidate (x,z) column the
// owner pre-picked, storing the solid bits over [minY..maxY] for each, so the off-tick
// findStandableYIn is a pure lookup. The map is built once on the owner and read-only thereafter.
type spawnSnapshot struct {
	refY       int                     // the scan center Y (player feet) carried into the worker
	minY, maxY int                     // the snapshot's inclusive Y-window (refY ± spawnScanYRange, padded)
	columns    map[[2]int]map[int]bool // (x,z) -> y -> solid; out-of-snapshot reads treated as non-solid
}

// solidAt reports whether (x,y,z) was solid in the frozen snapshot. A position outside the copied
// window or column is reported NON-solid (air): the scan only probes the exact picked columns over
// the bounded window, so an out-of-window read is never a real spawn-relevant block — treating it
// as air keeps the ON_GROUND check (solid-below + clear feet/head) faithful within the window. This
// is the spawn analogue of pathRegion.solidAt (whose out-of-box policy is solid, for the A* frontier
// containment; here air is correct because the scan never needs blocks beyond its own window).
func (s *spawnSnapshot) solidAt(x, y, z int) bool {
	col, ok := s.columns[[2]int{x, z}]
	if !ok {
		return false
	}
	return col[y]
}

// findStandableYIn ports findStandableY's ON_GROUND scan over the SNAPSHOT instead of the live
// world: scan the bounded Y window for the first Y whose block below is solid and whose feet+head
// are clear air, reading s.solidAt (the frozen copy) — NEVER t.blockSolidAt. This is the pure,
// off-tick-safe twin of findStandableY (they share the identical below/feet/head logic; only the
// solidity source differs), so a candidate found here is exactly one the synchronous scan would
// have found against the same blocks. Returns the feet Y and true, or (0,false) if no standable
// block exists in the window.
func findStandableYIn(s *spawnSnapshot, x, z int) (int, bool) {
	for y := s.refY + spawnScanYRange; y >= s.refY-spawnScanYRange; y-- {
		below := s.solidAt(x, y-1, z)
		feet := s.solidAt(x, y, z)
		head := s.solidAt(x, y+1, z)
		if below && !feet && !head {
			return y, true
		}
	}
	return 0, false
}

// snapshotSpawnColumns COPIES the block solidity of the pre-picked candidate columns over the
// scan Y-window into an IMMUTABLE spawnSnapshot, ON the owner goroutine — the OPT-03 mirror of
// snapshotRegion. It reads through the SAME blockSolidAt the synchronous scan used (so the off-tick
// result can never disagree with the live world about where a block is), but only for the exact
// (x,z) columns the owner rolled, over [refY-range-1 .. refY+range+1] (the extra ±1 covers the
// below/head reads at the window edges). The returned value is frozen here; the off-tick worker
// consumes it with no race (08-RESEARCH Pitfall 1). Tick-owned (called only on the owner).
func (t *TickLoop) snapshotSpawnColumns(picks []spawnCandidatePick, refY int) *spawnSnapshot {
	minY := refY - spawnScanYRange - 1
	maxY := refY + spawnScanYRange + 1
	s := &spawnSnapshot{
		refY:    refY,
		minY:    minY,
		maxY:    maxY,
		columns: make(map[[2]int]map[int]bool, len(picks)),
	}
	for _, p := range picks {
		key := [2]int{p.x, p.z}
		if _, done := s.columns[key]; done {
			continue // a duplicate random pick: its solidity is already copied
		}
		col := make(map[int]bool, maxY-minY+1)
		for y := minY; y <= maxY; y++ {
			if t.blockSolidAt(p.x, y, p.z) { // the SAME live read physics uses — copied here, frozen after
				col[y] = true
			}
		}
		s.columns[key] = col
	}
	return s
}

// naturalSpawn is the faithful-but-minimal NaturalSpawner cycle (ported, v1 subset) — OPT-03 SPLITS
// it so the read-only candidate SCAN runs OFF-TICK while the spawn MUTATION rejoins on the owner.
// ON the tick (here) it:
//  1. gates single-in-flight (one scan at a time, like OPT-01's !pending gate) — if a scan is
//     already pending it returns,
//  2. computes the spawnable columns near players + the spawnableChunkCount,
//  3. counts the live CREATURE mobs from the tick-owned store (countByCategory) and, if CREATURE is
//     AT/OVER its cap (maxInstancesPerChunk * spawnableChunkCount — the Pitfall 3 anti-flood gate),
//     attempts NO scan (no submit),
//  4. otherwise rolls spawnAttemptsPerCycle random in-column candidate (x,z) picks, COPIES those
//     columns' solidity into an immutable spawnSnapshot (mirroring snapshotRegion), and SUBMITS the
//     standable-Y scan over the SNAPSHOT to spawnPool. On a successful submit it sets
//     spawnScanPending; on pool overload it leaves the flag clear and simply skips this cycle
//     (retrying next spawnInterval — 08-RESEARCH Pitfall 4).
//
// The scan worker reads ONLY the snapshot (no live world/store — 08-RESEARCH Pitfall 3) and hands
// back the standable candidates as spawnCandidatesReady on asyncIn2; the OWNER re-checks the cap +
// the mobNear packing guard and adds ONE Pig in spawnCandidatesReady.applyTo (async.go). The
// MUTATION (entityStore.add + idAlloc) stays single-owner (TICK-05); only the column/Y scan moved
// off-tick. Every existing guard survives: the spawnInterval throttle (still gated in tickAI), the
// CREATURE cap (gated here AND re-checked on apply), the mobNear packing guard (re-checked on
// apply), and the bounded findStandableY window (the snapshot is sized to it). Deferred vs vanilla
// (all documented): the full multi-category density model + PotentialCalculator, the
// LocalMobCapCalculator per-player distance weighting, the per-position MIN_SPAWN_DISTANCE check,
// biome spawn lists + the creature-probability roll, structure spawns, and light-level rules.
func (t *TickLoop) naturalSpawn() {
	if t.cur().entities == nil || t.world() == nil {
		return
	}
	if t.cur().spawnScanPending {
		return // a scan is already in flight: single-in-flight gate (Pitfall 4 / OPT-01 !pending)
	}
	// ServerChunkCache.tickChunks: the ENTIRE natural spawner runs only when getGameRules().get(SPAWN_MOBS)
	// (ex-doMobSpawning) is true. SPAWN_MOBS false -> no createState, no spawnForChunk, zero spawns. CITE:
	// ServerChunkCache.tickChunks (SPAWN_MOBS gate).
	if !t.gameRule(ruleSpawnMobs) {
		return
	}
	cols := t.spawnableColumns()
	if len(cols) == 0 {
		return // no loaded columns near a player: nothing to populate
	}

	spawnableChunkCount := len(cols)
	// Reference Y for the column scan: a player's feet (the surface a near-player spawn sits on).
	refY := t.spawnRefY()

	// POPULATION BOUND -- vanilla bounds the live mob POPULATION two ways, and BOTH are now live in
	// Sulfur (so the entity count stabilizes at the vanilla cap and never runs to thousands, task #12):
	//   1. the per-category spawn CAP (creatureCap/monsterCap): submitSpawnScanFor GATES against the
	//      GLOBAL live count (spawnLiveCount -> countByCategoryAcrossRegions) BEFORE submitting a scan,
	//      and spawnCandidatesReady.applyTo (async.go) RE-CHECKS the same live count on the owner before
	//      the add -- so no spawn ever pushes the population past the cap (the load-bearing anti-flood);
	//   2. the per-mob Mob.checkDespawn cull (despawn.go), run at the TOP of the tickAI per-mob loop
	//      (tick_phases.go, before serverAiStep) for EVERY live AI mob: it reads the noActionTime idle
	//      counter (incremented in serverAiStep, ai_mob.go) + getNearestPlayer distance and DISCARDS a
	//      mob past its EntityType.getCategory() despawn distance (instant cull) or, past the soft
	//      radius after noActionTime>600, on a random.nextInt(800)==0 roll -- respecting the
	//      persistenceRequired/requiresCustomPersistence guards. So far mobs are culled continuously and
	//      the spawner backfills only up to the cap.
	// Together the cap gate (spawns can't exceed the cap) and checkDespawn (idle/far mobs are removed)
	// keep the world at the vanilla steady-state. Deferred vs vanilla (documented): the per-biome
	// MobSpawnSettings spawn weights and the full LocalMobCapCalculator per-player distance weighting.
	// CITE: net.minecraft.world.entity.Mob.checkDespawn; NaturalSpawner.getFilteredSpawningCategories.

	// getFilteredSpawningCategories(state, spawnFriendly, spawnEnemy) over SPAWNING_CATEGORIES (P0-05, the
	// external-audit fix that WIRES ALL MobCategory pools, not just CREATURE + MONSTER). Vanilla
	// ServerChunkCache.tickChunks computes spawnFriendly = (getGameTime() % 400 == 0) and passes
	// spawnEnemy = this.spawnEnemies (isSpawningMonsters), then getFilteredSpawningCategories iterates
	// SPAWNING_CATEGORIES (MobCategory.values() minus MISC, in ordinal order MONSTER, CREATURE, AMBIENT,
	// AXOLOTLS, UNDERGROUND_WATER_CREATURE, WATER_CREATURE, WATER_AMBIENT) keeping a category iff:
	//     (spawnFriendly || !category.isFriendly()) && (spawnEnemy || !category.isPersistent())
	//     && state.canSpawnForCategoryGlobal(category)   [the per-category cap, re-checked in submitSpawnScanFor]
	// So MONSTER (friendly=false, persistent=false) is eligible EVERY tick; CREATURE (friendly=true,
	// persistent=true) needs spawnFriendly (every 400 ticks) AND spawnEnemy; and AMBIENT/AXOLOTLS/WATER_*
	// (friendly=true, persistent=false) need only spawnFriendly (every 400 ticks). This ports the vanilla
	// spawn CADENCE (task 4): passives + water + ambient fire on the 400-tick friendly cycle, hostiles
	// every tick. CITE: ServerChunkCache.tickChunks (spawnFriendly = gameTime%400==0; spawnEnemy =
	// spawnEnemies); NaturalSpawner.getFilteredSpawningCategories; MobCategory.isFriendly/isPersistent.
	//
	// The OPT-03 single-in-flight gate (spawnScanPending) admits ONE scan per cycle, so we iterate the
	// filtered categories and submit the first that actually submits a scan; a category at cap /
	// pool-overloaded falls through to the next. The per-position spawn-rules gates
	// (Monster.isDarkEnoughToSpawn for MONSTER, Animal/Rabbit.checkSpawnRules for CREATURE, and the
	// WATER/AMBIENT placement predicates once those mobs are declared) still run per-candidate inside
	// spawnPackAt at the exact vanilla isValidSpawnPostitionForType point -- this cadence loop only
	// chooses WHICH category's scan to submit.
	spawnFriendly := t.gametime%friendlySpawnInterval == 0
	spawnEnemy := t.isSpawningMonsters()
	for _, cat := range spawnCategoryOrder(spawnFriendly) {
		if !spawnFriendly && cat.isFriendly() {
			continue // getFilteredSpawningCategories: skip a FRIENDLY category off the 400-tick friendly cycle
		}
		if !spawnEnemy && cat.isPersistent() {
			continue // skip a PERSISTENT category when SPAWN_MONSTERS is off (spawnEnemy false)
		}
		if t.submitSpawnScanFor(cat, cols, spawnableChunkCount, refY) {
			return // one scan in flight this cycle (the single-in-flight gate is set)
		}
	}
}

// spawnCategoryOrder returns the category iteration order for one naturalSpawn cycle. Vanilla
// getFilteredSpawningCategories iterates SPAWNING_CATEGORIES in a single tickChunks that runs EVERY
// eligible category to completion, so ORDER is observationally irrelevant there. Sulfur's OPT-03 async
// spawner instead admits ONE scan per cycle (the per-region single-in-flight gate spawnScanPending), so
// a naive MONSTER-first iteration would let MONSTER -- eligible EVERY tick -- always win the single slot
// and starve the FRIENDLY categories out of their once-per-400-tick window entirely. To preserve the
// vanilla OBSERVABLE outcome (passives/water/ambient DO spawn on their friendly cycle; hostiles spawn
// ~every tick) under the single-slot model, on a FRIENDLY cycle (gametime%400==0) the friendly categories
// take the slot first (MONSTER yields its 1-in-20 friendly-cycle opportunity -- it still submits on the
// other ~19 spawnInterval cycles per friendly window), and on a NON-friendly cycle only MONSTER is
// eligible so order is moot. The friendly sub-order follows the vanilla ordinal (CREATURE, AMBIENT,
// AXOLOTLS, UNDERGROUND_WATER_CREATURE, WATER_CREATURE, WATER_AMBIENT). This is the minimal, documented
// deviation the async single-slot substrate forces; the per-category caps + spawn-rules gates are all 1:1.
func spawnCategoryOrder(spawnFriendly bool) []mobCategory {
	if !spawnFriendly {
		return spawningCategories // MONSTER-first ordinal; only MONSTER passes the friendly gate anyway
	}
	return []mobCategory{
		categoryCreature,
		categoryAmbient,
		categoryAxolotls,
		categoryUndergroundWaterCreature,
		categoryWaterCreature,
		categoryWaterAmbient,
		categoryMonster,
	}
}

// spawnLiveCount returns the GLOBAL live count for a category for naturalSpawn's pre-submit cap gate
// (Phase 35-02 generalization of the Phase-27 N=2 CREATURE gate). Inside the parallel fan-out it reads
// the coordinator's quiescent pre-fan-out snapshot (race-free); from a direct single-threaded call (a
// test, no region ticking) it computes the cross-region count live (also race-free). Either way the
// count is GLOBAL and matches the apply-time countByCategoryAcrossRegions re-check (async.go).
func (t *TickLoop) spawnLiveCount(cat mobCategory) int {
	if _, inFanOut := t.resolveRegion(); inFanOut {
		if cat == categoryMonster {
			return t.spawnLiveMonsterSnapshot // race-free snapshot the coordinator took while quiescent
		}
		return t.spawnLiveCreatureSnapshot
	}
	return t.countByCategoryAcrossRegions()[cat] // direct call: quiescent, live is safe
}

// submitSpawnScanFor runs ONE category's pre-submit gate + the off-tick candidate-scan submit, the
// category-parameterized core extracted from the old pig-only naturalSpawn tail (Phase 35-02). It
// returns true iff it actually SUBMITTED a scan (and therefore set the single-in-flight gate), so the
// caller can stop after the first submitting pass. The per-category cap scales with the spawnable-chunk
// count exactly as vanilla derives it (state.spawnableChunkCount * maxInstancesPerChunk /
// MAGIC_NUMBER); the live count is a snapshot, so applyTo RE-CHECKS the right per-category cap before
// the add (the load-bearing anti-flood, Pitfall 3). The roll/snapshot/submit machinery is wholly
// category-agnostic — only the cap, the live count, and the category carried on the result differ.
func (t *TickLoop) submitSpawnScanFor(cat mobCategory, cols []level.ChunkPos, spawnableChunkCount, refY int) bool {
	cap := categorySpawnCap(cat, spawnableChunkCount) // maxInstancesPerChunk * count / MAGIC_NUMBER (vanilla)
	if t.spawnLiveCount(cat) >= cap {
		return false // AT or OVER cap: the anti-flood gate (Pitfall 3) — submit no scan
	}

	// Roll the random in-column candidate positions ON the owner (trivial rand). Vanilla picks a
	// random chunk + getRandomPosWithin; the random pick spreads spawns across the loaded area so
	// they don't pile on one block. The EXPENSIVE part (is this column standable?) goes off-tick.
	//
	// T-34-11 (COMPLETION): draw the column + in-chunk offsets from the SUBMITTING region's seeded
	// levelRandom (Level.random analogue), NOT the process-global math/rand/v2 rand.IntN. The mob-TYPE
	// pick was already moved to levelRandom (async.go pickNaturalSpawnMob); the COLUMN/offset pick was
	// left on the unseeded global stream, which (a) is shared across every test in the package, so a
	// prior test's draws desync this one's sequence (the intermittent 0-spawn flake under the full
	// suite / -count), and (b) is not per-region deterministic. This runs on the owner inside the
	// fan-out (cur() == the submitting region — TICK-05), so its levelRandom is advanced only on this
	// region's goroutine (race-clean).
	lr := t.cur().levelRandom
	picks := make([]spawnCandidatePick, 0, spawnAttemptsPerCycle)
	for i := 0; i < spawnAttemptsPerCycle; i++ {
		col := cols[lr.NextIntN(int32(len(cols)))]
		picks = append(picks, spawnCandidatePick{
			x: int(col[0])*16 + int(lr.NextIntN(16)),
			z: int(col[1])*16 + int(lr.NextIntN(16)),
		})
	}

	// COPY the candidate columns' solidity into an immutable snapshot on the owner (snapshotRegion
	// discipline), then SUBMIT the standable-Y scan over the SNAPSHOT to spawnPool. The worker reads
	// only the snapshot + the carried spawnableChunkCount (immutable ints) — no live world/store
	// (Pitfall 3) — and sends the standable candidates back on asyncIn2.
	snap := t.snapshotSpawnColumns(picks, refY)
	// Phase-27 STEP-3 (N=2): capture the SUBMITTING region ON the owner (here, inside the fan-out,
	// cur() resolves to this region). The worker closure must NOT call cur() (it runs on a pool
	// goroutine where cur() would fall back to globalRegion / panic under strictRegion), so the source
	// region is captured as a value and carried in the result so applyTo clears THIS region's in-flight
	// gate. cur() (not only()) so an unwrapped coordinator-side submit is caught by the strict guard.
	submitRegion := t.cur()
	submitted := submitOrDrop(t.spawnPool, func() {
		candidates := make([]spawnCandidate, 0, len(picks))
		for _, p := range picks {
			if y, ok := findStandableYIn(snap, p.x, p.z); ok {
				candidates = append(candidates, spawnCandidate{x: p.x, y: y, z: p.z})
			}
		}
		// Rejoin on the owner: applyTo re-checks the cap + mobNear and adds at most one mob. Always
		// send (even an empty candidate set) so applyTo clears the in-flight gate — otherwise a
		// cycle that found nothing would wedge spawnScanPending forever. Carry the category so applyTo
		// re-checks the RIGHT cap + picks from the RIGHT species list (Phase 35-02).
		t.asyncIn2 <- spawnCandidatesReady{candidates: candidates, spawnableChunkCount: spawnableChunkCount, region: submitRegion, category: cat}
	})
	if submitted {
		t.cur().spawnScanPending = true // one scan in flight; cleared by spawnCandidatesReady.applyTo
	}
	// On overload (submitted == false) the gate stays clear and the cycle is a no-op — it retries
	// next spawnInterval (Pitfall 4). No spawn, no block.
	return submitted
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

// mobNear reports whether any entity sits within rangeBlocks horizontal distance of (x,z). Used
// by the spawner's packing guard to avoid stacking mobs on one block. It uses the store's
// per-column broad phase (near) so the scan is bounded to the candidate's column neighborhood,
// not the whole world. Tick-owned.
func (t *TickLoop) mobNear(x, z, rangeBlocks float64) bool {
	if t.cur().entities == nil {
		return false
	}
	r2 := rangeBlocks * rangeBlocks
	for _, e := range t.cur().entities.near(x, z, 1) {
		dx := e.x - x
		dz := e.z - z
		if dx*dx+dz*dz <= r2 {
			return true
		}
	}
	return false
}

// (compile guard) — keep the pk import wired for the world block reads naturalSpawn relies on
// via blockSolidAt -> world.GetBlock(pk.Position, …); a future refactor that inlines a read
// here uses pk.Position directly.
var _ = pk.Position{}
