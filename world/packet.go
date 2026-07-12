package world

import (
	"bytes"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// WriteLevelChunkWithLight assembles a ClientboundLevelChunkWithLight packet
// (WORLD-02 full + WORLD-03 light). Top-level wire order is jar-verified:
//
//	Int x, Int z, chunkData, lightData
//
// where chunkData+lightData are exactly what level.Chunk.WriteTo emits (post
// Plan-04-01: the 3 CLIENT heightmaps, the section blob with the two shorts,
// block entities, then the light masks + arrays). The chunk body is reused
// wholesale — it is NOT re-implemented here; this only prepends Int x, Int z.
func WriteLevelChunkWithLight(cx, cz int32, ch *level.Chunk) (pk.Packet, error) {
	var body bytes.Buffer
	if _, err := pk.Int(cx).WriteTo(&body); err != nil {
		return pk.Packet{}, err
	}
	if _, err := pk.Int(cz).WriteTo(&body); err != nil {
		return pk.Packet{}, err
	}
	if _, err := ch.WriteTo(&body); err != nil {
		return pk.Packet{}, err
	}
	return pk.Packet{
		ID:   int32(packetid.ClientboundLevelChunkWithLight),
		Data: body.Bytes(),
	}, nil
}

// WriteLightUpdate assembles a ClientboundLightUpdate packet for one column's freshly-recomputed light —
// the incremental-relight push LevelChunk.setBlockState drives after a light-affecting edit (via
// getLightEngine().checkBlock + the ThreadedLevelLightEngine emitting ClientboundLightUpdatePacket to the
// column's trackers). Wire order is jar-verified (ClientboundLightUpdatePacket.write):
//
//	VarInt chunkX, VarInt chunkZ, <LightUpdatePacketData>
//
// where LightUpdatePacketData.write == the SAME skyMask/blockMask/emptySky/emptyBlock/skyArrays/blockArrays
// layout level.Chunk.WriteTo emits for the in-chunk light (level/chunk.go lightData.WriteTo). We reproduce
// that layout here from the per-section arrays (a nil section == "no data", its mask bit unset), using the
// identical mask sizing so the payload is byte-compatible with the chunk-embedded light. CITE:
// net.minecraft.network.protocol.game.ClientboundLightUpdatePacket.write +
// ClientboundLightUpdatePacketData.write.
func WriteLightUpdate(cl ColumnLight) pk.Packet {
	// Identical encoder to the in-chunk light (level.EncodeLightData): the same
	// minSectionY-1 base, secs+2 light sections (block section si -> bit si+1,
	// padding sections at bit 0 and bit secs+1 unset), per-layer isEmpty empty
	// masks (nil array => neither bit), and vanilla-trimmed BitSets. CITE:
	// ClientboundLightUpdatePacketData.write == ClientboundLevelChunkWithLight's
	// embedded light — they MUST be byte-identical.
	ld := level.EncodeLightData(cl.Sky, cl.Block)
	return pk.Marshal(int32(packetid.ClientboundLightUpdate),
		pk.VarInt(cl.Pos[0]), pk.VarInt(cl.Pos[1]),
		ld.SkyLightMask, ld.BlockLightMask, ld.EmptySkyLightMask, ld.EmptyBlockLightMask,
		pk.Array(ld.SkyLight), pk.Array(ld.BlockLight),
	)
}

// SetChunkCacheCenter tells the client which chunk is the streaming center.
// Fields: VarInt chunkX, VarInt chunkZ.
func SetChunkCacheCenter(cx, cz int32) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundSetChunkCacheCenter), pk.VarInt(cx), pk.VarInt(cz))
}

// SetChunkCacheRadius sets the client's view distance in chunks. Field: VarInt.
func SetChunkCacheRadius(r int32) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundSetChunkCacheRadius), pk.VarInt(r))
}

// ChunkBatchStart opens a chunk batch (no fields). The streamer (Plan 04-03)
// brackets a run of LevelChunkWithLight packets between Start and Finished so the
// client can pace its acknowledgement.
func ChunkBatchStart() pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundChunkBatchStart))
}

// ChunkBatchFinished closes a chunk batch. Field: VarInt batchSize (number of
// chunks sent in the batch).
func ChunkBatchFinished(n int32) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundChunkBatchFinished), pk.VarInt(n))
}

// ForgetLevelChunk tells the client to drop a chunk column from its cache — the
// re-center prune (PLAY-04): when the view-ring follows the player, columns that
// leave the window are forgotten so the client frees them and re-requests on a
// later approach.
//
// JAR-DERIVED 26.2 wire layout (NOT the ≤773 wiki's "VarInt z, VarInt x"):
// ClientboundForgetLevelChunkPacket.write -> FriendlyByteBuf.writeChunkPos ->
// ChunkPos.pack() -> a single big-endian Long. The pack is:
//
//	pack(x, z) = (x & 0xFFFFFFFF) | ((z & 0xFFFFFFFF) << 32)
//
// i.e. x occupies the LOW 32 bits, z the HIGH 32 bits. unpack(L) does
// x = (int)L, z = (int)(L >> 32) (arithmetic shift, so signs round-trip). The
// client reads it via readChunkPos -> readLong -> ChunkPos.unpack. Verified by
// `javap -p -c net.minecraft.network.protocol.game.ClientboundForgetLevelChunkPacket`
// and `javap -p -c net.minecraft.world.level.ChunkPos` (pack/unpack) against the
// 26.2 inner jar (recorded for 05-CAPTURE-DIFF.md).
func ForgetLevelChunk(cx, cz int32) pk.Packet {
	packed := int64(uint32(cx)) | int64(uint32(cz))<<32
	return pk.Marshal(int32(packetid.ClientboundForgetLevelChunk), pk.Long(packed))
}
