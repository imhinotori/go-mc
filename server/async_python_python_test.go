//go:build python

// async_python_python_test.go is the TAGGED-BUILD off-tick ROUND-TRIP test. It
// runs ONLY under `CGO_ENABLED=1 go test -tags python -race ./server/` on the
// python3.14 Docker image (libpython 3.14 + libffi) — NOT a default Windows/CGO=0
// box (26-RESEARCH Pitfall 6). It proves the FULL lane: WirePython registers the
// real gopy runtime + the off-tick dispatch, LoadDir loads the heavylogger python
// plugin, firing EventBlockBreak SUBMITS the hook off-tick (not inline), and the
// pythonHookReady rejoins via asyncIn2 → applyAsyncResults → applyTo on the owner.

package server

import (
	"time"

	"github.com/imhinotori/sulfur/plugin/host"
	"testing"
)

// TestPythonRegisterAndFire: with the real CPython runtime wired (WirePython), the
// heavylogger python plugin loads through host.LoadDir, and firing EventBlockBreak
// via host.Emit submits its on_block_break hook OFF-TICK (the pluginPool worker,
// GIL-held) — the result rejoins via pythonHookReady on asyncIn2, which we drain +
// apply on the owner, bumping the applied counter. This is the off-tick round-trip
// without world mutation (the Phase-26 gate behavior).
func TestPythonRegisterAndFire(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	defer loop.Close()

	m := host.New()
	WirePython(loop, m) // registers the gopy runtime + routes hooks to submitPythonHook
	if err := m.LoadDir("testdata/plugins_python"); err != nil {
		t.Fatalf("LoadDir(testdata/plugins_python) with the python runtime built in: %v", err)
	}
	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount = %d; want 1 (heavylogger must load through the python runtime)", m.PluginCount())
	}
	loop.SetPlugins(m)

	// Fire the discrete event on this (the "tick") goroutine. emitPython SUBMITS the
	// hook off-tick (drop-on-overload) and returns immediately — it must NOT run the
	// python hook inline here.
	m.Emit(host.EventBlockBreak, host.BlockBreakEvent{X: 1, Y: 64, Z: 1, State: 0, PlayerID: 42})

	// The off-tick worker runs CallHook (GIL-held) and rejoins on asyncIn2. Drain it.
	select {
	case r := <-loop.asyncIn2:
		r.applyTo(loop)
	case <-time.After(5 * time.Second):
		t.Fatal("python on_block_break never rejoined via asyncIn2 (off-tick submit → pool → rejoin failed)")
	}

	applied, errs := loop.PythonHookStats()
	if errs != 0 {
		t.Fatalf("pythonHookErrors = %d after one fire, want 0 (the hook must run cleanly off-tick)", errs)
	}
	if applied != 1 {
		t.Fatalf("pythonHooksApplied = %d after one EventBlockBreak, want 1 (the off-tick round-trip must complete on the owner)", applied)
	}
}
