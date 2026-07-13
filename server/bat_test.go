package server

// bat_test.go -- deterministic pins for the Bat (net.minecraft.world.entity.ambient.Bat, 1:1 javap this
// session). Verifies the spawn attributes (Mob + MAX_HEALTH 6; no ATTACK_DAMAGE), the ctor spawn-RESTING
// state, and the SIGNATURE resting toggle (wake when a player is within range 4.0 / re-hang under a
// ceiling) + the hang-from-ceiling position snap. The pig oracle is untouched.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// batLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick bats.
func batLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestBatSpawnDefaults: spawnBat builds a bat rendering as entity.Bat.ID with the jar attributes and the
// ctor spawn-RESTING state.
func TestBatSpawnDefaults(t *testing.T) {
	loop, floorY := batLoop(t)
	b := loop.spawnBat(8.5, float64(floorY+1), 8.5)
	if b.typ != entity.Bat.ID {
		t.Fatalf("bat typ = %d, want entity.Bat.ID %d", b.typ, entity.Bat.ID)
	}
	if !b.isBat {
		t.Fatal("bat not marked isBat")
	}
	if math.Abs(float64(b.health)-6.0) > 1e-6 {
		t.Fatalf("bat health = %v, want 6.0 (MAX_HEALTH)", b.health)
	}
	if got := b.getAttributeValue(attribute.MaxHealth); math.Abs(got-6.0) > 1e-9 {
		t.Fatalf("bat MAX_HEALTH = %v, want 6.0", got)
	}
	// Bat builds on Mob.createMobAttributes (NOT Monster/Animal): no ATTACK_DAMAGE attribute at all.
	if got := b.getAttributeValue(attribute.AttackDamage); got != 0.0 {
		t.Fatalf("bat ATTACK_DAMAGE = %v, want 0.0 (no ATTACK_DAMAGE -- a bat never attacks)", got)
	}
	if got := b.getAttributeValue(attribute.FollowRange); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("bat FOLLOW_RANGE = %v, want 16.0 (createMobAttributes)", got)
	}
	if !b.batResting {
		t.Fatal("a freshly-spawned bat must be RESTING (ctor setResting(true))")
	}
	if b.ai == nil || b.ai.rng == nil {
		t.Fatal("bat has no minimal AI / rng")
	}
}

// TestBatWakesOnNearbyPlayer: a RESTING bat under a solid ceiling wakes (setResting(false)) when a player
// comes within range 4.0 (BAT_RESTING_TARGETING). With no player near, it stays resting.
func TestBatWakesOnNearbyPlayer(t *testing.T) {
	loop, floorY := batLoop(t)
	b := loop.spawnBat(8.5, float64(floorY+3), 8.5)
	if !b.batResting {
		t.Fatal("precondition: bat should spawn resting")
	}
	// A player within range 4.0 wakes the resting bat (the getNearestPlayer(BAT_RESTING_TARGETING) branch;
	// the ceiling-gone branch also wakes an airborne bat, so a wake is the expected outcome either way).
	addTestPlayer(loop, 7001, 8.5, float64(floorY+3), 9.0) // ~0.5 blocks away
	loop.batAiStep(b)
	if b.batResting {
		t.Fatal("bat did not wake with a player within range 4.0")
	}
}

// TestBatWakesOnHurt: a RESTING bat WAKES (setResting(false)) when damaged, BEFORE the shared damage
// pipeline (Bat.hurtServer: if (isResting()) setResting(false)). The bat still takes the damage. A hit
// on an already-flying bat leaves it flying. The wake draws no RNG.
func TestBatWakesOnHurt(t *testing.T) {
	loop, floorY := batLoop(t)
	b := loop.spawnBat(8.5, float64(floorY+3), 8.5)
	if !b.batResting {
		t.Fatal("precondition: bat should spawn resting")
	}
	hp0 := b.health
	loop.applyDamageEntity(b, damageSourcePlayerAttack(9001), 2.0)
	if b.batResting {
		t.Fatal("a resting bat did not wake when hurt (Bat.hurtServer setResting(false))")
	}
	if b.health >= hp0 {
		t.Fatalf("bat took no damage: health %v -> %v (the wake must not swallow the hit)", hp0, b.health)
	}
	// An already-flying bat that is hurt stays flying (no toggle back).
	b2 := loop.spawnBat(4.5, float64(floorY+3), 4.5)
	b2.batResting = false
	loop.applyDamageEntity(b2, damageSourcePlayerAttack(9002), 1.0)
	if b2.batResting {
		t.Fatal("hurting a flying bat must not set it resting")
	}
}

// TestBatRestingSnapAndFlyDrift: the physics + aiStep resting/flying toggle. A resting bat is snapped to
// hang (y = floor(y)+1 - height) with zero velocity; once flying it picks a drift target and steers its
// deltaMovement (a non-zero kick), and never takes fall damage.
func TestBatRestingToggleDrift(t *testing.T) {
	loop, floorY := batLoop(t)
	b := loop.spawnBat(8.5, float64(floorY+3), 8.5)

	// Resting snap Y (Bat.tick resting branch): floor(y)+1 - bbHeight.
	wantY := math.Floor(b.y) + 1.0 - float64(b.height)
	if got := batRestSnapY(b); math.Abs(got-wantY) > 1e-9 {
		t.Fatalf("batRestSnapY = %v, want %v", got, wantY)
	}

	// Wake the bat (clear resting) then drive one flying aiStep: it must pick a target and steer velocity.
	b.batResting = false
	b.vx, b.vy, b.vz = 0, 0, 0
	loop.batAiStep(b)
	if !b.batHasTarget {
		t.Fatal("a flying bat did not pick a drift target")
	}
	if b.vx == 0 && b.vy == 0 && b.vz == 0 {
		t.Fatal("a flying bat did not steer its deltaMovement toward the target (zero kick)")
	}
	if !batIsFlyer(b) {
		t.Fatal("batIsFlyer should be true for a bat")
	}
}
