package structure

import (
	"hash/fnv"
	"sort"
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// desertType resolves the "minecraft:desert" biome Type once (the gate's allow-set value).
var desertType = func() levelbiome.Type {
	var t levelbiome.Type
	if err := t.UnmarshalText([]byte("minecraft:desert")); err != nil {
		panic("test: cannot resolve minecraft:desert biome type: " + err.Error())
	}
	return t
}()

// The desert-pyramid test anchor: world seed 38 places a desert pyramid at chunk (-58,32)
// (derived FROM the placement algorithm in TestDesertPyramidExpectedChunk, NOT eyeballed),
// with biome=desert above sea level. Its 21x21 footprint spans 4 chunks (an ideal cross-chunk
// idempotence subject).
const (
	desertSeed  = int64(38)
	desertChunkX = -58
	desertChunkZ = 32
)

// fullBox returns a writable box covering the whole vertical column at all XZ — so a test
// placing into a mapView gets the ENTIRE pyramid (no per-chunk clip), for the fingerprint.
func fullBox() BoundingBox {
	return BoundingBox{MinX: -1 << 20, MinY: -64, MinZ: -1 << 20, MaxX: 1 << 20, MaxY: 320, MaxZ: 1 << 20}
}

// placeWholePyramid builds the seed-38 desert pyramid start and PostProcesses its single
// piece into a fresh mapView over the full box (the entire structure lands). Returns the
// view + the start so the caller can fingerprint / inspect.
func placeWholePyramid(t *testing.T) (*mapView, *StructureStart, *DesertPyramidPiece) {
	t.Helper()
	gen, err := NewDesertPyramidStartGen()
	if err != nil {
		t.Fatal(err)
	}
	starts := gen.GenerateStarts(desertSeed, level.ChunkPos{desertChunkX, desertChunkZ}, desertSurfaceSampler{}, desertBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected exactly 1 desert pyramid start at (%d,%d), got %d", desertChunkX, desertChunkZ, len(starts))
	}
	start := starts[0]
	piece, ok := start.Pieces[0].(*DesertPyramidPiece)
	if !ok {
		t.Fatalf("start piece is %T, want *DesertPyramidPiece", start.Pieces[0])
	}
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(desertSeed, desertChunkX, desertChunkZ)
	piece.PostProcess(view, fullBox(), level.ChunkPos{desertChunkX, desertChunkZ}, rng)
	return view, start, piece
}

// desertSurfaceSampler / desertBiomeAt are FIXED stubs reproducing the seed-38 (-58,32)
// terrain: surface at y=64 everywhere (above sea), biome=desert. This isolates the geometry
// test from the noise router (the real router agrees — the probe confirmed sy=64,
// biome=desert at this chunk) so the fingerprint is a stable, router-independent oracle.
type desertSurfaceSampler struct{}

func (desertSurfaceSampler) SampleSurfaceY(int, int) int { return 64 }

func desertBiomeAt(int, int, int) levelbiome.Type { return desertType }

// TestDesertPyramidExpectedChunk: the owning chunk is computed FROM getPotentialStructureChunk
// (the placement algorithm), NOT eyeballed — proving the pyramid lands in a vanilla position.
func TestDesertPyramidExpectedChunk(t *testing.T) {
	placement := RandomSpreadStructurePlacement{Spacing: desertPyramidSpacing, Separation: desertPyramidSeparation, Salt: desertPyramidSalt, SpreadType: SpreadLinear}
	got := placement.PotentialStructureChunk(desertSeed, desertChunkX, desertChunkZ)
	if got != (level.ChunkPos{desertChunkX, desertChunkZ}) {
		t.Fatalf("seed %d region of (%d,%d) -> potential chunk %v, want the owning chunk itself", desertSeed, desertChunkX, desertChunkZ, got)
	}
	if !placement.IsStructureChunk(desertSeed, desertChunkX, desertChunkZ) {
		t.Fatalf("(%d,%d) is not its own region's structure chunk for seed %d", desertChunkX, desertChunkZ, desertSeed)
	}
}

// TestDesertPyramidStartPure: the start is PURE over (seed,pos) — two GenerateStarts calls
// yield the identical owner / origin / bbox (the determinism contract; placement-order
// cannot change which chunk owns the pyramid or its geometry anchor).
func TestDesertPyramidStartPure(t *testing.T) {
	gen, err := NewDesertPyramidStartGen()
	if err != nil {
		t.Fatal(err)
	}
	a := gen.GenerateStarts(desertSeed, level.ChunkPos{desertChunkX, desertChunkZ}, desertSurfaceSampler{}, desertBiomeAt)
	b := gen.GenerateStarts(desertSeed, level.ChunkPos{desertChunkX, desertChunkZ}, desertSurfaceSampler{}, desertBiomeAt)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 start each, got %d and %d", len(a), len(b))
	}
	if a[0].ChunkPos != b[0].ChunkPos || a[0].BBox != b[0].BBox {
		t.Fatalf("start not pure over (seed,pos): %+v vs %+v", a[0], b[0])
	}
}

// TestDesertPyramidPlaces is THE completeness gate: place the WHOLE seed-38 pyramid and assert
// (a) the signature blocks at their expected local coords appear (the apex chiseled band, the
// blue/orange terracotta mosaic center, the hidden TNT, the pressure plate, the 4 chests), and
// (b) the FULL block-set fingerprint (an FNV-1a hash over every (pos,state)) matches a pinned
// oracle. A truncated / wrong-block / reordered geometry changes the fingerprint -> fails.
func TestDesertPyramidPlaces(t *testing.T) {
	view, start, piece := placeWholePyramid(t)

	// (a) Signature presence: resolve the expected world coords via the piece's local->world
	// transform (the orientation may rotate the pyramid; the helper applies it).
	blue := stateOf(block.BlueTerracotta{})
	wantBlue := worldOf(piece, 10, 0, 10)
	if got := view.GetBlock(wantBlue[0], wantBlue[1], wantBlue[2]); got != blue {
		t.Fatalf("mosaic center (local 10,0,10) = %v, want blue terracotta %v — geometry truncated/wrong", got, blue)
	}

	// The hidden chamber TNT layer at local y=-13 (the edge cells are TNT, interior air).
	tnt := stateOf(block.Tnt{Unstable: false})
	wantTNT := worldOf(piece, 9, -13, 9)
	if got := view.GetBlock(wantTNT[0], wantTNT[1], wantTNT[2]); got != tnt {
		t.Fatalf("TNT trap (local 9,-13,9) = %v, want tnt %v", got, tnt)
	}

	// The pressure-plate trigger at local (10,-11,10).
	plate := stateOf(block.StonePressurePlate{Powered: false})
	wantPlate := worldOf(piece, 10, -11, 10)
	if got := view.GetBlock(wantPlate[0], wantPlate[1], wantPlate[2]); got != plate {
		t.Fatalf("pressure plate (local 10,-11,10) = %v, want stone_pressure_plate %v", got, plate)
	}

	// The apex chiseled-sandstone band (local 10,5,0 over the entrance lintel).
	chiseled := stateOf(block.ChiseledSandstone{})
	wantApex := worldOf(piece, 10, 5, 0)
	if got := view.GetBlock(wantApex[0], wantApex[1], wantApex[2]); got != chiseled {
		t.Fatalf("entrance lintel chiseled (local 10,5,0) = %v, want chiseled_sandstone %v", got, chiseled)
	}

	// (a') The 4 chests were placed (loot deferred — recorded on the piece).
	if len(piece.LootChests) != 4 {
		t.Fatalf("expected 4 chests placed, got %d", len(piece.LootChests))
	}
	for _, lc := range piece.LootChests {
		if lc.LootTable != desertPyramidLootTable {
			t.Fatalf("chest loot table = %q, want %q", lc.LootTable, desertPyramidLootTable)
		}
		if block.StateList[view.GetBlock(lc.X, lc.Y, lc.Z)].ID() != "minecraft:chest" {
			t.Fatalf("chest position (%d,%d,%d) does not hold a chest block", lc.X, lc.Y, lc.Z)
		}
	}

	// (b) The full fingerprint over every placed (pos,state). A complete pyramid has a large,
	// stable block count + hash; a truncation shrinks both.
	count, hash := fingerprint(view)
	if count < 3000 {
		t.Fatalf("placed block count %d is implausibly small for a full pyramid (truncated geometry)", count)
	}
	// Pinned oracle (regenerate ONLY when the geometry is intentionally changed). The count
	// includes the deep fillColumnDown foundation (the pyramid's columns fill to bedrock in
	// the isolated mapView — vanilla-faithful, deterministic over the fixed y=64 sampler). The
	// FNV-1a hash over the sorted (pos,state) set flips on ANY block/draw-order divergence.
	const wantCount = 57050
	const wantHash = uint64(0x02570922c9bdc1bf)
	if count != wantCount {
		t.Fatalf("placed block count = %d, want pinned %d (geometry truncated/changed)", count, wantCount)
	}
	if hash != wantHash {
		t.Fatalf("pyramid fingerprint = %#016x, want pinned %#016x (block/draw-order divergence)", hash, wantHash)
	}

	_ = start
}

// TestDesertPyramidCrossChunkIdempotent: the seed-38 pyramid spans multiple chunks. Placing it
// from EACH overlapping chunk's writable box (via placeInChunk) writes ONLY that chunk's slice;
// the union equals the whole-pyramid placement with NO double-writes / truncation (Pitfall #2).
func TestDesertPyramidCrossChunkIdempotent(t *testing.T) {
	whole, start, _ := placeWholePyramid(t)

	// The set of chunks the bbox touches.
	bb := start.BBox
	minCX, maxCX := bb.MinX>>4, bb.MaxX>>4
	minCZ, maxCZ := bb.MinZ>>4, bb.MaxZ>>4

	union := newMapView()
	for cx := minCX; cx <= maxCX; cx++ {
		for cz := minCZ; cz <= maxCZ; cz++ {
			c := level.ChunkPos{int32(cx), int32(cz)}
			box := WritableArea(c, -64, 384)
			placeInChunk(start, union, box, c, desertSeed)
		}
	}

	// The union must EXACTLY equal the whole-pyramid placement (same positions, same states).
	if len(union.blocks) != len(whole.blocks) {
		t.Fatalf("cross-chunk union has %d blocks, whole placement has %d (truncation or double-write)", len(union.blocks), len(whole.blocks))
	}
	for pos, st := range whole.blocks {
		if union.blocks[pos] != st {
			t.Fatalf("cross-chunk union diverges at %v: %v vs whole %v", pos, union.blocks[pos], st)
		}
	}

	// And each chunk wrote ONLY its own slice: re-place chunk (desertChunkX,desertChunkZ)
	// alone and confirm every write lands inside that chunk's XZ column.
	c := level.ChunkPos{desertChunkX, desertChunkZ}
	box := WritableArea(c, -64, 384)
	single := newMapView()
	placeInChunk(start, single, box, c, desertSeed)
	for pos := range single.blocks {
		if pos[0]>>4 != desertChunkX || pos[2]>>4 != desertChunkZ {
			t.Fatalf("placeInChunk(%v) leaked a write to chunk (%d,%d)", c, pos[0]>>4, pos[2]>>4)
		}
	}
}

// fingerprint returns the placed-block count + an FNV-1a hash over the sorted (x,y,z,state)
// tuples — order-independent, so it pins the SET of placed blocks (the completeness oracle).
func fingerprint(v *mapView) (int, uint64) {
	type cell struct {
		x, y, z int
		st      block.StateID
	}
	cells := make([]cell, 0, len(v.blocks))
	for pos, st := range v.blocks {
		cells = append(cells, cell{pos[0], pos[1], pos[2], st})
	}
	sort.Slice(cells, func(i, j int) bool {
		a, b := cells[i], cells[j]
		if a.x != b.x {
			return a.x < b.x
		}
		if a.y != b.y {
			return a.y < b.y
		}
		return a.z < b.z
	})
	h := fnv.New64a()
	var buf [20]byte
	for _, c := range cells {
		putInt(buf[0:], c.x)
		putInt(buf[4:], c.y)
		putInt(buf[8:], c.z)
		putInt(buf[12:], int(c.st))
		h.Write(buf[:16])
	}
	return len(cells), h.Sum64()
}

func putInt(b []byte, v int) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// worldOf maps a piece-local (x,y,z) to its world coords via the piece's orientation
// transform (so the test asserts the right cell regardless of the RNG-chosen orientation).
func worldOf(p *DesertPyramidPiece, x, y, z int) [3]int {
	return [3]int{p.getWorldX(x, z), p.getWorldY(y), p.getWorldZ(x, z)}
}
