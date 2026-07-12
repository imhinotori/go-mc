package level

import (
	"bytes"
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// decodeLightPayload reads back the four VarInt-prefixed long[] BitSets and the two
// light-array lists exactly as a vanilla client's ClientboundLightUpdatePacketData
// reader does (readBitSet x4, then readList x2). Returns the raw long slices so a
// test can inspect exact bit positions and trimming.
func decodeLightPayload(t *testing.T, ld *lightData) (skyMask, blockMask, emptySky, emptyBlock []int64, skyArrs, blockArrs int) {
	t.Helper()
	var buf bytes.Buffer
	if _, err := ld.WriteTo(&buf); err != nil {
		t.Fatalf("lightData.WriteTo: %v", err)
	}
	r := bytes.NewReader(buf.Bytes())
	readBS := func(name string) []int64 {
		var n pk.VarInt
		if _, err := n.ReadFrom(r); err != nil {
			t.Fatalf("read %s len: %v", name, err)
		}
		out := make([]int64, int(n))
		for i := range out {
			var l pk.Long
			if _, err := l.ReadFrom(r); err != nil {
				t.Fatalf("read %s long %d: %v", name, i, err)
			}
			out[i] = int64(l)
		}
		return out
	}
	skyMask = readBS("skyMask")
	blockMask = readBS("blockMask")
	emptySky = readBS("emptySky")
	emptyBlock = readBS("emptyBlock")
	readArrs := func(name string) int {
		var n pk.VarInt
		if _, err := n.ReadFrom(r); err != nil {
			t.Fatalf("read %s count: %v", name, err)
		}
		for i := 0; i < int(n); i++ {
			var al pk.VarInt
			if _, err := al.ReadFrom(r); err != nil {
				t.Fatalf("read %s arrlen %d: %v", name, i, err)
			}
			skip := make([]byte, int(al))
			if _, err := r.Read(skip); err != nil {
				t.Fatalf("read %s arr %d: %v", name, i, err)
			}
		}
		return int(n)
	}
	skyArrs = readArrs("skyUpdates")
	blockArrs = readArrs("blockUpdates")
	if r.Len() != 0 {
		t.Fatalf("light payload left %d trailing bytes", r.Len())
	}
	return
}

func bitSet(words []int64, i int) bool {
	w := i / 64
	if w < 0 || w >= len(words) {
		return false
	}
	return words[w]&(1<<uint(i%64)) != 0
}

// TestLightMaskPaddingScheme locks the vanilla section-index scheme from
// ClientboundLightUpdatePacketData: getMinLightSection() = minSectionY-1 puts a
// padding light section at BIT 0, so the bottom BLOCK section lands at BIT 1, and
// the top padding is BIT secs+1. A present (2048-byte) array sets the DATA bit; a
// nil array (absent DataLayer) sets NEITHER bit. The empty bit is set only for a
// present-but-empty (len 0) layer.
func TestLightMaskPaddingScheme(t *testing.T) {
	const secs = 24
	sky := make([][]byte, secs)
	block := make([][]byte, secs)
	// Give block section 0 (the bottom block section) real light data.
	block[0] = make([]byte, 2048)
	block[0][0] = 0x0F
	// Give sky section 5 data too, to check an interior bit.
	sky[5] = make([]byte, 2048)

	ld := EncodeLightData(sky, block)
	skyMask, blockMask, emptySky, emptyBlock, skyArrs, blockArrs := decodeLightPayload(t, &ld)

	// The padding-below section is bit 0 and must be UNSET in every mask.
	if bitSet(blockMask, 0) || bitSet(skyMask, 0) || bitSet(emptyBlock, 0) || bitSet(emptySky, 0) {
		t.Fatalf("padding-below (bit 0) must be unset in all masks; sky=%v block=%v emptySky=%v emptyBlock=%v",
			skyMask, blockMask, emptySky, emptyBlock)
	}
	// The bottom BLOCK section's data bit is at index 1, NOT 0.
	if !bitSet(blockMask, 1) {
		t.Fatalf("bottom block section must set blockMask bit 1 (minSectionY-1 base), got %v", blockMask)
	}
	// Sky section 5 lands at bit 6.
	if !bitSet(skyMask, 6) {
		t.Fatalf("sky section 5 must set skyMask bit 6 (si+1), got %v", skyMask)
	}
	// The top padding section (bit secs+1 == 25) must be unset (no fill).
	if bitSet(skyMask, secs+1) || bitSet(emptySky, secs+1) {
		t.Fatalf("top padding section (bit %d) must be unset -- client derives 15 natively", secs+1)
	}
	// Arrays: exactly the present layers, in ascending section order.
	if skyArrs != 1 {
		t.Fatalf("skyUpdates count = %d, want 1", skyArrs)
	}
	if blockArrs != 1 {
		t.Fatalf("blockUpdates count = %d, want 1", blockArrs)
	}
}

// TestLightMaskEmptyBitSemantics locks prepareSectionData's three states:
//   - a present non-empty (2048) layer  => DATA bit, no empty bit, one array
//   - a present EMPTY (len 0) layer      => EMPTY bit, no data bit, no array
//   - an absent (nil) layer              => NEITHER bit (client keeps its light)
func TestLightMaskEmptyBitSemantics(t *testing.T) {
	const secs = 4
	sky := make([][]byte, secs)
	block := make([][]byte, secs)
	block[1] = make([]byte, 2048) // data
	block[2] = []byte{}           // present-but-empty
	// block[0], block[3] stay nil (absent); all sky nil.

	ld := EncodeLightData(sky, block)
	_, blockMask, _, emptyBlock, _, blockArrs := decodeLightPayload(t, &ld)

	// section 1 -> bit 2: data bit set, empty bit clear.
	if !bitSet(blockMask, 2) {
		t.Fatalf("present layer (section 1) must set blockMask bit 2, got %v", blockMask)
	}
	if bitSet(emptyBlock, 2) {
		t.Fatalf("present non-empty layer must NOT set the empty bit at 2")
	}
	// section 2 -> bit 3: empty bit set, data bit clear, no array.
	if !bitSet(emptyBlock, 3) {
		t.Fatalf("empty layer (section 2) must set emptyBlock bit 3, got %v", emptyBlock)
	}
	if bitSet(blockMask, 3) {
		t.Fatalf("empty layer must NOT set the data bit at 3")
	}
	// absent sections 0,3 -> bits 1,4: neither bit set anywhere.
	for _, bit := range []int{1, 4} {
		if bitSet(blockMask, bit) || bitSet(emptyBlock, bit) {
			t.Fatalf("absent section at bit %d must set NEITHER data nor empty bit", bit)
		}
	}
	// Only the one non-empty layer produced an array.
	if blockArrs != 1 {
		t.Fatalf("blockUpdates count = %d, want 1 (only the non-empty layer carries an array)", blockArrs)
	}
}

// TestLightMaskTrimming locks that an all-clear mask serializes as VarInt(0)
// (java.util.BitSet.toLongArray drops trailing zero words), not a fixed 64-long
// blob, so the wire is byte-identical to vanilla.
func TestLightMaskTrimming(t *testing.T) {
	const secs = 24
	sky := make([][]byte, secs) // all nil -> all masks clear
	block := make([][]byte, secs)
	ld := EncodeLightData(sky, block)
	skyMask, blockMask, emptySky, emptyBlock, skyArrs, blockArrs := decodeLightPayload(t, &ld)
	if len(skyMask) != 0 || len(blockMask) != 0 || len(emptySky) != 0 || len(emptyBlock) != 0 {
		t.Fatalf("all-clear masks must trim to length 0 (toLongArray semantics); got sky=%d block=%d emptySky=%d emptyBlock=%d",
			len(skyMask), len(blockMask), len(emptySky), len(emptyBlock))
	}
	if skyArrs != 0 || blockArrs != 0 {
		t.Fatalf("no arrays expected; got sky=%d block=%d", skyArrs, blockArrs)
	}
}
