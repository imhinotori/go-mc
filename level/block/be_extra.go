package block

// be_extra.go -- the block-package half of the LECTERN + JUKEBOX block-entity ports: the per-state
// predicates and state accessors the server-side BE logic (server/lectern_be.go, server/jukebox_be.go)
// reads and writes. Every branch is a literal 1:1 copy of the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR / javap this session). This file only exposes the vanilla state shape
// (LecternBlock HAS_BOOK/POWERED/FACING, JukeboxBlock HAS_RECORD) as typed Go lookups so the server can run
// the faithful BE logic.
//
// CITE (classes):
//   net.minecraft.world.level.block.LecternBlock (HAS_BOOK, POWERED, FACING) -- resetBookState sets
//     POWERED=false + HAS_BOOK=lit; getAnalogOutputSignal reads LecternBlockEntity.getRedstoneSignal.
//   net.minecraft.world.level.block.JukeboxBlock (HAS_RECORD) -- notifyItemChangedInJukebox sets
//     HAS_RECORD; getAnalogOutputSignal reads JukeboxBlockEntity.getComparatorOutput.

// IsLectern reports whether a state id is a lectern (any HAS_BOOK/POWERED/FACING combo). CITE LecternBlock.
func IsLectern(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Lectern)
	return ok
}

// LecternHasBook returns the HAS_BOOK property of a lectern (true when a book is on the lectern), or false
// for a non-lectern. CITE LecternBlock.HAS_BOOK (LecternBlockEntity.hasBook mirror).
func LecternHasBook(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Lectern); ok {
		return bool(b.HasBook)
	}
	return false
}

// LecternResetBookState resolves the lectern state with POWERED=false and HAS_BOOK=hasBook, preserving
// FACING. Returns (s, false) for a non-lectern. CITE LecternBlock.resetBookState (state.setValue(POWERED,
// false).setValue(HAS_BOOK, hasBook)).
func LecternResetBookState(s StateID, hasBook bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Lectern); ok {
		b.Powered = Boolean(false)
		b.HasBook = Boolean(hasBook)
		return lookup(b)
	}
	return s, false
}

// LecternWithPowered resolves the lectern state with POWERED=powered, preserving FACING/HAS_BOOK. Returns
// (s, false) for a non-lectern. CITE LecternBlock.changePowered (state.setValue(POWERED, powered)).
func LecternWithPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Lectern); ok {
		b.Powered = Boolean(powered)
		return lookup(b)
	}
	return s, false
}

// IsDecoratedPot reports whether a state id is a decorated_pot (any cracked/facing/waterlogged combo).
// CITE DecoratedPotBlock.
func IsDecoratedPot(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(DecoratedPot)
	return ok
}

// IsJukebox reports whether a state id is a jukebox (either HAS_RECORD combo). CITE JukeboxBlock.
func IsJukebox(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Jukebox)
	return ok
}

// JukeboxHasRecord returns the HAS_RECORD property of a jukebox (true when a disc is loaded), or false for
// a non-jukebox. CITE JukeboxBlock.HAS_RECORD.
func JukeboxHasRecord(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(Jukebox); ok {
		return bool(b.HasRecord)
	}
	return false
}

// JukeboxWithRecord resolves the jukebox state with HAS_RECORD=hasRecord. Returns (s, false) for a
// non-jukebox. CITE JukeboxBlockEntity.notifyItemChangedInJukebox (state.setValue(HAS_RECORD, hasRecord)).
func JukeboxWithRecord(s StateID, hasRecord bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(Jukebox); ok {
		b.HasRecord = Boolean(hasRecord)
		return lookup(b)
	}
	return s, false
}
