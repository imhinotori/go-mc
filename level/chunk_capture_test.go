package level

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestSectionWireVsVanillaCapture is the AUTHORITATIVE WORLD-02/03 gate: it
// byte-diffs Sulfur's ClientboundLevelChunkWithLight encoder output against a
// real vanilla 26.2 superflat chunk packet (fixtures/vanilla-superflat-chunk.bin,
// captured from temp/cache/26.2-server.jar on level-type=minecraft:flat,
// level-seed=144, chunk (0,0); see WORLD-CAPTURE-DIFF.md for the reproducible
// method). A Go self-round-trip CANNOT prove WORLD-02 because the fork's
// Section read/write are symmetric — the section-header bits-per-entry bug this
// capture caught (header said 1 bit while the data was 4-bit/256 longs) was
// invisible to every round-trip test. Only the byte-diff vs vanilla exposes it.
//
// The assertions are on the LOAD-BEARING FRAMING — exactly the divergences that
// cause client stripes/void:
//   - the heightmap set: exactly 3 entries, ids {1,4,5} (Usage.CLIENT only)
//   - per-section: two shorts (blockCount, fluidCount) then a paletted-states
//     container whose header bits-per-entry matches the long count it packs
//     (NO VarInt length prefix), then a biomes container
//   - the section count derived from the dimension height (24 for overworld)
//   - the light bitsets + 2048-byte arrays
//
// Exact block CONTENT differs (vanilla superflat = bedrock+dirt+grass; Sulfur's
// stub = bedrock+stone) — that is expected and documented in WORLD-CAPTURE-DIFF.md.
// The test asserts STRUCTURE, not block ids.
func TestSectionWireVsVanillaCapture(t *testing.T) {
	fixture := filepath.Join("..", ".planning", "phases", "04-world-chunk-system", "fixtures", "vanilla-superflat-chunk.bin")
	vanilla, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("vanilla golden fixture absent (%v) — the capture-diff is the gate; "+
			"regenerate per .planning/phases/04-world-chunk-system/WORLD-CAPTURE-DIFF.md", err)
	}

	const secs = 24 // overworld height 384 / 16 (derived, never hard-coded on the wire path)

	// --- Parse the vanilla golden body and assert its framing. ---
	van := parseChunkBody(t, "vanilla", vanilla, secs)
	assertFraming(t, "vanilla", van)

	// --- Build Sulfur's chunk for the SAME coords + encode it the same way. ---
	sulfurChunk := buildSulfurSuperflat(t, secs, -64, -48)
	var body bytes.Buffer
	if _, err := pk.Int(0).WriteTo(&body); err != nil { // chunk x
		t.Fatalf("write x: %v", err)
	}
	if _, err := pk.Int(0).WriteTo(&body); err != nil { // chunk z
		t.Fatalf("write z: %v", err)
	}
	if _, err := sulfurChunk.WriteTo(&body); err != nil {
		t.Fatalf("Sulfur Chunk.WriteTo: %v", err)
	}
	sul := parseChunkBody(t, "sulfur", body.Bytes(), secs)
	assertFraming(t, "sulfur", sul)

	// --- The load-bearing diff: Sulfur's framing must MATCH vanilla's. ---
	if sul.heightmapCount != van.heightmapCount {
		t.Errorf("heightmap count: sulfur=%d vanilla=%d", sul.heightmapCount, van.heightmapCount)
	}
	if !sameSet(sul.heightmapIDs, van.heightmapIDs) {
		t.Errorf("heightmap id set: sulfur=%v vanilla=%v (must be {1,4,5})", sul.heightmapIDs, van.heightmapIDs)
	}
	if sul.sectionCount != van.sectionCount {
		t.Errorf("section count: sulfur=%d vanilla=%d", sul.sectionCount, van.sectionCount)
	}
	if sul.sectionCount != secs {
		t.Errorf("section count %d != derived %d", sul.sectionCount, secs)
	}

	// Per-section header invariant (the bug the capture caught): the stored
	// states bits-per-entry header must imply EXACTLY the long count packed.
	for i := 0; i < secs; i++ {
		vs := van.sections[i]
		ss := sul.sections[i]
		if want := expectedStateLongs(ss.statesBits); ss.statesLongs != want {
			t.Errorf("sulfur sec %d: states header bits=%d implies %d longs but packed %d "+
				"(the bits/longs misframe that causes stripes)", i, ss.statesBits, want, ss.statesLongs)
		}
		if want := expectedStateLongs(vs.statesBits); vs.statesLongs != want {
			t.Errorf("vanilla sec %d: states header bits=%d implies %d longs but packed %d", i, vs.statesBits, want, vs.statesLongs)
		}
	}

	// Light arrays must be the fixed 2048-byte form, masks present.
	for i, n := range sul.skyArrayLens {
		if n != 2048 {
			t.Errorf("sulfur skyLight array %d len=%d, want 2048", i, n)
		}
	}
	if van.bodyTrailing != 0 {
		t.Errorf("vanilla golden parse left %d trailing bytes (framing drift in the golden or the parser)", van.bodyTrailing)
	}
	if sul.bodyTrailing != 0 {
		t.Errorf("sulfur parse left %d trailing bytes (framing drift)", sul.bodyTrailing)
	}
}

// chunkFraming is the structural summary extracted by re-walking a chunk body.
type chunkFraming struct {
	heightmapCount int
	heightmapIDs   []int32
	sectionCount   int
	sections       []sectionFraming
	skyArrayLens   []int
	bodyTrailing   int
}

type sectionFraming struct {
	blockCount, fluidCount int16
	statesBits             int
	statesLongs            int
	biomesBits             int
}

// parseChunkBody walks "Int x, Int z, heightmaps, section blob, block entities,
// light" manually so it can report per-field framing (it does NOT lean on the
// symmetric Chunk.ReadFrom, which would hide a bits/longs misframe).
func parseChunkBody(t *testing.T, label string, body []byte, secs int) chunkFraming {
	t.Helper()
	r := bytes.NewReader(body)
	var x, z pk.Int
	mustRead(t, label+" x", &x, r)
	mustRead(t, label+" z", &z, r)

	var out chunkFraming

	// Heightmaps: VarInt count, then {VarInt type, VarInt-len long[]}.
	var hmCount pk.VarInt
	mustRead(t, label+" hm count", &hmCount, r)
	out.heightmapCount = int(hmCount)
	for i := 0; i < int(hmCount); i++ {
		var typ, longCount pk.VarInt
		mustRead(t, label+" hm type", &typ, r)
		mustRead(t, label+" hm longCount", &longCount, r)
		for j := 0; j < int(longCount); j++ {
			var l pk.Long
			mustRead(t, label+" hm long", &l, r)
		}
		out.heightmapIDs = append(out.heightmapIDs, int32(typ))
	}

	// Section blob: VarInt byte length + the concatenated section encodings.
	var blobLen pk.VarInt
	mustRead(t, label+" blob len", &blobLen, r)
	blob := make([]byte, int(blobLen))
	if _, err := r.Read(blob); err != nil {
		t.Fatalf("%s read section blob: %v", label, err)
	}
	sr := bytes.NewReader(blob)
	for i := 0; i < secs; i++ {
		var sf sectionFraming
		var bc, fc pk.Short
		mustRead(t, label+" blockCount", &bc, sr)
		mustRead(t, label+" fluidCount", &fc, sr)
		sf.blockCount, sf.fluidCount = int16(bc), int16(fc)
		sf.statesBits, sf.statesLongs = walkContainer(t, label+" states", sr, 4096)
		sf.biomesBits, _ = walkContainer(t, label+" biomes", sr, 64)
		out.sections = append(out.sections, sf)
	}
	if sr.Len() != 0 {
		t.Errorf("%s: %d trailing bytes in section blob (section framing drift)", label, sr.Len())
	}
	out.sectionCount = secs

	// Block entities: VarInt count (bodies skipped — superflat has none).
	var beCount pk.VarInt
	mustRead(t, label+" be count", &beCount, r)
	if int(beCount) != 0 {
		t.Fatalf("%s: unexpected %d block entities in a superflat chunk", label, int(beCount))
	}

	// Light: four VarInt-len long[] bitsets, then skyUpdates + blockUpdates lists.
	skipBitSet(t, label+" skyMask", r)
	skipBitSet(t, label+" blockMask", r)
	skipBitSet(t, label+" emptySkyMask", r)
	skipBitSet(t, label+" emptyBlockMask", r)
	out.skyArrayLens = readLightArrayLens(t, label+" skyUpdates", r)
	_ = readLightArrayLens(t, label+" blockUpdates", r)

	out.bodyTrailing = r.Len()
	return out
}

// walkContainer reads a paletted container exactly as a client would: header
// bits-per-entry byte, palette (format keyed off the header bits), then a
// fixed-size long[] whose count is DERIVED from the header bits (NO prefix).
// Returns (headerBits, longCount).
func walkContainer(t *testing.T, label string, r *bytes.Reader, entryCount int) (int, int) {
	t.Helper()
	var b pk.UnsignedByte
	mustRead(t, label+" bits", &b, r)
	bits := int(b)
	directThreshold := 8
	if entryCount == 64 {
		directThreshold = 3
	}
	switch {
	case bits == 0:
		var v pk.VarInt
		mustRead(t, label+" single", &v, r)
		return 0, 0
	case bits <= directThreshold:
		var n pk.VarInt
		mustRead(t, label+" palLen", &n, r)
		for i := 0; i < int(n); i++ {
			var v pk.VarInt
			mustRead(t, label+" palEntry", &v, r)
		}
	default:
		// direct/global palette: no palette section.
	}
	vpl := 64 / bits
	longs := (entryCount + vpl - 1) / vpl
	for i := 0; i < longs; i++ {
		var l pk.Long
		mustRead(t, label+" dataLong", &l, r)
	}
	return bits, longs
}

// expectedStateLongs is the long count a states container MUST pack for a given
// header bits-per-entry over 4096 entries (no length prefix — the client derives
// it from the header). A mismatch with the packed count is the stripes/void bug.
func expectedStateLongs(bits int) int {
	if bits == 0 {
		return 0
	}
	vpl := 64 / bits
	return (4096 + vpl - 1) / vpl
}

func skipBitSet(t *testing.T, label string, r *bytes.Reader) {
	t.Helper()
	var n pk.VarInt
	mustRead(t, label+" len", &n, r)
	for i := 0; i < int(n); i++ {
		var l pk.Long
		mustRead(t, label+" long", &l, r)
	}
}

func readLightArrayLens(t *testing.T, label string, r *bytes.Reader) []int {
	t.Helper()
	var n pk.VarInt
	mustRead(t, label+" count", &n, r)
	lens := make([]int, 0, int(n))
	for i := 0; i < int(n); i++ {
		var arrLen pk.VarInt
		mustRead(t, label+" arrLen", &arrLen, r)
		buf := make([]byte, int(arrLen))
		if _, err := r.Read(buf); err != nil {
			t.Fatalf("%s read array %d: %v", label, i, err)
		}
		lens = append(lens, int(arrLen))
	}
	return lens
}

func mustRead(t *testing.T, what string, f pk.FieldDecoder, r *bytes.Reader) {
	t.Helper()
	if _, err := f.ReadFrom(r); err != nil {
		t.Fatalf("read %s: %v", what, err)
	}
}

// assertFraming checks the framing invariants that hold for BOTH a vanilla and a
// Sulfur superflat chunk (independent of block content).
func assertFraming(t *testing.T, label string, f chunkFraming) {
	t.Helper()
	if f.heightmapCount != 3 {
		t.Errorf("%s heightmap count=%d, want 3 (Usage.CLIENT only)", label, f.heightmapCount)
	}
	if !sameSet(f.heightmapIDs, []int32{1, 4, 5}) {
		t.Errorf("%s heightmap ids=%v, want {1,4,5}", label, f.heightmapIDs)
	}
	if f.bodyTrailing != 0 {
		t.Errorf("%s: %d trailing bytes after full body parse (framing drift)", label, f.bodyTrailing)
	}
}

func sameSet(got, want []int32) bool {
	if len(got) != len(want) {
		return false
	}
	m := map[int32]int{}
	for _, v := range got {
		m[v]++
	}
	for _, v := range want {
		if m[v] == 0 {
			return false
		}
		m[v]--
	}
	return true
}

// buildSulfurSuperflat replicates the world.Superflat fill (bedrock floor +
// stone column to surfaceY, plains single-value biome, full sky light, the 3
// CLIENT heightmaps) using only level primitives, so this package-level test
// avoids the level<-world import cycle while exercising the SAME encoder path.
func buildSulfurSuperflat(t *testing.T, secs, minY, surfaceY int) *Chunk {
	t.Helper()
	stone, ok := block.ToStateID[block.Stone{}]
	if !ok {
		t.Fatal("block.ToStateID[block.Stone{}] missing")
	}
	bedrock, ok := block.ToStateID[block.Bedrock{}]
	if !ok {
		t.Fatal("block.ToStateID[block.Bedrock{}] missing")
	}
	var plains biome.Type
	if err := plains.UnmarshalText([]byte("minecraft:plains")); err != nil {
		t.Fatalf("plains biome: %v", err)
	}

	ch := EmptyChunk(secs)
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			for y := minY; y <= surfaceY; y++ {
				sec := (y - minY) >> 4
				local := (y&15)<<8 | (z&15)<<4 | (x & 15)
				if sec < 0 || sec >= len(ch.Sections) {
					continue
				}
				if y == minY {
					ch.Sections[sec].SetBlock(local, bedrock)
				} else {
					ch.Sections[sec].SetBlock(local, stone)
				}
			}
		}
	}
	for i := range ch.Sections {
		s := &ch.Sections[i]
		s.FluidCount = 0
		s.Biomes = NewBiomesPaletteContainer(4*4*4, plains)
		s.SkyLight = make([]byte, 2048)
		for j := range s.SkyLight {
			s.SkyLight[j] = 0xFF
		}
	}
	ch.Status = StatusFull
	return ch
}
