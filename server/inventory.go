package server

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/registryid"
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
	inv.incrementStateId()
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
	inv.incrementStateId()
	for i := range now {
		var prev component.SlotData
		if i < len(before) {
			prev = before[i]
		}
		if slotDataEqual(prev, now[i]) {
			continue // remoteSlots[i] == item: synchronizeSlotToRemote sends nothing
		}
		p.client.Send(containerSetSlot(playerContainerID, inv.stateID, int16(i), now[i]))
		// ADVANCEMENTS (advancements.go): minecraft:inventory_changed — vanilla drives
		// InventoryChangeTrigger from Inventory.setChanged -> ServerPlayer.inventoryChanged(container),
		// which scans the whole inventory. Fired here for each CHANGED slot's new item id (the diff seam
		// AbstractContainerMenu.broadcastChanges runs on the player inventory): a crafted/moved item
		// landing in a slot grants inventory_changed criteria that list it (story/root crafting_table).
		// The grant is idempotent, so overlapping with the item-pickup feed (item_entity.go) is harmless.
		// Only fired when this is the player inventory window (containerId 0). CITE: Inventory.setChanged
		// -> ServerPlayer.inventoryChanged -> CriteriaTriggers.INVENTORY_CHANGED.trigger.
		if int(now[i].ItemID) >= 0 && int(now[i].ItemID) < len(registryid.Item) && now[i].Count > 0 {
			t.triggerInventoryChanged(p, registryid.Item[now[i].ItemID])
		}
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
			case containerKindLoom:
				t.clickedLoom(p, p.openContainer, slotNum, button, input)
				return
			case containerKindShulker:
				t.clickedShulker(p, p.openContainer, slotNum, button, input)
				return
			case containerKindEnderChest:
				t.clickedEnderChest(p, p.openContainer, slotNum, button, input)
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
	// A client-initiated ContainerClose runs the exact AbstractContainerMenu.removed() teardown the
	// server-initiated stillValid auto-close runs -- both delegate to closeOpenContainer (menu_stillvalid.go)
	// so the per-kind clearContainer/placeItemBack, the carried-cursor return, and the CONTAINER_CLOSE game
	// event are IDENTICAL on both paths. The client already tore down its own screen when it sent this
	// packet, so (unlike serverCloseContainer) no ClientboundContainerClose is echoed back.
	t.closeOpenContainer(p)
	_ = pkt
}
