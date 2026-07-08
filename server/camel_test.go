package server

// camel_test.go -- deterministic pins for the Camel (net.minecraft.world.entity.animal.camel.Camel, 1:1
// javap this session). Verifies the spawn attributes (MAX_HEALTH 32 / MOVEMENT_SPEED 0.09000000357627869 /
// STEP_HEIGHT 1.5 / SAFE_FALL_DISTANCE 6.0) folded from createBaseHorseAttributes + the Camel overrides.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func camelLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestCamelSpawnDefaults: spawnCamel builds a camel rendering as entity.Camel.ID with the jar attributes,
// including the STEP_HEIGHT 1.5 tall-block step-up and the SAFE_FALL_DISTANCE 6.0 from the horse base.
func TestCamelSpawnDefaults(t *testing.T) {
	loop, floorY := camelLoop(t)
	c := loop.spawnCamel(8.5, float64(floorY+1), 8.5, false)
	if c.typ != entity.Camel.ID {
		t.Fatalf("camel typ = %d, want entity.Camel.ID %d", c.typ, entity.Camel.ID)
	}
	if !c.isCamel {
		t.Fatal("camel not marked isCamel")
	}
	if math.Abs(float64(c.health)-32.0) > 1e-6 {
		t.Fatalf("camel health = %v, want 32.0 (MAX_HEALTH)", c.health)
	}
	if got := c.getAttributeValue(attribute.MaxHealth); math.Abs(got-32.0) > 1e-9 {
		t.Fatalf("camel MAX_HEALTH = %v, want 32.0", got)
	}
	if got := c.getAttributeValue(attribute.MovementSpeed); math.Float64bits(got) != math.Float64bits(0.09000000357627869) {
		t.Fatalf("camel MOVEMENT_SPEED = %v, want 0.09000000357627869 (bit-exact)", got)
	}
	if got := c.getAttributeValue(attribute.StepHeight); math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("camel STEP_HEIGHT = %v, want 1.5 (override of the 0.6 base)", got)
	}
	if got := c.getAttributeValue(attribute.SafeFallDistance); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("camel SAFE_FALL_DISTANCE = %v, want 6.0 (horse base)", got)
	}
	if c.ai == nil || c.ai.rng == nil {
		t.Fatal("camel has no minimal AI / rng")
	}
}
