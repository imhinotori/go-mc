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
