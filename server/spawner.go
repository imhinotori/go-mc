package server

import (
	"math/rand/v2"

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
	if t.cur().entities == nil {
		return counts
	}
	for _, e := range t.cur().entities.byID {
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
		for _, e := range r.entities.byID {
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
	refY       int            // the scan center Y (player feet) carried into the worker
	minY, maxY int            // the snapshot's inclusive Y-window (refY ± spawnScanYRange, padded)
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
	cols := t.spawnableColumns()
	if len(cols) == 0 {
		return // no loaded columns near a player: nothing to populate
	}

	// The CREATURE cap scales with the spawnable-chunk count, exactly as vanilla derives it from
	// state.spawnableChunkCount * maxInstancesPerChunk (getFilteredSpawningCategories). Gate it on
	// the owner BEFORE submitting (don't burn an off-tick scan when already at cap); the count is a
	// snapshot, so applyTo RE-CHECKS it before the add (the load-bearing anti-flood, Pitfall 3).
	//
	// Phase-27 N=2: the cap spans ALL players/regions, so the pre-submit count must be GLOBAL too (a
	// per-region countByCategory() under-counts and lets each region submit even at the global cap).
	// But naturalSpawn runs INSIDE the parallel fan-out, where ranging another region's live store
	// would RACE its concurrent mutations. So: when a region is registered (the production fan-out),
	// read the coordinator's quiescent pre-fan-out snapshot (spawnLiveCreatureSnapshot — race-free);
	// when NONE is registered (a direct single-threaded test call), no region is ticking, so a live
	// cross-region count is race-free — compute it directly. Either way the count is GLOBAL and matches
	// the apply-time countByCategoryAcrossRegions re-check (async.go).
	spawnableChunkCount := len(cols)
	cap := categoryCreature.maxInstancesPerChunk() * spawnableChunkCount
	var live int
	if _, inFanOut := t.resolveRegion(); inFanOut {
		live = t.spawnLiveCreatureSnapshot // race-free snapshot the coordinator took while quiescent
	} else {
		live = t.countByCategoryAcrossRegions()[categoryCreature] // direct call: quiescent, live is safe
	}
	if live >= cap {
		return // AT or OVER cap: the anti-flood gate (Pitfall 3) — submit no scan
	}

	// Reference Y for the column scan: a player's feet (the surface a near-player spawn sits on).
	refY := t.spawnRefY()

	// Roll the random in-column candidate positions ON the owner (trivial rand). Vanilla picks a
	// random chunk + getRandomPosWithin; the random pick spreads spawns across the loaded area so
	// they don't pile on one block. The EXPENSIVE part (is this column standable?) goes off-tick.
	picks := make([]spawnCandidatePick, 0, spawnAttemptsPerCycle)
	for i := 0; i < spawnAttemptsPerCycle; i++ {
		col := cols[rand.IntN(len(cols))]
		picks = append(picks, spawnCandidatePick{
			x: int(col[0])*16 + rand.IntN(16),
			z: int(col[1])*16 + rand.IntN(16),
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
		// Rejoin on the owner: applyTo re-checks the cap + mobNear and adds at most one Pig. Always
		// send (even an empty candidate set) so applyTo clears the in-flight gate — otherwise a
		// cycle that found nothing would wedge spawnScanPending forever.
		t.asyncIn2 <- spawnCandidatesReady{candidates: candidates, spawnableChunkCount: spawnableChunkCount, region: submitRegion}
	})
	if submitted {
		t.cur().spawnScanPending = true // one scan in flight; cleared by spawnCandidatesReady.applyTo
	}
	// On overload (submitted == false) the gate stays clear and the cycle is a no-op — it retries
	// next spawnInterval (Pitfall 4). No spawn, no block.
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
