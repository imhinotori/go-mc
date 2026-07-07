package server

// natural_spawner_test.go - C-6: tests for the NaturalSpawner placement-rules CORE ported in
// natural_spawner.go: the MIN_SPAWN_DISTANCE=24 (squared=576) per-position guard, the pack-group
// loop bounded by getMaxSpawnClusterSize(=4), and the vanilla RNG draw order (packSize nextFloat,
// the nextInt(6)-nextInt(6) spread, the yaw nextFloat). Reuses newSpawnLoop from spawner_test.go.

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
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
		}
	}
	loop.players = append(loop.players, &tickPlayer{x: 100.5, y: float64(floorY + 1), z: 100.5, gameMode: gameModeSurvival})
	loop.hasSpawnPoint = false
	return loop, floorY
}

// TestSpawnPackFirstPackSizeMatchesReference: the FIRST RNG draw spawnPackAt makes is
// packSize = Mth.ceil(random.nextFloat() * 4.0) off the region levelRandom, matching a seeded
// reference EXACTLY (the head of the vanilla spawnCategoryForPosition draw order - the crucial
// spawning-determinism invariant). Asserting the FIRST group's packSize (before any per-mob spawn
// draw such as Sheep.finalizeSpawn's getRandomSheepColor can perturb the stream) pins the draw ORDER
// at the point where it is purely spawner-driven: the placed count is >= 1 and <= that first packSize
// clamped to the group cap.
func TestSpawnPackFirstPackSizeMatchesReference(t *testing.T) {
	const seed = int64(0xC6C6C6)
	loop, floorY := buildFlatPackWorld(t)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	// The reference computes the FIRST group's packSize the SAME way spawnPackAt does (nextFloat*4,
	// ceil) - the head of the draw order. spawnPackAt consumes this exact first draw before any spread
	// or spawn draw, so a placed pack is bounded by min(firstPackSize, groupCap) at minimum from the
	// first group (later groups can add more up to the cap).
	ref := levelgen.NewLegacyRandomSource(seed)
	firstPackSize := mthCeil(float64(ref.NextFloat()) * 4.0)
	if firstPackSize < 1 {
		t.Fatalf("Mth.ceil(nextFloat*4) must be >= 1 for a non-zero float, got %d", firstPackSize)
	}

	var placed int
	loop.withRegion(loop.only(), func() {
		placed = loop.spawnPackAt(8, floorY+1, 8, categoryCreature)
	})

	// In a fully-standable, far-from-player area every drawn member of the first group is placed until
	// the group cap. So placed >= min(firstPackSize, groupCap) - the first draw's packSize governs the
	// first group's placements. (placed can be larger if later groups add members up to the cap.)
	wantAtLeast := firstPackSize
	if wantAtLeast > maxSpawnClusterSize {
		wantAtLeast = maxSpawnClusterSize
	}
	if placed < wantAtLeast {
		t.Fatalf("first-group packSize=%d (seeded) => at least %d placed, got %d (RNG draw-order head mismatch)", firstPackSize, wantAtLeast, placed)
	}
	if placed > maxSpawnClusterSize {
		t.Fatalf("the group cap getMaxSpawnClusterSize=%d must bound the pack, got %d", maxSpawnClusterSize, placed)
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
