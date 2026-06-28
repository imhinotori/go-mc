//go:build !python

// python_bridge_skipped_test.go holds the DEFAULT-build-ONLY assertion that the
// runtime="python" worldmutate plugin is SKIPPED when the CPython runtime is not
// compiled in. It is `//go:build !python` because under `-tags python` WirePython
// registers the REAL gopy runtime + bridge factory, so the plugin LOADS
// (PluginCount==1) — the "skipped" assertion only holds when WirePython is the
// no-op stub (async_python_stub.go). The opt-in build's positive twin is
// TestPythonWorldBridge (python_bridge_python_test.go), which loads the SAME
// worldmutate plugins through the real runtime and exercises the world bridge.

package server

import (
	"testing"

	"github.com/imhinotori/sulfur/plugin/host"
)

// TestPythonWorldBridgeSkippedDefault: on the default (no `-tags python`) build,
// WirePython is a no-op, so the runtime="python" worldmutate plugin is SKIPPED by
// LoadDir — no error, no plugin loaded, no gopy linked. The world-bridge is additive:
// a server with no python lane is unaffected. This is the gate the local CGO=0 box runs.
func TestPythonWorldBridgeSkippedDefault(t *testing.T) {
	loop, _ := newBlockLoop()
	m := host.New()

	// WirePython is the no-op stub on the default build — no python runtime, no bridge
	// factory, so the python manifest is skipped.
	WirePython(loop, m)

	if err := m.LoadDir("testdata/worldbridge_granted"); err != nil {
		t.Fatalf("LoadDir(worldbridge_granted) on default build must skip the python plugin gracefully, got err: %v", err)
	}
	if got := m.PluginCount(); got != 0 {
		t.Fatalf("PluginCount = %d; want 0 (the runtime=\"python\" worldmutate plugin must be skipped on the default build)", got)
	}
}
