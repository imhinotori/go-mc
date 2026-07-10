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
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// spawnTestSeed is the FIXED per-region levelRandom seed the spawner tests pin so the natural-spawn
// draws (the candidate column pick + the pack cluster-spread nextInt(6) draws + the packSize/yaw
// nextFloat draws -- spawner.go/natural_spawner.go) are DETERMINISTIC. Production seeds each region's
// levelRandom from uniqueLevelRandomSeed() (region.go -- a nanoTime-XOR nondeterministic seed, faithful
// to vanilla RandomSupport.generateUniqueSeed). That nondeterminism is exactly what made the count
// assertions flaky: on ~3% of process seeds the randomly-picked spawn column + pack spread landed
// INSIDE the vanilla 24-block no-spawn bubble around the player (NaturalSpawner.
// isRightDistanceToPlayerAndSpawnPoint: d <= 576 rejects it), so a valid cycle produced 0 mobs and the
// "below cap a spawn happens" assertion failed nondeterministically (the STATE.md async-spawner flake).
// Pinning the seed makes the outcome reproducible WITHOUT changing any production spawn logic -- the
// same faithful cap/distance/placement gates run; only the RNG stream is fixed. spawnTestSeed is a seed
// VERIFIED (brute-force over 1..200: 194/200 seeds spawn) to place the pack OUTSIDE the 24-block bubble
// in newSpawnLoop's 11x11 geometry, so the load-bearing assertion ("below cap, a spawn happens") still
// holds and still catches a real regression in the cap/placement path.
const spawnTestSeed = int64(1)

// seedSpawnRegions pins EVERY region's levelRandom to spawnTestSeed so the natural-spawn draws are
// deterministic across runs / -count / full-suite CPU contention. Applied by newSpawnLoop right after
// the world+player are wired so the first naturalSpawn draw is reproducible. It seeds ALL regions (not
// just region 0) because the spawn is routed into whichever region OWNS the randomly-picked column
// (regionOf(col)=(x^z)&1), so the OWNING region's levelRandom is the one the pack-spread draws read.
func seedSpawnRegions(loop *TickLoop) {
	for _, r := range loop.regions {
		if r != nil {
			r.levelRandom = levelgen.NewLegacyRandomSource(spawnTestSeed)
		}
	}
}

// totalEntities sums the entity count ACROSS every region. The natural spawner picks RANDOM
// candidate columns across the loaded 11x11 area and routes the spawned mob into the region that
// OWNS that column (regionOf(col)=(x^z)&1) — so a spawn can land in EITHER region. loop.only()
// resolves to region 0 only, which would make a region-1 spawn invisible to a count assertion;
// counting across regions is the region-routing-correct way to ask "did the spawner add a mob".
func totalEntities(loop *TickLoop) int {
	total := 0
	for _, r := range loop.regions {
		if r != nil && r.entities != nil {
			total += r.entities.len()
		}
	}
	return total
}

// findEntityOfType scans EVERY region's by-id map for the first entity whose type matches one of
// typ. Used by the "find the spawned Pig" assertions: the spawned mob can be routed into either
// region, so a region-0-only scan (loop.only().entities.byID) would miss a region-1 spawn.
func findEntityOfType(loop *TickLoop, typ ...entity.Entity) *Entity {
	for _, r := range loop.regions {
		if r == nil || r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			for _, want := range typ {
				if e.typ == want.ID {
					return e
				}
			}
		}
	}
	return nil
}

// findAnyNaturalCreature scans EVERY region for the first naturally-spawnable CREATURE mob (pig, cow,
// sheep, or chicken). Plan 34-04 generalized the natural spawner from pig-hardcoded to a uniform pick
// among the 4 CREATURE mobs, so a spawn-placement/store/AI assertion can no longer expect a Pig
// specifically — it expects ONE of the 4. The placement + store + AI properties under test are
// CREATURE-agnostic (every passive mob lands ON_GROUND, gets a fresh id, carries a real mobAI).
func findAnyNaturalCreature(loop *TickLoop) *Entity {
	return findEntityOfType(loop, entity.Pig, entity.Cow, entity.Sheep, entity.Chicken, entity.Rabbit)
}

// runSpawnCycle drives ONE full OPT-03 natural-spawn cycle end-to-end for a test: it submits the
// off-tick candidate scan via naturalSpawn (the owner side), then — if a scan was actually
// submitted (under cap, columns available, pool not overloaded) — deterministically receives the
// single worker result off asyncIn2 and applies it on the test goroutine (standing in for the
// owner's applyAsyncResults drain). This makes the now-async spawner synchronous for assertions
// WITHOUT changing the production flow: in production applyAsyncResults drains asyncIn2 and calls
// applyTo exactly the same way. When naturalSpawn submits nothing (at cap / no spawnable column /
// overloaded), spawnScanPending stays false and the helper returns immediately — no spawn, as
// expected.
func runSpawnCycle(t *testing.T, loop *TickLoop) {
	t.Helper()
	loop.naturalSpawn()
	if !loop.only().spawnScanPending {
		return // no scan submitted this cycle (at cap, no eligible column, or pool overloaded)
	}
	select {
	case r := <-loop.asyncIn2:
		r.applyTo(loop) // the owner re-checks the cap + mobNear and adds at most one (async.go)
	case <-time.After(2 * time.Second):
		t.Fatal("spawn scan result never arrived on asyncIn2 (the off-tick worker did not rejoin)")
	}
}

// newSpawnLoop builds a tick loop with one ready chunk at column (0,0), a solid floor at
// floorY, and a player standing on it — the minimal eligible spawn environment. Returns the
// loop, the chunk, and the floor Y so a test can place/clear blocks for placement assertions.
func newSpawnLoop(t *testing.T) (*TickLoop, *level.Chunk, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY) // solid [floorY,floorY+1); standable feet Y is floorY+1
	// The floor SURFACE (world-Y == floorY, the block below a feet-Y==floorY+1 spawn) is laid as
	// grass_block so the CREATURE spawn-rules gate passes: the natural spawner's CREATURE pass runs
	// Animal.checkAnimalSpawnRules (below is #animals_spawnable_on == grass_block) at the
	// isValidSpawnPostitionForType -> checkSpawnRules point inside spawnPackAt; on a stone floor those
	// rules (correctly) reject every candidate. RNG-free (a pure tag + brightness read), so the pig/
	// creature draw stream stays byte-identical. Cite Animal.checkAnimalSpawnRules.
	setSurfaceGrass(ch, floorY)
	// The vanilla CREATURE cap is maxInstancesPerChunk * spawnableChunkCount / MAGIC_NUMBER(289)
	// (creatureCap). A single loaded column yields cap = 10*1/289 = 0 — no spawn is ever allowed,
	// which is correct vanilla behavior but makes a "must spawn" test impossible. Load enough columns
	// around the player that the cap is >= a few: 11x11 = 121 columns -> cap = 10*121/289 = 4. The
	// floor is filled in (0,0) (the column the player + the scan reference sit on, where spawns land).
	const r = 5 // 11x11 columns around the player
	for dx := -r; dx <= r; dx++ {
		for dz := -r; dz <= r; dz++ {
			if dx == 0 && dz == 0 {
				continue // (0,0) already created + floored above
			}
			c := putChunk(mgr, level.ChunkPos{int32(dx), int32(dz)})
			fillFloor(c, floorY)
			setSurfaceGrass(c, floorY) // grass surface so the CREATURE spawn-rules ground check passes
		}
	}
	// A player on the floor at the column center so spawnableColumns includes (0,0) and the
	// scan reference Y is the floor surface.
	loop.players = append(loop.players, &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 8.5})
	// Pin the per-region RNG so the natural-spawn column pick + pack spread are deterministic (see
	// spawnTestSeed): production's nondeterministic uniqueLevelRandomSeed() otherwise lands the pack
	// inside the 24-block no-spawn bubble on a minority of seeds, flaking the count assertions.
	seedSpawnRegions(loop)
	// Light the spawn columns to full daylight (SKY 15) by DEFAULT so the per-position monster darkness
	// gate (Monster.isDarkEnoughToSpawn, spawner.go) treats this as a daylit surface -- the MONSTER pass
	// then places NO hostile, isolating the CREATURE assertions in these tests from incidental monster
	// spawns. Monster-specific tests (spawner_monster_test.go) call lightAllSpawnColumns(loop, 0) after
	// this to open the dark gate. The CREATURE spawn path draws NO light-gate RNG (Animal spawn rules use
	// a pure brightness read, no nextInt), so lighting here leaves every creature/pig draw byte-identical.
	for _, col := range loop.spawnableColumns() {
		if c, ok := mgr.Get(col); ok {
			setSkyLight(c, 15)
		}
	}
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
	cap := creatureCap(len(cols)) // maxInstancesPerChunk * spawnableChunkCount / MAGIC_NUMBER (vanilla)

	// Fill the store with exactly `cap` CREATURE mobs (pigs) so CREATURE is AT the cap.
	for i := 0; i < cap; i++ {
		loop.only().entities.add(NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, float64(floorY+1), 8.5))
	}
	before := totalEntities(loop)
	runSpawnCycle(t, loop)
	if totalEntities(loop) != before {
		t.Fatalf("at cap, naturalSpawn must not spawn: count %d -> %d", before, totalEntities(loop))
	}

	// Remove one so we are below cap; now a spawn IS allowed.
	for id := range loop.only().entities.byID {
		loop.only().entities.remove(id)
		break
	}
	below := totalEntities(loop)
	runSpawnCycle(t, loop)
	// The spawned mob(s) are routed into the region OWNING the candidate column — either region — so
	// count across regions, not just region 0 (loop.only()). C-6: the apply now places a vanilla
	// PACK-GROUP (NaturalSpawner.spawnCategoryForPosition, up to getMaxSpawnClusterSize=4) instead of a
	// single mob, so below cap the cycle adds AT LEAST one (the exact count is the drawn packSize,
	// bounded by the group cap). The load-bearing assertion is "below cap, a spawn happens".
	added := totalEntities(loop) - below
	if added < 1 {
		t.Fatalf("below cap, naturalSpawn should add at least one mob (a pack): count %d -> %d", below, totalEntities(loop))
	}
	if added > maxSpawnClusterSize {
		t.Fatalf("the pack-group cap (getMaxSpawnClusterSize=%d) must bound one apply: added %d", maxSpawnClusterSize, added)
	}
}

// TestSpawnPlacementOnGround: naturalSpawn places the Pig only at a VALID standable block
// (solid below + clear air at feet and head). With the floor present a spawn lands on it; with
// the column blocked solid (no air) no spawn occurs (the ON_GROUND check rejects it).
func TestSpawnPlacementOnGround(t *testing.T) {
	loop, ch, floorY := newSpawnLoop(t)

	// (a) Valid floor: a spawn lands on the surface (feet at floorY+1, solid floorY below).
	runSpawnCycle(t, loop)
	// The mob is routed into whichever region owns its random candidate column — scan across regions.
	// Plan 34-04: the natural spawn picks among the 4 CREATURE mobs, so accept any of them.
	spawned := findAnyNaturalCreature(loop)
	if spawned == nil {
		t.Fatal("expected a CREATURE mob (pig/cow/sheep/chicken) to spawn on the valid floor")
	}
	if int(spawned.y) != floorY+1 {
		t.Fatalf("mob should stand on the floor surface (feet Y=%d), got y=%v", floorY+1, spawned.y)
	}
	if !loop.blockSolidAt(floorI(spawned.x), int(spawned.y)-1, floorI(spawned.z)) {
		t.Fatal("mob must have a solid block below it (ON_GROUND)")
	}
	if loop.blockSolidAt(floorI(spawned.x), int(spawned.y), floorI(spawned.z)) {
		t.Fatal("mob must NOT be placed inside a solid block (clear feet)")
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
	before := totalEntities(loop2)
	runSpawnCycle(t, loop2)
	if totalEntities(loop2) != before {
		t.Fatalf("a fully-blocked column must not spawn (no ON_GROUND clearance): %d -> %d", before, totalEntities(loop2))
	}
	_ = ch2
}

// TestSpawnAddsToStore: a successful spawn calls entityStore.add with a fresh-id Pig at the
// placement position, AND attaches a real mobAI (so tickAI's serverAiStep will drive it). This
// is what makes the tracker spawn it on clients and the mob wander via the real AI.
func TestSpawnAddsToStore(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)

	before := totalEntities(loop)
	runSpawnCycle(t, loop)
	// The spawned mob(s) are routed into the region owning the candidate column — count + scan across
	// regions (loop.only() is region 0 only and would miss a region-1 spawn). C-6: a valid apply now
	// places a vanilla PACK-GROUP (up to getMaxSpawnClusterSize=4), so it adds AT LEAST one.
	added := totalEntities(loop) - before
	if added < 1 {
		t.Fatalf("a valid spawn must add at least one entity (a pack): %d -> %d", before, totalEntities(loop))
	}
	if added > maxSpawnClusterSize {
		t.Fatalf("the pack-group cap (getMaxSpawnClusterSize=%d) must bound one apply: added %d", maxSpawnClusterSize, added)
	}
	mob := findAnyNaturalCreature(loop)
	if mob == nil {
		t.Fatal("the spawned entity should be a CREATURE mob (pig/cow/sheep/chicken)")
	}
	if mob.id == 0 {
		t.Fatal("the spawned mob must have a fresh non-zero allocated id")
	}
	if mob.ai == nil {
		t.Fatal("a naturally-spawned mob must have a real mobAI attached (so it wanders via serverAiStep), not a static mover")
	}
	// The mob is registered in the bucket index too (so near()/the tracker sees it). Query near()
	// on the region that OWNS the mob (its column may be region 1, not region 0).
	owner := loop.owningRegion(mob.id)
	if owner == nil {
		t.Fatal("the spawned mob must be owned by some region")
	}
	got := owner.entities.near(mob.x, mob.z, 0)
	found := false
	for _, e := range got {
		if e.id == mob.id {
			found = true
		}
	}
	if !found {
		t.Fatal("the spawned mob must be bucketed so the tracker's near() returns it")
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
	loop.only().entities.add(e)

	startX := e.x
	// Drive the AI pipeline until the mob has clearly advanced toward its goal, up to a generous
	// tick budget. The off-tick A* (OPT-01) rejoins via asyncIn2 1+ ticks late, and under heavy
	// CPU contention (the full-suite run) the ants pool can take many ticks to deliver the first
	// path — so a FIXED 400-tick loop with a non-blocking applyAsyncResults occasionally finished
	// before the path landed and the mob had not yet moved (the historical flake). Looping until
	// the advance threshold is met (or the budget is exhausted) makes the assertion robust to that
	// scheduling jitter WITHOUT changing any production AI logic: it still proves tickAI drives
	// serverAiStep toward the goal, just without assuming a specific arrival tick. A blocking drain
	// of asyncIn2 each iteration (mirroring TestTickAISpawns) guarantees a delivered path is applied
	// the same tick it arrives rather than being missed by the non-blocking drain.
	const tickBudget = 4000
	const advanceThreshold = 2.0
	for i := 0; i < tickBudget && e.x <= startX+advanceThreshold; i++ {
		loop.tickAI() // the SLOT under test: it must drive serverAiStep for the mob
		loop.tickPhysics()
		// Apply any path the off-tick worker has already delivered (non-blocking, like production).
		loop.applyAsyncResults()
		// If nothing was queued yet but the worker is still computing, BLOCK BRIEFLY for the pool to
		// deliver so a contended scheduler does not starve this test of its single path. The previous
		// version used a non-blocking `default` here despite the comment claiming a timeout — under
		// full-suite CPU contention that let the loop spin past the still-computing path and exhaust
		// the budget without ever applying it (the residual flake). A real timed receive waits up to
		// a few ms per iteration (×4000 budget = ample wall-clock for the single A*) without hanging.
		select {
		case r := <-loop.asyncIn2:
			r.applyTo(loop)
		case <-time.After(2 * time.Millisecond):
		}
	}
	if e.x <= startX+advanceThreshold {
		t.Fatalf("tickAI did not drive the mob's serverAiStep toward its goal within %d ticks: x=%v (start %v)", tickBudget, e.x, startX)
	}
}

// TestTickAISpawns: over enough throttled cycles with a player present + under cap, tickAI's
// naturalSpawn adds a Pig to the store (the spawner is wired into the live tick, not just
// callable directly).
func TestTickAISpawns(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)

	before := totalEntities(loop)
	// Drive enough ticks that gametime crosses at least one spawnInterval boundary. gametime is
	// 0 on the first tickAI call, so the very first call already submits a spawn scan. OPT-03: the
	// scan is off-tick — tickAI SUBMITS to spawnPool and applyAsyncResults (the pipeline phase that
	// runs after tickAI each tick in the live loop) rejoins it on the owner. Drive applyAsyncResults
	// here so the off-tick candidate scan lands and the owner adds the mob (the spawn arrives a tick
	// or two later, as designed).
	for i := 0; i < spawnInterval+1; i++ {
		loop.tickAI()
		loop.applyAsyncResults() // drain asyncIn2: the owner re-checks the cap + adds (async.go)
		loop.gametime++          // mirror tickOnce's per-tick increment (tickAI does not bump it itself)
	}
	// The off-tick worker may not have finished within the loop above (the pool runs concurrently);
	// give the pending scan a final bounded chance to rejoin so the assertion is deterministic.
	if loop.only().spawnScanPending {
		select {
		case r := <-loop.asyncIn2:
			r.applyTo(loop)
		case <-time.After(2 * time.Second):
			t.Fatal("tickAI's off-tick spawn scan never rejoined on asyncIn2")
		}
	}
	// The spawn is routed into the region owning its random candidate column — count across regions.
	if totalEntities(loop) <= before {
		t.Fatalf("tickAI's throttled naturalSpawn should have added a mob over %d ticks: %d -> %d",
			spawnInterval+1, before, totalEntities(loop))
	}
	// And the spawned mob carries a real AI (it will wander next tick). Plan 34-04: any of the 4
	// CREATURE mobs may be the one picked, so accept any of them.
	mob := findAnyNaturalCreature(loop)
	if mob == nil || mob.ai == nil {
		t.Fatal("tickAI-spawned CREATURE mob must have a real mobAI attached")
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

	pig, ok := loop.only().entities.get(loop.debug.pigID)
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

// --- OPT-03 (08-05): async mob spawning — off-tick scan, owner-side cap-checked add ----------

// TestAsyncSpawnRejoinsAndAdds: under cap with a standable column, naturalSpawn SUBMITS the
// candidate scan off-tick (spawnScanPending set) and, after the pool runs and the owner applies the
// rejoin, exactly ONE Pig is added at a standable position with a real mobAI attached. This is the
// happy path of the off-tick scan -> owner add swap.
func TestAsyncSpawnRejoinsAndAdds(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)

	before := totalEntities(loop)
	loop.naturalSpawn()
	if !loop.only().spawnScanPending {
		t.Fatal("under cap with a standable column, naturalSpawn must SUBMIT an off-tick scan (spawnScanPending)")
	}
	// Rejoin the off-tick result on the owner (the production applyAsyncResults drain).
	select {
	case r := <-loop.asyncIn2:
		r.applyTo(loop)
	case <-time.After(2 * time.Second):
		t.Fatal("the off-tick spawn scan never rejoined on asyncIn2")
	}
	if loop.only().spawnScanPending {
		t.Fatal("applyTo must CLEAR the single-in-flight gate so the next cycle can submit")
	}
	// The mob(s) are routed into the region owning the candidate column — count + scan across regions
	// (loop.only() is region 0 only and would miss a region-1 spawn). C-6: the apply now places a
	// vanilla PACK-GROUP (up to getMaxSpawnClusterSize=4), so it adds AT LEAST one.
	addedAsync := totalEntities(loop) - before
	if addedAsync < 1 {
		t.Fatalf("the async scan + owner add must add at least one mob (a pack): %d -> %d", before, totalEntities(loop))
	}
	if addedAsync > maxSpawnClusterSize {
		t.Fatalf("the pack-group cap (getMaxSpawnClusterSize=%d) must bound one apply: added %d", maxSpawnClusterSize, addedAsync)
	}
	// Plan 34-04: the natural spawn picks among the 4 CREATURE mobs, so accept any of them.
	mob := findAnyNaturalCreature(loop)
	if mob == nil {
		t.Fatal("the rejoined spawn must add a CREATURE mob (pig/cow/sheep/chicken)")
	}
	if mob.ai == nil {
		t.Fatal("an async-spawned mob must have a real mobAI attached (the owner add path mirrors the sync spawn)")
	}
	if int(mob.y) != floorY+1 {
		t.Fatalf("the mob must stand on the floor surface (feet Y=%d), got y=%v", floorY+1, mob.y)
	}
	// The add ran on the OWNER (in applyTo), so the bucket index is consistent for near(). Query
	// near() on the region that OWNS the mob (its column may be region 1, not region 0).
	owner := loop.owningRegion(mob.id)
	if owner == nil {
		t.Fatal("the async-spawned mob must be owned by some region")
	}
	found := false
	for _, e := range owner.entities.near(mob.x, mob.z, 0) {
		if e.id == mob.id {
			found = true
		}
	}
	if !found {
		t.Fatal("the async-spawned mob must be bucketed (the owner add re-buckets it)")
	}
}

// TestAsyncSpawnCapRecheck: the cap is RE-CHECKED on apply against the AUTHORITATIVE store (Pitfall
// 3 anti-flood). naturalSpawn submits a scan while UNDER cap; then, BEFORE the rejoin is applied,
// the store is filled to the cap (simulating other spawns landing between the off-tick scan and its
// apply). applyTo must re-check and DROP — no over-cap add — because the scan's count was stale.
func TestAsyncSpawnCapRecheck(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)

	cols := loop.spawnableColumns()
	if len(cols) == 0 {
		t.Fatal("expected at least one spawnable column near the player")
	}
	cap := creatureCap(len(cols)) // maxInstancesPerChunk * spawnableChunkCount / MAGIC_NUMBER (vanilla)

	// Submit the scan while the store is EMPTY (well under cap).
	loop.naturalSpawn()
	if !loop.only().spawnScanPending {
		t.Fatal("under cap, naturalSpawn must submit an off-tick scan")
	}
	// Receive the scan result but DO NOT apply it yet.
	var result asyncResult
	select {
	case result = <-loop.asyncIn2:
	case <-time.After(2 * time.Second):
		t.Fatal("the off-tick spawn scan never rejoined on asyncIn2")
	}

	// Now flood the store to EXACTLY the cap BEFORE applying — the world filled up since the scan.
	for i := 0; i < cap; i++ {
		loop.only().entities.add(NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, float64(floorY+1), 8.5))
	}
	atCap := totalEntities(loop)

	// Apply the STALE scan result on the owner: the cap re-check must DROP it (no over-cap add).
	// The cap is GLOBAL/cross-region, and a re-checked spawn would route into either region — count
	// across regions so a (dropped) region-1 candidate is still accounted for.
	result.applyTo(loop)
	if totalEntities(loop) != atCap {
		t.Fatalf("a stale scan must NOT over-spawn past the re-checked cap: %d -> %d (cap=%d)",
			atCap, totalEntities(loop), cap)
	}
	if loop.only().spawnScanPending {
		t.Fatal("applyTo must clear the in-flight gate even when it drops the spawn (no wedge)")
	}
}

// TestAsyncSpawnOccupiedDropped: the mobNear anti-piling guard is RE-CHECKED on apply against the
// LIVE store. A candidate whose position is already occupied (a mob within the packing radius at
// apply time) is DROPPED — the owner never piles a mob onto an occupied spot, even though the
// off-tick scan (over a stale snapshot) proposed it.
func TestAsyncSpawnOccupiedDropped(t *testing.T) {
	loop, _, floorY := newSpawnLoop(t)

	// Pre-place a mob at the exact candidate position so mobNear is true there at apply time.
	const cx, cz = 8, 8
	occupier := NewEntity(loop.idAlloc.AllocID(), entity.Pig, float64(cx)+0.5, float64(floorY+1), float64(cz)+0.5)
	loop.only().entities.add(occupier)
	before := loop.only().entities.len()

	// A scan result proposing ONLY the occupied candidate. spawnableChunkCount is large so the cap
	// re-check passes (live=1 << cap) and the ONLY drop reason under test is the mobNear guard.
	result := spawnCandidatesReady{
		candidates:          []spawnCandidate{{x: cx, y: floorY + 1, z: cz}},
		spawnableChunkCount: 100,
		category:            categoryCreature, // Phase 35-02: the message now carries its category
	}
	result.applyTo(loop)

	if loop.only().entities.len() != before {
		t.Fatalf("an occupied candidate must be DROPPED (anti-piling guard re-checked on the owner): %d -> %d",
			before, loop.only().entities.len())
	}
}

// TestAsyncSpawnPoolOverloadSkips: a saturated spawnPool makes the cycle a no-op (Pitfall 4). With
// every spawnPool worker busy, naturalSpawn's submit is DROPPED: no scan is in flight, no mob is
// added, and the cycle simply retries next spawnInterval. The single-in-flight gate stays clear so
// a later cycle (when the pool frees up) can submit again.
func TestAsyncSpawnPoolOverloadSkips(t *testing.T) {
	loop, _, _ := newSpawnLoop(t)

	// Saturate the spawnPool: occupy all asyncSmallPoolSize workers with a blocking task each, so
	// the next Submit returns ErrPoolOverload (the pool is non-blocking).
	release := make(chan struct{})
	busy := make(chan struct{}, asyncSmallPoolSize)
	for i := 0; i < asyncSmallPoolSize; i++ {
		if !submitOrDrop(loop.spawnPool, func() {
			busy <- struct{}{}
			<-release // hold the worker until the test releases it
		}) {
			t.Fatal("failed to saturate the spawnPool for the overload test")
		}
	}
	// Wait until every worker is actually occupied (not merely queued) before the overload submit.
	for i := 0; i < asyncSmallPoolSize; i++ {
		select {
		case <-busy:
		case <-time.After(2 * time.Second):
			t.Fatal("the saturating tasks never started")
		}
	}

	before := loop.only().entities.len()
	loop.naturalSpawn() // the candidate-scan submit must be DROPPED on the saturated pool
	if loop.only().spawnScanPending {
		t.Fatal("on pool overload naturalSpawn must NOT mark a scan in flight (the cycle is skipped)")
	}
	if loop.only().entities.len() != before {
		t.Fatalf("an overloaded cycle must add no mob (it retries next interval): %d -> %d",
			before, loop.only().entities.len())
	}
	close(release) // free the saturating workers
}

// TestAsyncSpawnScanReadsSnapshot: the off-tick scan reads the IMMUTABLE solidity SNAPSHOT copied on
// the owner, NOT the live world. We build the snapshot over a standable column, then MUTATE the live
// world (clear the floor) — the snapshot's findStandableYIn still finds the standable Y (proving it
// reads the frozen copy), while the live findStandableY no longer does (proving the world actually
// changed). This is the load-bearing -race safety: the worker can never read a block the tick is
// concurrently mutating, because it reads only the copy (08-RESEARCH Pitfall 1).
func TestAsyncSpawnScanReadsSnapshot(t *testing.T) {
	loop, ch, floorY := newSpawnLoop(t)

	const px, pz = 8, 8
	// Copy the candidate column's solidity into the snapshot ON the owner (the production path).
	snap := loop.snapshotSpawnColumns([]spawnCandidatePick{{x: px, z: pz}}, floorY+1)

	// Sanity: against the snapshot the column is standable (solid below floorY+1, clear feet/head).
	if _, ok := findStandableYIn(snap, px, pz); !ok {
		t.Fatal("the snapshot of a floored column must report a standable Y")
	}

	// Now MUTATE the live world: clear the floor block under the candidate. The snapshot is frozen,
	// so it must be unaffected; the LIVE read must now reflect the change.
	ch.Sections[(floorY-dimMinY)>>4].SetBlock((floorY&15)<<8|(pz&15)<<4|(px&15), block.ToStateID[block.Air{}])

	// The live world no longer has a standable surface there (the floor block is gone).
	if _, ok := loop.findStandableY(px, pz, floorY+1); ok {
		t.Fatal("the live world should NOT be standable after clearing the floor block (test setup check)")
	}
	// But the SNAPSHOT — the value the off-tick worker reads — is unchanged: still standable. This
	// is what makes the off-tick scan -race clean: it reads the copy, never the mutated live world.
	if y, ok := findStandableYIn(snap, px, pz); !ok || y != floorY+1 {
		t.Fatalf("the snapshot must be IMMUTABLE: the off-tick scan still finds the standable Y (=%d) after the live world changed (got y=%d ok=%v)",
			floorY+1, y, ok)
	}
}
