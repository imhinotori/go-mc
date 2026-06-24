package region

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"math/rand"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// buildKnownRegion creates a LinearRegion populated with deterministic blobs at
// a known set of cells, returning the region plus the cell->blob expectations.
func buildKnownRegion(t *testing.T) (*LinearRegion, map[[2]int][]byte) {
	t.Helper()
	lr := &LinearRegion{}
	want := make(map[[2]int][]byte)

	cells := []struct {
		x, z int
		size int
	}{
		{0, 0, 100},
		{1, 0, 1},
		{31, 31, 4096},
		{15, 20, 777},
		{30, 1, 65000},
	}
	rng := rand.New(rand.NewSource(42))
	for _, c := range cells {
		blob := make([]byte, c.size)
		rng.Read(blob)
		lr.Set(c.x, c.z, blob, uint32(1000+c.x+c.z))
		want[[2]int{c.x, c.z}] = blob
	}
	return lr, want
}

func TestLinearRoundTrip(t *testing.T) {
	lr, want := buildKnownRegion(t)

	var buf bytes.Buffer
	if err := WriteLinear(&buf, lr); err != nil {
		t.Fatalf("WriteLinear: %v", err)
	}

	got, err := ReadLinear(buf.Bytes())
	if err != nil {
		t.Fatalf("ReadLinear: %v", err)
	}

	// Every known cell round-trips byte-identically.
	for cell, blob := range want {
		gotBlob, ok := got.Get(cell[0], cell[1])
		if !ok {
			t.Fatalf("cell (%d,%d) absent after round-trip, want present", cell[0], cell[1])
		}
		if !bytes.Equal(gotBlob, blob) {
			t.Fatalf("cell (%d,%d) blob mismatch: got %d bytes, want %d", cell[0], cell[1], len(gotBlob), len(blob))
		}
	}

	// Empty cells stay empty (absent reads as absent, not a zero-filled chunk).
	for z := 0; z < 32; z++ {
		for x := 0; x < 32; x++ {
			if _, set := want[[2]int{x, z}]; set {
				continue
			}
			if _, ok := got.Get(x, z); ok {
				t.Fatalf("cell (%d,%d) present after round-trip, want absent", x, z)
			}
		}
	}
}

func TestLinearSignatureFraming(t *testing.T) {
	lr, want := buildKnownRegion(t)
	var buf bytes.Buffer
	if err := WriteLinear(&buf, lr); err != nil {
		t.Fatalf("WriteLinear: %v", err)
	}
	raw := buf.Bytes()

	// Begins with the signature.
	if got := binary.BigEndian.Uint64(raw[0:8]); got != linearSignature {
		t.Fatalf("leading signature = %#x, want %#x", got, linearSignature)
	}
	// Ends with the signature.
	if got := binary.BigEndian.Uint64(raw[len(raw)-8:]); got != linearSignature {
		t.Fatalf("trailing signature = %#x, want %#x", got, linearSignature)
	}
	// Header magic + version + chunkCount.
	if got := binary.BigEndian.Uint64(raw[8:16]); got != linearSignature {
		t.Fatalf("header magic = %#x, want %#x", got, linearSignature)
	}
	if raw[16] != linearVersion {
		t.Fatalf("version = %d, want %d", raw[16], linearVersion)
	}
	// chunkCount is at offset 8(lead)+8(magic)+1(ver)+8(ts)+1(level) = 26.
	chunkCount := int16(binary.BigEndian.Uint16(raw[26:28]))
	if int(chunkCount) != len(want) {
		t.Fatalf("chunkCount = %d, want %d", chunkCount, len(want))
	}

	// A wrong leading signature is rejected.
	bad := append([]byte(nil), raw...)
	binary.BigEndian.PutUint64(bad[0:8], 0xdeadbeefdeadbeef)
	if _, err := ReadLinear(bad); !errors.Is(err, ErrLinearCorrupt) {
		t.Fatalf("bad leading signature: got err %v, want ErrLinearCorrupt", err)
	}

	// A wrong trailing signature is rejected.
	bad2 := append([]byte(nil), raw...)
	binary.BigEndian.PutUint64(bad2[len(bad2)-8:], 0xdeadbeefdeadbeef)
	if _, err := ReadLinear(bad2); !errors.Is(err, ErrLinearCorrupt) {
		t.Fatalf("bad trailing signature: got err %v, want ErrLinearCorrupt", err)
	}
}

func TestLinearCorruptSignatureRejected(t *testing.T) {
	// A truncated file is rejected, not panicked.
	if _, err := ReadLinear([]byte{0x01, 0x02, 0x03}); !errors.Is(err, ErrLinearCorrupt) {
		t.Fatalf("truncated file: got err %v, want ErrLinearCorrupt", err)
	}

	// A valid frame whose body length lies beyond the file is rejected.
	lr := &LinearRegion{}
	lr.Set(0, 0, []byte("hello"), 1)
	var buf bytes.Buffer
	if err := WriteLinear(&buf, lr); err != nil {
		t.Fatalf("WriteLinear: %v", err)
	}
	raw := buf.Bytes()
	// dataLength sits at offset 28 (after chunkCount at 26..28).
	binary.BigEndian.PutUint32(raw[28:32], 0xFFFFFFF0)
	if _, err := ReadLinear(raw); !errors.Is(err, ErrLinearCorrupt) {
		t.Fatalf("oversized declared body: got err %v, want ErrLinearCorrupt", err)
	}
}

func TestLinearDecompressionBombRejected(t *testing.T) {
	// Craft a .linear whose body decompresses far beyond maxDecompressedSize.
	// We zstd-encode a huge run of zeros (compresses to almost nothing) and
	// frame it with valid signatures + a header that claims a single cell, then
	// confirm ReadLinear rejects it with ErrLinearTooLarge rather than OOMing.
	bombSize := maxDecompressedSize + (16 << 20) // safely above the cap
	huge := make([]byte, bombSize)               // all zeros -> tiny compressed
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	compressed := enc.EncodeAll(huge, nil)
	_ = enc.Close()

	var buf bytes.Buffer
	writeU64(&buf, linearSignature) // leading
	writeU64(&buf, linearSignature) // header magic
	buf.WriteByte(linearVersion)
	writeU64(&buf, 0)         // newestTimestamp
	buf.WriteByte(byte(6))    // compressionLevel
	writeU16(&buf, uint16(1)) // chunkCount (claimed; not validated before decode)
	writeU32(&buf, uint32(len(compressed)))
	writeU64(&buf, 0) // dataHash
	buf.Write(compressed)
	var trailer [8]byte
	binary.BigEndian.PutUint64(trailer[:], linearSignature)
	buf.Write(trailer[:])

	if _, err := ReadLinear(buf.Bytes()); !errors.Is(err, ErrLinearTooLarge) {
		t.Fatalf("decompression bomb: got err %v, want ErrLinearTooLarge", err)
	}
}

// TestLinearSmallerThanMca demonstrates the footprint reduction: a populated
// region encoded as .linear is smaller than the equivalent .mca with zlib
// per-chunk sectors + 4KB padding.
func TestLinearSmallerThanMca(t *testing.T) {
	// Build a region of compressible NBT-like blobs at many cells.
	lr := &LinearRegion{}
	rng := rand.New(rand.NewSource(7))
	type cell struct{ x, z int }
	var cells []cell
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			cells = append(cells, cell{x, z})
		}
	}
	// Compressible payload: repeated patterns, like real chunk NBT.
	payload := make([]byte, 6000)
	for i := range payload {
		payload[i] = byte(i % 37)
	}
	for _, c := range cells {
		blob := append([]byte(nil), payload...)
		// add a little entropy so it is not trivially identical
		rng.Read(blob[:64])
		lr.WriteSectorLinear(c.x, c.z, blob)
	}

	// Encode .linear.
	var linBuf bytes.Buffer
	if err := WriteLinear(&linBuf, lr); err != nil {
		t.Fatalf("WriteLinear: %v", err)
	}
	linearSize := linBuf.Len()

	// Encode the equivalent .mca (zlib per-chunk sectors).
	mcaFile, err := os.CreateTemp(t.TempDir(), "cmp-*.mca")
	if err != nil {
		t.Fatalf("temp mca: %v", err)
	}
	defer mcaFile.Close()
	reg, err := CreateWriter(mcaFile)
	if err != nil {
		t.Fatalf("CreateWriter: %v", err)
	}
	for _, c := range cells {
		blob, _ := lr.Get(c.x, c.z)
		// .mca sectors store a 1-byte compression tag + zlib data; emulate by
		// zlib-compressing as mca.go's writer expects raw bytes. We store the
		// raw blob directly (WriteSector frames length+data); for a fair
		// footprint comparison we compress the blob like the real codec does.
		comp := zlibCompress(t, blob)
		framed := append([]byte{2}, comp...) // 2 = zlib tag (Anvil convention)
		if err := reg.WriteSector(c.x, c.z, framed); err != nil {
			t.Fatalf("WriteSector: %v", err)
		}
	}
	if err := reg.PadToFullSector(); err != nil {
		t.Fatalf("PadToFullSector: %v", err)
	}
	stat, err := mcaFile.Stat()
	if err != nil {
		t.Fatalf("stat mca: %v", err)
	}
	mcaSize := int(stat.Size())

	t.Logf(".linear=%d bytes  .mca=%d bytes (%.1f%% of mca)", linearSize, mcaSize, 100*float64(linearSize)/float64(mcaSize))
	if linearSize >= mcaSize {
		t.Fatalf(".linear (%d) not smaller than .mca (%d) for a populated region", linearSize, mcaSize)
	}
}

func zlibCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return b.Bytes()
}
