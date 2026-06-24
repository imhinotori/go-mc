package server

// spawner_test.go — AI-03 tests: the ported MobCategory cap + the faithful-minimal
// NaturalSpawner (cap accounting + ON_GROUND placement + store add), and the tickAI() fill
// (drives serverAiStep + the throttled naturalSpawn) + the retired sinusoidal debug pig.
//
// The harness reuses the physics test world builder (newPhysicsLoop / putChunk / fillFloor /
// setBlock) plus a player so spawnableColumns has a loaded column near a player.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// newSpawnLoop builds a tick loop with one ready chunk at column (0,0), a solid floor at
// floorY, and a player standing on it — the minimal eligible spawn environment. Returns the
// loop, the chunk, and the floor Y so a test can place/clear blocks for placement assertions.
func newSpawnLoop(t *testing.T) (*TickLoop, *level.Chunk, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY) // solid [floorY,floorY+1); standable feet Y is floorY+1
	// A player on the floor at the column center so spawnableColumns includes (0,0) and the
	// scan reference Y is the floor surface.
	loop.players = append(loop.players, &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 8.5})
	return loop, ch, floorY
}

// TestMobCategoryCap: the ported CREATURE cap matches the javap-read value (10) and the Pig
// maps to CREATURE; a non-creature type maps elsewhere (MISC) so it does not consume the cap.
func TestMobCategoryCap(t *testing.T) {
	if got := categoryCreature.maxInstancesPerChunk(); got != 10 {
		t.Fatalf("CREATURE max-instances-per-chunk: got %d want 10 (javap NaturalSpawner/MobCategory)", got)
	}
	if got := categoryOf(entity.Pig.ID); got != categoryCreature {
		t.Fatalf("Pig should map to CREATURE, got %v", got)
	}
	if got := categoryOf(entity.SulfurCube.ID); got == categoryCreature {
		t.Fatalf("non-creature type must not map to CREATURE (would corrupt the cap count)")
	}
	// Spot-check a couple more ported caps so the whole enum is faithful, not just CREATURE.
	if got := categoryMonster.maxInstancesPerChunk(); got != 70 {
		t.Fatalf("MONSTER cap: got %d want 70", got)
	}
	if got := categoryAmbient.maxInstancesPerChunk(); got != 15 {
		t.Fatalf("AMBIENT cap: got %d want 15", got)
	}
}

// TestSpawnCapAccounting: with the store already holding CREATURE mobs AT the cap for the
// spawnable region, naturalSpawn adds NO new mob; below the cap it MAY add one (Pitfall 3 — no
// flood, no starvation). The cap = maxInstancesPerChunk * spawnableChunkCount.
func TestSpawnCapAccounting(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)

	cols := loop.spawnableColumns()
	if len(cols) == 0 {
		t.Fatal("expected at least one spawnable column near the player")
	}
	cap := categoryCreature.maxInstancesPerChunk() * len(cols)

	// Fill the store with exactly `cap` CREATURE mobs (pigs) so CREATURE is AT the cap.
	for i := 0; i < cap; i++ {
		loop.entities.add(NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, float64(floorY+1), 8.5))
	}
	before := loop.entities.len()
	loop.naturalSpawn()
	if loop.entities.len() != before {
		t.Fatalf("at cap, naturalSpawn must not spawn: count %d -> %d", before, loop.entities.len())
	}

	// Remove one so we are below cap; now a spawn IS allowed.
	for id := range loop.entities.byID {
		loop.entities.remove(id)
		break
	}
	below := loop.entities.len()
	loop.naturalSpawn()
	if loop.entities.len() != below+1 {
		t.Fatalf("below cap, naturalSpawn should add exactly one mob: count %d -> %d", below, loop.entities.len())
	}
}

// TestSpawnPlacementOnGround: naturalSpawn places the Pig only at a VALID standable block
// (solid below + clear air at feet and head). With the floor present a spawn lands on it; with
// the column blocked solid (no air) no spawn occurs (the ON_GROUND check rejects it).
func TestSpawnPlacementOnGround(t *testing.T) {
	loop, ch, floorY := newSpawnLoop(t)

	// (a) Valid floor: a spawn lands on the surface (feet at floorY+1, solid floorY below).
	loop.naturalSpawn()
	var spawned *Entity
	for _, e := range loop.entities.byID {
		if e.typ == entity.Pig.ID {
			spawned = e
		}
	}
	if spawned == nil {
		t.Fatal("expected a Pig to spawn on the valid floor")
	}
	if int(spawned.y) != floorY+1 {
		t.Fatalf("Pig should stand on the floor surface (feet Y=%d), got y=%v", floorY+1, spawned.y)
	}
	if !loop.blockSolidAt(floorI(spawned.x), int(spawned.y)-1, floorI(spawned.z)) {
		t.Fatal("Pig must have a solid block below it (ON_GROUND)")
	}
	if loop.blockSolidAt(floorI(spawned.x), int(spawned.y), floorI(spawned.z)) {
		t.Fatal("Pig must NOT be placed inside a solid block (clear feet)")
	}

	// (b) Block the entire scan window solid at the column center so there is NO air gap: no
	// standable block exists -> no spawn (the ON_GROUND placement check rejects every Y).
	loop2, mgr2 := newPhysicsLoop()
	ch2 := putChunk(mgr2, level.ChunkPos{0, 0})
	for y := floorY - spawnScanYRange - 2; y <= floorY+spawnScanYRange+2; y++ {
		setBlock(ch2, 8, y, 8) // solid column at the candidate (x=8,z=8) — no clearance anywhere
	}
	_ = ch // keep ch referenced (floor world built above)
	loop2.players = append(loop2.players, &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 8.5})
	before := loop2.entities.len()
	loop2.naturalSpawn()
	if loop2.entities.len() != before {
		t.Fatalf("a fully-blocked column must not spawn (no ON_GROUND clearance): %d -> %d", before, loop2.entities.len())
	}
	_ = ch2
}

// TestSpawnAddsToStore: a successful spawn calls entityStore.add with a fresh-id Pig at the
// placement position, AND attaches a real mobAI (so tickAI's serverAiStep will drive it). This
// is what makes the tracker spawn it on clients and the mob wander via the real AI.
func TestSpawnAddsToStore(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)

	before := loop.entities.len()
	loop.naturalSpawn()
	if loop.entities.len() != before+1 {
		t.Fatalf("a valid spawn must add exactly one entity: %d -> %d", before, loop.entities.len())
	}
	var pig *Entity
	for _, e := range loop.entities.byID {
		if e.typ == entity.Pig.ID {
			pig = e
		}
	}
	if pig == nil {
		t.Fatal("the spawned entity should be a Pig")
	}
	if pig.id == 0 {
		t.Fatal("the spawned Pig must have a fresh non-zero allocated id")
	}
	if pig.ai == nil {
		t.Fatal("a naturally-spawned Pig must have a real mobAI attached (so it wanders via serverAiStep), not a static mover")
	}
	// The mob is registered in the bucket index too (so near()/the tracker sees it).
	got := loop.entities.near(pig.x, pig.z, 0)
	found := false
	for _, e := range got {
		if e.id == pig.id {
			found = true
		}
	}
	if !found {
		t.Fatal("the spawned Pig must be bucketed so the tracker's near() returns it")
	}
}
