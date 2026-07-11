package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
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

// setEntityDataFlags scans a ClientboundSetEntityData packet and returns the
// DATA_LIVING_ENTITY_FLAGS byte (index 8, serializer 0) if present. The body is a sequence of
// (UByte index, VarInt serializerId, value...) ending in 0xFF; we only need to find index 8.
func setEntityDataFlags(t *testing.T, p pk.Packet) (int8, bool) {
	t.Helper()
	var id pk.VarInt
	r := bytes.NewReader(p.Data)
	if _, err := id.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEntityData id: %v", err)
	}
	for {
		var index pk.UnsignedByte
		if _, err := index.ReadFrom(r); err != nil {
			return 0, false
		}
		if index == 0xFF {
			return 0, false // EOF marker, flags not present
		}
		var ser pk.VarInt
		if _, err := ser.ReadFrom(r); err != nil {
			return 0, false
		}
		// Only the BYTE serializer (id 0) entries we emit here carry a single byte value.
		var b pk.Byte
		if _, err := b.ReadFrom(r); err != nil {
			return 0, false
		}
		if uint8(index) == dataLivingEntityFlagsIndex && int32(ser) == byteSerializerID {
			return int8(b), true
		}
	}
}

// TestUsingItemPoseBroadcast: starting to eat broadcasts DATA_LIVING_ENTITY_FLAGS with the
// IS_USING bit to a tracking observer; stopping clears it. Not echoed to the eater.
func TestUsingItemPoseBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)
	observer.tracked = map[int32]bool{actor.entityID: true}

	loop.broadcastUsingItem(actor, true, interactionHandMain)
	loop.broadcastUsingItem(actor, false, interactionHandMain)

	got := drainPackets(observer.client)
	var flagsSeen []int8
	for _, p := range got {
		if p.ID != int32(packetid.ClientboundSetEntityData) {
			continue
		}
		if f, ok := setEntityDataFlags(t, p); ok {
			flagsSeen = append(flagsSeen, f)
		} else {
			flagsSeen = append(flagsSeen, 0) // a flags entry that decoded to the clear (0) state
		}
	}
	if len(flagsSeen) != 2 {
		t.Fatalf("expected 2 SetEntityData (start+stop), got %d", len(flagsSeen))
	}
	if flagsSeen[0]&livingFlagUsingItem == 0 {
		t.Fatalf("start: IS_USING bit must be set, got 0x%02x", byte(flagsSeen[0]))
	}
	if flagsSeen[1] != 0 {
		t.Fatalf("stop: flags must clear to 0, got 0x%02x", byte(flagsSeen[1]))
	}

	if countID(drainPackets(actor.client), packetid.ClientboundSetEntityData) != 0 {
		t.Fatal("the eater must NOT receive its own using-pose metadata")
	}
}

// TestUsingItemPoseOffHandBit: starting with the OFF_HAND sets bit 0x02 alongside IS_USING.
func TestUsingItemPoseOffHandBit(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	actor := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	observer := newTrackerPlayer(loop, 1001, 9.5, 8.5)
	observer.tracked = map[int32]bool{actor.entityID: true}

	loop.broadcastUsingItem(actor, true, interactionHandOff)

	for _, p := range drainPackets(observer.client) {
		if p.ID != int32(packetid.ClientboundSetEntityData) {
			continue
		}
		f, ok := setEntityDataFlags(t, p)
		if !ok {
			t.Fatal("off-hand start must carry a flags entry")
		}
		if f&livingFlagUsingItem == 0 || f&livingFlagOffHandUse == 0 {
			t.Fatalf("off-hand using flags = 0x%02x, want IS_USING|OFFHAND", byte(f))
		}
		return
	}
	t.Fatal("no SetEntityData emitted for off-hand use")
}

// newMoveEntity registers a tracked entity + an observer that sees it, and seeds the entity's
// move base (one tickEntityMovement). Returns the entity, the observer, and the loop so a test
// can mutate the entity and tick again.
func newMoveEntity(t *testing.T) (*TickLoop, *Entity, *tickPlayer) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	observer := newTrackerPlayer(loop, 100000, 8.5, 8.5)
	e := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 9.5, 64, 9.5)
	loop.only().entities.add(e)
	observer.tracked = map[int32]bool{e.id: true}
	loop.tickEntityMovement() // seed moveInit (sendTickCount stays 0); no packet
	// Advance to a STEADY-STATE tick before the caller drives its move. Vanilla ServerEntity
	// .sendChanges runs its move body only when tickCount % updateInterval == 0, and ALSO sends a
	// forced position component when tickCount % 60 == 0 (var10) -- which is TRUE at tickCount 0,
	// the pig-interval-3 entity's first real send. To isolate the delta-TYPE selection (Pos vs Rot
	// vs PosRot vs nothing) from that first-tick forced-pos artifact, tick idle until sendTickCount
	// lands on a value that is a multiple of the pig interval (3) but NOT of 60 (sendTickCount 3):
	// tick 0 sends the forced pos, ticks 1/2 are gated out, tick 3 is a clean gate-passing tick with
	// no %60 forced pos. Each idle tick's packets are discarded.
	for e.sendTickCount%60 == 0 || e.sendTickCount%entityUpdateInterval(entity.Pig.ID) != 0 {
		loop.tickEntityMovement()
		_ = drainPackets(observer.client)
		observer.client = captureClient(64)
		loop.clientIndex[observer.client] = observer
	}
	observer.client = captureClient(64)
	loop.clientIndex[observer.client] = observer
	return loop, e, observer
}

// TestDeltaMoveSmallStepIsPos: a small position change (fits a short delta) sends MoveEntityPos,
// not an absolute sync.
func TestDeltaMoveSmallStepIsPos(t *testing.T) {
	loop, e, observer := newMoveEntity(t)
	loop.only().entities.move(e, e.x+1.0, e.y, e.z) // 1 block → 4096 units, well within short range
	loop.tickEntityMovement()
	got := drainPackets(observer.client)
	if countID(got, packetid.ClientboundMoveEntityPos) != 1 {
		t.Fatalf("small step: want 1 MoveEntityPos, got %d", countID(got, packetid.ClientboundMoveEntityPos))
	}
	if countID(got, packetid.ClientboundEntityPositionSync) != 0 {
		t.Fatalf("small step must not send EntityPositionSync, got %d", countID(got, packetid.ClientboundEntityPositionSync))
	}
}

// TestDeltaMoveBigJumpIsPositionSync: a jump larger than ±8 blocks overflows the short delta and
// must fall back to an absolute EntityPositionSync.
func TestDeltaMoveBigJumpIsPositionSync(t *testing.T) {
	loop, e, observer := newMoveEntity(t)
	loop.only().entities.move(e, e.x+10.0, e.y, e.z) // 10 blocks → 40960 units > 32767: overflow
	loop.tickEntityMovement()
	got := drainPackets(observer.client)
	if countID(got, packetid.ClientboundEntityPositionSync) != 1 {
		t.Fatalf("big jump: want 1 EntityPositionSync, got %d", countID(got, packetid.ClientboundEntityPositionSync))
	}
	if countID(got, packetid.ClientboundMoveEntityPos) != 0 {
		t.Fatalf("big jump must not send a delta MoveEntityPos, got %d", countID(got, packetid.ClientboundMoveEntityPos))
	}
}

// TestDeltaMoveRotationOnlyIsRot: a pure look-angle change (no position change) sends
// MoveEntityRot, not a Pos.
func TestDeltaMoveRotationOnlyIsRot(t *testing.T) {
	loop, e, observer := newMoveEntity(t)
	e.yaw += 45 // big enough that packDegrees differs by >= 1
	loop.tickEntityMovement()
	got := drainPackets(observer.client)
	if countID(got, packetid.ClientboundMoveEntityRot) != 1 {
		t.Fatalf("rotation only: want 1 MoveEntityRot, got %d", countID(got, packetid.ClientboundMoveEntityRot))
	}
	if countID(got, packetid.ClientboundMoveEntityPos) != 0 {
		t.Fatalf("rotation only must not send a Pos, got %d", countID(got, packetid.ClientboundMoveEntityPos))
	}
}

// TestDeltaMoveIdleSendsNothing: a stationary, unrotated entity (between the 60-tick re-anchor
// ticks) sends no move packet.
func TestDeltaMoveIdleSendsNothing(t *testing.T) {
	loop, _, observer := newMoveEntity(t)
	loop.tickEntityMovement() // no move, no rotation, teleportDelay=2 (not %60)
	got := drainPackets(observer.client)
	for _, want := range []packetid.ClientboundPacketID{
		packetid.ClientboundMoveEntityPos, packetid.ClientboundMoveEntityPosRot,
		packetid.ClientboundMoveEntityRot, packetid.ClientboundEntityPositionSync,
	} {
		if n := countID(got, want); n != 0 {
			t.Fatalf("idle entity sent %d of packet %d, want 0", n, int32(want))
		}
	}
}

// TestDeltaMovePosAndRotIsPosRot: a simultaneous position + rotation change sends a single
// MoveEntityPosRot (not separate Pos and Rot).
func TestDeltaMovePosAndRotIsPosRot(t *testing.T) {
	loop, e, observer := newMoveEntity(t)
	loop.only().entities.move(e, e.x+1.0, e.y, e.z)
	e.yaw += 45
	loop.tickEntityMovement()
	got := drainPackets(observer.client)
	if countID(got, packetid.ClientboundMoveEntityPosRot) != 1 {
		t.Fatalf("pos+rot: want 1 MoveEntityPosRot, got %d", countID(got, packetid.ClientboundMoveEntityPosRot))
	}
	if countID(got, packetid.ClientboundMoveEntityPos)+countID(got, packetid.ClientboundMoveEntityRot) != 0 {
		t.Fatal("pos+rot must be a single PosRot, not separate Pos/Rot")
	}
}
