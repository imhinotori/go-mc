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
	carried := component.SlotData{Count: 0} // v1: nothing on the cursor
	p.client.Send(containerSetContent(playerContainerID, inv.stateID, inv.snapshot(), carried))
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
// client-claimed item data, T-6-02), and re-sends the authoritative ContainerSetContent. A
// malformed/truncated click Scan-errors to a silent no-op (no mutation, no re-send) and never
// panics (T-6-04).
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

	// The click decoded cleanly. The server is AUTHORITATIVE: it ignored the client's claimed
	// slot contents (the hashes) entirely and re-sends its own inventory so the client's view
	// is corrected. v1 applies no survival slot mechanics (crafting/shift-click) — the wire is
	// the requirement.
	t.sendContent(p)
}

// handleSetCreativeModeSlot resolves a ServerboundSetCreativeModeSlot on-tick (ENT-04): a
// creative player sets a slot directly with a FULL component-slot ItemStack (server-bound).
// This is the simple path to a visible item. Decoded defensively; stored into the tick-owned
// inventory. Jar-derived: Short slot + ItemStack (SlotData).
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
	ensureInventory(p).set(int16(slot), item)
	// Echo the authoritative content back so the client renders the slot (sendContent bumps
	// the state id).
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

// handleContainerClose resolves a ServerboundContainerClose on-tick (ENT-04). v1 cleanup is a
// no-op (the player inventory window stays open conceptually); a later plan tears down any
// open container state. Defensive: never reads the (small) payload, never panics.
func (t *TickLoop) handleContainerClose(p *tickPlayer, pkt pk.Packet) {
	// No-op for v1. The packet carries a single VarInt containerId; nothing to do yet.
	_ = p
	_ = pkt
}
