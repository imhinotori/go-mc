package server

// map_packet.go -- the ClientboundMapItemDataPacket wire encoder + the per-carrier send drive. A 1:1 port
// of net.minecraft.network.protocol.game.ClientboundMapItemDataPacket over the 26.2 jar (javap this
// session). The packet id is packetid.ClientboundMapItemData (51, from the generated registry).
//
// WIRE LAYOUT (VERIFIED javap ClientboundMapItemDataPacket.STREAM_CODEC composite this session):
//   mapId       -- VarInt   (MapId.STREAM_CODEC)
//   scale       -- Byte     (ByteBufCodecs.BYTE)
//   locked      -- Bool     (ByteBufCodecs.BOOL)
//   decorations -- Optional<List<MapDecoration>>  (ByteBufCodecs.optional(list(MapDecoration.STREAM_CODEC)))
//                  encoded as: Bool present; if present VarInt count + each decoration. DEFERRED empty:
//                  we send Optional.empty (single Bool false, no decoration list) -- a map with no markers.
//   colorPatch  -- Optional<MapPatch>  (MapPatch.STREAM_CODEC): a single Byte `columns`; if columns > 0 then
//                  Byte rows, Byte startX, Byte startY, then a length-prefixed byte-array of colors (row-major
//                  columns*rows). columns == 0 means "no pixel update this packet". VERIFIED MapPatch.write:
//                  writeByte(width); writeByte(height); writeByte(startX); writeByte(startY); writeByteArray(mapColors).
//
// The MapPatch record calls its fields (startX,startY,width,height) but write() emits width,height,startX,startY
// -- so the wire order is width(=columns), height(=rows), startX, startY, colors. CITE: MapPatch.write.

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// encodeMapItemData builds a ClientboundMapItemDataPacket. When width==0 no color patch is sent (a
// decoration-only / keep-alive update); otherwise the width*height color rectangle at (startX,startY) is
// sent. Decorations are always empty (Optional.empty -> a single false byte). CITE:
// ClientboundMapItemDataPacket.STREAM_CODEC + MapPatch.write.
func encodeMapItemData(mapID int32, scale byte, locked bool, startX, startY, width, height byte, colors []byte) pk.Packet {
	fields := []pk.FieldEncoder{
		pk.VarInt(mapID),
		pk.Byte(scale),
		pk.Boolean(locked),
		// decorations Optional.empty: a single Bool false (no list follows). CITE: ByteBufCodecs.optional.
		pk.Boolean(false),
	}
	// colorPatch (MapPatch.write): if width == 0 write only the single columns byte (0); else the full patch.
	if width == 0 {
		fields = append(fields, pk.Byte(0))
	} else {
		fields = append(fields,
			pk.Byte(int8(width)),  // width (columns)
			pk.Byte(int8(height)), // height (rows)
			pk.Byte(int8(startX)), // startX
			pk.Byte(int8(startY)), // startY
			pk.ByteArray(colors),  // length-prefixed color bytes (FriendlyByteBuf.writeByteArray)
		)
	}
	return pk.Marshal(int32(packetid.ClientboundMapItemData), fields...)
}

// mapSendFull sends the ENTIRE 128x128 map to the holder (the initial getUpdatePacket after a map is first
// held / created): a full-image color patch. Used on empty-map creation so the client shows the map
// immediately. Clears the carrier dirty rectangle. CITE: MapItemSavedData.getUpdatePacket (full-map patch).
func (t *TickLoop) mapSendFull(p *tickPlayer, held component.SlotData) {
	data := t.mapGetSavedData(held)
	if data == nil || p.client == nil {
		return
	}
	mapID, ok := mapIDOf(held)
	if !ok {
		return
	}
	// Full patch is width=height=128 at (0,0); MapPatch stores colors COLUMN-major (col*height+row) while
	// data.colors is row-major (x + y*128), so repack. CITE: MapPatch.createPatch.
	colors := make([]byte, mapColorArraySize)
	for cx := 0; cx < mapImageSize; cx++ {
		for cz := 0; cz < mapImageSize; cz++ {
			colors[cx*mapImageSize+cz] = data.colors[cx+cz*mapImageSize]
		}
	}
	p.client.Send(encodeMapItemData(mapID, data.scale, data.locked, 0, 0, mapImageSize, mapImageSize, colors))
	// Clear this carrier's dirty rectangle (the full image was just sent).
	if hp := data.mapGetHoldingPlayer(p.entityID); hp != nil {
		hp.dirtyData = false
	}
}

// mapSendUpdate ports MapItemSavedData.HoldingPlayer.nextUpdatePacket -> createPatch: if this carrier has a
// dirty color rectangle, send only that sub-rectangle and clear the dirty flag. No dirty rectangle -> no
// packet. Decoration updates are DEFERRED (no markers). CITE: MapItemSavedData.HoldingPlayer.nextUpdatePacket.
func (t *TickLoop) mapSendUpdate(p *tickPlayer, held component.SlotData, data *mapItemSavedData) {
	if p.client == nil {
		return
	}
	hp := data.mapGetHoldingPlayer(p.entityID)
	if !hp.dirtyData {
		return
	}
	mapID, ok := mapIDOf(held)
	if !ok {
		return
	}
	hp.dirtyData = false

	// createPatch: startX = minDirtyX; startY = minDirtyY; width = maxDirtyX+1-minDirtyX; height = maxDirtyY+1-minDirtyY.
	startX := hp.minDirtyX
	startY := hp.minDirtyY
	width := hp.maxDirtyX + 1 - hp.minDirtyX
	height := hp.maxDirtyY + 1 - hp.minDirtyY
	if width <= 0 || height <= 0 {
		return
	}
	colors := make([]byte, width*height)
	for cx := 0; cx < width; cx++ {
		for cz := 0; cz < height; cz++ {
			colors[cx*height+cz] = data.colors[(startX+cx)+(startY+cz)*mapImageSize]
		}
	}
	p.client.Send(encodeMapItemData(mapID, data.scale, data.locked, byte(startX), byte(startY), byte(width), byte(height), colors))
}
