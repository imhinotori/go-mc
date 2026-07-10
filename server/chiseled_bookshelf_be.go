package server

// chiseled_bookshelf_be.go -- the CHISELED BOOKSHELF block-entity + block-interaction port
// (BOOKSHELF-01), a 1:1 copy of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR / javap this
// session): net.minecraft.world.level.block.entity.ChiseledBookShelfBlockEntity (the 6-slot container +
// lastInteractedSlot tracking + setBlockState occupancy sync + NBT Items/last_interacted_slot) and
// net.minecraft.world.level.block.ChiseledBookShelfBlock (useItemOn/useWithoutItem hit-vector -> slot
// geometry, addBook/removeBook, getAnalogOutputSignal == BE.getLastInteractedSlot()+1, the
// #bookshelf_books tag filter, the insert/pickup sounds). The jukebox_be.go twin (a self-contained
// container BE with a comparator seam).
//
// 1:1 ANCHORS (VERIFIED javap this session):
//   ChiseledBookShelfBlockEntity ctor: items = NonNullList.withSize(6, EMPTY); lastInteractedSlot = -1.
//   getMaxStackSize() = 1.
//   acceptsItemType(stack) = stack.is(ItemTags.BOOKSHELF_BOOKS).
//   removeItem(i, n): stack = items.get(i); items.set(i, EMPTY); if (!stack.isEmpty()) updateState(i);
//       return stack.  (n is ignored -- maxStackSize 1, so a slot is all-or-nothing.)
//   setItem(i, stack): if (acceptsItemType(stack)) { items.set(i, stack); updateState(i); }
//       else if (stack.isEmpty()) removeItem(i, getMaxStackSize()).
//   updateState(i): if (i<0||i>=6) { LOGGER.error("Expected slot 0-5, got {}", i); return; }
//       lastInteractedSlot = i; state = getBlockState();
//       for (int j=0;j<SLOT_OCCUPIED_PROPERTIES.size();j++)
//           state = state.setValue(SLOT_OCCUPIED_PROPERTIES.get(j), !getItem(j).isEmpty());
//       level.setBlock(worldPosition, state, 3); level.gameEvent(BLOCK_CHANGE, worldPosition, of(state)).
//   getLastInteractedSlot() = lastInteractedSlot.
//   loadAdditional(in): items.clear(); ContainerHelper.loadAllItems(in, items);   // "Items"
//       lastInteractedSlot = in.getIntOr("last_interacted_slot", -1).
//   saveAdditional(out): ContainerHelper.saveAllItems(out, items, true);          // "Items"
//       out.putInt("last_interacted_slot", lastInteractedSlot).
//   ChiseledBookShelfBlock.getRows()=2, getColumns()=3 (SelectableSlotContainer); MAX_BOOKS = 6.
//   ChiseledBookShelfBlock.hasAnalogOutputSignal = true;
//   getAnalogOutputSignal(state, level, pos, dir): if (level.isClientSide) return 0;
//       be = getBlockEntity(pos); if (be instanceof ChiseledBookShelfBlockEntity) return
//       be.getLastInteractedSlot() + 1; return 0.
//   addBook(level,pos,player,be,stack,slot): (server only) awardStat; sound =
//       stack.is(ENCHANTED_BOOK) ? CHISELED_BOOKSHELF_INSERT_ENCHANTED : CHISELED_BOOKSHELF_INSERT;
//       be.setItem(slot, stack.consumeAndReturn(1, player));
//       level.playSound(null, pos, sound, BLOCKS, 1.0F, 1.0F).
//   removeBook(level,pos,player,be,slot): (server only) stack = be.removeItem(slot, 1); sound =
//       stack.is(ENCHANTED_BOOK) ? CHISELED_BOOKSHELF_PICKUP_ENCHANTED : CHISELED_BOOKSHELF_PICKUP;
//       level.playSound(null, pos, sound, BLOCKS, 1.0F, 1.0F);
//       if (!player.getInventory().add(stack)) player.drop(stack, false);
//       level.gameEvent(player, BLOCK_CHANGE, pos).
//   useItemOn(stack,state,...,hit): be = getBlockEntity; if not bookshelf -> PASS.
//       if (!stack.is(BOOKSHELF_BOOKS)) -> TRY_WITH_EMPTY_HAND; slot = getHitSlot(hit, FACING);
//       if (slot.isEmpty()) -> PASS; if (SLOT_OCCUPIED_PROPERTIES.get(slot)) -> TRY_WITH_EMPTY_HAND;
//       addBook(...); -> SUCCESS.
//   useWithoutItem(state,...,hit): be = getBlockEntity; if not bookshelf -> PASS;
//       slot = getHitSlot(hit, FACING); if (slot.isEmpty()) -> PASS;
//       if (!SLOT_OCCUPIED_PROPERTIES.get(slot)) -> CONSUME; removeBook(...); -> SUCCESS.
//   SelectableSlotContainer.getHitSlot / getRelativeHitCoordinatesForBlockFace / getSection: the
//       hit-vec -> (col,row) -> slot geometry, ported bit-exact in chiseledBookshelfHitSlot below.
//
// STUBS (cited): gameEvent(BLOCK_CHANGE) is a no-op (no game-event subsystem in v1, the jukebox/lectern
// twin). The awardStat(ITEM_USED) increment on addBook is a cite-deferred no-op (no per-player stats
// subsystem). The insert/pickup SOUNDS are NOT stubbed -- they emit via t.playSound (the real
// ServerLevel.playSound(null, pos, sound, BLOCKS, 1.0F, 1.0F) broadcast, the door/portal seam).

import (
	"math"
	"math/rand/v2"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// chiseledBookshelfMaxBooks is ChiseledBookShelfBlock.MAX_BOOKS_IN_STORAGE == 6 (the container size).
// CITE ChiseledBookShelfBlockEntity ctor (NonNullList.withSize(6, EMPTY)).
const chiseledBookshelfMaxBooks = 6

// chiseledBookshelfRows / chiseledBookshelfColumns are ChiseledBookShelfBlock.getRows()==2 /
// getColumns()==3 (the SelectableSlotContainer grid: 2 rows x 3 columns = 6 slots on the facing face).
// CITE ChiseledBookShelfBlock.getRows / getColumns.
const (
	chiseledBookshelfRows    = 2
	chiseledBookshelfColumns = 3
)

// chiseledBookshelfDefaultLastSlot is ChiseledBookShelfBlockEntity.DEFAULT_LAST_INTERACTED_SLOT == -1
// (so an untouched shelf reads getLastInteractedSlot()+1 == 0 on the comparator). CITE
// ChiseledBookShelfBlockEntity ctor (lastInteractedSlot = -1) + loadAdditional default.
const chiseledBookshelfDefaultLastSlot = -1

// enchantedBookItemID is Items.ENCHANTED_BOOK (data/item/item.go id 1274), the sound-branch discriminator
// in addBook/removeBook (stack.is(Items.ENCHANTED_BOOK) picks the *_ENCHANTED sound variant). CITE
// ChiseledBookShelfBlock.addBook/removeBook (stack.is(Items.ENCHANTED_BOOK)).
const enchantedBookItemID = 1274

// chiseled bookshelf insert/pickup sound ids (data/soundid/soundid.go), played on SoundSource.BLOCKS.
// CITE SoundEvents.CHISELED_BOOKSHELF_INSERT/INSERT_ENCHANTED/PICKUP/PICKUP_ENCHANTED.
const (
	chiseledBookshelfInsertSoundID          int32 = 365 // block.chiseled_bookshelf.insert
	chiseledBookshelfInsertEnchantedSoundID int32 = 366 // block.chiseled_bookshelf.insert.enchanted
	chiseledBookshelfPickupSoundID          int32 = 368 // block.chiseled_bookshelf.pickup
	chiseledBookshelfPickupEnchantedSoundID int32 = 369 // block.chiseled_bookshelf.pickup.enchanted
)

// chiseledBookshelfBE is the tick-owned state of one chiseled_bookshelf block-entity -- the Go analogue
// of ChiseledBookShelfBlockEntity. items is the 6-slot container (empty Count 0 == ItemStack.EMPTY);
// lastInteractedSlot is the slot last added-to/removed-from (-1 when untouched), the comparator source.
// Registered on placement; read by the interaction + comparator seams. No per-tick drive (a chiseled
// bookshelf does not tick).
type chiseledBookshelfBE struct {
	items              [chiseledBookshelfMaxBooks]component.SlotData
	lastInteractedSlot int
}

// chiseledBookshelfAcceptsItemType ports ChiseledBookShelfBlockEntity.acceptsItemType(stack):
// stack.is(ItemTags.BOOKSHELF_BOOKS). CITE ChiseledBookShelfBlockEntity.acceptsItemType.
func chiseledBookshelfAcceptsItemType(stack component.SlotData) bool {
	if stackEmpty(stack) {
		return false
	}
	return itemInTag(int32(stack.ItemID), "bookshelf_books")
}

// chiseledBookshelfUpdateState ports ChiseledBookShelfBlockEntity.updateState(slot): guard 0<=slot<6
// (else a logged no-op), record slot as lastInteractedSlot, then re-derive all six SLOT_N_OCCUPIED flags
// from the container (!getItem(j).isEmpty()) and write the state with setBlock flag 3 (broadcast +
// neighbor update). The gameEvent(BLOCK_CHANGE) is a cite-deferred no-op (no game-event subsystem, the
// jukebox/lectern twin). CITE ChiseledBookShelfBlockEntity.updateState.
func (t *TickLoop) chiseledBookshelfUpdateState(pos pk.Position, b *chiseledBookshelfBE, slot int) {
	if slot < 0 || slot >= chiseledBookshelfMaxBooks {
		// LOGGER.error("Expected slot 0-5, got {}", slot): a logged no-op (no lastInteractedSlot change).
		udebug("bookshelf", "expected slot 0-5, got %d", slot)
		return
	}
	b.lastInteractedSlot = slot
	if t.world() == nil {
		return
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsChiseledBookshelf(state) {
		return
	}
	// for j in 0..5: state = state.setValue(SLOT_OCCUPIED_PROPERTIES.get(j), !getItem(j).isEmpty()).
	for j := 0; j < chiseledBookshelfMaxBooks; j++ {
		occ := !stackEmpty(b.items[j])
		ns, okj := block.ChiseledBookshelfWithSlot(state, j, occ)
		if !okj {
			return
		}
		state = ns
	}
	// level.setBlock(worldPosition, state, 3): broadcast + neighbor update.
	if !t.world().SetBlock(pos, state, dimMinY) {
		return
	}
	t.broadcastBlockUpdate(pos, state)
	// level.gameEvent(BLOCK_CHANGE, worldPosition, of(state)): cite-deferred (no game-event subsystem).
	t.onRedstoneEdit(pos) // the comparator below re-reads getLastInteractedSlot()+1
	t.markBookshelfDirty(pos)
}

// chiseledBookshelfRemoveItem ports ChiseledBookShelfBlockEntity.removeItem(slot, count): read the slot,
// clear it to EMPTY, and (if it held something) updateState(slot). Returns the removed stack (EMPTY for
// an empty/out-of-range slot). count is ignored (maxStackSize 1 -> a slot is all-or-nothing). CITE
// ChiseledBookShelfBlockEntity.removeItem.
func (t *TickLoop) chiseledBookshelfRemoveItem(pos pk.Position, b *chiseledBookshelfBE, slot int) component.SlotData {
	if slot < 0 || slot >= chiseledBookshelfMaxBooks {
		return component.SlotData{Count: 0}
	}
	stack := b.items[slot]
	b.items[slot] = component.SlotData{Count: 0}
	if !stackEmpty(stack) {
		t.chiseledBookshelfUpdateState(pos, b, slot)
	}
	return stack
}

// chiseledBookshelfSetItem ports ChiseledBookShelfBlockEntity.setItem(slot, stack): if the stack is a
// bookshelf-book, store it in the slot + updateState(slot); if it is EMPTY, removeItem(slot,
// getMaxStackSize()); otherwise (a non-book non-empty stack) a no-op. CITE
// ChiseledBookShelfBlockEntity.setItem.
func (t *TickLoop) chiseledBookshelfSetItem(pos pk.Position, b *chiseledBookshelfBE, slot int, stack component.SlotData) {
	if slot < 0 || slot >= chiseledBookshelfMaxBooks {
		return
	}
	if chiseledBookshelfAcceptsItemType(stack) {
		b.items[slot] = stack
		t.chiseledBookshelfUpdateState(pos, b, slot)
	} else if stackEmpty(stack) {
		t.chiseledBookshelfRemoveItem(pos, b, slot) // count = getMaxStackSize() (ignored)
	}
}

// chiseledBookshelfGetSection ports SelectableSlotContainer.getSection(f, n): Mth.clamp(Mth.floor(
// (f*16.0f)/(16.0f/n)), 0, n-1). The 16.0f/16.0f cancel algebraically to floor(f*n) clamped, but the
// port keeps the exact jar float ops (float multiply, float divide, Mth.floor == (int)Math.floor(
// (double)f)). CITE SelectableSlotContainer.getSection.
func chiseledBookshelfGetSection(f float32, n int) int {
	f2 := f * 16.0
	f3 := 16.0 / float32(n)
	sec := int(math.Floor(float64(f2 / f3)))
	return mthClampInt(sec, 0, n-1)
}

// chiseledBookshelfHitSlot ports SelectableSlotContainer.getHitSlot(hit, facing) ->
// getRelativeHitCoordinatesForBlockFace + lambda$getHitSlot$0. facing is the block's FACING value; the
// packet supplies the clicked face (hitFace) and the fractional cursor coordinates (cx,cy,cz) relative to
// the clicked block's own origin (the vanilla hit.getLocation() - blockPos, since Sulfur's cursor floats
// are already the in-block fraction). Returns (slot, true) when the click lands on the facing face's 2x3
// grid, (0, false) when the face mismatches or the direction is non-horizontal (Optional.empty). CITE
// SelectableSlotContainer.getHitSlot / getRelativeHitCoordinatesForBlockFace / getSection / lambda.
func chiseledBookshelfHitSlot(facing block.Direction, hitFace int, cx, cy, cz float32) (int, bool) {
	// getRelativeHitCoordinatesForBlockFace: if (hit.getDirection() != facing) return empty.
	if hitFace != int(facing) {
		return 0, false
	}
	// vanilla subtracts pos.relative(facing) from hit.getLocation(); Sulfur's cx/cy/cz are already the
	// in-block fraction of the CLICKED block. For a horizontal face the relevant in-face coords are:
	//   NORTH: Vec2(1 - x, y)   SOUTH: Vec2(x, y)   WEST: Vec2(z, y)   EAST: Vec2(1 - z, y)
	//   DOWN/UP: Optional.empty
	x := float64(cx)
	y := float64(cy)
	z := float64(cz)
	var vx, vy float32
	switch facing {
	case block.North:
		vx = float32(1.0 - x)
		vy = float32(y)
	case block.South:
		vx = float32(x)
		vy = float32(y)
	case block.West:
		vx = float32(z)
		vy = float32(y)
	case block.East:
		vx = float32(1.0 - z)
		vy = float32(y)
	default:
		return 0, false // DOWN/UP -> Optional.empty
	}
	// lambda$getHitSlot$0: row = getSection(1 - vec.y, getRows()); col = getSection(vec.x, getColumns());
	// return col + row * getColumns().
	row := chiseledBookshelfGetSection(1.0-vy, chiseledBookshelfRows)
	col := chiseledBookshelfGetSection(vx, chiseledBookshelfColumns)
	return col + row*chiseledBookshelfColumns, true
}

// chiseledBookshelfAddBook ports ChiseledBookShelfBlock.addBook(level,pos,player,be,stack,slot) (the
// server-side half; the isClientSide early-return is the caller's server context): pick the insert sound
// (enchanted variant for an enchanted_book), store one item into the slot (stack.consumeAndReturn(1) ==
// a single-count copy placed, the hand shrunk by 1 by the caller), and play the sound.
// awardStat(ITEM_USED) is a cite-deferred no-op (no stats subsystem). CITE ChiseledBookShelfBlock.addBook.
func (t *TickLoop) chiseledBookshelfAddBook(pos pk.Position, b *chiseledBookshelfBE, stack component.SlotData, slot int) {
	// player.awardStat(Stats.ITEM_USED.get(stack.getItem())): cite-deferred (no stats subsystem).
	sound := chiseledBookshelfInsertSoundID
	if int(stack.ItemID) == enchantedBookItemID {
		sound = chiseledBookshelfInsertEnchantedSoundID
	}
	// be.setItem(slot, stack.consumeAndReturn(1, player)): place a single-count copy of the held stack.
	one := stack
	one.Count = 1
	t.chiseledBookshelfSetItem(pos, b, slot, one)
	// level.playSound(null, pos, sound, SoundSource.BLOCKS, 1.0F, 1.0F).
	t.playSound(sound, soundSourceBlocks, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 1.0, 1.0, rand.Int64())
}

// chiseledBookshelfRemoveBook ports ChiseledBookShelfBlock.removeBook(level,pos,player,be,slot) (the
// server-side half): pull the slot's stack, pick the pickup sound, play it, then add the book back to the
// player's inventory (or drop it at the player when the inventory is full), and fire gameEvent(
// BLOCK_CHANGE) [cite-deferred]. CITE ChiseledBookShelfBlock.removeBook.
func (t *TickLoop) chiseledBookshelfRemoveBook(p *tickPlayer, pos pk.Position, b *chiseledBookshelfBE, slot int) {
	// ChiseledBookShelfBlockEntity.removeItem(slot, 1).
	stack := t.chiseledBookshelfRemoveItem(pos, b, slot)
	sound := chiseledBookshelfPickupSoundID
	if int(stack.ItemID) == enchantedBookItemID {
		sound = chiseledBookshelfPickupEnchantedSoundID
	}
	// level.playSound(null, pos, sound, SoundSource.BLOCKS, 1.0F, 1.0F).
	t.playSound(sound, soundSourceBlocks, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 1.0, 1.0, rand.Int64())
	// if (!player.getInventory().add(stack)) player.drop(stack, false).
	if p != nil {
		inv := ensureInventory(p)
		add := stack
		if !t.inventoryAdd(p, inv, &add) {
			t.playerDrop(p, add, false)
		}
	}
	// level.gameEvent(player, GameEvent.BLOCK_CHANGE, pos): cite-deferred (no game-event subsystem).
}

// useChiseledBookshelf ports ChiseledBookShelfBlock.useItemOn + useWithoutItem for a right-click on a
// chiseled bookshelf. It fuses the packet's clicked face + cursor fraction to resolve the hit slot, then
// dispatches ADD (a book in hand, empty slot) or REMOVE (empty-hand / occupied-slot path) exactly as the
// vanilla useItemOn -> TRY_WITH_EMPTY_HAND -> useWithoutItem chain does. Returns true when the
// interaction is CONSUMED (SUCCESS/CONSUME -- no block placed), false on the PASS paths (a miss /
// non-face click), matching the useBlockInteraction contract. CITE ChiseledBookShelfBlock.useItemOn /
// useWithoutItem.
func (t *TickLoop) useChiseledBookshelf(p *tickPlayer, pos pk.Position, state block.StateID, hitFace int, cx, cy, cz float32) bool {
	if t.world() == nil {
		return false
	}
	b := t.resolveChiseledBookshelf(pos)
	if b == nil {
		return false // getBlockEntity not a ChiseledBookShelfBlockEntity -> PASS
	}
	facing := block.ChiseledBookshelfFacing(state)
	slot, ok := chiseledBookshelfHitSlot(facing, hitFace, cx, cy, cz)
	if !ok {
		return false // getHitSlot Optional.empty -> PASS
	}
	occupied := block.ChiseledBookshelfSlotOccupied(state, slot)

	// useItemOn: read the held main-hand stack. A #bookshelf_books item into an EMPTY slot -> addBook.
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if !stackEmpty(held) && chiseledBookshelfAcceptsItemType(held) {
		// useItemOn: stack.is(BOOKSHELF_BOOKS) true. If the target slot is already occupied ->
		// TRY_WITH_EMPTY_HAND -> useWithoutItem removes it (the retry with an empty hand).
		if occupied {
			t.chiseledBookshelfRemoveBook(p, pos, b, slot)
			return true
		}
		// addBook: place one, consume 1 from the hand (creative keeps it -- ItemStack.consume no-ops
		// under hasInfiniteMaterials).
		t.chiseledBookshelfAddBook(pos, b, held, slot)
		if p.gameMode != gameModeCreative {
			t.shrinkHeldItem(p, inv)
		}
		return true
	}

	// useWithoutItem (empty hand, or a non-book hand -> useItemOn returns TRY_WITH_EMPTY_HAND -> retry):
	// an occupied slot -> removeBook (SUCCESS); an empty slot -> CONSUME (nothing to take, no placement).
	if occupied {
		t.chiseledBookshelfRemoveBook(p, pos, b, slot)
		return true
	}
	// slot empty + empty/non-book hand: useWithoutItem returns CONSUME (interaction consumed, no place).
	return true
}

// chiseledBookshelfAnalogOutputSignal ports ChiseledBookShelfBlock.getAnalogOutputSignal
// (hasAnalogOutputSignal == true): getBlockEntity(pos).getLastInteractedSlot() + 1. Returns (signal,
// true) for a chiseled bookshelf, (0, false) otherwise (so the comparator falls through to the next
// analog source / generic container path). CITE ChiseledBookShelfBlock.getAnalogOutputSignal.
func (t *TickLoop) chiseledBookshelfAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsChiseledBookshelf(state) {
		return 0, false
	}
	b := t.resolveChiseledBookshelf(pos)
	if b == nil {
		return 0, true
	}
	return b.lastInteractedSlot + 1, true
}

// resolveChiseledBookshelf returns the tick-owned chiseledBookshelfBE for pos, creating an EMPTY one
// (no books, lastInteractedSlot -1) on first access -- the analogue of a freshly-placed default
// ChiseledBookShelfBlockEntity. Returns nil when pos is not a chiseled bookshelf (or the world is
// unloaded). Tick-owned (t.bookshelves, the t.jukeboxes twin). On first access it folds in any persisted
// BE (Items + last_interacted_slot) via loadChiseledBookshelfBE.
func (t *TickLoop) resolveChiseledBookshelf(pos pk.Position) *chiseledBookshelfBE {
	if t.bookshelves == nil {
		t.bookshelves = make(map[pk.Position]*chiseledBookshelfBE)
	}
	if b, ok := t.bookshelves[pos]; ok {
		return b
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsChiseledBookshelf(state) {
		return nil
	}
	b := t.loadChiseledBookshelfBE(pos)
	if b == nil {
		b = &chiseledBookshelfBE{lastInteractedSlot: chiseledBookshelfDefaultLastSlot}
	}
	t.bookshelves[pos] = b
	return b
}
