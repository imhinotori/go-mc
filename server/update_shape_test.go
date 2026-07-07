package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// update_shape_test.go covers D-B1: the general Level.setBlock neighbour-update dispatch for the
// CrossCollisionBlock connection slice (fence/pane/bar) and the attachment drop-if-unsupported
// slice (torch). Tests assert (1) the connection predicate rules, (2) two adjacent fences connect
// on place (updateNeighbourShapes ran both ways), (3) a glass pane connects to a solid neighbour,
// and (4) breaking a torch's support drops the torch (neighborChanged -> canSurvive false -> drop).

// TestConnectionPredicates unit-checks the jar-cited CrossCollisionBlock connection rules.
func TestConnectionPredicates(t *testing.T) {
	oakFence := block.OakFence{}
	netherFence := block.NetherBrickFence{}
	pane := block.GlassPane{}
	stone := block.Stone{}
	barrier := block.Barrier{}
	pumpkin := block.Pumpkin{}

	// A fence at the placed state id, and a pane, so IsCrossCollisionBlock is exercised too.
	if !block.IsCrossCollisionBlock(block.DefaultStateID["minecraft:oak_fence"]) {
		t.Fatalf("oak_fence must be a CrossCollisionBlock")
	}
	if !block.IsCrossCollisionBlock(block.DefaultStateID["minecraft:glass_pane"]) {
		t.Fatalf("glass_pane must be a CrossCollisionBlock")
	}
	if block.IsCrossCollisionBlock(block.ToStateID[block.Stone{}]) {
		t.Fatalf("stone must NOT be a CrossCollisionBlock")
	}

	cases := []struct {
		name     string
		self     block.Block
		neighbor block.Block
		sturdy   bool
		dir      block.Direction
		want     bool
	}{
		{"fence connects to sturdy solid", oakFence, stone, true, block.North, true},
		{"fence does NOT connect to a non-sturdy face", oakFence, stone, false, block.North, false},
		{"fence connects to same wooden fence (no sturdy needed)", oakFence, oakFence, false, block.North, true},
		{"wooden fence does NOT connect to nether_brick_fence (different family)", oakFence, netherFence, false, block.North, false},
		{"fence does NOT connect to a barrier even if sturdy (exception)", oakFence, barrier, true, block.North, false},
		{"fence does NOT connect to a pumpkin even if sturdy (exception)", oakFence, pumpkin, true, block.North, false},
		{"pane connects to sturdy solid", pane, stone, true, block.East, true},
		{"pane connects to another pane (iron-bars family)", pane, pane, false, block.East, true},
		{"pane does NOT connect to a wooden fence (fences are not bars-family)", pane, oakFence, false, block.East, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := block.CrossConnectsTo(c.self, c.neighbor, c.sturdy, c.dir); got != c.want {
				t.Fatalf("CrossConnectsTo(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

// TestFenceConnectAdjacentOnPlace: placing a fence next to an existing fence connects BOTH — the
// existing fence gets its connection toward the new one (updateNeighbourShapes) and the new fence
// derives its own connection from the existing one (recomputeCrossConnections).
func TestFenceConnectAdjacentOnPlace(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 65.0, 1.5)

	fenceSID := block.DefaultStateID["minecraft:oak_fence"]
	posA := pk.Position{X: 1, Y: 64, Z: 1}
	posB := pk.Position{X: 2, Y: 64, Z: 1} // one cell EAST of A

	// A is placed first (already in the world, fully disconnected as getStateForPlacement seeds it).
	mgr.SetBlock(posA, fenceSID, dimMinY)
	// Now B is placed EAST of A: run the dispatch as the place path would.
	mgr.SetBlock(posB, fenceSID, dimMinY)
	loop.updateShapeOnEdit(posB, fenceSID)

	// B is EAST of A, so B connects WEST (toward A) and A connects EAST (toward B).
	aState, _ := mgr.GetBlock(posA, dimMinY)
	bState, _ := mgr.GetBlock(posB, dimMinY)
	if !block.ConnectionValue(aState, block.East) {
		t.Fatalf("fence A (west) must connect EAST toward the newly placed fence B; state=%d", aState)
	}
	if !block.ConnectionValue(bState, block.West) {
		t.Fatalf("newly placed fence B (east) must connect WEST toward fence A; state=%d", bState)
	}
}

// TestGlassPaneConnectsToSolid: placing a glass pane next to a solid stone block sets the pane's
// connection toward the stone (IronBarsBlock.attachsTo on a sturdy face).
func TestGlassPaneConnectsToSolid(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 65.0, 1.5)

	paneSID := block.DefaultStateID["minecraft:glass_pane"]
	stoneSID := block.ToStateID[block.Stone{}]
	stonePos := pk.Position{X: 1, Y: 64, Z: 1}
	panePos := pk.Position{X: 1, Y: 64, Z: 2} // one cell SOUTH of the stone

	mgr.SetBlock(stonePos, stoneSID, dimMinY)
	mgr.SetBlock(panePos, paneSID, dimMinY)
	loop.updateShapeOnEdit(panePos, paneSID)

	// The pane is SOUTH of the stone, so the pane connects NORTH (toward the stone).
	paneState, _ := mgr.GetBlock(panePos, dimMinY)
	if !block.ConnectionValue(paneState, block.North) {
		t.Fatalf("glass pane must connect NORTH toward the solid stone neighbour; state=%d", paneState)
	}
}

// TestNeighborChangedStandingTorchDrops: breaking the block under a standing torch drops the torch
// (neighborChanged -> TorchBlock.canSurvive false -> destroyBlock+dropResources).
func TestNeighborChangedStandingTorchDrops(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 66.0, 1.5)

	support := pk.Position{X: 1, Y: 64, Z: 1}
	torchPos := pk.Position{X: 1, Y: 65, Z: 1}
	mgr.SetBlock(support, block.ToStateID[block.Stone{}], dimMinY)
	mgr.SetBlock(torchPos, block.ToStateID[block.Torch{}], dimMinY)

	beforeItems := countItemEntities(loop)
	// Break the support: SetBlock air then run the neighbour dispatch (as the break path does).
	mgr.SetBlock(support, block.ToStateID[block.Air{}], dimMinY)
	loop.updateShapeOnEdit(support, block.ToStateID[block.Air{}])

	if got, ok := mgr.GetBlock(torchPos, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("standing torch at %v = (state %d, ok=%v), want AIR after its support was broken", torchPos, got, ok)
	}
	if got := countItemEntities(loop); got != beforeItems+1 {
		t.Fatalf("item entities = %d, want %d (the dropped torch)", got, beforeItems+1)
	}
}

// TestNeighborChangedWallTorchDrops: breaking the wall a wall_torch is attached to pops the torch
// off (WallTorchBlock.canSurvive false -> destroyBlock). A wall_torch with FACING=East is attached
// to the wall on its WEST side (behind = West). This asserts the DESTRUCTION half of neighborChanged
// (the dispatcher behaviour); the standing-torch test above asserts the drop half. (The vendored loot
// tree ships blocks/torch but not blocks/wall_torch, so a wall-torch break yields no item entity here
// - a loot-data gap outside this dispatch, not a dispatcher bug: the pop-off itself is what D-B1 owns.)
func TestNeighborChangedWallTorchDrops(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 65.0, 1.5)

	wallPos := pk.Position{X: 1, Y: 64, Z: 1}
	torchPos := pk.Position{X: 2, Y: 64, Z: 1} // one cell EAST of the wall: torch FACING=East, behind=West=wall
	mgr.SetBlock(wallPos, block.ToStateID[block.Stone{}], dimMinY)
	mgr.SetBlock(torchPos, block.ToStateID[block.WallTorch{Facing: block.East}], dimMinY)

	mgr.SetBlock(wallPos, block.ToStateID[block.Air{}], dimMinY)
	loop.updateShapeOnEdit(wallPos, block.ToStateID[block.Air{}])

	if got, ok := mgr.GetBlock(torchPos, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("wall torch at %v = (state %d, ok=%v), want AIR after its wall was broken", torchPos, got, ok)
	}
}

// TestNeighborChangedTorchSurvivesUnrelatedBreak: breaking a block that is NOT a torch's support
// leaves the torch intact (canSurvive still true).
func TestNeighborChangedTorchSurvivesUnrelatedBreak(t *testing.T) {
	loop, mgr := newDropLoop()
	_ = blockPlayer(loop, 1.5, 66.0, 1.5)

	support := pk.Position{X: 1, Y: 64, Z: 1}
	torchPos := pk.Position{X: 1, Y: 65, Z: 1}
	unrelated := pk.Position{X: 3, Y: 64, Z: 1} // far away, not adjacent to the torch
	mgr.SetBlock(support, block.ToStateID[block.Stone{}], dimMinY)
	mgr.SetBlock(torchPos, block.ToStateID[block.Torch{}], dimMinY)
	mgr.SetBlock(unrelated, block.ToStateID[block.Stone{}], dimMinY)

	beforeItems := countItemEntities(loop)
	mgr.SetBlock(unrelated, block.ToStateID[block.Air{}], dimMinY)
	loop.updateShapeOnEdit(unrelated, block.ToStateID[block.Air{}])

	if got, ok := mgr.GetBlock(torchPos, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("standing torch at %v = (state %d, ok=%v), want STILL-TORCH (its support was not broken)", torchPos, got, ok)
	}
	if got := countItemEntities(loop); got != beforeItems {
		t.Fatalf("item entities = %d, want %d (no spurious drop)", got, beforeItems)
	}
}
