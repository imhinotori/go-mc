package server

// lightning_test.go — port gates for LIGHTNING BOLT ON THUNDER (lightning.go / ServerLevel.tickThunder +
// LightningBolt.tick). These are PORT-EXACT gates: the strike-probability gate is exercised against a seed
// where the FIRST LegacyRandomSource.nextInt(100000) draw is 0 (the strike) vs a seed where it is not (no
// strike); the target selection, bolt lifespan, entity damage (5.0 lightning), and ground fire are
// asserted directly against the jar-derived behavior. The visual-only (skeleton-trap) branch is asserted
// to deal NO damage and start NO fire.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// thunderStrikeSeed is a LegacyRandomSource seed whose FIRST nextInt(100000) draw is 0 (found offline by a
// brute sweep) — it makes tickThunderChunk's strike gate PASS on the first column. Its SECOND draw
// (nextDouble, the skeleton-trap roll) is ~0.84, which is >= effectiveDifficulty(NORMAL)*0.01 == 0.015, so
// the bolt is NOT a trap (not visual-only) and DEALS damage — exactly the case the strike test wants.
const thunderStrikeSeed = 49501

// newThunderLoop builds a thundering world with a loaded, open-sky chunk over a solid floor at y=64, ready
// for the thunder strike. It returns the loop + manager. The weather is forced fully raining + thundering
// (isRaining/isThundering both true) so tickThunder's flag gate passes.
func newThunderLoop(t *testing.T, seed int64) (*TickLoop, *world.ChunkManager) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
	loop.spawnSurfaceY = 64 // open sky at/above y=64 (canSeeSkyAt / isRainingAt)

	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)

	// A solid stone floor across the chunk at y=64 so the MOTION_BLOCKING heightmap top is 64 (strike lands
	// at y=65, the first air above the floor) and the strike cell has a solid support (fire can survive).
	stone := block.ToStateID[block.Stone{}]
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			mgr.SetBlock(pk.Position{X: x, Y: 64, Z: z}, stone, dimMinY)
		}
	}

	// Force fully raining + thundering so tickThunder's flag gate passes.
	loop.weather.raining = true
	loop.weather.rainLevel = 1.0
	loop.weather.oRainLevel = 1.0
	loop.weather.thundering = true
	loop.weather.thunderLevel = 1.0
	loop.weather.oThunderLevel = 1.0
	if !loop.isRaining() || !loop.isThundering() {
		t.Fatal("precondition: world must be raining AND thundering")
	}
	return loop, mgr
}

// countBolts returns how many LightningBolt entities are in the global region store.
func countBolts(loop *TickLoop) int {
	n := 0
	for _, e := range loop.only().entities.byID {
		if e.isBolt {
			n++
		}
	}
	return n
}

// TestThunderGateSpawnsBoltOnHit: with the strike seed (first nextInt(100000)==0) a single tickThunder over
// the loaded chunk spawns exactly one LightningBolt at a valid open-sky target (y==65, the first air above
// the y=64 floor). The bolt carries START_LIFE==2 and a flashes count in [1,3], and is NOT visual-only
// (the trap roll missed).
func TestThunderGateSpawnsBoltOnHit(t *testing.T) {
	loop, _ := newThunderLoop(t, thunderStrikeSeed)

	if countBolts(loop) != 0 {
		t.Fatal("precondition: no bolt before the strike")
	}
	loop.tickThunder()

	if got := countBolts(loop); got != 1 {
		t.Fatalf("tickThunder with the strike seed spawned %d bolts, want exactly 1", got)
	}
	var bolt *Entity
	for _, e := range loop.only().entities.byID {
		if e.isBolt {
			bolt = e
		}
	}
	if bolt.typ != entity.LightningBolt.ID {
		t.Fatalf("spawned entity type = %d, want LightningBolt (%d)", bolt.typ, entity.LightningBolt.ID)
	}
	if bolt.boltLife != boltStartLife {
		t.Fatalf("bolt life = %d, want START_LIFE %d", bolt.boltLife, boltStartLife)
	}
	if bolt.boltFlashes < 1 || bolt.boltFlashes > 3 {
		t.Fatalf("bolt flashes = %d, want [1,3] (nextInt(3)+1)", bolt.boltFlashes)
	}
	if bolt.boltVisualOnly {
		t.Fatal("bolt must NOT be visual-only (the trap roll ~0.84 missed the 0.015 gate)")
	}
	// snapTo(Vec3.atBottomCenterOf(pos)): x+0.5, y, z+0.5; the strike lands on the surface top+1 == y=65.
	if bolt.y != 65 {
		t.Fatalf("bolt y = %v, want 65 (first air above the y=64 floor)", bolt.y)
	}
	if bolt.x != float64(int(bolt.x))+0.5 || bolt.z != float64(int(bolt.z))+0.5 {
		t.Fatalf("bolt pos (%v,%v) not block-bottom-centered (x+0.5,z+0.5)", bolt.x, bolt.z)
	}
}

// TestThunderGateNoStrikeWhenClear: a world that is raining but NOT thundering never strikes (the
// isThundering flag gate short-circuits before any per-column draw), so no bolt spawns even with the strike
// seed. Also asserts a non-strike seed under full thunder does not strike on the first tick.
func TestThunderGateNoStrikeWhenNotThundering(t *testing.T) {
	loop, _ := newThunderLoop(t, thunderStrikeSeed)
	// Drop thunder below the isThundering threshold (rain stays). No thunder -> no strike.
	loop.weather.thundering = false
	loop.weather.thunderLevel = 0.0
	loop.weather.oThunderLevel = 0.0
	if loop.isThundering() {
		t.Fatal("precondition: isThundering must be false")
	}

	loop.tickThunder()
	if got := countBolts(loop); got != 0 {
		t.Fatalf("tickThunder while not thundering spawned %d bolts, want 0", got)
	}
}

// TestThunderGateNoStrikeNonHitSeed: under full rain+thunder, a seed whose first nextInt(100000) draw is
// NOT 0 does not strike on the first tick (the per-column gate fails). Seed 1's first draw is non-zero.
func TestThunderGateNoStrikeNonHitSeed(t *testing.T) {
	// Confirm seed 1's first nextInt(100000) is non-zero (else pick another) — an independent reference.
	if levelgen.NewLegacyRandomSource(1).NextIntN(100000) == 0 {
		t.Skip("seed 1 unexpectedly strikes; test needs a non-hit seed")
	}
	loop, _ := newThunderLoop(t, 1)
	loop.tickThunder()
	if got := countBolts(loop); got != 0 {
		t.Fatalf("tickThunder with a non-hit seed spawned %d bolts, want 0", got)
	}
}

// TestFindLightningTargetHeightmap: with no entities near, findLightningTargetAround returns the
// MOTION_BLOCKING heightmap top+1 of the seed column (the first air above the surface). Over a y=64 stone
// floor that is y=65.
func TestFindLightningTargetHeightmap(t *testing.T) {
	loop, _ := newThunderLoop(t, 1)
	seed := pk.Position{X: 8, Y: 0, Z: 8}
	target := loop.findLightningTargetAround(seed)
	if target.X != 8 || target.Z != 8 {
		t.Fatalf("target (%d,%d) != seed column (8,8)", target.X, target.Z)
	}
	if target.Y != 65 {
		t.Fatalf("target Y = %d, want 65 (heightmap top 64 + 1)", target.Y)
	}
}

// TestFindLightningTargetPicksNearbyEntity: with an alive, sky-exposed mob in the tall search box above the
// column, findLightningTargetAround redirects the strike to that mob's block position (the vanilla
// "lightning hits the tallest/nearest entity" behavior — the single candidate is always picked).
func TestFindLightningTargetPicksNearbyEntity(t *testing.T) {
	loop, _ := newThunderLoop(t, 1)

	// A cow standing on the floor at (8,65,8), sky-exposed. It is the only candidate, so nextInt(1)==0
	// always selects it.
	cow := NewEntity(loop.idAlloc.AllocID(), entity.Cow, 8.5, 65, 8.5)
	cow.health = 10.0
	loop.only().entities.add(cow)

	seed := pk.Position{X: 8, Y: 0, Z: 8}
	target := loop.findLightningTargetAround(seed)
	if target.X != 8 || target.Y != 65 || target.Z != 8 {
		t.Fatalf("target %v, want the cow's block pos (8,65,8)", target)
	}
}

// TestBoltLifecycleDamagesMobAndLeavesFireThenDespawns: the full LightningBolt.tick lifecycle over a bolt
// spawned at (8.5,65,8.5): a cow within the ±3 box takes 5.0 lightning damage (and catches fire), a fire
// block is left on the ground at the strike cell (doFireTick + NORMAL difficulty), and the bolt discards
// after its life/flashes window (no bolt remains in the store).
func TestBoltLifecycleDamagesMobAndLeavesFireThenDespawns(t *testing.T) {
	loop, mgr := newThunderLoop(t, 1)

	// A cow next to the strike, within the ±3 damage box, alive.
	const cowHealth = 10.0
	cow := NewEntity(loop.idAlloc.AllocID(), entity.Cow, 9.0, 65, 9.0)
	cow.health = cowHealth
	loop.only().entities.add(cow)

	// Spawn a non-visual-only bolt at the surface strike cell (8,65,8 -> center 8.5,65,8.5).
	bolt := loop.spawnLightningBolt(pk.Position{X: 8, Y: 65, Z: 8}, false)
	if bolt.boltVisualOnly {
		t.Fatal("precondition: bolt must not be visual-only")
	}

	// Tick the bolt lifecycle to completion (it lives ~life+flashes ticks; 200 is a generous budget). Stop
	// once the bolt is gone.
	firstDamageHealth := float32(-1)
	for i := 0; i < 200; i++ {
		loop.tickLightning()
		if firstDamageHealth < 0 && cow.health < cowHealth {
			firstDamageHealth = cow.health
		}
		if countBolts(loop) == 0 {
			break
		}
	}

	// The bolt despawned within its lifespan.
	if got := countBolts(loop); got != 0 {
		t.Fatalf("bolt still present after 200 ticks, want despawned (life/flashes exhausted)")
	}
	// The cow took at least one 5.0 lightning hit (health dropped by 5.0 on the first damaging tick).
	if firstDamageHealth < 0 {
		t.Fatal("cow never took lightning damage from the bolt in range")
	}
	if firstDamageHealth != cowHealth-5.0 {
		t.Fatalf("first lightning hit left cow at %v, want %v (10.0 - 5.0 lightning damage)", firstDamageHealth, cowHealth-5.0)
	}
	// The cow caught fire (remainingFireTicks bumped by thunderHit).
	if cow.remainingFireTicks <= 0 {
		t.Fatal("cow should be on fire after a lightning strike (thunderHit remainingFireTicks++)")
	}
	// A fire block was left on the ground at the strike cell (air above the floor -> fire).
	fireState, ok := mgr.GetBlock(pk.Position{X: 8, Y: 65, Z: 8}, dimMinY)
	if !ok || fireState != block.DefaultStateID["minecraft:fire"] {
		t.Fatalf("strike cell (8,65,8) state = %d, want fire (%d)", fireState, block.DefaultStateID["minecraft:fire"])
	}
}

// TestVisualOnlyBoltDealsNoDamageNoFire: a visual-only (skeleton-trap) bolt renders but must deal NO entity
// damage and start NO fire — the visualOnly guard in both the damage loop and spawnFire.
func TestVisualOnlyBoltDealsNoDamageNoFire(t *testing.T) {
	loop, mgr := newThunderLoop(t, 1)

	const cowHealth = 10.0
	cow := NewEntity(loop.idAlloc.AllocID(), entity.Cow, 9.0, 65, 9.0)
	cow.health = cowHealth
	loop.only().entities.add(cow)

	// A VISUAL-ONLY bolt at the strike cell.
	loop.spawnLightningBolt(pk.Position{X: 8, Y: 65, Z: 8}, true)

	for i := 0; i < 200; i++ {
		loop.tickLightning()
		if countBolts(loop) == 0 {
			break
		}
	}

	if cow.health != cowHealth {
		t.Fatalf("visual-only bolt damaged the cow to %v, want no damage (%v)", cow.health, cowHealth)
	}
	if cow.remainingFireTicks != 0 {
		t.Fatalf("visual-only bolt set the cow on fire (remainingFireTicks=%d), want 0", cow.remainingFireTicks)
	}
	// No fire block placed (the strike cell stays air).
	state, ok := mgr.GetBlock(pk.Position{X: 8, Y: 65, Z: 8}, dimMinY)
	if ok && state == block.DefaultStateID["minecraft:fire"] {
		t.Fatal("visual-only bolt left fire on the ground, want none")
	}
}
