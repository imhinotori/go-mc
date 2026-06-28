//go:build python

// bridge_python_test.go is the TAGGED-BUILD unit test for the world-bridge REQUEST
// PRODUCERS (set_block/spawn/log/block_at builtins, bridge_python.go). It runs ONLY
// under `CGO_ENABLED=1 go test -tags python -race ./plugin/python/` on the python3.14
// image. It proves the builtins, when a .py calls them, hand the right plain scalars
// to the installed host.WorldBridge (a pure-Go fake here) — the python → request
// boundary, WITHOUT a server/owner. The full off-tick → owner round-trip lives in
// server/python_bridge_python_test.go.

package python

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/imhinotori/sulfur/plugin/host"
	"gopython.xyz/py/v14"
)

// fakeBridge is a pure-Go host.WorldBridge double recording the requests the builtins
// produced. BlockAt returns a canned snapshot so the read path can be exercised.
type fakeBridge struct {
	mu        sync.Mutex
	setBlocks [][4]int // each: {x,y,z,state}
	spawns    [][3]int
	logs      []string
	reads     [][3]int
	readState int
	readOK    bool
}

func (f *fakeBridge) SetBlock(x, y, z, state int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setBlocks = append(f.setBlocks, [4]int{x, y, z, state})
}
func (f *fakeBridge) Spawn(x, y, z int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawns = append(f.spawns, [3]int{x, y, z})
}
func (f *fakeBridge) Log(msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, msg)
}
func (f *fakeBridge) BlockAt(x, y, z int) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, [3]int{x, y, z})
	return f.readState, f.readOK
}

var _ host.WorldBridge = (*fakeBridge)(nil)

// writeWorldPlugin writes a plugin whose hook calls every world builtin, so one
// dispatch exercises set_block/spawn/log/block_at. Returns the entrypoint path.
func writeWorldPlugin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.py")
	body := `last_read = None
def on_break(x, y, z, state, player_id):
    global last_read
    set_block(x, y + 1, z, 7)
    spawn(x, y, z)
    log("broke a block")
    last_read = block_at(x, y, z)
register("on_block_break", on_break)
`
	if err := os.WriteFile(entry, []byte(body), 0o600); err != nil {
		t.Fatalf("write world plugin main.py: %v", err)
	}
	return entry
}

// TestWorldBuiltinsProduceRequests: loading the plugin, installing a fake bridge, and
// dispatching its hook off-tick produces exactly the requests the .py asked for, with
// the right scalars (set_block carries x,y+1,z,7; spawn carries x,y,z; log carries the
// message; block_at returns the bridge's snapshot copy).
func TestWorldBuiltinsProduceRequests(t *testing.T) {
	entry := writeWorldPlugin(t)
	rt, err := Load(entry)
	if err != nil {
		t.Fatalf("Load(world plugin): %v", err)
	}
	defer rt.Close()

	fb := &fakeBridge{readState: 99, readOK: true}
	rt.SetWorldBridge(fb)

	// Dispatch the hook off-tick (GIL-held, like the real pool worker).
	if err := rt.CallHook("on_block_break", 10, 64, 20, 0, 1); err != nil {
		t.Fatalf("CallHook(on_block_break): %v", err)
	}

	fb.mu.Lock()
	defer fb.mu.Unlock()
	if len(fb.setBlocks) != 1 || fb.setBlocks[0] != [4]int{10, 65, 20, 7} {
		t.Fatalf("set_block requests = %v, want one {10,65,20,7}", fb.setBlocks)
	}
	if len(fb.spawns) != 1 || fb.spawns[0] != [3]int{10, 64, 20} {
		t.Fatalf("spawn requests = %v, want one {10,64,20}", fb.spawns)
	}
	if len(fb.logs) != 1 || fb.logs[0] != "broke a block" {
		t.Fatalf("log requests = %v, want one \"broke a block\"", fb.logs)
	}
	if len(fb.reads) != 1 || fb.reads[0] != [3]int{10, 64, 20} {
		t.Fatalf("block_at requests = %v, want one {10,64,20}", fb.reads)
	}

	// The plugin captured block_at's return — confirm the snapshot copy reached the .py.
	lr := pluginGlobalTuple(t, rt, "last_read")
	if lr[0] != 99 || lr[1] != 1 {
		t.Fatalf("plugin last_read = (%d, %d), want (99, 1) — the snapshot copy must reach python", lr[0], lr[1])
	}
}

// TestWorldBuiltinsNoBridgeNoOp: with NO bridge installed (the server never wired the
// world lane), the builtins are safe no-ops — the hook runs without error and produces
// no requests. The world-bridge is additive: a python plugin without a world lane just
// can't mutate.
func TestWorldBuiltinsNoBridgeNoOp(t *testing.T) {
	entry := writeWorldPlugin(t)
	rt, err := Load(entry)
	if err != nil {
		t.Fatalf("Load(world plugin): %v", err)
	}
	defer rt.Close()
	// No SetWorldBridge call → rt.bridge is nil.

	if err := rt.CallHook("on_block_break", 1, 64, 1, 0, 1); err != nil {
		t.Fatalf("CallHook with no bridge must be a clean no-op, got: %v", err)
	}
}

// pluginGlobalTuple reads a plugin module-level 2-tuple of ints back under the GIL
// (block_at returns (state, ok); the plugin stashes it). Returns {state, ok-as-int}.
func pluginGlobalTuple(t *testing.T, rt *Runtime, name string) [2]int {
	t.Helper()
	lock := py.NewLock()
	defer lock.Unlock()
	v, err := rt.globals.GetItemString(name)
	if err != nil || v == nil {
		t.Fatalf("read plugin global %q: %v", name, err)
	}
	tup, ok := v.(*py.Tuple)
	if !ok {
		t.Fatalf("plugin global %q is not a tuple (got %T)", name, v)
	}
	s, err := tup.GetIndex(0)
	if err != nil {
		t.Fatalf("read %q[0]: %v", name, err)
	}
	okObj, err := tup.GetIndex(1)
	if err != nil {
		t.Fatalf("read %q[1]: %v", name, err)
	}
	state, isLong := s.(*py.Long)
	if !isLong {
		t.Fatalf("%q[0] is not an int (got %T)", name, s)
	}
	b, isBool := okObj.(*py.Bool)
	if !isBool {
		t.Fatalf("%q[1] is not a bool (got %T)", name, okObj)
	}
	okInt := 0
	if b.Bool() {
		okInt = 1
	}
	return [2]int{int(state.Int64()), okInt}
}
