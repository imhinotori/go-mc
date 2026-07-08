package server

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// natural_spawner.go - C-6 (divergence audit): the 1:1 port of the placement-rules CORE of
// net.minecraft.world.level.NaturalSpawner.spawnCategoryForPosition - the pieces the earlier
// faithful-but-minimal spawner.go DEFERRED (spawner.go:32,369): the per-position
// MIN_SPAWN_DISTANCE=24 (squared = 576) guard to players + spawn point, and the PACK-GROUP loop
// (the outer spawnedInGroup / getMaxSpawnClusterSize group loop and the inner packSize /
// nextInt(6)-nextInt(6) cluster-spread loop). PORTED (the STANDING MANDATE - idiomatic Go, never a
// GPL paste) from the unobfuscated 26.2 jar via
//   javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.NaturalSpawner
//   javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.entity.Mob
// (this session, C-6). The vanilla spawnCategoryForPosition bytecode this session translates:
//
//   int spawnedInGroup = 0;                              // var 11
//   for (int i = 0; i < 3; i++) {                        // var 12 - the outer GROUP loop
//     int x = pos.getX(), z = pos.getZ();               // vars 13/14
//     int packSize = Mth.ceil(random.nextFloat()*4.0F); // var 18 - 1 nextFloat DRAW
//     int spawnedInPack = 0;                            // var 19
//     for (int j = 0; j < packSize; j++) {             // var 20 - the inner PACK loop
//       x += random.nextInt(6) - random.nextInt(6);    // 2 nextInt(6) DRAWS
//       z += random.nextInt(6) - random.nextInt(6);    // 2 nextInt(6) DRAWS
//       double cx = x + 0.5, cz = z + 0.5;
//       Player player = level.getNearestPlayer(cx, y, cz, -1.0, false);
//       if (player == null) continue;
//       double d = player.distanceToSqr(cx, y, cz);
//       if (!isRightDistanceToPlayerAndSpawnPoint(level, chunk, mutable, d)) continue;
//       if (spawnerData == null) {                       // first mob: pick from biome list
//         spawnerData = getRandomSpawnMobAt(...);        // WeightedList.getRandom - weighted pick
//         if (spawnerData == null) break;
//         packSize = minCount + random.nextInt(1 + maxCount - minCount); // re-set from data
//       }
//       if (!isValidSpawnPostitionForType(...)) continue;
//       Mob mob = getMobForSpawn(level, type); if (mob == null) return;
//       mob.snapTo(cx, y, cz, random.nextFloat()*360.0F, 0.0F); // 1 nextFloat DRAW (yaw)
//       if (!isValidPositionForMob(level, mob, d)) continue;
//       ...finalizeSpawn(...); spawnedInGroup++; spawnedInPack++; level.addFreshEntity(mob);
//       if (spawnedInGroup >= mob.getMaxSpawnClusterSize()) return;  // GROUP cap (base 4)
//       if (mob.isMaxGroupSizeReached(spawnedInPack)) break;         // PACK cap (base false)
//     }
//   }
//
//   isRightDistanceToPlayerAndSpawnPoint(level, chunk, pos, d):
//     if (d <= 576.0) return false;                      // MIN_SPAWN_DISTANCE = 24; 24*24 = 576
//     RespawnData rd = level.getRespawnData();
//     if (rd.dimension() == level.dimension()
//         && rd.pos().closerToCenterThan(Vec3(x+0.5,y,z+0.5), 24.0)) return false;
//     ChunkPos cp = ChunkPos.containing(pos);
//     return cp.equals(chunk.getPos()) || level.canSpawnEntitiesInChunk(cp);
//
// PORTED (gap-reaudit #9): the WEIGHTED biome mob pick + the SpawnerData packSize re-set are now REAL
//   (biome_spawners.go), replacing the earlier uniform stub. On the first pack member that clears the
//   player/distance gate spawnPackAt calls pickBiomeSpawnMob (NaturalSpawner.getRandomSpawnMobAt ->
//   WeightedList.getRandom: biome MobSpawnSettings lookup + total-weight nextInt + cumulative walk) and
//   re-sets packSize = minCount + nextInt(1+maxCount-minCount) (packSizeFromSpawnerData), exactly at the
//   vanilla getOrCreateNextSpawnData point + draw order. The Mth.ceil(nextFloat*4) size drawn per group
//   is the vanilla FALLBACK, overwritten by the SpawnerData min/max once a pick lands (an empty biome
//   list breaks the pack loop with NO extra draw, the vanilla if (spawnerData == null) break). The pick
//   is FILTERED to the ported natural pool (biome_spawners.go V1 SUBSET note) - only implemented mob
//   types are drawn, so a biome type not yet ported neither spawns nor consumes a weight.
//   - isValidSpawnPostitionForType (canSpawnFarFromPlayer distance + checkSpawnRules +
//     isSpawnPositionOk) is collapsed into the ON_GROUND standable check the off-tick scan already
//     ran (spawner.go findStandableY) + the monster light-gate (isDarkEnoughToSpawn), so a candidate
//     handed here is already position-valid; the per-pack-member RE-scan of the spread position keeps
//     the ON_GROUND check faithful for the nextInt(6)-shifted members.
//
// SINGLE-OWNER (TICK-05): spawnPackAt runs inside spawnCandidatesReady.applyTo on the OWNER at the
// quiescent barrier (async.go), inside a withRegion scope so cur() resolves the owning region and
// its seeded levelRandom (Level.random analogue) is advanced only on that goroutine. Every RNG draw
// (packSize nextFloat, the 4 nextInt(6) spread draws, the yaw nextFloat) is on that levelRandom in
// the EXACT vanilla order, so the spawn stream is deterministic per region seed.

// minSpawnDistanceSq ports NaturalSpawner.isRightDistanceToPlayerAndSpawnPoint first guard:
// MIN_SPAWN_DISTANCE = 24 blocks, the check is (d <= 576.0) return false where 576.0 == 24*24
// (javap: ldc2_w 576.0d; dcmpg; ifgt). A candidate CLOSER than 24 blocks (squared distance <= 576)
// to the nearest player is rejected - the vanilla no-spawn bubble around a player.
// Cite net.minecraft.world.level.NaturalSpawner.isRightDistanceToPlayerAndSpawnPoint.
const minSpawnDistanceSq = 576.0

// spawnPointExclusionDist ports the 24.0 arg to BlockPos.closerToCenterThan in
// isRightDistanceToPlayerAndSpawnPoint: a candidate within 24 blocks of the world respawn point (in
// the same dimension) is rejected. Cite NaturalSpawner.isRightDistanceToPlayerAndSpawnPoint.
const spawnPointExclusionDist = 24.0

// spawnGroupAttempts ports the OUTER group loop bound in spawnCategoryForPosition (for i=0;i<3;i++ -
// javap iconst_3; if_icmpge): the natural spawner makes up to 3 group attempts at one candidate
// position, each drawing its own packSize. Cite NaturalSpawner.
const spawnGroupAttempts = 3

// packSpread ports the per-pack-member cluster spread bound: x += nextInt(6) - nextInt(6) (javap
// bipush 6; RandomSource.nextInt twice, subtracted). Each pack member is offset up to +/-5 blocks in
// x and z from the previous, so the pack clusters around the candidate. Cite NaturalSpawner.
const packSpread = 6

// nearestPlayerDistSq ports ServerLevel.getNearestPlayer(x,y,z,-1.0,false) + Player.distanceToSqr:
// the squared distance from (cx,cy,cz) to the nearest non-spectator, live player, and whether one
// exists. The -1.0 radius means UNBOUNDED (no range filter). ok=false when no player exists (vanilla
// if (player == null) continue). Tick-owned read (TICK-05). Cite EntityGetter.getNearestPlayer(DDDDZ)
// + Entity.distanceToSqr(DDD).
func (t *TickLoop) nearestPlayerDistSq(cx, cy, cz float64) (d float64, ok bool) {
	best := 0.0
	for _, p := range t.players {
		if p == nil || p.dead || p.gameMode == gameModeSpectator {
			continue // getNearestPlayer NO_SPECTATORS + not-dead predicate
		}
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		d2 := dx*dx + dy*dy + dz*dz
		if !ok || d2 < best {
			best = d2
			ok = true
		}
	}
	return best, ok
}

// isRightDistanceToPlayerAndSpawnPoint ports NaturalSpawner.isRightDistanceToPlayerAndSpawnPoint 1:1.
// d is the squared distance to the nearest player. Returns false - REJECT the position - when the
// candidate is within MIN_SPAWN_DISTANCE (24 blocks) of a player OR within 24 blocks of the world
// spawn point (same dimension). The final chunk-eligibility branch (cp.equals(chunk.getPos()) ||
// canSpawnEntitiesInChunk(cp)) is TRUE for v1: the candidate columns the off-tick scan handed here
// are exactly the loaded spawnable columns (spawnableColumns), so the nextInt(6)-shifted member is in
// or adjacent to a loaded, spawn-enabled column - the deferred canSpawnEntitiesInChunk read only
// gates unloaded/spawn-disabled chunks, which spawnableColumns already excludes. Cite NaturalSpawner.
func (t *TickLoop) isRightDistanceToPlayerAndSpawnPoint(cx, cy, cz, d float64) bool {
	// (d <= 576.0) return false - the MIN_SPAWN_DISTANCE=24 (24*24=576) no-spawn bubble.
	if d <= minSpawnDistanceSq {
		return false
	}
	// respawn.dimension()==dimension() && respawn.pos().closerToCenterThan(pos,24.0) -> reject. v1 has
	// one overworld dimension so the dimension guard is always true when a spawn point is set;
	// closerToCenterThan compares the candidate CENTER to the spawn point within 24 blocks.
	if t.hasSpawnPoint {
		sx := t.spawnPoint.X - cx
		sy := t.spawnPoint.Y - cy
		sz := t.spawnPoint.Z - cz
		if sx*sx+sy*sy+sz*sz < spawnPointExclusionDist*spawnPointExclusionDist {
			return false
		}
	}
	// The chunk-eligibility branch collapses to TRUE for v1 (candidate columns already loaded +
	// spawn-enabled via spawnableColumns) - the deferred canSpawnEntitiesInChunk read slots in here.
	return true
}

// spawnPackAt ports the spawnCategoryForPosition GROUP + PACK loops 1:1, placing a cluster of mobs
// around the candidate block position (cx0,cy,cz0 are the ON_GROUND-validated candidate the off-tick
// scan found). It runs on the OWNER inside a withRegion scope (async.go applyTo), so cur().levelRandom
// is this.random and every draw is in the exact vanilla order. Returns the number of mobs placed.
//
// The RNG draw order (the crucial spawning-determinism invariant - C-6, now with the biome-weighted
// pick wired in at its EXACT vanilla position, gap-reaudit #9):
//
//	outer group i in 0..2:
//	  packSize = Mth.ceil(nextFloat() * 4)          - the per-group FALLBACK size draw
//	  spawnerData = null                            - reset per group (no draw)
//	  inner pack j in 0..packSize-1:
//	    x += nextInt(6) - nextInt(6)                - the cluster spread (2 draws)
//	    z += nextInt(6) - nextInt(6)                - the cluster spread (2 draws)
//	    (nearest-player + MIN_SPAWN_DISTANCE gate - no draw)
//	    if spawnerData == null:                     - the FIRST cleared member fetches the pick
//	      spawnerData = getRandomSpawnMobAt(...)     - WeightedList.getRandom: nextInt(totalWeight)
//	                                                   (or NO draw + break the pack loop on empty list)
//	      packSize = minCount + nextInt(1+max-min)   - the SpawnerData packSize RE-SET draw
//	    (ON_GROUND re-check of the shifted position - no draw)
//	    yaw = nextFloat() * 360                      - the yaw draw (only when the mob is placed)
//
// The mob TYPE pick is now getRandomSpawnMobAt (pickBiomeSpawnMob, biome_spawners.go) drawn ON THE FIRST
// cleared pack member BEFORE the packSize re-set - the exact vanilla draw position + order, not the old
// per-placed-member uniform stub. cat carries the category the caller already re-checked the cap for;
// the group/pack caps (getMaxSpawnClusterSize=4, isMaxGroupSizeReached=false) bound the cluster.
func (t *TickLoop) spawnPackAt(cx0, cy, cz0 int, cat mobCategory) int {
	r := t.cur()
	if r == nil || r.levelRandom == nil {
		return 0
	}
	spawnedInGroup := 0
	for i := 0; i < spawnGroupAttempts; i++ {
		x := cx0
		z := cz0
		// packSize = Mth.ceil(random.nextFloat() * 4.0F) - the first per-group draw (the FALLBACK size,
		// RE-SET below from the picked SpawnerData min/max once the biome list yields one).
		packSize := mthCeil(float64(r.levelRandom.NextFloat()) * 4.0)
		spawnedInPack := 0
		// spawnerData (var 16) is the biome MobSpawnSettings pick for THIS group, fetched ONCE on the
		// first pack member that clears the player/distance gate and REUSED for the rest of the group
		// (vanilla resets it to null at the top of each outer group loop iteration, bytecode aconst_null
		// astore 16). haveData tracks "already picked" (the ifnonnull 325 guard). mobName is the resolved
		// declared mob for the pick.
		var spawnerData biomeSpawnerData
		var mobName string
		haveData := false
		for j := 0; j < packSize; j++ {
			// x += nextInt(6) - nextInt(6); z += nextInt(6) - nextInt(6) - the cluster spread (4 draws).
			x += int(r.levelRandom.NextIntN(packSpread)) - int(r.levelRandom.NextIntN(packSpread))
			z += int(r.levelRandom.NextIntN(packSpread)) - int(r.levelRandom.NextIntN(packSpread))

			cxF := float64(x) + 0.5
			czF := float64(z) + 0.5
			cyF := float64(cy)

			// getNearestPlayer(cx, y, cz, -1.0, false); if (player == null) continue;
			d, ok := t.nearestPlayerDistSq(cxF, cyF, czF)
			if !ok {
				continue
			}
			// isRightDistanceToPlayerAndSpawnPoint - the MIN_SPAWN_DISTANCE squared + spawn-point guard.
			if !t.isRightDistanceToPlayerAndSpawnPoint(cxF, cyF, czF, d) {
				continue
			}
			// getRandomSpawnMobAt + packSize RE-SET (the biome-weighted pick, gap-reaudit #9). Vanilla:
			//   if (spawnerData == null) {                                  // ifnonnull 325
			//     spawnerData = getRandomSpawnMobAt(...);                    // WeightedList.getRandom draw
			//     if (spawnerData == null) break;                           // empty list -> next group
			//     packSize = minCount + nextInt(1 + maxCount - minCount);   // RE-SET from the picked data
			//   }
			// The pick is drawn HERE (after the player/distance gate clears, before the position check),
			// ONCE per group, on THIS region seeded levelRandom - the EXACT vanilla draw position + order.
			// pickBiomeSpawnMob draws one nextInt(totalWeight) on a non-empty biome list, or NOTHING and
			// ok=false on an empty one (mirroring getRandom Optional.empty()); an empty list breaks the
			// pack loop (the vanilla if (spawnerData == null) break), moving to the next group attempt.
			if !haveData {
				sd, name, pickedOK := t.pickBiomeSpawnMob(x, cy, z, cat)
				if !pickedOK {
					break // no biome spawn entry for this category here: break the pack loop (next group)
				}
				spawnerData = sd
				mobName = name
				haveData = true
				// packSize = minCount + nextInt(1 + maxCount - minCount) - the SpawnerData min/max re-set.
				packSize = t.packSizeFromSpawnerData(spawnerData)
			}
			// isValidSpawnPostitionForType collapse: re-run the ON_GROUND standable check on the
			// nextInt(6)-shifted position (spawner.go findStandableY logic) so a shifted pack member
			// still lands on a solid surface with clear feet/head. A shift onto a non-standable column
			// is skipped (continue), exactly as vanilla isValidSpawnPostitionForType rejects it.
			if !(t.blockSolidAt(x, cy-1, z) && !t.blockSolidAt(x, cy, z) && !t.blockSolidAt(x, cy+1, z)) {
				continue
			}
			// isValidSpawnPostitionForType -> SpawnPlacements.checkSpawnRules(type, level, NATURAL, pos,
			// level.random): the per-candidate spawn-rules gate. For a MONSTER-category type this dispatches
			// to Monster.checkMonsterSpawnRules -> isDarkEnoughToSpawn (the real light read + RNG draws:
			// nextInt(32) SKY sample, then conditionally nextInt(8) light-test). It runs HERE, at the exact
			// vanilla draw position (after the biome pick/packSize-reset, before the yaw draw), on THIS
			// region's seeded levelRandom (cur().levelRandom == r.levelRandom, the Level.random analogue). A
			// candidate that is too bright (a daylit surface cell) is REJECTED here, so the pack member is
			// skipped (continue) exactly as vanilla checkSpawnRules returns false. The CREATURE path draws
			// NO RNG in its spawn-rules gate (Animal.checkAnimalSpawnRules uses isBrightEnoughToSpawn, a pure
			// getRawBrightness read with no nextInt), so gating monsters here leaves the creature/pig spawn
			// RNG stream byte-identical (the pig oracle). CITE: net.minecraft.world.entity.SpawnPlacements
			// .checkSpawnRules; net.minecraft.world.entity.monster.Monster.checkMonsterSpawnRules.
			if cat == categoryMonster && !t.isDarkEnoughToSpawn(pk.Position{X: x, Y: cy, Z: z}) {
				continue // not dark enough at this candidate (e.g. daylit surface): reject, exactly as checkSpawnRules
			}
			// mob.snapTo(cx, y, cz, random.nextFloat() * 360.0F, 0.0F) - the yaw draw.
			yaw := r.levelRandom.NextFloat() * 360.0
			mob := t.spawnVanillaMob(mobName, cxF, cyF, czF)
			if mob == nil {
				return spawnedInGroup // getMobForSpawn null -> vanilla returns
			}
			mob.yaw = yaw
			mob.headYaw = yaw

			// isValidPositionForMob(level, mob, d): despawn-distance + checkSpawnRules + obstruction.
			// v1 placement validity was established by the ON_GROUND re-check above + the caller light
			// gate; removeWhenFarAway is base-true and the despawn-distance guard here mirrors
			// isValidPositionForMob (d > despawn*despawn && removeWhenFarAway(d)) return false. If the
			// freshly-placed position fails, discard the mob (undo the add) rather than leave it.
			despawn := float64(cat.despawnDistance())
			if d > despawn*despawn && removeWhenFarAway(mob, d) {
				mob.dead = true
				r.entities.remove(mob.id) // discard the over-despawn-distance placement
				continue
			}

			spawnedInGroup++
			spawnedInPack++
			// (spawnedInGroup >= mob.getMaxSpawnClusterSize()) return - GROUP cap (base 4).
			if spawnedInGroup >= maxSpawnClusterSize {
				return spawnedInGroup
			}
			// (mob.isMaxGroupSizeReached(spawnedInPack)) break - PACK cap (base false).
			if isMaxGroupSizeReached(spawnedInPack) {
				break
			}
		}
	}
	return spawnedInGroup
}
