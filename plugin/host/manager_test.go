package host

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"go.starlark.net/starlark"
)

// counter is the observable side effect the fixture hooks drive via the
// injected `count` builtin. It uses atomic ops so the race test can read it
// concurrently with Emit.
type counter struct{ n atomic.Int64 }

// countBuiltin returns a `count(delta)` builtin that adds delta to c. Injected
// into the predeclared set via LoadDirWith so a fixture hook's execution is
// observable from the test.
func countBuiltin(c *counter) *starlark.Builtin {
	return starlark.NewBuiltin("count", func(th *starlark.Thread, b *starlark.Builtin,
		args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var delta int
		if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &delta); err != nil {
			return nil, err
		}
		c.n.Add(int64(delta))
		return starlark.None, nil
	})
}

// loadGreeter loads ONLY the greeter fixture (single-dir testdata root) with a
// count builtin injected, returning the manager + the counter.
func loadGreeter(t *testing.T) (*Manager, *counter) {
	t.Helper()
	c := &counter{}
	m := New()
	root := singlePluginRoot(t, "greeter")
	if err := m.LoadDirWith(root, starlark.StringDict{"count": countBuiltin(c)}); err != nil {
		t.Fatalf("LoadDirWith(greeter): %v", err)
	}
	return m, c
}

// singlePluginRoot builds a temp plugins/ root containing a copy of one
// testdata fixture, so a test can load it in isolation.
func singlePluginRoot(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	copyFixture(t, name, dirJoin(t, root, name))
	return root
}

// dirJoin makes root/name and returns it.
func dirJoin(t *testing.T, root, name string) string {
	t.Helper()
	dst := filepath.Join(root, name)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	return dst
}

// copyFixture copies testdata/plugins/<name>/{plugin.toml,main.star} into dst.
func copyFixture(t *testing.T, name, dst string) {
	t.Helper()
	src := filepath.Join("testdata", "plugins", name)
	for _, f := range []string{"plugin.toml", "main.star"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatalf("read fixture %s/%s: %v", name, f, err)
		}
		if err := os.WriteFile(filepath.Join(dst, f), b, 0o600); err != nil {
			t.Fatalf("write fixture %s/%s: %v", name, f, err)
		}
	}
}

// TestDiscoverLoad: LoadDir over testdata/plugins loads greeter (and badhook),
// and greeter's register(on_block_break) populated the bus — proving
// register-ONCE-at-load.
func TestDiscoverLoad(t *testing.T) {
	m, _ := loadGreeter(t)
	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount = %d, want 1", m.PluginCount())
	}
	if got := m.HookCount(EventBlockBreak); got != 1 {
		t.Fatalf("HookCount(on_block_break) = %d, want 1 (register captured once at load)", got)
	}
}

// TestRegisterUnknownEvent: a plugin that registers a typo'd event name is SKIPPED by the
// tolerant operator LoadDir (Plan 28-02) — the bad plugin is logged + dropped, not loaded,
// and the scan returns nil so other (good) plugins still load. The register builtin still
// rejects the typo'd event (no hook is captured). The strict boot-load path (LoadDirWith)
// keeps aborting on such an error; this asserts the operator path's skip-and-continue.
func TestRegisterUnknownEvent(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "typo")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pdir, "plugin.toml"),
		"name=\"typo\"\nversion=\"1\"\nentrypoint=\"main.star\"\nruntime=\"starlark\"\n")
	mustWrite(t, filepath.Join(pdir, "main.star"),
		"def f(x,y,z,s,p):\n    pass\nregister(\"on_blockbreak\", f)\n") // missing underscore

	m := New()
	// The tolerant operator scan SKIPS the bad plugin (logged) and returns nil.
	if err := m.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir (tolerant) should skip the bad plugin and return nil, got: %v", err)
	}
	// The typo'd plugin must NOT be loaded, and no hook captured for it.
	if n := m.PluginCount(); n != 0 {
		t.Fatalf("PluginCount = %d, want 0 (the typo'd plugin must be skipped, not loaded)", n)
	}
	if n := m.HookCount(EventBlockBreak); n != 0 {
		t.Fatalf("HookCount(on_block_break) = %d, want 0 (the typo'd register must capture no hook)", n)
	}

	// The STRICT boot-load path (LoadDirWith) still aborts on the same typo (load-loudly).
	if err := New().LoadDirWith(dir, nil); err == nil {
		t.Fatal("LoadDirWith (strict) with a typo'd event: want load error, got nil")
	}
}

// TestUnload: after Unload the plugin's hooks are gone from every event.
func TestUnload(t *testing.T) {
	m, _ := loadGreeter(t)
	if m.HookCount(EventBlockBreak) != 1 {
		t.Fatalf("precondition: HookCount = %d, want 1", m.HookCount(EventBlockBreak))
	}
	m.Unload("greeter")
	if got := m.HookCount(EventBlockBreak); got != 0 {
		t.Fatalf("after Unload: HookCount(on_block_break) = %d, want 0", got)
	}
	if m.PluginCount() != 0 {
		t.Fatalf("after Unload: PluginCount = %d, want 0", m.PluginCount())
	}
}

// TestSkipNonStarlark: a plugin with runtime!="starlark" is skipped (Phase-26
// python reserved), not loaded.
func TestSkipNonStarlark(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "pyplug")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pdir, "plugin.toml"),
		"name=\"pyplug\"\nversion=\"1\"\nentrypoint=\"main.py\"\nruntime=\"python\"\n")
	mustWrite(t, filepath.Join(pdir, "main.py"), "print('hi')\n")

	m := New()
	if err := m.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir with non-starlark plugin: unexpected error: %v", err)
	}
	if m.PluginCount() != 0 {
		t.Fatalf("PluginCount = %d, want 0 (python skipped)", m.PluginCount())
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestNestedContainerDirLoad: a subdir WITHOUT a plugin.toml is a CONTAINER and is
// scanned recursively, so a plugin nested under plugins/mobs/<name>/ still loads.
// This is the /mobs/* organization seam: operators may group plugins in folders.
func TestNestedContainerDirLoad(t *testing.T) {
	root := t.TempDir()
	// plugins/mobs/np/ — a self-contained plugin one level DEEPER than the scan root.
	// The "mobs" dir has no plugin.toml, so it must be treated as a container and recursed.
	nested := filepath.Join(root, "mobs", "np")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(nested, "plugin.toml"),
		"name=\"np\"\nversion=\"1\"\nentrypoint=\"main.star\"\nruntime=\"starlark\"\n")
	mustWrite(t, filepath.Join(nested, "main.star"),
		"def f(x,y,z,s,p):\n    pass\nregister(\"on_block_break\", f)\n")

	m := New()
	if err := m.LoadDir(root); err != nil {
		t.Fatalf("LoadDir over a nested layout: %v", err)
	}
	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount = %d, want 1 (the nested plugin must load)", m.PluginCount())
	}
	if got := m.HookCount(EventBlockBreak); got != 1 {
		t.Fatalf("HookCount(on_block_break) = %d, want 1 (nested plugin's register must capture)", got)
	}
}

// TestNestedDoesNotRecurseIntoPlugin: a dir WITH a plugin.toml is loaded as a plugin
// and NOT recursed into — a stray subdir inside a plugin dir must not be scanned.
func TestNestedDoesNotRecurseIntoPlugin(t *testing.T) {
	root := t.TempDir()
	pdir := filepath.Join(root, "np")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(pdir, "plugin.toml"),
		"name=\"np\"\nversion=\"1\"\nentrypoint=\"main.star\"\nruntime=\"starlark\"\n")
	mustWrite(t, filepath.Join(pdir, "main.star"),
		"def f(x,y,z,s,p):\n    pass\nregister(\"on_block_break\", f)\n")
	// A junk subdir inside the plugin dir that itself contains a plugin.toml — if the
	// loader wrongly recursed into a plugin dir, it would load this too and PluginCount
	// would be 2. It must be ignored (np is a plugin, not a container).
	junk := filepath.Join(pdir, "inner")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(junk, "plugin.toml"),
		"name=\"inner\"\nversion=\"1\"\nentrypoint=\"main.star\"\nruntime=\"starlark\"\n")
	mustWrite(t, filepath.Join(junk, "main.star"),
		"def f(x,y,z,s,p):\n    pass\nregister(\"on_block_break\", f)\n")

	m := New()
	if err := m.LoadDir(root); err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount = %d, want 1 (a plugin dir must not be recursed into)", m.PluginCount())
	}
}
