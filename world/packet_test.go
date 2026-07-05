package world

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	pk "github.com/imhinotori/sulfur/net/packet"
)

func TestLevelChunkPacketAssembly(t *testing.T) {
	g := NewSuperflat(24, -64, -1)
	ch := g.Generate(level.ChunkPos{0, 0})

	const cx, cz = int32(3), int32(-7)
	p, err := WriteLevelChunkWithLight(cx, cz, ch)
	if err != nil {
		t.Fatalf("WriteLevelChunkWithLight: %v", err)
	}
	if p.ID != int32(packetid.ClientboundLevelChunkWithLight) {
		t.Fatalf("packet ID = %d, want %d", p.ID, int32(packetid.ClientboundLevelChunkWithLight))
	}

	// Body must begin with Int(cx), Int(cz).
	r := bytes.NewReader(p.Data)
	var gotX, gotZ pk.Int
	if _, err := gotX.ReadFrom(r); err != nil {
		t.Fatalf("read x: %v", err)
	}
	if _, err := gotZ.ReadFrom(r); err != nil {
		t.Fatalf("read z: %v", err)
	}
	if int32(gotX) != cx || int32(gotZ) != cz {
		t.Fatalf("decoded (x,z) = (%d,%d), want (%d,%d)", gotX, gotZ, cx, cz)
	}

	// The remainder must round-trip through level.Chunk.ReadFrom into a 24-section chunk.
	rt := level.EmptyChunk(24)
	if _, err := rt.ReadFrom(r); err != nil {
		t.Fatalf("chunk ReadFrom: %v", err)
	}

	// 3 CLIENT heightmaps recovered.
	if rt.HeightMaps.WorldSurface == nil {
		t.Fatal("WorldSurface heightmap (type 1) not recovered")
	}
	if rt.HeightMaps.MotionBlocking == nil {
		t.Fatal("MotionBlocking heightmap (type 4) not recovered")
	}
	if rt.HeightMaps.MotionBlockingNoLeaves == nil {
		t.Fatal("MotionBlockingNoLeaves heightmap (type 5) not recovered")
	}
	if got := rt.HeightMaps.WorldSurface.Get(0); got != ch.HeightMaps.WorldSurface.Get(0) {
		t.Fatalf("WorldSurface[0] round-trip = %d, want %d", got, ch.HeightMaps.WorldSurface.Get(0))
	}

	// Section blob round-trips with the two shorts; bottom section has bedrock+stone.
	if len(rt.Sections) != 24 {
		t.Fatalf("round-trip section count = %d, want 24", len(rt.Sections))
	}
	if rt.Sections[0].BlockCount != ch.Sections[0].BlockCount {
		t.Fatalf("section[0] BlockCount round-trip = %d, want %d",
			rt.Sections[0].BlockCount, ch.Sections[0].BlockCount)
	}
	if rt.Sections[0].FluidCount != 0 {
		t.Fatalf("section[0] FluidCount round-trip = %d, want 0", rt.Sections[0].FluidCount)
	}

	// Light arrays (WORLD-03): the source chunk carries a full 2048-byte SkyLight
	// per section, which Chunk.WriteTo serializes into the lightData section of the
	// packet. (level.Chunk.ReadFrom decodes the light masks/arrays off the wire but
	// does not re-attach them to Section.SkyLight, so we assert on the source — the
	// bytes that actually go out — and confirm the body fully drains, proving the
	// light tail was both written and read.)
	// Real light (LevelLightEngine) now decides per section whether a sky-light array is present:
	// fully-lit / attenuated sections carry a 2048-byte array; a fully-dark section carries nil
	// (skipped in the wire light mask, exactly as vanilla). Assert every PRESENT array is 2048
	// bytes and that at least the open-air sections above the terrain surface are lit — the wire
	// still round-trips and fully drains regardless. CITE: level/chunk.go WriteTo lightData.
	var skyCount int
	for i, s := range ch.Sections {
		if s.SkyLight != nil && len(s.SkyLight) != 2048 {
			t.Fatalf("source section %d SkyLight len = %d, want 2048 (or nil)", i, len(s.SkyLight))
		}
		if s.SkyLight != nil {
			skyCount++
		}
	}
	if skyCount == 0 {
		t.Fatalf("no section carries sky light — expected the open-air sections to be lit")
	}

	// The reader must be fully consumed: x + z + heightmaps + sections + block
	// entities + light all accounted for (no trailing/truncated bytes).
	if rem := r.Len(); rem != 0 {
		t.Fatalf("packet body has %d unconsumed bytes after full decode", rem)
	}
}

func TestForgetLevelChunkWire(t *testing.T) {
	const cx, cz = int32(7), int32(-3)

	p := ForgetLevelChunk(cx, cz)
	if p.ID != int32(packetid.ClientboundForgetLevelChunk) {
		t.Fatalf("ForgetLevelChunk ID = %d, want %d", p.ID, int32(packetid.ClientboundForgetLevelChunk))
	}

	// Jar-derived layout: a single packed Long, x in the low 32 bits, z in the
	// high 32 bits (ChunkPos.pack). Decode it back the way the client's
	// ChunkPos.unpack does: x = (int)L, z = (int)(L>>32).
	var packed pk.Long
	if err := p.Scan(&packed); err != nil {
		t.Fatalf("scan ForgetLevelChunk: %v", err)
	}
	gotX := int32(uint64(packed))        // low 32 bits
	gotZ := int32(uint64(packed) >> 32)  // high 32 bits, sign-extended on cast
	if gotX != cx || gotZ != cz {
		t.Fatalf("decoded (x,z) = (%d,%d), want (%d,%d)", gotX, gotZ, cx, cz)
	}

	// Body must be exactly 8 bytes (one Long), no VarInt framing.
	if len(p.Data) != 8 {
		t.Fatalf("ForgetLevelChunk body = %d bytes, want 8 (packed Long)", len(p.Data))
	}
}

func TestCachePackets(t *testing.T) {
	const cx, cz, radius, batch = int32(5), int32(-9), int32(10), int32(42)

	center := SetChunkCacheCenter(cx, cz)
	if center.ID != int32(packetid.ClientboundSetChunkCacheCenter) {
		t.Fatalf("center ID = %d, want %d", center.ID, int32(packetid.ClientboundSetChunkCacheCenter))
	}
	var gx, gz pk.VarInt
	if err := center.Scan(&gx, &gz); err != nil {
		t.Fatalf("scan center: %v", err)
	}
	if int32(gx) != cx || int32(gz) != cz {
		t.Fatalf("center (x,z) = (%d,%d), want (%d,%d)", gx, gz, cx, cz)
	}

	rad := SetChunkCacheRadius(radius)
	if rad.ID != int32(packetid.ClientboundSetChunkCacheRadius) {
		t.Fatalf("radius ID = %d, want %d", rad.ID, int32(packetid.ClientboundSetChunkCacheRadius))
	}
	var gr pk.VarInt
	if err := rad.Scan(&gr); err != nil {
		t.Fatalf("scan radius: %v", err)
	}
	if int32(gr) != radius {
		t.Fatalf("radius = %d, want %d", gr, radius)
	}

	start := ChunkBatchStart()
	if start.ID != int32(packetid.ClientboundChunkBatchStart) {
		t.Fatalf("batch start ID = %d, want %d", start.ID, int32(packetid.ClientboundChunkBatchStart))
	}

	fin := ChunkBatchFinished(batch)
	if fin.ID != int32(packetid.ClientboundChunkBatchFinished) {
		t.Fatalf("batch finished ID = %d, want %d", fin.ID, int32(packetid.ClientboundChunkBatchFinished))
	}
	var gn pk.VarInt
	if err := fin.Scan(&gn); err != nil {
		t.Fatalf("scan batch finished: %v", err)
	}
	if int32(gn) != batch {
		t.Fatalf("batch size = %d, want %d", gn, batch)
	}
}
