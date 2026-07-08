package server

// breeze_test.go -- deterministic pins for the Breeze (net.minecraft.world.entity.monster.breeze.Breeze,
// 1:1 javap this task). Verifies the spawn attributes (MOVEMENT_SPEED 0.63 / MAX_HEALTH 30 / FOLLOW_RANGE
// 24 / ATTACK_DAMAGE 3) and the SIGNATURE shoot cadence (fire in range, then arm the cooldown).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func breezeLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestBreezeSpawnDefaults: spawnBreeze renders entity.Breeze with the jar attributes.
func TestBreezeSpawnDefaults(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)
	if b.typ != entity.Breeze.ID {
		t.Fatalf("breeze typ = %d, want entity.Breeze.ID %d", b.typ, entity.Breeze.ID)
	}
	if !b.isBreeze {
		t.Fatal("breeze not marked isBreeze")
	}
	if math.Abs(float64(b.health)-30.0) > 1e-6 {
		t.Fatalf("breeze health = %v, want 30.0 (MAX_HEALTH)", b.health)
	}
	if got := b.getAttributeValue(attribute.MaxHealth); math.Abs(got-30.0) > 1e-9 {
		t.Fatalf("breeze MAX_HEALTH = %v, want 30.0", got)
	}
	if got := b.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.6299999952316284) > 1e-12 {
		t.Fatalf("breeze MOVEMENT_SPEED = %v, want 0.6299999952316284", got)
	}
	if got := b.getAttributeValue(attribute.FollowRange); math.Abs(got-24.0) > 1e-9 {
		t.Fatalf("breeze FOLLOW_RANGE = %v, want 24.0", got)
	}
	if got := b.getAttributeValue(attribute.AttackDamage); math.Abs(got-3.0) > 1e-9 {
		t.Fatalf("breeze ATTACK_DAMAGE = %v, want 3.0", got)
	}
}

// TestBreezeShootCadence: a Breeze with a player in range fires (arming the shoot cooldown), then counts
// the cooldown down before it can fire again.
func TestBreezeShootCadence(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)

	// A player within FOLLOW_RANGE (a few blocks away).
	p := &tickPlayer{x: 12.5, y: float64(floorY + 1), z: 8.5, entityID: 7201}
	loop.players = append(loop.players, p)

	if b.breezeShootCooldown != 0 {
		t.Fatalf("fresh breeze shoot cooldown = %d, want 0", b.breezeShootCooldown)
	}
	// First step: acquires the target + fires + arms the cooldown.
	loop.breezeAiStep(b)
	if b.ai == nil || b.ai.attackTargetID != p.entityID {
		t.Fatalf("breeze did not acquire the nearby player as target: got %d", b.ai.attackTargetID)
	}
	wantCooldown := breezeShootInitial + breezeShootRecover + breezeShootCooldownT
	if b.breezeShootCooldown != wantCooldown {
		t.Fatalf("breeze shoot cooldown after firing = %d, want %d", b.breezeShootCooldown, wantCooldown)
	}
	// Next step: on cooldown -> counts down, does not re-arm to the full cadence.
	loop.breezeAiStep(b)
	if b.breezeShootCooldown != wantCooldown-1 {
		t.Fatalf("breeze shoot cooldown did not count down: %d, want %d", b.breezeShootCooldown, wantCooldown-1)
	}
}
