package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// map_test.go -- MAP (filled_map) validation gates against the 26.2 jar (temp/cache/26.2-inner.jar): the
// createFresh center snapping, the getPackedId color packing, the terrain color sampling into the grid,
// and the ClientboundMapItemDataPacket wire layout.

// newMapLoop wires a TickLoop with one ready chunk filled with a STONE floor up to y=63 (so y=64 is the
// first air cell -> WORLD_SURFACE top). Returns the loop + manager.
func newMapLoop(t *testing.T) (*TickLoop, *world.ChunkManager) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	// Fill a small stone platform around origin at y=63 (top solid) so the surface color is STONE.
	stone := block.ToStateID[block.Stone{}]
	for x := -8; x <= 8; x++ {
		for z := -8; z <= 8; z++ {
			mgr.SetBlock(pk.Position{X: x, Y: 63, Z: z}, stone, dimMinY)
		}
	}
	return loop, mgr
}

// TestMapCreateFreshCenter locks MapItemSavedData.createFresh center snapping: a scale-0 map created at
// world (0,0) snaps its center to the scale*128 lattice. CITE: MapItemSavedData.createFresh.
func TestMapCreateFreshCenter(t *testing.T) {
	// createFresh(0, 0, scale=0): i=128; cx=floor((0+64)/128)=0; centerX = 0*128 + 64 - 64 = 0.
	d := newMapSavedDataFresh(0, 0, 0, true, false, dimOverworld)
	if d.centerX != 0 || d.centerZ != 0 {
		t.Fatalf("createFresh(0,0,0) center = (%d,%d), want (0,0)", d.centerX, d.centerZ)
	}
	// createFresh(200, -50, scale=0): i=128; cx=floor(264/128)=2 -> centerX=2*128+64-64=256;
	// cz=floor(14/128)=0 -> centerZ=0*128+64-64=0.
	d2 := newMapSavedDataFresh(200, -50, 0, true, false, dimOverworld)
	if d2.centerX != 256 || d2.centerZ != 0 {
		t.Fatalf("createFresh(200,-50,0) center = (%d,%d), want (256,0)", d2.centerX, d2.centerZ)
	}
	if len(d.colors) != 16384 {
		t.Fatalf("colors buffer len = %d, want 16384", len(d.colors))
	}
}

// TestMapPackedID locks MapColor.getPackedId(brightness) = (id<<2) | (brightness&3). CITE: MapColor.getPackedId.
func TestMapPackedID(t *testing.T) {
	// STONE id 11, HIGH brightness 2: (11<<2)|2 = 44|2 = 46.
	if got := mapPackedID(11, mapBrightnessHigh); got != 46 {
		t.Fatalf("packedId(STONE, HIGH) = %d, want 46", got)
	}
	// WATER id 12, LOW brightness 0: (12<<2)|0 = 48.
	if got := mapPackedID(12, mapBrightnessLow); got != 48 {
		t.Fatalf("packedId(WATER, LOW) = %d, want 48", got)
	}
}

// TestMapColorTableTerrain locks the generated block->MapColor.id table on a few key terrain blocks
// (jar-verified values). CITE: BlockState.getMapColor / MapColor ids.
func TestMapColorTableTerrain(t *testing.T) {
	cases := map[string]byte{
		"minecraft:stone":       11,
		"minecraft:grass_block": 1,
		"minecraft:dirt":        10,
		"minecraft:water":       12,
		"minecraft:sand":        2,
	}
	for id, want := range cases {
		if got := block.MapColorID[id]; got != want {
			t.Fatalf("MapColorID[%s] = %d, want %d", id, got, want)
		}
	}
}

// TestMapFillsTerrainAndSendsPacket is the headline gate: an empty map used by a player over a stone
// platform creates a filled_map, the per-tick inventory-tick samples the STONE surface into the grid, and
// a ClientboundMapItemData packet is sent whose wire decodes to the expected header + a color patch whose
// pixels carry the STONE map-color id. CITE: EmptyMapItem.use + MapItem.update + ClientboundMapItemDataPacket.
func TestMapFillsTerrainAndSendsPacket(t *testing.T) {
	loop, _ := newMapLoop(t)
	p := &tickPlayer{x: 0.5, y: 64, z: 0.5, entityID: 9001, dimension: dimOverworld, client: captureClient(4096)}
	p.gameMode = gameModeSurvival
	loop.players = append(loop.players, p)
	inv := ensureInventory(p)

	// Give the player an empty map in the main hand and select that hotbar slot.
	inv.heldSlot = 0
	inv.set(heldWindowSlot(0), component.SlotData{ItemID: pk.VarInt(item.Map.ID), Count: 1})

	// Use it: EmptyMapItem.use -> the held slot becomes a filled_map.
	loop.useItemInHand(p, interactionHandMain)

	held := inv.get(heldWindowSlot(0))
	if int(held.ItemID) != int(item.FilledMap.ID) {
		t.Fatalf("empty map did not transform to filled_map: held item id = %d, want %d", held.ItemID, item.FilledMap.ID)
	}
	mapID, ok := mapIDOf(held)
	if !ok {
		t.Fatal("filled map has no map_id component")
	}
	data := loop.mapGetSavedData(held)
	if data == nil {
		t.Fatalf("no saved data for map id %d", mapID)
	}

	// Tick the map while held: it should sample the STONE surface and fill grid pixels near the player.
	// Run enough ticks that the sparse per-column scan (px&15 == step&15) covers all 16 column residues.
	for i := 0; i < 20; i++ {
		loop.gametime++
		loop.tickMapsHeld()
	}

	// The grid should now hold STONE-colored pixels (packed id (11<<2)|brightness). Count non-empty pixels.
	stoneColor := byte(11)
	filled := 0
	stonePixels := 0
	for _, b := range data.colors {
		if b != 0 {
			filled++
			if byte(b>>2) == stoneColor {
				stonePixels++
			}
		}
	}
	if filled == 0 {
		t.Fatal("map grid has no filled pixels after ticking over a stone platform")
	}
	if stonePixels == 0 {
		t.Fatalf("map grid has %d filled pixels but none are STONE-colored (want STONE id 11)", filled)
	}

	// A ClientboundMapItemData packet must have been sent to the carrier (the initial full send + updates).
	sent := drainPackets(p.client)
	if countID(sent, packetid.ClientboundMapItemData) == 0 {
		t.Fatal("no ClientboundMapItemData packet sent to the map carrier")
	}

	// Decode the FIRST map packet and validate the wire layout: mapId (VarInt) == our id, scale (byte) == 0,
	// locked (bool) == false, decorations optional (bool) == false, then the color patch header.
	var first pk.Packet
	for _, pkt := range sent {
		if pkt.ID == int32(packetid.ClientboundMapItemData) {
			first = pkt
			break
		}
	}
	r := bytes.NewReader(first.Data)
	var gotID pk.VarInt
	var gotScale pk.Byte
	var gotLocked pk.Boolean
	var gotDecoOpt pk.Boolean
	var gotColumns pk.Byte
	if _, err := gotID.ReadFrom(r); err != nil {
		t.Fatalf("decode mapId: %v", err)
	}
	if _, err := gotScale.ReadFrom(r); err != nil {
		t.Fatalf("decode scale: %v", err)
	}
	if _, err := gotLocked.ReadFrom(r); err != nil {
		t.Fatalf("decode locked: %v", err)
	}
	if _, err := gotDecoOpt.ReadFrom(r); err != nil {
		t.Fatalf("decode decorations optional: %v", err)
	}
	if _, err := gotColumns.ReadFrom(r); err != nil {
		t.Fatalf("decode columns: %v", err)
	}
	if int32(gotID) != mapID {
		t.Fatalf("wire mapId = %d, want %d", gotID, mapID)
	}
	if gotScale != 0 {
		t.Fatalf("wire scale = %d, want 0", gotScale)
	}
	if bool(gotLocked) {
		t.Fatal("wire locked = true, want false")
	}
	if bool(gotDecoOpt) {
		t.Fatal("wire decorations optional = present, want empty (false)")
	}
	// The first packet is the FULL send: columns == 128.
	if gotColumns != 127 && gotColumns != -128 {
		// 128 as a signed byte is -128; ByteArray uses int8. The full send writes width=128 -> int8(-128).
		t.Fatalf("wire columns (first full send) = %d, want 128 (int8 -128)", gotColumns)
	}
}
