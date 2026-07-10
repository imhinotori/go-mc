package server

// area_effect_cloud_test.go -- pins the AreaEffectCloud port: the radius shrink (radiusPerTick == -0.005
// for a lingering cloud), the wait phase, the every-5-tick effect application, and the per-victim
// reapplication delay. Also pins the lingering-potion -> AEC spawn seam (ThrownLingeringPotion.onHitAsPotion).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestLingeringPotionSpawnsCloud: a thrown lingering potion, when it hits, spawns an AreaEffectCloud with
// the lingering shape (radius 3.0, duration 600, waitTime 10, radiusOnUse -0.5, radiusPerTick -0.005) and
// discards the potion.
func TestLingeringPotionSpawnsCloud(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	pot := loop.spawnLingeringPotion(0, 8.5, float64(floorY+1), 8.5, 0, 0, 0, []splashEffect{
		{id: effectPoison, duration: 100, amplifier: 0},
	})

	loop.splashPotion(pot) // the hit path routes a lingering potion to spawnLingeringCloud

	if _, ok := loop.cur().entities.get(pot.id); ok {
		t.Fatal("lingering potion was not discarded after spawning the cloud")
	}
	var cloud *Entity
	for _, e := range loop.cur().entities.all() {
		if e.isAreaEffectCloud {
			cloud = e
			break
		}
	}
	if cloud == nil {
		t.Fatal("no AreaEffectCloud spawned by the lingering potion")
	}
	if cloud.aecRadius != 3.0 {
		t.Fatalf("cloud radius = %v, want 3.0", cloud.aecRadius)
	}
	if cloud.aecDuration != 600 {
		t.Fatalf("cloud duration = %v, want 600", cloud.aecDuration)
	}
	if cloud.aecWaitTime != 10 {
		t.Fatalf("cloud waitTime = %v, want 10", cloud.aecWaitTime)
	}
	if cloud.aecRadiusOnUse != -0.5 {
		t.Fatalf("cloud radiusOnUse = %v, want -0.5", cloud.aecRadiusOnUse)
	}
	// radiusPerTick = -radius/duration = -3.0/600 = -0.005.
	if math.Abs(float64(cloud.aecRadiusPerTick)-(-0.005)) > 1e-7 {
		t.Fatalf("cloud radiusPerTick = %v, want -0.005", cloud.aecRadiusPerTick)
	}
}

// TestAECRadiusShrink: a cloud with radiusPerTick -0.005 and waitTime 0 shrinks its radius by 0.005 each
// active tick (after the wait phase). Starting radius 3.0, after one active tick -> 2.995.
func TestAECRadiusShrink(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	// waitTime 0 so the very first tick is active (nextWaiting = tickCount(1) < 0 == false).
	c := loop.spawnAreaEffectCloud(0, 8.5, float64(floorY+1), 8.5, nil,
		3.0, 600, 0, 20, 0, 0, -0.005)

	loop.tickAreaEffectCloud(c)

	if math.Abs(float64(c.aecRadius)-2.995) > 1e-6 {
		t.Fatalf("radius after one active tick = %v, want 2.995 (shrink -0.005)", c.aecRadius)
	}
}

// TestAECWaitPhase: while tickCount < waitTime the cloud does NOT shrink (it is waiting). A waitTime-5 cloud
// keeps its radius for the first 5 ticks, then begins shrinking.
func TestAECWaitPhase(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	c := loop.spawnAreaEffectCloud(0, 8.5, float64(floorY+1), 8.5, nil,
		3.0, 600, 5, 20, 0, 0, -0.005)

	// Ticks 1..5 are the wait phase (tickCount < 5 for ticks 1..4; tick 5 has tickCount==5 -> active).
	for i := 0; i < 4; i++ {
		loop.tickAreaEffectCloud(c)
		if c.aecRadius != 3.0 {
			t.Fatalf("radius during wait tick %d = %v, want unchanged 3.0", i+1, c.aecRadius)
		}
	}
	// Tick 5: tickCount becomes 5, nextWaiting = 5 < 5 == false -> now active, shrinks.
	loop.tickAreaEffectCloud(c)
	if math.Abs(float64(c.aecRadius)-2.995) > 1e-6 {
		t.Fatalf("radius on first active tick = %v, want 2.995", c.aecRadius)
	}
}

// TestAECAppliesEffectAndReapplyDelay: a cloud with a POISON payload applies it to a mob in range on a
// tickCount%5==0 pass, records the reapplication cooldown so it does NOT re-apply on the very next pass,
// and re-applies once the delay expires.
func TestAECAppliesEffectAndReapplyDelay(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	pig := mobEffectTestEntity(loop, entity.Pig, 8.5, float64(floorY+1), 8.5)
	// waitTime 0, no radius shrink, reapplicationDelay 20.
	c := loop.spawnAreaEffectCloud(0, 8.5, float64(floorY+1), 8.5,
		[]splashEffect{{id: effectPoison, duration: 100, amplifier: 0}},
		3.0, 600, 0, 20, 0, 0, 0)

	// Advance to the first application pass (tickCount == 5).
	for i := 0; i < 5; i++ {
		loop.tickAreaEffectCloud(c)
	}
	if !entityHasEffect(pig, effectPoison) {
		t.Fatal("AEC did not apply POISON to the mob in range on the tickCount%%5 pass")
	}
	// The pig is recorded as a victim with a cooldown expiring at tickCount(5)+20 == 25.
	if got, ok := c.aecVictims[pig.id]; !ok || got != 25 {
		t.Fatalf("victim cooldown = (%d, %v), want 25/true", got, ok)
	}
	// Clear the effect and run the NEXT pass (tickCount 10) -- still within the cooldown, must NOT reapply.
	delete(pig.mobEffects, effectPoison)
	for i := 0; i < 5; i++ {
		loop.tickAreaEffectCloud(c)
	}
	if entityHasEffect(pig, effectPoison) {
		t.Fatal("AEC re-applied POISON during the reapplication-delay cooldown")
	}
	// Run passes past the cooldown (tickCount reaches 25+): the victim entry expires and it re-applies.
	for i := 0; i < 20; i++ {
		loop.tickAreaEffectCloud(c)
	}
	if !entityHasEffect(pig, effectPoison) {
		t.Fatal("AEC did not re-apply POISON after the reapplication delay expired")
	}
}
