package server

// spawner_test.go — AI-03 tests: the ported MobCategory cap + the faithful-minimal
// NaturalSpawner (cap accounting + ON_GROUND placement + store add), and the tickAI() fill
// (drives serverAiStep + the throttled naturalSpawn) + the retired sinusoidal debug pig.
//
// The harness reuses the physics test world builder (newPhysicsLoop / putChunk / fillFloor /
// setBlock) plus a player so spawnableColumns has a loaded column near a player.

import (
	"reflect"
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

// --- Task 2: tickAI fill + retired sinusoidal debug pig --------------------------------

// TestTickAIDrivesMobs: calling tickAI runs serverAiStep for every AI mob in the store, so a
// mob with a deterministic MOVE goal WALKS toward its target over ticks (the per-mob AI is
// actually driven from the tickAI slot, not just defined). tickPhysics runs each tick too (as
// in the live pipeline) so gravity settles the post-AI-move Y.
func TestTickAIDrivesMobs(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	e := testEntity(1, entity.Pig, 2.5, float64(floorY+1), 8.5)
	ai := &mobAI{}
	ai.navigation.speed = 0.2
	ai.goals.addGoal(0, &fixedTargetGoal{
		baseGoal: newBaseGoal(flagMove),
		tx:       11.5, ty: float64(floorY + 1), tz: 8.5, // reachable point east along the floor
	})
	e.ai = ai
	loop.entities.add(e)

	startX := e.x
	for i := 0; i < 400; i++ {
		loop.tickAI() // the SLOT under test: it must drive serverAiStep for the mob
		loop.tickPhysics()
	}
	if e.x <= startX+2.0 {
		t.Fatalf("tickAI did not drive the mob's serverAiStep toward its goal: x=%v (start %v)", e.x, startX)
	}
}

// TestTickAISpawns: over enough throttled cycles with a player present + under cap, tickAI's
// naturalSpawn adds a Pig to the store (the spawner is wired into the live tick, not just
// callable directly).
func TestTickAISpawns(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)

	before := loop.entities.len()
	// Drive enough ticks that gametime crosses at least one spawnInterval boundary. gametime is
	// 0 on the first tickAI call, so the very first call already attempts a spawn.
	for i := 0; i < spawnInterval+1; i++ {
		loop.tickAI()
		loop.gametime++ // mirror tickOnce's per-tick increment (tickAI does not bump it itself)
	}
	if loop.entities.len() <= before {
		t.Fatalf("tickAI's throttled naturalSpawn should have added a mob over %d ticks: %d -> %d",
			spawnInterval+1, before, loop.entities.len())
	}
	// And the spawned mob carries a real AI (it will wander next tick).
	var pig *Entity
	for _, e := range loop.entities.byID {
		if e.typ == entity.Pig.ID {
			pig = e
		}
	}
	if pig == nil || pig.ai == nil {
		t.Fatal("tickAI-spawned Pig must have a real mobAI attached")
	}
}

// TestTickPhaseOrderUnchanged: filling tickAI must NOT reorder the fixed pipeline — tickAI
// still runs in its slot, after tickEntities and before tickPhysics. (Mirrors the canonical
// TestTickPhaseOrder so this plan's edit is self-guarded.)
func TestTickPhaseOrderUnchanged(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	var trace []string
	loop.traceTo(&trace)

	loop.tickOnce()

	if !reflect.DeepEqual(trace, expectedPhaseOrder) {
		t.Fatalf("tickAI fill changed the pipeline order:\n got: %v\nwant: %v", trace, expectedPhaseOrder)
	}
	// Explicitly assert tickAI sits between tickEntities and tickPhysics.
	idx := map[string]int{}
	for i, p := range trace {
		idx[p] = i
	}
	if !(idx["tickEntities"] < idx["tickAI"] && idx["tickAI"] < idx["tickPhysics"]) {
		t.Fatalf("tickAI must run after tickEntities and before tickPhysics: %v", trace)
	}
}

// TestDebugPigUsesRealAI: when SULFUR_DEBUG is armed (SetDebug) and a player is present, the
// debug pig spawned by tickDebug is driven by a real mobAI (it has an ai handle) and its motion
// comes from the AI path, NOT a sinusoidal mover — i.e. with NO floor (so the AI cannot move
// it) and no sine code, the pig does not drift along X by a cosmetic oscillation.
func TestDebugPigUsesRealAI(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	// The debug pig now spawns only once its column (0,0) is LOADED (the runtime async-chunk
	// guard). Load an EMPTY (all-air) column so the spawn fires but the AI still cannot move the
	// pig (no floor → no path) — exactly the no-sine assertion this test makes.
	putChunk(mgr, level.ChunkPos{0, 0})
	loop.SetDebug(64)          // arm the off-by-default debug trigger
	loop.debug.damageEvery = 0 // disable the damage bite (it needs a real Client.Send)
	// debugGaveItems=true skips the give-stone branch (which needs a real Client.Send); this
	// test only exercises the spawn + motion path, not the inventory/damage triggers.
	loop.players = append(loop.players, &tickPlayer{x: 8.5, y: 65, z: 8.5, debugGaveItems: true})

	// One tickEntities runs tickDebug, which spawns the debug pig.
	loop.tickEntities()

	pig, ok := loop.entities.get(loop.debug.pigID)
	if !ok {
		t.Fatal("SULFUR_DEBUG should have spawned a debug pig")
	}
	if pig.ai == nil {
		t.Fatal("the debug pig must be driven by a real mobAI (newPigAI), not the retired sinusoidal mover")
	}

	// Assert the OLD sinusoidal mover is gone: tickDebug must not itself mutate the pig's X with
	// a sine each tick. With no world wired the AI navigation cannot move the pig (no path), so
	// repeated tickDebug calls must leave the pig's X exactly where it spawned — proving no
	// cosmetic sine oscillation drives it anymore.
	startX := pig.x
	for i := 0; i < 50; i++ {
		loop.tickDebug()
	}
	if pig.x != startX {
		t.Fatalf("tickDebug still moves the pig with a sinusoidal mover (x %v -> %v); the sine mover must be retired",
			startX, pig.x)
	}
}
