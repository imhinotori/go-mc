package server

import (
	"bytes"

	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// inventory.go holds the server-owned, tick-owned component-slot inventory (ENT-04) and the
// on-tick handlers for the container packets routed in via dispatch (server/tick.go) and
// resolved in applyInput (server/subtick.go). The inventory is the AUTHORITATIVE source of
// truth (T-6-02): a ContainerClick's HashedStack hashes are decoded-and-DISCARDED — the
// server never builds a slot from client-claimed item data — and the server re-sends
// authoritative ContainerSetContent. Every handler decodes defensively: a malformed/short
// payload is a silent no-op, never a panic (the dispatch contract, T-6-04). All state is
// mutated ONLY on the tick goroutine (TICK-05 / T-6-08).

// playerInventorySize is the v1 player-inventory window slot count (containerId 0). The
// vanilla player inventory window is 46 slots (4 craft result/grid + 4 armor + 36 main +
// 1 offhand). v1 only needs a fixed-size authoritative slot array; the exact survival slot
// semantics (crafting, shift-click) are beyond v1.
const playerInventorySize = 46

// offhandWindowSlot is the single off-hand slot (Inventory.OFFHAND_SLOT) as a player-inventory
// WINDOW index: the LAST index of the 46-slot window (4 craft + 4 armor + 36 main + 1 offhand → 45).
// The off-hand sibling of heldWindowSlot (block_interact.go:326). Used by the held-item read
// (playerHoldsTempt, ai_goals_passive.go) since TemptGoal.shouldFollow tests main || off hand.
// (Equal to item_use.go's offHandMenuSlot 45 — same physical slot, named here for the S4 read seam.)
const offhandWindowSlot = 45

// playerContainerID is the window id of the player's own inventory (always 0 in vanilla).
const playerContainerID = 0

// Inventory is a fixed-size array of component-slot ItemStacks plus the selected hotbar slot.
// Server-owned and tick-owned: constructed and mutated only on the tick goroutine. The zero
// value is NOT ready — use newInventory.
type Inventory struct {
	slots    []component.SlotData
	heldSlot int16 // the selected hotbar slot (0..8), updated by SetCarriedItem
	// stateId is the container state counter echoed in ContainerSetContent/SetSlot. It is
	// incremented on each authoritative re-send so the client can detect desync.
	stateID int32

	// carried is the cursor (carried) ItemStack — AbstractContainerMenu.carried. It is the item the
	// player is "holding" on the mouse between clicks: PICKUP moves items between it and a slot, THROW
	// drops it, etc. Synced to the client via ClientboundContainerSetSlot(-1, stateId, -1, carried)
	// (setRemoteCarried). Empty == Count <= 0. Tick-owned (mutated only by the click engine).
	carried component.SlotData

	// quickcraftStatus / quickcraftType / quickcraftSlots are the QUICK_CRAFT (mouse-drag) state
	// machine fields — AbstractContainerMenu.quickcraftStatus (0=idle/end-bookkeeping, 1=dragging,
	// 2=releasing), quickcraftType (0=spread-even, 1=single, 2=creative clone), and the set of menu-slot
	// indices the drag has touched (the Set<Slot> as an index slice, dedup'd on add). Reset by
	// resetQuickCraft. Tick-owned.
	quickcraftStatus int
	quickcraftType   int
	quickcraftSlots  []int
}

// addQuickcraftSlot adds a menu-slot index to the quick-craft drag set if absent (the Set<Slot>.add
// dedup). Tick-owned.
func (inv *Inventory) addQuickcraftSlot(index int) {
	for _, e := range inv.quickcraftSlots {
		if e == index {
			return
		}
	}
	inv.quickcraftSlots = append(inv.quickcraftSlots, index)
}

// newInventory builds an empty player inventory (all slots empty, count 0).
func newInventory() *Inventory {
	return &Inventory{slots: make([]component.SlotData, playerInventorySize)}
}

// get returns the slot at index i, or an empty SlotData for an out-of-range index (defensive).
func (inv *Inventory) get(i int16) component.SlotData {
	if inv == nil || i < 0 || int(i) >= len(inv.slots) {
		return component.SlotData{Count: 0}
	}
	return inv.slots[i]
}

// set stores item into slot i if in range (a no-op out-of-range — never panics).
func (inv *Inventory) set(i int16, item component.SlotData) {
	if inv == nil || i < 0 || int(i) >= len(inv.slots) {
		return
	}
	inv.slots[i] = item
}

// snapshot returns a copy of the slot array for an authoritative ContainerSetContent.
func (inv *Inventory) snapshot() []component.SlotData {
	if inv == nil {
		return nil
	}
	out := make([]component.SlotData, len(inv.slots))
	copy(out, inv.slots)
	return out
}

// ensureInventory lazily initializes p.inventory. Owner-goroutine only.
func ensureInventory(p *tickPlayer) *Inventory {
	if p.inventory == nil {
		p.inventory = newInventory()
	}
	return p.inventory
}

// sendContent pushes the authoritative ContainerSetContent for the player's inventory through
// the bounded outbound queue. Bumps the state id so the client tracks the authoritative state.
func (t *TickLoop) sendContent(p *tickPlayer) {
	inv := ensureInventory(p)
	inv.stateID++
	p.client.Send(containerSetContent(playerContainerID, inv.stateID, inv.snapshot(), inv.getCarried()))
}

// slotDataEqual reports whether two slots are wire-identical (same emptiness, item, components).
// An empty slot is Count <= 0 regardless of the other fields, so two empties always compare equal.
func slotDataEqual(a, b component.SlotData) bool {
	if a.Count <= 0 || b.Count <= 0 {
		return a.Count <= 0 && b.Count <= 0
	}
	return a.Count == b.Count && a.ItemID == b.ItemID && bytes.Equal(a.RawComponents, b.RawComponents)
}

// broadcastInventoryChanges ports AbstractContainerMenu.broadcastChanges → synchronizeSlotToRemote
// (BUG-3): it diffs the current inventory against `before` and sends an authoritative
// ClientboundContainerSetSlot for EACH slot that changed, so the client always reflects the new
// contents (e.g. a picked-up stack appearing in a main/hotbar slot). Vanilla iterates every menu
// slot and, when the remote copy differs, sends a SetSlot(containerId, stateId, slot, item) where
// stateId comes from incrementStateId() (`(stateId + 1) & 32767`) — bumped ONCE per broadcast and
// reused for every changed slot. Mirrors that: one stateId bump, then one SetSlot per changed slot.
// If nothing changed (shouldn't happen after a successful add), no packets are sent. Tick-owned.
//
// Vanilla (javap AbstractContainerMenu.broadcastChanges / synchronizeSlotToRemote / incrementStateId):
//
//	for (i = 0; i < slots.size(); i++) { item = slots.get(i).getItem();
//	    synchronizeSlotToRemote(i, item, ...); }   // sends SetSlot when remoteSlots[i] != item
//	... using getStateId(); the menu's stateId is bumped (incrementStateId) on the broadcast.
func (t *TickLoop) broadcastInventoryChanges(p *tickPlayer, inv *Inventory, before []component.SlotData) {
	if p.client == nil {
		return
	}
	now := inv.slots
	// incrementStateId(): (stateId + 1) & 32767, bumped once for the whole broadcast.
	inv.stateID = (inv.stateID + 1) & 0x7FFF
	for i := range now {
		var prev component.SlotData
		if i < len(before) {
			prev = before[i]
		}
		if slotDataEqual(prev, now[i]) {
			continue // remoteSlots[i] == item: synchronizeSlotToRemote sends nothing
		}
		p.client.Send(containerSetSlot(playerContainerID, inv.stateID, int16(i), now[i]))
	}
}

// handleContainerClick resolves a ServerboundContainerClick on-tick (ENT-04). It decodes the
// 1.21.5+ HashedStack form WITHOUT mis-framing (jar-derived framing, server/slot_encode.go),
// DISCARDS the client's hashes (the server is authoritative — it never builds a slot from
// client-claimed item data, T-6-02), and APPLIES the click 1:1 via AbstractContainerMenu.clicked →
// doClick (server/inventory_doclick.go), then broadcasts the changed slots + the carried item so the
// client reflects the authoritative result. A malformed/truncated click Scan-errors to a silent no-op
// (no mutation, no re-send) and never panics (T-6-04); a panic INSIDE the click logic is recovered to a
// silent authoritative resend (the vanilla clicked() try/CrashReport equivalent — no partial mutation
// leaks).
//
// Jar-derived field order (ServerboundContainerClickPacket.STREAM_CODEC, 7-field composite):
//
//	VarInt containerId, VarInt stateId, Short slotNum, Byte buttonNum,
//	VarInt containerInput, map<Short,HashedStack> changedSlots, HashedStack carriedItem
func (t *TickLoop) handleContainerClick(p *tickPlayer, pkt pk.Packet) {
	r := bytes.NewReader(pkt.Data)

	var containerID, stateID pk.VarInt
	var slotNum pk.Short
	var button pk.Byte
	var containerInput pk.VarInt
	header := pk.Tuple{&containerID, &stateID, &slotNum, &button, &containerInput}
	if _, err := header.ReadFrom(r); err != nil {
		return // malformed header: no-op (T-6-04)
	}

	// changedSlots: map<Short slot -> HashedStack>. VarInt count, then count × (Short + HashedStack).
	var changedCount pk.VarInt
	if _, err := changedCount.ReadFrom(r); err != nil {
		return
	}
	// Bound the count defensively (vanilla SLOTS_STREAM_CODEC max is 128) so a forged huge
	// count cannot drive a long loop before the reader EOFs.
	if changedCount < 0 || changedCount > 128 {
		return
	}
	for i := int32(0); i < int32(changedCount); i++ {
		var slot pk.Short
		if _, err := slot.ReadFrom(r); err != nil {
			return
		}
		// Decode and DISCARD the HashedStack (consume bytes, ignore the hashes — T-6-02).
		if _, err := decodeHashedStack(r); err != nil {
			return
		}
	}

	// carriedItem: a single HashedStack. Decode-and-discard. If this mis-framed, the read
	// would error here — proving the whole packet was consumed correctly.
	if _, err := decodeHashedStack(r); err != nil {
		return
	}

	// The click decoded cleanly and the client's claimed contents (the hashes) are discarded — the
	// server is AUTHORITATIVE. Apply it.
	t.clicked(p, int32(containerID), int16(slotNum), int(button), int32(containerInput))
}

// clicked ports AbstractContainerMenu.clicked(slotId, button, input, player) for the player inventory
// window (containerId 0). Only window 0 exists in v1; a click on any other container id resends
// authoritative content and returns (other menus unimplemented — CITE: no non-player menus in v1). It
// snapshots the slots + carried, runs doClick inside a panic-recover (the vanilla clicked() try block
// → on a throw, no partial mutation leaks; resend authoritative content — T-6-04), then broadcasts the
// changed slots (broadcastInventoryChanges) and, when the carried item changed, syncs it to the client
// (synchronizeCarriedToRemote).
func (t *TickLoop) clicked(p *tickPlayer, containerID int32, slotNum int16, button int, input int32) {
	inv := ensureInventory(p)

	// An OPEN chest window (STRUCT-POLISH-01) routes to the chest click engine: the click moves
	// items between the chest container and the player inventory over the shared cursor. The window
	// id must match the player's currently-open container (a forged/stale id is rejected — resend).
	if containerID != playerContainerID {
		if p.openContainer != nil && containerID == int32(p.openContainer.windowID) {
			switch p.openContainer.kind {
			case containerKindChest:
				if cl := t.resolveChest(p.openContainer.chestPos); cl != nil {
					t.clickedChest(p, cl, slotNum, button, input)
					return
				}
			case containerKindCrafting:
				t.clickedCrafting(p, p.openContainer, slotNum, button, input)
				return
			case containerKindStonecutter:
				t.clickedStonecutter(p, p.openContainer, slotNum, button, input)
				return
			case containerKindMerchant:
				t.clickedMerchant(p, p.openContainer, slotNum, button, input)
				return
			case containerKindFurnace:
				t.clickedFurnace(p, p.openContainer, slotNum, button, input)
				return
			case containerKindBrewingStand:
				t.clickedBrewingStand(p, p.openContainer, slotNum, button, input)
				return
			case containerKindDispenser:
				t.clickedDispenser(p, p.openContainer, slotNum, button, input)
				return
			case containerKindHopper:
				t.clickedHopper(p, p.openContainer, slotNum, button, input)
				return
			case containerKindBeacon:
				t.clickedBeacon(p, p.openContainer, slotNum, button, input)
				return
			case containerKindMinecartChest:
				t.clickedMinecartChest(p, p.openContainer, slotNum, button, input)
				return
			case containerKindAnvil:
				t.clickedAnvil(p, p.openContainer, slotNum, button, input)
				return
			case containerKindEnchant:
				t.clickedEnchant(p, p.openContainer, slotNum, button, input)
				return
			case containerKindGrindstone:
				t.clickedGrindstone(p, p.openContainer, slotNum, button, input)
				return
			case containerKindSmithing:
				t.clickedSmithing(p, p.openContainer, slotNum, button, input)
				return
			}
		}
		t.sendContent(p) // unknown/stale window: resend authoritative player content
		return
	}

	before := inv.snapshot()
	carriedBefore := inv.getCarried()

	// Recover a panic inside doClick to a silent no-op + authoritative resend (T-6-04): the in-place
	// mutations up to the panic point are discarded by restoring the pre-click snapshot, so no partial
	// state leaks to the client.
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				copy(inv.slots, before)
				inv.setCarried(carriedBefore)
				t.sendContent(p)
			}
		}()
		t.doClick(p, inv, int(slotNum), button, int(input))
	}()

	// slotsChanged: after the click, recompute the 2x2 player-grid result through the plugin matcher
	// (CraftingMenu.slotsChanged -> slotChangedCraftingGrid). A change to grid slots 1-4 repopulates (or
	// clears) result slot 0. The consume (onTakeCraft) also re-runs this, so a take leaves a correct
	// result too. Recomputed from the (possibly panic-restored) grid. PLUGIN-05.
	t.slotChangedCraftingGrid(playerCraftView(inv))

	// broadcastChanges: send a SetSlot for each changed slot (one stateId bump for the whole broadcast).
	t.broadcastInventoryChanges(p, inv, before)

	// synchronizeCarriedToRemote: when the carried item changed, sync it via
	// ClientboundContainerSetSlot(-1, stateId, -1, carried) (containerId -1, slot -1). Reuses the stateId
	// just bumped by broadcastInventoryChanges.
	if !slotDataEqual(carriedBefore, inv.getCarried()) {
		p.client.Send(setCursorItem(inv.getCarried()))
	}
}

// handleSetCreativeModeSlot resolves a ServerboundSetCreativeModeSlot on-tick (ENT-04): a
// CREATIVE player sets a slot directly with a FULL component-slot ItemStack (server-bound).
// Decoded defensively; stored into the tick-owned inventory. Jar-derived: Short slot + ItemStack (SlotData).
//
// 1:1 net.minecraft.server.network.ServerGamePacketListenerImpl.handleSetCreativeModeSlot:
//
//	if (!player.hasInfiniteMaterials()) return;                       // CREATIVE gate (SECURITY: a survival
//	                                                                  //   client must not fabricate items)
//	validSlot = slot >= 1 && slot <= 45;
//	validItem = item.isEmpty() || item.getCount() <= item.getMaxStackSize();
//	if (validSlot && validItem) { inventoryMenu.getSlot(slot).setByPlayer(item); setRemoteSlot(slot, item); }
//
// The slot < 0 branch (drop the item into the world via the dropSpamThrottler) is CITE-DEFERRED (no throttler
// wired); the security-critical gate + validity checks are the deliverable. CITE handleSetCreativeModeSlot.
func (t *TickLoop) handleSetCreativeModeSlot(p *tickPlayer, pkt pk.Packet) {
	r := bytes.NewReader(pkt.Data)
	var slot pk.Short
	if _, err := slot.ReadFrom(r); err != nil {
		return
	}
	var item component.SlotData
	if _, err := item.ReadFrom(r); err != nil {
		return // malformed item: no-op
	}
	// hasInfiniteMaterials(): ONLY a creative player may set a creative slot. A survival/adventure client
	// sending this packet is ignored (it would otherwise be a free item-duplication exploit).
	if p.gameMode != gameModeCreative {
		return
	}
	// validSlot: the InventoryMenu window is 1..45 (slot 0 is the craft result — never client-settable).
	// A forged out-of-range slot (or the -1 world-drop, deferred) is rejected.
	if slot < 1 || slot > 45 {
		return
	}
	// validItem: an empty stack clears the slot; a non-empty one must not exceed its max stack size.
	if !stackEmpty(item) && int(item.Count) > stackMaxSize(item) {
		return
	}
	ensureInventory(p).set(int16(slot), item)
	// Echo the authoritative content back so the client renders the slot (sendContent bumps the state id).
	t.sendContent(p)
}

// handleSetCarriedItem resolves a ServerboundSetCarriedItem on-tick (ENT-04): the player's
// selected hotbar slot (held item). Jar-derived: a single Short slot. Defensive decode.
func (t *TickLoop) handleSetCarriedItem(p *tickPlayer, pkt pk.Packet) {
	r := bytes.NewReader(pkt.Data)
	var slot pk.Short
	if _, err := slot.ReadFrom(r); err != nil {
		return
	}
	// Bound to the 9 hotbar slots; a forged out-of-range slot is ignored.
	if slot < 0 || slot > 8 {
		return
	}
	ensureInventory(p).heldSlot = int16(slot)
}

// handleContainerClose resolves a ServerboundContainerClose on-tick (ENT-04 / STRUCT-POLISH-01).
// When the player has an OPEN chest window, closing it ports ServerPlayer.closeContainer →
// AbstractContainerMenu.removed: the chest container's contents are already authoritative in the
// tick-owned chestLoot (every click mutated it in place and t.openChests retains it across opens),
// so close just FREES the windowId (clears p.openContainer) — the chest items persist in
// t.openChests and re-open serves them. The player-inventory window (id 0) close is a no-op (it
// stays conceptually open). Defensive: a malformed/short payload is tolerated (never panics).
//
// Persistence note: the chest contents survive in t.openChests for the server's lifetime; flushing
// them back to the chunk BlockEntity NBT on chunk-unload/world-save is a follow-up (the BE NBT
// currently only carries {LootTable, LootTableSeed} — a future plan adds the item list on save).
func (t *TickLoop) handleContainerClose(p *tickPlayer, pkt pk.Packet) {
	if p == nil {
		return
	}
	// A CRAFTING window (PLUGIN-05) returns its transient 3x3 grid to the player on close
	// (CraftingMenu.removed -> clearContainer) BEFORE freeing the window — NOT the chest persist model
	// (Pitfall 7): the grid items are real and must not be lost. The chest path keeps its items in the
	// tick-owned chestLoot (already mutated by clicks), so freeing the window is its whole close.
	if p.openContainer != nil && p.openContainer.kind == containerKindCrafting {
		t.closeCraftingWindow(p, p.openContainer)
	}
	// A STONECUTTER window (PLUGIN-05 Plan 25-03) returns its transient INPUT to the player on close
	// (StonecutterMenu.removed -> clearContainer over the input slot) — the result is virtual (not
	// returned). The input is real and must not be lost.
	if p.openContainer != nil && p.openContainer.kind == containerKindStonecutter {
		t.closeStonecutterWindow(p, p.openContainer)
	}
	// A MERCHANT window (VILLAGER-MENU) returns its two transient PAYMENT inputs to the player on close
	// (MerchantMenu.removed -> placeItemBackInInventory over payment slots 0,1) and clears the villager's
	// tradingPlayer (trader.setTradingPlayer(null)). The result is virtual (not returned). The payments are
	// real and must not be lost.
	if p.openContainer != nil && p.openContainer.kind == containerKindMerchant {
		t.closeMerchantWindow(p, p.openContainer)
	}
	// A FURNACE window (GAMEPLAY-05) is the block-entity container: its 3 slots persist in the tick-owned
	// furnaceBE (like a chest), so close just frees the window (closeFurnaceWindow is a no-op) — the items
	// are NOT returned to the player (they belong to the furnace). The carried (cursor) item return below
	// still runs for all window kinds.
	if p.openContainer != nil && p.openContainer.kind == containerKindFurnace {
		t.closeFurnaceWindow(p, p.openContainer)
	}
	// A DISPENSER/DROPPER window (REDSTONE TIER-4) is the block-entity container: its 9 slots persist in the
	// tick-owned dispenserBE (like a chest/furnace), so close just frees the window (closeDispenserWindow is
	// a no-op) — the items are NOT returned to the player (they belong to the dispenser). The carried (cursor)
	// item return below still runs for all window kinds.
	if p.openContainer != nil && p.openContainer.kind == containerKindDispenser {
		t.closeDispenserWindow(p, p.openContainer)
	}
	// A HOPPER window is the block-entity container: its 5 slots persist in the tick-owned hopperBE (like a
	// dispenser/furnace/chest), so close just frees the window (closeHopperWindow is a no-op) — the items are
	// NOT returned to the player. The carried (cursor) item return below still runs for all window kinds.
	if p.openContainer != nil && p.openContainer.kind == containerKindHopper {
		t.closeHopperWindow(p, p.openContainer)
	}
	// A BEACON window (BEACON-01) DROPS its payment slot on close (BeaconMenu.removed -> itemStack =
	// paymentSlot.remove(maxStackSize); if (!empty) player.drop(itemStack, false)). The payment is transient
	// (it backs the menu's PaymentSlot, not the beacon's long-lived state), so an unpaid ingot is returned to
	// the world rather than kept. The beacon's selected effect + level persist in the BE.
	if p.openContainer != nil && p.openContainer.kind == containerKindBeacon {
		t.closeBeaconWindow(p, p.openContainer)
	}
	// An ANVIL window (ItemCombinerMenu) returns its two TRANSIENT input slots to the player on close
	// (ItemCombinerMenu.removed -> super.removed -> clearContainer over the input container). The result
	// (2) is virtual (never returned). The inputs are real and must not be lost.
	if p.openContainer != nil && p.openContainer.kind == containerKindAnvil {
		t.closeAnvilWindow(p, p.openContainer)
	}
	// An ENCHANTMENT-TABLE window returns its two TRANSIENT slots (item + lapis) to the player on close
	// (EnchantmentMenu.removed -> clearContainer over the enchant slots). Both are real and must not be lost.
	if p.openContainer != nil && p.openContainer.kind == containerKindEnchant {
		t.closeEnchantWindow(p, p.openContainer)
	}
	// A GRINDSTONE window returns its two transient INPUT slots to the player on close (GrindstoneMenu.
	// removed -> clearContainer over repairSlots). The result is virtual (not returned). The inputs are
	// real and must not be lost.
	if p.openContainer != nil && p.openContainer.kind == containerKindGrindstone {
		t.closeGrindstoneWindow(p, p.openContainer)
	}
	// A SMITHING window returns its three transient INPUT slots to the player on close (ItemCombinerMenu.
	// removed -> clearContainer over inputSlots). The result is virtual (not returned). The inputs are
	// real and must not be lost.
	if p.openContainer != nil && p.openContainer.kind == containerKindSmithing {
		t.closeSmithingWindow(p, p.openContainer)
	}
	// The CARRIED (cursor) item: vanilla AbstractContainerMenu.removed() places a left-on-cursor item
	// back into the inventory (or drops it) and clears the cursor. v1 previously LEFT it on the cursor —
	// but ClientboundContainerClose tears down the window, so the client no longer renders the cursor
	// item: it became invisible/limbo (the "items go weird on close" bug). Port removed() faithfully:
	// place the carried item back into the player inventory (drop it if the inventory is full), clear the
	// cursor, and re-sync window 0 so the client shows the reconciled inventory.
	//   [VERIFIED javap: net.minecraft.world.inventory.AbstractContainerMenu.removed(Player): if carried
	//    not empty -> dropOrPlaceInInventory(player, carried) { live player -> placeItemBackInInventory;
	//    disconnected/removed -> drop } then setCarried(EMPTY). v1's normal close path is the live-player
	//    placeItemBackInInventory branch.]
	inv := ensureInventory(p)
	if carried := inv.getCarried(); !stackEmpty(carried) {
		add := carried
		if !t.inventoryAdd(p, inv, &add) {
			// Inventory full: drop the residual into the world (placeItemBackInInventory's overflow
			// path -> player.drop), mirroring dropOrPlaceInInventory's drop branch.
			t.playerDrop(p, add, false)
		}
		inv.setCarried(component.SlotData{})
		t.sendContent(p) // re-sync window 0 so the client renders the reconciled inventory + empty cursor
	}
	p.openContainer = nil
	_ = pkt
}
