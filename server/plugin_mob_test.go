package server

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/plugin/host"
	starlarkpkg "github.com/imhinotori/sulfur/plugin/starlark"
	"go.starlark.net/starlark"
)

// plugin_mob_test.go — PLUGIN-03 (Wave 2): the declare_mob/goal load-time capture, the starlarkGoal
// adapter that slots into the EXISTING goalSelector, buildAIFromDecl + spawnDeclaredMob, and THE GATE
// (a Starlark wander mob that spawns, ticks, and MOVES through the real Go nav).
//
// The tests load a .star body through the host's LoadDirWith with the server-built declare_mob/goal
// builtins injected as `extra` (the import-direction-A seam: the server owns the registry + builtins,
// the host just runs the module body that captures into them).

// --- load harness -----------------------------------------------------------------------------

// mobpluginsRoot is the isolated testdata root holding ONLY the wandermob plugin. It is SEPARATE from
// testdata/plugins (the events fixture) because the events test loads the whole testdata/plugins dir
// WITHOUT the declare_mob/goal builtins — loading wandermob there would error on the undefined
// declare_mob. Keeping the mob plugin in its own root keeps both load paths clean.
const mobpluginsRoot = "testdata/mobplugins"

// loadMobRegistry builds a mobRegistry, injects its declare_mob/goal builtins into a host.Manager,
// and loads the given root — returning the registry holding the captured declarations. It is the
// exact import-direction-A seam the server wiring uses (the server owns the registry; the host runs
// the body that captures into it). The caps default to capAll (the registry's default) so the
// handles built for goal callbacks work in tests without a manifest-derived narrower set.
func loadMobRegistry(t *testing.T, root string) *mobRegistry {
	t.Helper()
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(%s): %v", root, err)
	}
	return r
}

// writeMobPlugin writes a one-off plugin dir (plugin.toml + main.star) under a fresh temp root and
// returns the root — for the reject tests that need a deliberately-bad declaration. The toml mirrors
// the wandermob manifest (the capabilities are irrelevant to a load-time capture/reject).
func writeMobPlugin(t *testing.T, star string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	toml := "name = \"p\"\nversion = \"0.1.0\"\nentrypoint = \"main.star\"\nruntime = \"starlark\"\ncapabilities = [\"entities.read\", \"entities.write\", \"nav\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.star"), []byte(star), 0o644); err != nil {
		t.Fatalf("write star: %v", err)
	}
	return root
}

// loadMobRegistryExpectErr loads a one-off plugin and asserts the load FAILED (the declaration was
// rejected loudly). Returns the error for message assertions.
func loadMobRegistryExpectErr(t *testing.T, star string) error {
	t.Helper()
	root := writeMobPlugin(t, star)
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	err := m.LoadDirWith(root, extra)
	if err == nil {
		t.Fatalf("expected a load error, got nil (the bad declaration was accepted)")
	}
	return err
}

// --- Task 1: declare_mob/goal capture + spawnDeclaredMob ---------------------------------------

// TestDeclareMobCaptures: loading the wandermob plugin captures ONE mobDecl keyed "wanderer" with
// baseType pig, the declared attribute overrides, and one goalDecl{priority:6, flags:flagMove,
// tickFn:<frozen callable>}.
func TestDeclareMobCaptures(t *testing.T) {
	r := loadMobRegistry(t, mobpluginsRoot)

	if len(r.byName) != 1 {
		t.Fatalf("registry holds %d decls, want exactly 1 (declare-once)", len(r.byName))
	}
	decl, ok := r.byName["wanderer"]
	if !ok {
		t.Fatalf("no mobDecl captured under %q", "wanderer")
	}
	if decl.baseType.ID != entity.Pig.ID {
		t.Fatalf("baseType = %v, want pig (%d)", decl.baseType.ID, entity.Pig.ID)
	}
	if got := decl.attrs["max_health"]; got != 12.0 {
		t.Fatalf("attrs[max_health] = %v, want 12.0", got)
	}
	if got := decl.attrs["movement_speed"]; got != 0.25 {
		t.Fatalf("attrs[movement_speed] = %v, want 0.25", got)
	}
	if len(decl.goals) != 1 {
		t.Fatalf("captured %d goals, want 1", len(decl.goals))
	}
	g := decl.goals[0]
	if g.priority != 6 {
		t.Fatalf("goal priority = %d, want 6", g.priority)
	}
	if g.flags != flagMove {
		t.Fatalf("goal flags = %b, want flagMove (%b)", g.flags, flagMove)
	}
	if g.tickFn == nil {
		t.Fatalf("goal tickFn is nil — the frozen callable was not captured")
	}
}

// TestDeclareMobRejectsBadBaseType: an unknown base_type errors at LOAD (loud), not silently.
func TestDeclareMobRejectsBadBaseType(t *testing.T) {
	star := `
def t(e, w, n):
    pass
declare_mob(name="x", base_type="dragon", goals=[goal(priority=1, flags=["MOVE"], tick=t)])
`
	err := loadMobRegistryExpectErr(t, star)
	if !contains(err.Error(), "unknown base_type") {
		t.Fatalf("error %q does not mention the unknown base_type", err.Error())
	}
}

// TestDeclareMobRejectsBadFlag: a goal with an unknown flag errors at load.
func TestDeclareMobRejectsBadFlag(t *testing.T) {
	star := `
def t(e, w, n):
    pass
declare_mob(name="x", base_type="pig", goals=[goal(priority=1, flags=["BOGUS"], tick=t)])
`
	err := loadMobRegistryExpectErr(t, star)
	if !contains(err.Error(), "unknown flag") {
		t.Fatalf("error %q does not mention the unknown flag", err.Error())
	}
}

// TestDeclareMobRejectsDuplicate: two declare_mob calls with the same name error at load.
func TestDeclareMobRejectsDuplicate(t *testing.T) {
	star := `
def t(e, w, n):
    pass
declare_mob(name="x", base_type="pig", goals=[goal(priority=1, flags=["MOVE"], tick=t)])
declare_mob(name="x", base_type="cow", goals=[goal(priority=1, flags=["MOVE"], tick=t)])
`
	err := loadMobRegistryExpectErr(t, star)
	if !contains(err.Error(), "duplicate") {
		t.Fatalf("error %q does not mention the duplicate name", err.Error())
	}
}

// TestSpawnDeclaredMobAttributes: spawnDeclaredMob returns an *Entity whose AttributeMap reflects the
// declared overrides (max_health=12) AND a real pig base (the Wave-1 SUB-ATTRIB fix — a base_type pig
// gets a real supplier, so movement_speed resolves to the declared 0.25, not the bare living default
// 0.7). The mob renders as the pig wire id.
func TestSpawnDeclaredMobAttributes(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	const floorY = 64
	fillFloor(ch, floorY)

	r := loadMobRegistry(t, mobpluginsRoot)
	decl := r.byName["wanderer"]

	e := loop.spawnDeclaredMob(decl, 2.5, float64(floorY+1), 2.5)

	if e.typ != entity.Pig.ID {
		t.Fatalf("declared mob typ = %d, want pig wire id %d (custom = behavior, not a new wire type)", e.typ, entity.Pig.ID)
	}
	if e.attributes == nil {
		t.Fatalf("declared mob has no attribute map (the pig supplier must back it)")
	}
	if got := e.attributes.GetValue(attribute.MaxHealth.Name()); got != 12.0 {
		t.Fatalf("max_health = %v, want the declared override 12.0", got)
	}
	// The real pig base: a base_type pig has a movement_speed supplier (Wave-1 SUB-ATTRIB), so the
	// declared 0.25 applies — NOT the bare living createLivingAttributes default 0.7.
	if got := e.attributes.GetValue(attribute.MovementSpeed.Name()); got != 0.25 {
		t.Fatalf("movement_speed = %v, want the declared 0.25 over a real pig base (not 0.7)", got)
	}
	if e.ai == nil {
		t.Fatalf("declared mob has no AI (buildAIFromDecl must attach a mobAI)")
	}
	if _, ok := loop.only().entities.get(e.id); !ok {
		t.Fatalf("spawnDeclaredMob did not add the mob to the tick-owned store")
	}
}

// contains is a tiny substring helper (avoids importing strings just for the reject assertions).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- Task 2: starlarkGoal (implements Goal) + navHandle + buildAIFromDecl -----------------------

// countingFn returns a starlark.Callable that increments *n each time it is invoked (and returns
// None). It is the test stand-in for a goal's tick/can_use callback so a test can assert HOW OFTEN
// the interpreter actually fired — the load-bearing "interpreter only when running" proof.
func countingFn(n *int) starlark.Callable {
	return starlark.NewBuiltin("counting", func(_ *starlark.Thread, _ *starlark.Builtin,
		_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		*n++
		return starlark.None, nil
	})
}

// erroringFn returns a starlark.Callable that always raises (returns an error) — the test stand-in
// for a buggy goal callback whose error MUST be isolated (logged, the tick survives).
func erroringFn() starlark.Callable {
	return starlark.NewBuiltin("erroring", func(_ *starlark.Thread, _ *starlark.Builtin,
		_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return nil, errBoom
	})
}

var errBoom = starlarkError("boom")

// starlarkError is a trivial error type so erroringFn can raise without importing errors.
type starlarkError string

func (e starlarkError) Error() string { return string(e) }

// TestStarlarkGoalImplementsGoal: a starlarkGoal is assignable to server.Goal (the compile-time
// assertion `var _ Goal = (*starlarkGoal)(nil)` in plugin_mob_ai.go), and goalSelector.addGoal
// accepts it without a bypass.
func TestStarlarkGoalImplementsGoal(t *testing.T) {
	var sel goalSelector
	g := &starlarkGoal{baseGoal: newBaseGoal(flagMove), name: "x"}
	sel.addGoal(6, g) // must compile + accept a starlarkGoal as a Goal
	if len(sel.goals) != 1 {
		t.Fatalf("addGoal did not register the starlarkGoal")
	}
}

// TestStarlarkGoalArbitration: two starlarkGoals both claim MOVE; the higher-precedence (smaller
// priority) one runs and holds MOVE, the other does NOT tick — proving a declared goal goes THROUGH
// the goalSelector flag-locking arbitration, not around it.
func TestStarlarkGoalArbitration(t *testing.T) {
	loop, _ := newPhysicsLoop()
	e := testEntity(1, entity.Pig, 0, 0, 0)
	e.ai = &mobAI{}
	loop.only().entities.add(e)

	var hiCount, loCount int
	hi := &starlarkGoal{baseGoal: newBaseGoal(flagMove), t: loop, caps: capAll, name: "hi", tickFn: countingFn(&hiCount)}
	lo := &starlarkGoal{baseGoal: newBaseGoal(flagMove), t: loop, caps: capAll, name: "lo", tickFn: countingFn(&loCount)}
	e.ai.goals.addGoal(2, hi) // smaller priority = higher precedence
	e.ai.goals.addGoal(6, lo)

	for i := 0; i < 5; i++ {
		e.ai.goals.tick(loop, e)
	}
	if hiCount == 0 {
		t.Fatalf("the higher-precedence MOVE goal never ticked (arbitration broken)")
	}
	if loCount != 0 {
		t.Fatalf("the lower-precedence MOVE goal ticked %d times — it must be locked out of MOVE", loCount)
	}
}

// TestStarlarkGoalCallsFnOnlyWhenRunning: a goal's tickFn is invoked ONLY while the goal is running.
// A goal whose can_use returns false NEVER starts, so its tickFn fires 0 times across many ticks.
func TestStarlarkGoalCallsFnOnlyWhenRunning(t *testing.T) {
	loop, _ := newPhysicsLoop()
	e := testEntity(1, entity.Pig, 0, 0, 0)
	e.ai = &mobAI{}
	loop.only().entities.add(e)

	var tickCount int
	// can_use returns False (a falsey value) so the goal can never start.
	falseFn := starlark.NewBuiltin("false", func(_ *starlark.Thread, _ *starlark.Builtin,
		_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return starlark.False, nil
	})
	g := &starlarkGoal{baseGoal: newBaseGoal(flagMove), t: loop, caps: capAll, name: "idle",
		canUseFn: falseFn, tickFn: countingFn(&tickCount)}
	e.ai.goals.addGoal(6, g)

	for i := 0; i < 10; i++ {
		e.ai.goals.tick(loop, e)
	}
	if tickCount != 0 {
		t.Fatalf("a never-running goal's tickFn fired %d times — the interpreter must fire only while running", tickCount)
	}

	// Now a goal that CAN run: its tickFn must fire on every running tick (>=1).
	var runCount int
	g2 := &starlarkGoal{baseGoal: newBaseGoal(flagLook), t: loop, caps: capAll, name: "active",
		tickFn: countingFn(&runCount)}
	e.ai.goals.addGoal(7, g2)
	for i := 0; i < 5; i++ {
		e.ai.goals.tick(loop, e)
	}
	if runCount == 0 {
		t.Fatalf("a running goal's tickFn never fired — the interpreter is not invoked for the active seam")
	}
}

// TestGoalCallbackIsolation: a tickFn that errors (raises) is logged and the tick SURVIVES — the
// error does not propagate to crash the goalSelector tick, and a sibling goal still ticks.
func TestGoalCallbackIsolation(t *testing.T) {
	loop, _ := newPhysicsLoop()
	e := testEntity(1, entity.Pig, 0, 0, 0)
	e.ai = &mobAI{}
	loop.only().entities.add(e)

	var siblingCount int
	bad := &starlarkGoal{baseGoal: newBaseGoal(flagMove), t: loop, caps: capAll, name: "bad", tickFn: erroringFn()}
	good := &starlarkGoal{baseGoal: newBaseGoal(flagLook), t: loop, caps: capAll, name: "good", tickFn: countingFn(&siblingCount)}
	e.ai.goals.addGoal(6, bad)
	e.ai.goals.addGoal(7, good)

	// The erroring goal must not panic / propagate; the sibling (different flag) must still tick.
	for i := 0; i < 3; i++ {
		e.ai.goals.tick(loop, e) // must not panic
	}
	if siblingCount == 0 {
		t.Fatalf("an erroring goal's callback killed the tick — the sibling goal never ticked (isolation broken)")
	}
}

// TestNavHandlePathTo: navHandle.path_to(x,y,z) routes to e.ai.setWantTarget (hasTarget set, the
// want target recorded); has_path reads e.ai.hasTarget; path_to is gated on capNav.
func TestNavHandlePathTo(t *testing.T) {
	loop, _ := newPhysicsLoop()
	e := testEntity(1, entity.Pig, 0, 0, 0)
	e.ai = &mobAI{}
	loop.only().entities.add(e)

	// With capNav: path_to sets the want target.
	nh := newNavHandle(loop, e.id, capAll)
	pathTo, err := nh.Attr("path_to")
	if err != nil {
		t.Fatalf("Attr(path_to): %v", err)
	}
	pf := pathTo.(*starlark.Builtin)
	if _, err := starlark.Call(starlarkThread(), pf, starlark.Tuple{starlark.Float(10), starlark.Float(64), starlark.Float(3)}, nil); err != nil {
		t.Fatalf("path_to call: %v", err)
	}
	if !e.ai.hasTarget {
		t.Fatalf("path_to did not set the want target (hasTarget still false)")
	}
	if e.ai.wantX != 10 || e.ai.wantY != 64 || e.ai.wantZ != 3 {
		t.Fatalf("path_to want target = (%v,%v,%v), want (10,64,3)", e.ai.wantX, e.ai.wantY, e.ai.wantZ)
	}

	// has_path() reads hasTarget (a bound callable, invoked as nav.has_path()).
	hpAttr, err := nh.Attr("has_path")
	if err != nil {
		t.Fatalf("Attr(has_path): %v", err)
	}
	hp, err := starlark.Call(starlarkThread(), hpAttr.(*starlark.Builtin), nil, nil)
	if err != nil {
		t.Fatalf("has_path call: %v", err)
	}
	if hp != starlark.True {
		t.Fatalf("has_path() = %v, want True after path_to", hp)
	}

	// WITHOUT capNav: path_to is denied (capability error), the target is unchanged.
	e2 := testEntity(2, entity.Pig, 0, 0, 0)
	e2.ai = &mobAI{}
	loop.only().entities.add(e2)
	denied := newNavHandle(loop, e2.id, capEntitiesRead) // no nav cap
	pathTo2, _ := denied.Attr("path_to")
	if _, err := starlark.Call(starlarkThread(), pathTo2.(*starlark.Builtin), starlark.Tuple{starlark.Float(1), starlark.Float(2), starlark.Float(3)}, nil); err == nil {
		t.Fatalf("path_to without capNav must error (capability denied)")
	}
	if e2.ai.hasTarget {
		t.Fatalf("a denied path_to must NOT set the want target")
	}
}

// starlarkThread builds a fresh budget-bounded thread for a direct handle-method call in tests
// (the same NewThread the goal callbacks use).
func starlarkThread() *starlark.Thread {
	return starlarkpkg.NewThread("test")
}

// --- Task 3: THE GATE — wander mob spawns + ticks + MOVES; idle = 0 interpreter -----------------

// TestWanderMobSpawnsAndMoves is THE GATE (the PLUGIN-03 architecture proof): a Starlark-declared
// wander mob (base_type pig, ONE MOVE goal whose tick nav-targets a fixed nearby pos) spawns, ticks
// through the FULL pipeline (tickOnce -> tickAI -> serverAiStep -> starlarkGoal.tick -> nav.path_to
// -> requestPath -> applyAsyncResults adopts the path -> navigation.tick -> moveEntity), and its
// (x,z) CHANGES via the real Go nav. It renders as the EXISTING pig wire id (custom = behavior).
func TestWanderMobSpawnsAndMoves(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	// A floor wide enough for the mob to walk 8 blocks east: fill the spawn column + the eastward
	// columns it will cross.
	const floorY = 64
	for cx := 0; cx <= 2; cx++ {
		ch := putChunk(mgr, level.ChunkPos{int32(cx), 0})
		fillFloor(ch, floorY)
	}

	r := loadMobRegistry(t, mobpluginsRoot)
	decl := r.byName["wanderer"]

	const x0, z0 = 2.5, 8.5
	e := loop.spawnDeclaredMob(decl, x0, float64(floorY+1), z0)

	if e.typ != entity.Pig.ID {
		t.Fatalf("declared mob typ = %d, want pig wire id %d (custom = behavior, not a new wire type)", e.typ, entity.Pig.ID)
	}
	if e.attributes == nil || e.attributes.GetValue(attribute.MaxHealth.Name()) != 12.0 {
		t.Fatalf("declared mob is missing the declared max_health (the supplier+override chain broke)")
	}

	// Drive the FULL tick pipeline: tickAI fires serverAiStep (the goal sets a nav target via the
	// handle), applyAsyncResults adopts the async path, navigation.tick walks the mob via moveEntity.
	for i := 0; i < 400; i++ {
		loop.tickOnce()
		// Re-resolve: a moved mob re-buckets but keeps its id; the store get is the authoritative read.
		if _, ok := loop.only().entities.get(e.id); !ok {
			t.Fatalf("the wander mob vanished from the store mid-walk")
		}
	}

	moved := math.Hypot(e.x-x0, e.z-z0)
	if moved < 2.0 {
		t.Fatalf("the wander mob did not move via the Go nav: |Δ|=%v (start %v,%v -> %v,%v); want > 2 blocks", moved, x0, z0, e.x, e.z)
	}
	// Still the pig wire id after walking (the behavior is custom, the wire type is not).
	if e.typ != entity.Pig.ID {
		t.Fatalf("after walking, typ = %d, want pig wire id %d", e.typ, entity.Pig.ID)
	}
}

// TestNoInterpreterWhenIdle proves the interpreter fires ONLY inside a running goal callback — an
// IDLE declared mob makes 0 starlark.Calls per tick, an ACTIVE one makes >=1 (T-23-08, the per-mob-
// per-tick interpreter-storm guard). It instruments the call count via a counting builtin the goal
// ticks: an idle mob (its goal's can_use false, so it never runs) yields 0 calls across many ticks;
// the active mob (the wander goal runs) yields >0. The assertion is NOT O(mobs*ticks) unconditional.
func TestNoInterpreterWhenIdle(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 64
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)

	// IDLE mob: a MOVE goal whose can_use returns False — it never runs, so its tickFn never fires.
	idle := testEntity(1, entity.Pig, 2.5, float64(floorY+1), 2.5)
	var idleCalls int
	falseFn := starlark.NewBuiltin("false", func(_ *starlark.Thread, _ *starlark.Builtin,
		_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		return starlark.False, nil
	})
	idleAI := &mobAI{}
	idleAI.navigation.speed = pigWalkSpeed
	idleAI.goals.addGoal(6, &starlarkGoal{baseGoal: newBaseGoal(flagMove), t: loop, caps: capAll,
		name: "idle", canUseFn: falseFn, tickFn: countingFn(&idleCalls)})
	idle.ai = idleAI
	loop.only().entities.add(idle)

	// ACTIVE mob: a MOVE goal with no can_use (always usable) whose tickFn increments the counter.
	active := testEntity(2, entity.Pig, 8.5, float64(floorY+1), 8.5)
	var activeCalls int
	activeAI := &mobAI{}
	activeAI.navigation.speed = pigWalkSpeed
	activeAI.goals.addGoal(6, &starlarkGoal{baseGoal: newBaseGoal(flagMove), t: loop, caps: capAll,
		name: "active", tickFn: countingFn(&activeCalls)})
	active.ai = activeAI
	loop.only().entities.add(active)

	const ticks = 10
	for i := 0; i < ticks; i++ {
		loop.tickOnce()
	}

	if idleCalls != 0 {
		t.Fatalf("an IDLE declared mob fired %d interpreter calls — the interpreter must not run per-mob-per-tick unconditionally", idleCalls)
	}
	if activeCalls == 0 {
		t.Fatalf("an ACTIVE declared mob fired 0 interpreter calls — the running goal's tick never invoked the callback")
	}
}
