package server

// allay_test.go -- deterministic pins for the Allay (net.minecraft.world.entity.animal.allay.Allay, 1:1
// javap this session). Verifies the spawn attributes (MAX_HEALTH 20 / MOVEMENT_SPEED 0.10000000149011612 /
// FLYING_SPEED 0.10000000149011612 / ATTACK_DAMAGE 2). Allay is NOT Ageable (a misc PathfinderMob).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func allayLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestAllaySpawnDefaults: spawnAllay builds an allay rendering as entity.Allay.ID with the jar attributes
// (MAX_HEALTH 20, FLYING_SPEED + MOVEMENT_SPEED 0.1 bit-exact, ATTACK_DAMAGE 2).
func TestAllaySpawnDefaults(t *testing.T) {
	loop, floorY := allayLoop(t)
	a := loop.spawnAllay(8.5, float64(floorY+1), 8.5)
	if a.typ != entity.Allay.ID {
		t.Fatalf("allay typ = %d, want entity.Allay.ID %d", a.typ, entity.Allay.ID)
	}
	if !a.isAllay {
		t.Fatal("allay not marked isAllay")
	}
	if math.Abs(float64(a.health)-20.0) > 1e-6 {
		t.Fatalf("allay health = %v, want 20.0 (MAX_HEALTH)", a.health)
	}
	if got := a.getAttributeValue(attribute.MaxHealth); math.Abs(got-20.0) > 1e-9 {
		t.Fatalf("allay MAX_HEALTH = %v, want 20.0", got)
	}
	if got := a.getAttributeValue(attribute.MovementSpeed); math.Float64bits(got) != math.Float64bits(0.10000000149011612) {
		t.Fatalf("allay MOVEMENT_SPEED = %v, want 0.10000000149011612 (bit-exact)", got)
	}
	if got := a.getAttributeValue(attribute.FlyingSpeed); math.Float64bits(got) != math.Float64bits(0.10000000149011612) {
		t.Fatalf("allay FLYING_SPEED = %v, want 0.10000000149011612 (bit-exact)", got)
	}
	if got := a.getAttributeValue(attribute.AttackDamage); math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("allay ATTACK_DAMAGE = %v, want 2.0", got)
	}
	if a.ai == nil || a.ai.rng == nil {
		t.Fatal("allay has no minimal AI / rng")
	}
}

// TestAllayAiStepRegen: Allay.aiStep heals 1.0 every 10 ticks (tickCount % 10 == 0) up to MAX_HEALTH.
// A damaged allay recovers 1 HP on each aiStep whose aiTickCount is a multiple of 10 and does NOT
// overheal past 20.0. Cite Allay.aiStep + LivingEntity.heal.
func TestAllayAiStepRegen(t *testing.T) {
	loop, floorY := allayLoop(t)
	a := loop.spawnAllay(8.5, float64(floorY+1), 8.5)
	a.health = 5.0
	// aiTickCount % 10 != 0 -> no heal.
	a.ai.aiTickCount = 7
	loop.allayAiStep(a)
	if a.health != 5.0 {
		t.Fatalf("allay healed off-cadence: health = %v, want 5.0", a.health)
	}
	// aiTickCount % 10 == 0 -> heal 1.0.
	a.ai.aiTickCount = 10
	loop.allayAiStep(a)
	if a.health != 6.0 {
		t.Fatalf("allay heal = %v, want 6.0 (5.0 + 1.0)", a.health)
	}
	// clamp to MAX_HEALTH 20.0 (no overheal).
	a.health = 19.5
	a.ai.aiTickCount = 20
	loop.allayAiStep(a)
	if math.Abs(float64(a.health)-20.0) > 1e-6 {
		t.Fatalf("allay overheal: health = %v, want clamped 20.0", a.health)
	}
}
