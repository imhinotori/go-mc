package server

import (
	"reflect"
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// plugin_mob_race_test.go — PLUGIN-03 (Wave 2): the declared-mob -race gate. A declared mob ticking
// (the goal callback reads + the nav mutates + the async A* path compute) must be race-clean, and NO
// handle may escape holding a live *Entity. This file is meaningful only under `-race` (Docker
// CGO=1) — the assertions hold trivially without it, but the data-race detector proves the tick
// path + the handle re-resolution discipline are concurrency-safe.

// TestDeclaredMobRace spawns the wander mob and drives the FULL tick pipeline repeatedly. The real
// cross-goroutine boundary in a declared mob's tick is the async pathfinding pool (OPT-01): the
// MOVE goal's tick sets a nav target, serverAiStep SUBMITS the pure computePath to the pathPool (a
// real worker goroutine) over an IMMUTABLE snapshot copied ON the tick, and the result rejoins via
// applyAsyncResults on the owner. tickOnce here exercises that submit -> off-tick compute -> owner
// apply round trip every tick. Under -race this proves the declared-mob path is race-clean: the
// goal callback reads + the nav mutate run on the OWNER (single-owner TICK-05), and the only thing
// crossing the goroutine boundary is the immutable snapshot + the id-carrying pathReady (never a
// live *Entity). The store is tick-owned (NOT concurrent), so this test does NOT read it off-tick —
// it drives only the legitimate owner+pool concurrency the production loop uses.
func TestDeclaredMobRace(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	for cx := 0; cx <= 2; cx++ {
		ch := putChunk(mgr, level.ChunkPos{int32(cx), 0})
		fillFloor(ch, floorY)
	}

	r := loadMobRegistry(t, mobpluginsRoot)
	decl := r.byName["wanderer"]
	e := loop.spawnDeclaredMob(decl, 2.5, float64(floorY+1), 2.5)

	// Drive the full pipeline: every tick the goal may submit an A* compute to the real pathPool
	// goroutine and the owner applies the immutable result — the genuine declared-mob concurrency.
	for i := 0; i < 200; i++ {
		loop.tickOnce()
	}

	// A mob that walked across a region seam transfers to the other region's store, so resolve across
	// ALL regions (owningRegion), not just loop.only() — a legitimately region-crossing mob is not "gone".
	if loop.owningRegion(e.id) == nil {
		t.Fatalf("the declared mob vanished from every region store during the race run")
	}
}

// TestHandlesHoldNoLivePointer is the STRUCTURAL no-escape gate: the entity/world/nav handles must
// carry an id (int32) + *TickLoop + capSet, NEVER a *Entity field. A handle stashed in a Starlark
// module global and read off-tick must only ever re-resolve the store on the owner — a *Entity field
// would let a live pointer escape (T-23-09). This reflects over the handle structs and fails if any
// field is a *Entity (or *server.Entity). It is a compile-time-stable invariant the -race run backs.
func TestHandlesHoldNoLivePointer(t *testing.T) {
	entityPtr := reflect.TypeOf((*Entity)(nil))
	for _, h := range []any{
		&entityHandle{},
		&worldHandle{},
		&navHandle{},
	} {
		st := reflect.TypeOf(h).Elem()
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			if f.Type == entityPtr {
				t.Fatalf("%s.%s is a *Entity — a handle must store an id, never a live *Entity (T-23-09)", st.Name(), f.Name)
			}
		}
	}
}

// TestStarlarkGoalHoldsNoLivePointer extends the no-escape gate to the starlarkGoal struct: it must
// not stash a live *Entity either (it receives the *Entity per call from the selector and builds
// fresh id-based handles; it never retains one). A *Entity field would let the goal hold a live
// pointer across ticks.
func TestStarlarkGoalHoldsNoLivePointer(t *testing.T) {
	entityPtr := reflect.TypeOf((*Entity)(nil))
	st := reflect.TypeOf(&starlarkGoal{}).Elem()
	for i := 0; i < st.NumField(); i++ {
		f := st.Field(i)
		if f.Type == entityPtr {
			t.Fatalf("starlarkGoal.%s is a *Entity — a goal must not retain a live *Entity (T-23-09)", f.Name)
		}
	}
}
