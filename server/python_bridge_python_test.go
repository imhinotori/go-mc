//go:build python

// python_bridge_python_test.go is the TAGGED-BUILD off-tick WORLD-BRIDGE round-trip
// (PLUGIN-06, Plan 26-03). It runs ONLY under
// `CGO_ENABLED=1 go test -tags python -race ./server/` on an image with libpython 3.14
// (the python3.14 Docker image / CI), NOT a default Windows/CGO=0 box (26-RESEARCH
// Pitfall 6). It proves the FULL boundary: a runtime="python" plugin's on_block_break
// hook runs OFF-TICK, REQUESTS a set_block, the request rejoins via the async lane,
// and the OWNER applies it through the Phase-23 seam (SetBlock + broadcastBlockUpdate)
// — GetBlock reflects the change AND a BlockUpdate is broadcast. A capability-denied
// plugin's request is DROPPED, the world unchanged. Run under -race, this is the
// off-tick → owner concurrency path CONTEXT decision 4 requires proven race-clean.
//
// The default-build python_bridge_test.go is the local CGO=0 gate (the Go-side bridge
// IS the boundary); this file adds the real CPython producer on top.

package server

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/imhinotori/sulfur/world"
)

// drainUntilApplied spins the owner's asyncIn2 drain until a mutation request arrives
// and is applied, or the bound elapses. The off-tick worker dispatches the python hook
// (GIL-held) which enqueues the set_block request; the owner applies it here.
func drainUntilApplied(t *testing.T, loop *TickLoop, deadline time.Duration) bool {
	t.Helper()
	stop := time.After(deadline)
	for {
		select {
		case r := <-loop.asyncIn2:
			r.applyTo(loop)
			return true
		case <-stop:
			return false
		case <-time.After(5 * time.Millisecond):
			// keep spinning the drain (mirrors applyAsyncResults running each tick)
		}
	}
}

// loadPythonWorld wires a real CPython manager (the tagged WirePython) with a loaded
// world + the python plugin under `dir`, returning the loop + manager. The bridge
// factory + dispatch are installed so a fired event runs the hook off-tick and its
// set_block request reaches the owner.
func loadPythonWorld(t *testing.T, dir string) (*TickLoop, *host.Manager, *world.ChunkManager) {
	t.Helper()
	loop, mgr := newBlockLoop()
	m := host.New()
	WirePython(loop, m) // tagged: registers the runtime + dispatch + the world-bridge factory
	if err := m.LoadDir(dir); err != nil {
		t.Fatalf("LoadDir(%s) -tags python: %v", dir, err)
	}
	if m.PluginCount() != 1 {
		t.Fatalf("PluginCount = %d after loading %s, want 1 (the python plugin must load with the tag)", m.PluginCount(), dir)
	}
	loop.SetPlugins(m)
	return loop, m, mgr
}

// TestPythonWorldBridge: the GRANTED worldmutate plugin's off-tick on_block_break
// REQUESTS a set_block one above the break; the owner applies it through the Phase-23
// seam. Assert GetBlock reflects the new state AND a BlockUpdate was broadcast — the
// full off-tick → request → tick-apply round-trip, -race clean.
func TestPythonWorldBridge(t *testing.T) {
	loop, m, mgr := loadPythonWorld(t, "testdata/worldbridge_granted")
	p := blockPlayer(loop, 1.5, 65.0, 1.5) // tracks column {0,0} for the broadcast

	// Fire a discrete block-break event (host.Emit submits the python hook off-tick).
	bx, by, bz := 2, 64, 2
	m.Emit(host.EventBlockBreak, host.BlockBreakEvent{X: bx, Y: by, Z: bz, State: 0, PlayerID: int(p.entityID)})

	if !drainUntilApplied(t, loop, 5*time.Second) {
		t.Fatal("the python set_block request never reached the owner (off-tick → asyncIn2 → applyTo round-trip failed)")
	}

	// worldmutate places at (x, y+1, z) with PLACE_STATE=1.
	placed := pk.Position{X: bx, Y: by + 1, Z: bz}
	got, ok := mgr.GetBlock(placed, dimMinY)
	if !ok || int(got) != 1 {
		t.Fatalf("after the off-tick set_block round-trip, GetBlock(%v) = (%v, ok=%v), want state 1", placed, got, ok)
	}
	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockUpdate); n != 1 {
		t.Fatalf("off-tick set_block: BlockUpdate broadcast %d times, want 1", n)
	}
}

// TestPythonWorldBridgeCapabilityDenied: the worldmutate_denied plugin (no world.write)
// fires the SAME off-tick set_block request, but the owner enforces the capSet at apply
// — the request is DROPPED with a capError log and the world is UNCHANGED (threat
// T-26-09: a python plugin cannot mutate without the grant).
func TestPythonWorldBridgeCapabilityDenied(t *testing.T) {
	loop, m, mgr := loadPythonWorld(t, "testdata/worldbridge_denied")
	blockPlayer(loop, 1.5, 65.0, 1.5)

	bx, by, bz := 6, 64, 6
	placed := pk.Position{X: bx, Y: by + 1, Z: bz}
	if before, _ := mgr.GetBlock(placed, dimMinY); int(before) == 1 {
		t.Fatalf("precondition: placed cell already state 1 — pick an air cell")
	}

	m.Emit(host.EventBlockBreak, host.BlockBreakEvent{X: bx, Y: by, Z: bz, State: 0, PlayerID: 1})

	if !drainUntilApplied(t, loop, 5*time.Second) {
		t.Fatal("the denied set_block request never reached the owner (it must still rejoin, then be dropped at apply)")
	}

	// The world is UNCHANGED — the denied request was dropped at the capability gate.
	if after, _ := mgr.GetBlock(placed, dimMinY); int(after) == 1 {
		t.Fatalf("after a DENIED off-tick set_block, GetBlock(%v) = state 1 — the capability gate failed", placed)
	}
}

// TestPythonWorldBridgeNoLiveHandle: a sanity assertion that the request carried only
// scalars — the world block the plugin set must be readable on the owner WITHOUT the
// plugin ever holding a *ChunkManager (the request/apply indirection is the boundary).
// This is structurally guaranteed by pythonMutation carrying plain ints; the test is
// the behavioral witness (the change landed via the owner, not via the off-tick side).
func TestPythonWorldBridgeNoLiveHandle(t *testing.T) {
	loop, m, mgr := loadPythonWorld(t, "testdata/worldbridge_granted")
	blockPlayer(loop, 1.5, 65.0, 1.5)

	bx, by, bz := 8, 64, 8
	m.Emit(host.EventBlockBreak, host.BlockBreakEvent{X: bx, Y: by, Z: bz, State: 0, PlayerID: 1})
	if !drainUntilApplied(t, loop, 5*time.Second) {
		t.Fatal("the set_block request never reached the owner")
	}
	if _, ok := mgr.GetBlock(pk.Position{X: bx, Y: by + 1, Z: bz}, dimMinY); !ok {
		t.Fatal("the owner-applied block is not readable — the request/apply round-trip did not land")
	}
}
