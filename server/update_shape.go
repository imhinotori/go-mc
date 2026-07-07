package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// update_shape.go - the general Level.setBlock flag-driven neighbour-update dispatch (D-B1): the
// 1:1 port of the vanilla update propagation that runs after a block edit, split into the two
// vanilla halves plus their highest-value block overrides.
//
//	(1) updateNeighbourShapes (flag UPDATE_CLIENTS+ path -> BlockStateBase.updateNeighbourShapes):
//	    for each of the 6 neighbours, neighbor.updateShape(direction-toward-changed-cell, changedState)
//	    returns a possibly-changed state that is set via Block.updateOrDestroy. Ported override:
//	    CrossCollisionBlock.updateShape (fence / iron-bars / glass-pane connection booleans).
//	(2) updateNeighborsAt (flag UPDATE_NEIGHBORS -> Level.updateNeighborsAt -> per-neighbour
//	    neighborChanged): a neighbour reacts to the change. Ported override: the drop-if-unsupported
//	    reaction for attachment blocks (a torch pops off when its support is removed).
//
// Existing targeted hooks (reconcileEdit: updateVegetationOnEdit, scheduleFluidNeighborsOnEdit,
// onRedstoneEdit, onBlockTickEdit, onFallingBlockEdit, onObserverEdit) already cover the fluid /
// vegetation / redstone / falling / observer slices of this same vanilla chain; this file adds the
// GENERAL CrossCollisionBlock connection dispatch + the attachment drop-if-unsupported reaction
// that were missing (D-B1). It is wired into the place + break paths as a single call each.
//
// Cited bytecode (temp/cache/26.2-inner.jar, decompiled javap -c -p this session):
//
//	net.minecraft.world.level.Level.setBlock(BlockPos, BlockState, int flags, int recursionLeft):
//	    LevelChunk.setBlockState(pos, state, flags);
//	    if ((flags & 2) != 0 && ...) sendBlockUpdated(...);                 // UPDATE_CLIENTS
//	    if ((flags & 1) != 0) { updateNeighborsAt(pos, state.getBlock());   // UPDATE_NEIGHBORS
//	                            if (state.hasAnalogOutputSignal()) updateNeighbourForOutputSignal(...); }
//	    if ((flags & 16) == 0 && recursionLeft > 0) {                       // !UPDATE_KNOWN_SHAPE
//	        int passFlags = flags & ~34;   // clear UPDATE_NEIGHBORS(1)?? no: & 0xFFFFFFDE = ~34 = ~(2|32)
//	        state.updateIndirectNeighbourShapes(this, pos, passFlags, recursionLeft-1);
//	        newState.updateNeighbourShapes(this, pos, passFlags, recursionLeft-1);
//	        state.updateIndirectNeighbourShapes(this, pos, passFlags, recursionLeft-1); }
//	Block.UPDATE_NEIGHBORS=1, UPDATE_CLIENTS=2, UPDATE_INVISIBLE=4, UPDATE_IMMEDIATE=8,
//	    UPDATE_KNOWN_SHAPE=16, UPDATE_SUPPRESS_DROPS=32, UPDATE_MOVE_BY_PISTON=64,
//	    UPDATE_NONE=0, UPDATE_ALL=UPDATE_NEIGHBORS|UPDATE_CLIENTS=3, UPDATE_LIMIT=512.
//	BlockStateBase.updateNeighbourShapes(level, pos, flags, recursionLeft):
//	    for (Direction d : UPDATE_SHAPE_ORDER) { mut.setWithOffset(pos, d);
//	        level.neighborShapeChanged(d.getOpposite(), pos, mut, asState(), flags, recursionLeft); }
//	NeighborUpdater.executeShapeUpdate(level, dir, pos, neighborPos, neighborState, flags, recursionLeft):
//	    BlockState s = level.getBlockState(pos);
//	    BlockState u = s.updateShape(level, level, pos, dir, neighborPos, neighborState, level.getRandom());
//	    Block.updateOrDestroy(s, u, level, pos, flags, recursionLeft);
//	Block.updateOrDestroy(old, newState, level, pos, flags, recursionLeft):
//	    if (newState != old) { if (newState.isAir()) { if (!isClientSide)
//	          level.destroyBlock(pos, (flags & 32) == 0, null, recursionLeft); }
//	        else level.setBlock(pos, newState, flags & ~32, recursionLeft); }
//	FenceBlock.updateShape / IronBarsBlock.updateShape (see level/block/connections.go):
//	    if (dir.getAxis().isHorizontal()) return state.setValue(PROPERTY_BY_DIRECTION.get(dir),
//	        connectsTo/attachsTo(neighborState, neighborState.isFaceSturdy(level, neighborPos, dir.getOpposite())));
//	    else super (vertical: unchanged).
//	WallTorchBlock.canSurvive: state.isFaceSturdy(behindPos, FACING.getOpposite()).
//	TorchBlock.canSurvive: canSupportCenter(level, below, UP).
//
// All access is on the TICK goroutine over the tick-owned ChunkManager (TICK-05): world reads/writes
// go through the dimension-aware manager, and the drop reuses the GAMEPLAY-06 spawnBlockDrop path.

// updateShapeRecursionLimit is Block.UPDATE_LIMIT (512): the recursionLeft seed threaded through
// updateOrDestroy so a self-supporting cascade (a stacked plant / a chain of unsupported
// attachments) fully resolves but a pathological loop cannot run forever. CITE: Block.UPDATE_LIMIT.
const updateShapeRecursionLimit = 512

// updateShapeOrder is BlockBehaviour.UPDATE_SHAPE_ORDER: the fixed 6-direction iteration order the
// vanilla updateNeighbourShapes walks (WEST, EAST, DOWN, UP, NORTH, SOUTH). CITE:
// NeighborUpdater.UPDATE_ORDER / BlockBehaviour.UPDATE_SHAPE_ORDER static init.
var updateShapeOrder = [6]block.Direction{block.West, block.East, block.Down, block.Up, block.North, block.South}

// updateShapeOnEdit runs the general neighbour-update dispatch after the cell at pos changed to
// newState (a place or a break; for a break newState is air). It is the Sulfur equivalent of the
// Level.setBlock flag-1+2 tail for the CrossCollisionBlock + attachment slices:
//
//	(A) the CHANGED cell itself, if it is a CrossCollisionBlock (a freshly-placed fence), computes
//	    its own 4-direction connection state from its horizontal neighbours (Block.getStateForPlacement
//	    seeds a placed fence disconnected; updateNeighbourShapes then wires it). This is done first so a
//	    placed fence connects on the same edit.
//	(B) updateNeighbourShapes(pos): each of the 6 neighbours runs updateShape given the changed cell.
//	    The ported override is CrossCollisionBlock: a fence/pane/bar neighbour recomputes its connection
//	    boolean toward pos.
//	(C) updateNeighborsAt(pos): each neighbour reacts via neighborChanged. The ported override is the
//	    attachment drop-if-unsupported reaction (a torch pops off when its support was removed).
//
// Tick-owned. mgr is the edited cell dimension world; minY that dimension min-Y.
func (t *TickLoop) updateShapeOnEdit(pos pk.Position, newState block.StateID) {
	mgr := t.world()
	if mgr == nil {
		return
	}
	// (A) The changed cell: if it is a CrossCollisionBlock, compute its own connection state from its
	// four horizontal neighbours and write it back (updateNeighbourShapes applied to the placed block).
	if block.IsCrossCollisionBlock(newState) {
		t.recomputeCrossConnections(pos, newState)
	}
	// (B) + (C): the 6 neighbours.
	t.updateNeighbourShapes(pos, updateShapeRecursionLimit)
	t.updateNeighborsAt(pos, updateShapeRecursionLimit)
	// (D) LEAVES: the changed cell also runs LeavesBlock.updateShape on each of its six neighbour leaves
	// (getDistanceAt(newState) feeds their DISTANCE recompute). This is what makes a chopped log wake the
	// surrounding canopy: the log removal raises each neighbour leaf DISTANCE toward 7 and, over the
	// scheduled ticks, decays it. Kept as a dedicated hook (like the fluid/vegetation/redstone seams) so
	// the general dispatcher stays the CrossCollisionBlock+attachment subset. CITE: LeavesBlock.updateShape.
	t.onLeavesEdit(pos, newState)
}

// recomputeCrossConnections is the changed-cell half: a CrossCollisionBlock at pos re-derives each
// of its four horizontal connection booleans from the neighbour in that direction (the union of
// getStateForPlacement + the per-direction updateShape). It writes the fully-connected state back
// and broadcasts it. Tick-owned. CITE: CrossCollisionBlock.updateShape aggregated over the 4 dirs.
func (t *TickLoop) recomputeCrossConnections(pos pk.Position, state block.StateID) {
	mgr := t.world()
	if mgr == nil {
		return
	}
	selfBlock := block.StateList[state]
	cur := state
	for _, dir := range crossHorizontalDirsServer() {
		np := relative(pos, dir)
		ns, ok := mgr.GetBlock(np, dimMinY)
		if !ok {
			continue // unreadable neighbour: leave that connection at its current value
		}
		neighborBlock := block.StateList[ns]
		// connectsTo(neighborState, neighborState.isFaceSturdy(np, dir.getOpposite()), dir):
		// the neighbour face pointing back at pos is dir.getOpposite().
		sturdy := block.IsFaceSturdy(ns, dirOpposite(dir), block.SupportFull)
		connected := block.CrossConnectsTo(selfBlock, neighborBlock, sturdy, dir)
		if nc, ok := block.WithConnection(cur, dir, connected); ok {
			cur = nc
		}
	}
	if cur != state {
		if mgr.SetBlock(pos, cur, dimMinY) {
			t.broadcastBlockUpdate(pos, cur)
		}
	}
}

// crossHorizontalDirsServer returns the four horizontal Directions carrying a CrossCollisionBlock
// connection property (NORTH/SOUTH/WEST/EAST) - the PROPERTY_BY_DIRECTION.keySet() the dispatcher
// walks when re-deriving a placed fence own connection state. CITE: CrossCollisionBlock.PROPERTY_BY_DIRECTION.
func crossHorizontalDirsServer() [4]block.Direction {
	return [4]block.Direction{block.North, block.South, block.West, block.East}
}

// updateNeighbourShapes ports BlockStateBase.updateNeighbourShapes(pos): for each of the 6
// UPDATE_SHAPE_ORDER directions, the neighbour at pos+d runs updateShape given the changed cell.
// The single ported override is CrossCollisionBlock: a fence/pane/bar neighbour recomputes its
// connection boolean toward pos (the changed cell). recursionLeft bounds any cascade via
// updateOrDestroy. Tick-owned. CITE: BlockStateBase.updateNeighbourShapes -> executeShapeUpdate.
func (t *TickLoop) updateNeighbourShapes(pos pk.Position, recursionLeft int) {
	if recursionLeft <= 0 {
		return
	}
	mgr := t.world()
	if mgr == nil {
		return
	}
	changedState, ok := mgr.GetBlock(pos, dimMinY)
	if !ok {
		return
	}
	changedBlock := block.StateList[changedState]
	for _, d := range updateShapeOrder {
		np := relative(pos, d)
		ns, ok := mgr.GetBlock(np, dimMinY)
		if !ok {
			continue
		}
		if !block.IsCrossCollisionBlock(ns) {
			continue // only the CrossCollisionBlock override changes shape here (v1 subset)
		}
		// From the neighbour perspective the changed cell lies in direction d.getOpposite(); the
		// neighbour recomputes its connection toward pos. sturdy = changedCell.isFaceSturdy(face d),
		// the changed cell face that points at the neighbour.
		towardChanged := dirOpposite(d)
		sturdy := block.IsFaceSturdy(changedState, d, block.SupportFull)
		neighborBlock := block.StateList[ns]
		connected := block.CrossConnectsTo(neighborBlock, changedBlock, sturdy, towardChanged)
		u, ok := block.WithConnection(ns, towardChanged, connected)
		if !ok || u == ns {
			continue // updateShape returned the state unchanged
		}
		// Block.updateOrDestroy(ns, u): u is never air here (a connection flip), so it is the
		// setBlock(u, flags & ~32) branch. Write + broadcast; recurse the shape update at np so a
		// chained CrossCollisionBlock line settles (bounded by recursionLeft).
		if mgr.SetBlock(np, u, dimMinY) {
			t.broadcastBlockUpdate(np, u)
		}
	}
}

// updateNeighborsAt ports Level.updateNeighborsAt(pos) -> per-neighbour neighborChanged, scoped to
// the ported attachment drop-if-unsupported reaction: a torch (standing or wall) whose support was
// the changed cell pops off + drops when it can no longer survive. Each destroy is itself an edit,
// so the destroyed cell recurses (bounded by recursionLeft) to re-notify ITS neighbours - a chain
// of stacked attachments cascades. Tick-owned. CITE: Level.updateNeighborsAt -> Block.neighborChanged
// -> canSurvive -> dropResources + removeBlock.
func (t *TickLoop) updateNeighborsAt(pos pk.Position, recursionLeft int) {
	if recursionLeft <= 0 {
		return
	}
	mgr := t.world()
	if mgr == nil {
		return
	}
	for _, d := range updateShapeOrder {
		np := relative(pos, d)
		ns, ok := mgr.GetBlock(np, dimMinY)
		if !ok {
			continue
		}
		if t.torchCanSurvive(np, ns) {
			continue // still supported, or not an attachment we handle -> neighborChanged no-op
		}
		// neighborChanged found the attachment unsupported: dropResources + removeBlock to air, then
		// recurse (the destroyed cell notifies its own neighbours).
		brokenState := ns
		if !mgr.SetBlock(np, t.airState(), dimMinY) {
			continue
		}
		t.broadcastBlockUpdate(np, t.airState())
		t.spawnBlockDrop(nil, np, brokenState) // entity=null: drops regardless of who removed support
		t.updateNeighborsAt(np, recursionLeft-1)
	}
}

// torchCanSurvive returns true if the block at pos either is NOT a torch we handle, or is a torch
// whose support is present. It is the neighborChanged canSurvive gate for the torch family:
//
//	standing torch (Torch/SoulTorch/RedstoneTorch/CopperTorch): canSupportCenter(below, UP) ==
//	    below.isFaceSturdy(UP, CENTER). CITE: TorchBlock.canSurvive -> Block.canSupportCenter.
//	wall torch (WallTorch/...): behind.isFaceSturdy(FACING.getOpposite()) where behind = pos.relative(
//	    FACING.getOpposite()). CITE: WallTorchBlock.canSurvive.
//
// A non-torch block returns true (this dispatcher only owns the torch attachment reaction; other
// families - vegetation, rails, redstone components - are covered by their own hooks). An unreadable
// support cell returns true (non-destructive on an unloaded read, matching updateVegetationOnEdit).
func (t *TickLoop) torchCanSurvive(pos pk.Position, s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return true
	}
	mgr := t.world()
	if mgr == nil {
		return true
	}
	switch b := block.StateList[s].(type) {
	case block.Torch, block.SoulTorch, block.RedstoneTorch, block.CopperTorch:
		// Standing torch: the cell below must support a centered attachment on its UP face.
		belowState, ok := mgr.GetBlock(below(pos), dimMinY)
		if !ok {
			return true
		}
		return block.IsFaceSturdy(belowState, block.Up, block.SupportCenter)
	case block.WallTorch:
		return t.wallTorchSupported(pos, b.Facing)
	case block.SoulWallTorch:
		return t.wallTorchSupported(pos, b.Facing)
	case block.RedstoneWallTorch:
		return t.wallTorchSupported(pos, b.Facing)
	case block.CopperWallTorch:
		return t.wallTorchSupported(pos, b.Facing)
	default:
		return true // not a torch we own
	}
}

// wallTorchSupported ports WallTorchBlock.canSurvive: the block behind the torch (pos.relative(
// FACING.getOpposite())) must be face-sturdy on the face pointing at the torch (FACING.getOpposite()).
// CITE: WallTorchBlock.canSurvive.
func (t *TickLoop) wallTorchSupported(pos pk.Position, facing block.Direction) bool {
	mgr := t.world()
	if mgr == nil {
		return true
	}
	behind := dirOpposite(facing)
	bp := relative(pos, behind)
	behindState, ok := mgr.GetBlock(bp, dimMinY)
	if !ok {
		return true
	}
	// isFaceSturdy(level, behindPos, FACING.getOpposite()==behind) -> the behind block face pointing
	// toward the torch is `facing` (the opposite of `behind`). WallTorchBlock passes FACING.getOpposite()
	// as the direction, and isFaceSturdy checks that block face; the behind block face toward the torch
	// is `facing`.
	return block.IsFaceSturdy(behindState, facing, block.SupportFull)
}
