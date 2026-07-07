package server

// ghast_test.go -- deterministic pins for the hostile Ghast (net.minecraft.world.entity.monster.Ghast,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 10 / FOLLOW_RANGE 100), the shoot
// goal's 0..20 charge + the LargeFireball fired at 20 toward the target (spawn + velocity direction), the
// DATA_IS_CHARGING flip at the >10 threshold, and the fireball explosion on impact (explosionPower 1).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// ghastLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick ghasts.
func ghastLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestGhastSpawnDefaults: spawnGhast builds a ghast rendering as entity.Ghast.ID with the jar attributes
// (MAX_HEALTH 10.0 -> health 10, FOLLOW_RANGE 100.0), the default explosionPower 1, and a per-entity rng.
func TestGhastSpawnDefaults(t *testing.T) {
	loop, floorY := ghastLoop(t)
	g := loop.spawnGhast(8.5, float64(floorY+20), 8.5)
	if g.typ != entity.Ghast.ID {
		t.Fatalf("ghast typ = %d, want entity.Ghast.ID %d", g.typ, entity.Ghast.ID)
	}
	if !g.isGhast {
		t.Fatal("ghast not marked isGhast")
	}
	if math.Abs(float64(g.health)-10.0) > 1e-6 {
		t.Fatalf("ghast health = %v, want 10.0 (MAX_HEALTH)", g.health)
	}
	if got := g.getAttributeValue(attribute.MaxHealth); math.Abs(got-10.0) > 1e-9 {
		t.Fatalf("ghast MAX_HEALTH = %v, want 10.0", got)
	}
	if got := g.getAttributeValue(attribute.FollowRange); math.Abs(got-100.0) > 1e-9 {
		t.Fatalf("ghast FOLLOW_RANGE = %v, want 100.0", got)
	}
	if got := g.getAttributeValue(attribute.FlyingSpeed); math.Abs(got-0.06) > 1e-9 {
		t.Fatalf("ghast FLYING_SPEED = %v, want 0.06", got)
	}
	if g.ghastExplosionPower != 1 {
		t.Fatalf("ghast explosionPower = %d, want 1", g.ghastExplosionPower)
	}
	if g.ai == nil || g.ai.rng == nil {
		t.Fatal("ghast has no minimal AI / rng (mobRandom would not be per-entity seeded)")
	}
}

// TestGhastAcquiresNearestPlayer: a ghast with a player within FOLLOW_RANGE (100) and within the +/-4.0
// y-window acquires it as the attack target; a player outside the y-window is NOT acquired.
func TestGhastAcquiresNearestPlayer(t *testing.T) {
	loop, floorY := ghastLoop(t)
	gy := float64(floorY + 20)
	g := loop.spawnGhast(8.5, gy, 8.5)

	p := combatTestPlayer(loop, 20.5, gy, 8.5, 4242)
	loop.ghastAcquireNearestPlayer(g)
	if g.ai.attackTargetID != p.entityID {
		t.Fatalf("ghast target = %d, want the nearest player %d", g.ai.attackTargetID, p.entityID)
	}

	g.ai.attackTargetID = 0
	p.y = gy + 5.0
	loop.ghastAcquireNearestPlayer(g)
	if g.ai.attackTargetID != 0 {
		t.Fatalf("ghast acquired a player outside the +/-4.0 y-window (target=%d)", g.ai.attackTargetID)
	}
}

// TestGhastShootChargesAndFires: the shoot goal advances chargeTime 0..20 while a target is in range with
// line of sight, and at 20 it spawns a LargeFireball heading toward the target, then resets chargeTime to
// -40 (the cooldown). Asserts the fireball kind, owner, spawn near the ghast, and velocity direction.
func TestGhastShootChargesAndFires(t *testing.T) {
	loop, floorY := ghastLoop(t)
	gy := float64(floorY + 30) // high up, open air, clear LoS to a player at the same height
	g := loop.spawnGhast(8.5, gy, 8.5)
	p := combatTestPlayer(loop, 28.5, gy, 8.5, 4242)
	g.ai.attackTargetID = p.entityID

	for i := 0; i < 19; i++ {
		loop.ghastFaceMovementDirection(g) // GhastLookGoal faces the target so the view vector aims +X
		loop.ghastShootFireball(g)
	}
	if g.ghastChargeTime != 19 {
		t.Fatalf("ghast chargeTime = %d after 19 ticks, want 19", g.ghastChargeTime)
	}
	if fireballCount(loop) != 0 {
		t.Fatalf("ghast fired before chargeTime 20 (fireballs=%d)", fireballCount(loop))
	}

	loop.ghastFaceMovementDirection(g)
	loop.ghastShootFireball(g)
	if g.ghastChargeTime != -40 {
		t.Fatalf("ghast chargeTime = %d after firing, want -40 (cooldown)", g.ghastChargeTime)
	}
	fb := onlyFireball(t, loop)
	if fb.hurtingKind != hurtLargeFireball {
		t.Fatalf("fired projectile kind = %d, want hurtLargeFireball %d", fb.hurtingKind, hurtLargeFireball)
	}
	if fb.hurtOwnerID != g.id {
		t.Fatalf("fireball owner = %d, want the ghast %d", fb.hurtOwnerID, g.id)
	}
	if fb.hurtExplosion != 1 {
		t.Fatalf("fireball explosionPower = %d, want 1 (getExplosionPower)", fb.hurtExplosion)
	}
	if fb.x <= g.x {
		t.Fatalf("fireball spawn x = %v, want > ghast x %v (muzzle offset toward the +X target)", fb.x, g.x)
	}
	if fb.vx <= 0 {
		t.Fatalf("fireball vx = %v, want > 0 (heading toward the +X target)", fb.vx)
	}
	if math.Abs(fb.vx) <= math.Abs(fb.vz) {
		t.Fatalf("fireball heading not dominantly +X: vx=%v vz=%v", fb.vx, fb.vz)
	}
}

// TestGhastDataIsChargingFlips: DATA_IS_CHARGING (ghastCharging) is false at/below chargeTime 10 and true
// once chargeTime exceeds 10 (setCharging(chargeTime > 10)). Drive the charge and watch the flip.
func TestGhastDataIsChargingFlips(t *testing.T) {
	loop, floorY := ghastLoop(t)
	gy := float64(floorY + 30)
	g := loop.spawnGhast(8.5, gy, 8.5)
	p := combatTestPlayer(loop, 28.5, gy, 8.5, 4242)
	g.ai.attackTargetID = p.entityID

	for tick := 1; tick <= 12; tick++ {
		loop.ghastFaceMovementDirection(g)
		loop.ghastShootFireball(g)
		wantCharging := g.ghastChargeTime > 10
		if g.ghastCharging != wantCharging {
			t.Fatalf("tick %d chargeTime %d: ghastCharging = %v, want %v", tick, g.ghastChargeTime, g.ghastCharging, wantCharging)
		}
	}
	if !g.ghastCharging {
		t.Fatal("ghast not charging after chargeTime exceeded 10 (DATA_IS_CHARGING never flipped)")
	}

	g.ai.attackTargetID = 0
	loop.ghastShootFireball(g)
	if g.ghastCharging {
		t.Fatal("ghast still charging after losing the target (stop must setCharging false)")
	}
	if g.ghastChargeTime != 0 {
		t.Fatalf("ghast chargeTime = %d after losing the target, want 0 (start resets)", g.ghastChargeTime)
	}
}

// TestGhastLargeFireballExplodesOnImpact: a large fireball fired by a ghast explodes on impact -- a player
// standing at the impact takes MORE than the direct-hit fireball damage (6.0) because the onHit explosion
// (explosionPower 1) adds blast damage, and the fireball is discarded after the hit.
func TestGhastLargeFireballExplodesOnImpact(t *testing.T) {
	loop, floorY := ghastLoop(t)
	py := float64(floorY + 5)
	victim := combatTestPlayer(loop, 8.5, py, 12.5, 4242)
	victim.health = 20.0
	start := victim.health

	fb := loop.spawnHurtingProjectile(999, hurtLargeFireball, 8.5, py+playerHeight*0.5, 11.4, 0.0, 0.0, 1.0)
	fb.hurtExplosion = 1 // getExplosionPower() == 1

	present := true
	for i := 0; i < 40 && present; i++ {
		loop.withRegion(loop.regions[globalRegion], func() { loop.tickHurtingProjectile(fb) })
		_, present = loop.regions[globalRegion].entities.get(fb.id)
	}
	if present {
		t.Fatal("large fireball never hit / discarded (it should hit the player and explode)")
	}
	dealt := start - victim.health
	if float64(dealt) <= largeFireballDamage {
		t.Fatalf("victim took %v damage, want > %v (direct 6.0 + the onHit explosion blast)", dealt, largeFireballDamage)
	}
}

// TestGhastSkippedByGroundPhysics: the ghast is a flyer -- tickPhysics must take the travelFlying (no
// gravity, 0.91 drift) branch, NOT the ground gravity path. A stationary ghast high in the air does NOT
// gain downward velocity from gravity after a tickPhysics pass.
func TestGhastSkippedByGroundPhysics(t *testing.T) {
	loop, floorY := ghastLoop(t)
	g := loop.spawnGhast(8.5, float64(floorY+40), 8.5)
	g.vx, g.vy, g.vz = 0, 0, 0

	loop.tickPhysics()

	if g.vy < 0 {
		t.Fatalf("ghast vy = %v after tickPhysics, want >= 0 (a flyer takes the no-gravity travelFlying branch)", g.vy)
	}
}

// --- test helpers ------------------------------------------------------------------------------

// fireballCount counts the live large-fireball hurting projectiles in the global region.
func fireballCount(loop *TickLoop) int {
	n := 0
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.isHurting && e.hurtingKind == hurtLargeFireball {
			n++
		}
	}
	return n
}

// onlyFireball returns the single live large fireball (fails if there is not exactly one).
func onlyFireball(t *testing.T, loop *TickLoop) *Entity {
	t.Helper()
	var found *Entity
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.isHurting && e.hurtingKind == hurtLargeFireball {
			if found != nil {
				t.Fatal("more than one large fireball present")
			}
			found = e
		}
	}
	if found == nil {
		t.Fatal("no large fireball present (the ghast never fired)")
	}
	return found
}
