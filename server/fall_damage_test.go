package server

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
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

// TestCalculateFallDamageMatchesVanillaFormula pins the literal vanilla product against the
// jar-verified expression Mth.floor((d + 1e-6 - 3.0) * mul * 1.0). This guards against anyone
// re-collapsing or re-paraphrasing the formula: calculateFallDamage MUST equal the explicit
// float-floor of (calculateFallPower(d) * damageMultiplier * FALL_DAMAGE_MULTIPLIER).
func TestCalculateFallDamageMatchesVanillaFormula(t *testing.T) {
	cases := []struct {
		d   float64
		mul float64
	}{
		{0, 1.0}, {2.0, 1.0}, {3.0, 1.0}, {3.5, 1.0}, {10.0, 1.0},
		{12.0, 1.0}, {23.0, 1.0}, {255.0, 1.0}, {10.0, 0.5},
	}
	for _, c := range cases {
		// The literal vanilla formula, written out independently of the implementation:
		// LivingEntity.calculateFallDamage -> Mth.floor(calculateFallPower(d) * mul * FALL_DAMAGE_MULTIPLIER),
		// calculateFallPower(d) = (d + 1.0E-6) - SAFE_FALL_DISTANCE(=3.0), FALL_DAMAGE_MULTIPLIER=1.0.
		want := int(math.Floor((c.d + 1.0e-6 - 3.0) * c.mul * 1.0))
		if got := calculateFallDamage(c.d, c.mul); got != want {
			t.Fatalf("calculateFallDamage(%v, %v) = %d, want %d (floor((d+1e-6-3.0)*mul*1.0))",
				c.d, c.mul, got, want)
		}
	}
}

// TestCheckFallDamageFloatCast pins the (float) narrowing cast in Entity.checkFallDamage:
// fallDistance -= (double)(float) deltaY. A deltaY that is NOT exactly representable as a float32
// must accumulate the float32-rounded magnitude, not the raw float64 — proving the d2f/f2d cast is
// present. We pick deltaY = -0.1 (0.1 is not exactly representable; float32(0.1) != float64(0.1)).
func TestCheckFallDamageFloatCast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	defer loop.Close() // release the async pools so this test does not leak pathfinding workers
	p := fallPlayer(loop, 1, 100)

	// Airborne, dry, descending by exactly 0.1: deltaY = -0.1. With the (float) cast, fallDistance
	// accumulates float64(float32(0.1)), NOT 0.1.
	p.onGround, p.wasOnGround = false, false
	p.lastY = 100
	p.y = 100 - 0.1
	loop.tickFallDamage()

	wantCast := float64(float32(0.1)) // exactly what `fallDistance -= (float)deltaY` adds
	if p.fallDistance != wantCast {
		t.Fatalf("fallDistance = %v after a 0.1 drop, want %v (float32-cast magnitude); "+
			"a raw float64 0.1 would be %v — the (float) cast is missing", p.fallDistance, wantCast, 0.1)
	}
	// And it must NOT equal the un-cast float64 value (the cast genuinely changes the bits).
	if p.fallDistance == 0.1 {
		t.Fatalf("fallDistance == raw float64 0.1: the (float) narrowing cast was skipped")
	}
}

// --- 17-08 WATER GUARD ---
//
// Vanilla (Entity.checkFallDamage + Entity.updateFluidInteraction, javap-verified this session)
// negates ALL fall damage in water: descent in water accumulates no fall distance, and touching
// water resetFallDistance()s any distance built up before entering. These tests pin both halves.

// waterFallLoop wires a fluid world (one ready, all-air chunk at column {0,0}) with a water
// source column, and registers a combatPlayer (full health + capturing client) positioned over
// that column. The player's x/z (8.5, 8.5) sit inside the {8,*,8} block so playerInWater is true
// once its feet reach the water at y=64.
func waterFallLoop(t *testing.T) (*TickLoop, *world.ChunkManager, *tickPlayer) {
	t.Helper()
	loop, mgr := newFluidLoop()
	// A 3-block-deep water column at x=8,z=8 from y=64 up to y=66 (surface block top at y=67) so a
	// player with feet at y>=67 is clearly above the pool and a player with feet at y<=66 is in it.
	for y := 64; y <= 66; y++ {
		setWater(mgr, pk.Position{X: 8, Y: y, Z: 8}, 0)
	}
	p := combatPlayer(loop, 1)
	p.x, p.z = 8.5, 8.5
	p.y, p.lastY = 80, 80
	p.onGround, p.wasOnGround = false, false // already airborne above the pool
	return loop, mgr, p
}

// TestFallIntoWaterNoDamage: a long fall that ends in water deals 0 damage — the headline 17-08
// fix. Without the water guard the player would take floor(13-3)=10 damage on the water floor.
func TestFallIntoWaterNoDamage(t *testing.T) {
	loop, _, p := waterFallLoop(t)

	// Fall from y=80 down to y=67 through air (13 blocks of descent, feet still ABOVE the water
	// surface at y=67), then continue down to y=64 INSIDE the water column, then "land" on the
	// submerged floor at y=64.
	step(loop, p, 67, false) // descends 13 in air -> fallDistance 13 (dry above the pool)
	if p.fallDistance == 0 {
		t.Fatalf("dry airborne descent did not accumulate fallDistance (got 0)")
	}
	step(loop, p, 64, false) // now feet at y=64: in water -> reset, no accumulation
	if p.fallDistance != 0 {
		t.Fatalf("entering water did not reset fallDistance, got %v (want 0)", p.fallDistance)
	}
	step(loop, p, 64, true) // landing edge while submerged: must deal 0 damage

	if p.health != maxHealth {
		t.Fatalf("fall into water dealt damage: health = %v, want %v (vanilla negates fall damage in water)", p.health, float32(maxHealth))
	}
	if p.fallDistance != 0 {
		t.Fatalf("fallDistance = %v after landing in water, want 0", p.fallDistance)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundSetHealth); n != 0 {
		t.Fatalf("fall into water sent %d SetHealth, want 0 (no damage)", n)
	}
}

// TestDescentInWaterNoAccumulation: a player sinking entirely within water never accumulates fall
// distance (Entity.checkFallDamage's `!isInWater()` accumulation guard).
func TestDescentInWaterNoAccumulation(t *testing.T) {
	loop, _, p := waterFallLoop(t)
	// Start the player already submerged (feet at y=66, inside the 64..67 water column).
	p.y, p.lastY = 66, 66

	// Sink within the water: 66 -> 65 -> 64. Every tick playerInWater is true, so no accumulation.
	step(loop, p, 65, false)
	step(loop, p, 64, false)

	if p.fallDistance != 0 {
		t.Fatalf("descent within water accumulated fallDistance = %v, want 0", p.fallDistance)
	}
	step(loop, p, 64, true) // surface to a submerged floor: still 0 damage
	if p.health != maxHealth {
		t.Fatalf("sinking-in-water player took damage: health = %v, want %v", p.health, float32(maxHealth))
	}
}
