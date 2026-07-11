package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// entity_track_fidelity_test.go covers the three ChunkMap.TrackedEntity fidelity ports:
//  1. per-type clientTrackingRange + updateInterval lookup (entity_track_range.go),
//  2. the leash ClientboundSetEntityLink pairing/attach/detach (leash.go, tracker sendPairingData),
//  3. the all-slot mob equipment change re-broadcast (detectMobEquipmentUpdates for an ARMOR slot).

// TestTrackParamsMatchJar asserts the per-type (clientTrackingRange, updateInterval) lookup returns
// the exact values parsed from the EntityTypes static register chains in the 26.2 jar.
func TestTrackParamsMatchJar(t *testing.T) {
	cases := []struct {
		typ   entity.ID
		name  string
		wantR int
		wantI int
	}{
		{entity.Pig.ID, "pig", 10, 3},
		{entity.Cow.ID, "cow", 10, 3},
		{entity.Skeleton.ID, "skeleton", 8, 3},
		{entity.Allay.ID, "allay", 8, 2},
		{entity.Item.ID, "item", 6, 20},
		{entity.ExperienceOrb.ID, "experience_orb", 6, 20},
		{entity.Arrow.ID, "arrow", 4, 20},
		{entity.Snowball.ID, "snowball", 4, 10},
		{entity.FallingBlock.ID, "falling_block", 10, 20},
		{entity.Painting.ID, "painting", 10, trackUpdateNever},
		{entity.ItemFrame.ID, "item_frame", 10, trackUpdateNever},
		{entity.LightningBolt.ID, "lightning_bolt", 16, trackUpdateNever},
		{entity.EndCrystal.ID, "end_crystal", 16, trackUpdateNever},
		{entity.Warden.ID, "warden", 16, 3},
		{entity.Marker.ID, "marker", 0, 3},
		{entity.Player.ID, "player", 32, 2},
		{entity.BlockDisplay.ID, "block_display", 10, 1},
	}
	for _, c := range cases {
		if got := entityTrackRangeChunks(c.typ); got != c.wantR {
			t.Errorf("%s clientTrackingRange = %d, want %d (jar)", c.name, got, c.wantR)
		}
		if got := entityUpdateInterval(c.typ); got != c.wantI {
			t.Errorf("%s updateInterval = %d, want %d (jar)", c.name, got, c.wantI)
		}
	}
}

// TestEntityInTrackRangeGate exercises the updatePlayer distance gate.
func TestEntityInTrackRangeGate(t *testing.T) {
	if !entityInTrackRange(100, 0, 0, 0, entity.Pig.ID, 10) {
		t.Fatalf("pig at 100 blocks with viewDist 10 (160 block cap) must be in range")
	}
	if entityInTrackRange(200, 0, 0, 0, entity.Pig.ID, 10) {
		t.Fatalf("pig at 200 blocks (> 160 block range) must be OUT of range")
	}
	if entityInTrackRange(100, 0, 0, 0, entity.Player.ID, 4) {
		t.Fatalf("viewDist 4 (64 block cap) must clamp a range-32 type OUT at 100 blocks")
	}
	if entityInTrackRange(0, 0, 0, 0, entity.Marker.ID, 10) {
		t.Fatalf("a clientTrackingRange-0 marker must NEVER be tracked")
	}
}

// TestMovementGatedByUpdateInterval asserts sendEntityMovementChanges only emits a move packet on
// ticks where sendTickCount modulo updateInterval == 0 (the ServerEntity.sendChanges gate).
func TestMovementGatedByUpdateInterval(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)
	e := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 9.5, 64, 9.5)
	loop.only().entities.add(e)

	syncTrackerTick(loop)
	loop.tickEntityMovement()
	_ = drainPackets(p.client)

	moves := 0
	for i := 0; i < 6; i++ {
		loop.only().entities.move(e, e.x+0.05, 64, e.z)
		p.client = captureClient(64)
		loop.clientIndex[p.client] = p
		loop.tickEntityMovement()
		got := drainPackets(p.client)
		moves += countID(got, packetid.ClientboundMoveEntityPos) + countID(got, packetid.ClientboundMoveEntityPosRot)
	}
	if moves != 2 {
		t.Fatalf("pig (updateInterval 3) sent %d move packets over 6 move ticks, want 2", moves)
	}
}

// TestLeashedMobEmitsSetEntityLinkAtSpawn asserts a leashed entity emits ClientboundSetEntityLink at
// tracking-start (sendPairingData leash branch) and an un-leashed pig emits none.
func TestLeashedMobEmitsSetEntityLinkAtSpawn(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := newTrackerPlayer(loop, 1000, 8.5, 8.5)

	const holderID = 777
	leashed := NewEntity(loop.idAlloc.AllocID(), entity.Cow, 9.5, 64, 9.5)
	leashed.leashHolderID = holderID
	loop.only().entities.add(leashed)

	bare := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 9.5, 64, 9.5)
	loop.only().entities.add(bare)

	syncTrackerTick(loop)
	got := drainPackets(p.client)

	var links []pk.Packet
	for _, pkt := range got {
		if pkt.ID == int32(packetid.ClientboundSetEntityLink) {
			links = append(links, pkt)
		}
	}
	if len(links) != 1 {
		t.Fatalf("spawn emitted %d SetEntityLink, want exactly 1 (leashed cow only, not the bare pig)", len(links))
	}
	r := bytes.NewReader(links[0].Data)
	var src, dst pk.Int
	if _, err := src.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEntityLink source: %v", err)
	}
	if _, err := dst.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEntityLink dest: %v", err)
	}
	if int32(src) != leashed.id {
		t.Fatalf("SetEntityLink sourceId = %d, want %d", int32(src), leashed.id)
	}
	if int32(dst) != holderID {
		t.Fatalf("SetEntityLink destId = %d, want %d", int32(dst), holderID)
	}
}

// TestAttachAndDropLeashBroadcast asserts attach/detach set leashHolderID + broadcast the link.
func TestAttachAndDropLeashBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(loop.idAlloc.AllocID(), entity.Cow, 9.5, 64, 9.5)
	loop.only().entities.add(e)

	viewer := &tickPlayer{client: captureClient(64), entityID: 5000, tracked: map[int32]bool{e.id: true}}
	loop.players = append(loop.players, viewer)

	const holderID = 888
	loop.attachLeash(e, holderID)
	if e.leashHolderID != holderID {
		t.Fatalf("attachLeash: leashHolderID = %d, want %d", e.leashHolderID, holderID)
	}
	got := drainPackets(viewer.client)
	if n := countID(got, packetid.ClientboundSetEntityLink); n != 1 {
		t.Fatalf("attachLeash broadcast %d SetEntityLink, want 1", n)
	}
	for _, pkt := range got {
		if pkt.ID == int32(packetid.ClientboundSetEntityLink) {
			r := bytes.NewReader(pkt.Data)
			var src, dst pk.Int
			_, _ = src.ReadFrom(r)
			_, _ = dst.ReadFrom(r)
			if int32(dst) != holderID {
				t.Fatalf("attach dest = %d, want %d", int32(dst), holderID)
			}
		}
	}

	viewer.client = captureClient(64)
	loop.clientIndex = map[*Client]*tickPlayer{viewer.client: viewer}
	loop.dropLeash(e)
	if e.leashHolderID != 0 {
		t.Fatalf("dropLeash: leashHolderID = %d, want 0", e.leashHolderID)
	}
	got2 := drainPackets(viewer.client)
	if n := countID(got2, packetid.ClientboundSetEntityLink); n != 1 {
		t.Fatalf("dropLeash broadcast %d SetEntityLink, want 1", n)
	}
	for _, pkt := range got2 {
		if pkt.ID == int32(packetid.ClientboundSetEntityLink) {
			r := bytes.NewReader(pkt.Data)
			var src, dst pk.Int
			_, _ = src.ReadFrom(r)
			_, _ = dst.ReadFrom(r)
			if int32(dst) != 0 {
				t.Fatalf("detach dest = %d, want 0", int32(dst))
			}
		}
	}
}

// TestDetectEquipmentUpdatesArmorSlotBroadcasts asserts an armor (CHEST) change re-broadcasts a
// single-slot SetEquipment -- proving the mob equipment sync covers armor, not just MAINHAND.
func TestDetectEquipmentUpdatesArmorSlotBroadcasts(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, _ := lethalPigInRegion0(loop)
	mob.typ = entity.Zombie.ID

	mob.equipmentLastBroadcast[eqSlotChest] = component.SlotData{}
	mob.equipmentBroadcastInit = true
	mob.equipment[eqSlotChest] = component.SlotData{ItemID: pk.VarInt(item.IronChestplate.ID), Count: 1}

	viewer := &tickPlayer{client: captureClient(64), entityID: 6000, tracked: map[int32]bool{mob.id: true}}
	loop.players = append(loop.players, viewer)

	loop.detectMobEquipmentUpdates(mob)

	got := drainPackets(viewer.client)
	if n := countID(got, packetid.ClientboundSetEquipment); n != 1 {
		t.Fatalf("an armor (CHEST) change broadcast %d SetEquipment, want exactly 1", n)
	}
	for _, pkt := range got {
		if pkt.ID == int32(packetid.ClientboundSetEquipment) {
			r := bytes.NewReader(pkt.Data)
			var id pk.VarInt
			var slot pk.Byte
			_, _ = id.ReadFrom(r)
			_, _ = slot.ReadFrom(r)
			if byte(slot) != eqSlotChest {
				t.Fatalf("SetEquipment slot = %d, want CHEST %d", byte(slot), eqSlotChest)
			}
		}
	}
}
