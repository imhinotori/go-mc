package server

// async_python_test.go (DEFAULT build, no tag, CGO=0) proves the off-tick python
// lane is ADDITIVE and cgo-free: a runtime="python" plugin is skipped gracefully
// when the python runtime is not built in (the default binary), the starlark
// inline path is unaffected, and the cgo-free rejoin plumbing (pythonHookReady +
// PythonHookStats) round-trips on the owner. The TAGGED off-tick round-trip
// (TestPythonRegisterAndFire) lives in async_python_python_test.go and runs on the
// python3.14 Docker image; THIS file is what the local CGO=0 gate runs.

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
)

// TestPythonLaneAdditiveToStarlark: wiring the (no-op) python lane and loading a
// python manifest alongside a starlark plugin must NOT disturb the inline starlark
// dispatch — a real block break still fires the starlark on_block_break exactly
// once. The python lane is additive: it never breaks the proven Phase-22 path.
func TestPythonLaneAdditiveToStarlark(t *testing.T) {
	loop, mgr := newBlockLoop()
	m, ec := loadEventsManager(t) // the starlark events fixture (8 counting hooks)

	// Wire the no-op python lane + load the python manifest dir ON TOP of the
	// starlark manager. The python plugin is skipped (default build); the starlark
	// hooks remain.
	WirePython(loop, m)
	if err := m.LoadDir("testdata/plugins_python"); err != nil {
		t.Fatalf("LoadDir(testdata/plugins_python): %v", err)
	}
	loop.SetPlugins(m)

	// Break one stone block — the starlark on_block_break must fire exactly once.
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeCreative
	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(),
		Packet: playerActionPacket(0 /*START*/, target, 1, 1)})

	if got := ec.get("on_block_break"); got != 1 {
		t.Fatalf("starlark on_block_break = %d after one break with the python lane wired, want 1 "+
			"(the python lane must be ADDITIVE, never breaking the inline starlark path)", got)
	}
}

// TestPythonHookReadyRejoin: the cgo-free rejoin (pythonHookReady) round-trips on
// the owner exactly like pathReady — a successful note bumps the applied counter,
// an error note bumps the error counter. This exercises the default-built plumbing
// (no gopy) so the rejoin contract is covered even without libpython.
func TestPythonHookReadyRejoin(t *testing.T) {
	loop := NewTickLoop(newFakeClock())

	pythonHookReady{event: "on_block_break", note: "ok"}.applyTo(loop)
	pythonHookReady{event: "on_block_break", note: "ok"}.applyTo(loop)
	pythonHookReady{event: "on_block_break", note: "boom"}.applyTo(loop)

	applied, errs := loop.PythonHookStats()
	if applied != 2 {
		t.Fatalf("pythonHooksApplied = %d, want 2 (two ok rejoins applied on the owner)", applied)
	}
	if errs != 1 {
		t.Fatalf("pythonHookErrors = %d, want 1 (one errored rejoin counted on the owner)", errs)
	}
}

// TestSubmitPythonHookOffTickRoundTrip: submitPythonHook submits a fake python
// plugin's hook to the pluginPool (off-tick), the worker runs CallHook off the
// caller goroutine and rejoins via pythonHookReady on asyncIn2; draining asyncIn2
// + applyTo bumps the owner counter. This proves the OFF-TICK round-trip with a
// pure-Go fake (no gopy) — the gopy-backed version runs on the Docker image.
func TestSubmitPythonHookOffTickRoundTrip(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	defer loop.Close()

	fake := &recordingPlugin{}
	loop.submitPythonHook(fake, "on_block_break", []any{1, 64, 1, 0, 42})

	// Drain the rejoin from asyncIn2 (the worker sends a pythonHookReady after
	// CallHook returns) and apply it on the owner.
	select {
	case r := <-loop.asyncIn2:
		r.applyTo(loop)
	case <-time.After(2 * time.Second):
		t.Fatal("off-tick python hook never rejoined via asyncIn2 (submitPythonHook → pool → asyncIn2 round-trip failed)")
	}

	if !fake.called {
		t.Fatal("CallHook was never invoked off-tick (the pool worker did not run the hook)")
	}
	if applied, _ := loop.PythonHookStats(); applied != 1 {
		t.Fatalf("pythonHooksApplied = %d after one off-tick round-trip, want 1", applied)
	}
}

// recordingPlugin is a pure-Go host.PythonPlugin double for the off-tick lane test
// (no gopy). It records that CallHook ran and on which goroutine.
type recordingPlugin struct {
	called bool
	args   []any
	bridge host.WorldBridge
}

func (r *recordingPlugin) CallHook(event string, args ...any) error {
	r.called = true
	r.args = args
	return nil
}
func (r *recordingPlugin) SetWorldBridge(b host.WorldBridge) { r.bridge = b }
func (r *recordingPlugin) Close()                            {}
