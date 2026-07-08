package server

// copper_golem_test.go -- deterministic pins for the CopperGolem (net.minecraft.world.entity.animal.golem.
// CopperGolem, 1:1 javap this task). Verifies the spawn attributes (MOVEMENT_SPEED 0.2 / STEP_HEIGHT 1.0 /
// MAX_HEALTH 12) and the SIGNATURE oxidation stepper (updateWeathering stage advance on the gameTime schedule).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func copperGolemLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestCopperGolemSpawnDefaults: spawnCopperGolem renders entity.CopperGolem with the jar attributes.
func TestCopperGolemSpawnDefaults(t *testing.T) {
	loop, floorY := copperGolemLoop(t)
	c := loop.spawnCopperGolem(8.5, float64(floorY+1), 8.5)
	if c.typ != entity.CopperGolem.ID {
		t.Fatalf("copper golem typ = %d, want entity.CopperGolem.ID %d", c.typ, entity.CopperGolem.ID)
	}
	if !c.isCopperGolem {
		t.Fatal("copper golem not marked isCopperGolem")
	}
	if math.Abs(float64(c.health)-12.0) > 1e-6 {
		t.Fatalf("copper golem health = %v, want 12.0 (MAX_HEALTH)", c.health)
	}
	if got := c.getAttributeValue(attribute.MaxHealth); math.Abs(got-12.0) > 1e-9 {
		t.Fatalf("copper golem MAX_HEALTH = %v, want 12.0", got)
	}
	if got := c.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.20000000298023224) > 1e-12 {
		t.Fatalf("copper golem MOVEMENT_SPEED = %v, want 0.20000000298023224", got)
	}
	if got := c.getAttributeValue(attribute.StepHeight); math.Abs(got-1.0) > 1e-12 {
		t.Fatalf("copper golem STEP_HEIGHT = %v, want 1.0", got)
	}
	if c.copperGolemWeather != copperGolemWeatherUnaffected {
		t.Fatalf("copper golem initial weather = %d, want UNAFFECTED %d", c.copperGolemWeather, copperGolemWeatherUnaffected)
	}
	if c.copperGolemNextWeatherTick != copperGolemWeatherUninit {
		t.Fatalf("copper golem nextWeatherTick = %d, want -1 (uninitialized)", c.copperGolemNextWeatherTick)
	}
}

// TestCopperGolemOxidationStep: the first updateWeathering seeds the schedule; forcing the schedule due
// advances the weather state one stage (UNAFFECTED -> EXPOSED).
func TestCopperGolemOxidationStep(t *testing.T) {
	loop, floorY := copperGolemLoop(t)
	c := loop.spawnCopperGolem(8.5, float64(floorY+1), 8.5)

	// First step seeds nextWeatheringTick (gameTime + [504000,552000]); no stage change yet.
	loop.copperGolemAiStep(c)
	if c.copperGolemNextWeatherTick == copperGolemWeatherUninit {
		t.Fatal("first updateWeathering must seed nextWeatheringTick off the -1 sentinel")
	}
	if c.copperGolemWeather != copperGolemWeatherUnaffected {
		t.Fatalf("weather changed on the seeding tick: %d, want UNAFFECTED", c.copperGolemWeather)
	}
	seeded := c.copperGolemNextWeatherTick
	if seeded < loop.gametime+copperGolemWeatheringFrom || seeded > loop.gametime+copperGolemWeatheringTo {
		t.Fatalf("seeded nextWeatheringTick = %d, want in gameTime+[504000,552000]", seeded)
	}

	// Force the schedule due: the next step advances UNAFFECTED -> EXPOSED.
	c.copperGolemNextWeatherTick = loop.gametime
	loop.copperGolemAiStep(c)
	if c.copperGolemWeather != copperGolemWeatherExposed {
		t.Fatalf("weather after due schedule = %d, want EXPOSED %d", c.copperGolemWeather, copperGolemWeatherExposed)
	}
}
