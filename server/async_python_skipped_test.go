//go:build !python

// async_python_skipped_test.go holds the DEFAULT-build-ONLY assertion that a
// runtime="python" plugin is SKIPPED when the CPython runtime is not compiled in.
// It is `//go:build !python` because under `-tags python` WirePython registers the
// REAL gopy runtime, so the plugin LOADS (PluginCount==1) — the "skipped" assertion
// only holds when WirePython is the no-op stub (async_python_stub.go). The opt-in
// build's positive twin is TestPythonRegisterAndFire (async_python_python_test.go),
// which loads the SAME heavylogger plugin through the real runtime.

package server

import (
	"testing"

	"github.com/imhinotori/sulfur/plugin/host"
)

// TestPythonLaneSkippedDefault: on the default (no `-tags python`) build,
// WirePython is a no-op, so a runtime="python" plugin (heavylogger) is SKIPPED by
// LoadDir — no error, no plugin loaded, no gopy linked. This is the "python lane
// is opt-in" gate the local CGO=0 box runs.
func TestPythonLaneSkippedDefault(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	m := host.New()

	// WirePython is the no-op stub on the default build — it must NOT register a
	// python runtime, so the python manifest is skipped.
	WirePython(loop, m)

	if err := m.LoadDir("testdata/plugins_python"); err != nil {
		t.Fatalf("LoadDir(testdata/plugins_python) on default build must skip the python plugin gracefully, got err: %v", err)
	}
	if got := m.PluginCount(); got != 0 {
		t.Fatalf("PluginCount = %d; want 0 (the runtime=\"python\" plugin must be skipped on the default build, not loaded)", got)
	}
}
