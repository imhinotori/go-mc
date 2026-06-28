package server

import (
	"bytes"
	"runtime"
	"strconv"
)

// goroutine_id.go provides curGoroutineID — a tiny helper to read the running goroutine's id from
// the runtime stack header. It exists ONLY to support the Phase-27 N=2 per-goroutine current-region
// resolution (region.go's currentRegion map): each region's fan-out goroutine registers ITSELF as
// the current region for the duration of its tick, so the ~200 existing `t.only()` call sites in the
// per-region phases (physics/AI/spawner/etc.) route to the OWNING region's store WITHOUT being
// rewritten. This is the lowest-churn way to scope the single-owner store per region across the conc
// fan-out (an explicit thread-through of *region into 200 call sites would be a vastly larger,
// error-prone change with no behavior benefit).
//
// The id is parsed from the first line of runtime.Stack ("goroutine N [running]: ..."). This is the
// well-known Go idiom for a goroutine-local; it is called exactly ONCE per region per tick (at
// region.tick entry/exit and inside only()'s lookup) — a handful of times per tick total — so the
// cost is negligible relative to the tick budget. It is NEVER used for game logic, only to key the
// current-region map so the store resolves correctly per fan-out goroutine.

// goroutineStackHeader is the fixed prefix runtime.Stack writes before the goroutine number.
var goroutineStackHeader = []byte("goroutine ")

// curGoroutineID returns the running goroutine's numeric id. It reads only the small stack header
// (runtime.Stack with a tiny buffer and all=false), so it does not walk the whole stack.
func curGoroutineID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	b := buf[:n]
	b = bytes.TrimPrefix(b, goroutineStackHeader)
	// b now begins with the decimal id followed by a space.
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		return 0
	}
	id, err := strconv.ParseInt(string(b[:i]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
