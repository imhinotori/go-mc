package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
)

// fall_damage_test.go covers GAMEPLAY-04 (the environmental half): tickFallDamage accumulates a
// player's airborne descent into fallDistance and, on the onGround false->true landing edge,
// applies floor(fallDistance - 3.0) damage through the existing applyDamage flow, then resets.
//
// JAR-VERIFIED FORMULA (javap -c -p from temp/cache/26.2-inner.jar this session):
//   Entity.checkFallDamage          accumulates fallDistance -= deltaY while airborne (deltaY<0)
//                                    and on landing calls fallOn -> causeFallDamage, then resetFallDistance.
//   LivingEntity.calculateFallDamage = Mth.floor(calculateFallPower(d) * damageMul * FALL_DAMAGE_MULTIPLIER)
//   LivingEntity.calculateFallPower  = (fallDistance + 1.0E-6) - SAFE_FALL_DISTANCE   (SAFE_FALL_DISTANCE=3.0)
//   => default-attribute damage = floor(fallDistance - 3.0) HP.

// fallPlayer registers a confirmed player seeded as "standing on the ground at startY" so the
// first tick's bookkeeping (wasOnGround/lastY) is consistent. Reuses combatPlayer (full health,
// capturing client).
func fallPlayer(loop *TickLoop, entityID int32, startY float64) *tickPlayer {
	p := combatPlayer(loop, entityID)
	p.y, p.lastY = startY, startY
	p.onGround, p.wasOnGround = true, true
	return p
}

// step simulates one movement tick: sets the player's new y + onGround, then runs the
// fall-damage pass (the tickEntities call site).
func step(loop *TickLoop, p *tickPlayer, y float64, onGround bool) {
	p.y = y
	p.onGround = onGround
	loop.tickFallDamage()
}

// TestFallDamageAccumulates: a player descending while airborne accumulates fallDistance equal
// to the total descent.
func TestFallDamageAccumulates(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	// Leave the ground and fall: 100 -> 96 -> 92 -> 88 (12 blocks of descent), still airborne.
	step(loop, p, 96, false)
	step(loop, p, 92, false)
	step(loop, p, 88, false)

	if p.fallDistance != 12 {
		t.Fatalf("fallDistance = %v after a 12-block descent, want 12", p.fallDistance)
	}
}

// TestFallDamageOnLanding: a 10-block fall then a landing applies floor(10-3)=7 damage and
// resets fallDistance to 0.
func TestFallDamageOnLanding(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	// Fall 10 blocks: 100 -> 90 (airborne), then land at 90 (onGround true).
	step(loop, p, 90, false) // descends 10 while airborne
	step(loop, p, 90, true)  // landing edge: onGround false->true at fallDistance 10

	want := maxHealth - 7
	if p.health != want {
		t.Fatalf("after a 10-block fall health = %v, want %v (floor(10-3)=7 damage)", p.health, want)
	}
	if p.fallDistance != 0 {
		t.Fatalf("fallDistance = %v after landing, want 0 (reset)", p.fallDistance)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundSetHealth); n != 1 {
		t.Fatalf("landing damage sent %d SetHealth, want 1", n)
	}
}

// TestSmallFallNoDamage: a 2-block fall (< 3.0 safe distance) deals 0 damage on landing.
func TestSmallFallNoDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 100)

	step(loop, p, 98, false) // descends 2 while airborne
	step(loop, p, 98, true)  // land

	if p.health != maxHealth {
		t.Fatalf("a 2-block fall changed health to %v, want %v (below safe distance)", p.health, float32(maxHealth))
	}
	if p.fallDistance != 0 {
		t.Fatalf("fallDistance = %v after a sub-threshold landing, want 0", p.fallDistance)
	}
}

// TestStayGroundedNoDamage: a player on the ground every tick accumulates nothing and takes no
// damage.
func TestStayGroundedNoDamage(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := fallPlayer(loop, 1, 64)

	for i := 0; i < 5; i++ {
		step(loop, p, 64, true)
	}

	if p.fallDistance != 0 {
		t.Fatalf("grounded player accumulated fallDistance = %v, want 0", p.fallDistance)
	}
	if p.health != maxHealth {
		t.Fatalf("grounded player took damage, health = %v", p.health)
	}
}
