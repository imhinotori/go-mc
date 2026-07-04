package block

// dispenser.go — the block-package half of the DISPENSER + DROPPER port (redstone tier-4): the
// per-state predicates and state accessors/resolvers the server-side dispenser/dropper logic
// (server/dispenser_be.go, server/dispenser.go) reads and writes. Every branch is a literal 1:1
// copy of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar), decompiled via CFR / `javap -c -p`
// this session. This file only exposes the vanilla state shape (Dispenser/Dropper FACING/TRIGGERED)
// as typed Go lookups so the server can run the faithful logic.
//
// CITE (classes):
//   net.minecraft.world.level.block.DispenserBlock (FACING = DirectionalBlock.FACING; TRIGGERED)
//   net.minecraft.world.level.block.DropperBlock   (extends DispenserBlock; same FACING/TRIGGERED)

// IsDispenser reports whether a state id is a dispenser (any FACING/TRIGGERED combo) — the plain
// minecraft:dispenser, NOT the dropper subclass (a distinct Block). CITE: DispenserBlock.
func IsDispenser(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Dispenser)
	return ok
}

// IsDropper reports whether a state id is a dropper (any FACING/TRIGGERED combo). DropperBlock
// extends DispenserBlock but is a separate Block singleton, so it is a separate Go type. CITE:
// DropperBlock.
func IsDropper(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Dropper)
	return ok
}

// IsDispenserFamily reports whether s is a dispenser OR a dropper (the shared DispenserBlock
// neighborChanged/tick/dispenseFrom machinery applies to both). CITE: DropperBlock extends
// DispenserBlock.
func IsDispenserFamily(s StateID) bool {
	return IsDispenser(s) || IsDropper(s)
}

// DispenserFacing returns the FACING of a dispenser/dropper (the direction it ejects toward), or
// (Down, false) if not a dispenser-family block. CITE: DispenserBlock.FACING (DirectionalBlock.FACING).
func DispenserFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	switch b := StateList[s].(type) {
	case Dispenser:
		return b.Facing, true
	case Dropper:
		return b.Facing, true
	default:
		return Down, false
	}
}

// DispenserTriggered returns the TRIGGERED property of a dispenser/dropper (the redstone latch), or
// false if not a dispenser-family block. CITE: DispenserBlock.TRIGGERED (BlockStateProperties.TRIGGERED).
func DispenserTriggered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case Dispenser:
		return bool(b.Triggered)
	case Dropper:
		return bool(b.Triggered)
	default:
		return false
	}
}

// DispenserWithTriggered resolves the same dispenser/dropper with TRIGGERED set to `triggered`,
// preserving FACING. Returns (s, false) if not a dispenser-family block. CITE:
// DispenserBlock.neighborChanged (state.setValue(TRIGGERED, ...)).
func DispenserWithTriggered(s StateID, triggered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	tr := Boolean(triggered)
	switch b := StateList[s].(type) {
	case Dispenser:
		b.Triggered = tr
		return lookup(b)
	case Dropper:
		b.Triggered = tr
		return lookup(b)
	default:
		return s, false
	}
}
