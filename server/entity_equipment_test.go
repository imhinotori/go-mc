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

// entity_equipment_test.go — the mob EQUIPMENT layer proof (the held-item/armor slots, a 1:1 port of
// EntityEquipment / LivingEntity.equipment). It pins: (1) the EnumMap-backed get/set semantics
// (an un-populated slot reads back EMPTY), (2) the skeleton's spawn-time bow in MAINHAND
// (AbstractSkeleton.populateDefaultEquipmentSlots) so RangedBowAttackGoal.isHoldingBow is backed by a
// real item, (3) the spawn-time ClientboundSetEquipment wire (the client SEES the bow), and (4) that a
// no-equipment mob (the pig) carries an all-zero slot array and emits ZERO equipment packets — the
// byte-identical default the oracle relies on.

// TestEquipmentGetSetEmptyDefault: getItemBySlot on a fresh entity reads EMPTY (zero SlotData) for
// every slot; setItemSlot stores, getItemBySlot reads it back — the EntityEquipment get/set contract.
func TestEquipmentGetSetEmptyDefault(t *testing.T) {
	e := NewEntity(1, entity.Pig, 0, 0, 0)
	for slot := 0; slot < equipmentSlotCount; slot++ {
		if got := e.getItemBySlot(slot); got.Count != 0 {
			t.Fatalf("fresh slot %d = %+v, want EMPTY (Count 0) — EntityEquipment.get getOrDefault(EMPTY)", slot, got)
		}
	}
	bow := itemStackOf(item.Bow)
	e.setItemSlot(eqSlotMainHand, bow)
	if got := e.getItemBySlot(eqSlotMainHand); got.Count != 1 || int32(got.ItemID) != int32(item.Bow.ID) {
		t.Fatalf("MAINHAND after set = %+v, want bow (count 1, id %d)", got, item.Bow.ID)
	}
	// Every OTHER slot stays EMPTY — a set is a single-slot write (EnumMap.put), never a broadcast.
	if got := e.getItemBySlot(eqSlotOffHand); got.Count != 0 {
		t.Fatalf("OFFHAND after MAINHAND set = %+v, want still EMPTY", got)
	}
}

// TestSkeletonPopulatesBow: a skeleton spawned through the shared spawnDeclaredMob path carries a
// single BOW in MAINHAND (AbstractSkeleton.populateDefaultEquipmentSlots) and isHoldingItem(BOW) is
// true — so RangedBowAttackGoal.isHoldingBow() is backed by the real held item.
func TestSkeletonPopulatesBow(t *testing.T) {
	loop, floorY, _ := skeletonLoop(t)
	skel := spawnSkeleton(loop, 8.5, float64(floorY+1), 8.5)

	main := skel.getMainHandItem()
	if main.Count != 1 || int32(main.ItemID) != int32(item.Bow.ID) {
		t.Fatalf("skeleton MAINHAND = %+v, want a single bow (id %d) — populateSkeletonEquipment did not run", main, item.Bow.ID)
	}
	if !skel.isHoldingItem(int32(item.Bow.ID)) {
		t.Fatal("skeleton isHoldingItem(BOW) = false, want true (the RangedBowAttackGoal.isHoldingBow backing)")
	}
}

// TestSkeletonEquipmentSpawnPacket: equipmentSpawnPackets emits exactly ONE ClientboundSetEquipment for
// the skeleton (the MAINHAND bow), with slot byte MAINHAND (ordinal 0, no continuation) and the bow item
// on the wire — the packet a newly-tracking client needs to render the held bow.
func TestSkeletonEquipmentSpawnPacket(t *testing.T) {
	loop, floorY, _ := skeletonLoop(t)
	skel := spawnSkeleton(loop, 8.5, float64(floorY+1), 8.5)

	pkts := equipmentSpawnPackets(skel)
	if len(pkts) != 1 {
		t.Fatalf("skeleton equipmentSpawnPackets = %d, want 1 (the MAINHAND bow)", len(pkts))
	}
	p := pkts[0]
	if p.ID != int32(packetid.ClientboundSetEquipment) {
		t.Fatalf("packet id = %d, want ClientboundSetEquipment %d", p.ID, int32(packetid.ClientboundSetEquipment))
	}
	// Decode: VarInt entityId, Byte slot, then the ItemStack (OPTIONAL_STREAM_CODEC / SlotData).
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var slot pk.Byte
	if _, err := id.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEquipment entity id: %v", err)
	}
	if int32(id) != skel.id {
		t.Fatalf("SetEquipment entity id = %d, want skeleton %d", int32(id), skel.id)
	}
	if _, err := slot.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEquipment slot: %v", err)
	}
	if byte(slot) != eqSlotMainHand {
		t.Fatalf("SetEquipment slot byte = %d, want MAINHAND ordinal %d (no continuation bit)", byte(slot), eqSlotMainHand)
	}
	var stack component.SlotData
	if _, err := stack.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEquipment ItemStack: %v", err)
	}
	if stack.Count != 1 || int32(stack.ItemID) != int32(item.Bow.ID) {
		t.Fatalf("SetEquipment ItemStack = %+v, want a single bow (id %d)", stack, item.Bow.ID)
	}
}

// TestNoEquipmentMobEmitsNothing: a mob with no populated slots (a pig — populateSkeletonEquipment is
// skeleton-gated, so it never runs for a pig) carries an all-zero equipment array and equipmentSpawnPackets
// returns NO packets — zero wire bytes, the byte-identical default (the oracle pig is unperturbed).
func TestNoEquipmentMobEmitsNothing(t *testing.T) {
	e := NewEntity(2, entity.Pig, 0, 0, 0)
	for slot := 0; slot < equipmentSlotCount; slot++ {
		if e.getItemBySlot(slot).Count != 0 {
			t.Fatalf("pig slot %d is non-empty, want the all-zero default", slot)
		}
	}
	if pkts := equipmentSpawnPackets(e); len(pkts) != 0 {
		t.Fatalf("pig equipmentSpawnPackets = %d, want 0 (no equipment → no wire bytes)", len(pkts))
	}
	if e.isHoldingItem(int32(item.Bow.ID)) {
		t.Fatal("pig isHoldingItem(BOW) = true, want false (an empty-handed mob holds nothing)")
	}
}
