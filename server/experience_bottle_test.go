package server

// experience_bottle_test.go -- pins the ThrownExperienceBottle port: the 0.07 gravity override, and the
// onHit XP split (3 + nextInt(5) + nextInt(5)) that spawns collectible ExperienceOrbs at the impact.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/item"
)

// TestExperienceBottleGravity: a thrown experience bottle applies getDefaultGravity() == 0.07 each tick
// (heavier than the 0.03 throwable default). After one tick from a horizontal launch: vy' = (0 - 0.07)*drag.
func TestExperienceBottleGravity(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	// Spawn high so it never hits during the test; horizontal launch.
	e := loop.spawnThrowable(0, throwExperienceBottle, 8.5, float64(floorY+50), 8.5, 1.0, 0.0, 0.0)

	loop.tickThrowable(e)

	// Throwable order: gravity (0.07) then drag (0.99). vy starts 0 -> -0.07, then *0.99.
	wantVY := (0.0 - xpBottleGravity) * throwAirDrag
	if math.Abs(e.vy-wantVY) > 1e-9 {
		t.Fatalf("xp bottle vy after one tick = %v, want %v (gravity 0.07 then drag 0.99)", e.vy, wantVY)
	}
}

// TestExperienceBottleItemMapsToThrowable: the experience_bottle item maps to the throwExperienceBottle kind.
func TestExperienceBottleItemMapsToThrowable(t *testing.T) {
	kind, ok := itemToThrowableKind(int32(item.ExperienceBottle.ID))
	if !ok || kind != throwExperienceBottle {
		t.Fatalf("experience_bottle -> (%d, %v), want (throwExperienceBottle, true)", kind, ok)
	}
}

// TestExperienceBottleBreakSpawnsOrbs: when a thrown xp bottle breaks it splits into XP orbs whose total
// value equals 3 + nextInt(5) + nextInt(5), drawn in that ORDER on the bottle's own stream, and each orb is
// a collectible ExperienceOrb (isOrb) at the impact point. The bottle is then discarded.
func TestExperienceBottleBreakSpawnsOrbs(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	e := loop.spawnThrowable(0, throwExperienceBottle, 8.5, float64(floorY+1), 8.5, 0, 0, 0)

	// Reproduce the draw order from the same seed: 3 + nextInt(5) + nextInt(5).
	r := newEntityRandom(uint64(e.id))
	want := 3 + r.nextInt(5) + r.nextInt(5)

	loop.experienceBottleBreak(e)

	// Sum the spawned orb values.
	total := 0
	orbCount := 0
	for _, o := range loop.cur().entities.all() {
		if o.isOrb {
			total += o.xpValue
			orbCount++
		}
	}
	if total != want {
		t.Fatalf("xp orb total = %d, want %d (3 + nextInt(5) + nextInt(5))", total, want)
	}
	if orbCount == 0 {
		t.Fatal("no ExperienceOrb spawned by the broken xp bottle")
	}
	// The value is in the valid range 3..11 (3 + 0..4 + 0..4).
	if total < 3 || total > 11 {
		t.Fatalf("xp total %d out of [3, 11]", total)
	}
}

// TestExperienceBottleDiscardsOnHit: a thrown xp bottle that hits a block is discarded (broken).
func TestExperienceBottleDiscardsOnHit(t *testing.T) {
	loop, floorY := mobEffectEntityLoop(t)
	// Launch straight down into the floor so it hits a solid block next tick.
	e := loop.spawnThrowable(0, throwExperienceBottle, 8.5, float64(floorY+1)+0.5, 8.5, 0, -2.0, 0)

	loop.tickThrowable(e)

	if _, ok := loop.cur().entities.get(e.id); ok {
		t.Fatal("xp bottle was not discarded after hitting the floor")
	}
	// And it left orbs behind.
	found := false
	for _, o := range loop.cur().entities.all() {
		if o.isOrb {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("broken xp bottle left no ExperienceOrbs")
	}
}
