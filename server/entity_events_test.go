package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// entity_events_test.go gates the GAMEPLAY-07 visibility broadcasts: arm-swing (Animate) and
// held-item (SetEquipment) reach the players TRACKING the actor, and NOT the actor itself.

// makeSwingPacket builds a ServerboundSwing body (a single VarInt InteractionHand ordinal).
func makeSwingPacket(hand int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundSwing), pk.VarInt(hand))
}

// TestSwingBroadcastsToTrackersNotSelf: a swinging player's Animate goes to a player tracking
// it, but never echoes back to the swinger.
func TestSwingBroadcastsToTrackersNotSelf(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)

	// The observer can see the actor (tracker has spawned it).
	observer.tracked = map[int32]bool{actor.entityID: true}

	loop.handleSwing(actor, makeSwingPacket(0)) // main hand

	got := drainPackets(observer.client)
	if countID(got, packetid.ClientboundAnimate) != 1 {
		t.Fatalf("observer must receive exactly 1 Animate, got %d", countID(got, packetid.ClientboundAnimate))
	}
	self := drainPackets(actor.client)
	if countID(self, packetid.ClientboundAnimate) != 0 {
		t.Fatalf("the swinging player must NOT receive its own Animate, got %d", countID(self, packetid.ClientboundAnimate))
	}
}

// TestSwingActionByHand: main hand → action 0, off hand → action 3 (LivingEntity.swing).
func TestSwingActionByHand(t *testing.T) {
	for _, tc := range []struct {
		hand   int32
		action byte
	}{{0, 0}, {1, 3}} {
		loop := NewTickLoop(newFakeClock())
		actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
		observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)
		observer.tracked = map[int32]bool{actor.entityID: true}

		loop.handleSwing(actor, makeSwingPacket(tc.hand))

		got := drainPackets(observer.client)
		var found bool
		for _, p := range got {
			if p.ID != int32(packetid.ClientboundAnimate) {
				continue
			}
			var id pk.VarInt
			var act pk.UnsignedByte
			if err := p.Scan(&id, &act); err != nil {
				t.Fatalf("decode Animate: %v", err)
			}
			if int32(id) != actor.entityID {
				t.Fatalf("Animate entity id = %d, want %d", int32(id), actor.entityID)
			}
			if byte(act) != tc.action {
				t.Fatalf("hand %d: Animate action = %d, want %d", tc.hand, byte(act), tc.action)
			}
			found = true
		}
		if !found {
			t.Fatalf("hand %d: no Animate packet emitted", tc.hand)
		}
	}
}

// TestSwingUntrackedNoBroadcast: a player who does NOT track the actor receives nothing.
func TestSwingUntrackedNoBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	stranger := newTrackerPlayer(loop, 1001, 9.5, 8.5) // tracked map empty: cannot see actor

	loop.handleSwing(actor, makeSwingPacket(0))

	got := drainPackets(stranger.client)
	if countID(got, packetid.ClientboundAnimate) != 0 {
		t.Fatalf("a non-tracking player must receive no Animate, got %d", countID(got, packetid.ClientboundAnimate))
	}
}

// itemStack builds a minimal non-empty SlotData (count + item id, no extra components).
func itemStack(itemID, count int32) component.SlotData {
	return component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(itemID)}
}

// TestEquipmentBroadcastOnHeldItemChange: a change in the player's mainhand item, detected by
// tickEquipment, broadcasts SetEquipment to a tracking observer (and not to self). The FIRST
// tick seeds the snapshot without broadcasting (equipInit).
func TestEquipmentBroadcastOnHeldItemChange(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)
	observer.tracked = map[int32]bool{actor.entityID: true}

	// Give the actor an item in hotbar slot 0 (= menu slot 36) and select it.
	inv := ensureInventory(actor)
	inv.heldSlot = 0
	inv.set(int16(windowHotbarFirst), itemStack(1, 1)) // some item

	// First tickEquipment: seeds lastMainHand, no broadcast (equipInit gate). Then change the
	// held item and tick again. drainPackets CLOSES the queue, so drain ONCE at the end and
	// assert the cumulative result is exactly one SetEquipment (the change, not the seed).
	loop.tickEquipment()
	inv.set(int16(windowHotbarFirst), itemStack(2, 1))
	loop.tickEquipment()

	got := drainPackets(observer.client)
	if n := countID(got, packetid.ClientboundSetEquipment); n != 1 {
		t.Fatalf("observer must receive exactly 1 SetEquipment (the change, not the seed), got %d", n)
	}
	if n := countID(drainPackets(actor.client), packetid.ClientboundSetEquipment); n != 0 {
		t.Fatalf("the actor must NOT receive its own SetEquipment, got %d", n)
	}
}

// TestEquipmentNoBroadcastWhenUnchanged: a stable mainhand across ticks broadcasts nothing
// after the initial seed (detectEquipmentUpdates is a no-op when equipment matches).
func TestEquipmentNoBroadcastWhenUnchanged(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)
	observer.tracked = map[int32]bool{actor.entityID: true}

	inv := ensureInventory(actor)
	inv.set(int16(windowHotbarFirst), itemStack(1, 1))

	loop.tickEquipment() // seed
	loop.tickEquipment() // unchanged
	loop.tickEquipment() // unchanged
	if n := countID(drainPackets(observer.client), packetid.ClientboundSetEquipment); n != 0 {
		t.Fatalf("a stable mainhand must broadcast no SetEquipment, got %d", n)
	}
}

// TestEquipmentSlotIsMainHand: the broadcast SetEquipment carries the MAINHAND slot byte (0,
// no continuation bit) and the actor's entity id.
func TestEquipmentSlotIsMainHand(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)
	observer.tracked = map[int32]bool{actor.entityID: true}

	inv := ensureInventory(actor)
	loop.tickEquipment() // seed empty hand
	inv.set(int16(windowHotbarFirst), itemStack(5, 3))
	loop.tickEquipment()

	for _, p := range drainPackets(observer.client) {
		if p.ID != int32(packetid.ClientboundSetEquipment) {
			continue
		}
		var id pk.VarInt
		var slot pk.Byte
		if err := p.Scan(&id, &slot); err != nil {
			t.Fatalf("decode SetEquipment header: %v", err)
		}
		if int32(id) != actor.entityID {
			t.Fatalf("SetEquipment entity id = %d, want %d", int32(id), actor.entityID)
		}
		if byte(slot) != equipmentSlotMainHand {
			t.Fatalf("SetEquipment slot byte = %d, want %d (MAINHAND, no continuation bit)", byte(slot), equipmentSlotMainHand)
		}
		return
	}
	t.Fatal("no SetEquipment packet emitted after held-item change")
}
