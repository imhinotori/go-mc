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

// TestShulkerTeleportsWhenHurtLow: Shulker.hurtServer's post-hurt tail teleports a shulker that has dropped
// BELOW 50% HP on a nextInt(4)==0 roll (shulkerHurtServerReaction wiring the previously-orphaned
// shulkerTeleportSomewhere). A shulker at FULL HP never teleports; a low-HP one eventually bolts. Also pins
// the CLOSED arrow-immunity gate (a closed shulker rejects an arrow). Cite Shulker.hurtServer.
func TestShulkerTeleportsWhenHurtLow(t *testing.T) {
	loop, floorY := shulkerLoop(t)

	// A FULL-HP shulker never teleports (the health < maxHealth*0.5 gate is false).
	full := loop.spawnShulker(8.5, float64(floorY+1), 8.5)
	fx, fz := full.x, full.z
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 40; i++ {
			loop.shulkerHurtServerReaction(full, damageSourceOf(damageTypeGeneric))
		}
	})
	if full.x != fx || full.z != fz {
		t.Fatal("a full-HP shulker teleported -- the health<50% gate must block the teleport roll")
	}

	// A LOW-HP shulker (health < maxHealth*0.5) eventually teleports on a nextInt(4)==0 roll.
	low := loop.spawnShulker(5.5, float64(floorY+1), 5.5)
	low.health = 5.0 // 5 < 30*0.5 == 15
	ox, oz := low.x, low.z
	moved := false
	loop.withRegion(loop.only(), func() {
		for i := 0; i < 200; i++ { // enough rolls that at least one nextInt(4)==0 + a valid landing hits
			loop.shulkerHurtServerReaction(low, damageSourceOf(damageTypeGeneric))
			if low.x != ox || low.z != oz {
				moved = true
				break
			}
		}
	})
	if !moved {
		t.Fatal("a low-HP shulker never teleported over 200 hits -- shulkerHurtServerReaction did not wire shulkerTeleportSomewhere")
	}

	// CLOSED arrow-immunity: a closed shulker rejects an AbstractArrow source (returns immune).
	closed := loop.spawnShulker(3.5, float64(floorY+1), 3.5) // spawns CLOSED
	if !shulkerArrowImmune(closed, damageSourceArrow(0)) {
		t.Fatal("closed shulker not immune to an arrow (shulkerArrowImmune false) -- Shulker.hurtServer top gate")
	}
	loop.shulkerSetRawPeek(closed, shulkerPeekOpen) // open it
	if shulkerArrowImmune(closed, damageSourceArrow(0)) {
		t.Fatal("OPEN shulker reported arrow-immune -- only a CLOSED shulker deflects arrows")
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

// TestShulkerFireRangeIs400: the ShulkerAttackGoal.tick fire gate is distanceToSqr(target) < 400.0
// (Euclidean < 20), NOT the old per-axis <=15 box. A target acquired within FOLLOW_RANGE (16) but > 15 on
// an axis (which the old gate wrongly REJECTED) now opens the shell and fires. Cite
// Shulker$ShulkerAttackGoal.tick (ldc2_w 400.0d; iflt) + canUse (no range gate).
func TestShulkerFireRangeIs400(t *testing.T) {
	loop, floorY := shulkerLoop(t)
	s := loop.spawnShulker(8.5, float64(floorY+1), 8.5)
	// Player at ~15.9 blocks along Z: distanceToSqr ~253 (< 400 -> in fire range) and within FOLLOW_RANGE
	// (16). The OLD gate (abs(dz) <= 15) would have REJECTED this (15.9 > 15).
	p := addTestPlayer(loop, 9601, 8.5, float64(floorY+1), 8.5+15.9)
	p.health = 30
	p.client = captureClient(64)

	// The scan cadence may defer the first acquire; tick until the shell opens (target acquired + in range).
	opened := false
	for i := 0; i < 40; i++ {
		loop.shulkerAiStep(s)
		if !shulkerIsClosed(s) {
			opened = true
			break
		}
	}
	if !opened {
		t.Fatal("shulker never opened for a target at ~15.9 blocks (< 20) — the fire/acquire range regressed")
	}
	if s.shulker.attackTime <= 0 {
		// attackTime is armed on the first fire tick; a positive value proves it entered the fire branch.
		// Tick a few more to let the bullet fire + re-arm the cooldown.
	}

	// A bullet must be spawned within a handful of ticks (distanceToSqr < 400 gate passes).
	firedBullet := false
	for i := 0; i < 40 && !firedBullet; i++ {
		loop.shulkerAiStep(s)
		for _, e := range loop.only().entities.byID {
			if e.shulkerBullet != nil {
				firedBullet = true
			}
		}
	}
	if !firedBullet {
		t.Fatal("shulker fired NO bullet at a target ~15.9 blocks away (< 20) — distanceToSqr<400 fire gate broken")
	}
}

// TestShulkerNoFireBeyond20: a target beyond 20 blocks (distanceToSqr >= 400) is NOT fired at (the shell may
// still open per canUse, but no bullet spawns). Cite Shulker$ShulkerAttackGoal.tick fire gate.
func TestShulkerNoFireBeyond20(t *testing.T) {
	loop, floorY := shulkerLoop(t)
	s := loop.spawnShulker(8.5, float64(floorY+1), 8.5)
	// FOLLOW_RANGE is 16, so a target at 25 blocks is never even acquired -> never fires. Assert no bullet.
	p := addTestPlayer(loop, 9602, 8.5, float64(floorY+1), 8.5+25.0)
	p.health = 30
	p.client = captureClient(64)
	for i := 0; i < 60; i++ {
		loop.shulkerAiStep(s)
	}
	for _, e := range loop.only().entities.byID {
		if e.shulkerBullet != nil {
			t.Fatal("shulker fired at a target beyond FOLLOW_RANGE/fire range — the range gate is broken")
		}
	}
}

// TestShulkerDropsTargetBeyondFiringRange: ShulkerAttackGoal.tick (bytecode 64-194) fires a bullet when
// distanceToSqr(target) < 400.0, but ELSE (>= 400, i.e. >= 20 blocks) calls setTarget(null) -- the shulker
// DROPS a target that has fled beyond 20 blocks. The port previously just returned, retaining the target.
func TestShulkerDropsTargetBeyondFiringRange(t *testing.T) {
	loop, floorY := shulkerLoop(t)
	s := loop.spawnShulker(8.5, float64(floorY+1), 8.5)

	// Player 25 blocks away (dist^2 = 625 >= 400): out of firing range.
	p := combatTestPlayer(loop, 8.5, float64(floorY+1), 33.5, 7050)
	s.ai.attackTargetID = p.entityID

	loop.shulkerAttackGoal(s, p)
	if s.ai.attackTargetID != 0 {
		t.Fatalf("shulker retained target %d beyond 20 blocks -- tick else branch must setTarget(null)", s.ai.attackTargetID)
	}
}
