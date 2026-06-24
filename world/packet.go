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
