package server

// shulker_test.go -- deterministic pins for the Shulker + ShulkerBullet (1:1 javap this task).
// Verifies the spawn attributes (MAX_HEALTH 30 + the +20 covered armor while closed), the peek
// open/close armor toggle, and the bullet on-hit LEVITATION (200 ticks).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// shulkerLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick shulkers.
func shulkerLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestShulkerSpawnDefaults: spawnShulker builds a shulker rendering as entity.Shulker.ID with
// MAX_HEALTH 30 (health 30), CLOSED (peek 0), attachFace DOWN, and the +20 covered-armor modifier
// applied (ARMOR value 20 while closed). Cite Shulker.createAttributes + setRawPeekAmount(0).
func TestShulkerSpawnDefaults(t *testing.T) {
	loop, floorY := shulkerLoop(t)
	s := loop.spawnShulker(8.5, float64(floorY+1), 8.5)
	if s.typ != entity.Shulker.ID {
		t.Fatalf("shulker typ = %d, want entity.Shulker.ID %d", s.typ, entity.Shulker.ID)
	}
	if s.shulker == nil {
		t.Fatal("shulker has no shulkerState (e.shulker nil -- the per-type gate would never fire)")
	}
	if math.Abs(float64(s.health)-30.0) > 1e-6 {
		t.Fatalf("shulker health = %v, want 30.0 (MAX_HEALTH)", s.health)
	}
	if s.shulker.peek != shulkerPeekClosed {
		t.Fatalf("shulker peek = %d, want CLOSED %d", s.shulker.peek, shulkerPeekClosed)
	}
	if !shulkerIsClosed(s) {
		t.Fatal("shulker not closed at spawn (isClosed false)")
	}
	armor := s.attributes.GetValue(attribute.Armor.Name())
	if math.Abs(armor-shulkerCoveredArmor) > 1e-6 {
		t.Fatalf("closed shulker ARMOR = %v, want +20 covered %v", armor, shulkerCoveredArmor)
	}
}

// TestShulkerPeekArmorToggle: opening the shulker (peek > 0) REMOVES the covered armor (ARMOR back to
// 0); closing it restores +20. Cite Shulker.setRawPeekAmount (add when peek==0, remove otherwise).
func TestShulkerPeekArmorToggle(t *testing.T) {
	loop, floorY := shulkerLoop(t)
	s := loop.spawnShulker(8.5, float64(floorY+1), 8.5)
	loop.shulkerSetRawPeek(s, shulkerPeekOpen)
	if open := s.attributes.GetValue(attribute.Armor.Name()); math.Abs(open) > 1e-6 {
		t.Fatalf("open shulker ARMOR = %v, want 0 (covered armor removed)", open)
	}
	loop.shulkerSetRawPeek(s, shulkerPeekClosed)
	if closed := s.attributes.GetValue(attribute.Armor.Name()); math.Abs(closed-shulkerCoveredArmor) > 1e-6 {
		t.Fatalf("re-closed shulker ARMOR = %v, want +20 covered", closed)
	}
}

// TestShulkerBulletLevitation: a ShulkerBullet that reaches its target deals 4.0 damage + applies
// LEVITATION (200 ticks) to the player. Cite ShulkerBullet.onHitEntity.
func TestShulkerBulletLevitation(t *testing.T) {
	loop, floorY := shulkerLoop(t)
	s := loop.spawnShulker(8.5, float64(floorY+1), 8.5)
	p := addTestPlayer(loop, 9500, 8.5, float64(floorY+1), 8.5) // co-located -> immediate hit
	p.health = 20
	p.client = captureClient(64)
	bullet := loop.spawnShulkerBullet(s, p)
	if bullet.shulkerBullet == nil {
		t.Fatal("bullet has no shulkerBulletState")
	}
	// One tick: the bullet is on top of the player, so shulkerBulletTick hits + applies levitation.
	loop.shulkerBulletTick(bullet)
	if !playerHasEffect(p, effectLevitation) {
		t.Fatal("player has no LEVITATION after a shulker-bullet hit")
	}
}
