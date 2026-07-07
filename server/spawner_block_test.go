package server

// spawner_block_test.go - SPAWNER-01 tests for the BaseSpawner monster-spawner block-entity port
// (spawner_block.go). Asserts the four load-bearing behaviors of BaseSpawner.serverTick:
//   1. a player IN range -> countdown, then the spawnCount(4) burst on delay-0 (seeded RNG),
//   2. no player in range -> NO countdown (the isNearPlayer gate),
//   3. the maxNearbyEntities(6) cap blocks spawns when too many same-type mobs are already nearby,
//   4. the delay reroll lands in [minSpawnDelay(200), maxSpawnDelay(800)).
//
// The harness reuses newSpawnLoop (a floored world + a player near column 0) from spawner_test.go and
// places the spawner high in the air so every drawn spawn position (spawnerY + nextInt(3) - 1) lands
// in clear air above the floor. The level RNG is seeded to a fixed value so the burst is deterministic.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// seedSpawnerLoop returns a newSpawnLoop with a deterministic level RNG (fixed seed) on region 0 so a
// spawner burst is reproducible, plus the spawner world position placed HIGH in the air near the
// player. The spawner block-entity is configured to spawn a pig (a boot-loaded declaration).
func seedSpawnerLoop(t *testing.T, seed int64) (*TickLoop, pk.Position, *spawnerBE) {
	t.Helper()
	loop, _, floorY := newSpawnLoop(t)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
	pos := pk.Position{X: 8, Y: floorY + 5, Z: 8}
	be := newSpawnerBE(vanillaPigMobName)
	return loop, pos, be
}

// TestSpawnerBlockCountdownAndBurst: with a player IN range and spawnDelay wound to 0, one serverTick
// runs the full spawnCount(4) burst - it adds up to spawnCount mobs (all positions land in air) and
// rerolls the delay into [minSpawnDelay, maxSpawnDelay).
func TestSpawnerBlockCountdownAndBurst(t *testing.T) {
	loop, pos, be := seedSpawnerLoop(t, 42)
	be.spawnDelay = 0

	before := totalEntities(loop)
	loop.spawnerServerTick(pos, be)
	added := totalEntities(loop) - before

	if added <= 0 {
		t.Fatalf("a delay-0 spawner with a player in range must spawn at least one mob, added %d", added)
	}
	if added > be.spawnCount {
		t.Fatalf("the burst must add at most spawnCount(%d) mobs, added %d", be.spawnCount, added)
	}
	if be.spawnDelay < be.minSpawnDelay || be.spawnDelay >= be.maxSpawnDelay {
		t.Fatalf("post-burst spawnDelay must be in [%d,%d), got %d", be.minSpawnDelay, be.maxSpawnDelay, be.spawnDelay)
	}
	if findEntityOfType(loop, entity.Pig) == nil {
		t.Fatalf("the burst must spawn the configured mob (pig)")
	}
}

// TestSpawnerBlockCountsDownWithPlayerInRange: a positive spawnDelay with a player in range decrements
// by exactly one per tick and spawns nothing while counting down.
func TestSpawnerBlockCountsDownWithPlayerInRange(t *testing.T) {
	loop, pos, be := seedSpawnerLoop(t, 1)
	be.spawnDelay = 100

	before := totalEntities(loop)
	loop.spawnerServerTick(pos, be)

	if be.spawnDelay != 99 {
		t.Fatalf("with a player in range, spawnDelay must decrement by 1 (100 -> 99), got %d", be.spawnDelay)
	}
	if totalEntities(loop) != before {
		t.Fatalf("no mob may spawn while the spawner is still counting down")
	}
}

// TestSpawnerBlockNoCountdownWithoutPlayer: with NO player in range (isNearPlayer false) the spawner
// does not count down and spawns nothing - serverTick returns at the gate.
func TestSpawnerBlockNoCountdownWithoutPlayer(t *testing.T) {
	loop, pos, be := seedSpawnerLoop(t, 1)
	loop.players[0].x = 8.5 + 1000
	loop.players[0].z = 8.5 + 1000
	be.spawnDelay = 100

	before := totalEntities(loop)
	loop.spawnerServerTick(pos, be)

	if be.spawnDelay != 100 {
		t.Fatalf("with no player in range, spawnDelay must NOT change (stays 100), got %d", be.spawnDelay)
	}
	if totalEntities(loop) != before {
		t.Fatalf("no mob may spawn with no player in range")
	}
}

// TestSpawnerBlockIsNearPlayerRangeGate: isNearPlayer is true exactly when a player is within
// requiredPlayerRange of the spawner center - true just inside, false just outside.
func TestSpawnerBlockIsNearPlayerRangeGate(t *testing.T) {
	loop, pos, be := seedSpawnerLoop(t, 1)
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	loop.players[0].x = cx + 15
	loop.players[0].y = cy
	loop.players[0].z = cz
	if !loop.spawnerIsNearPlayer(pos, be) {
		t.Fatalf("a player 15 blocks away (< requiredPlayerRange 16) must be near")
	}
	loop.players[0].x = cx + 20
	if loop.spawnerIsNearPlayer(pos, be) {
		t.Fatalf("a player 20 blocks away (> requiredPlayerRange 16) must NOT be near")
	}
}

// TestSpawnerBlockMaxNearbyCap: when maxNearbyEntities same-type mobs are already inside the spawner
// cube inflated by spawnRange, the burst blocks after the first attempt (the cap gate) - the number of
// NEW mobs added is 0 (the just-spawned one is undone), and the delay is rerolled.
func TestSpawnerBlockMaxNearbyCap(t *testing.T) {
	loop, pos, be := seedSpawnerLoop(t, 7)
	be.spawnDelay = 0

	px := float64(pos.X) + 0.5
	py := float64(pos.Y)
	pz := float64(pos.Z) + 0.5
	for i := 0; i < be.maxNearbyEntities; i++ {
		loop.only().entities.add(NewEntity(loop.idAlloc.AllocID(), entity.Pig, px, py, pz))
	}
	before := totalEntities(loop)

	loop.spawnerServerTick(pos, be)

	if totalEntities(loop) != before {
		t.Fatalf("at the maxNearbyEntities cap, the burst must add no net new mob: %d -> %d", before, totalEntities(loop))
	}
	if be.spawnDelay < be.minSpawnDelay || be.spawnDelay >= be.maxSpawnDelay {
		t.Fatalf("the cap path must reroll spawnDelay into [%d,%d), got %d", be.minSpawnDelay, be.maxSpawnDelay, be.spawnDelay)
	}
}

// TestSpawnerBlockDelayReroll: the delay() reroll always lands in [minSpawnDelay, maxSpawnDelay) over
// many draws (the minSpawnDelay + nextInt(maxSpawnDelay - minSpawnDelay) bytecode).
func TestSpawnerBlockDelayReroll(t *testing.T) {
	loop, pos, be := seedSpawnerLoop(t, 123)
	for i := 0; i < 500; i++ {
		be.spawnDelay = -999
		loop.spawnerDelay(pos, be)
		if be.spawnDelay < be.minSpawnDelay || be.spawnDelay >= be.maxSpawnDelay {
			t.Fatalf("draw %d: spawnDelay %d out of [%d,%d)", i, be.spawnDelay, be.minSpawnDelay, be.maxSpawnDelay)
		}
	}
}

// TestSpawnerBlockDelayRerollDegenerate: when maxSpawnDelay <= minSpawnDelay the reroll sets exactly
// minSpawnDelay (the no-draw branch), matching the BaseSpawner.delay guard.
func TestSpawnerBlockDelayRerollDegenerate(t *testing.T) {
	loop, pos, be := seedSpawnerLoop(t, 1)
	be.minSpawnDelay = 300
	be.maxSpawnDelay = 300
	be.spawnDelay = -1
	loop.spawnerDelay(pos, be)
	if be.spawnDelay != 300 {
		t.Fatalf("with maxSpawnDelay <= minSpawnDelay, spawnDelay must be minSpawnDelay(300), got %d", be.spawnDelay)
	}
}

// TestBaseSpawnerDefaults: the newSpawnerBE ctor defaults match the javap-read BaseSpawner constructor
// constants exactly.
func TestBaseSpawnerDefaults(t *testing.T) {
	be := newSpawnerBE("")
	if be.spawnDelay != 20 {
		t.Fatalf("DEFAULT_SPAWN_DELAY: got %d want 20", be.spawnDelay)
	}
	if be.minSpawnDelay != 200 {
		t.Fatalf("DEFAULT_MIN_SPAWN_DELAY: got %d want 200", be.minSpawnDelay)
	}
	if be.maxSpawnDelay != 800 {
		t.Fatalf("DEFAULT_MAX_SPAWN_DELAY: got %d want 800", be.maxSpawnDelay)
	}
	if be.spawnCount != 4 {
		t.Fatalf("DEFAULT_SPAWN_COUNT: got %d want 4", be.spawnCount)
	}
	if be.maxNearbyEntities != 6 {
		t.Fatalf("DEFAULT_MAX_NEARBY_ENTITIES: got %d want 6", be.maxNearbyEntities)
	}
	if be.requiredPlayerRange != 16 {
		t.Fatalf("DEFAULT_REQUIRED_PLAYER_RANGE: got %d want 16", be.requiredPlayerRange)
	}
	if be.spawnRange != 4 {
		t.Fatalf("DEFAULT_SPAWN_RANGE: got %d want 4", be.spawnRange)
	}
}

// TestSpawnerBlockEmptyMobNameNoSpawn: an unconfigured spawner (empty mobName) spawns nothing even on
// delay-0 with a player in range, but still rerolls the delay (so it does not spin at 0).
func TestSpawnerBlockEmptyMobNameNoSpawn(t *testing.T) {
	loop, pos, _ := seedSpawnerLoop(t, 1)
	be := newSpawnerBE("")
	be.spawnDelay = 0

	before := totalEntities(loop)
	loop.spawnerServerTick(pos, be)

	if totalEntities(loop) != before {
		t.Fatalf("an empty-mobName spawner must spawn nothing, added %d", totalEntities(loop)-before)
	}
	if be.spawnDelay < be.minSpawnDelay || be.spawnDelay >= be.maxSpawnDelay {
		t.Fatalf("an empty spawner must still reroll the delay into [%d,%d), got %d", be.minSpawnDelay, be.maxSpawnDelay, be.spawnDelay)
	}
}
