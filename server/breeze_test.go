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

// TestBreezeFiresWindCharge: on the fire tick the Breeze spawns a real BreezeWindCharge projectile
// (hurtWindCharge on the shared hurtingprojectile machinery) aimed at the target -- NOT a no-op. The
// projectile carries the breeze as owner and flies (non-zero velocity). Cite BreezeAi Shoot
// (spawnProjectileUsingShoot(new BreezeWindCharge(breeze, level), ..., 0.7f, 5 - difficulty*4)).
func TestBreezeFiresWindCharge(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)
	p := &tickPlayer{x: 12.5, y: float64(floorY + 1), z: 8.5, entityID: 7301}
	loop.players = append(loop.players, p)

	loop.breezeAiStep(b) // acquires + fires

	var wc *Entity
	for _, e := range loop.only().entities.byID {
		if e.isHurting && e.hurtingKind == hurtWindCharge {
			wc = e
		}
	}
	if wc == nil {
		t.Fatal("breeze fire tick spawned NO wind charge (breezeFireWindCharge must not be a no-op)")
	}
	if wc.hurtOwnerID != b.id {
		t.Fatalf("wind charge owner = %d, want the breeze id %d", wc.hurtOwnerID, b.id)
	}
	speed := wc.vx*wc.vx + wc.vy*wc.vy + wc.vz*wc.vz
	if speed <= 0 {
		t.Fatal("wind charge has zero velocity — it must fly toward the target")
	}
	// It should head generally toward +X (the target is at +X from the breeze).
	if wc.vx <= 0 {
		t.Fatalf("wind charge vx = %v, want > 0 (aimed toward the +X target)", wc.vx)
	}
}

// TestBreezeShootRangeGate: the Shoot.isTargetWithinRange gate (distanceToSqr < 256.0 == 16 blocks)
// must NOT fire on a target that is acquired within FOLLOW_RANGE (24) but sits BEYOND the 16-block
// shoot range. A player 20 blocks away is a valid target (acquired) but out of shoot range, so the
// breeze must NOT arm its cooldown (no wind charge). Regression guard: the port previously compared
// against 256^2 (=65536), giving a bogus 256-block shoot range. Cite Breeze Shoot.isTargetWithinRange.
func TestBreezeShootRangeGate(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)

	// Player 20 blocks north: inside FOLLOW_RANGE (24) so acquired, but beyond the 16-block shoot range.
	p := &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 28.5, entityID: 7401}
	loop.players = append(loop.players, p)

	loop.breezeAiStep(b)
	if b.ai == nil || b.ai.attackTargetID != p.entityID {
		t.Fatalf("breeze should still ACQUIRE a target within FOLLOW_RANGE 24: got %d", b.ai.attackTargetID)
	}
	if b.breezeShootCooldown != 0 {
		t.Fatalf("breeze armed its shoot cooldown (%d) against a target 20 blocks away -- Shoot.isTargetWithinRange (<256 sq == 16 blocks) must gate it out", b.breezeShootCooldown)
	}

	// Now move the player to 15 blocks (inside 16-block range, dist^2 = 225 < 256): the breeze fires.
	p.z = 23.5
	loop.breezeAiStep(b)
	if b.breezeShootCooldown == 0 {
		t.Fatal("breeze did NOT fire at a target 15 blocks away (dist^2=225 < 256) -- the shoot gate is too tight")
	}
}

// TestBreezeDeflectsArrow: a non-wind-charge projectile (an arrow) whose flight segment reaches a Breeze is
// REVERSE-deflected (Breeze.deflection returns PROJECTILE_DEFLECTION because a breeze is in
// EntityTypeTags.DEFLECTS_PROJECTILES) -- its velocity is reversed + halved and it is NOT consumed. Cite
// Breeze.deflection + ProjectileDeflection.REVERSE.
func TestBreezeDeflectsArrow(t *testing.T) {
	loop, floorY := breezeLoop(t)

	// A breeze sitting in the arrow's flight path.
	b := loop.spawnBreeze(11.5, float64(floorY+1), 8.5)

	// An arrow fired toward +X (toward the breeze) fast enough to reach it this tick.
	a := loop.spawnArrow(0, 8.5, float64(floorY+1)+0.5, 8.5, 4.0, 0.0, 0.0, 3.0)
	vx0 := a.vx
	loop.tickArrow(a)

	// The arrow must NOT be consumed (it bounces off).
	if _, ok := loop.only().entities.byID[a.id]; !ok {
		t.Fatal("arrow was consumed by the breeze -- it must be DEFLECTED (REVERSE), not absorbed")
	}
	// REVERSE.deflect scales the velocity by -0.5: the x-velocity now points back (negative) at half.
	if a.vx >= 0 {
		t.Fatalf("arrow vx after deflection = %v, want < 0 (velocity reversed by REVERSE.deflect)", a.vx)
	}
	if math.Abs(a.vx-(vx0*-0.5)) > 1e-9 {
		t.Fatalf("arrow vx after deflection = %v, want %v (getDeltaMovement().scale(-0.5))", a.vx, vx0*-0.5)
	}
	_ = b

	// A pure decision check: the breeze deflects an arrow but NOT its own wind-charge family.
	if !breezeDeflectsProjectile(b, entity.Arrow.ID) {
		t.Fatal("breezeDeflectsProjectile(arrow) = false, want true (arrows are deflected)")
	}
	if breezeDeflectsProjectile(b, entity.WindCharge.ID) {
		t.Fatal("breezeDeflectsProjectile(wind_charge) = true, want false (the breeze's own wind charge passes)")
	}
	if breezeDeflectsProjectile(b, entity.BreezeWindCharge.ID) {
		t.Fatal("breezeDeflectsProjectile(breeze_wind_charge) = true, want false (NONE for the breeze family)")
	}
}

// TestBreezeRepositionsTowardTarget: the BreezeAi Slide port makes the Breeze MOVE. With a target present,
// on the ground, and no active path, breezeSlide commits a navigation want (hasTarget) -- the Breeze is no
// longer a turret. The committed want must differ from the Breeze's own block (it repositions). Cite
// BreezeAi Slide + Slide.start.
func TestBreezeRepositionsTowardTarget(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)
	b.onGround = true // Slide.checkExtraStartConditions requires onGround

	// A target 12 blocks north (outside the inner circle, so the Slide takes the behind/middle path).
	p := &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 20.5, entityID: 7501}
	loop.players = append(loop.players, p)

	loop.breezeAcquireNearestPlayer(b)
	if b.ai == nil || b.ai.attackTargetID != p.entityID {
		t.Fatalf("breeze did not acquire target: %d", b.ai.attackTargetID)
	}
	if b.ai.hasTarget {
		t.Fatal("fresh breeze already has a nav want before sliding")
	}
	loop.breezeSlide(b, p)
	if !b.ai.hasTarget {
		t.Fatal("breezeSlide did NOT commit a navigation want -- the Breeze must reposition (it plays as a turret otherwise)")
	}
	// The want must not be the Breeze's own cell (it moved somewhere).
	if mthFloor(b.ai.wantX) == mthFloor(b.x) && mthFloor(b.ai.wantZ) == mthFloor(b.z) {
		t.Fatalf("breezeSlide want (%v,%v) == the Breeze cell -- it did not reposition", b.ai.wantX, b.ai.wantZ)
	}
}

// TestBreezeSlideGatedOffGround: Slide.checkExtraStartConditions requires onGround. A mid-air Breeze does
// NOT commit a slide want. Cite Slide.checkExtraStartConditions.
func TestBreezeSlideGatedOffGround(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)
	b.onGround = false // not on the ground
	p := &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 20.5, entityID: 7601}
	loop.players = append(loop.players, p)
	loop.breezeAcquireNearestPlayer(b)
	loop.breezeSlide(b, p)
	if b.ai.hasTarget {
		t.Fatal("a mid-air breeze committed a slide want -- Slide.checkExtraStartConditions requires onGround")
	}
}

// TestBreezeMiddleCircleStepsTowardTarget: breezeRandomPointInMiddleCircle (the non-flee, non-behind Slide
// branch) steps the Breeze TOWARD the target by (length - lerp(nextDouble, 8, 4)) along the toTarget unit
// vector -- so the returned point lies between the Breeze and the target (closer to the target than the
// Breeze starts, for a target > 8 blocks away). Cite Slide.randomPointInMiddleCircle.
func TestBreezeMiddleCircleStepsTowardTarget(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)
	// Target 20 blocks north; a middle-circle step ends up between the Breeze and the target.
	p := &tickPlayer{x: 8.5, y: float64(floorY + 1), z: 28.5, entityID: 7701}
	x, _, z := breezeRandomPointInMiddleCircle(b, p)
	// The point must be north of the Breeze (toward the +Z target) and not past it.
	if z <= b.z {
		t.Fatalf("middle-circle point z=%v is not toward the +Z target (breeze z=%v)", z, b.z)
	}
	if z >= p.z {
		t.Fatalf("middle-circle point z=%v overshoots the target z=%v", z, p.z)
	}
	// X stays aligned (the target is directly +Z, so no X drift).
	if math.Abs(x-b.x) > 1e-9 {
		t.Fatalf("middle-circle point x=%v drifted from the aligned breeze x=%v", x, b.x)
	}
}

// TestBreezeInnerCircleFlees: when the target is inside the inner circle (within 4 blocks XZ), the Slide
// prefers a flee-away pos (getPosAway) that is FARTHER from the target than the Breeze -- and regardless of
// the branch taken it commits a nav want. Verifies breezeWithinInnerCircle detects the close target and the
// Breeze still repositions. Cite Slide.start (withinInnerCircleRange -> getPosAway retreat).
func TestBreezeInnerCircleFlees(t *testing.T) {
	loop, floorY := breezeLoop(t)
	b := loop.spawnBreeze(8.5, float64(floorY+1), 8.5)
	b.onGround = true
	// Target 2 blocks away (inside the 4-block inner-circle radius).
	p := &tickPlayer{x: 10.5, y: float64(floorY + 1), z: 8.5, entityID: 7801}
	if !breezeWithinInnerCircle(b, p.x, p.y, p.z) {
		t.Fatal("breezeWithinInnerCircle should be true for a target 2 blocks away")
	}
	loop.players = append(loop.players, p)
	loop.breezeAcquireNearestPlayer(b)
	loop.breezeSlide(b, p)
	if !b.ai.hasTarget {
		t.Fatal("breeze inside the inner circle did NOT commit a reposition want")
	}
}
