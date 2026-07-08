package server

// map_use.go -- the filled_map LIFECYCLE: create-from-empty (EmptyMapItem.use), the per-tick sample-while-
// held drive (MapItem.inventoryTick), and the map-id / saved-data registry. A 1:1 port of the relevant
// net.minecraft.world.item.EmptyMapItem + MapItem + ServerLevel map methods over the 26.2 jar (javap this
// session). The color sampling lives in map_item.go; the pixel wire in map_packet.go.
//
// CITE (methods, jar-verified this session):
//   ServerLevel.getFreeMapId(): a monotonically increasing counter; setMapData(id, data) stores it.
//   MapItem.create(level, x, z, scale, tracking, unlimited): FILLED_MAP stack; set MAP_ID = createNewSavedData.
//   createNewSavedData: data = MapItemSavedData.createFresh(x, z, scale, tracking, unlimited, dim);
//       id = getFreeMapId(); setMapData(id, data); return id.
//   MapItem.inventoryTick(stack, level, entity, slot): data = getSavedData(stack, level); if (data==null) return;
//       tickCarriedBy(player, stack, null); if (!locked && slot.type == HAND) update(level, entity, data).
//   EmptyMapItem.use: held.consume(1); filled = create(level, blockX, blockZ, 0, true, false);
//       if (held.isEmpty()) heldItemTransformedTo(filled); else inventory.add(filled.copy())/drop.

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// compMapID is the minecraft:map_id data-component wire id (index 46 in the generated
// registryid.DataComponentType). CITE: DataComponents.MAP_ID (registry index 46).
const compMapID = 46

// mapDefaultScale is the scale of a fresh map from an empty map (MapItem.create scale arg 0). CITE:
// EmptyMapItem.use (create(..., 0, true, false)).
const mapDefaultScale = 0

// getFreeMapID ports ServerLevel.getFreeMapId(): allocate the next map id (monotonic counter). Tick-owned.
// CITE: ServerLevel.getFreeMapId.
func (t *TickLoop) getFreeMapID() int32 {
	id := t.nextMapID
	t.nextMapID++
	return id
}

// mapCreate ports MapItem.create(level, x, z, scale, tracking, unlimited): allocate a fresh
// MapItemSavedData snapped to (x,z), register it under a free map id, and return a FILLED_MAP SlotData
// carrying that map_id component. Tick-owned. CITE: MapItem.create + createNewSavedData.
func (t *TickLoop) mapCreate(x, z float64, scale byte, tracking, unlimited bool, dimension int) component.SlotData {
	if t.maps == nil {
		t.maps = make(map[int32]*mapItemSavedData)
	}
	data := newMapSavedDataFresh(x, z, scale, tracking, unlimited, dimension)
	id := t.getFreeMapID()
	t.maps[id] = data

	filled := component.SlotData{ItemID: pk.VarInt(item.FilledMap.ID), Count: 1}
	patch := component.DecodePatch(filled)
	patch.Set(compMapID, &component.MapID{VarInt: pk.VarInt(id)})
	return patch.ApplyTo(filled)
}

// mapIDOf reads the minecraft:map_id component off a filled_map stack, returning (id, true) or (0, false)
// when absent. CITE: ItemStack.get(DataComponents.MAP_ID).
func mapIDOf(s component.SlotData) (int32, bool) {
	if int(s.ItemID) != int(item.FilledMap.ID) {
		return 0, false
	}
	patch := component.DecodePatch(s)
	dc := patch.Get(compMapID)
	if dc == nil {
		return 0, false
	}
	if mid, ok := dc.(*component.MapID); ok {
		return int32(mid.VarInt), true
	}
	return 0, false
}

// mapGetSavedData ports MapItem.getSavedData(stack, level): resolve the MapItemSavedData a filled_map
// stack points at (via its map_id component), or nil when the stack is not a filled map / has no id / the
// id is unknown. Tick-owned. CITE: MapItem.getSavedData.
func (t *TickLoop) mapGetSavedData(s component.SlotData) *mapItemSavedData {
	id, ok := mapIDOf(s)
	if !ok || t.maps == nil {
		return nil
	}
	return t.maps[id]
}

// tryUseEmptyMap ports EmptyMapItem.use for the item-use gate: an empty map (minecraft:map) becomes a
// fresh filled_map centered on the player, the empty map shrinks by 1, and the filled map replaces the
// held slot (or is added to the inventory / dropped). Returns true when handled. Called from
// useItemInHand (item_use.go). CITE: EmptyMapItem.use.
func (t *TickLoop) tryUseEmptyMap(p *tickPlayer, inv *Inventory, held component.SlotData, hand int32) bool {
	if int(held.ItemID) != int(item.Map.ID) {
		return false
	}

	// held.consume(1, player): shrink the empty map by 1.
	slot := heldMenuSlot(p, hand)
	consumed := held
	consumed.Count = toVar(int(consumed.Count) - 1)
	if consumed.Count <= 0 {
		consumed = component.SlotData{Count: 0}
	}

	// filled = MapItem.create(level, floor(x), floor(z), 0, true, false).
	filled := t.mapCreate(float64(mthFloorD(p.x)), float64(mthFloorD(p.z)), mapDefaultScale, true, false, p.dimension)

	if slotIsEmpty(consumed) {
		// held.isEmpty() -> heldItemTransformedTo(filled): the held slot BECOMES the filled map.
		inv.set(slot, filled)
	} else {
		// else: consume writes the shrunk empty map back, then add the filled map to the inventory / drop.
		inv.set(slot, consumed)
		add := filled
		if !t.invAdd(inv, &add) {
			t.playerDrop(p, add, false)
		}
	}

	if p.client != nil {
		inv.incrementStateId()
		p.client.Send(containerSetSlot(playerContainerID, inv.stateID, slot, inv.get(slot)))
	}
	// Send the initial full map-data packet so the client shows the (blank) map immediately.
	t.mapSendFull(p, filled)
	return true
}

// mapInventoryTick ports MapItem.inventoryTick for a filled map a player holds IN HAND: resolve its saved
// data, register the player as a carrier (tickCarriedBy), and (when not locked) re-sample the terrain
// around the holder (update) then flush any dirty pixels to that player. Called from the per-tick player
// item pass. CITE: MapItem.inventoryTick.
func (t *TickLoop) mapInventoryTick(p *tickPlayer, held component.SlotData) {
	data := t.mapGetSavedData(held)
	if data == nil {
		return
	}
	// tickCarriedBy(player, stack, null): ensure the carrier cursor exists (decoration tracking DEFERRED).
	data.mapGetHoldingPlayer(p.entityID)

	if !data.locked {
		t.mapUpdate(p, data)
	}
	// nextUpdatePacket: flush the dirty color patch (if any) to this carrier.
	t.mapSendUpdate(p, held, data)
}

// registryidMapDecorationTypeSize keeps the map_decoration registry referenced so the packet encoder's
// decoration-type ids stay consistent with the generated registry (decorations are DEFERRED, so this is a
// compile-time anchor only). CITE: registryid.MapDecorationType.
var _ = registryid.MapDecorationType

// tickMapsHeld drives MapItem.inventoryTick for every player holding a filled_map in either hand: it
// samples the terrain around the holder into the held map and flushes dirty pixels to that carrier.
// The MAIN and OFF hands are both checked (a map is ticked when held in either hand; vanilla ticks the
// SelectionSlot/offhand HAND-type equipment). Tick-owned. CITE: MapItem.inventoryTick (EquipmentSlot.Type.HAND).
func (t *TickLoop) tickMapsHeld() {
	if len(t.maps) == 0 {
		return
	}
	for _, p := range t.players {
		if p == nil || p.dead || p.client == nil {
			continue
		}
		inv := ensureInventory(p)
		main := inv.get(heldWindowSlot(inv.heldSlot))
		if int(main.ItemID) == int(item.FilledMap.ID) {
			t.mapInventoryTick(p, main)
		}
		off := inv.get(offhandWindowSlot)
		if int(off.ItemID) == int(item.FilledMap.ID) {
			t.mapInventoryTick(p, off)
		}
	}
}
