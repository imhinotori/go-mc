package block

// hopper.go — the block-package half of the HOPPER port (redstone tier / container-transfer): the
// per-state predicates and state accessors the server-side hopper logic (server/hopper_be.go,
// server/hopper_menu.go) reads and writes. Every branch is a literal 1:1 copy of the unobfuscated
// 26.2 jar (temp/cache/26.2-inner.jar), decompiled via CFR / `javap -c -p` this session. This file
// only exposes the vanilla state shape (HopperBlock FACING/ENABLED) as typed Go lookups so the
// server can run the faithful transfer logic.
//
// CITE (classes):
//   net.minecraft.world.level.block.HopperBlock
//     FACING  = DirectionalBlock.FACING restricted to the 5 dirs (no UP — getStateForPlacement maps a
//               Y-axis click to DOWN); the Hopper struct here carries a full Direction but a placed
//               hopper never faces UP.
//     ENABLED = BlockStateProperties.ENABLED (default true; checkPoweredState sets it to
//               !hasNeighborSignal(pos)).

// IsHopper reports whether a state id is a hopper (any FACING/ENABLED combo). CITE: HopperBlock.
func IsHopper(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Hopper)
	return ok
}

// HopperFacing returns the FACING of a hopper (the direction it EJECTS toward — the "spout"), or
// (Down, false) if not a hopper. CITE: HopperBlock.FACING (used by HopperBlockEntity.facing).
func HopperFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(Hopper); ok {
		return b.Facing, true
	}
	return Down, false
}

// HopperEnabled returns the ENABLED property of a hopper (the redstone gate: a hopper only transfers
// when ENABLED), or false if not a hopper. CITE: HopperBlock.ENABLED
// (HopperBlockEntity.tryMoveItems gates on state.getValue(ENABLED)).
func HopperEnabled(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Hopper); ok {
		return bool(b.Enabled)
	}
	return false
}

// HopperWithEnabled resolves the same hopper with ENABLED set to `enabled`, preserving FACING.
// Returns (s, false) if not a hopper. CITE: HopperBlock.checkPoweredState
// (level.setBlock(pos, state.setValue(ENABLED, shouldBeOn), 2)).
func HopperWithEnabled(s StateID, enabled bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Hopper); ok {
		b.Enabled = Boolean(enabled)
		return lookup(b)
	}
	return s, false
}
