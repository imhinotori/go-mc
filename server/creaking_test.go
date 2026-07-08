package server

// creaking_test.go -- deterministic pins for the Creaking (net.minecraft.world.entity.monster.creaking.
// Creaking, 1:1 javap this task). Verifies the spawn attributes (MAX_HEALTH 1 / MOVEMENT_SPEED 0.4 /
// ATTACK_DAMAGE 3 / FOLLOW_RANGE 32 / STEP_HEIGHT 1.0625) and the SIGNATURE observed-freeze (checkCanMove).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func creakingLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestCreakingSpawnDefaults: spawnCreaking renders entity.Creaking with the jar attributes.
func TestCreakingSpawnDefaults(t *testing.T) {
	loop, floorY := creakingLoop(t)
	c := loop.spawnCreaking(8.5, float64(floorY+1), 8.5)
	if c.typ != entity.Creaking.ID {
		t.Fatalf("creaking typ = %d, want entity.Creaking.ID %d", c.typ, entity.Creaking.ID)
	}
	if !c.isCreaking {
		t.Fatal("creaking not marked isCreaking")
	}
	if math.Abs(float64(c.health)-1.0) > 1e-6 {
		t.Fatalf("creaking health = %v, want 1.0 (MAX_HEALTH)", c.health)
	}
	if got := c.getAttributeValue(attribute.MaxHealth); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("creaking MAX_HEALTH = %v, want 1.0", got)
	}
	if got := c.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.4000000059604645) > 1e-12 {
		t.Fatalf("creaking MOVEMENT_SPEED = %v, want 0.4000000059604645", got)
	}
	if got := c.getAttributeValue(attribute.AttackDamage); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("creaking ATTACK_DAMAGE = %v, want 3.0", got)
	}
	if got := c.getAttributeValue(attribute.FollowRange); math.Abs(got-32.0) > 1e-9 {
		t.Fatalf("creaking FOLLOW_RANGE = %v, want 32.0", got)
	}
	if got := c.getAttributeValue(attribute.StepHeight); math.Abs(got-1.0625) > 1e-12 {
		t.Fatalf("creaking STEP_HEIGHT = %v, want 1.0625", got)
	}
	if !c.creakingCanMove {
		t.Fatal("creaking CAN_MOVE must default true")
	}
}

// TestCreakingFreezesWhenObserved: a player LOOKING at the creaking (within 12 blocks) activates it and
// FREEZES it (canMove -> false); a player looking AWAY leaves it free to move.
func TestCreakingFreezesWhenObserved(t *testing.T) {
	loop, floorY := creakingLoop(t)
	c := loop.spawnCreaking(8.5, float64(floorY+1), 18.5)

	// A player 12 blocks away looking directly at the creaking.
	looker := addStaringPlayerAt(loop, 7101, c, 8.5, float64(floorY+1), 8.5)
	loop.creakingAiStep(c)
	if c.creakingCanMove {
		t.Fatal("creaking must FREEZE (canMove=false) while a player looks at it")
	}
	if !c.creakingActive {
		t.Fatal("creaking must ACTIVATE when caught unobserved within 12 blocks")
	}

	// Turn the looker away (yaw 180 from the creaking) -> unobserved -> can move again.
	looker.yaw += 180
	loop.creakingAiStep(c)
	if !c.creakingCanMove {
		t.Fatal("creaking must be free to move (canMove=true) when no player looks at it")
	}
}

// addStaringPlayerAt registers a player at (x,y,z) whose yaw/pitch aim directly at the creaking eye.
func addStaringPlayerAt(loop *TickLoop, entityID int32, e *Entity, x, y, z float64) *tickPlayer {
	yaw, pitch := lookAnglesToward(x, y+playerStandingEyeHeight, z, e.x, e.y+e.height*0.85, e.z)
	p := &tickPlayer{x: x, y: y, z: z, entityID: entityID, yaw: yaw, pitch: pitch}
	loop.players = append(loop.players, p)
	return p
}
