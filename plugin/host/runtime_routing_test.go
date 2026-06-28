package host

// runtime_routing_test.go is a DEFAULT-build (no tag, CGO=0) test: it proves
// LoadDir routes a runtime="python" manifest WITHOUT any gopy/cgo. The python
// concrete impl is replaced by a pure-Go fake implementing PythonRuntime /
// PythonPlugin, so the routing logic is exercised on the default build. The
// no-gopy-in-graph gate (go list -deps ./plugin/host) confirms the host stays
// cgo-free.

import "testing"

// fakePythonPlugin is a pure-Go PythonPlugin test double.
type fakePythonPlugin struct {
	closed   bool
	hookHits int
}

func (f *fakePythonPlugin) CallHook(event string, args ...any) error { f.hookHits++; return nil }
func (f *fakePythonPlugin) Close()                                   { f.closed = true }

// fakePythonRuntime is a pure-Go PythonRuntime test double. It records whether
// Load was called and hands back a fakePythonPlugin.
type fakePythonRuntime struct {
	available bool
	loadCalls int
	lastEntry string
	plugin    *fakePythonPlugin
}

func (f *fakePythonRuntime) Available() bool { return f.available }
func (f *fakePythonRuntime) Load(entrypoint string) (PythonPlugin, error) {
	f.loadCalls++
	f.lastEntry = entrypoint
	f.plugin = &fakePythonPlugin{}
	return f.plugin, nil
}

const pyTestdata = "testdata/python_plugins"

// TestRuntimeRoutingDefaultBuild: with NO python runtime registered (the default
// build), a runtime="python" manifest is SKIPPED gracefully — no error, the scan
// completes, and no plugin is loaded.
func TestRuntimeRoutingDefaultBuild(t *testing.T) {
	m := New()
	if err := m.LoadDir(pyTestdata); err != nil {
		t.Fatalf("LoadDir with an unregistered python runtime must skip gracefully, got err: %v", err)
	}
	if got := m.PluginCount(); got != 0 {
		t.Fatalf("PluginCount = %d; want 0 (the python plugin must be skipped, not loaded)", got)
	}
}

// TestRuntimeRoutingRegistered: WITH a fake python runtime registered, a
// runtime="python" manifest routes to it — Load is called with the entrypoint,
// the plugin is loaded, and Unload tears the python handle down.
func TestRuntimeRoutingRegistered(t *testing.T) {
	m := New()
	fake := &fakePythonRuntime{available: true}
	m.SetPythonRuntime(fake)

	if err := m.LoadDir(pyTestdata); err != nil {
		t.Fatalf("LoadDir err: %v", err)
	}
	if fake.loadCalls != 1 {
		t.Fatalf("python runtime Load called %d times; want 1 (the python manifest must route to it)", fake.loadCalls)
	}
	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount = %d; want 1 (the python plugin must be loaded via the interface)", m.PluginCount())
	}

	// Unload must close the python handle.
	m.Unload("heavylogger")
	if m.PluginCount() != 0 {
		t.Fatalf("PluginCount after Unload = %d; want 0", m.PluginCount())
	}
	if fake.plugin == nil || !fake.plugin.closed {
		t.Fatal("Unload must Close() the python plugin handle")
	}
}

// TestRuntimeRoutingUnavailableRuntime: a registered-but-unavailable runtime
// (Available()==false) is treated the same as "not built in" — skipped.
func TestRuntimeRoutingUnavailableRuntime(t *testing.T) {
	m := New()
	m.SetPythonRuntime(&fakePythonRuntime{available: false})
	if err := m.LoadDir(pyTestdata); err != nil {
		t.Fatalf("LoadDir err: %v", err)
	}
	if got := m.PluginCount(); got != 0 {
		t.Fatalf("PluginCount = %d; want 0 (an unavailable python runtime must skip)", got)
	}
}
