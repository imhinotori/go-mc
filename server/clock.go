package server

import "time"

// Clock is the tick loop's time source. The production implementation uses
// time.Now() (which carries a monotonic reading, immune to wall-clock/NTP jumps);
// tests inject a fake that returns caller-advanced instants so the accumulator,
// game-time anchor, and MSPT measurement are deterministically testable without
// any real time.Sleep (research Pitfall 3 — sleep jitter must never enter timing
// assertions).
type Clock interface {
	// Now returns the current instant. Successive readings must be monotonic
	// non-decreasing; the loop measures frame deltas via t2.Sub(t1).
	Now() time.Time
}

// systemClock is the real clock backing a running server. time.Now() embeds a
// monotonic reading, so Sub() between two readings is immune to wall-clock jumps.
type systemClock struct{}

// Now returns the current monotonic instant.
func (systemClock) Now() time.Time { return time.Now() }

// SystemClock returns the production Clock backing a running server (time.Now() with
// its monotonic reading). main() passes it to NewTickLoop; tests inject a fakeClock
// instead. It is the only exported way to obtain the real clock — the systemClock type
// stays unexported so the wall-clock source cannot be mistaken for an injectable one.
func SystemClock() Clock { return systemClock{} }
