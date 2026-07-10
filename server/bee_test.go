package server

// bee_test.go -- deterministic pins for the Bee (net.minecraft.world.entity.animal.bee.Bee, 1:1 javap
// this session). Verifies the spawn attributes (MAX_HEALTH 10 / FLYING_SPEED 0.6 / MOVEMENT_SPEED 0.3 /
// ATTACK_DAMAGE 2 / FOLLOW_RANGE 16) and the SIGNATURE sting-then-die-over-1200-ticks countdown.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// beeLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick bees.
func beeLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// beeLoopMgr is beeLoop but also hands back the chunk manager (needed to flood a cell for the drown test).
func beeLoopMgr(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// TestBeeSpawnDefaults: spawnBee builds a bee rendering as entity.Bee.ID with the jar attributes.
func TestBeeSpawnDefaults(t *testing.T) {
	loop, floorY := beeLoop(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	if b.typ != entity.Bee.ID {
		t.Fatalf("bee typ = %d, want entity.Bee.ID %d", b.typ, entity.Bee.ID)
	}
	if !b.isBee {
		t.Fatal("bee not marked isBee")
	}
	if math.Abs(float64(b.health)-10.0) > 1e-6 {
		t.Fatalf("bee health = %v, want 10.0 (MAX_HEALTH)", b.health)
	}
	if got := b.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("bee MAX_HEALTH = %v, want 10.0", got)
	}
	if got := b.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.30000001192092896) > 1e-12 {
		t.Fatalf("bee MOVEMENT_SPEED = %v, want 0.30000001192092896", got)
	}
	if got := b.getAttributeValue(attribute.FlyingSpeed); math.Abs(got-0.6000000238418579) > 1e-12 {
		t.Fatalf("bee FLYING_SPEED = %v, want 0.6000000238418579", got)
	}
	if got := b.getAttributeValue(attribute.AttackDamage); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("bee ATTACK_DAMAGE = %v, want 2.0", got)
	}
	if got := b.getAttributeValue(attribute.FollowRange); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("bee FOLLOW_RANGE = %v, want 16.0 (createMobAttributes, no override)", got)
	}
	if b.ai == nil || b.ai.rng == nil {
		t.Fatal("bee has no minimal AI / rng")
	}
}

// TestBeeStingDeathCountdown: a bee that has stung eventually dies from its own sting -- beeAiStep only
// draws/damages on the (% 5) cadence, and the generic self-damage equals current health (a kill). Drive
// the countdown far enough that the rising-probability roll fires and the bee dies.
func TestBeeStingDeathCountdown(t *testing.T) {
	loop, floorY := beeLoop(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	b.health = 10.0
	b.beeHasStung = true

	// A never-stung bee (fresh) draws nothing; a stung bee counts up. Off-cadence ticks are pure no-ops.
	loop.beeAiStep(b) // timeSinceSting 1 (not % 5) -> no roll
	if b.beeTimeSinceSting != 1 {
		t.Fatalf("timeSinceSting = %d after one tick, want 1", b.beeTimeSinceSting)
	}
	if b.health != 10.0 {
		t.Fatalf("bee took damage off the %% 5 cadence: health = %v", b.health)
	}

	// Drive until the bee dies (the death probability rises as timeSinceSting -> 1200). Bounded loop.
	died := false
	for i := 0; i < 20000 && !died; i++ {
		loop.beeAiStep(b)
		if b.dead || b.health <= 0 {
			died = true
		}
	}
	if !died {
		t.Fatalf("bee never died from its sting after 20000 ticks (timeSinceSting=%d, health=%v)", b.beeTimeSinceSting, b.health)
	}
}


// TestBeeUnStungDrowns pins the customServerAiStep fix: the underwater-drown block runs UNCONDITIONALLY of
// sting state. An un-stung bee submerged in water increments underWaterTicks every tick and, once it
// exceeds 20, takes 1.0F drown damage EVERY tick until it dies. (Regression: the old beeAiStep returned
// early for a never-stung bee, so it never drowned.) Cite Bee.customServerAiStep.
func TestBeeUnStungDrowns(t *testing.T) {
	loop, mgr, floorY := beeLoopMgr(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	b.health = 10.0
	if b.beeHasStung {
		t.Fatal("fresh bee should not have stung")
	}
	// Flood the bee's feet cell so entityInWater is true.
	water := block.DefaultStateID["minecraft:water"]
	mgr.SetBlock(pk.Position{X: 8, Y: floorY + 1, Z: 8}, water, dimMinY)
	if !loop.entityInWater(b) {
		t.Fatal("submerged bee not detected in water")
	}

	// Ticks 1..20: underWaterTicks climbs but never exceeds 20 -> NO drown damage yet.
	for i := 1; i <= beeDrownThreshold; i++ {
		loop.beeAiStep(b)
		if b.beeUnderWaterTicks != i {
			t.Fatalf("underWaterTicks = %d after %d ticks, want %d", b.beeUnderWaterTicks, i, i)
		}
	}
	if b.health != 10.0 {
		t.Fatalf("bee took drown damage at underWaterTicks<=20: health = %v, want 10.0", b.health)
	}
	// Tick 21: underWaterTicks == 21 > 20 -> first 1.0F drown hit lands.
	loop.beeAiStep(b)
	if b.beeUnderWaterTicks != beeDrownThreshold+1 {
		t.Fatalf("underWaterTicks = %d, want %d", b.beeUnderWaterTicks, beeDrownThreshold+1)
	}
	if math.Abs(float64(10.0-b.health)-beeDrownDamage) > 1e-6 {
		t.Fatalf("first drown tick dealt %v, want %v (hurtServer(drown(), 1.0F))", 10.0-b.health, beeDrownDamage)
	}
	// Keep ticking: the un-stung bee drowns to death (no sting ever set). Drown deals 1.0F but the
	// invulnerableTime i-frame gate (LivingEntity.hurtServer) admits an equal-magnitude re-hit only once
	// the grace window falls out of its upper half, so ~1 HP per ~20-tick window -- a generous bound.
	died := false
	for i := 0; i < 5000 && !died; i++ {
		b.invulnerableTime = 0 // the main tick pipeline decrements the i-frame window each tick; do it here
		loop.beeAiStep(b)
		if b.dead || b.health <= 0 {
			died = true
		}
	}
	if !died {
		t.Fatalf("un-stung submerged bee never drowned (health=%v, underWaterTicks=%d)", b.health, b.beeUnderWaterTicks)
	}
	if b.beeHasStung {
		t.Fatal("bee should have drowned WITHOUT ever stinging")
	}
}

// TestBeeDryUnStungNoDamage: a dry, never-stung bee is a pure no-op each tick -- underWaterTicks stays 0 and
// it takes no damage (the water reset branch + the never-stung early return).
func TestBeeDryUnStungNoDamage(t *testing.T) {
	loop, floorY := beeLoop(t)
	b := loop.spawnBee(8.5, float64(floorY+1), 8.5, false)
	b.health = 10.0
	for i := 0; i < 100; i++ {
		loop.beeAiStep(b)
	}
	if b.beeUnderWaterTicks != 0 {
		t.Fatalf("dry bee underWaterTicks = %d, want 0", b.beeUnderWaterTicks)
	}
	if b.health != 10.0 {
		t.Fatalf("dry un-stung bee took damage: health = %v, want 10.0", b.health)
	}
}
