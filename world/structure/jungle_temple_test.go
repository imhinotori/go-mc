package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// jungleType resolves the "minecraft:jungle" biome Type (in the allow-set).
var jungleType = mustBiome("minecraft:jungle")

func mustBiome(id string) levelbiome.Type {
	var t levelbiome.Type
	if err := t.UnmarshalText([]byte(id)); err != nil {
		panic("test: cannot resolve biome " + id + ": " + err.Error())
	}
	return t
}

// The jungle-temple test anchor: seed 6 places a jungle temple at chunk (0,2) (derived FROM
// the placement algorithm in TestJungleTempleExpectedChunk), orientation WEST.
const (
	jungleSeed   = int64(6)
	jungleChunkX = 0
	jungleChunkZ = 2
)

// jungleSurfaceSampler / jungleBiomeAt are FIXED stubs: surface at y=80 everywhere (above the
// sea-level gate), biome=jungle. This isolates the geometry fingerprint from the noise router.
type jungleSurfaceSampler struct{}

func (jungleSurfaceSampler) SampleSurfaceY(int, int) int { return 80 }

func jungleBiomeAt(int, int, int) levelbiome.Type { return jungleType }

func placeWholeJungleTemple(t *testing.T) (*mapView, *StructureStart, *JungleTemplePiece) {
	t.Helper()
	gen, err := NewJungleTempleStartGen()
	if err != nil {
		t.Fatal(err)
	}
	starts := gen.GenerateStarts(jungleSeed, level.ChunkPos{jungleChunkX, jungleChunkZ}, jungleSurfaceSampler{}, jungleBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected exactly 1 jungle temple start at (%d,%d), got %d", jungleChunkX, jungleChunkZ, len(starts))
	}
	start := starts[0]
	piece, ok := start.Pieces[0].(*JungleTemplePiece)
	if !ok {
		t.Fatalf("start piece is %T, want *JungleTemplePiece", start.Pieces[0])
	}
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(jungleSeed, jungleChunkX, jungleChunkZ)
	piece.PostProcess(view, fullBox(), level.ChunkPos{jungleChunkX, jungleChunkZ}, rng)
	return view, start, piece
}

// TestJungleTempleExpectedChunk: the owning chunk is computed FROM getPotentialStructureChunk
// with the JUNGLE salt 14357619 (cross-checked vs the embedded structure_set) — proving the
// temple lands in a vanilla position, not eyeballed.
func TestJungleTempleExpectedChunk(t *testing.T) {
	placement := RandomSpreadStructurePlacement{Spacing: jungleTempleSpacing, Separation: jungleTempleSeparation, Salt: jungleTempleSalt, SpreadType: SpreadLinear}
	got := placement.PotentialStructureChunk(jungleSeed, jungleChunkX, jungleChunkZ)
	if got != (level.ChunkPos{jungleChunkX, jungleChunkZ}) {
		t.Fatalf("seed %d region of (%d,%d) -> potential chunk %v, want the owning chunk itself", jungleSeed, jungleChunkX, jungleChunkZ, got)
	}
	if !placement.IsStructureChunk(jungleSeed, jungleChunkX, jungleChunkZ) {
		t.Fatalf("(%d,%d) is not its own region's structure chunk for seed %d", jungleChunkX, jungleChunkZ, jungleSeed)
	}
	// The jungle salt MUST differ from the desert salt (Pitfall #3 cross-check).
	if jungleTempleSalt == desertPyramidSalt {
		t.Fatalf("jungle salt %d must NOT equal desert salt %d", jungleTempleSalt, desertPyramidSalt)
	}
}

// TestJungleTempleBiomeGate: a non-jungle origin yields NO start (the biome check is
// load-bearing, not accept-by-default).
func TestJungleTempleBiomeGate(t *testing.T) {
	gen, err := NewJungleTempleStartGen()
	if err != nil {
		t.Fatal(err)
	}
	// A desert origin (not in {bamboo_jungle, jungle}) -> no start.
	outOfBiome := func(int, int, int) levelbiome.Type { return mustBiome("minecraft:desert") }
	starts := gen.GenerateStarts(jungleSeed, level.ChunkPos{jungleChunkX, jungleChunkZ}, jungleSurfaceSampler{}, outOfBiome)
	if len(starts) != 0 {
		t.Fatalf("a non-jungle origin produced %d starts, want 0 (accept-by-default leaked)", len(starts))
	}
}

// TestJungleTempleStartPure: the start is PURE over (seed,pos).
func TestJungleTempleStartPure(t *testing.T) {
	gen, err := NewJungleTempleStartGen()
	if err != nil {
		t.Fatal(err)
	}
	a := gen.GenerateStarts(jungleSeed, level.ChunkPos{jungleChunkX, jungleChunkZ}, jungleSurfaceSampler{}, jungleBiomeAt)
	b := gen.GenerateStarts(jungleSeed, level.ChunkPos{jungleChunkX, jungleChunkZ}, jungleSurfaceSampler{}, jungleBiomeAt)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 start each, got %d and %d", len(a), len(b))
	}
	if a[0].ChunkPos != b[0].ChunkPos || a[0].BBox != b[0].BBox {
		t.Fatalf("start not pure over (seed,pos): %+v vs %+v", a[0], b[0])
	}
}

// TestJungleTemplePlaces is the completeness gate: place the whole jungle temple and assert
// the signature blocks + the full block-set fingerprint (the MossStoneSelector per-cell draws
// + the trap/puzzle blocks + 2 chests). A truncation/draw-order divergence flips the hash.
func TestJungleTemplePlaces(t *testing.T) {
	view, _, piece := placeWholeJungleTemple(t)

	// (a) Signature presence (mapped to world via the orientation transform).
	chiseled := stateOf(block.ChiseledStoneBricks{})
	wantChisel := worldOfPiece(&piece.StructurePiece, 9, -2, 11)
	if got := view.GetBlock(wantChisel[0], wantChisel[1], wantChisel[2]); got != chiseled {
		t.Fatalf("puzzle chiseled (local 9,-2,11) = %v, want chiseled_stone_bricks %v", got, chiseled)
	}

	// (a') The 2 chests were placed (loot deferred — recorded on the piece).
	if len(piece.LootChests) != 2 {
		t.Fatalf("expected 2 chests placed, got %d", len(piece.LootChests))
	}
	for _, lc := range piece.LootChests {
		if lc.LootTable != jungleTempleMainLoot {
			t.Fatalf("chest loot table = %q, want %q", lc.LootTable, jungleTempleMainLoot)
		}
		if block.StateList[view.GetBlock(lc.X, lc.Y, lc.Z)].ID() != "minecraft:chest" {
			t.Fatalf("chest position (%d,%d,%d) does not hold a chest block", lc.X, lc.Y, lc.Z)
		}
	}

	// (b) The full fingerprint over every placed (pos,state).
	count, hash := fingerprint(view)
	if count < 200 {
		t.Fatalf("placed block count %d is implausibly small for a jungle temple (truncated)", count)
	}
	// Pinned oracle (regenerate ONLY on an intentional geometry change). Captured from the
	// seed-6 (0,2) WEST-oriented temple over the fixed y=80/jungle stub. The hash is FNV-1a
	// over the sorted (pos,state) set; it flips on ANY block/draw-order/moss-selector divergence.
	//
	// RE-SEALED 20-02: createChest now draws rng.NextLong() for the chest lootTableSeed
	// (jar-faithful — JungleTemplePiece.postProcess calls createChest BEFORE 3 more
	// generateBox(STONE_SELECTOR) boxes, so the chest's nextLong shifts those moss-selector
	// nextFloat draws). Block COUNT is unchanged (1746); only cobble<->mossy_cobble cells flip.
	// The new hash is the FAITHFUL value (the old no-draw hash was unfaithful to vanilla).
	const wantCount = 1746
	const wantHash = uint64(0x471f867df2e73b40)
	if count != wantCount {
		t.Fatalf("placed block count = %d, want pinned %d (geometry truncated/changed)", count, wantCount)
	}
	if hash != wantHash {
		t.Fatalf("jungle temple fingerprint = %#016x, want pinned %#016x (block/draw-order divergence)", hash, wantHash)
	}
}

// worldOfPiece maps a piece-local (x,y,z) to world coords via the base piece's orientation
// transform — reused by every temple test.
func worldOfPiece(p *StructurePiece, x, y, z int) [3]int {
	return [3]int{p.getWorldX(x, z), p.getWorldY(y), p.getWorldZ(x, z)}
}
