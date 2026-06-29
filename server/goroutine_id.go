package server

import "github.com/petermattis/goid"

// goroutine_id.go provides curGoroutineID — the goroutine-local key for the Phase-27 N=2
// per-goroutine current-region resolution (region.go's currentRegion map): each region's fan-out
// goroutine registers ITSELF as the current region for the duration of its tick, so the ~170 existing
// `t.only()` call sites in the per-region phases (physics/AI/spawner/etc.) route to the OWNING
// region's store WITHOUT being rewritten.
//
// PERF (regression fix): only() is NOT called "a handful of times per tick" — it is on the HOTTEST
// path (every block read in clipAxis/boxOverlapsSolid during tickPhysics calls it), i.e. millions of
// times per tick. The previous implementation read the id from runtime.Stack(), which forces a FULL
// goroutine stack traceback on EVERY call (the small buffer only truncates the OUTPUT, not the work).
// A CPU profile showed only()->runtime.Stack() at ~73% of the tick budget (~500ms ticks). goid.Get()
// reads the goroutine id directly from the g struct via a per-Go-version field offset (assembly /
// linkname, no traceback) in ~1ns — the standard fast goroutine-local technique (the same approach
// xsync and other concurrency libs use). It is pure-Go (CGO_ENABLED=0 preserved) and is used ONLY to
// key the current-region map, never for game logic.

// curGoroutineID returns the running goroutine's numeric id (a fast g-struct offset read, no stack
// traceback).
func curGoroutineID() int64 {
	return goid.Get()
}
