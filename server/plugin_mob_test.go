package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/plugin/host"
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
	if _, ok := loop.entities.get(e.id); !ok {
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
