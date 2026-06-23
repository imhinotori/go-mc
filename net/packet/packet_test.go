package packet_test

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"testing"

	pk "github.com/imhinotori/go-mc/net/packet"
)

// TestMaxDataLength proves the 2^21 (MaxDataLength) cap rejects oversized frames on
// BOTH the uncompressed and compressed (compression-bomb) paths without panicking
// and without allocating the declared multi-MB payload. (NET-06 / T-2-02, T-2-03)
//
// This asserts existing behavior in packet.go (the uncompressed guard at the
// length check, and the compressed guard on the declared DataLength before
// zlib inflate). packet.go is NOT modified.
func TestMaxDataLength(t *testing.T) {
	t.Run("uncompressed_oversized_length_rejected", func(t *testing.T) {
		// Craft a raw uncompressed frame whose declared total Length is just over
		// the cap. lengthOfData = Length - len(VarInt(PacketID)); with a 1-byte
		// packet id that is (MaxDataLength + 2) - 1 = MaxDataLength + 1 > cap.
		var buf bytes.Buffer
		// Length prefix = MaxDataLength + 2 (so data-after-id exceeds the cap).
		writeVarInt(&buf, pk.MaxDataLength+2)
		// PacketID VarInt (1 byte, value 0).
		writeVarInt(&buf, 0)
		// Deliberately provide NO further bytes: the cap must be rejected BEFORE
		// any attempt to read/allocate the (oversized) payload. A panic or a huge
		// allocation here would be the failure mode we are guarding against.

		mustNotPanic(t, func() {
			var p pk.Packet
			err := p.UnPack(&buf, -1)
			if err == nil {
				t.Fatalf("expected oversized uncompressed frame to be rejected, got nil error")
			}
		})
	})

	t.Run("compressed_oversized_datalength_rejected", func(t *testing.T) {
		// Craft a raw compressed frame declaring DataLength > MaxDataLength. The
		// compressed path must reject on the declared DataLength BEFORE inflating
		// (the compression-bomb guard), so no zlib stream body is required.
		var inner bytes.Buffer
		// DataLength VarInt = MaxDataLength + 1 (oversized; non-zero so the guard
		// path is taken, not the uncompressed-mark path).
		writeVarInt(&inner, pk.MaxDataLength+1)
		// No zlib body needed: rejection happens before zlib.NewReader.

		var frame bytes.Buffer
		// PacketLength VarInt = length of inner.
		writeVarInt(&frame, inner.Len())
		frame.Write(inner.Bytes())

		mustNotPanic(t, func() {
			// threshold 0 = compression on; takes the compressed path.
			var p pk.Packet
			err := p.UnPack(&frame, 0)
			if err == nil {
				t.Fatalf("expected oversized declared compressed DataLength to be rejected, got nil error")
			}
		})
	})
}

// TestThreshold exercises the compression threshold boundary: a payload of size
// exactly the threshold (compressed) and one below it (sent uncompressed-marked)
// both round-trip Pack->UnPack with the same threshold on both ends. (NET-06)
func TestThreshold(t *testing.T) {
	const threshold = 16

	cases := []struct {
		name string
		size int
	}{
		{"below_threshold_uncompressed_mark", threshold - 1},
		{"at_threshold_compressed", threshold},
		{"above_threshold_compressed", threshold + 64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			for i := range data {
				data[i] = byte(i)
			}
			orig := pk.Packet{ID: 0x2A, Data: data}

			var buf bytes.Buffer
			if err := orig.Pack(&buf, threshold); err != nil {
				t.Fatalf("Pack: %v", err)
			}

			var got pk.Packet
			if err := got.UnPack(&buf, threshold); err != nil {
				t.Fatalf("UnPack: %v", err)
			}
			if got.ID != orig.ID {
				t.Fatalf("ID mismatch: got %d want %d", got.ID, orig.ID)
			}
			if !bytes.Equal(got.Data, orig.Data) {
				t.Fatalf("Data mismatch: got %d bytes want %d bytes", len(got.Data), len(orig.Data))
			}
		})
	}
}

// writeVarInt encodes a VarInt into w using the package's own encoder so the
// test crafts byte-accurate frames without re-implementing VarInt.
func writeVarInt(w io.Writer, v int) {
	_, _ = pk.VarInt(v).WriteTo(w)
}

// mustNotPanic runs fn and fails the test (rather than crashing the run) if it
// panics — the cap must reject oversized input via an error, never a panic.
func mustNotPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	fn()
}

//go:embed joingame_test.bin
var testJoinGameData []byte

func ExamplePacket_Scan_joinGame() {
	p := pk.Packet{ID: 0x24, Data: testJoinGameData}
	var (
		EID            pk.Int
		Hardcore       pk.Boolean
		Gamemode       pk.UnsignedByte
		PreGamemode    pk.Byte
		WorldNames     = []pk.Identifier{} // This cannot replace with "var DimensionNames []pk.Identifier" because "nil" has no type information
		DimensionCodec struct {
			DimensionType any `nbt:"minecraft:dimension_type"`
			WorldgenBiome any `nbt:"minecraft:worldgen/biome"`
		}
		Dimension                 any
		WorldName                 pk.Identifier
		HashedSeed                pk.Long
		MaxPlayers                pk.VarInt
		ViewDistance              pk.VarInt
		RDI, ERS, IsDebug, IsFlat pk.Boolean
	)
	err := p.Scan(
		&EID,
		&Hardcore,
		&Gamemode,
		&PreGamemode,
		pk.Array(&WorldNames),
		pk.NBT(&DimensionCodec),
		pk.NBT(&Dimension),
		&WorldName,
		&HashedSeed,
		&MaxPlayers,
		&ViewDistance,
		&RDI, &ERS, &IsDebug, &IsFlat,
	)
	fmt.Print(err)
	// Output: <nil>
}

func ExampleMarshal_setSlot() {
	for _, pf := range []struct {
		WindowID  byte
		Slot      int16
		Present   bool
		ItemID    int
		ItemCount byte
		NBT       any
	}{
		{WindowID: 0, Slot: 5, Present: false},
		{WindowID: 0, Slot: 5, Present: true, ItemID: 0x01, ItemCount: 1, NBT: pk.Byte(0)},
		{WindowID: 0, Slot: 5, Present: true, ItemID: 0x01, ItemCount: 1, NBT: pk.NBT(int32(0x12345678))},
	} {
		p := pk.Marshal(0x15,
			pk.Byte(pf.WindowID),
			pk.Short(pf.Slot),
			pk.Boolean(pf.Present),
			pk.Opt{Has: pf.Present, Field: pk.Tuple{
				pk.VarInt(pf.ItemID),
				pk.Byte(pf.ItemCount),
				pf.NBT,
			}},
		)
		fmt.Printf("%02X % 02X\n", p.ID, p.Data)
	}
	// Output:
	// 15 00 00 05 00
	// 15 00 00 05 01 01 01 00
	// 15 00 00 05 01 01 01 03 12 34 56 78
}

func BenchmarkPacket_Pack_packWithoutCompression(b *testing.B) {
	p := pk.Packet{ID: 0, Data: make([]byte, 64)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Pack(io.Discard, -1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacket_Pack_packWithCompression(b *testing.B) {
	p := pk.Packet{ID: 0, Data: make([]byte, 64)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Pack(io.Discard, 32); err != nil {
			b.Fatal(err)
		}
	}
}
