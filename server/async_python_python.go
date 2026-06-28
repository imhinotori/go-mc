//go:build python

// async_python_python.go is the OPT-IN (`-tags python`) wiring that plugs the
// concrete CPython runtime + the off-tick dispatch into the host. It is the ONLY
// server-side file that imports plugin/python's tagged side, so it is build-tag
// split: the `//go:build !python` twin (async_python_stub.go) is a no-op, keeping
// the DEFAULT `server` build cgo-free with ZERO gopython in the import graph (THE
// #1 gate). gopy never appears here directly — plugin/python.Runtime carries it
// behind its own tag; this file only references the package's exported surface.
//
// WirePython does two registrations, both BEFORE the tick loop owns the Manager:
//   1. SetPythonRuntime — the concrete gopy-backed loader (so LoadDir actually
//      loads a runtime="python" plugin, 26-01's seam).
//   2. SetPythonDispatch — routes a python plugin's hook to t.submitPythonHook,
//      the OFF-TICK ants-pool submit (the pathReady lane). This is what makes a
//      python on_block_break fire off-tick (PLUGIN-06).
// On the default build, main calls the no-op stub and python manifests are skipped.

package server

import (
	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/imhinotori/sulfur/plugin/python"
)

// pythonRuntimeAdapter wraps the package-level plugin/python.Load behind the
// host.PythonRuntime interface (the cgo-free seam). It exists only on the tagged
// build; the gopy import lives in plugin/python, not here.
type pythonRuntimeAdapter struct{}

func (pythonRuntimeAdapter) Available() bool { return python.Available() }

func (pythonRuntimeAdapter) Load(entrypoint string) (host.PythonPlugin, error) {
	return python.Load(entrypoint) // *python.Runtime satisfies host.PythonPlugin (CallHook/Close)
}

// WirePython registers the CPython runtime + the off-tick dispatch + the WORLD-BRIDGE
// factory on the manager so runtime="python" plugins load, their hooks fire OFF-TICK
// via t.pluginPool, and their off-tick set_block/spawn/log/block_at builtins reach the
// tick-owned world through the request/apply lane. Called from main BEFORE
// SetPlugins/Run. On the `-tags python` build this is the real wiring; the default
// build calls the no-op stub twin instead.
func WirePython(t *TickLoop, m *host.Manager) {
	m.SetPythonRuntime(pythonRuntimeAdapter{})
	m.SetPythonDispatch(t.submitPythonHook)
	// WORLD-BRIDGE (Plan 26-03): the host calls this per python plugin at load with
	// the plugin's name + manifest capabilities; the server parses the capabilities
	// (the SAME parseCapabilities the Phase-23 Starlark handles use) into a capSet and
	// returns a per-plugin serverWorldBridge stamped with it. The factory signature is
	// plain Go (no cgo) so plugin/host stays cgo-free.
	m.SetPythonBridgeFactory(func(plugin string, capabilities []string) (host.WorldBridge, error) {
		return t.newServerWorldBridge(plugin, capabilities)
	})
}
