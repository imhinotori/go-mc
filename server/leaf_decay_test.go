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

// TestLeafAdjacentToLogSurvivesManyRandomTicks is the DIRECT bug-report proof: a non-persistent leaf
// anchored next to a log (DISTANCE 1) driven through the REAL random-tick dispatch path
// (dispatchRandomTick) across many iterations must NEVER decay. This is the "bare trunks" symptom guard:
// isRandomlyTicking(DISTANCE<7) is false, so the driver never even routes the leaf into leavesRandomTick,
// and the leaf stays put no matter how many ticks fire. CITE: LeavesBlock.isRandomlyTicking (DISTANCE==7
// && !PERSISTENT); ServerLevel.tickChunk (only isRandomlyTicking states are randomTick'd).
func TestLeafAdjacentToLogSurvivesManyRandomTicks(t *testing.T) {
	loop, mgr := newLeafDecayLoop()
	pos := pk.Position{X: 5, Y: 71, Z: 5}
	leaf := oakLeaves(1, false) // anchored: DISTANCE 1 (a log neighbour)
	mgr.SetBlock(pos, leaf, dimMinY)
	mgr.SetBlock(below(pos), oakLog(), dimMinY)

	// isRandomlyTicking must be false for an anchored leaf: the driver would never sample it.
	if block.IsRandomlyTicking(leaf) {
		t.Fatal("a DISTANCE-1 leaf must not be randomly ticking (would wrongly expose it to decay)")
	}
	// Even if the driver DID route it (defensive), dispatchRandomTick -> leavesRandomTick must no-op
	// because decaying(DISTANCE 1)==false. Drive it directly many times: the leaf must survive.
	for i := 0; i < 4096; i++ {
		loop.dispatchRandomTick(loop.only(), leaf, pos)
	}
	got := mustGet(t, mgr, pos)
	if block.IsAir(got) {
		t.Fatal("a leaf adjacent to a log must NEVER decay, even across thousands of random ticks (bare-trunk bug)")
	}
	if !block.IsLeaves(got) || block.LeavesDistance(got) != 1 {
		t.Fatalf("the anchored leaf must stay a DISTANCE-1 leaf; got distance %d", block.LeavesDistance(got))
	}
}

// TestLeafWithinSixOfLogNeverDecays walks DISTANCE 1..6 (a leaf anywhere within a log's reach) and
// asserts none is decaying / randomly ticking — only DISTANCE 7 (out of reach) decays. Proves the
// KEY invariant: a leaf within DISTANCE<=6 of a log has distance<7 and MUST NOT decay. CITE:
// LeavesBlock.decaying (DISTANCE==7 only) / isRandomlyTicking.
func TestLeafWithinSixOfLogNeverDecays(t *testing.T) {
	for d := 1; d <= 6; d++ {
		leaf := oakLeaves(d, false)
		if block.LeavesDecaying(leaf) {
			t.Fatalf("a DISTANCE-%d leaf must NOT be decaying (only 7 decays)", d)
		}
		if block.IsRandomlyTicking(leaf) {
			t.Fatalf("a DISTANCE-%d leaf must NOT be randomly ticking", d)
		}
	}
	if !block.LeavesDecaying(oakLeaves(7, false)) {
		t.Fatal("a DISTANCE-7 non-persistent leaf MUST be decaying")
	}
}

// TestLeavesUpdateDistanceRecomputesFromNeighbours proves the DISTANCE recompute (updateDistance) is
// correct off live neighbours: a leaf two cells from a log (log -> leaf(1) -> subject) recomputes to
// DISTANCE 2, and a leaf with no log/leaf anchor within reach recomputes to 7. CITE:
// LeavesBlock.updateDistance / getDistanceAt (min over 6 neighbours of getDistanceAt+1).
func TestLeavesUpdateDistanceRecomputesFromNeighbours(t *testing.T) {
	loop, mgr := newLeafDecayLoop()

	// Chain: log at y, anchored leaf(1) at y+1, subject leaf at y+2. Subject recomputes to 2.
	logP := pk.Position{X: 8, Y: 68, Z: 8}
	midP := pk.Position{X: 8, Y: 69, Z: 8}
	topP := pk.Position{X: 8, Y: 70, Z: 8}
	mgr.SetBlock(logP, oakLog(), dimMinY)
	mgr.SetBlock(midP, oakLeaves(1, false), dimMinY)
	mgr.SetBlock(topP, oakLeaves(7, false), dimMinY)

	updated, ok := loop.leavesUpdateDistance(oakLeaves(7, false), topP)
	if !ok {
		t.Fatal("leavesUpdateDistance ok=false")
	}
	if d := block.LeavesDistance(updated); d != 2 {
		t.Fatalf("a leaf one cell above a DISTANCE-1 leaf must recompute to 2; got %d", d)
	}

	// An isolated leaf (all neighbours air) recomputes to DISTANCE 7 (getDistanceAt(air)==7 -> +1 clamp).
	isoP := pk.Position{X: 12, Y: 75, Z: 12}
	mgr.SetBlock(isoP, oakLeaves(1, false), dimMinY)
	iso, ok := loop.leavesUpdateDistance(oakLeaves(1, false), isoP)
	if !ok {
		t.Fatal("leavesUpdateDistance ok=false for isolated leaf")
	}
	if d := block.LeavesDistance(iso); d != 7 {
		t.Fatalf("an isolated leaf (no anchor) must recompute to 7; got %d", d)
	}
}
