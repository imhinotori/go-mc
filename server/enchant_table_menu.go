package server

// enchant_table_menu.go — the ENCHANTMENT-TABLE menu (EnchantmentMenu): the item + lapis slot
// container with the 3 seeded offers, ported 1:1 from net.minecraft.world.inventory.EnchantmentMenu
// over the 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). Opens on right-clicking an
// enchanting_table block (chest_open.go useBlockInteraction).
//
// 1:1 jar chain:
//
//	EnchantingTableBlock.useWithoutItem -> player.openMenu(EnchantmentMenu); return SUCCESS.
//	EnchantmentMenu(id, inv, access): addSlot(item@0, max stack 1), addSlot(lapis@1, mayPlace = LAPIS),
//	    addStandardInventorySlots(inv, 8, 84) -> main 2..28, hotbar 29..37 (38 slots). addDataSlots:
//	    costs[0..2], enchantmentSeed (= player.getEnchantmentSeed()), enchantClue[0..2], levelClue[0..2].
//	EnchantmentMenu.slotsChanged(enchantSlots): if item empty || !isEnchantable -> clear costs/clues;
//	    else access.execute: bookcases = scan BOOKSHELF_OFFSETS; random.setSeed(enchantmentSeed);
//	    for i in 0..2: costs[i] = getEnchantmentCost(random, i, bookcases, item); clues -1; if costs[i] <
//	    i+1 costs[i]=0. then for i in 0..2: if costs[i]>0 { list = getEnchantmentList(item, i, costs[i]);
//	    ench = list.get(random.nextInt(size)); enchantClue[i]=id(ench); levelClue[i]=level }.
//	EnchantmentMenu.clickMenuButton(player, id): validate id; enchantmentCost = id+1; require lapis >=
//	    enchantmentCost (or creative); require costs[id]>0 && item non-empty && (level >= id+1 && level >=
//	    costs[id] || creative). Apply: getEnchantmentList(item,id,costs[id]); if !empty {
//	    onEnchantmentPerformed(item, enchantmentCost); (BOOK -> ENCHANTED_BOOK); enchant each; lapis
//	    .consume(enchantmentCost); awardStat; enchantmentSeed = getEnchantmentSeed(); slotsChanged }.
//	EnchantmentMenu.getEnchantmentList(item, slot, cost): random.setSeed(enchantmentSeed + slot);
//	    list = selectEnchantment(random, item, cost, IN_ENCHANTING_TABLE.stream()); if (item.is(BOOK) &&
//	    list.size() > 1) list.remove(random.nextInt(size)); return list.
//	EnchantmentMenu.removed(player): clearContainer over the 2 enchant slots (return item + lapis).
//
// v1 subset (cited): no stats/criteria/sound (awardStat / ENCHANTED_ITEM trigger / the
// ENCHANTMENT_TABLE_USE sound are faithful no-ops); the enchant-table book-open particle animation is
// a cited client cosmetic. The ContainerLevelAccess reach folds into the open-time reach gate. The 2
// slots are a TRANSIENT container returned to the player on close — NOT a block-entity.
//
// COMPONENT SEAM (cited, enchantment_logic.go header): the table offers gate on isEnchantable ==
// has(ENCHANTABLE component) — a v1 stack carries components only when set (RawComponents), so a bare
// item with no ENCHANTABLE component offers nothing, faithfully matching ItemStack.isEnchantable().

import (
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// enchantMenuSize is the EnchantmentMenu slot count: item 0 + lapis 1 + 27 main + 9 hotbar = 38.
const enchantMenuSize = 2 + 27 + 9 // 38

const (
	enchantSlotItem  = 0 // the item to enchant (max stack 1)
	enchantSlotLapis = 1 // the lapis lazuli currency (mayPlace = LAPIS_LAZULI)
)

// enchant data-slot ids (EnchantmentMenu.addDataSlot order): costs[0..2], enchantmentSeed,
// enchantClue[0..2], levelClue[0..2]. 10 data slots total.
const (
	enchantDataCost0  = 0
	enchantDataCost1  = 1
	enchantDataCost2  = 2
	enchantDataSeed   = 3
	enchantDataClue0  = 4
	enchantDataClue1  = 5
	enchantDataClue2  = 6
	enchantDataLevel0 = 7
	enchantDataLevel1 = 8
	enchantDataLevel2 = 9
	enchantDataValues = 10
)

// isEnchantingTableBlock reports whether a block state is Blocks.ENCHANTING_TABLE. CITE
// EnchantingTableBlock.
func isEnchantingTableBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	_, ok := block.StateList[s].(block.EnchantingTable)
	return ok
}

// openEnchantTable ports EnchantingTableBlock.useWithoutItem -> ServerPlayer.openMenu(EnchantmentMenu):
// close any prior window, allocate a windowId, seed the enchantmentSeed data slot from the player's
// getEnchantmentSeed(), send ClientboundOpenScreen(enchantment) + the initial ContainerSetContent +
// the 10 data slots. Returns true (the interaction was consumed).
func (t *TickLoop) openEnchantTable(p *tickPlayer, pos pk.Position) bool {
	if p == nil || p.client == nil || t.world() == nil {
		return false
	}
	if p.openContainer != nil {
		p.openContainer = nil
	}
	win := p.nextContainerCounter()
	oc := &openContainer{windowID: win, kind: containerKindEnchant, enchantPos: pos}
	// enchantmentSeed DataSlot = inventory.player.getEnchantmentSeed() (set once at ctor).
	oc.enchantSeed = int32(getEnchantmentSeed(p))
	oc.enchantClueEnch = [3]int32{-1, -1, -1}
	oc.enchantClueLevel = [3]int32{-1, -1, -1}
	p.openContainer = oc

	menuID := menuTypeID(registryid.Menu, "minecraft:enchantment")
	p.client.Send(openScreen(int32(win), menuID, "Enchant"))

	t.sendEnchantContent(p, oc)
	t.sendEnchantData(p, oc)
	return true
}

// enchantMenuItems builds the 38-slot ContainerSetContent list: item 0, lapis 1, then the player
// inventory (main 2..28 <- window 9..35, hotbar 29..37 <- window 36..44).
func enchantMenuItems(oc *openContainer, inv *Inventory) []component.SlotData {
	out := make([]component.SlotData, enchantMenuSize)
	out[enchantSlotItem] = oc.enchantItem
	out[enchantSlotLapis] = oc.enchantLapis
	for i := 0; i < 27; i++ {
		out[2+i] = inv.get(int16(windowMainFirst + i))
	}
	for i := 0; i < 9; i++ {
		out[2+27+i] = inv.get(int16(windowHotbarFirst + i))
	}
	return out
}

// sendEnchantContent pushes the authoritative ContainerSetContent for the open enchant window.
func (t *TickLoop) sendEnchantContent(p *tickPlayer, oc *openContainer) {
	if p.client == nil || oc == nil || oc.kind != containerKindEnchant {
		return
	}
	inv := ensureInventory(p)
	inv.incrementStateId()
	p.client.Send(containerSetContent(int32(oc.windowID), inv.stateID, enchantMenuItems(oc, inv), inv.getCarried()))
}

// sendEnchantData pushes the 10 data slots (costs, seed, enchant clues, level clues) — the offers the
// client renders. CITE EnchantmentMenu.addDataSlots + broadcastChanges.
func (t *TickLoop) sendEnchantData(p *tickPlayer, oc *openContainer) {
	if p.client == nil || oc == nil || oc.kind != containerKindEnchant {
		return
	}
	win := int32(oc.windowID)
	p.client.Send(containerSetData(win, enchantDataCost0, int16(oc.enchantCosts[0])))
	p.client.Send(containerSetData(win, enchantDataCost1, int16(oc.enchantCosts[1])))
	p.client.Send(containerSetData(win, enchantDataCost2, int16(oc.enchantCosts[2])))
	p.client.Send(containerSetData(win, enchantDataSeed, int16(oc.enchantSeed)))
	p.client.Send(containerSetData(win, enchantDataClue0, int16(oc.enchantClueEnch[0])))
	p.client.Send(containerSetData(win, enchantDataClue1, int16(oc.enchantClueEnch[1])))
	p.client.Send(containerSetData(win, enchantDataClue2, int16(oc.enchantClueEnch[2])))
	p.client.Send(containerSetData(win, enchantDataLevel0, int16(oc.enchantClueLevel[0])))
	p.client.Send(containerSetData(win, enchantDataLevel1, int16(oc.enchantClueLevel[1])))
	p.client.Send(containerSetData(win, enchantDataLevel2, int16(oc.enchantClueLevel[2])))
}

// enchantSlotsChanged ports EnchantmentMenu.slotsChanged(enchantSlots): recompute the 3 offers from
// the item slot + the bookshelf power. Called after any change to the item/lapis slots. CITE
// EnchantmentMenu.slotsChanged.
func (t *TickLoop) enchantSlotsChanged(oc *openContainer) {
	item := oc.enchantItem
	// if (itemStack.isEmpty() || !itemStack.isEnchantable()) { clear costs + clues }
	if stackEmpty(item) || !isEnchantable(item) {
		for i := 0; i < 3; i++ {
			oc.enchantCosts[i] = 0
			oc.enchantClueEnch[i] = -1
			oc.enchantClueLevel[i] = -1
		}
		return
	}

	// access.execute: bookcases = scan; random.setSeed(enchantmentSeed).
	//
	// LOAD-BEARING RNG: `random` is the menu's SINGLE `this.random` instance (a per-call legacyRandom
	// here). getEnchantmentList RESEEDS this SAME random to enchantmentSeed+slot and draws through it, so
	// after the clue-loop's getEnchantmentList call, the `random.nextInt(list.size())` clue pick reads
	// from that mutated stream (NOT the cost-loop stream) — exactly as vanilla's shared this.random does.
	bookcases := t.enchantBookshelfPower(oc.enchantPos)
	random := newLegacyRandom(int64(oc.enchantSeed))
	for i := 0; i < 3; i++ {
		oc.enchantCosts[i] = getEnchantmentCost(random, i, bookcases, item)
		oc.enchantClueEnch[i] = -1
		oc.enchantClueLevel[i] = -1
		if oc.enchantCosts[i] < i+1 {
			oc.enchantCosts[i] = 0
		}
	}
	for i := 0; i < 3; i++ {
		if oc.enchantCosts[i] <= 0 {
			continue
		}
		list := enchantGetList(random, oc.enchantSeed, item, i, oc.enchantCosts[i])
		if len(list) == 0 {
			continue
		}
		// ench = list.get(this.random.nextInt(list.size())) — the SAME random getEnchantmentList reseeded.
		ench := list[int(random.nextIntN(int32(len(list))))]
		oc.enchantClueEnch[i] = int32(enchantWireID(ench.id))
		oc.enchantClueLevel[i] = int32(ench.level)
	}
}

// enchantGetList ports EnchantmentMenu.getEnchantmentList(access, itemStack, slot, cost):
// this.random.setSeed(enchantmentSeed + slot); list = selectEnchantment(this.random, item, cost,
// IN_ENCHANTING_TABLE.stream()); if (item.is(BOOK) && list.size() > 1) list.remove(this.random.nextInt
// (size)). It RESEEDS + draws through the caller's shared `random` (the menu's this.random) so the
// caller's subsequent draws read the mutated stream. CITE EnchantmentMenu.getEnchantmentList.
func enchantGetList(random *legacyRandom, seed int32, item component.SlotData, slot, cost int) []enchantInstance {
	random.setSeed(int64(seed) + int64(slot)) // this.random.setSeed(enchantmentSeed + slot)
	list := selectEnchantment(random, item, cost, inEnchantingTableSource())
	if int32(item.ItemID) == itemNameToID("book") && len(list) > 1 {
		idx := int(random.nextIntN(int32(len(list))))
		list = append(list[:idx], list[idx+1:]...)
	}
	return list
}

// enchantBookshelfPower ports the BOOKSHELF_OFFSETS scan in EnchantmentMenu.slotsChanged: count the
// valid bookshelves around the enchanting table. BOOKSHELF_OFFSETS = the (x,0..1,z) in [-2,2]×[0,1]×
// [-2,2] where |x|==2 or |z|==2; a bookshelf is valid at `offset` iff the block at pos+offset is in
// ENCHANTMENT_POWER_PROVIDER (bookshelf) AND the block at pos+(offset.x/2, offset.y, offset.z/2) is in
// ENCHANTMENT_POWER_TRANSMITTER (the gap must be passable/air). CITE EnchantingTableBlock.
// BOOKSHELF_OFFSETS + isValidBookShelf.
func (t *TickLoop) enchantBookshelfPower(pos pk.Position) int {
	if t.world() == nil {
		return 0
	}
	bookcases := 0
	for _, off := range enchantBookshelfOffsets() {
		if t.enchantValidBookshelf(pos, off) {
			bookcases++
		}
	}
	return bookcases
}

// enchantOffset is a BOOKSHELF_OFFSETS entry.
type enchantOffset struct{ x, y, z int }

// enchantBookshelfOffsets builds EnchantingTableBlock.BOOKSHELF_OFFSETS: BlockPos.betweenClosed(-2,0,
// -2, 2,1,2) filtered to |x|==2 || |z|==2. The iteration order matches BlockPos.betweenClosedStream
// (x outer, y middle, z inner) — though the scan only COUNTS (order does not affect the total).
func enchantBookshelfOffsets() []enchantOffset {
	out := make([]enchantOffset, 0, 32)
	for x := -2; x <= 2; x++ {
		for y := 0; y <= 1; y++ {
			for z := -2; z <= 2; z++ {
				if absInt(x) == 2 || absInt(z) == 2 {
					out = append(out, enchantOffset{x, y, z})
				}
			}
		}
	}
	return out
}

// enchantValidBookshelf ports EnchantingTableBlock.isValidBookShelf(level, pos, offset): the block at
// pos+offset is in ENCHANTMENT_POWER_PROVIDER AND the block at pos+(offset.x/2, offset.y, offset.z/2)
// is in ENCHANTMENT_POWER_TRANSMITTER.
func (t *TickLoop) enchantValidBookshelf(pos pk.Position, off enchantOffset) bool {
	shelfPos := pk.Position{X: pos.X + off.x, Y: pos.Y + off.y, Z: pos.Z + off.z}
	shelf, ok := t.world().GetBlock(shelfPos, dimMinY)
	if !ok || !blockInTag(shelf, "enchantment_power_provider") {
		return false
	}
	gapPos := pk.Position{X: pos.X + off.x/2, Y: pos.Y + off.y, Z: pos.Z + off.z/2}
	gap, ok := t.world().GetBlock(gapPos, dimMinY)
	if !ok {
		return false
	}
	return blockInTag(gap, "enchantment_power_transmitter")
}

// closeEnchantWindow ports EnchantmentMenu.removed(player): clearContainer over the two enchant slots
// (return item + lapis to the player, or drop on overflow). CITE EnchantmentMenu.removed.
func (t *TickLoop) closeEnchantWindow(p *tickPlayer, oc *openContainer) {
	inv := ensureInventory(p)
	for _, s := range []component.SlotData{oc.enchantItem, oc.enchantLapis} {
		if stackEmpty(s) {
			continue
		}
		stack := s
		if !t.invAdd(inv, &stack) {
			t.playerDrop(p, stack, false)
		}
	}
	oc.enchantItem = component.SlotData{Count: 0}
	oc.enchantLapis = component.SlotData{Count: 0}
}
