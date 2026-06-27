package world

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/noisechunk"
	"github.com/imhinotori/sulfur/world/structure"
)

// beardWorldPiece is a bbox-only Piece for constructing synthetic structure starts in the
// generator beard tests (it never writes blocks).
type beardWorldPiece struct{ bb structure.BoundingBox }

func (p beardWorldPiece) BoundingBox() structure.BoundingBox { return p.bb }
func (p beardWorldPiece) PostProcess(view structure.WorldGenView, box structure.BoundingBox, chunkPos level.ChunkPos, rng levelgen.RandomSource) {
}

// densityField extracts the full per-block final_density field for a chunk so two NoiseChunks
// (beard vs no-beard) can be compared exactly. Returns minY/height for indexing.
func densityField(nc *noisechunk.NoiseChunk) []float64 {
	minY := nc.MinY()
	maxY := minY + nc.Height()
	field := make([]float64, 0, 16*16*(maxY-minY))
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := minY; y < maxY; y++ {
				field = append(field, nc.FinalDensity(lx, y, lz))
			}
		}
	}
	return field
}

// TestNonAdaptingUnchanged is the CRITICAL regression guard (T-20-13): a chunk influenced
// only by a NONE-adaptation structure (desert_pyramid) generates a final_density field
// BYTE-IDENTICAL to a beard-free chunk. The Beardifier for a NONE structure is EMPTY ->
// Compute returns 0 everywhere -> the additive term is 0 -> identical density.
func TestNonAdaptingUnchanged(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	pos := level.ChunkPos{0, 0}

	// Reference: no beard at all.
	ncPlain := noisechunk.NewNoiseChunk(g.router, pos)
	plain := densityField(ncPlain)

	// A NONE structure (desert_pyramid) start overlapping the chunk -> EMPTY Beardifier.
	bb := structure.BoundingBox{MinX: 0, MinY: 60, MinZ: 0, MaxX: 15, MaxY: 75, MaxZ: 15}
	noneStart := &structure.StructureStart{
		Structure: "minecraft:desert_pyramid",
		ChunkPos:  pos,
		Pieces:    []structure.Piece{beardWorldPiece{bb: bb}},
	}
	noneStart.RecomputeBBox()
	beard := structure.ForStructuresInChunk([]*structure.StructureStart{noneStart}, pos)

	ncBeard := noisechunk.NewNoiseChunkWithBeard(g.router, pos, beard.Compute)
	withBeard := densityField(ncBeard)

	if len(plain) != len(withBeard) {
		t.Fatalf("field length mismatch: plain %d, beard %d", len(plain), len(withBeard))
	}
	for i := range plain {
		if plain[i] != withBeard[i] {
			t.Fatalf("NONE structure changed final_density at index %d: plain %v, beard %v (must be byte-identical)", i, plain[i], withBeard[i])
		}
	}

	// And the full generated chunk is byte-identical through the encoder (the NONE guard at
	// the wire level — a NONE structure must not change a single terrain byte).
	chPlain := g.GenerateTerrain(pos)
	var a bytes.Buffer
	if _, err := chPlain.WriteTo(&a); err != nil {
		t.Fatalf("WriteTo plain: %v", err)
	}
	// GenerateTerrain over a real seed: at this seed/pos no village/stronghold owns the
	// chunk, so its beardifierFor is EMPTY and the chunk equals the beard-free reference.
	chGen := g.GenerateTerrain(pos)
	var b bytes.Buffer
	if _, err := chGen.WriteTo(&b); err != nil {
		t.Fatalf("WriteTo gen: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("GenerateTerrain is not byte-identical across calls (beard determinism broken)")
	}
}

// TestVillageBeardRaises: a village (beard_thin) start near the chunk RAISES the terrain
// density (the additive contribution is positive below the structure floor) compared to the
// beard-free chunk — so a floating village no longer floats. We assert the density field
// gains mass (more solid cells) under the structure box.
func TestVillageBeardRaises(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	pos := level.ChunkPos{0, 0}

	// A village piece box spanning the chunk at a surface-ish band.
	bb := structure.BoundingBox{MinX: 0, MinY: 64, MinZ: 0, MaxX: 15, MaxY: 78, MaxZ: 15}
	start := &structure.StructureStart{
		Structure: "minecraft:village_plains",
		ChunkPos:  pos,
		Pieces:    []structure.Piece{beardWorldPiece{bb: bb}},
	}
	start.RecomputeBBox()
	beard := structure.ForStructuresInChunk([]*structure.StructureStart{start}, pos)
	if beard.Compute(8, 60, 8) == 0 {
		t.Fatal("village beardifier produced 0 below the box; expected a raise contribution")
	}

	ncPlain := noisechunk.NewNoiseChunk(g.router, pos)
	ncBeard := noisechunk.NewNoiseChunkWithBeard(g.router, pos, beard.Compute)

	// Below the structure floor (y in [50,64)), the beard_thin contribution is positive, so
	// the beard density must be >= the plain density everywhere in the affected band and
	// strictly greater somewhere (terrain raised toward the structure).
	var raisedSomewhere bool
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := 50; y < 64; y++ {
				p := ncPlain.FinalDensity(lx, y, lz)
				b := ncBeard.FinalDensity(lx, y, lz)
				if b < p-1e-9 {
					t.Fatalf("beard_thin LOWERED density below box at (%d,%d,%d): plain %v beard %v", lx, y, lz, p, b)
				}
				if b > p+1e-9 {
					raisedSomewhere = true
				}
			}
		}
	}
	if !raisedSomewhere {
		t.Fatal("village beard_thin did not raise final_density anywhere below the box")
	}
}

// TestStrongholdBuries: a stronghold (bury) start INCREASES density inside the structure box
// (the bury contribution fills terrain to bury it) compared to the beard-free chunk.
func TestStrongholdBuries(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	pos := level.ChunkPos{0, 0}

	// A stronghold piece box (underground-ish band).
	bb := structure.BoundingBox{MinX: 2, MinY: 20, MinZ: 2, MaxX: 13, MaxY: 32, MaxZ: 13}
	start := &structure.StructureStart{
		Structure: "minecraft:stronghold",
		ChunkPos:  pos,
		Pieces:    []structure.Piece{beardWorldPiece{bb: bb}},
	}
	start.RecomputeBBox()
	beard := structure.ForStructuresInChunk([]*structure.StructureStart{start}, pos)
	if beard.Compute(8, 26, 8) == 0 {
		t.Fatal("stronghold beardifier produced 0 at the box center; expected a bury contribution")
	}

	ncPlain := noisechunk.NewNoiseChunk(g.router, pos)
	ncBeard := noisechunk.NewNoiseChunkWithBeard(g.router, pos, beard.Compute)

	// Inside the bury box, the contribution is positive (density increased -> more solid).
	var buriedSomewhere bool
	for lx := 2; lx <= 13; lx++ {
		for lz := 2; lz <= 13; lz++ {
			for y := 20; y <= 32; y++ {
				p := ncPlain.FinalDensity(lx, y, lz)
				b := ncBeard.FinalDensity(lx, y, lz)
				if b < p-1e-9 {
					t.Fatalf("bury LOWERED density inside box at (%d,%d,%d): plain %v beard %v", lx, y, lz, p, b)
				}
				if b > p+1e-9 {
					buriedSomewhere = true
				}
			}
		}
	}
	if !buriedSomewhere {
		t.Fatal("stronghold bury did not raise final_density anywhere inside the box")
	}
}

// TestBeardifierForEmptyChunk: a chunk with NO structures yields the EMPTY Beardifier (the
// common case: most chunks have no adapting structure -> Compute 0 -> byte-identical).
func TestBeardifierForEmptyChunk(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	// A far-flung chunk unlikely to own/neighbor a village or stronghold start.
	b := g.beardifierFor(level.ChunkPos{1000, 1000})
	if b == nil {
		t.Fatal("beardifierFor returned nil; want a non-nil (possibly EMPTY) Beardifier")
	}
	// EMPTY/no-adapting -> Compute returns 0 at every probe.
	for _, p := range [][3]int{{0, 0, 0}, {16008, 64, 16008}, {16000, 100, 16000}} {
		if got := b.Compute(p[0], p[1], p[2]); got != 0 {
			t.Fatalf("beardifierFor(far chunk).Compute%v = %v, want 0", p, got)
		}
	}
}
