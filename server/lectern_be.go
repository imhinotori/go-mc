package server

// lectern_be.go -- the LECTERN BLOCK-ENTITY (LECTERN-01): a 1:1 port of
// net.minecraft.world.level.block.entity.LecternBlockEntity (setBook / setPage / getPageCount /
// getRedstoneSignal / onBookItemRemove) + the LecternBlock use/place/take hooks over the 26.2 jar
// (temp/cache/26.2-inner.jar, CFR/javap this session). A lectern holds ONE book stack + a current page +
// the book page count; it does NOT tick (no serverTick). Placing a #lectern_books item sets HAS_BOOK=true
// and resets the page to 0; the redstone comparator reads a page-fraction signal; taking the book resets
// HAS_BOOK=false + drops the book.
//
// 1:1 jar (net.minecraft.world.level.block.entity.LecternBlockEntity), VERIFIED CFR:
//   DATA_PAGE = 0; NUM_DATA = 1; SLOT_BOOK = 0; NUM_SLOTS = 1.
//   hasBook(): book.has(WRITABLE_BOOK_CONTENT) || book.has(WRITTEN_BOOK_CONTENT).
//   setBook(stack, player): book = resolveBook(stack, player); page = 0; pageCount = getPageCount(book);
//       setChanged().
//   setPage(n): page = Mth.clamp(n, 0, pageCount - 1); if changed { setChanged(); LecternBlock.signalPageChange }.
//   getRedstoneSignal(): float f = pageCount > 1 ? getPage() / (float)(pageCount - 1) : 1.0f;
//       return Mth.floor(f * 14.0f) + (hasBook() ? 1 : 0);
//   onBookItemRemove(): page = 0; pageCount = 0; LecternBlock.resetBookState(null, level, pos, state, false).
//
// The book + page + pageCount state + the comparator formula are preserved exactly. getPageCount (the
// WRITABLE/WRITTEN book content page-list length) is stubbed to 1 in v1 (cited): the book-content component
// parser is not yet ported, so a placed book reports 1 page -> getRedstoneSignal = floor(1.0*14)+1 = 15
// (the single-page comparator level), the exact vanilla value for a 1-page book. The LecternMenu open (a
// page-turn/take-book GUI) is DEFERRED cited (no lectern menu wired); take-book is offered via a sneak/empty
// -hand interaction seam (takeLecternBook) that is server-authoritative.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// lecternBE is the tick-owned state of one lectern block-entity -- the Go analogue of LecternBlockEntity.
// book is the held book stack (empty Count 0 == ItemStack.EMPTY); page is the current page (0-based);
// pageCount is the book page-list length (1 in v1 -- getPageCount stub, cited). Registered on placement /
// on tryPlaceBook; read by the comparator analog-output seam. No per-tick drive (a lectern does not tick).
type lecternBE struct {
	book      component.SlotData
	page      int
	pageCount int
}

// lecternHasBook ports LecternBlockEntity.hasBook(): the held stack is a writable/written book. v1 gates on
// a non-empty book that is in the #lectern_books tag (writable_book / written_book) -- the same set the
// WRITABLE/WRITTEN book-content component check resolves to. CITE LecternBlockEntity.hasBook.
func (l *lecternBE) hasBook() bool {
	if stackEmpty(l.book) {
		return false
	}
	return itemInTag(int32(l.book.ItemID), "lectern_books")
}

// lecternSetBook ports LecternBlockEntity.setBook(stack, player): store the (resolved) book, reset the page
// to 0, and recompute pageCount. resolveBook (the WrittenBookContent command resolution for a written book)
// is a no-op here -- v1 stores the stack verbatim (the resolution only expands macro components, which the
// v1 book-content parser does not model; DEFERRED cited). CITE LecternBlockEntity.setBook.
func (l *lecternBE) lecternSetBook(book component.SlotData) {
	l.book = book
	l.page = 0
	l.pageCount = lecternGetPageCount(book)
}

// lecternGetPageCount ports LecternBlockEntity.getPageCount(stack): the length of the book WRITABLE_BOOK
// _CONTENT pages (or WRITTEN_BOOK_CONTENT pages) list. v1 does not parse the book-content component, so this
// returns 1 for any non-empty book (a single-page book) -- so getRedstoneSignal = floor(1.0*14)+1 = 15, the
// exact vanilla comparator level for a 1-page book. A precise multi-page count is a cited follow-up once the
// book-content component parser lands. CITE LecternBlockEntity.getPageCount (book-content pages length).
func lecternGetPageCount(book component.SlotData) int {
	if stackEmpty(book) {
		return 0
	}
	return 1 // DEFERRED: book-content component page-list length (v1 stub -> 1). Cited.
}

// lecternSetPage ports LecternBlockEntity.setPage(n): clamp n to [0, pageCount-1] and, if it changed, mark
// changed + fire LecternBlock.signalPageChange (the comparator pulse). The signal-page-change block-tick /
// levelEvent are cite-deferred (the page-turn is driven by the deferred menu; the clamp + the stored page
// are the server-authoritative half). CITE LecternBlockEntity.setPage.
func (l *lecternBE) lecternSetPage(n int) bool {
	clamped := mthClampInt(n, 0, l.pageCount-1)
	if clamped == l.page {
		return false
	}
	l.page = clamped
	return true
}

// lecternGetRedstoneSignal ports LecternBlockEntity.getRedstoneSignal():
//
//	float f = pageCount > 1 ? (float) getPage() / (float) (pageCount - 1) : 1.0f;
//	return Mth.floor(f * 14.0f) + (hasBook() ? 1 : 0);
//
// CITE LecternBlockEntity.getRedstoneSignal.
func (l *lecternBE) lecternGetRedstoneSignal() int {
	var f float32
	if l.pageCount > 1 {
		f = float32(l.page) / float32(l.pageCount-1)
	} else {
		f = 1.0
	}
	sig := mthFloor(float64(f) * 14.0)
	if l.hasBook() {
		sig++
	}
	return sig
}

// useLectern ports LecternBlock.useItemOn -> tryPlaceBook AND useWithoutItem -> openScreen: a right-click
// on a lectern WITHOUT a book, holding a #lectern_books item, places the book (HAS_BOOK=true); a right-click
// on a lectern WITH a book opens the reading screen (LecternMenu -- DEFERRED cited). Either way the
// interaction is consumed so no block is placed. A right-click WITHOUT a book holding a non-book item falls
// through (returns false -> placement continues). CITE LecternBlock.useItemOn / useWithoutItem / tryPlaceBook.
//
// 1:1 net.minecraft.world.level.block.LecternBlock.useItemOn / useWithoutItem
func (t *TickLoop) useLectern(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	if t.world() == nil {
		return false
	}
	// useWithoutItem: if HAS_BOOK, openScreen(LecternMenu). DEFERRED (no lectern menu wired) -- but the
	// interaction is still CONSUMED (SUCCESS) so no block is placed on a book-bearing lectern.
	if block.LecternHasBook(state) {
		// LecternMenu open DEFERRED (cited). Consume the click (SUCCESS): no block-place fall-through.
		return true
	}
	// useItemOn (no book): if the held item is #lectern_books, tryPlaceBook.
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if stackEmpty(held) || !itemInTag(int32(held.ItemID), "lectern_books") {
		// Not a lectern book -> useItemOn returns TRY_WITH_EMPTY_HAND/PASS -> placement continues.
		return false
	}
	// tryPlaceBook(player, level, pos, state, stack): place the book, consume 1 (creative keeps it).
	if t.lecternTryPlaceBook(p, pos, state, held) {
		if p.gameMode != gameModeCreative {
			// stack.consumeAndReturn(1, player): shrink the held stack by 1 + sync the slot.
			t.shrinkHeldItem(p, inv)
		}
	}
	return true // consumed (placed) -- never a block-place fall-through for a lectern book.
}

// lecternTryPlaceBook ports LecternBlock.tryPlaceBook -> placeBook: set the lectern BE book (one item, a
// single-count copy of the held stack), then resetBookState(HAS_BOOK=true) so the block state + comparator
// reflect the placed book. Returns true (a book was placed). CITE LecternBlock.tryPlaceBook / placeBook.
func (t *TickLoop) lecternTryPlaceBook(p *tickPlayer, pos pk.Position, state block.StateID, held component.SlotData) bool {
	if block.LecternHasBook(state) {
		return false // tryPlaceBook: already has a book -> false
	}
	l := t.resolveLectern(pos)
	if l == nil {
		return false
	}
	// setBook(stack.consumeAndReturn(1, player)): store one item.
	one := held
	one.Count = 1
	l.lecternSetBook(one)
	// resetBookState(player, level, pos, state, true): POWERED=false, HAS_BOOK=true, setBlock flag 3.
	t.lecternResetBookState(pos, state, true)
	// playSound(BOOK_PUT): cite-deferred (client cosmetic).
	return true
}

// takeLecternBook ports LecternBlockEntity.onBookItemRemove (the take-book path the LecternMenu removeBook
// button drives): drop the held book back to the player/world, reset page + pageCount, and
// resetBookState(HAS_BOOK=false). v1 offers this via a SNEAK-right-click on a book-bearing lectern (the
// menu removeBook is DEFERRED); the book is dropped as an Item entity at the lectern. Returns true when a
// book was taken. CITE LecternBlockEntity.onBookItemRemove + LecternBlock.resetBookState.
func (t *TickLoop) takeLecternBook(pos pk.Position, state block.StateID) bool {
	if t.world() == nil || !block.LecternHasBook(state) {
		return false
	}
	l := t.resolveLectern(pos)
	if l == nil || stackEmpty(l.book) {
		return false
	}
	// Drop the book back into the world at the lectern (Clearable.clearContent drops on removal).
	if t.cur() != nil {
		x := float64(pos.X) + 0.5
		y := float64(pos.Y) + 1.0
		z := float64(pos.Z) + 0.5
		ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, l.book)
		t.cur().entities.add(ie)
	}
	// onBookItemRemove: page = 0; pageCount = 0; resetBookState(HAS_BOOK=false).
	l.book = component.SlotData{Count: 0}
	l.page = 0
	l.pageCount = 0
	t.lecternResetBookState(pos, state, false)
	return true
}

// lecternResetBookState ports LecternBlock.resetBookState(entity, level, pos, state, hasBook): write the
// lectern state with POWERED=false + HAS_BOOK=hasBook (setBlock flag 3 -> broadcast + neighbor update), then
// updateBelow (the comparator below re-reads). CITE LecternBlock.resetBookState.
func (t *TickLoop) lecternResetBookState(pos pk.Position, state block.StateID, hasBook bool) {
	newState, ok := block.LecternResetBookState(state, hasBook)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return
	}
	t.broadcastBlockUpdate(pos, newState)
	// gameEvent(BLOCK_CHANGE) deferred. updateBelow: notify the comparator below via the redstone edit hook.
	t.onRedstoneEdit(pos)
}

// lecternAnalogOutputSignal ports LecternBlock.getAnalogOutputSignal (hasAnalogOutputSignal == true):
// HAS_BOOK ? LecternBlockEntity.getRedstoneSignal() : 0. Returns (signal, true) for a lectern, (0, false)
// otherwise (so the comparator falls to its container/super path). CITE LecternBlock.getAnalogOutputSignal.
func (t *TickLoop) lecternAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsLectern(state) {
		return 0, false
	}
	if !block.LecternHasBook(state) {
		return 0, true // HAS_BOOK false -> analog 0 (still hasAnalogOutputSignal true).
	}
	l := t.resolveLectern(pos)
	if l == nil {
		return 0, true
	}
	return l.lecternGetRedstoneSignal(), true
}

// resolveLectern returns the tick-owned lecternBE for pos, creating an EMPTY one (no book) on first access
// -- the analogue of a freshly-placed lectern default LecternBlockEntity. Returns nil when pos is not a
// lectern block (or the world is unloaded). Tick-owned (t.lecterns, the t.bells twin).
func (t *TickLoop) resolveLectern(pos pk.Position) *lecternBE {
	if t.lecterns == nil {
		t.lecterns = make(map[pk.Position]*lecternBE)
	}
	if l, ok := t.lecterns[pos]; ok {
		return l
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsLectern(state) {
		return nil
	}
	l := &lecternBE{}
	t.lecterns[pos] = l
	return l
}
