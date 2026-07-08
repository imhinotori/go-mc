package server

// armadillo_test.go -- deterministic pins for the Armadillo (net.minecraft.world.entity.animal.armadillo.
// Armadillo, 1:1 javap this task). Verifies the spawn attributes (MAX_HEALTH 12 / MOVEMENT_SPEED 0.14) and
// the SIGNATURE roll-up-on-threat state machine + the scute-shed countdown.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func armadilloLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestArmadilloSpawnDefaults: spawnArmadillo renders entity.Armadillo with the jar attributes.
func TestArmadilloSpawnDefaults(t *testing.T) {
	loop, floorY := armadilloLoop(t)
	a := loop.spawnArmadillo(8.5, float64(floorY+1), 8.5, false)
	if a.typ != entity.Armadillo.ID {
		t.Fatalf("armadillo typ = %d, want entity.Armadillo.ID %d", a.typ, entity.Armadillo.ID)
	}
	if !a.isArmadillo {
		t.Fatal("armadillo not marked isArmadillo")
	}
	if math.Abs(float64(a.health)-12.0) > 1e-6 {
		t.Fatalf("armadillo health = %v, want 12.0 (MAX_HEALTH)", a.health)
	}
	if got := a.getAttributeValue(attribute.MaxHealth); math.Abs(got-12.0) > 1e-9 {
		t.Fatalf("armadillo MAX_HEALTH = %v, want 12.0", got)
	}
	if got := a.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.14) > 1e-12 {
		t.Fatalf("armadillo MOVEMENT_SPEED = %v, want 0.14", got)
	}
	if a.armadilloState != armadilloStateIdle {
		t.Fatalf("armadillo initial state = %d, want IDLE %d", a.armadilloState, armadilloStateIdle)
	}
	if a.armadilloScuteTime < armadilloScuteDropWindow || a.armadilloScuteTime > 2*armadilloScuteDropWindow {
		t.Fatalf("armadillo scuteTime = %d, want in [6000,12000] (pickNextScuteDropTime)", a.armadilloScuteTime)
	}
}

// TestArmadilloRollsUpOnThreat: a sprinting player nearby drives the armadillo IDLE -> ROLLING -> SCARED;
// when the threat leaves it eventually UNROLLS back to IDLE.
func TestArmadilloRollsUpOnThreat(t *testing.T) {
	loop, floorY := armadilloLoop(t)
	a := loop.spawnArmadillo(8.5, float64(floorY+1), 8.5, false)

	// A sprinting player right next to the armadillo is a threat (isScaredBy).
	p := &tickPlayer{x: 9.0, y: float64(floorY + 1), z: 8.5, entityID: 7001, sprinting: true}
	loop.players = append(loop.players, p)

	// First tick: the scan detects the threat and rolls the armadillo up (ROLLING).
	loop.armadilloAiStep(a)
	if a.armadilloState != armadilloStateRolling {
		t.Fatalf("state after threat scan = %d, want ROLLING %d", a.armadilloState, armadilloStateRolling)
	}
	// Drive past ROLLING.animationDuration (10) -> SCARED.
	for i := 0; i < armadilloRollingAnimDur+2; i++ {
		loop.armadilloAiStep(a)
	}
	if a.armadilloState != armadilloStateScared {
		t.Fatalf("state after roll animation = %d, want SCARED %d", a.armadilloState, armadilloStateScared)
	}

	// Player stops being a threat (not sprinting, not riding).
	p.sprinting = false
	// Drive past SCARED.animationDuration (50) with no threat -> UNROLLING -> IDLE.
	died := false
	for i := 0; i < armadilloScaredAnimDur+armadilloUnrollingAnimDur+5 && !died; i++ {
		loop.armadilloAiStep(a)
		if a.armadilloState == armadilloStateIdle {
			died = true
		}
	}
	if a.armadilloState != armadilloStateIdle {
		t.Fatalf("armadillo did not unroll back to IDLE after the threat left: state=%d", a.armadilloState)
	}
}

// TestArmadilloScuteShed: driving scuteTime to 0 resets it to a fresh pickNextScuteDropTime window.
func TestArmadilloScuteShed(t *testing.T) {
	loop, floorY := armadilloLoop(t)
	a := loop.spawnArmadillo(8.5, float64(floorY+1), 8.5, false)
	a.armadilloScuteTime = 1 // one tick from a shed
	loop.armadilloAiStep(a)
	if a.armadilloScuteTime < armadilloScuteDropWindow {
		t.Fatalf("scuteTime after shed = %d, want reset to >= %d", a.armadilloScuteTime, armadilloScuteDropWindow)
	}
}
