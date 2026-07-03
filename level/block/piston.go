package block

// piston.go — the block-package half of the PISTON + OBSERVER port (redstone tier-3): the per-state
// predicates and state accessors/resolvers the server-side piston/observer logic (server/piston.go,
// server/observer.go) reads and writes. Every branch is a literal 1:1 copy of the unobfuscated 26.2
// jar (temp/cache/26.2-inner.jar), decompiled via CFR / `javap -c -p` this session. This file only
// exposes the vanilla state shape (Piston EXTENDED/FACING, PistonHead TYPE/FACING/SHORT, MovingPiston
// FACING/TYPE, Observer POWERED/FACING) as typed Go lookups so the server can run the faithful logic.
//
// CITE (classes):
//   net.minecraft.world.level.block.piston.PistonBaseBlock  (EXTENDED, FACING; isSticky)
//   net.minecraft.world.level.block.piston.PistonHeadBlock  (TYPE, FACING, SHORT)
//   net.minecraft.world.level.block.piston.MovingPistonBlock(FACING, TYPE)
//   net.minecraft.world.level.block.ObserverBlock           (POWERED, FACING; ownSignal/getSignal)

// ---------------------------------------------------------------------------------------------
// Piston base (PistonBaseBlock) — piston + sticky_piston
// ---------------------------------------------------------------------------------------------

// IsPiston reports whether a state id is a piston or sticky_piston (any EXTENDED/FACING combo). CITE:
// PistonBaseBlock.
func IsPiston(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Piston, StickyPiston:
		return true
	default:
		return false
	}
}

// IsStickyPiston reports whether a state id is specifically the sticky_piston (isSticky==true). CITE:
// PistonBaseBlock.isSticky / Blocks.STICKY_PISTON.
func IsStickyPiston(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(StickyPiston)
	return ok
}

// PistonFacing returns the FACING of a piston (the direction the head extends toward), or (Down, false)
// if not a piston. CITE: PistonBaseBlock.FACING (DirectionalBlock.FACING).
func PistonFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	switch b := StateList[s].(type) {
	case Piston:
		return b.Facing, true
	case StickyPiston:
		return b.Facing, true
	default:
		return Down, false
	}
}

// PistonExtended returns the EXTENDED property of a piston, or false if not a piston. CITE:
// PistonBaseBlock.EXTENDED.
func PistonExtended(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case Piston:
		return bool(b.Extended)
	case StickyPiston:
		return bool(b.Extended)
	default:
		return false
	}
}

// PistonWithExtended resolves the same piston with EXTENDED set to `extended`, preserving FACING.
// Returns (s, false) if not a piston. CITE: PistonBaseBlock.triggerEvent (state.setValue(EXTENDED, ...)).
func PistonWithExtended(s StateID, extended bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	e := Boolean(extended)
	switch b := StateList[s].(type) {
	case Piston:
		b.Extended = e
		return lookup(b)
	case StickyPiston:
		b.Extended = e
		return lookup(b)
	default:
		return s, false
	}
}

// ---------------------------------------------------------------------------------------------
// Piston head (PistonHeadBlock)
// ---------------------------------------------------------------------------------------------

// IsPistonHead reports whether a state id is a piston_head (any TYPE/FACING/SHORT combo). CITE:
// PistonHeadBlock.
func IsPistonHead(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(PistonHead)
	return ok
}

// PistonHeadFacing returns the FACING of a piston_head (the direction the arm points away from its
// piston base), or (Down, false) if not a head. CITE: PistonHeadBlock.FACING.
func PistonHeadFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(PistonHead); ok {
		return b.Facing, true
	}
	return Down, false
}

// PistonHeadState resolves the piston_head state with the given FACING, TYPE and SHORT. Used by
// moveBlocks when placing the extended arm. Returns (0, false) if the combination is unresolvable.
// CITE: PistonBaseBlock.moveBlocks (Blocks.PISTON_HEAD.defaultBlockState().setValue(FACING, dir)
// .setValue(TYPE, type)).
func PistonHeadState(facing Direction, typ PistonType, short bool) (StateID, bool) {
	return lookup(PistonHead{Facing: facing, Type: typ, Short: Boolean(short)})
}

// ---------------------------------------------------------------------------------------------
// Moving piston (MovingPistonBlock) — the animation block-entity carrier
// ---------------------------------------------------------------------------------------------

// IsMovingPiston reports whether a state id is the moving_piston (the transient block placed over a
// block that is being pushed/pulled while its PistonMovingBlockEntity animates). CITE: MovingPistonBlock.
func IsMovingPiston(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(MovingPiston)
	return ok
}

// MovingPistonFacing returns the FACING of a moving_piston, or (Down, false) if not one. CITE:
// MovingPistonBlock.FACING.
func MovingPistonFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(MovingPiston); ok {
		return b.Facing, true
	}
	return Down, false
}

// MovingPistonState resolves the moving_piston state with the given FACING and TYPE. Used by
// moveBlocks/triggerEvent when placing the transient animation block. Returns (0, false) if
// unresolvable. CITE: PistonBaseBlock.moveBlocks / triggerEvent (Blocks.MOVING_PISTON
// .defaultBlockState().setValue(FACING, dir).setValue(TYPE, type)).
func MovingPistonState(facing Direction, typ PistonType) (StateID, bool) {
	return lookup(MovingPiston{Facing: facing, Type: typ})
}

// ---------------------------------------------------------------------------------------------
// Observer (ObserverBlock)
// ---------------------------------------------------------------------------------------------

// IsObserver reports whether a state id is an observer (any POWERED/FACING combo). CITE: ObserverBlock.
func IsObserver(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Observer)
	return ok
}

// ObserverFacing returns the FACING of an observer — the face that WATCHES the neighbor it monitors
// AND the face out which it emits its output signal (getSignal returns ownSignal only when
// FACING == direction). Returns (Down, false) if not an observer. CITE: ObserverBlock.FACING /
// getSignal (`FACING == direction ? ownSignal : 0`) / updateShape (`FACING == directionToNeighbour`).
func ObserverFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down, false
	}
	if b, ok := StateList[s].(Observer); ok {
		return b.Facing, true
	}
	return Down, false
}

// ObserverPowered returns the POWERED property of an observer, or false if not an observer. CITE:
// ObserverBlock.POWERED.
func ObserverPowered(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Observer); ok {
		return bool(b.Powered)
	}
	return false
}

// ObserverShouldConnectTo is the RedStoneWireBlock.shouldConnectTo OBSERVER special case: a wire
// connects to an observer at state `s` toward `direction` iff direction == the observer's FACING (a wire
// connects only to the observer's output face). Returns (false, false) if s is not an observer (caller
// falls through to the generic isSignalSource tail). CITE: RedStoneWireBlock.shouldConnectTo
// (`blockState.is(OBSERVER)` branch: `direction == blockState.getValue(ObserverBlock.FACING)`).
func ObserverShouldConnectTo(s StateID, direction Direction) (connect bool, isObserver bool) {
	facing, ok := ObserverFacing(s)
	if !ok {
		return false, false
	}
	return direction == facing, true
}

// ObserverWithPowered resolves the same observer with POWERED set to `powered`, preserving FACING.
// Returns (s, false) if not an observer. CITE: ObserverBlock.tick (state.setValue(POWERED, ...)).
func ObserverWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Observer); ok {
		b.Powered = Boolean(powered)
		return lookup(b)
	}
	return s, false
}
