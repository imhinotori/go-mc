package server

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// fluid_schedule_test.go covers GAMEPLAY-05 Task 1: the net-new scheduled-block-tick queue
// (deterministic drain), the jar-exact getLegacyLevel encoding, and the water level
// read/write round-trip. The constants and the source?0:(8-min(amount,8))+(falling?8:0)
// mapping are ported from temp/cache/26.2-inner.jar
// (javap net.minecraft.world.level.material.FlowingFluid.getLegacyLevel +
//  net.minecraft.world.level.material.WaterFluid getDropOff/getTickDelay/getSlopeFindDistance).

// TestGetLegacyLevel pins the jar-verified legacy-level encoding (FlowingFluid.getLegacyLevel):
//
//	source           -> 0
//	flowing amount a -> 8 - min(a,8)   (a=8 -> 0, a=7 -> 1, ... a=1 -> 7)
//	falling          -> +8             (amount 8 falling -> 8, amount 1 falling -> 15)
func TestGetLegacyLevel(t *testing.T) {
	cases := []struct {
		amount  int
		falling bool
		source  bool
		want    int
	}{
		{amount: 8, source: true, want: 0},     // a source is always legacy level 0
		{amount: 8, want: 0},                    // full flowing (amount 8) -> 0
		{amount: 7, want: 1},                    // each drop-off step adds 1
		{amount: 6, want: 2},
		{amount: 1, want: 7},                    // the shallowest flowing
		{amount: 8, falling: true, want: 8},     // falling full -> +8
		{amount: 1, falling: true, want: 15},    // falling shallow -> 7 + 8
	}
	for _, c := range cases {
		if got := getLegacyLevel(c.amount, c.falling, c.source); got != c.want {
			t.Errorf("getLegacyLevel(amount=%d, falling=%v, source=%v) = %d, want %d",
				c.amount, c.falling, c.source, got, c.want)
		}
	}
}

// TestWaterLevelRoundTrip: waterStateID(n) then waterLevelOf must round-trip the legacy level
// n, and a non-water StateID must report isWater=false.
func TestWaterLevelRoundTrip(t *testing.T) {
	for n := 0; n <= 15; n++ {
		id := waterStateID(n)
		got, isWater := waterLevelOf(id)
		if !isWater {
			t.Fatalf("waterLevelOf(waterStateID(%d)) reported non-water", n)
		}
		if got != n {
			t.Errorf("round-trip level %d -> %d", n, got)
		}
	}
	// A non-water block (air) is not water.
	if _, isWater := waterLevelOf(airStateID()); isWater {
		t.Errorf("waterLevelOf(air) reported water")
	}
}

// TestScheduleDrainDeterministic: three ticks scheduled at the same gametime in a shuffled
// order drain back sorted by packed pos (deterministic, run-to-run identical); a tick
// scheduled for a later gametime does NOT drain early.
func TestScheduleDrainDeterministic(t *testing.T) {
	q := newFluidScheduleQueue()

	// Schedule out of pos order at gametime 10.
	q.schedule(pk.Position{X: 5, Y: 64, Z: 1}, 10)
	q.schedule(pk.Position{X: 1, Y: 64, Z: 1}, 10)
	q.schedule(pk.Position{X: 3, Y: 64, Z: 2}, 10)
	// And one for a later gametime — must not drain at 10.
	q.schedule(pk.Position{X: 9, Y: 64, Z: 9}, 15)

	due := q.drainDue(10)
	if len(due) != 3 {
		t.Fatalf("drainDue(10) returned %d, want 3 (the future tick must not drain)", len(due))
	}

	// Deterministic: sorted by packed pos. Capture the order and confirm it is sorted and
	// reproducible.
	first := make([]pk.Position, len(due))
	for i, st := range due {
		first[i] = st.pos
	}
	for i := 1; i < len(first); i++ {
		if packPos(first[i-1]) > packPos(first[i]) {
			t.Fatalf("drain order not sorted by packed pos: %v then %v", first[i-1], first[i])
		}
	}

	// Re-run with a fresh queue, same shuffled inserts: identical drain order.
	q2 := newFluidScheduleQueue()
	q2.schedule(pk.Position{X: 3, Y: 64, Z: 2}, 10)
	q2.schedule(pk.Position{X: 5, Y: 64, Z: 1}, 10)
	q2.schedule(pk.Position{X: 1, Y: 64, Z: 1}, 10)
	due2 := q2.drainDue(10)
	if len(due2) != len(first) {
		t.Fatalf("second drain len %d != first %d", len(due2), len(first))
	}
	for i := range due2 {
		if due2[i].pos != first[i] {
			t.Fatalf("drain order not reproducible at %d: %v vs %v", i, due2[i].pos, first[i])
		}
	}

	// The drained bucket is gone: a second drainDue(10) yields nothing.
	if again := q.drainDue(10); len(again) != 0 {
		t.Fatalf("re-draining gametime 10 returned %d, want 0 (bucket must be deleted)", len(again))
	}

	// The future tick still drains at its own gametime.
	if late := q.drainDue(15); len(late) != 1 {
		t.Fatalf("drainDue(15) returned %d, want 1", len(late))
	}
}
