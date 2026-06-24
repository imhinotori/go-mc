package region

import (
	"bytes"
	"compress/zlib"
	"math/rand"
	"os"
	"testing"
)

// linear_bench_test.go — OPT-05 (08-03) post-integration round-trip + footprint check, run as part
// of the Phase-8 OPT-06 acceptance gate. The .linear codec landed in 08-03; this test confirms it
// still works AFTER the whole Phase-8 integration (the per-subsystem async swaps + the format-aware
// loader): a populated region written via WriteLinear and read back via ReadLinear round-trips
// byte-identically per cell, AND the .linear file is smaller than the equivalent .mca (the disk-
// savings reason the format exists). It is in the OPT-06 gate's package set (./save/...) so the same
// Docker -race -count=10 run that vets the async subsystems also re-vets the region codec.

// TestLinearRoundTripIntegration writes a densely-populated region as .linear, reads it back, and
// asserts (a) every present cell round-trips byte-for-byte, (b) absent cells stay absent, and (c) the
// .linear encoding is strictly smaller than the equivalent .mca (zlib per-chunk sectors + 4KB sector
// padding) for the same data — the whole-region zstd footprint win OPT-05 promises, confirmed intact
// after the Phase-8 integration.
func TestLinearRoundTripIntegration(t *testing.T) {
	// Build a region populated across a 16x16 block of cells with compressible, NBT-like blobs (a
	// repeated pattern plus a little per-cell entropy) so the comparison reflects real chunk data,
	// not random noise that neither codec can compress.
	lr := &LinearRegion{}
	want := make(map[[2]int][]byte)
	rng := rand.New(rand.NewSource(20260624))

	pattern := make([]byte, 6000)
	for i := range pattern {
		pattern[i] = byte(i % 41)
	}
	type cell struct{ x, z int }
	var cells []cell
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			cells = append(cells, cell{x, z})
		}
	}
	for _, c := range cells {
		blob := append([]byte(nil), pattern...)
		rng.Read(blob[:96]) // a little entropy so cells are not trivially identical
		lr.WriteSectorLinear(c.x, c.z, blob)
		want[[2]int{c.x, c.z}] = blob
	}

	// (a) + (b): write .linear and read it back; every present cell must be byte-identical, every
	// unwritten cell must read as absent (not a zero-filled chunk).
	var linBuf bytes.Buffer
	if err := WriteLinear(&linBuf, lr); err != nil {
		t.Fatalf("WriteLinear: %v", err)
	}
	got, err := ReadLinear(linBuf.Bytes())
	if err != nil {
		t.Fatalf("ReadLinear: %v", err)
	}
	for cell, blob := range want {
		gotBlob, ok := got.Get(cell[0], cell[1])
		if !ok {
			t.Fatalf("cell (%d,%d) absent after round-trip, want present", cell[0], cell[1])
		}
		if !bytes.Equal(gotBlob, blob) {
			t.Fatalf("cell (%d,%d) blob mismatch: got %d bytes, want %d (round-trip not byte-identical)",
				cell[0], cell[1], len(gotBlob), len(blob))
		}
	}
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
	linearSize := linBuf.Len()

	// (c): encode the SAME data as .mca (zlib per-chunk sectors, 4KB sector padding) and confirm the
	// .linear footprint is strictly smaller — the whole-region zstd win.
	mcaFile, err := os.CreateTemp(t.TempDir(), "integration-*.mca")
	if err != nil {
		t.Fatalf("temp mca: %v", err)
	}
	defer mcaFile.Close()
	reg, err := CreateWriter(mcaFile)
	if err != nil {
		t.Fatalf("CreateWriter: %v", err)
	}
	for _, c := range cells {
		blob := want[[2]int{c.x, c.z}]
		framed := append([]byte{2}, zlibCompressBlob(t, blob)...) // 2 = zlib tag (Anvil convention)
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

	t.Logf("OPT-05 footprint: .linear=%d bytes  .mca=%d bytes (%.1f%% of mca)",
		linearSize, mcaSize, 100*float64(linearSize)/float64(mcaSize))
	if linearSize >= mcaSize {
		t.Fatalf(".linear (%d bytes) is not smaller than .mca (%d bytes) for a populated region — OPT-05 footprint win lost",
			linearSize, mcaSize)
	}
}

// zlibCompressBlob zlib-compresses a blob the way the Anvil per-chunk sector codec stores it, for the
// .linear-vs-.mca footprint comparison. (linear_test.go has an equivalent helper named zlibCompress;
// this file uses a distinct name to avoid a redeclaration within the package.)
func zlibCompressBlob(t *testing.T, data []byte) []byte {
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
