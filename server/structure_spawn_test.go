package server

import (
	"sync"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/structure"
)

// structure_spawn_test.go — STRUCT-POLISH-02 Task 1: the off-tick->tick spawn seam.
//
// The worker RECORDS structure-inhabitant SpawnRequests on ChunkResult.Spawns off-tick; the
// tick DRAINS them onto the entity store (chunkReady.applyTo -> drainStructureSpawns). These
// tests prove: (1) N requests -> N entities added with allocated ids; (2) the -race seam (the
// worker side only records, the tick side only adds — no off-tick store mutation); (3) the
// resolved entity types match the request id strings.

// newStructureSpawnLoop wires a TickLoop with one ready chunk so the spawn drain has a loaded column.
func newStructureSpawnLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	return loop, mgr
}

// TestStructureSpawnSeam: a ChunkResult carrying N SpawnRequests, drained by applyTo, adds N
// entities to the store, each with an allocated id and the resolved entity type/position.
func TestStructureSpawnSeam(t *testing.T) {
	loop, _ := newStructureSpawnLoop()

	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull

	reqs := []structure.SpawnRequest{
		{EntityType: "minecraft:witch", X: 8.5, Y: 65, Z: 8.5, PersistenceRequired: true},
		{EntityType: "minecraft:cat", X: 8.5, Y: 65, Z: 8.5, PersistenceRequired: true},
		{EntityType: "minecraft:villager", X: 4.5, Y: 64, Z: 4.5, PersistenceRequired: true},
	}

	before := loop.only().entities.len()
	cr := chunkReady{res: world.ChunkResult{Pos: level.ChunkPos{0, 0}, Chunk: ch, Spawns: reqs}}
	cr.applyTo(loop)

	if got := loop.only().entities.len(); got != before+len(reqs) {
		t.Fatalf("entity count = %d, want %d (%d structure spawns)", got, before+len(reqs), len(reqs))
	}

	// Each request resolved to the right type at the right position.
	want := map[entity.ID]int{entity.Witch.ID: 1, entity.Cat.ID: 1, entity.Villager.ID: 1}
	got := map[entity.ID]int{}
	for _, e := range loop.only().entities.all() {
		got[e.typ]++
		if e.id == 0 {
			t.Fatalf("spawned entity has id 0 — AllocID not called")
		}
	}
	for typ, n := range want {
		if got[typ] != n {
			t.Fatalf("entity type %d count = %d, want %d", typ, got[typ], n)
		}
	}
}

// TestStructureSpawnMobTakesDamageAndDies is the CR-01 regression: a structure-spawned mob (NOT a
// declared mob — it rides drainStructureSpawns, the spawn path that previously omitted health init)
// must spawn at its folded MaxHealth, lose health on a player hit, and die when its health reaches 0.
//
// Before the fix every structure mob was born at health 0, which made applyDamageEntity's
// `e.health <= 0` entry guard a silent no-op and treated the mob as already-dead — the keystone
// "a mob can take and deal damage" (MOB-SUB-01) was silently defeated for this path and NO test
// covered it. This test would have failed (health 0 at spawn; the hit a no-op) before the fix.
func TestStructureSpawnMobTakesDamageAndDies(t *testing.T) {
	loop, _ := newStructureSpawnLoop()

	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull

	// A single Witch at column (0,0) -> region 0, so cur()/regionForEntity resolve to the same
	// single test region (the damage + death store mutations land where the mob lives).
	reqs := []structure.SpawnRequest{
		{EntityType: "minecraft:witch", X: 8.5, Y: 65, Z: 8.5, PersistenceRequired: true},
	}
	chunkReady{res: world.ChunkResult{Pos: level.ChunkPos{0, 0}, Chunk: ch, Spawns: reqs}}.applyTo(loop)

	// Find the spawned witch in the store.
	var witch *Entity
	for _, e := range loop.only().entities.all() {
		if e.typ == entity.Witch.ID {
			witch = e
			break
		}
	}
	if witch == nil {
		t.Fatal("witch was not spawned into the store")
	}

	// (1) Born at its folded MaxHealth (Witch MAX_HEALTH = 26.0), NOT health 0 (the bug).
	const witchMaxHealth float32 = 26.0
	if witch.health != witchMaxHealth {
		t.Fatalf("structure mob spawn health = %v, want %v (LivingEntity.<init> setHealth(getMaxHealth())) — health 0 means the CR-01 spawn-health gap is back", witch.health, witchMaxHealth)
	}

	// (2) A player hit reduces health (the keystone: a structure mob CAN take damage). 0 armor on
	// the witch base, so the hit lands full (player_attack folds the armor curve over base-0 armor).
	src := damageSourcePlayerAttack(0)
	loop.applyDamageEntity(witch, src, 6.0)
	if witch.health != witchMaxHealth-6.0 {
		t.Fatalf("after a 6.0 hit, structure mob health = %v, want %v (the hit must land, not be a health-0 no-op)", witch.health, witchMaxHealth-6.0)
	}

	// (3) Lethal damage kills it: dieEntity marks it dead but leaves the corpse in the world for the
	// ~1s death animation (vanilla die() has no remove()); tickDeath removes it at deathTime>=20. Wait
	// out the i-frame window between hits so the second hit is not absorbed by invulnerableTime.
	for i := int32(0); i < witch.invulnerableTime; i++ {
		loop.tickMobIFrames(witch)
	}
	loop.applyDamageEntity(witch, src, witchMaxHealth) // overkill -> health 0 -> dieEntity (no remove)
	if !witch.dead {
		t.Fatalf("structure mob not marked dead after a lethal hit — a health-0-born mob could never die through this path")
	}
	if _, ok := loop.only().entities.byID[witch.id]; !ok {
		t.Fatalf("structure mob removed immediately on death — die() must leave the corpse for the death animation")
	}
	// Drive the death-animation countdown: removed exactly at deathTime>=20.
	for i := int32(0); i < deathAnimationTicks; i++ {
		loop.tickDeath(witch)
	}
	if _, ok := loop.only().entities.byID[witch.id]; ok {
		t.Fatalf("structure mob still in the store after the death animation (deathTime>=%d), want removed (tickDeath)", deathAnimationTicks)
	}
}

// TestStructureSpawnUnknownTypeSkipped: an unresolvable entity-type id is a no-op (never panics,
// never adds a malformed entity) — the region NBT / build-data robustness contract.
func TestStructureSpawnUnknownTypeSkipped(t *testing.T) {
	loop, _ := newStructureSpawnLoop()
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull

	reqs := []structure.SpawnRequest{
		{EntityType: "minecraft:not_a_real_entity", X: 1, Y: 64, Z: 1},
		{EntityType: "minecraft:witch", X: 2, Y: 64, Z: 2},
	}
	before := loop.only().entities.len()
	chunkReady{res: world.ChunkResult{Pos: level.ChunkPos{0, 0}, Chunk: ch, Spawns: reqs}}.applyTo(loop)

	// Only the witch resolves; the unknown id is skipped.
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("entity count = %d, want %d (only the witch resolves)", got, before+1)
	}
}

// TestStructureSpawnRaceClean: the worker side (building the immutable ChunkResult with Spawns)
// runs concurrently with NO access to the tick store; the tick drains on the owner. This
// exercises the seam from two goroutines so the -race gate proves the off-tick->tick crossing is
// race-clean (Pitfall 5): the requests are a VALUE, the store add happens only on the tick.
func TestStructureSpawnRaceClean(t *testing.T) {
	loop, _ := newStructureSpawnLoop()

	const n = 16
	results := make(chan world.ChunkResult, n)

	// Off-tick producers: each builds an immutable ChunkResult carrying SpawnRequests. They
	// touch NO tick state (no loop.only().entities, no idAlloc) — exactly the worker discipline.
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ch := level.EmptyChunk(blockTestSecs)
			ch.Status = level.StatusFull
			results <- world.ChunkResult{
				Pos:   level.ChunkPos{int32(i), 0},
				Chunk: ch,
				Spawns: []structure.SpawnRequest{
					{EntityType: "minecraft:witch", X: float64(i*16) + 8.5, Y: 65, Z: 8.5, PersistenceRequired: true},
				},
			}
		}(i)
	}
	wg.Wait()
	close(results)

	// The TICK drains each on the owner goroutine (this goroutine) — the only store mutation.
	// Phase-27 N=2: the 16 columns alternate regions (regionOf checkerboard), so chunkReady.applyTo
	// now routes each witch into the region that OWNS its column. Count ACROSS regions — the spawns
	// are distributed, not all in region 0 (the fix this whole change makes correct).
	allEntities := func() int {
		total := 0
		for _, r := range loop.regions {
			if r.entities != nil {
				total += r.entities.len()
			}
		}
		return total
	}
	before := allEntities()
	for res := range results {
		chunkReady{res: res}.applyTo(loop)
	}
	if got := allEntities(); got != before+n {
		t.Fatalf("entity count = %d, want %d (one witch per chunk, across regions)", got, before+n)
	}
}
