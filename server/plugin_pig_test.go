package server

// plugin_pig_test.go — PLUGIN-04 (Plan 24-02): THE GATE for the FIRST 1:1 vanilla-mob dogfood. It
// proves the bundled vanilla_pig plugin (the 3 passive goals re-expressed 1:1) is behavior-identical
// to the Go-native pig it replaces:
//
//   - TestPluginPigBootLoads      — the embedded plugin boot-loads into a registry; spawnVanillaPig
//                                   builds a live pig (entity.Pig.ID, 3 goals @6/@7/@8).
//   - TestVanillaPigDeclaresGoalSet — the declaration captures the 3 goals at the jar priorities+flags.
//   - TestVanillaPigGoalsPorted   — @8 carries requiresUpdateEveryTick (FIDELITY GAP 1 fix).
//   - TestVanillaPigStrollSetsTarget — the plugin stroll goal sets a wantTarget within ±10/±7.
//   - TestVanillaPigLooksAtPlayer — the plugin lookAt goal faces the nearest player via set_look_at.
//   - TestPluginPigEqualsGoNativePig — THE proof: a Go pig (newPigAI) and a plugin pig
//                                   (spawnVanillaPig) with the SAME id-derived seed + the SAME world/
//                                   player inputs produce the IDENTICAL observable sequence (wantTarget,
//                                   yaw, position) over many ticks. Passes ONLY if the goals are ported
//                                   1:1, the draw order matches, and set_look_at writes exactly what
//                                   the Go goal wrote.

import (
	"math"
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// --- boot-load + declaration -------------------------------------------------------------------

// TestPluginPigBootLoads: the embedded vanilla_pig plugin boot-loads (LoadVanillaPigRegistry) and a
// spawnVanillaPig builds a live pig that renders as entity.Pig.ID with a non-nil AI holding the 3
// goals at @6/@7/@8.
func TestPluginPigBootLoads(t *testing.T) {
	loop, mgr := newPhysicsLoop() // installs the vanilla_pig registry
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	if loop.mobRegistry == nil {
		t.Fatal("newPhysicsLoop did not install a mob registry")
	}
	if _, ok := loop.mobRegistry.byName[vanillaPigMobName]; !ok {
		t.Fatalf("registry has no %q declaration after boot-load", vanillaPigMobName)
	}

	pig := loop.spawnVanillaPig(8.5, float64(floorY+1), 8.5)
	if pig.typ != entity.Pig.ID {
		t.Fatalf("plugin pig typ = %d, want entity.Pig.ID %d (custom = behavior, not a new wire type)", pig.typ, entity.Pig.ID)
	}
	if pig.ai == nil {
		t.Fatal("plugin pig has no AI")
	}
	if got := len(pig.ai.goals.goals); got != 3 {
		t.Fatalf("plugin pig has %d goals, want 3 (@6/@7/@8)", got)
	}
	priorities := map[int]bool{}
	for _, wg := range pig.ai.goals.goals {
		priorities[wg.priority] = true
	}
	for _, p := range []int{6, 7, 8} {
		if !priorities[p] {
			t.Fatalf("plugin pig missing a goal at priority %d (got %v)", p, priorities)
		}
	}
	if _, ok := loop.only().entities.get(pig.id); !ok {
		t.Fatal("spawnVanillaPig did not add the pig to the tick-owned store")
	}
}

// TestVanillaPigDeclaresGoalSet: the boot-loaded declaration captures the 3 passive goals at the EXACT
// jar priorities + flags (RandomStroll@6 MOVE, LookAtPlayer@7 LOOK, RandomLookAround@8 MOVE|LOOK).
func TestVanillaPigDeclaresGoalSet(t *testing.T) {
	r, err := loadVanillaPigRegistry()
	if err != nil {
		t.Fatalf("loadVanillaPigRegistry: %v", err)
	}
	decl, ok := r.byName[vanillaPigMobName]
	if !ok {
		t.Fatalf("no %q declaration captured", vanillaPigMobName)
	}
	if decl.baseType.ID != entity.Pig.ID {
		t.Fatalf("base type = %d, want pig %d", decl.baseType.ID, entity.Pig.ID)
	}
	// Jar attributes (Pig.createAttributes): max_health 10.0, movement_speed 0.25.
	if decl.attrs["max_health"] != 10.0 {
		t.Fatalf("max_health = %v, want 10.0 (Pig.createAttributes)", decl.attrs["max_health"])
	}
	if decl.attrs["movement_speed"] != 0.25 {
		t.Fatalf("movement_speed = %v, want 0.25 (Pig.createAttributes)", decl.attrs["movement_speed"])
	}
	if len(decl.goals) != 3 {
		t.Fatalf("captured %d goals, want 3", len(decl.goals))
	}
	byPriority := map[int]goalDecl{}
	for _, g := range decl.goals {
		byPriority[g.priority] = g
	}
	// @6 MOVE
	if g, ok := byPriority[6]; !ok || g.flags != flagMove {
		t.Fatalf("@6 flags = %b, want flagMove %b (WaterAvoidingRandomStrollGoal)", g.flags, flagMove)
	}
	// @7 LOOK
	if g, ok := byPriority[7]; !ok || g.flags != flagLook {
		t.Fatalf("@7 flags = %b, want flagLook %b (LookAtPlayerGoal)", g.flags, flagLook)
	}
	// @8 MOVE|LOOK
	if g, ok := byPriority[8]; !ok || g.flags != (flagMove|flagLook) {
		t.Fatalf("@8 flags = %b, want flagMove|flagLook %b (RandomLookAroundGoal)", g.flags, flagMove|flagLook)
	}
}

// TestVanillaPigGoalsPorted: @8 RandomLookAroundGoal carries requiresUpdateEveryTick=true threaded
// from the declaration into the starlarkGoal (FIDELITY GAP 1, flagged by 24-01) — and @6/@7 do NOT.
func TestVanillaPigGoalsPorted(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	pig := loop.spawnVanillaPig(8.5, float64(floorY+1), 8.5)
	byPriority := map[int]*starlarkGoal{}
	for _, wg := range pig.ai.goals.goals {
		sg, ok := wg.g.(*starlarkGoal)
		if !ok {
			t.Fatalf("@%d is %T, want *starlarkGoal", wg.priority, wg.g)
		}
		byPriority[wg.priority] = sg
	}
	if !byPriority[8].requiresUpdateEveryTick() {
		t.Fatal("@8 RandomLookAround must have requiresUpdateEveryTick=true (jar-confirmed); the decl thread is broken")
	}
	if byPriority[6].requiresUpdateEveryTick() {
		t.Fatal("@6 stroll must NOT requiresUpdateEveryTick (jar default false)")
	}
	if byPriority[7].requiresUpdateEveryTick() {
		t.Fatal("@7 lookAt must NOT requiresUpdateEveryTick (jar default false)")
	}
}

// --- the ported-goal behavior (the plugin analogues of the Go-goal tests) ----------------------

// TestVanillaPigStrollSetsTarget: with the stroll goal forced to fire (probability-1 by exhausting
// the RNG to a 0 gate), the plugin stroll goal sets a wantTarget within the ±10/±7 radius via
// nav.path_to — the plugin analogue of TestRandomStrollSetsTarget.
func TestVanillaPigStrollSetsTarget(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	for cx := -1; cx <= 1; cx++ {
		ch := putChunk(mgr, level.ChunkPos{int32(cx), 0})
		fillFloor(ch, floorY)
	}
	pig := loop.spawnVanillaPig(8.5, float64(floorY+1), 8.5)
	startX, startY, startZ := pig.x, pig.y, pig.z

	// Drive serverAiStep until the stroll goal's 1-in-120 gate eventually fires and a wantTarget is
	// set (a generous tick budget; the seeded RNG guarantees it fires within a few hundred ticks).
	var fired bool
	for i := 0; i < 4000 && !fired; i++ {
		pig.ai.serverAiStep(loop, pig)
		if pig.ai.hasTarget {
			fired = true
		}
	}
	if !fired {
		t.Fatal("the plugin stroll goal never set a wantTarget over 4000 ticks")
	}
	if math.Abs(pig.ai.wantX-startX) > 10 || math.Abs(pig.ai.wantZ-startZ) > 10 || math.Abs(pig.ai.wantY-startY) > 7 {
		t.Fatalf("plugin stroll wantTarget (%v,%v,%v) outside ±10/±7 of start (%v,%v,%v)",
			pig.ai.wantX, pig.ai.wantY, pig.ai.wantZ, startX, startY, startZ)
	}
}

// TestVanillaPigLooksAtPlayer: with a player in range the plugin lookAt goal faces the pig toward the
// player via set_look_at (headYaw/yaw = yawTowardDeg) — the plugin analogue of
// TestLookAtPlayerFacesNearest. It drives serverAiStep until the lookAt goal runs and writes the yaw.
func TestVanillaPigLooksAtPlayer(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	pig := loop.spawnVanillaPig(8.5, float64(floorY+1), 8.5)
	// A player due east (+X) within the 6.0 look distance.
	loop.players = append(loop.players, &tickPlayer{x: pig.x + 4, y: pig.y, z: pig.z})

	want := yawTowardDeg(4, 0) // facing +X
	var faced bool
	for i := 0; i < 4000 && !faced; i++ {
		pig.ai.serverAiStep(loop, pig)
		if math.Abs(float64(pig.headYaw-want)) <= 0.001 && pig.headYaw != 0 {
			faced = true
		}
	}
	if !faced {
		t.Fatalf("the plugin lookAt goal never faced the player over 4000 ticks (headYaw=%v, want %v)", pig.headYaw, want)
	}
}

// --- THE behavior-identical proof --------------------------------------------------------------

// pigObservation is the observable per-tick state both pigs must produce identically.
type pigObservation struct {
	hasTarget          bool
	wantX, wantY, wantZ float64
	yaw, headYaw       float32
	x, y, z            float64
}

// TestPluginPigEqualsGoNativePig: a Go-native pig (newPigAI) and a plugin pig (spawnVanillaPig) with
// the SAME entity id (=> the SAME reseed-derived RNG seed) and the SAME world + player inputs, driven
// through serverAiStep for N ticks, produce the IDENTICAL observable sequence. This passes ONLY if the
// plugin goals are ported 1:1 (same priorities/flags), the RNG draw ORDER matches the Go oracle, and
// set_look_at writes exactly what the Go look goals wrote.
func TestPluginPigEqualsGoNativePig(t *testing.T) {
	const floorY = 64
	const pigID = 4242 // same id for both => same reseedMobAI seed => same RNG stream

	// Two independent loops with identical worlds + an identical player, so the only difference is the
	// AI builder (Go newPigAI vs the plugin declaration). The player sits east of the pig within look
	// range so the lookAt goal fires for both.
	build := func() (*TickLoop, *Entity) {
		loop, mgr := newPhysicsLoop()
		for cx := -1; cx <= 1; cx++ {
			for cz := -1; cz <= 1; cz++ {
				ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
				fillFloor(ch, floorY)
			}
		}
		loop.players = append(loop.players, &tickPlayer{x: 11.5, y: float64(floorY + 1), z: 8.5})
		return loop, nil
	}

	// The Go-native oracle pig.
	goLoop, _ := build()
	goPig := NewEntity(pigID, entity.Pig, 8.5, float64(floorY+1), 8.5)
	goPig.ai = newPigAI()
	reseedMobAI(goPig.ai, goPig.id)
	goLoop.only().entities.add(goPig)

	// The plugin pig — same id (so reseedMobAI gives the identical seed), same start pos.
	plLoop, _ := build()
	plPig := plLoop.spawnVanillaPigWithID(pigID, 8.5, float64(floorY+1), 8.5)

	observe := func(e *Entity) pigObservation {
		return pigObservation{
			hasTarget: e.ai.hasTarget,
			wantX:     e.ai.wantX, wantY: e.ai.wantY, wantZ: e.ai.wantZ,
			yaw: e.yaw, headYaw: e.headYaw,
			x: e.x, y: e.y, z: e.z,
		}
	}

	const ticks = 500
	for i := 0; i < ticks; i++ {
		goPig.ai.serverAiStep(goLoop, goPig)
		plPig.ai.serverAiStep(plLoop, plPig)
		// Rejoin any async path DETERMINISTICALLY on BOTH loops: the off-tick A* (OPT-01) rejoins via
		// asyncIn2 1+ ticks late and at a non-deterministic wall-clock moment, so a plain non-blocking
		// applyAsyncResults would let one pig adopt its path a different tick than the other (a TIMING
		// artifact, not a behavior difference). Block-draining each pig's pending path to completion the
		// same tick it is requested removes the async jitter so the comparison tests the GOAL/RNG/look
		// logic, not the scheduler. (The path is identical for both — same want, same world.)
		drainPendingPath(goLoop, goPig)
		drainPendingPath(plLoop, plPig)

		go_ := observe(goPig)
		pl := observe(plPig)
		if go_ != pl {
			t.Fatalf("tick %d: plugin pig diverged from the Go oracle:\n  go     = %+v\n  plugin = %+v", i, go_, pl)
		}
	}
}

// drainPendingPath rejoins any in-flight async path for the mob deterministically: it applies queued
// results, and if the mob's nav is still pending, blocks briefly on asyncIn2 for the worker to deliver
// — so both pigs in the equality test adopt their (identical) path on the SAME tick, removing async
// timing jitter from the comparison.
func drainPendingPath(loop *TickLoop, e *Entity) {
	loop.applyAsyncResults()
	if e.ai == nil || !e.ai.navigation.pending {
		return
	}
	deadline := time.After(2 * time.Second)
	for e.ai.navigation.pending {
		select {
		case r := <-loop.asyncIn2:
			r.applyTo(loop)
		case <-deadline:
			return
		}
	}
}
