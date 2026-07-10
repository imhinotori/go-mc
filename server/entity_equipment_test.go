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

// --- on-death dropEquipment tests (1:1 port of Mob.dropPreservedEquipment) ----------------

// dropEquipMob equips e with the supplied per-slot stack map (slot -> component.SlotData).
// Drops everything else; equips ONLY the listed slots. Test fixture.
func dropEquipMob(e *Entity, items map[int]component.SlotData) {
	for i := 0; i < equipmentSlotCount; i++ {
		e.equipment[i] = component.SlotData{}
	}
	for slot, st := range items {
		if slot >= 0 && slot < equipmentSlotCount {
			e.equipment[slot] = st
		}
	}
}

// TestDropEquipmentPerSlotRoll: a mob that DIES with non-empty MAINHAND + HEAD carries an
// equipment[MAINHAND] bow + equipment[HEAD] leather helmet. dropMobEquipment reads the region
// levelRandom and rolls `NextFloat() < slotDropChance(slot)` per slot. slotDropChance returns the
// per-slot equipmentDropChances override when it is > 0, else the 0.085f default. Every region is
// constructed with a live (nondeterministically-seeded) levelRandom (see newRegion), so the roll is
// ALWAYS taken. The outcome is made seed-independent here by forcing the per-slot chance to 1.0
// (NextFloat() is in [0,1) so `< 1.0` is unconditionally true). Both rolls PASS -> two Item entities
// spawn (a bow + a helmet), each with the vanilla per-slot damage roll applied, and both slots clear.
func TestDropEquipmentPerSlotRoll(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, owner := lethalPigInRegion0(loop)
	mob.typ = entity.Skeleton.ID // bow spawn path (also lets us run dropMobEquipment on a skeleton)
	// Equip MAINHAND with a fresh bow + HEAD with a fresh leather helmet (a non-damaged non-bow).
	bow := component.SlotData{ItemID: pk.VarInt(item.Bow.ID), Count: 1}
	helmet := component.SlotData{ItemID: pk.VarInt(item.LeatherHelmet.ID), Count: 1}
	dropEquipMob(mob, map[int]component.SlotData{eqSlotMainHand: bow, eqSlotHead: helmet})
	// Force BOTH per-slot rolls to PASS deterministically: chance 1.0 overrides the 0.085 default
	// (slotDropChance returns the override when > 0), and NextFloat() < 1.0 is always true. This
	// makes the drop outcome seed-independent -- the region nondeterministic levelRandom no longer
	// decides pass/fail, only the damage roll draws from it.
	mob.equipmentDropChances[eqSlotMainHand] = 1.0
	mob.equipmentDropChances[eqSlotHead] = 1.0

	// Build a tracker for the player so the spawned items + death are observed.
	viewer := &tickPlayer{client: captureClient(64), entityID: 1000, tracked: map[int32]bool{mob.id: true}}
	loop.players = append(loop.players, viewer)

	before := 0
	loop.withRegion(owner, func() { before = owner.entities.len() })
	src := damageSourcePlayerAttack(42)
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 100.0) }) // lethal

	if !mob.dead {
		t.Fatalf("mob not dead after lethal hit -- applyDamageEntity chain did not reach die()")
	}
	// Both rolls passed -> both slots CLEARED on the dead mob.
	if mob.getMainHandItem().Count != 0 {
		t.Fatalf("MAINHAND after death = %+v, want EMPTY (drop roll forced to pass)", mob.getMainHandItem())
	}
	if mob.getItemBySlot(eqSlotHead).Count != 0 {
		t.Fatalf("HEAD after death = %+v, want EMPTY (drop roll forced to pass)", mob.getItemBySlot(eqSlotHead))
	}
	// Two equipment Item entities were spawned into the owner region (plus any loot/xp the death path
	// adds); assert the bow + helmet drops landed as Item entity spawns.
	after := 0
	bows, helmets := 0, 0
	loop.withRegion(owner, func() {
		after = owner.entities.len()
		for _, e := range owner.entities.all() {
			if e == nil || e.typ != entity.Item.ID {
				continue
			}
			switch int32(e.itemStack.ItemID) {
			case int32(item.Bow.ID):
				bows++
			case int32(item.LeatherHelmet.ID):
				helmets++
			}
		}
	})
	if after <= before {
		t.Fatalf("region entity count did not grow after death drops: before=%d after=%d", before, after)
	}
	if bows != 1 || helmets != 1 {
		t.Fatalf("dropped equipment Item entities: bow=%d helmet=%d, want bow=1 helmet=1", bows, helmets)
	}
}

// TestDetectEquipmentUpdatesBroadcastsDelta: a mob's equipment changes mid-life. detectMobEquipmentUpdates
// diffs against the last-broadcast snapshot; on a non-empty-slot change it emits one ClientboundSetEquipment
// packet per CHANGED slot via broadcastToTrackers. The first call (equipmentBroadcastInit == false) SEEDS
// the snapshot WITHOUT broadcasting — the spawn-time equipmentSpawnPackets is authoritative.
//
// This test seeds the init manually, then changes MAINHAND, runs detectMobEquipmentUpdates once, and
// asserts exactly ONE ClientboundSetEquipment packet was emitted, with the new MAINHAND item on the wire.
func TestDetectEquipmentUpdatesBroadcastsDelta(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, _ := lethalPigInRegion0(loop)
	mob.typ = entity.Skeleton.ID

	// Pre-seed the last-broadcast snapshot (simulate the init having already run, so this is not the
	// first call): an EMPTY MAINHAND.
	mob.equipmentLastBroadcast[eqSlotMainHand] = component.SlotData{}
	mob.equipmentBroadcastInit = true

	// Now set MAINHAND to a bow.
	mob.equipment[eqSlotMainHand] = component.SlotData{ItemID: pk.VarInt(item.Bow.ID), Count: 1}

	// Set up a tracker so broadcastToTrackers routes the packet somewhere observable.
	viewer := &tickPlayer{client: captureClient(64), entityID: 2000, tracked: map[int32]bool{mob.id: true}}
	loop.players = append(loop.players, viewer)

	loop.detectMobEquipmentUpdates(mob)

	got := drainPackets(viewer.client)
	var setEquip []*pk.Packet
	for _, p := range got {
		if p.ID == int32(packetid.ClientboundSetEquipment) {
			setEquip = append(setEquip, &p)
		}
	}
	if len(setEquip) != 1 {
		t.Fatalf("detectMobEquipmentUpdates emitted %d SetEquipment packets, want exactly 1 (the MAINHAND bow)", len(setEquip))
	}
	// Decode + assert MAINHAND slot ordinal + bow.
	p := setEquip[0]
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var slot pk.Byte
	if _, err := id.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEquipment entity id: %v", err)
	}
	if int32(id) != mob.id {
		t.Fatalf("SetEquipment entity id = %d, want %d", int32(id), mob.id)
	}
	if _, err := slot.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEquipment slot: %v", err)
	}
	if byte(slot) != eqSlotMainHand {
		t.Fatalf("SetEquipment slot = %d, want MAINHAND %d", byte(slot), eqSlotMainHand)
	}
	var stack component.SlotData
	if _, err := stack.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEquipment ItemStack: %v", err)
	}
	if stack.Count != 1 || int32(stack.ItemID) != int32(item.Bow.ID) {
		t.Fatalf("SetEquipment ItemStack = %+v, want bow", stack)
	}
}

// TestDetectEquipmentUpdatesNoBroadcastWhenEqual: when the LIVE slot matches the last-broadcast
// snapshot, detectMobEquipmentUpdates emits ZERO SetEquipment packets — the byte-identical default
// the oracle relies on (the pig's slots are EMPTY, last-broadcast is the init-seed EMPTY, no diff).
func TestDetectEquipmentUpdatesNoBroadcastWhenEqual(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, _ := lethalPigInRegion0(loop)

	// First call: seeds the snapshot (no broadcast; init branch returns early when all slots EMPTY).
	loop.detectMobEquipmentUpdates(mob)
	if mob.equipmentBroadcastInit {
		t.Fatalf("equipmentBroadcastInit flipped to true on an all-EMPTY mob — init should stay silent")
	}

	// Tracker to observe any broadcasts (there should be none).
	viewer := &tickPlayer{client: captureClient(64), entityID: 3000, tracked: map[int32]bool{mob.id: true}}
	loop.players = append(loop.players, viewer)

	// Second call: still all EMPTY, no diff → no SetEquipment.
	loop.detectMobEquipmentUpdates(mob)
	if got := drainPackets(viewer.client); len(got) != 0 {
		t.Fatalf("detectMobEquipmentUpdates emitted %d packets on a no-diff tick, want 0", len(got))
	}
}
