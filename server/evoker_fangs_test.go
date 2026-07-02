package server

// evoker_fangs_test.go -- deterministic pins for the EvokerFangs entity (net.minecraft.world.entity.
// projectile.EvokerFangs, 1:1 CFR this session). Verifies the warmup countdown, the attack fires at
// warmupDelayTicks==-8 (6.0 magic damage to a LivingEntity in the inflated box), the ~22-tick lifetime
// despawn, the owner-skip guard, and that a warmup delay staggers the attack tick.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// fangsLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick fangs.
func fangsLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestEvokerFangsSpawnDefaults: spawnEvokerFangs builds a fangs rendering as entity.EvokerFangs.ID with
// the vanilla defaults (lifeTicks 22, the warmup arg, the owner, the yaw from the radians arg).
func TestEvokerFangsSpawnDefaults(t *testing.T) {
	loop, floorY := fangsLoop(t)
	f := loop.spawnEvokerFangs(4242, 8.5, float64(floorY+1), 8.5, 0.0, 0)
	if f.typ != entity.EvokerFangs.ID {
		t.Fatalf("fangs typ = %d, want entity.EvokerFangs.ID %d", f.typ, entity.EvokerFangs.ID)
	}
	if !f.isFangs {
		t.Fatal("fangs not marked isFangs")
	}
	if f.fangsLifeTicks != fangsDefaultLifeTicks {
		t.Fatalf("fangs lifeTicks = %d, want %d", f.fangsLifeTicks, fangsDefaultLifeTicks)
	}
	if f.fangsOwnerID != 4242 {
		t.Fatalf("fangs owner = %d, want 4242", f.fangsOwnerID)
	}
	if _, ok := loop.only().entities.byID[f.id]; !ok {
		t.Fatal("fangs was not added to the store")
	}
}

// TestEvokerFangsWarmupThenAttack: a fangs with warmup 0 counts down; at the tick where warmupDelayTicks
// reaches -8 it deals exactly 6.0 magic damage to a player standing on it. Before -8 it deals nothing.
func TestEvokerFangsWarmupThenAttack(t *testing.T) {
	loop, floorY := fangsLoop(t)
	// Player standing exactly where the fangs erupt (inside the inflated box).
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 4242)
	f := loop.spawnEvokerFangs(0, 8.5, float64(floorY+1), 8.5, 0.0, 0)

	// warmupDelayTicks starts at 0; each tickFang does --warmup. It reaches -8 on the 8th tick after it
	// first goes below 0 (tick1: -1 ... tick8: -8). No damage until then.
	start := p.health
	for i := 0; i < 7; i++ {
		loop.tickFang(f)
	}
	if p.health != start {
		t.Fatalf("player took damage before the -8 attack tick (health %v -> %v)", start, p.health)
	}
	// The 8th tickFang brings warmup to -8 and fires the bite.
	loop.tickFang(f)
	if got := start - p.health; math.Abs(float64(got)-fangsDamage) > 1e-6 {
		t.Fatalf("fangs dealt %v damage, want %v (6.0 magic)", got, fangsDamage)
	}
	if !f.fangsSentSpikeEvent {
		t.Fatal("fangs did not latch the spike event on the attack tick")
	}
}

// TestEvokerFangsAttacksOnce: the fangs bite fires exactly once (the -8 tick), not every tick after.
func TestEvokerFangsAttacksOnce(t *testing.T) {
	loop, floorY := fangsLoop(t)
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 4242)
	f := loop.spawnEvokerFangs(0, 8.5, float64(floorY+1), 8.5, 0.0, 0)
	// Give the player a big health pool so multiple hits would be visible if they (wrongly) happened.
	p.health = 200
	start := p.health

	// Tick through the whole life (warmup 8 + lifeTicks 22 -> ~30 ticks, plus slack).
	for i := 0; i < 40; i++ {
		if _, ok := loop.only().entities.byID[f.id]; !ok {
			break
		}
		loop.tickFang(f)
	}
	if got := start - p.health; math.Abs(float64(got)-fangsDamage) > 1e-6 {
		t.Fatalf("fangs dealt %v total damage over its life, want exactly one 6.0 hit", got)
	}
}

// TestEvokerFangsDespawns: a fangs discards from the store once its lifeTicks run out (warmup 8 + 22 life).
func TestEvokerFangsDespawns(t *testing.T) {
	loop, floorY := fangsLoop(t)
	f := loop.spawnEvokerFangs(0, 8.5, float64(floorY+1), 8.5, 0.0, 0)
	// warmup drops below 0 after 1 tick; then lifeTicks (22) count down; discard at < 0. Tick generously.
	for i := 0; i < 60; i++ {
		if _, ok := loop.only().entities.byID[f.id]; !ok {
			return // despawned -- pass
		}
		loop.tickFang(f)
	}
	t.Fatal("fangs never despawned within its lifetime")
}

// TestEvokerFangsSkipsOwner: the fangs never bites its own owner (the casting evoker). A player who IS the
// owner id takes no damage.
func TestEvokerFangsSkipsOwner(t *testing.T) {
	loop, floorY := fangsLoop(t)
	// The fangs' owner is a MOB id, never a player id -- but we assert the guard by making a mob owner sit
	// on the fangs and confirming it is not bitten. Spawn a lightweight vex as the owner-mob standing here.
	owner := loop.spawnVex(0, 8.5, float64(floorY+1), 8.5, 8, floorY+1, 8, 600)
	ownerHealth := owner.health
	f := loop.spawnEvokerFangs(owner.id, 8.5, float64(floorY+1), 8.5, 0.0, 0)

	for i := 0; i < 9; i++ {
		loop.tickFang(f)
	}
	if owner.health != ownerHealth {
		t.Fatalf("fangs bit its own owner (health %v -> %v)", ownerHealth, owner.health)
	}
}

// TestEvokerFangsWarmupStagger: a fangs spawned with a positive warmupDelay fires LATER than a 0-warmup one
// (the ARC/LINE stagger). A warmup of 3 fires 3 ticks later than a warmup of 0.
func TestEvokerFangsWarmupStagger(t *testing.T) {
	loop, floorY := fangsLoop(t)
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 8.5, 4242)
	p.health = 200
	f := loop.spawnEvokerFangs(0, 8.5, float64(floorY+1), 8.5, 0.0, 3)
	// With warmup 3, warmupDelayTicks goes 3->2->1->0->-1...-8: it reaches -8 on the 11th tickFang (3 extra
	// warmup ticks + 8). Assert no damage at tick 8 (a 0-warmup would have bitten) and damage by tick 11.
	start := p.health
	for i := 0; i < 8; i++ {
		loop.tickFang(f)
	}
	if p.health != start {
		t.Fatalf("staggered fangs bit too early (health %v -> %v)", start, p.health)
	}
	for i := 0; i < 3; i++ {
		loop.tickFang(f)
	}
	if got := start - p.health; math.Abs(float64(got)-fangsDamage) > 1e-6 {
		t.Fatalf("staggered fangs dealt %v, want %v after the extra 3-tick warmup", got, fangsDamage)
	}
}
