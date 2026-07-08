package server

// frog_test.go -- deterministic pins for the Frog (net.minecraft.world.entity.animal.frog.Frog, 1:1 javap
// this session). Verifies the spawn attributes (MOVEMENT_SPEED 1.0 / MAX_HEALTH 10 / ATTACK_DAMAGE 10 /
// STEP_HEIGHT 1.0) and the default (temperate) variant.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func frogLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestFrogSpawnDefaults: spawnFrog builds a frog rendering as entity.Frog.ID with the jar attributes,
// including the STEP_HEIGHT 1.0 override (a frog hops a full block) and the default temperate variant.
func TestFrogSpawnDefaults(t *testing.T) {
	loop, floorY := frogLoop(t)
	f := loop.spawnFrog(8.5, float64(floorY+1), 8.5, false)
	if f.typ != entity.Frog.ID {
		t.Fatalf("frog typ = %d, want entity.Frog.ID %d", f.typ, entity.Frog.ID)
	}
	if !f.isFrog {
		t.Fatal("frog not marked isFrog")
	}
	if math.Abs(float64(f.health)-10.0) > 1e-6 {
		t.Fatalf("frog health = %v, want 10.0 (MAX_HEALTH)", f.health)
	}
	if got := f.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("frog MAX_HEALTH = %v, want 10.0", got)
	}
	if got := f.getAttributeValue(attribute.MovementSpeed); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("frog MOVEMENT_SPEED = %v, want 1.0", got)
	}
	if got := f.getAttributeValue(attribute.AttackDamage); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("frog ATTACK_DAMAGE = %v, want 10.0", got)
	}
	if got := f.getAttributeValue(attribute.StepHeight); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("frog STEP_HEIGHT = %v, want 1.0 (override of the 0.6 base)", got)
	}
	if f.frogVariant != frogVariantTemperate {
		t.Fatalf("frog variant = %d, want temperate %d (DEFAULT_VARIANT)", f.frogVariant, frogVariantTemperate)
	}
	if f.ai == nil || f.ai.rng == nil {
		t.Fatal("frog has no minimal AI / rng")
	}
}
