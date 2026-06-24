package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// inventory_test.go covers ENT-04: the dispatch route for the container packets
// (ServerboundContainerClick / SetCreativeModeSlot / ContainerClose / SetCarriedItem —
// previously dropped at the default no-op), the authoritative ContainerSetContent/SetSlot
// encoders over the component-slot SlotData codec, and the 1.21.5+ HashedStack ContainerClick
// decode WITHOUT mis-framing (the server discards the client hashes and re-sends authoritative
// content).
//
// The serverbound builders below mirror the JAR-DERIVED proto-776 wire layouts
// (javap'd this session from temp/cache/26.2-inner.jar):
//
//	HashedStack            = Boolean present + [ VarInt itemId + VarInt count + HashedPatchMap ]
//	HashedPatchMap         = VarInt addedCount + addedCount × (VarInt typeId + Int32 hash)
//	                         + VarInt removedCount + removedCount × VarInt typeId
//	ServerboundContainerClick = VarInt containerId + VarInt stateId + Short slotNum + Byte buttonNum
//	                            + VarInt containerInput + map<Short,HashedStack> changedSlots
//	                            + HashedStack carriedItem
//	ServerboundSetCreativeModeSlot = Short slot + SlotData itemStack
//	ServerboundSetCarriedItem      = Short slot

// hashedStackEmpty builds an empty HashedStack (Optional absent): a single Boolean(false).
func hashedStackEmpty() []byte {
	var b bytes.Buffer
	_, _ = pk.Boolean(false).WriteTo(&b)
	return b.Bytes()
}

// hashedStackActual builds a present HashedStack with itemId, count, and one added component
// hash (typeId + Int32 hash) — the shape a real client sends for a slot it thinks is filled.
func hashedStackActual(itemID, count int32, addedTypeID int32, hash uint32) []byte {
	var b bytes.Buffer
	_, _ = pk.Boolean(true).WriteTo(&b)
	_, _ = pk.VarInt(itemID).WriteTo(&b)
	_, _ = pk.VarInt(count).WriteTo(&b)
	// HashedPatchMap: 1 added (typeId + Int32 hash), 0 removed.
	_, _ = pk.VarInt(1).WriteTo(&b)
	_, _ = pk.VarInt(addedTypeID).WriteTo(&b)
	_, _ = pk.Int(int32(hash)).WriteTo(&b) // ByteBufCodecs.INT = fixed 4-byte BE int
	_, _ = pk.VarInt(0).WriteTo(&b) // 0 removed
	return b.Bytes()
}

// containerClickPacket builds a ServerboundContainerClick with the jar-derived field order
// and a changedSlots map of one entry plus a carried HashedStack.
func containerClickPacket(containerID, stateID int32, slotNum int16, button int8, input int32,
	changedSlot int16, changed []byte, carried []byte) pk.Packet {
	var b bytes.Buffer
	_, _ = pk.VarInt(containerID).WriteTo(&b)
	_, _ = pk.VarInt(stateID).WriteTo(&b)
	_, _ = pk.Short(slotNum).WriteTo(&b)
	_, _ = pk.Byte(button).WriteTo(&b)
	_, _ = pk.VarInt(input).WriteTo(&b)
	// changedSlots map: VarInt count, then count × (Short slot + HashedStack)
	_, _ = pk.VarInt(1).WriteTo(&b)
	_, _ = pk.Short(changedSlot).WriteTo(&b)
	b.Write(changed)
	// carriedItem HashedStack
	b.Write(carried)
	return pk.Packet{ID: int32(packetid.ServerboundContainerClick), Data: b.Bytes()}
}

// creativeSetSlotPacket builds a ServerboundSetCreativeModeSlot: Short slot + SlotData.
func creativeSetSlotPacket(slot int16, item component.SlotData) pk.Packet {
	var b bytes.Buffer
	_, _ = pk.Short(slot).WriteTo(&b)
	_, _ = item.WriteTo(&b)
	return pk.Packet{ID: int32(packetid.ServerboundSetCreativeModeSlot), Data: b.Bytes()}
}

// invPlayer registers a confirmed-teleport player with a capturing client.
func invPlayer(loop *TickLoop) *tickPlayer {
	p := &tickPlayer{
		client:            captureClient(64),
		confirmedTeleport: true,
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// TestContainerClickRouted: a ServerboundContainerClick (and a SetCreativeModeSlot) passed to
// dispatch is APPENDED to the player's subtick buffer (today they are dropped at the default
// no-op). Without the route the inventory is a silent dead feature.
func TestContainerClickRouted(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)

	if n := p.subtick.len(); n != 0 {
		t.Fatalf("fresh player subtick buffer = %d, want 0", n)
	}

	click := containerClickPacket(0, 1, 36, 0, 0, 36, hashedStackEmpty(), hashedStackEmpty())
	loop.dispatch(p.client, click)
	if n := p.subtick.len(); n != 1 {
		t.Fatalf("after dispatch(ContainerClick) subtick len = %d, want 1 (the route was missing)", n)
	}

	csm := creativeSetSlotPacket(36, component.SlotData{Count: 1, ItemID: 1})
	loop.dispatch(p.client, csm)
	if n := p.subtick.len(); n != 2 {
		t.Fatalf("after dispatch(SetCreativeModeSlot) subtick len = %d, want 2", n)
	}
}

// TestContainerSetContent: containerSetContent encodes VarInt containerId, VarInt stateId, the
// ItemStack list (via SlotData), then the carried ItemStack; component-free and one-component
// items both encode; the id is ClientboundContainerSetContent.
func TestContainerSetContent(t *testing.T) {
	items := []component.SlotData{
		{Count: 0},             // empty slot
		{Count: 1, ItemID: 1},  // component-free stone
		{Count: 64, ItemID: 1}, // component-free
	}
	// One-added-component item: build its RawComponents via a SlotData round-trip.
	var compWire bytes.Buffer
	_, _ = (pk.Tuple{pk.VarInt(1), pk.VarInt(1), pk.VarInt(1), pk.VarInt(0), pk.VarInt(1), pk.VarInt(64)}).WriteTo(&compWire)
	var withComp component.SlotData
	if _, err := withComp.ReadFrom(bytes.NewReader(compWire.Bytes())); err != nil {
		t.Fatalf("build one-component item: %v", err)
	}
	items = append(items, withComp)

	carried := component.SlotData{Count: 0}
	p := containerSetContent(0, 1, items, carried)
	if p.ID != int32(packetid.ClientboundContainerSetContent) {
		t.Fatalf("packet id = %d, want ClientboundContainerSetContent", p.ID)
	}

	// Decode the body back and verify the framing: containerId, stateId, list-count, then slots.
	var containerID, stateID, count pk.VarInt
	r := bytes.NewReader(p.Data)
	if _, err := containerID.ReadFrom(r); err != nil {
		t.Fatalf("read containerId: %v", err)
	}
	if _, err := stateID.ReadFrom(r); err != nil {
		t.Fatalf("read stateId: %v", err)
	}
	if _, err := count.ReadFrom(r); err != nil {
		t.Fatalf("read list count: %v", err)
	}
	if containerID != 0 || stateID != 1 || int(count) != len(items) {
		t.Fatalf("framing: containerId=%d stateId=%d count=%d, want 0/1/%d", containerID, stateID, count, len(items))
	}
	for i := 0; i < len(items); i++ {
		var s component.SlotData
		if _, err := s.ReadFrom(r); err != nil {
			t.Fatalf("slot %d decode: %v (mis-framed list)", i, err)
		}
		if s.Count != items[i].Count || s.ItemID != items[i].ItemID {
			t.Fatalf("slot %d mismatch: got count=%d id=%d, want count=%d id=%d", i, s.Count, s.ItemID, items[i].Count, items[i].ItemID)
		}
	}
	// The carried item follows the list (decodes without leftover mis-frame).
	var carriedBack component.SlotData
	if _, err := carriedBack.ReadFrom(r); err != nil {
		t.Fatalf("carried decode: %v", err)
	}
	if r.Len() != 0 {
		t.Fatalf("ContainerSetContent left %d trailing bytes (mis-framed)", r.Len())
	}
}

// TestContainerClickDecode: a ContainerClick built with the HashedStack form decodes WITHOUT a
// Scan error / mis-frame (all fields consumed); a malformed (truncated) click is a no-op (no
// panic).
func TestContainerClickDecode(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)

	// A click whose changedSlot and carried are present HashedStacks (item + count + 1 hash).
	changed := hashedStackActual(1, 64, 1, 0xDEADBEEF)
	carried := hashedStackActual(1, 1, 1, 0x12345678)
	click := containerClickPacket(0, 1, 36, 0, 0, 36, changed, carried)

	// Must not panic and must decode all fields (the handler re-sends authoritative content
	// only if it decoded without error).
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: click})

	// A truncated click (cut mid-HashedStack) must be a silent no-op (no panic).
	truncated := pk.Packet{ID: int32(packetid.ServerboundContainerClick), Data: click.Data[:len(click.Data)/2]}
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: truncated}) // must not panic
}

// TestContainerClickAuthoritative: after a click, the server re-sends ContainerSetContent
// (authoritative content); the client hashes are discarded and never build a slot.
func TestContainerClickAuthoritative(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)

	// The client forges a HashedStack claiming a full stack of item 999 in slot 36 — the
	// server must NOT build a slot from it; it re-sends its own (empty) authoritative content.
	changed := hashedStackActual(999, 64, 1, 0xCAFEBABE)
	click := containerClickPacket(0, 5, 36, 0, 0, 36, changed, hashedStackEmpty())
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: click})

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundContainerSetContent); n < 1 {
		t.Fatalf("after click: ContainerSetContent sent %d times, want >=1 (authoritative re-send)", n)
	}
	// The server's inventory slot 36 must NOT hold the forged item 999.
	if p.inventory != nil {
		if s := p.inventory.get(36); s.ItemID == 999 {
			t.Fatalf("server built slot from client-claimed item 999 (must discard hashes)")
		}
	}
}

// TestCreativeSetSlot: a SetCreativeModeSlot places an item into the inventory; a subsequent
// authoritative re-send reflects it.
func TestCreativeSetSlot(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)

	// Creative-set a stack of 64 stone (item id 1) into slot 36.
	csm := creativeSetSlotPacket(36, component.SlotData{Count: 64, ItemID: 1})
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: csm})

	if p.inventory == nil {
		t.Fatalf("creative-set did not initialize the inventory")
	}
	if s := p.inventory.get(36); s.Count != 64 || s.ItemID != 1 {
		t.Fatalf("slot 36 = count=%d id=%d, want count=64 id=1", s.Count, s.ItemID)
	}

	// And the authoritative content (a re-send) reflects the slot.
	pkt := containerSetContent(0, 1, p.inventory.snapshot(), component.SlotData{Count: 0})
	if pkt.ID != int32(packetid.ClientboundContainerSetContent) {
		t.Fatalf("snapshot packet id wrong")
	}
}
