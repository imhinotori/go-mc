package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// leaf_decay_test.go - the LeavesBlock DISTANCE-maintenance seam (leaf_decay.go): the updateShape
// schedule + the scheduled-tick DISTANCE recompute + the log-break propagation, complementing the
// random-tick decay proofs in growth_test.go. Proves the D-B3 fix: leaves now decay when the log is
// chopped. All against the ported net.minecraft.world.level.block.LeavesBlock.

// newLeafDecayLoop wires a TickLoop with one ready all-air chunk AND a registered block-tick container
// for column (0,0), so onLeavesEdit -> scheduleLeavesTick has a container to route into (mirrors
// newSugarCaneLoop).
func newLeafDecayLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

func oakLog() block.StateID { return block.ToStateID[block.OakLog{Axis: block.Y}] }

// TestLeafAdjacentToLogLowDistanceNoDecay: a leaf recomputed next to a log lands at DISTANCE 1 and is
// NOT decaying (isRandomlyTicking false). Proves the anchor case: a leaf touching a log never decays.
func TestLeafAdjacentToLogLowDistanceNoDecay(t *testing.T) {
	loop, mgr := newLeafDecayLoop()
	pos := pk.Position{X: 4, Y: 70, Z: 4}
	mgr.SetBlock(pos, oakLeaves(7, false), dimMinY)
	mgr.SetBlock(below(pos), oakLog(), dimMinY) // a log neighbour -> getDistanceAt == 0 -> distance 1

	updated, ok := loop.leavesUpdateDistance(oakLeaves(7, false), pos)
	if !ok {
		t.Fatal("leavesUpdateDistance ok=false for oak leaves next to a log")
	}
	if d := block.LeavesDistance(updated); d != 1 {
		t.Fatalf("distance next to a log = %d, want 1 (getDistanceAt(log)+1)", d)
	}
	if block.LeavesDecaying(updated) {
		t.Fatal("a leaf at DISTANCE 1 must NOT be decaying")
	}
	if block.IsRandomlyTicking(updated) {
		t.Fatal("a DISTANCE-1 leaf must not be randomly ticking (isRandomlyTicking == d==7 && !persistent)")
	}
}

// TestLeafDistanceSevenDecaysOnRandomTick: a non-persistent leaf at DISTANCE 7 (no log in range) decays
// to air on a random tick; block -> air (the drop is the deferred loot half). Proves the decay gate
// decaying == !PERSISTENT && DISTANCE==7.
func TestLeafDistanceSevenDecaysOnRandomTick(t *testing.T) {
	loop, mgr := newLeafDecayLoop()
	pos := pk.Position{X: 6, Y: 72, Z: 6}
	leaf := oakLeaves(7, false)
	mgr.SetBlock(pos, leaf, dimMinY)

	loop.leavesRandomTick(loop.only(), leaf, pos)

	if !block.IsAir(mustGet(t, mgr, pos)) {
		t.Fatal("a non-persistent DISTANCE-7 leaf must decay to air on a random tick")
	}
}

// TestPersistentLeafNeverDecays: a PERSISTENT leaf at DISTANCE 7 never decays (player-placed leaves are
// permanent). Proves the !PERSISTENT guard.
func TestPersistentLeafNeverDecays(t *testing.T) {
	loop, mgr := newLeafDecayLoop()
	pos := pk.Position{X: 9, Y: 72, Z: 9}
	leaf := oakLeaves(7, true) // PERSISTENT
	mgr.SetBlock(pos, leaf, dimMinY)

	if block.LeavesDecaying(leaf) {
		t.Fatal("a PERSISTENT leaf must never be decaying")
	}
	if block.IsRandomlyTicking(leaf) {
		t.Fatal("a PERSISTENT leaf must not be randomly ticking")
	}
	loop.leavesRandomTick(loop.only(), leaf, pos)
	if block.IsAir(mustGet(t, mgr, pos)) {
		t.Fatal("a PERSISTENT leaf must not decay even at DISTANCE 7")
	}
}

// TestBreakingLogRaisesNeighbourLeafDistance: the D-B3 propagation. A leaf anchored at DISTANCE 1 by a
// log below it; breaking that log (an edit -> updateShapeOnEdit -> onLeavesEdit) schedules the leaf
// DISTANCE-recompute tick, and draining it (tickScheduledBlocks -> leavesTick) raises the leaf DISTANCE
// to 7 (no log left in range), making it decaying. Proves updateShape schedule + tick recompute.
func TestBreakingLogRaisesNeighbourLeafDistance(t *testing.T) {
	loop, mgr := newLeafDecayLoop()
	leafPos := pk.Position{X: 3, Y: 74, Z: 3}
	logPos := below(leafPos)

	// Seed: a DISTANCE-1 leaf anchored by the log below it (its true steady state next to a log).
	mgr.SetBlock(leafPos, oakLeaves(1, false), dimMinY)
	mgr.SetBlock(logPos, oakLog(), dimMinY)

	// Break the log: set air, then run the general neighbour-update dispatch exactly as block_break does.
	mgr.SetBlock(logPos, loop.airState(), dimMinY)
	loop.updateShapeOnEdit(logPos, loop.airState())

	// updateShape must have SCHEDULED a DISTANCE-recompute tick on the neighbour leaf.
	if !loop.hasScheduledBlockTick(leafPos, blockTickType(block.StateList[oakLeaves(1, false)].ID())) {
		t.Fatal("breaking a log must schedule a DISTANCE-recompute tick on the neighbour leaf (updateShape)")
	}

	// Drain the scheduled tick at its target game-time: leavesTick recomputes DISTANCE from live
	// neighbours (the log is gone -> all neighbours air -> DISTANCE 7).
	loop.gametime++
	loop.tickScheduledBlocks()

	got := mustGet(t, mgr, leafPos)
	if d := block.LeavesDistance(got); d != 7 {
		t.Fatalf("after the log break the leaf DISTANCE = %d, want 7 (no log in range)", d)
	}
	if !block.LeavesDecaying(got) {
		t.Fatal("a leaf recomputed to DISTANCE 7 (log gone) must be decaying")
	}
	if !block.IsRandomlyTicking(got) {
		t.Fatal("a decaying leaf must be randomly ticking so the driver can decay it")
	}
}
