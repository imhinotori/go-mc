package ticks

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// fired records the order ticks are dispatched, so a test can assert the deterministic drain
// order (triggerTick, priority, subTickOrder).
type fired struct {
	pos pk.Position
	typ string
}

func collect(out *[]fired) func(pos pk.Position, typ string) {
	return func(pos pk.Position, typ string) { *out = append(*out, fired{pos, typ}) }
}

// allLoaded is the always-tickable chunk gate.
func allLoaded(int64) bool { return true }

// posIn returns a block pos inside chunk (0,0) at distinct positions so ticks are unique.
func posAt(x, y, z int) pk.Position { return pk.Position{X: x, Y: y, Z: z} }

// TestDrainOrderWithinTick asserts that ticks all due at the SAME triggerTick drain in
// (priority, subTickOrder) order — the DRAIN_ORDER comparator: higher priority (lower ordinal)
// first, ties broken by schedule order (subTickOrder). CITE: ScheduledTick.DRAIN_ORDER.
func TestDrainOrderWithinTick(t *testing.T) {
	lt := NewLevelTicks[string](allLoaded)
	c := NewLevelChunkTicks[string]()
	lt.AddContainer(0, 0, c)

	// Schedule, at the SAME triggerTick=5, in a deliberately SCRAMBLED order:
	//   A: NORMAL,  subTick 0
	//   B: HIGH,    subTick 1  (higher priority -> should fire before A)
	//   C: NORMAL,  subTick 2  (same priority as A, later subTick -> after A)
	//   D: EXTREMELY_HIGH, subTick 3 (highest -> first overall)
	//   E: LOW,     subTick 4  (lowest -> last)
	lt.Schedule(NewScheduledTick("a", posAt(0, 0, 0), 5, PriorityNormal, 0))
	lt.Schedule(NewScheduledTick("b", posAt(1, 0, 0), 5, PriorityHigh, 1))
	lt.Schedule(NewScheduledTick("c", posAt(2, 0, 0), 5, PriorityNormal, 2))
	lt.Schedule(NewScheduledTick("d", posAt(3, 0, 0), 5, PriorityExtremelyHigh, 3))
	lt.Schedule(NewScheduledTick("e", posAt(4, 0, 0), 5, PriorityLow, 4))

	var out []fired
	lt.Tick(5, 65536, collect(&out))

	want := []string{"d", "b", "a", "c", "e"} // EXTREMELY_HIGH, HIGH, NORMAL(sub0), NORMAL(sub2), LOW
	if len(out) != len(want) {
		t.Fatalf("fired %d ticks, want %d: %+v", len(out), len(want), out)
	}
	for i := range want {
		if out[i].typ != want[i] {
			t.Fatalf("drain order[%d] = %q, want %q (full: %+v)", i, out[i].typ, want[i], out)
		}
	}
}

// TestTriggerTickOrderingAcrossTicks asserts an EARLIER triggerTick fires before a later one
// regardless of priority, and that a not-yet-due tick is held back. CITE: DRAIN_ORDER triggerTick
// is the primary key; LevelTicks.sortContainersToTick only collects due <= gameTime.
func TestTriggerTickOrderingAcrossTicks(t *testing.T) {
	lt := NewLevelTicks[string](allLoaded)
	c := NewLevelChunkTicks[string]()
	lt.AddContainer(0, 0, c)

	// A due at tick 5 (LOW priority), B due at tick 6 (EXTREMELY_HIGH). At gameTime 5 only A fires
	// even though B has a higher priority — triggerTick dominates.
	lt.Schedule(NewScheduledTick("a", posAt(0, 0, 0), 5, PriorityLow, 0))
	lt.Schedule(NewScheduledTick("b", posAt(1, 0, 0), 6, PriorityExtremelyHigh, 1))

	var out []fired
	lt.Tick(5, 65536, collect(&out))
	if len(out) != 1 || out[0].typ != "a" {
		t.Fatalf("gameTime 5: expected only [a], got %+v", out)
	}

	out = nil
	lt.Tick(6, 65536, collect(&out))
	if len(out) != 1 || out[0].typ != "b" {
		t.Fatalf("gameTime 6: expected only [b], got %+v", out)
	}
}

// TestInterChunkDrainOrder asserts the GLOBAL drain order interleaves ticks across DIFFERENT
// chunks by (triggerTick, priority, subTickOrder) — not chunk-by-chunk. CITE:
// LevelTicks.drainFromCurrentContainer (INTRA_TICK_DRAIN_ORDER yield between containers).
func TestInterChunkDrainOrder(t *testing.T) {
	lt := NewLevelTicks[string](allLoaded)
	c0 := NewLevelChunkTicks[string]()
	c1 := NewLevelChunkTicks[string]()
	lt.AddContainer(0, 0, c0) // chunk (0,0): block x in [0,15]
	lt.AddContainer(1, 0, c1) // chunk (1,0): block x in [16,31]

	// chunk0: a (NORMAL, sub 0), chunk1: b (HIGH, sub 1). Both triggerTick 3. HIGH (b) must fire
	// before NORMAL (a) even though they live in different chunks.
	lt.Schedule(NewScheduledTick("a", posAt(0, 0, 0), 3, PriorityNormal, 0))
	lt.Schedule(NewScheduledTick("b", posAt(16, 0, 0), 3, PriorityHigh, 1))

	var out []fired
	lt.Tick(3, 65536, collect(&out))
	want := []string{"b", "a"}
	if len(out) != 2 || out[0].typ != want[0] || out[1].typ != want[1] {
		t.Fatalf("inter-chunk drain order = %+v, want %v", out, want)
	}
}

// TestMaxAllowedTicksCap asserts that at most maxAllowedTicks fire in one drain, and the
// leftover is carried to the next game-time (rescheduleLeftoverContainers). CITE:
// LevelTicks.canScheduleMoreTicks / rescheduleLeftoverContainers.
func TestMaxAllowedTicksCap(t *testing.T) {
	lt := NewLevelTicks[string](allLoaded)
	c := NewLevelChunkTicks[string]()
	lt.AddContainer(0, 0, c)

	// 10 ticks all due at tick 1, distinct positions, ascending subTickOrder.
	for i := 0; i < 10; i++ {
		lt.Schedule(NewScheduledTick("x", posAt(i, 0, 0), 1, PriorityNormal, int64(i)))
	}

	var out []fired
	lt.Tick(1, 3, collect(&out)) // cap = 3
	if len(out) != 3 {
		t.Fatalf("cap=3: fired %d, want 3", len(out))
	}
	// The first 3 by subTickOrder fire (positions x=0,1,2).
	for i := 0; i < 3; i++ {
		if out[i].pos.X != i {
			t.Fatalf("capped drain[%d].X = %d, want %d", i, out[i].pos.X, i)
		}
	}

	// Drain the remaining 7 over subsequent ticks at the SAME game-time (still due). They were
	// rescheduled to retry, so a second Tick at gameTime 1 fires the next batch.
	out = nil
	lt.Tick(1, 65536, collect(&out))
	if len(out) != 7 {
		t.Fatalf("leftover: fired %d, want 7", len(out))
	}
	if lt.Count() != 0 {
		t.Fatalf("after full drain, Count = %d, want 0", lt.Count())
	}
}

// TestStaleAndDedup asserts (a) scheduling the SAME (type,pos) twice dedups to one tick, and
// (b) a tick survives in the queue until its triggerTick. CITE: LevelChunkTicks.schedule dedup.
func TestStaleAndDedup(t *testing.T) {
	lt := NewLevelTicks[string](allLoaded)
	c := NewLevelChunkTicks[string]()
	lt.AddContainer(0, 0, c)

	lt.Schedule(NewScheduledTick("a", posAt(2, 0, 2), 4, PriorityNormal, 0))
	lt.Schedule(NewScheduledTick("a", posAt(2, 0, 2), 4, PriorityNormal, 1)) // dup (type,pos)
	if got := lt.Count(); got != 1 {
		t.Fatalf("dedup: Count = %d, want 1", got)
	}
	if !lt.HasScheduledTick(posAt(2, 0, 2), "a") {
		t.Fatal("HasScheduledTick should be true for scheduled (a, pos)")
	}

	var out []fired
	lt.Tick(3, 65536, collect(&out)) // not due yet (triggerTick 4)
	if len(out) != 0 {
		t.Fatalf("gameTime 3: nothing due, got %+v", out)
	}
	lt.Tick(4, 65536, collect(&out))
	if len(out) != 1 {
		t.Fatalf("gameTime 4: expected 1 fire, got %+v", out)
	}
}

// TestPackUnpackRoundTrip asserts a chunk container's live ticks pack to SavedTicks (relative
// delays) and unpack back to absolute triggerTicks at a new game-time anchor. CITE:
// LevelChunkTicks.pack / unpack; SavedTick.toSavedTick / unpack.
func TestPackUnpackRoundTrip(t *testing.T) {
	c := NewLevelChunkTicks[string]()
	c.Schedule(NewScheduledTick("a", posAt(0, 0, 0), 105, PriorityHigh, 0))
	c.Schedule(NewScheduledTick("b", posAt(1, 0, 0), 110, PriorityNormal, 1))

	// Pack at gameTime 100: delays become 5 and 10.
	saved := c.Pack(100)
	if len(saved) != 2 {
		t.Fatalf("packed %d, want 2", len(saved))
	}
	// pack sorts by subTickOrder, so a(sub0) then b(sub1).
	if saved[0].Delay != 5 || saved[1].Delay != 10 {
		t.Fatalf("packed delays = %d,%d, want 5,10", saved[0].Delay, saved[1].Delay)
	}
	if saved[0].Priority != PriorityHigh {
		t.Fatalf("packed[0].Priority = %v, want High", saved[0].Priority)
	}

	// Load into a fresh container and unpack at a DIFFERENT anchor gameTime 200: absolute
	// triggerTicks become 205 and 210.
	c2 := NewLevelChunkTicksFromSaved(saved)
	if c2.Count() != 0 {
		t.Fatalf("before unpack Count = %d, want 0 (pending, not live)", c2.Count())
	}
	c2.Unpack(200)
	if c2.Count() != 2 {
		t.Fatalf("after unpack Count = %d, want 2", c2.Count())
	}
	head, _ := c2.Poll()
	if head.TriggerTick != 205 {
		t.Fatalf("unpacked head triggerTick = %d, want 205", head.TriggerTick)
	}
	next, _ := c2.Poll()
	if next.TriggerTick != 210 {
		t.Fatalf("unpacked next triggerTick = %d, want 210", next.TriggerTick)
	}
}

// TestPriorityValueRoundTrip asserts TickPriority.Value / PriorityByValue mirror the vanilla
// codec's getValue/byValue (value = ordinal - 3). CITE: TickPriority.getValue / byValue.
func TestPriorityValueRoundTrip(t *testing.T) {
	cases := []struct {
		p   TickPriority
		val int
	}{
		{PriorityExtremelyHigh, -3}, {PriorityVeryHigh, -2}, {PriorityHigh, -1},
		{PriorityNormal, 0}, {PriorityLow, 1}, {PriorityVeryLow, 2}, {PriorityExtremelyLow, 3},
	}
	for _, c := range cases {
		if c.p.Value() != c.val {
			t.Fatalf("%v.Value() = %d, want %d", c.p, c.p.Value(), c.val)
		}
		if PriorityByValue(c.val) != c.p {
			t.Fatalf("PriorityByValue(%d) = %v, want %v", c.val, PriorityByValue(c.val), c.p)
		}
	}
	// Out-of-range clamps: below -3 -> EXTREMELY_HIGH; above 3 -> EXTREMELY_LOW.
	if PriorityByValue(-100) != PriorityExtremelyHigh {
		t.Fatal("byValue(-100) should clamp to EXTREMELY_HIGH")
	}
	if PriorityByValue(100) != PriorityExtremelyLow {
		t.Fatal("byValue(100) should clamp to EXTREMELY_LOW")
	}
}

// TestUnloadedChunkScheduleDropped asserts a tick scheduled for a chunk with no container is
// dropped (vanilla logs-and-returns). CITE: LevelTicks.schedule null-container branch.
func TestUnloadedChunkScheduleDropped(t *testing.T) {
	lt := NewLevelTicks[string](allLoaded)
	// No AddContainer for chunk (0,0).
	lt.Schedule(NewScheduledTick("a", posAt(0, 0, 0), 1, PriorityNormal, 0))
	if lt.Count() != 0 {
		t.Fatalf("schedule into unloaded chunk should drop; Count = %d", lt.Count())
	}
	var out []fired
	lt.Tick(1, 65536, collect(&out))
	if len(out) != 0 {
		t.Fatalf("no container -> nothing fires, got %+v", out)
	}
}

// TestTickCheckGate asserts a chunk whose tickCheck returns false is NOT drained, and its ticks
// are retried once the gate opens. CITE: LevelTicks.sortContainersToTick tickCheck guard.
func TestTickCheckGate(t *testing.T) {
	open := false
	lt := NewLevelTicks[string](func(int64) bool { return open })
	c := NewLevelChunkTicks[string]()
	lt.AddContainer(0, 0, c)
	lt.Schedule(NewScheduledTick("a", posAt(0, 0, 0), 1, PriorityNormal, 0))

	var out []fired
	lt.Tick(1, 65536, collect(&out)) // gate closed
	if len(out) != 0 {
		t.Fatalf("gate closed: expected no fire, got %+v", out)
	}
	if lt.Count() != 1 {
		t.Fatalf("gate closed: tick should remain queued; Count = %d", lt.Count())
	}

	open = true
	lt.Tick(2, 65536, collect(&out)) // gate open, still due (triggerTick 1 <= 2)
	if len(out) != 1 || out[0].typ != "a" {
		t.Fatalf("gate open: expected [a], got %+v", out)
	}
}
