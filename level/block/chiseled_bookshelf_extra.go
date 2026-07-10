package block

// chiseled_bookshelf_extra.go -- the block-package half of the CHISELED BOOKSHELF port: the per-state
// predicate + the six SLOT_N_OCCUPIED accessors the server-side block-entity logic
// (server/chiseled_bookshelf_be.go) reads and writes. Every branch is a literal 1:1 copy of the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR / javap this session). This file exposes the
// vanilla state shape (ChiseledBookShelfBlock FACING + SLOT_0_OCCUPIED..SLOT_5_OCCUPIED) as typed Go
// lookups so the server can run the faithful BE occupancy-sync logic.
//
// CITE (class): net.minecraft.world.level.block.ChiseledBookShelfBlock
//   SLOT_OCCUPIED_PROPERTIES = List.of(SLOT_0_OCCUPIED, SLOT_1_OCCUPIED, SLOT_2_OCCUPIED, SLOT_3_OCCUPIED,
//     SLOT_4_OCCUPIED, SLOT_5_OCCUPIED); FACING = HorizontalDirectionalBlock.FACING.
//   ChiseledBookShelfBlockEntity.updateState: for i in 0..SLOT_OCCUPIED_PROPERTIES.size():
//     state = state.setValue(SLOT_OCCUPIED_PROPERTIES.get(i), !getItem(i).isEmpty()); level.setBlock(...).

// IsChiseledBookshelf reports whether a state id is a chiseled_bookshelf (any FACING/occupied combo).
// CITE ChiseledBookShelfBlock.
func IsChiseledBookshelf(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(ChiseledBookshelf)
	return ok
}

// ChiseledBookshelfFacing returns the FACING (horizontal Direction the shelf front points) of a chiseled
// bookshelf, or Down (a non-horizontal sentinel) for a non-bookshelf. CITE ChiseledBookShelfBlock.FACING
// (HorizontalDirectionalBlock.FACING).
func ChiseledBookshelfFacing(s StateID) Direction {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Down
	}
	if b, ok := StateList[s].(ChiseledBookshelf); ok {
		return b.Facing
	}
	return Down
}

// ChiseledBookshelfSlotOccupied returns the SLOT_<slot>_OCCUPIED property of a chiseled bookshelf (true
// when that slot holds a book), or false for a non-bookshelf or an out-of-range slot. slot is 0..5.
// CITE ChiseledBookShelfBlock.SLOT_OCCUPIED_PROPERTIES.get(slot).
func ChiseledBookshelfSlotOccupied(s StateID, slot int) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	b, ok := StateList[s].(ChiseledBookshelf)
	if !ok {
		return false
	}
	switch slot {
	case 0:
		return bool(b.Slot0Occupied)
	case 1:
		return bool(b.Slot1Occupied)
	case 2:
		return bool(b.Slot2Occupied)
	case 3:
		return bool(b.Slot3Occupied)
	case 4:
		return bool(b.Slot4Occupied)
	case 5:
		return bool(b.Slot5Occupied)
	default:
		return false
	}
}

// ChiseledBookshelfWithSlot resolves the bookshelf state with SLOT_<slot>_OCCUPIED=occupied, preserving
// FACING and the other five slot flags. Returns (s, false) for a non-bookshelf or an out-of-range slot.
// CITE ChiseledBookShelfBlockEntity.updateState (state.setValue(SLOT_OCCUPIED_PROPERTIES.get(i),
// !getItem(i).isEmpty())).
func ChiseledBookshelfWithSlot(s StateID, slot int, occupied bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	b, ok := StateList[s].(ChiseledBookshelf)
	if !ok {
		return s, false
	}
	switch slot {
	case 0:
		b.Slot0Occupied = Boolean(occupied)
	case 1:
		b.Slot1Occupied = Boolean(occupied)
	case 2:
		b.Slot2Occupied = Boolean(occupied)
	case 3:
		b.Slot3Occupied = Boolean(occupied)
	case 4:
		b.Slot4Occupied = Boolean(occupied)
	case 5:
		b.Slot5Occupied = Boolean(occupied)
	default:
		return s, false
	}
	return lookup(b)
}
