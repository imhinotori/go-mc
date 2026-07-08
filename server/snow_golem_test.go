package server

// snow_golem_test.go -- deterministic pins for the SnowGolem (net.minecraft.world.entity.animal.golem.
// SnowGolem, 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 4 / MOVEMENT_SPEED 0.2),
// the default pumpkin state + shear, and that the snow-trail aiStep leaves snow layers on a solid floor.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

func snowGolemLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestSnowGolemSpawnDefaults: spawnSnowGolem builds a golem rendering as entity.SnowGolem.ID with the jar
// attributes (MAX_HEALTH 4, MOVEMENT_SPEED 0.2) and the default pumpkin.
func TestSnowGolemSpawnDefaults(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	if g.typ != entity.SnowGolem.ID {
		t.Fatalf("snow_golem typ = %d, want entity.SnowGolem.ID %d", g.typ, entity.SnowGolem.ID)
	}
	if !g.isSnowGolem {
		t.Fatal("snow_golem not marked isSnowGolem")
	}
	if math.Abs(float64(g.health)-4.0) > 1e-6 {
		t.Fatalf("snow_golem health = %v, want 4.0 (MAX_HEALTH)", g.health)
	}
	if got := g.getAttributeValue(attribute.MaxHealth); math.Abs(got-4.0) > 1e-9 {
		t.Fatalf("snow_golem MAX_HEALTH = %v, want 4.0", got)
	}
	if got := g.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.20000000298023224) > 1e-12 {
		t.Fatalf("snow_golem MOVEMENT_SPEED = %v, want 0.20000000298023224", got)
	}
	if !g.snowGolemPumpkin {
		t.Fatal("snow_golem should spawn wearing a pumpkin (DATA_PUMPKIN_ID default true)")
	}
}

// TestSnowGolemShear: readyForShearing == isAlive() && hasPumpkin(); shear clears the pumpkin once and is
// idempotent afterward (a pumpkin-less golem cannot be sheared again). Cite SnowGolem.shear.
func TestSnowGolemShear(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	if !loop.snowGolemShear(g) {
		t.Fatal("first shear should succeed (golem has a pumpkin)")
	}
	if g.snowGolemPumpkin {
		t.Fatal("shear should clear the pumpkin")
	}
	if loop.snowGolemShear(g) {
		t.Fatal("second shear should fail (no pumpkin to remove)")
	}
}

// TestSnowGolemSnowTrail: with MOB_GRIEFING on, snowGolemAiStep leaves at least one snow layer around the
// golem's feet where the block is air with a solid floor below. Cite SnowGolem.aiStep.
func TestSnowGolemSnowTrail(t *testing.T) {
	loop, floorY := snowGolemLoop(t)
	// Stand the golem ON the floor (feet at floorY+1) so the four offsets land at floorY+1 (air) with the
	// solid floor at floorY below -> canSurvive.
	g := loop.spawnSnowGolem(8.5, float64(floorY+1), 8.5)
	snow, ok := block.DefaultStateID["minecraft:snow"]
	if !ok {
		t.Skip("no minecraft:snow default state in this build")
	}
	loop.snowGolemAiStep(g)
	// Check the four candidate offsets; at least one should now be a snow layer.
	placed := 0
	for i := 0; i < 4; i++ {
		bx := mthFloor(8.5 + float64((i%2)*2-1)*snowGolemTrailOffset)
		by := mthFloor(float64(floorY + 1))
		bz := mthFloor(8.5 + float64((i/2%2)*2-1)*snowGolemTrailOffset)
		if s, _ := loop.world().GetBlock(pk.Position{X: bx, Y: by, Z: bz}, dimMinY); s == snow {
			placed++
		}
	}
	if placed == 0 {
		t.Fatal("snowGolemAiStep placed no snow trail on a solid floor")
	}
}
