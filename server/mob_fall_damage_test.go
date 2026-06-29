package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// mob_fall_damage_test.go — LIVE-DEBUG B gates: a mob that falls from height takes fall damage on
// landing (the in-game "mob takes no fall damage" bug), a mob falling < SAFE_FALL_DISTANCE (3.0)
// takes none, and a mob landing in water takes none. The chain (Entity.checkFallDamage ->
// LivingEntity.causeFallDamage -> calculateFallDamage -> applyDamageEntity) is wired into tickPhysics
// (tick_phases.go) and ported in mob_fall_damage.go (javap citations there). These are PORT-EXACT
// behavior gates: each assertion mirrors the jar formula calculateFallDamage already pins for the
// player (fall_damage_test.go), now exercised on the *Entity path.

// dropMobAndLand spawns a pig at startY above a solid floor at floorY (in a 3x3 chunk neighborhood),
// then drives tickPhysics until the pig lands (onGround) or maxTicks elapse. It strips the AI so no
// FloatGoal/jump perturbs the fall, isolating the fall-damage path. Returns the pig and the loop so
// the caller can assert its post-landing health. health is initialized to the pig's MaxHealth.
func dropMobAndLand(t *testing.T, startY float64, floorY int, maxTicks int) (*TickLoop, *Entity) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillFloor(ch, floorY)
		}
	}
	pig := NewEntity(900, entity.Pig, 8.5, startY, 8.5)
	pig.onGround = false
	initSpawnHealth(pig) // health = MaxHealth (10 for a pig); a 0-health mob is treated as dead
	loop.only().entities.add(pig)

	for i := 0; i < maxTicks && !pig.onGround; i++ {
		loop.tickPhysics()
	}
	return loop, pig
}

// TestMobTakesFallDamageOnLanding: a pig dropped from a large height lands on the floor and LOSES
// health (the fall-damage chain ran). The drop is ~20 blocks (well past the 3.0 safe distance), so
// calculateFallDamage((~20+eps)-3.0) yields a positive int routed through applyDamageEntity.
func TestMobTakesFallDamageOnLanding(t *testing.T) {
	const (
		floorY   = 64
		startY   = 90.0 // ~25 blocks above the floor top (floorY+1 == 65)
		maxTicks = 200
	)
	_, pig := dropMobAndLand(t, startY, floorY, maxTicks)

	if !pig.onGround {
		t.Fatalf("pig never landed in %d ticks (y=%.4f) — the drop test cannot assert fall damage", maxTicks, pig.y)
	}
	maxHealth := float32(10.0) // the pig's declared MaxHealth (initSpawnHealth seeds health to it)
	if pig.health >= maxHealth {
		t.Fatalf("pig took NO fall damage after a %.0f-block drop: health=%.2f, MaxHealth=%.2f "+
			"(the mob fall-damage path did not run)", startY-float64(floorY+1), pig.health, maxHealth)
	}
	// And the fall distance must have been reset on landing (the resetFallDistance in checkFallDamage).
	if pig.fallDistance != 0 {
		t.Fatalf("pig fallDistance not reset on landing: %.4f, want 0", pig.fallDistance)
	}
}

// TestMobShortFallNoDamage: a pig dropped LESS than SAFE_FALL_DISTANCE (3.0) above the floor takes NO
// fall damage — calculateFallDamage((d+eps)-3.0) is <= 0 for d < 3.0, so Mth.floor yields 0 and no
// hurt fires. The pig keeps full health.
func TestMobShortFallNoDamage(t *testing.T) {
	const (
		floorY   = 64
		startY   = 66.5 // ~1.5 blocks above the floor top (65) — well under the 3.0 safe distance
		maxTicks = 100
	)
	loop, pig := dropMobAndLand(t, startY, floorY, maxTicks)
	_ = loop

	if !pig.onGround {
		t.Fatalf("pig never landed in %d ticks (y=%.4f)", maxTicks, pig.y)
	}
	maxHealth := float32(10.0)
	if pig.health != maxHealth {
		t.Fatalf("pig took fall damage on a short (<3 block) fall: health=%.2f, want full %.2f "+
			"(a fall under SAFE_FALL_DISTANCE=3.0 must deal 0 damage)", pig.health, maxHealth)
	}
}

// TestMobFallIntoWaterNoDamage: a pig dropped from height into a WATER column takes NO fall damage —
// the descent in water adds no fallDistance (Entity.checkFallDamage's !isInWater guard) AND the
// updateFluidInteraction per-tick water reset zeroes any pre-water distance. The pig keeps full
// health even after a tall fall, because it fell into water.
func TestMobFallIntoWaterNoDamage(t *testing.T) {
	const (
		floorY   = 40
		yLo      = floorY + 1 // water sits above the floor
		yHi      = 80         // a deep water column the pig falls into
		startY   = 100.0      // start ABOVE the water surface so the pig free-falls then enters water
		maxTicks = 300
	)
	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillWaterColumn(ch, yLo, yHi)
			fillFloor(ch, floorY)
		}
	}
	pig := NewEntity(901, entity.Pig, 8.5, startY, 8.5)
	pig.onGround = false
	initSpawnHealth(pig)
	loop.only().entities.add(pig)

	// Drive until the pig is in water (it entered the column), then a few more ticks. Once in water it
	// sinks slowly (travelInWaterVertical) and never lands hard — the point is it took no fall damage.
	entered := false
	for i := 0; i < maxTicks; i++ {
		loop.tickPhysics()
		if loop.mobInWater(pig) {
			entered = true
			// Run a handful more ticks to be sure no landing edge fires a fall hit.
			for j := 0; j < 10; j++ {
				loop.tickPhysics()
			}
			break
		}
	}
	if !entered {
		t.Fatalf("pig never entered the water column (y=%.4f) — the into-water test cannot assert", pig.y)
	}

	maxHealth := float32(10.0)
	if pig.health != maxHealth {
		t.Fatalf("pig took fall damage falling INTO water: health=%.2f, want full %.2f (a fall into "+
			"water must deal 0 damage — the descent in water adds no fallDistance)", pig.health, maxHealth)
	}
	if pig.fallDistance != 0 {
		t.Fatalf("pig fallDistance not zeroed in water: %.4f, want 0 (the updateFluidInteraction reset)",
			pig.fallDistance)
	}
}
