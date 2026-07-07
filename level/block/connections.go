package block

import (
	"reflect"
	"strings"
)

// connections.go - the level/block half of the CrossCollisionBlock connection model: the
// per-state helpers a fence / iron-bars / glass-pane block uses to compute its NORTH/EAST/
// SOUTH/WEST connection booleans from a horizontal neighbour. These are the pure, no-world-
// context predicates (the server dispatcher in server/update_shape.go supplies the neighbour
// state + face-sturdiness); the connection RULE itself is a 1:1 port of the vanilla bytecode.
//
// Cited bytecode (temp/cache/26.2-inner.jar, decompiled javap -c -p this session):
//
//	CrossCollisionBlock - abstract base of FenceBlock and IronBarsBlock (StainedGlassPaneBlock
//	    extends IronBarsBlock). Carries BooleanProperty NORTH/EAST/SOUTH/WEST (+ WATERLOGGED)
//	    and PROPERTY_BY_DIRECTION.
//	FenceBlock.updateShape(state, ..., dir, neighborPos, neighborState, ...):
//	    if (dir.getAxis().isHorizontal())
//	        return state.setValue(PROPERTY_BY_DIRECTION.get(dir),
//	            connectsTo(neighborState, neighborState.isFaceSturdy(level, neighborPos, dir.getOpposite()),
//	                       dir.getOpposite()));
//	    else return super.updateShape(...);                              // vertical dir: unchanged
//	FenceBlock.connectsTo(state, isSturdy, dir):
//	    sameFence = isSameFence(state);
//	    gate = state.getBlock() instanceof FenceGateBlock and FenceGateBlock.connectsToDirection(state, dir);
//	    return (!isExceptionForConnection(state) and isSturdy) or sameFence or gate;
//	FenceBlock.isSameFence(state):
//	    return state.is(BlockTags.FENCES)
//	        and (state.is(BlockTags.WOODEN_FENCES) == defaultBlockState().is(BlockTags.WOODEN_FENCES));
//	IronBarsBlock.updateShape(state, ..., dir, neighborPos, neighborState, ...):
//	    if (dir.getAxis().isHorizontal())
//	        return state.setValue(PROPERTY_BY_DIRECTION.get(dir),
//	            attachsTo(neighborState, neighborState.isFaceSturdy(level, neighborPos, dir.getOpposite())));
//	    else return super.updateShape(...);
//	IronBarsBlock.attachsTo(state, isSturdy):
//	    return (!isExceptionForConnection(state) and isSturdy)
//	        or state.getBlock() instanceof IronBarsBlock              // panes/bars connect to each other
//	        or state.is(BlockTags.WALLS);
//	Block.isExceptionForConnection(state):
//	    state.is(BARRIER) or CARVED_PUMPKIN or JACK_O_LANTERN or MELON or PUMPKIN or state.is(SHULKER_BOXES).

// crossConnectionFieldByDir maps a horizontal Direction to the exported struct-field name of the
// connection BooleanProperty on the generated CrossCollisionBlock structs (fence/bars/pane carry
// East/North/South/West Boolean fields - CrossCollisionBlock.PROPERTY_BY_DIRECTION).
func crossConnectionFieldByDir(dir Direction) (string, bool) {
	switch dir {
	case North:
		return "North", true
	case South:
		return "South", true
	case West:
		return "West", true
	case East:
		return "East", true
	default:
		return "", false // Up/Down carry no horizontal connection property
	}
}

// IsCrossCollisionBlock reports whether a state resolves to a CrossCollisionBlock (fence, iron
// bars, or glass pane) - the family whose connection booleans this dispatcher recomputes. The
// discriminator is the generated struct carrying the four NORTH/EAST/SOUTH/WEST Boolean fields
// PLUS a WATERLOGGED Boolean (CrossCollisionBlock exact property set). A WallBlock carries
// WallSide-typed N/E/S/W (not Boolean) and an extra UP, so it is correctly excluded - walls are
// the separate WallBlock class with a tri-state connection model, handled elsewhere.
// CITE: CrossCollisionBlock (NORTH/EAST/SOUTH/WEST BooleanProperty + WATERLOGGED).
func IsCrossCollisionBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	b := StateList[s]
	return hasBoolField(b, "North") && hasBoolField(b, "East") &&
		hasBoolField(b, "South") && hasBoolField(b, "West") &&
		hasBoolField(b, "Waterlogged")
}

// isCrossFence reports whether a CrossCollisionBlock is a FenceBlock (ID suffix "_fence" but NOT
// "_fence_gate"). Fences use FenceBlock.connectsTo (same-fence + fence-gate connections);
// bars/panes use IronBarsBlock.attachsTo. CITE: FenceBlock vs IronBarsBlock.
func isCrossFence(b Block) bool {
	id := b.ID()
	return strings.HasSuffix(id, "_fence") && !strings.HasSuffix(id, "_fence_gate")
}

// isWoodenFence reports FenceBlock WOODEN_FENCES tag membership: every "_fence" except
// "minecraft:nether_brick_fence" (the only non-wooden fence). Used by isSameFence WOODEN==WOODEN
// parity check. CITE: BlockTags.WOODEN_FENCES (all fences minus nether_brick_fence).
func isWoodenFence(b Block) bool {
	return isCrossFence(b) && b.ID() != "minecraft:nether_brick_fence"
}

// isIronBarsFamily reports whether a block is an IronBarsBlock (iron_bars) or a subclass
// (glass_pane / *_stained_glass_pane extend IronBarsBlock). These connect to each other via
// attachsTo instanceof IronBarsBlock. CITE: IronBarsBlock / GlassPaneBlock / StainedGlassPaneBlock.
func isIronBarsFamily(b Block) bool {
	id := b.ID()
	return id == "minecraft:iron_bars" || strings.HasSuffix(id, "glass_pane")
}

// isWall reports BlockTags.WALLS membership (ID suffix "_wall"). attachsTo lets a pane/bar connect
// to a wall. CITE: BlockTags.WALLS.
func isWall(b Block) bool {
	return strings.HasSuffix(b.ID(), "_wall")
}

// isFenceGate reports whether a block is a FenceGateBlock (ID suffix "_fence_gate").
func isFenceGate(b Block) bool {
	return strings.HasSuffix(b.ID(), "_fence_gate")
}

// isExceptionForConnection ports Block.isExceptionForConnection(state): the small set of solid
// blocks a fence/pane must NOT connect to even though their face is sturdy (barrier, the carved-
// pumpkin family, melon/pumpkin, and shulker boxes). CITE: Block.isExceptionForConnection.
func isExceptionForConnection(b Block) bool {
	switch b.(type) {
	case Barrier, CarvedPumpkin, JackOLantern, Melon, Pumpkin:
		return true
	}
	// SHULKER_BOXES: the (dyed) shulker_box family (ID suffix "shulker_box").
	return strings.HasSuffix(b.ID(), "shulker_box")
}

// isSameFence ports FenceBlock.isSameFence(neighborState) relative to a self fence: both are in
// BlockTags.FENCES and their WOODEN_FENCES membership matches (a wooden fence connects to wooden
// fences, nether_brick_fence to nether_brick_fence, but the two families do not cross-connect).
// CITE: FenceBlock.isSameFence.
func isSameFence(self, neighbor Block) bool {
	if !isCrossFence(neighbor) {
		return false // neighbor not in BlockTags.FENCES
	}
	return isWoodenFence(neighbor) == isWoodenFence(self)
}

// fenceGateConnectsToDirection ports FenceGateBlock.connectsToDirection(state, dir): a fence
// connects to a fence gate only when the gate FACING axis is PERPENDICULAR to the connection
// direction (the gate opens across the fence line). connectsToDirection returns
// state.getValue(FACING).getClockWise().getAxis() == dir.getAxis(), i.e. FACING is perpendicular
// to dir. CITE: FenceGateBlock.connectsToDirection.
func fenceGateConnectsToDirection(gate Block, dir Direction) bool {
	v := reflect.ValueOf(gate)
	if v.Kind() != reflect.Struct {
		return false
	}
	f := v.FieldByName("Facing")
	if !f.IsValid() || f.Type() != reflect.TypeOf(Direction(0)) {
		return false
	}
	facing := Direction(f.Uint())
	// getClockWise().getAxis() == dir.getAxis(): facing rotated 90 degrees shares dir axis, i.e.
	// facing is perpendicular to dir. Horizontal axes: NORTH/SOUTH are Z, WEST/EAST are X.
	return horizontalAxis(facing) != horizontalAxis(dir)
}

// horizontalAxis returns 0 for the X axis (WEST/EAST) and 1 for the Z axis (NORTH/SOUTH); any
// non-horizontal direction returns -1. Mirrors Direction.getAxis() for the horizontal plane.
func horizontalAxis(dir Direction) int {
	switch dir {
	case West, East:
		return 0
	case North, South:
		return 1
	default:
		return -1
	}
}

// CrossConnectsTo computes whether a CrossCollisionBlock self-state connects toward dir given the
// horizontal neighbour in that direction: its state and whether the neighbour face toward self
// (dir.getOpposite()) is sturdy. Union of FenceBlock.connectsTo and IronBarsBlock.attachsTo,
// dispatched by the self block family.
//
//	FENCE:      (!isExceptionForConnection(n) && sturdy) || isSameFence(self,n) || fenceGate(n,dir)
//	BARS/PANE:  (!isExceptionForConnection(n) && sturdy) || isIronBarsFamily(n) || isWall(n)
//
// dir is the direction FROM self TO the neighbour; the fence-gate check uses dir (the gate must
// straddle the fence line along dir). sturdy is neighbour.isFaceSturdy(dir.getOpposite()) -
// supplied by the caller via the no-context support cache.
// CITE: FenceBlock.connectsTo / IronBarsBlock.attachsTo.
func CrossConnectsTo(self, neighbor Block, sturdy bool, dir Direction) bool {
	base := !isExceptionForConnection(neighbor) && sturdy
	if isCrossFence(self) {
		gate := isFenceGate(neighbor) && fenceGateConnectsToDirection(neighbor, dir)
		return base || isSameFence(self, neighbor) || gate
	}
	// IronBarsBlock family (iron_bars / glass_pane / stained_glass_pane).
	return base || isIronBarsFamily(neighbor) || isWall(neighbor)
}

// WithConnection returns the CrossCollisionBlock state with its dir connection boolean set to
// connected, preserving every other property (state.setValue(PROPERTY_BY_DIRECTION.get(dir),
// connected)). Returns (s, false) if the state is not a CrossCollisionBlock, dir is not
// horizontal, or the resulting state is not a registered block state.
func WithConnection(s StateID, dir Direction, connected bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	field, ok := crossConnectionFieldByDir(dir)
	if !ok {
		return s, false
	}
	nb, ok := withBoolField(StateList[s], field, connected)
	if !ok {
		return s, false
	}
	sid, ok := ToStateID[nb]
	return sid, ok
}

// ConnectionValue reads the CrossCollisionBlock state dir connection boolean
// (state.getValue(PROPERTY_BY_DIRECTION.get(dir))). Returns false for a non-horizontal dir or a
// state without that field.
func ConnectionValue(s StateID, dir Direction) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	field, ok := crossConnectionFieldByDir(dir)
	if !ok {
		return false
	}
	return boolField(StateList[s], field)
}
