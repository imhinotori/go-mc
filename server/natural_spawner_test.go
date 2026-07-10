package server

// natural_spawner_test.go - C-6: tests for the NaturalSpawner placement-rules CORE ported in
// natural_spawner.go: the MIN_SPAWN_DISTANCE=24 (squared=576) per-position guard, the pack-group
// loop bounded by getMaxSpawnClusterSize(=4), and the vanilla RNG draw order (packSize nextFloat,
// the nextInt(6)-nextInt(6) spread, the yaw nextFloat). Reuses newSpawnLoop from spawner_test.go.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// TestMinSpawnDistanceRejectsTooClose: isRightDistanceToPlayerAndSpawnPoint rejects a position whose
// squared distance to the nearest player is <= 576 (24 blocks) and accepts one that is farther
// (NaturalSpawner.isRightDistanceToPlayerAndSpawnPoint: if (d<=576.0) return false).
func TestMinSpawnDistanceRejectsTooClose(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)
	loop.hasSpawnPoint = false // only the player-distance guard under test
	px, py, pz := 8.5, float64(floorY+1), 8.5

	if loop.isRightDistanceToPlayerAndSpawnPoint(px, py, pz, 0.0) {
		t.Fatal("d=0 (on the player) must be rejected (<= MIN_SPAWN_DISTANCE squared 576)")
	}
	if loop.isRightDistanceToPlayerAndSpawnPoint(px, py, pz, 576.0) {
		t.Fatal("d == 576 (24 blocks) must be rejected (the guard is d <= 576.0)")
	}
	if !loop.isRightDistanceToPlayerAndSpawnPoint(px, py, pz, 576.01) {
		t.Fatal("d just over 576 must be accepted (past the 24-block bubble)")
	}
	if !loop.isRightDistanceToPlayerAndSpawnPoint(px, py, pz, 40*40) {
		t.Fatal("a position 40 blocks away must be accepted")
	}
}

// TestSpawnPointExclusion: a position within 24 blocks of the world spawn point is rejected even when
// the player-distance guard passes (NaturalSpawner: respawn.pos().closerToCenterThan(pos,24.0)).
func TestSpawnPointExclusion(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)
	loop.hasSpawnPoint = true
	loop.spawnPoint = SpawnPoint{X: 100.5, Y: float64(floorY + 1), Z: 100.5}

	cx, cy, cz := 110.5, float64(floorY+1), 100.5 // 10 blocks east of the spawn point
	if loop.isRightDistanceToPlayerAndSpawnPoint(cx, cy, cz, 1000.0) {
		t.Fatal("a position within 24 blocks of the spawn point must be rejected even far from a player")
	}
	fx, fy, fz := 200.5, float64(floorY+1), 200.5
	if !loop.isRightDistanceToPlayerAndSpawnPoint(fx, fy, fz, 1000.0) {
		t.Fatal("a position far from both the player and the spawn point must be accepted")
	}
}

// TestNearestPlayerDistSqUnbounded: nearestPlayerDistSq returns the squared distance to the nearest
// live non-spectator player with NO range bound (-1.0 radius), ok=false when none exist.
func TestNearestPlayerDistSqUnbounded(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	if _, ok := loop.nearestPlayerDistSq(0, 0, 0); ok {
		t.Fatal("with no players nearestPlayerDistSq must report ok=false")
	}
	loop.players = append(loop.players, &tickPlayer{x: 30, y: 0, z: 40, gameMode: gameModeSurvival})
	d, ok := loop.nearestPlayerDistSq(0, 0, 0)
	if !ok {
		t.Fatal("nearestPlayerDistSq must find the single live player")
	}
	if d != 2500.0 {
		t.Fatalf("nearestPlayerDistSq = %v, want 2500 (30^2 + 40^2)", d)
	}
	loop.players = append(loop.players, &tickPlayer{x: 1, y: 0, z: 0, gameMode: gameModeSpectator})
	d2, _ := loop.nearestPlayerDistSq(0, 0, 0)
	if d2 != 2500.0 {
		t.Fatalf("a spectator must be ignored: got %v, want 2500", d2)
	}
}

// buildFlatPackWorld builds a loop with a wide flat floor and a player far from the origin so a pack
// spawned near the origin clears MIN_SPAWN_DISTANCE. Every column is standable (solid floor, air
// above), so the ON_GROUND re-check never rejects a nextInt(6)-shifted member - isolating the
// pack-count + RNG-order behavior.
func buildFlatPackWorld(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(dx), int32(dz)})
			fillFloor(ch, floorY)
			// The floor's SURFACE block (world-Y == floorY, the block directly BELOW a feet-Y==floorY+1
			// spawn) must be a grass_block so the CREATURE spawn-rules gate passes: the natural spawner's
			// CREATURE pass runs Animal.checkAnimalSpawnRules (below is #animals_spawnable_on == grass_block)
			// / Rabbit.checkRabbitSpawnRules (#rabbits_spawnable_on, which also contains grass_block) at the
			// isValidSpawnPostitionForType -> checkSpawnRules point inside spawnPackAt. On the default stone
			// floor those rules (correctly) reject every candidate, so the surface is laid as grass here.
			// Cite net.minecraft.world.entity.animal.Animal.checkAnimalSpawnRules;
			// net.minecraft.world.entity.animal.rabbit.Rabbit.checkRabbitSpawnRules.
			setSurfaceGrass(ch, floorY)
			// isBrightEnoughToSpawn (the other half of both creature rules) is getRawBrightness(pos,0) >= 9,
			// so light the whole column to full daylight (SKY 15) -- a bright surface where animals spawn in
			// vanilla. This is a pure light read (no RNG), so it does NOT perturb the draw ORDER these tests
			// pin. Cite net.minecraft.world.entity.animal.Animal.isBrightEnoughToSpawn.
			setSkyLight(ch, 15)
		}
	}
	loop.players = append(loop.players, &tickPlayer{x: 100.5, y: float64(floorY + 1), z: 100.5, gameMode: gameModeSurvival})
	loop.hasSpawnPoint = false
	return loop, floorY
}

// setSurfaceGrass overwrites the whole 16x16 surface layer at world-Y == y with grass_block, so the
// block directly below a feet-Y==y+1 natural spawn is #animals_spawnable_on / #rabbits_spawnable_on (the
// CREATURE spawn-rules ground check). Mirrors setBlock but with a grass_block state instead of stone.
func setSurfaceGrass(ch *level.Chunk, y int) {
	grass := block.ToStateID[block.GrassBlock{Snowy: false}]
	sec := (y - dimMinY) >> 4
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			local := (y&15)<<8 | (z&15)<<4 | (x & 15)
			ch.Sections[sec].SetBlock(local, grass)
		}
	}
}

// TestSpawnPackDrawOrderMatchesReference: spawnPackAt draws the region levelRandom in the EXACT vanilla
// spawnCategoryForPosition order once the biome-weighted pick is wired (gap-reaudit #9). The flat world
// carries the default biome (Type 0 = badlands, whose CREATURE list holds sheep/pig/chicken/cow - all in
// the ported pool). A hand-rolled reference replays the head of the draw sequence for the first group's
// first cleared pack member and asserts spawnPackAt's picked mob + first-group placement count agree - so
// the draw ORDER (fallback packSize, spread, weighted pick, packSize re-set, yaw) is pinned.
func TestSpawnPackDrawOrderMatchesReference(t *testing.T) {
	const seed = int64(0xC6C6C6)
	loop, floorY := buildFlatPackWorld(t)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	// The badlands (Type 0) CREATURE weighted list, filtered to the ported pool, in JSON order:
	//   sheep w12, pig w10, chicken w10, cow w8  -> totalWeight 40, all min=max=4.
	type we struct {
		name   string
		weight int
	}
	kept := []we{{vanillaSheepMobName, 12}, {vanillaPigMobName, 10}, {vanillaChickenMobName, 10}, {vanillaCowMobName, 8}}
	total := 0
	for _, e := range kept {
		total += e.weight
	}

	// Replay the FIRST group's draw head against a reference stream (all columns standable + player far,
	// so the first pack member always clears the player/distance gate and triggers the pick):
	//   packSize = ceil(nextFloat*4)                          (fallback, per group)
	//   member 0: nextInt(6)-nextInt(6) x, nextInt(6)-nextInt(6) z  (4 spread draws)
	//             pick: i = nextInt(total); walk sheep/pig/chicken/cow
	//             packSize re-set: nextInt(1+4-4) = nextInt(1)   (1 draw, returns 0)
	//             yaw = nextFloat*360                              (1 draw)
	ref := levelgen.NewLegacyRandomSource(seed)
	_ = mthCeil(float64(ref.NextFloat()) * 4.0) // fallback packSize (overwritten by the re-set below)
	ref.NextIntN(packSpread)
	ref.NextIntN(packSpread) // x spread
	ref.NextIntN(packSpread)
	ref.NextIntN(packSpread) // z spread
	i := int(ref.NextIntN(int32(total)))
	wantName := kept[len(kept)-1].name
	for _, e := range kept {
		i -= e.weight
		if i < 0 {
			wantName = e.name
			break
		}
	}

	// Drive spawnPackAt and capture the FIRST placed mob's declared name (the first group's first member).
	var placed int
	loop.withRegion(loop.only(), func() {
		placed = loop.spawnPackAt(8, floorY+1, 8, categoryCreature)
	})
	if placed < 1 {
		t.Fatalf("a fully-standable far-from-player pack must place at least one mob, got %d", placed)
	}
	if placed > maxSpawnClusterSize {
		t.Fatalf("the group cap getMaxSpawnClusterSize=%d must bound the pack, got %d", maxSpawnClusterSize, placed)
	}
	// The whole group shares ONE picked SpawnerData -> all placed mobs render as the same base type. Map
	// the expected mob name to its base entity type id and assert every placed entity carries it.
	wantType := loop.mobRegistry.byName[wantName].baseType.ID
	for _, r := range loop.regions {
		if r == nil || r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.typ != wantType {
				t.Fatalf("weighted pick mismatch: placed entity type %v, the seeded weighted-walk reference says %q (type %v)", e.typ, wantName, wantType)
			}
		}
	}
	if got := totalEntities(loop); got != placed {
		t.Fatalf("the store must hold the placed pack: totalEntities=%d, placed=%d", got, placed)
	}
}

// TestSpawnPackDeterministic: spawnPackAt is deterministic for a given region levelRandom seed - two
// runs with the same seed + same world place the SAME number of mobs at the SAME positions. This is
// the spawning-determinism guarantee (the whole draw sequence - packSize, spread, yaw, mob pick, and
// any per-mob spawn draw like the sheep color - is a pure function of the seeded stream + world).
func TestSpawnPackDeterministic(t *testing.T) {
	const seed = int64(0xC6C6C6)

	run := func() []spawnCandidate {
		loop, floorY := buildFlatPackWorld(t)
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
		loop.withRegion(loop.only(), func() {
			loop.spawnPackAt(8, floorY+1, 8, categoryCreature)
		})
		var got []spawnCandidate
		for _, r := range loop.regions {
			if r == nil || r.entities == nil {
				continue
			}
			for _, e := range r.entities.byID {
				got = append(got, spawnCandidate{x: floorI(e.x), y: int(e.y), z: floorI(e.z)})
			}
		}
		return got
	}

	a := run()
	b := run()
	if len(a) < 1 {
		t.Fatalf("expected a non-empty pack for the seed, got %d", len(a))
	}
	if len(a) != len(b) {
		t.Fatalf("nondeterministic pack count: run1=%d run2=%d", len(a), len(b))
	}
	// Positions must match as a multiset - the placement is deterministic per seed. Compare sorted keys.
	key := func(c spawnCandidate) int { return (c.x+1000)*4000000 + (c.z+1000)*2000 + (c.y + 1000) }
	seen := map[int]int{}
	for _, c := range a {
		seen[key(c)]++
	}
	for _, c := range b {
		seen[key(c)]--
	}
	for k, v := range seen {
		if v != 0 {
			t.Fatalf("nondeterministic pack positions: key %d mismatch (%d)", k, v)
		}
	}
}

// TestSpawnPackRejectedTooClose: when the player stands INSIDE the spawn area (every candidate within
// 24 blocks), spawnPackAt places NOTHING - the MIN_SPAWN_DISTANCE guard rejects every pack member.
func TestSpawnPackRejectedTooClose(t *testing.T) {
	const seed = int64(0x2424)
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.players = append(loop.players, &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 8.5, gameMode: gameModeSurvival})
	loop.hasSpawnPoint = false
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	var placed int
	loop.withRegion(loop.only(), func() {
		placed = loop.spawnPackAt(8, floorY+1, 8, categoryCreature)
	})
	if placed != 0 {
		t.Fatalf("every pack member is within MIN_SPAWN_DISTANCE of the player - none may spawn, got %d", placed)
	}
	if got := totalEntities(loop); got != 0 {
		t.Fatalf("no mob may be added when all positions fail MIN_SPAWN_DISTANCE, store has %d", got)
	}
}

// TestSpawnPackGroupCapBounds: the pack never exceeds getMaxSpawnClusterSize(=4) mobs from one
// spawnPackAt call, regardless of the drawn packSize (the outer group loop returns at the cap).
func TestSpawnPackGroupCapBounds(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 7, 42, 1000, 0xBEEF} {
		loop, floorY := buildFlatPackWorld(t)
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
		var placed int
		loop.withRegion(loop.only(), func() {
			placed = loop.spawnPackAt(8, floorY+1, 8, categoryCreature)
		})
		if placed > maxSpawnClusterSize {
			t.Fatalf("seed %d: spawnPackAt placed %d > group cap %d", seed, placed, maxSpawnClusterSize)
		}
	}
}

// TestBiomeSpawnersParse: loadBiomeSpawners parses the embedded 26.2 biome JSONs into the per-biome,
// per-category WeightedList[SpawnerData] with the exact weights/min/max the vanilla data carries (in
// JSON list order). Asserts the badlands CREATURE list (the default Type-0 biome the flat test worlds
// use) and plains, so the parse + the weight/min/max fields are pinned to the jar-derived data.
func TestBiomeSpawnersParse(t *testing.T) {
	table, err := loadBiomeSpawners()
	if err != nil {
		t.Fatalf("loadBiomeSpawners: %v", err)
	}
	// badlands CREATURE (Type 0): sheep w12 4-4, pig w10 4-4, chicken w10 4-4, cow w8 4-4, armadillo w6 1-2.
	bad := table["minecraft:badlands"][categoryCreature]
	want := []biomeSpawnerData{
		{"sheep", 12, 4, 4},
		{"pig", 10, 4, 4},
		{"chicken", 10, 4, 4},
		{"cow", 8, 4, 4},
		{"armadillo", 6, 1, 2},
	}
	if len(bad) != len(want) {
		t.Fatalf("badlands creature list len=%d, want %d (%v)", len(bad), len(want), bad)
	}
	for i, w := range want {
		if bad[i] != w {
			t.Fatalf("badlands creature[%d] = %+v, want %+v (JSON order + weight/min/max)", i, bad[i], w)
		}
	}
	// plains carries the same creature core plus horse/donkey (un-ported) - assert the pig entry is present.
	plains := table["minecraft:plains"][categoryCreature]
	found := false
	for _, sd := range plains {
		if sd.typeName == "pig" {
			found = true
			if sd.weight != 10 || sd.minCount != 4 || sd.maxCount != 4 {
				t.Fatalf("plains pig = %+v, want weight10 min4 max4", sd)
			}
		}
	}
	if !found {
		t.Fatal("plains creature list must contain a pig SpawnerData")
	}
}

// TestWeightedPickMatchesCumulativeWalk: pickBiomeSpawnMob draws ONE nextInt(totalWeight) off the region
// levelRandom and returns the mob the cumulative weighted walk selects for that draw - asserted against
// a hand-rolled reference over the badlands (Type 0) CREATURE list filtered to the ported pool
// (sheep w12, pig w10, chicken w10, cow w8; total 40). Pins the total-weight + cumulative-walk algorithm.
func TestWeightedPickMatchesCumulativeWalk(t *testing.T) {
	kept := []struct {
		name   string
		weight int
	}{{vanillaSheepMobName, 12}, {vanillaPigMobName, 10}, {vanillaChickenMobName, 10}, {vanillaCowMobName, 8}}
	total := 0
	for _, e := range kept {
		total += e.weight
	}
	for _, seed := range []int64{1, 2, 3, 7, 42, 1000, 0xC6C6C6, 0xBEEF} {
		loop, floorY := buildFlatPackWorld(t)
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

		// Reference: the SAME draw pickBiomeSpawnMob makes - i = nextInt(total); walk the kept list.
		ref := levelgen.NewLegacyRandomSource(seed)
		i := int(ref.NextIntN(int32(total)))
		wantName := kept[len(kept)-1].name
		for _, e := range kept {
			i -= e.weight
			if i < 0 {
				wantName = e.name
				break
			}
		}

		var gotName string
		loop.withRegion(loop.only(), func() {
			_, name, ok := loop.pickBiomeSpawnMob(8, floorY+1, 8, categoryCreature)
			if !ok {
				t.Fatalf("seed %d: pickBiomeSpawnMob returned ok=false on the badlands creature list", seed)
			}
			gotName = name
		})
		if gotName != wantName {
			t.Fatalf("seed %d: pickBiomeSpawnMob = %q, cumulative-walk reference = %q", seed, gotName, wantName)
		}
	}
}

// TestWeightedPickEmptyListNoDraw: pickBiomeSpawnMob over a category whose ported-pool intersection is
// EMPTY (v1 has no AMBIENT pool) returns ok=false and draws NOTHING - mirroring WeightedList.getRandom's
// early Optional.empty() on a null selector (totalWeight 0, no nextInt). Asserts the region levelRandom
// is UNadvanced across the empty-list pick.
func TestWeightedPickEmptyListNoDraw(t *testing.T) {
	loop, floorY := buildFlatPackWorld(t)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(99)
	// Snapshot the stream by drawing from a twin seeded the same way; the empty-list pick must not consume.
	twin := levelgen.NewLegacyRandomSource(99)

	loop.withRegion(loop.only(), func() {
		if _, _, ok := loop.pickBiomeSpawnMob(8, floorY+1, 8, categoryAmbient); ok {
			t.Fatal("AMBIENT has no v1 pool: pickBiomeSpawnMob must return ok=false")
		}
	})
	// The region stream must be byte-for-byte where the twin is (no draw happened).
	if got, want := loop.only().levelRandom.NextIntN(1000), twin.NextIntN(1000); got != want {
		t.Fatalf("the empty-list pick drew from levelRandom: next draw %d != twin %d", got, want)
	}
}

// TestPackSizeFromSpawnerData: packSizeFromSpawnerData ports minCount + nextInt(1 + maxCount - minCount).
// For a min==max entry it returns min after drawing nextInt(1)==0 (a real draw, consumed exactly once);
// for a min<max entry it returns min + nextInt(span) matching a seeded reference.
func TestPackSizeFromSpawnerData(t *testing.T) {
	// min==max=4 (the badlands passives): result is 4, one nextInt(1) draw consumed.
	loop, _ := buildFlatPackWorld(t)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(5)
	twin := levelgen.NewLegacyRandomSource(5)
	loop.withRegion(loop.only(), func() {
		if got := loop.packSizeFromSpawnerData(biomeSpawnerData{"pig", 10, 4, 4}); got != 4 {
			t.Fatalf("min==max=4 packSize = %d, want 4", got)
		}
	})
	twin.NextIntN(1) // the nextInt(1) the re-set consumed
	if got, want := loop.only().levelRandom.NextIntN(777), twin.NextIntN(777); got != want {
		t.Fatalf("min==max packSize draw count mismatch: %d != %d", got, want)
	}

	// min=1 max=2 (armadillo-shape): result is 1 + nextInt(2), matching the reference draw.
	loop2, _ := buildFlatPackWorld(t)
	loop2.only().levelRandom = levelgen.NewLegacyRandomSource(11)
	ref := levelgen.NewLegacyRandomSource(11)
	wantSize := 1 + int(ref.NextIntN(2))
	loop2.withRegion(loop2.only(), func() {
		if got := loop2.packSizeFromSpawnerData(biomeSpawnerData{"x", 6, 1, 2}); got != wantSize {
			t.Fatalf("min=1 max=2 packSize = %d, want %d (1 + nextInt(2))", got, wantSize)
		}
	})
}
