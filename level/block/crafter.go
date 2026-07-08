package block

// crafter.go -- the block-package half of the CRAFTER port (redstone-driven auto-crafter): the
// per-state predicates and accessors the server-side crafter logic (server/crafter.go,
// server/crafter_be.go) reads and writes. Every branch is a literal 1:1 copy of the unobfuscated
// 26.2 jar (temp/cache/26.2-inner.jar), decompiled via javap -c -p this session. This file exposes
// the vanilla state shape (Crafter ORIENTATION/CRAFTING/TRIGGERED) as typed Go lookups so the server
// can run the faithful logic.
//
// CITE (class): net.minecraft.world.level.block.CrafterBlock
//   CRAFTING = BlockStateProperties.CRAFTING (BooleanProperty)
//   TRIGGERED = BlockStateProperties.TRIGGERED (BooleanProperty)
//   ORIENTATION = FrontAndTop enum property (front() drives the eject direction)

// IsCrafter reports whether a state id is a crafter (any ORIENTATION/CRAFTING/TRIGGERED combo).
// CITE: CrafterBlock.
func IsCrafter(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Crafter)
	return ok
}

// CrafterTriggered returns the TRIGGERED property of a crafter (the redstone latch), or false if not
// a crafter. CITE: CrafterBlock.TRIGGERED.
func CrafterTriggered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Crafter); ok {
		return bool(b.Triggered)
	}
	return false
}

// CrafterCrafting returns the CRAFTING property of a crafter (the 6-tick craft-animation latch), or
// false if not a crafter. CITE: CrafterBlock.CRAFTING.
func CrafterCrafting(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Crafter); ok {
		return bool(b.Crafting)
	}
	return false
}

// CrafterFacing returns the FRONT direction of a crafter's ORIENTATION (the direction the crafted
// result ejects toward), or (Down, false) if not a crafter. CITE: CrafterBlock.dispenseItem
// (state.getValue(ORIENTATION).front()).
func CrafterFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(Crafter); ok {
		front, _ := b.Orientation.Directions()
		return front, true
	}
	return Down, false
}

// CrafterWithTriggered resolves the same crafter with TRIGGERED set to triggered, preserving
// ORIENTATION + CRAFTING. Returns (s, false) if not a crafter. CITE: CrafterBlock.neighborChanged
// (state.setValue(TRIGGERED, ...)).
func CrafterWithTriggered(s StateID, triggered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Crafter); ok {
		b.Triggered = Boolean(triggered)
		return lookup(b)
	}
	return s, false
}

// CrafterWithCrafting resolves the same crafter with CRAFTING set to crafting, preserving
// ORIENTATION + TRIGGERED. Returns (s, false) if not a crafter. CITE: CrafterBlock.dispenseFrom
// (state.setValue(CRAFTING, true)) and CrafterBlockEntity.serverTick (state.setValue(CRAFTING, false)).
func CrafterWithCrafting(s StateID, crafting bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Crafter); ok {
		b.Crafting = Boolean(crafting)
		return lookup(b)
	}
	return s, false
}
