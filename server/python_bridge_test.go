package server

// python_bridge_test.go (DEFAULT build, no tag, CGO=0) proves the Go-side of the
// WORLD-BRIDGE (PLUGIN-06, Plan 26-03) — the request → owner-apply round-trip and the
// capability gate — WITHOUT libpython. It injects mutation requests directly onto the
// async rejoin lane (standing in for the off-tick python producer, which only
// constructs + enqueues the SAME plain pythonMutation) and asserts the owner applies
// them through the Phase-23 seam (SetBlock + broadcastBlockUpdate), drops a
// capability-denied request, and snapshots a read. The TAGGED off-tick round-trip
// through real CPython (TestPythonWorldBridge) lives in python_bridge_python_test.go
// and runs on the python3.14 Docker image under -race; THIS file is the local CGO=0
// gate. The Go-side bridge IS the safety boundary, so testing it directly here is the
// authoritative -race-able proof (the python side only produces the request).

import (
	"testing"
	"time"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
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

// drainOneMutation drains exactly one asyncResult off asyncIn2 and applies it on the
// owner (standing in for applyAsyncResults). Fails the test if nothing arrives.
func drainOneMutation(t *testing.T, loop *TickLoop) {
	t.Helper()
	select {
	case r := <-loop.asyncIn2:
		r.applyTo(loop)
	case <-time.After(2 * time.Second):
		t.Fatal("no mutation request arrived on asyncIn2 (the bridge did not enqueue)")
	}
}

// TestWorldBridgeSetBlockRoundTrip: a granted (world.write) serverWorldBridge's
// SetBlock enqueues a pythonMutation onto asyncIn2; draining + applyTo on the owner
// applies it through the Phase-23 seam — GetBlock reflects the new state AND a
// BlockUpdate is broadcast to the tracking player. This is the WRITE round-trip the
// off-tick python set_block ultimately drives (it only constructs the SAME request).
func TestWorldBridgeSetBlockRoundTrip(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5) // a player tracking column {0,0} for the broadcast

	bridge, err := loop.newServerWorldBridge("worldmutate", []string{"world.write"})
	if err != nil {
		t.Fatalf("newServerWorldBridge(world.write): %v", err)
	}

	// The off-tick producer enqueues a set_block request (here we call the bridge
	// directly from the test goroutine — the python builtin does exactly this).
	target := pk.Position{X: 2, Y: 66, Z: 2}
	state := int(block.ToStateID[block.Stone{}])
	bridge.SetBlock(target.X, target.Y, target.Z, state)

	// The OWNER drains + applies through SetBlock + broadcastBlockUpdate.
	drainOneMutation(t, loop)

	// The world reflects the change (applied through the Phase-23 seam).
	got, ok := mgr.GetBlock(target, dimMinY)
	if !ok || int(got) != state {
		t.Fatalf("after set_block round-trip, GetBlock = (%v, ok=%v), want stone %d", got, ok, state)
	}
	// The clients saw it (broadcastBlockUpdate fired exactly once to the tracking player).
	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockUpdate); n != 1 {
		t.Fatalf("set_block round-trip: BlockUpdate broadcast %d times, want 1", n)
	}
}

// TestWorldBridgeCapabilityDenied: a serverWorldBridge stamped with NO capabilities
// (the worldmutate_denied grant) enqueues the SAME set_block request, but the owner
// enforces the capSet at apply — the request is DROPPED and the world is UNCHANGED
// (threat T-26-09: no silent elevation). This proves the gate, not the producer, is
// the boundary.
func TestWorldBridgeCapabilityDenied(t *testing.T) {
	loop, mgr := newBlockLoop()
	blockPlayer(loop, 1.5, 65.0, 1.5)

	bridge, err := loop.newServerWorldBridge("worldmutate_denied", nil) // no capabilities
	if err != nil {
		t.Fatalf("newServerWorldBridge(none): %v", err)
	}

	target := pk.Position{X: 3, Y: 66, Z: 3}
	state := int(block.ToStateID[block.Stone{}])

	// Confirm the column is air BEFORE the denied request.
	if before, _ := mgr.GetBlock(target, dimMinY); int(before) == state {
		t.Fatalf("precondition: target already stone (%d) — pick an air cell", state)
	}

	bridge.SetBlock(target.X, target.Y, target.Z, state)
	drainOneMutation(t, loop) // applyTo drops it (capWorldWrite missing) + logs the capError

	// The world is UNCHANGED — the denied request never reached SetBlock.
	if after, _ := mgr.GetBlock(target, dimMinY); int(after) == state {
		t.Fatalf("after a DENIED set_block, GetBlock = %d (stone) — the capability gate failed to drop the request", after)
	}
}

// TestWorldBridgeReadSnapshot: a read (block_at) goes request → owner snapshot →
// return-a-copy. A granted (world.read) bridge's BlockAt enqueues a pythonReadReq; the
// owner snapshots GetBlock and sends the copied scalar back. This must be driven on a
// SEPARATE goroutine because BlockAt blocks on the reply while the owner drains — the
// off-tick → owner handoff. No live handle crosses; only the value copy.
func TestWorldBridgeReadSnapshot(t *testing.T) {
	loop, mgr := newBlockLoop()
	bridge, err := loop.newServerWorldBridge("reader", []string{"world.read"})
	if err != nil {
		t.Fatalf("newServerWorldBridge(world.read): %v", err)
	}

	// Seed a known block on the owner.
	read := pk.Position{X: 4, Y: 67, Z: 4}
	state := int(block.ToStateID[block.Stone{}])
	mgr.SetBlock(read, block.StateID(state), dimMinY)

	// BlockAt blocks on the reply — run it off the owner goroutine and drain on this one.
	type res struct {
		state int
		ok    bool
	}
	out := make(chan res, 1)
	go func() {
		s, ok := bridge.BlockAt(read.X, read.Y, read.Z)
		out <- res{s, ok}
	}()

	drainOneMutation(t, loop) // the owner snapshots GetBlock + replies the copy

	select {
	case r := <-out:
		if !r.ok || r.state != state {
			t.Fatalf("block_at snapshot = (%d, ok=%v), want stone %d", r.state, r.ok, state)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("block_at never returned (the owner snapshot reply was not delivered)")
	}
}

// TestWorldBridgeReadDenied: a read without world.read is dropped with ok=false (the
// owner enforces capWorldRead at apply), proving reads are capability-gated too.
func TestWorldBridgeReadDenied(t *testing.T) {
	loop, mgr := newBlockLoop()
	read := pk.Position{X: 5, Y: 67, Z: 5}
	mgr.SetBlock(read, block.ToStateID[block.Stone{}], dimMinY)

	bridge, err := loop.newServerWorldBridge("reader_denied", nil) // no world.read
	if err != nil {
		t.Fatalf("newServerWorldBridge(none): %v", err)
	}

	out := make(chan bool, 1)
	go func() {
		_, ok := bridge.BlockAt(read.X, read.Y, read.Z)
		out <- ok
	}()
	drainOneMutation(t, loop)

	select {
	case ok := <-out:
		if ok {
			t.Fatal("a DENIED block_at returned ok=true — the capWorldRead gate failed to drop the read")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("denied block_at never returned")
	}
}

// TestWorldBridgeUnknownCapability: an unknown manifest capability is rejected LOUDLY
// at bridge construction (the SAME parseCapabilities loud-reject the Starlark handles
// use), so a typo'd capability never silently grants nothing (threat T-26-09).
func TestWorldBridgeUnknownCapability(t *testing.T) {
	loop, _ := newBlockLoop()
	if _, err := loop.newServerWorldBridge("typo", []string{"world.wrte"}); err == nil {
		t.Fatal("newServerWorldBridge with an unknown capability must error (load-loudly), got nil")
	}
}
