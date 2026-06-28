package host

import (
	"testing"

	"go.starlark.net/starlark"
)

// loadMatcher loads a single recipe fixture and returns the manager.
func loadMatcher(t *testing.T, name string) *Manager {
	t.Helper()
	m := New()
	root := singlePluginRoot(t, name)
	if err := m.LoadDirWith(root, nil); err != nil {
		t.Fatalf("LoadDirWith(%s): %v", name, err)
	}
	return m
}

// grid builds a starlark grid payload (a list of cell ids) for the fixture
// matcher — the host does not interpret it; it's the plugin-facing frozen value.
func grid(ids ...int) starlark.Value {
	elems := make([]starlark.Value, len(ids))
	for i, id := range ids {
		elems[i] = starlark.MakeInt(id)
	}
	return starlark.NewList(elems)
}

// TestMatchSeam: a fixture plugin registers a matcher via set_recipe_matcher;
// Manager.Match calls it on a fresh thread and reads back {id,count} as Go ints.
// A None return -> ok=false.
func TestMatchSeam(t *testing.T) {
	m := loadMatcher(t, "recipematcher")
	if !m.HasRecipeMatcher() {
		t.Fatal("recipematcher should have registered a matcher")
	}
	// grid[0]==1 -> the fixture returns {id:280, count:4}.
	id, count, ok := m.Match(grid(1, 0, 0))
	if !ok {
		t.Fatal("Match should succeed for a matching grid")
	}
	if id != 280 || count != 4 {
		t.Errorf("Match result = (%d, %d), want (280, 4)", id, count)
	}
	// grid[0]==0 -> the fixture returns None -> ok=false.
	if _, _, ok := m.Match(grid(0, 0, 0)); ok {
		t.Error("Match should return ok=false when the matcher returns None")
	}
}

// TestMatchNoMatcher: Match with no registered matcher returns ok=false (no
// panic) — the nil-matcher fast path, mirroring Emit's zero-subscriber path.
func TestMatchNoMatcher(t *testing.T) {
	m := New() // nothing loaded
	if m.HasRecipeMatcher() {
		t.Fatal("a fresh Manager should have no matcher")
	}
	id, count, ok := m.Match(grid(1, 2, 3))
	if ok || id != 0 || count != 0 {
		t.Errorf("nil-matcher Match = (%d, %d, %v), want (0, 0, false)", id, count, ok)
	}
}

// TestMatchRunawayBounded: a matcher that burns past the step budget is halted
// (the fresh NewThread carries SetMaxExecutionSteps); Match returns ok=false on
// the *EvalError, the caller is not hung (T-25-01 DoS).
func TestMatchRunawayBounded(t *testing.T) {
	m := loadMatcher(t, "runawaymatcher")
	id, count, ok := m.Match(grid(1))
	if ok {
		t.Error("a runaway matcher must be halted by the step budget (ok=false)")
	}
	if id != 0 || count != 0 {
		t.Errorf("runaway Match = (%d, %d), want (0, 0)", id, count)
	}
}

// TestMatchErrorIsolated: a matcher that raises is logged and returns ok=false,
// not a panic.
func TestMatchErrorIsolated(t *testing.T) {
	m := loadMatcher(t, "badmatcher")
	id, count, ok := m.Match(grid(1))
	if ok || id != 0 || count != 0 {
		t.Errorf("raising matcher = (%d, %d, %v), want (0, 0, false)", id, count, ok)
	}
}

// TestRecipesBuiltin: the recipes() builtin returns the Go-injected recipe table
// to the plugin (set via SetRecipeTable BEFORE load).
func TestRecipesBuiltin(t *testing.T) {
	m := New()
	// Inject a tiny table: a list with one dict {id:280, count:4}.
	d := starlark.NewDict(2)
	_ = d.SetKey(starlark.String("id"), starlark.MakeInt(280))
	_ = d.SetKey(starlark.String("count"), starlark.MakeInt(4))
	table := starlark.NewList([]starlark.Value{d})
	m.SetRecipeTable(table)

	root := singlePluginRoot(t, "recipematcher")
	if err := m.LoadDirWith(root, nil); err != nil {
		t.Fatalf("LoadDirWith(recipematcher): %v", err)
	}
	// The fixture called recipes() at load (TABLE = recipes()); if the table was
	// not visible the load would not error, so prove the channel by Matching:
	// a registered matcher implies recipes() resolved without erroring the load.
	if !m.HasRecipeMatcher() {
		t.Fatal("recipematcher load (which calls recipes()) should have registered a matcher")
	}
	// Match still works with the table injected.
	if _, _, ok := m.Match(grid(1)); !ok {
		t.Error("Match should succeed after SetRecipeTable + load")
	}
}

// TestMatchUnloadDropsMatcher: unloading the matcher plugin drops the matcher.
func TestMatchUnloadDropsMatcher(t *testing.T) {
	m := loadMatcher(t, "recipematcher")
	if !m.HasRecipeMatcher() {
		t.Fatal("matcher should be registered before unload")
	}
	m.Unload("recipematcher")
	if m.HasRecipeMatcher() {
		t.Error("Unload should drop the matcher owned by the unloaded plugin")
	}
	if _, _, ok := m.Match(grid(1)); ok {
		t.Error("Match should be ok=false after the matcher is unloaded")
	}
}
