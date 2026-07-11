package server

// enderman_carry_visible_test.go — MOB-HOST-08 visible-state tests (audit-flagged 1/1 expansion):
// the MAINHAND-SYNTHETIC held-item surface for an Enderman carrying a block.
//
//   - TestEndermanCarrySpawnsMainHandItem: an enderman with a carried STONE block (carriedBlockSet
//     true + carriedBlockState == stone default) — equipmentSpawnPackets emits ONE extra
//     SetEquipment for MAINHAND with stone-as-item (item.Stone.ID).
//   - TestEndermanNoCarryNoMainHandItem: an enderman WITHOUT a carried block (carriedBlockSet
//     false) — equipmentSpawnPackets emits NO MAINHAND SetEquipment (the synthetic-add gate is
//     closed, the slot array stays all-empty, the wire is the byte-identical default).
//   - TestPigNotEnderman: a pig (typ == entity.Pig.ID) has NO enderman carry path: equipmentSpawnPackets
//     emits ZERO packets (the spec's "a pig has NO enderman carry logic" assertion) AND
//     detectEndermanCarryUpdates on a pig is a documented defensive no-op (zero wire bytes, the
//     pig-oracle stream is byte-identical).
//   - TestDetectEndermanCarryUpdatesBroadcastsDelta: per-tick detectEndermanCarryUpdates diffs
//     the carried state and broadcasts one SetEquipment on a TAKE (false->true) and one on a
//     LEAVE (true->false), with the proper MAINHAND slot ordinal + the carried item (or EMPTY on
//     the leave). The first call SEEDS the snapshot WITHOUT broadcasting (the spawn-time
//     equipmentSpawnPackets is the authoritative initial wire).
//   - TestDetectEndermanCarryUpdatesInitSeedsNoBroadcast: the FIRST detectEndermanCarryUpdates
//     call (endermanCarryBroadcastInit == false) records the live state WITHOUT broadcasting —
//     mirrors detectMobEquipmentUpdates' equipmentBroadcastInit discipline.
//   - TestCarriedBlockItemResolvesKnownBlock / TestCarriedBlockItemEmptyWhenNotCarrying: the
//     pure-data lookups (no equipment_packet emits) — pin carriedBlockItem's 1:1 block.name ->
//     item.id map + the EMPTY stack on carriedBlockSet=false.
//
// Cite EnderMan.getCarriedBlock / setCarriedBlock (jar-verified above; 26.2-inner.jar).

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestEndermanCarrySpawnsMainHandItem: an enderman with a carried STONE block — equipmentSpawnPackets
// emits ONE extra single-slot SetEquipment for MAINHAND carrying item.Stone (Count 1, ItemID ==
// item.Stone.ID). The carried block rides as a SYNTHETIC held item; e.equipment[MAINHAND] stays
// EMPTY (the carried state never writes the slot array). Cite EnderMan.getCarriedBlock for the
// state read; enderman_carry_visible.go for the synthetic-add gate.
func TestEndermanCarrySpawnsMainHandItem(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if e.typ != entity.Enderman.ID {
		t.Fatalf("enderman typ = %d, want entity.Enderman.ID %d", e.typ, entity.Enderman.ID)
	}

	// Pre-warm the endermanCarryItemID cache so the under-test path skips the lazy first-call
	// build (the 1:1 look-up map is cached forever; an un-initialized build in this test would
	// still resolve STONE to item.Stone.ID but adds a one-time scan to a test-bound goroutine).
	_ = carriedBlockItemID(block.Stone{})

	// Hand-carry the enderman a STONE block (as if setEndermanCarriedBlock(t, e, stoneDefault, true)
	// had just been called). Skip the broadcast helper — the visible-state surface is the under-test
	// piece (we want a fresh, in-test carry state for the snapshot).
	stoneDefault := block.DefaultStateID["minecraft:stone"]
	e.carriedBlockState = stoneDefault
	e.carriedBlockSet = true

	// equipment[MAINHAND] is still EMPTY (the synthetic add never writes the slot array).
	if got := e.equipment[eqSlotMainHand]; got.Count != 0 {
		t.Fatalf("MAINHAND before equipmentSpawnPackets = %+v, want EMPTY (the carried state never writes the slot array)", got)
	}

	pkts := equipmentSpawnPackets(e)
	// Filter to ClientboundSetEquipment packets only.
	var setEquip []*pk.Packet
	for i := range pkts {
		if pkts[i].ID == int32(packetid.ClientboundSetEquipment) {
			setEquip = append(setEquip, &pkts[i])
		}
	}
	if len(setEquip) != 1 {
		t.Fatalf("a carrying enderman emitted %d SetEquipment packets, want exactly 1 (the MAINHAND stone); full set size: %d", len(setEquip), len(pkts))
	}
	p := setEquip[0]
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var slot pk.Byte
	if _, err := id.ReadFrom(r); err != nil {
		t.Fatalf("decode SetEquipment entity id: %v", err)
	}
	if int32(id) != e.id {
		t.Fatalf("SetEquipment entity id = %d, want %d", int32(id), e.id)
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
	if stack.Count != 1 || int32(stack.ItemID) != int32(item.Stone.ID) {
		t.Fatalf("SetEquipment MAINHAND ItemStack = %+v, want a single stone (id %d)", stack, item.Stone.ID)
	}
}

// TestEndermanNoCarryNoMainHandItem: an enderman WITHOUT a carried block — equipmentSpawnPackets
// emits ZERO SetEquipment packets (the synthetic-add gate is closed on carriedBlockSet=false).
// The enderman still has all-empty equipment slots, so the wire is byte-identical to a freshly-
// spawned enderman mid-prey. Cite EnderMan.getCarriedBlock returning null -> the EMPTY SlotData.
func TestEndermanNoCarryNoMainHandItem(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if e.typ != entity.Enderman.ID {
		t.Fatalf("enderman typ = %d, want entity.Enderman.ID %d", e.typ, entity.Enderman.ID)
	}
	if e.carriedBlockSet {
		t.Fatal("a freshly spawned enderman must not be carrying a block")
	}

	pkts := equipmentSpawnPackets(e)
	for i := range pkts {
		if pkts[i].ID == int32(packetid.ClientboundSetEquipment) {
			t.Fatalf("a not-carrying enderman emitted a SetEquipment packet; gate failed (index %d, len=%d)", i, len(pkts))
		}
	}
}

// TestPigNotEnderman: a pig (typ == entity.Pig.ID) — equipmentSpawnPackets emits ZERO packets
// (the all-empty slots path) AND detectEndermanCarryUpdates (called only inside the
// enderman-gated tickAI branch) would be a no-op for a pig. The synthetic-add gate in
// equipmentSpawnPackets checks e.typ == entity.Enderman.ID + carriedBlockSet, both false for
// a pig -> zero wire bytes. Cite the test as the "pig has NO enderman carry logic" assertion.
func TestPigNotEnderman(t *testing.T) {
	e := NewEntity(1, entity.Pig, 0, 0, 0)
	if e.carriedBlockSet {
		t.Fatal("a freshly spawned pig must not have carriedBlockSet (the data field defaults to false)")
	}
	if got := e.carriedBlockItem(); got.Count != 0 {
		t.Fatalf("a pig's carriedBlockItem() = %+v, want EMPTY (Count 0 — the pig has no carry state)", got)
	}
	pkts := equipmentSpawnPackets(e)
	if len(pkts) != 0 {
		t.Fatalf("a pig emitted %d equipment packets, want 0 (the synthetic-add gate is enderman-gated; the pig carries no carry state)", len(pkts))
	}
	// detectEndermanCarryUpdates on a non-enderman is the documented defensive short-circuit
	// (entity_equipment.go detectEndermanCarryUpdates guards on e.typ == entity.Enderman.ID).
	loop, _ := newN2Loop(t)
	viewer := &tickPlayer{client: captureClient(64), entityID: 1000, tracked: map[int32]bool{e.id: true}}
	loop.players = append(loop.players, viewer)
	loop.detectEndermanCarryUpdates(e)
	got := drainPackets(viewer.client)
	if len(got) != 0 {
		t.Fatalf("detectEndermanCarryUpdates on a pig emitted %d packets, want 0", len(got))
	}
}

// TestDetectEndermanCarryUpdatesBroadcastsDelta: a per-tick detectEndermanCarryUpdates call diffs
// the carried state against the snapshot (entity.go:1605 endermanCarryLastBroadcast +
// endermanCarryLastBroadcastSet + endermanCarryBroadcastInit) and broadcasts ONE SetEquipment on
// a diff. The first call SEEDS the snapshot WITHOUT broadcasting (mirroring
// detectMobEquipmentUpdates' equipmentBroadcastInit discipline). The test:
//
//  1. Pre-seeds the init flag to true (post-spawn re-entry)
//  2. Records an EMPTY carry state (snap = absent), then sets carriedBlockState to STONE +
//     carriedBlockSet=true -> broadcasts ONE SetEquipment with the stone MAINHAND item.
//  3. Clears carry (carriedBlockSet=false) -> broadcasts ONE SetEquipment with the EMPTY stack.
//  4. Re-runs with no further change -> broadcasts NOTHING.
//
// Cite EnderMan.setCarriedBlock (a TAKE writes carriedBlockSet=true + a BlockState; a LEAVE
// writes carriedBlockSet=false / null). The audit-flagged expansion is the MAINHAND
// SetEquipment emit, not the SetEntityData carriedBlockDataEntry — the latter is the
// jar-faithful path.
func TestDetectEndermanCarryUpdatesBroadcastsDelta(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if e.typ != entity.Enderman.ID {
		t.Fatalf("enderman typ = %d, want entity.Enderman.ID %d", e.typ, entity.Enderman.ID)
	}

	viewer := &tickPlayer{client: captureClient(64), entityID: 2000, tracked: map[int32]bool{e.id: true}}
	loop.players = append(loop.players, viewer)

	// Pre-seed the init flag (post-spawn) so the next diff broadcasts. Snapshot starts EMPTY.
	e.endermanCarryBroadcastInit = true
	e.endermanCarryLastBroadcastSet = false
	e.endermanCarryLastBroadcast = 0

	_ = carriedBlockItemID(block.Stone{})
	stoneDefault := block.DefaultStateID["minecraft:stone"]
	e.carriedBlockState = stoneDefault
	e.carriedBlockSet = true

	loop.detectEndermanCarryUpdates(e)

	got := drainPackets(viewer.client)
	setEquip := filterSetEquip(got)
	if len(setEquip) != 1 {
		t.Fatalf("detectEndermanCarryUpdates emitted %d SetEquipment packets on TAKE, want 1", len(setEquip))
	}
	if err := assertMainHandStack(setEquip[0], e.id, uint32(item.Stone.ID)); err != nil {
		t.Fatalf("TAKE MAINHAND: %v", err)
	}

	// Pre-seed a fresh viewer so the next LEAVE broadcast routes somewhere observable.
	viewer2 := &tickPlayer{client: captureClient(64), entityID: 2001, tracked: map[int32]bool{e.id: true}}
	loop.players = append(loop.players, viewer2)

	// LEAVE: clear the carry (setCarriedBlock(null) in the jar).
	e.carriedBlockSet = false
	e.carriedBlockState = 0

	loop.detectEndermanCarryUpdates(e)
	gotLeave := drainPackets(viewer2.client)
	setEquipLeave := filterSetEquip(gotLeave)
	if len(setEquipLeave) != 1 {
		t.Fatalf("detectEndermanCarryUpdates emitted %d SetEquipment packets on LEAVE, want 1", len(setEquipLeave))
	}
	// LEAVE wire: a SetEquipment for MAINHAND with the EMPTY ItemStack (Count 0) so the client
	// clears the held-item slot. Cite EnderMan.setCarriedBlock(null) -> the carried state clears,
	// the synthetic add commits an EMPTY stack.
	r := bytes.NewReader(setEquipLeave[0].Data)
	var id pk.VarInt
	var slot pk.Byte
	if _, err := id.ReadFrom(r); err != nil {
		t.Fatalf("decode LEAVE SetEquipment entity id: %v", err)
	}
	if int32(id) != e.id {
		t.Fatalf("LEAVE SetEquipment entity id = %d, want %d", int32(id), e.id)
	}
	if _, err := slot.ReadFrom(r); err != nil {
		t.Fatalf("decode LEAVE SetEquipment slot: %v", err)
	}
	if byte(slot) != eqSlotMainHand {
		t.Fatalf("LEAVE SetEquipment slot = %d, want MAINHAND %d", byte(slot), eqSlotMainHand)
	}
	var stack component.SlotData
	if _, err := stack.ReadFrom(r); err != nil {
		t.Fatalf("decode LEAVE SetEquipment ItemStack: %v", err)
	}
	if stack.Count != 0 {
		t.Fatalf("LEAVE SetEquipment ItemStack = %+v, want EMPTY (Count 0)", stack)
	}

	// 3rd call: no further change -> no broadcast.
	viewer3 := &tickPlayer{client: captureClient(64), entityID: 2002, tracked: map[int32]bool{e.id: true}}
	loop.players = append(loop.players, viewer3)
	loop.detectEndermanCarryUpdates(e)
	if got := drainPackets(viewer3.client); len(got) != 0 {
		t.Fatalf("detectEndermanCarryUpdates emitted %d SetEquipment packets on a no-diff tick, want 0", len(got))
	}
}

// TestDetectEndermanCarryUpdatesInitSeedsNoBroadcast: the FIRST call (endermanCarryBroadcastInit
// == false) SEEDS the snapshot WITHOUT broadcasting — the spawn-time equipmentSpawnPackets is the
// authoritative initial wire. Without this gate a freshly-spawned carrying enderman would receive
// its MAINHAND packet TWICE: once from equipmentSpawnPackets, once from the init seed.
func TestDetectEndermanCarryUpdatesInitSeedsNoBroadcast(t *testing.T) {
	loop, floorY := endermanLoop(t)
	decl := loop.mobRegistry.byName["vanilla_enderman"]
	e := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	if e.typ != entity.Enderman.ID {
		t.Fatalf("enderman typ = %d, want entity.Enderman.ID %d", e.typ, entity.Enderman.ID)
	}

	// Fresh-spawn condition: not carrying, init flag false. Then hand-carry a stone BEFORE the
	// first detect call — simulates a state where the spawn-time equipmentSpawnPackets would
	// already have broadcast the MAINHAND stone; the FIRST detect call must NOT re-broadcast.
	_ = carriedBlockItemID(block.Stone{})
	stoneDefault := block.DefaultStateID["minecraft:stone"]
	e.carriedBlockSet = true
	e.carriedBlockState = stoneDefault

	viewer := &tickPlayer{client: captureClient(64), entityID: 3000, tracked: map[int32]bool{e.id: true}}
	loop.players = append(loop.players, viewer)

	loop.detectEndermanCarryUpdates(e)
	if got := drainPackets(viewer.client); len(got) != 0 {
		t.Fatalf("the first detectEndermanCarryUpdates call emitted %d packets, want 0 (the init seed is silent)", len(got))
	}
	if !e.endermanCarryBroadcastInit {
		t.Fatal("the first call did not flip endermanCarryBroadcastInit to true")
	}
	if !e.endermanCarryLastBroadcastSet || e.endermanCarryLastBroadcast != stoneDefault {
		t.Fatalf("init seed wrote (set=%v, sid=%d), want (set=%v, sid=%d)",
			e.endermanCarryLastBroadcastSet, e.endermanCarryLastBroadcast, true, stoneDefault)
	}
}

// TestCarriedBlockItemResolvesKnownBlock: the pure-data lookup — a carrying enderman with a STONE
// state id resolves its carriedBlockItem() to {Count: 1, ItemID: item.Stone.ID} (the 1:1
// block.name == item.name default map). Cite EnderMan.getCarriedBlock + the 1:1 fallback.
func TestCarriedBlockItemResolvesKnownBlock(t *testing.T) {
	e := NewEntity(1, entity.Enderman, 0, 0, 0)
	stoneDefault := block.DefaultStateID["minecraft:stone"]
	_ = carriedBlockItemID(block.Stone{})
	e.carriedBlockState = stoneDefault
	e.carriedBlockSet = true
	got := e.carriedBlockItem()
	if got.Count != 1 || int32(got.ItemID) != int32(item.Stone.ID) {
		t.Fatalf("carriedBlockItem(stone) = %+v, want {Count 1, ItemID %d}", got, item.Stone.ID)
	}
}

// TestCarriedBlockItemEmptyWhenNotCarrying: carriedBlockItem returns the EMPTY stack (Count 0)
// when carriedBlockSet=false — even if e.equipment[MAINHAND] is populated (the carried item is
// independent of the equipment layer; a non-carrying enderman MAY carry a real held item in v1
// future, but the carry state itself is empty). Cite EnderMan.getCarriedBlock returning null.
func TestCarriedBlockItemEmptyWhenNotCarrying(t *testing.T) {
	e := NewEntity(1, entity.Enderman, 0, 0, 0)
	e.carriedBlockSet = false
	e.carriedBlockState = 0
	if got := e.carriedBlockItem(); got.Count != 0 {
		t.Fatalf("a not-carrying enderman carriedBlockItem() = %+v, want EMPTY (Count 0)", got)
	}
}

// --- shared filter / decode helpers (local to this file) --------------------------------------

// filterSetEquip narrows ps to ClientboundSetEquipment packets (the visible-state wire).
func filterSetEquip(ps []pk.Packet) []*pk.Packet {
	var out []*pk.Packet
	for i := range ps {
		if ps[i].ID == int32(packetid.ClientboundSetEquipment) {
			out = append(out, &ps[i])
		}
	}
	return out
}

// assertMainHandStack decodes p as a SetEquipment with MAINHAND slot ordinal, entity id = eID, and
// the ItemStack carrying expectedItem at Count 1. Returns an error string on any mismatch.
func assertMainHandStack(p *pk.Packet, eID int32, expectedItem uint32) error {
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var slot pk.Byte
	if _, err := id.ReadFrom(r); err != nil {
		return err
	}
	if int32(id) != eID {
		return fmt.Errorf("entity id = %d, want %d", int32(id), eID)
	}
	if _, err := slot.ReadFrom(r); err != nil {
		return err
	}
	if byte(slot) != eqSlotMainHand {
		return fmt.Errorf("slot = %d, want MAINHAND %d", byte(slot), eqSlotMainHand)
	}
	var stack component.SlotData
	if _, err := stack.ReadFrom(r); err != nil {
		return err
	}
	if stack.Count != 1 || int32(stack.ItemID) != int32(expectedItem) {
		return fmt.Errorf("ItemID = %d (Count %d), want ItemID %d (Count 1)", int32(stack.ItemID), stack.Count, expectedItem)
	}
	return nil
}
