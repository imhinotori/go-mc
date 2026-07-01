package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestIgniteAndFireCountdown: igniteForSeconds sets the countdown (floor(s*20)); tickEntityFire
// decrements it and deals 1 damage every 20 ticks; it extinguishes at 0.
func TestIgniteAndFireCountdown(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Zombie, 8.5, 100, 8.5)
	e.health = 20

	loop.igniteForSeconds(e, 8.0)
	if e.remainingFireTicks != 160 {
		t.Fatalf("igniteForSeconds(8) -> remainingFireTicks %d, want 160", e.remainingFireTicks)
	}

	// A shorter ignite must NOT shorten an active longer burn (igniteForTicks only extends).
	loop.igniteForSeconds(e, 1.0)
	if e.remainingFireTicks != 160 {
		t.Fatalf("a shorter ignite shortened the burn to %d, want 160 (igniteForTicks never shortens)", e.remainingFireTicks)
	}

	startHP := e.health
	// Tick once: remainingFireTicks%20==0 (160) → 1 fire damage, then decrement to 159.
	loop.tickEntityFire(e)
	if e.remainingFireTicks != 159 {
		t.Fatalf("after one tick remainingFireTicks %d, want 159", e.remainingFireTicks)
	}
	if e.health >= startHP {
		t.Fatalf("no fire damage on the %%20==0 tick (health %v -> %v)", startHP, e.health)
	}

	// Run out the burn; it must reach 0 and stop.
	for i := 0; i < 200 && e.remainingFireTicks > 0; i++ {
		loop.tickEntityFire(e)
	}
	if e.remainingFireTicks != 0 {
		t.Fatalf("fire never extinguished: remainingFireTicks %d", e.remainingFireTicks)
	}
	// A non-burning entity is a no-op (no panic, no damage).
	loop.tickEntityFire(e)
}

// TestSunSensitiveGate: only zombie/skeleton are sun-sensitive; a pig/cow is not (so sunBurnTick
// never draws RNG on them — the pig-oracle guard).
func TestSunSensitiveGate(t *testing.T) {
	z := NewEntity(1, entity.Zombie, 0, 0, 0)
	s := NewEntity(2, entity.Skeleton, 0, 0, 0)
	p := NewEntity(3, entity.Pig, 0, 0, 0)
	if !isSunSensitive(z) || !isSunSensitive(s) {
		t.Error("zombie + skeleton must be sun-sensitive")
	}
	if isSunSensitive(p) {
		t.Error("pig must NOT be sun-sensitive (pig-oracle RNG guard)")
	}
}

// TestIsDayWindow: daytime is outside the [13000,23000) night window.
func TestIsDayWindow(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 6000 // noon
	if !loop.isDay() {
		t.Error("gametime 6000 (noon) should be day")
	}
	loop.gametime = 18000 // midnight
	if loop.isDay() {
		t.Error("gametime 18000 (midnight) should be night")
	}
}
