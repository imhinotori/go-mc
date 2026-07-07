package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// mob_drown_test.go gates the B-A6 generalization of LivingEntity.baseTick's air / drowning /
// suffocation block to mobs (breath_mob.go). The jar-verified values under test are the SAME ones
// the player port pins: air decrements 1/tick submerged (decreaseAirSupply, OXYGEN_BONUS 0),
// drowning at air <= -20 dealing 2.0 DROWN, IN_WALL suffocation 1.0, and the per-type
// canBreatheUnderwater tag guard (undead / water mobs never drown). A fresh mob is born with a full
// 300 bubble bar (NewEntity seeds getMaxAirSupply()).

// drownMob builds a fresh Pig *Entity at (x,y,z) with full health, added to the loop's single-region
// store. A pig (height 0.9 -> eye at y+0.765) has its eye block at floor(y+0.765); at y=64 that is
// block 64. The pig is NOT in the CAN_BREATHE_UNDER_WATER tag, so it drowns.
func drownMob(loop *TickLoop, id int32, x, y, z float64) *Entity {
	e := NewEntity(id, entity.Pig, x, y, z)
	e.health = 20
	loop.only().entities.add(e)
	return e
}

// TestMobAirDecrementsUnderwater: a submerged non-water mob loses exactly 1 air per tick.
func TestMobAirDecrementsUnderwater(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0) // pig eye block at y=64
	e := drownMob(loop, 1, 8.5, 64.0, 8.5)
	if e.airSupply != maxAirSupply {
		t.Fatalf("fresh mob air = %d, want %d (born full)", e.airSupply, maxAirSupply)
	}

	loop.tickMobBreath(e)
	if e.airSupply != maxAirSupply-1 {
		t.Fatalf("air after 1 underwater tick = %d, want %d", e.airSupply, maxAirSupply-1)
	}
	loop.tickMobBreath(e)
	if e.airSupply != maxAirSupply-2 {
		t.Fatalf("air after 2 underwater ticks = %d, want %d", e.airSupply, maxAirSupply-2)
	}
}

// TestMobDrownDamageAtThreshold: a submerged mob at air -19 crosses to -20 (shouldTakeDrowningDamage),
// resets air to 0, and takes exactly 2.0 DROWN damage.
func TestMobDrownDamageAtThreshold(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	e := drownMob(loop, 1, 8.5, 64.0, 8.5)
	e.airSupply = drowningThreshold + 1 // -19 -> after decrement -20

	loop.tickMobBreath(e)
	if e.airSupply != 0 {
		t.Fatalf("air after drown tick = %d, want 0 (reset on drown)", e.airSupply)
	}
	if e.health != 20-drownDamage {
		t.Fatalf("health after drown = %v, want %v (2.0 DROWN)", e.health, 20-drownDamage)
	}
}

// TestMobRefillsAboveWater: out of water a mob refills 4/tick, clamped to max, and a full bar is a
// no-op (the byte-identical property for a dry land mob at full air).
func TestMobRefillsAboveWater(t *testing.T) {
	loop, _ := newFluidLoop() // no water
	e := drownMob(loop, 1, 8.5, 64.0, 8.5)
	e.airSupply = 100

	loop.tickMobBreath(e)
	if e.airSupply != 104 {
		t.Fatalf("air after 1 dry tick = %d, want 104 (refill +4)", e.airSupply)
	}

	e.airSupply = 298
	loop.tickMobBreath(e)
	if e.airSupply != maxAirSupply {
		t.Fatalf("refill clamp = %d, want %d (min(air+4, max))", e.airSupply, maxAirSupply)
	}
}

// TestMobLandFullAirNoDamage is the oracle-shaped property: a dry land mob at full air takes NO
// damage and its air / metadata do not change -> no new wire output, byte-identical to a mob that
// never ran the breath step. (increaseAirSupply caps at max, no drown, no suffocation.)
func TestMobLandFullAirNoDamage(t *testing.T) {
	loop, _ := newFluidLoop() // no water, no wall
	e := drownMob(loop, 1, 8.5, 64.0, 8.5)
	beforeAir := e.airSupply
	beforeSent := e.lastAirSent
	beforeHealth := e.health

	loop.tickMobBreath(e)

	if e.airSupply != beforeAir {
		t.Fatalf("land full-air mob air changed: %d -> %d (want no-op)", beforeAir, e.airSupply)
	}
	if e.lastAirSent != beforeSent {
		t.Fatalf("land full-air mob lastAirSent changed: %d -> %d (want no metadata push)", beforeSent, e.lastAirSent)
	}
	if e.health != beforeHealth {
		t.Fatalf("land full-air mob took damage: %v -> %v (want none)", beforeHealth, e.health)
	}
}

// TestMobSuffocationInWall: a mob whose eye block is a solid (suffocating) block takes 1.0 IN_WALL
// damage per tick.
func TestMobSuffocationInWall(t *testing.T) {
	loop, mgr := newFluidLoop()
	setSolid(mgr, pk.Position{X: 8, Y: 64, Z: 8}) // pig eye block at y=64 -> stone -> in a wall
	e := drownMob(loop, 1, 8.5, 64.0, 8.5)

	if !loop.mobIsInWall(e) {
		t.Fatalf("mob with eye in stone should be in a wall")
	}
	loop.tickMobBreath(e)
	if e.health != 20-suffocationDamage {
		t.Fatalf("health after suffocation = %v, want %v (1.0 IN_WALL)", e.health, 20-suffocationDamage)
	}
}

// TestUndeadDoesNotDrown: a zombie (in EntityTypeTags.CAN_BREATHE_UNDER_WATER via #undead) submerged
// never loses air (canBreatheUnderwater folds the flag to false).
func TestUndeadDoesNotDrown(t *testing.T) {
	if !mobCanBreatheUnderwater(entity.Zombie.ID) {
		t.Fatalf("zombie must be in CAN_BREATHE_UNDER_WATER (#undead)")
	}
	loop, mgr := newFluidLoop()
	// Zombie height 1.95 -> eye at y+1.6575 -> block 65 at y=64.
	setWater(mgr, pk.Position{X: 8, Y: 65, Z: 8}, 0)
	z := NewEntity(1, entity.Zombie, 8.5, 64.0, 8.5)
	z.health = 20
	loop.only().entities.add(z)

	if !loop.mobEyeInWater(z) {
		t.Fatalf("zombie eyes should be in the water block")
	}
	loop.tickMobBreath(z)
	if z.airSupply != maxAirSupply {
		t.Fatalf("undead submerged air = %d, want %d (never drains)", z.airSupply, maxAirSupply)
	}
	if z.health != 20 {
		t.Fatalf("undead submerged health = %v, want 20 (no drown)", z.health)
	}
}

// TestMobWaterBreathingHoldsAir: a submerged mob holding WATER_BREATHING does not drain air (the
// baseTick flag folds !hasWaterBreathing) — MobEffectUtil.hasWaterBreathing over mobEffects.
func TestMobWaterBreathingHoldsAir(t *testing.T) {
	loop, mgr := newFluidLoop()
	setWater(mgr, pk.Position{X: 8, Y: 64, Z: 8}, 0)
	e := drownMob(loop, 1, 8.5, 64.0, 8.5)
	e.airSupply = 100
	e.mobEffects = map[string]*activeEffect{effectWaterBreathing: {id: effectWaterBreathing, duration: 600}}

	if !mobHasWaterBreathing(e) {
		t.Fatalf("mob with WATER_BREATHING effect should report hasWaterBreathing")
	}
	loop.tickMobBreath(e)
	// !flag -> not drained; the else-if refill needs shouldEffectsRefillAirsupply (nautilus-only),
	// which WATER_BREATHING alone does NOT satisfy, so air stays put (no drain, no refill).
	if e.airSupply != 100 {
		t.Fatalf("WATER_BREATHING mob air = %d, want 100 (no drain, no nautilus refill)", e.airSupply)
	}
}
