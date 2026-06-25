package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// swampType resolves the "minecraft:swamp" biome Type (the only allow-set value).
var swampType = mustBiome("minecraft:swamp")

// The swamp-hut test anchor: seed 5 places a swamp hut at chunk (0,2), orientation WEST.
const (
	swampSeed   = int64(5)
	swampChunkX = 0
	swampChunkZ = 2
)

// swampSurfaceSampler / swampBiomeAt are FIXED stubs: surface at y=64 (swamps are near sea
// level), biome=swamp.
type swampSurfaceSampler struct{}

func (swampSurfaceSampler) SampleSurfaceY(int, int) int { return 64 }

func swampBiomeAt(int, int, int) levelbiome.Type { return swampType }

func placeWholeSwampHut(t *testing.T) (*mapView, *StructureStart, *SwampHutPiece) {
	t.Helper()
	gen, err := NewSwampHutStartGen()
	if err != nil {
		t.Fatal(err)
	}
	starts := gen.GenerateStarts(swampSeed, level.ChunkPos{swampChunkX, swampChunkZ}, swampSurfaceSampler{}, swampBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected exactly 1 swamp hut start at (%d,%d), got %d", swampChunkX, swampChunkZ, len(starts))
	}
	start := starts[0]
	piece, ok := start.Pieces[0].(*SwampHutPiece)
	if !ok {
		t.Fatalf("start piece is %T, want *SwampHutPiece", start.Pieces[0])
	}
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(swampSeed, swampChunkX, swampChunkZ)
	piece.PostProcess(view, fullBox(), level.ChunkPos{swampChunkX, swampChunkZ}, rng)
	return view, start, piece
}

// TestSwampHutExpectedChunk: the owning chunk is derived FROM the placement algorithm with
// the SWAMP salt 14357620 (cross-checked vs the embedded structure_set).
func TestSwampHutExpectedChunk(t *testing.T) {
	placement := RandomSpreadStructurePlacement{Spacing: swampHutSpacing, Separation: swampHutSeparation, Salt: swampHutSalt, SpreadType: SpreadLinear}
	got := placement.PotentialStructureChunk(swampSeed, swampChunkX, swampChunkZ)
	if got != (level.ChunkPos{swampChunkX, swampChunkZ}) {
		t.Fatalf("seed %d region of (%d,%d) -> potential chunk %v, want the owning chunk itself", swampSeed, swampChunkX, swampChunkZ, got)
	}
	if !placement.IsStructureChunk(swampSeed, swampChunkX, swampChunkZ) {
		t.Fatalf("(%d,%d) is not its own region's structure chunk for seed %d", swampChunkX, swampChunkZ, swampSeed)
	}
	if swampHutSalt == desertPyramidSalt || swampHutSalt == jungleTempleSalt {
		t.Fatalf("swamp salt %d must be unique vs desert %d / jungle %d", swampHutSalt, desertPyramidSalt, jungleTempleSalt)
	}
}

// TestSwampHutBiomeGate: a non-swamp origin yields NO start (the biome check is load-bearing).
func TestSwampHutBiomeGate(t *testing.T) {
	gen, err := NewSwampHutStartGen()
	if err != nil {
		t.Fatal(err)
	}
	outOfBiome := func(int, int, int) levelbiome.Type { return mustBiome("minecraft:plains") }
	starts := gen.GenerateStarts(swampSeed, level.ChunkPos{swampChunkX, swampChunkZ}, swampSurfaceSampler{}, outOfBiome)
	if len(starts) != 0 {
		t.Fatalf("a non-swamp origin produced %d starts, want 0 (accept-by-default leaked)", len(starts))
	}
}

// TestSwampHutStartPure: the start is PURE over (seed,pos).
func TestSwampHutStartPure(t *testing.T) {
	gen, err := NewSwampHutStartGen()
	if err != nil {
		t.Fatal(err)
	}
	a := gen.GenerateStarts(swampSeed, level.ChunkPos{swampChunkX, swampChunkZ}, swampSurfaceSampler{}, swampBiomeAt)
	b := gen.GenerateStarts(swampSeed, level.ChunkPos{swampChunkX, swampChunkZ}, swampSurfaceSampler{}, swampBiomeAt)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 start each, got %d and %d", len(a), len(b))
	}
	if a[0].ChunkPos != b[0].ChunkPos || a[0].BBox != b[0].BBox {
		t.Fatalf("start not pure over (seed,pos): %+v vs %+v", a[0], b[0])
	}
}

// TestSwampHutPlaces is the completeness gate: place the whole hut + assert the signature
// blocks (cauldron, crafting table, potted red mushroom) + the full block-set fingerprint.
func TestSwampHutPlaces(t *testing.T) {
	view, _, piece := placeWholeSwampHut(t)

	// (a) Signature furnishings (mapped to world via the orientation transform).
	cauldron := stateOf(block.Cauldron{})
	wantCauldron := worldOfPiece(&piece.StructurePiece, 4, 2, 6)
	if got := view.GetBlock(wantCauldron[0], wantCauldron[1], wantCauldron[2]); got != cauldron {
		t.Fatalf("cauldron (local 4,2,6) = %v, want cauldron %v", got, cauldron)
	}
	craft := stateOf(block.CraftingTable{})
	wantCraft := worldOfPiece(&piece.StructurePiece, 3, 2, 6)
	if got := view.GetBlock(wantCraft[0], wantCraft[1], wantCraft[2]); got != craft {
		t.Fatalf("crafting table (local 3,2,6) = %v, want crafting_table %v", got, craft)
	}
	pot := stateOf(block.PottedRedMushroom{})
	wantPot := worldOfPiece(&piece.StructurePiece, 1, 3, 5)
	if got := view.GetBlock(wantPot[0], wantPot[1], wantPot[2]); got != pot {
		t.Fatalf("potted red mushroom (local 1,3,5) = %v, want potted_red_mushroom %v", got, pot)
	}

	// (b) The full fingerprint over every placed (pos,state). The count includes the 4
	// fillColumnDown oak-log stilts that descend to the world floor in the isolated mapView
	// (vanilla-faithful; deterministic over the fixed y=64 sampler).
	count, hash := fingerprint(view)
	if count < 100 {
		t.Fatalf("placed block count %d is implausibly small for a swamp hut (truncated)", count)
	}
	// Pinned oracle (regenerate ONLY on an intentional geometry change). Captured from the
	// seed-5 (0,2) WEST-oriented hut over the fixed y=64/swamp stub.
	const wantCount = 640
	const wantHash = uint64(0xa2cda652a4805635)
	if count != wantCount {
		t.Fatalf("placed block count = %d, want pinned %d (geometry truncated/changed)", count, wantCount)
	}
	if hash != wantHash {
		t.Fatalf("swamp hut fingerprint = %#016x, want pinned %#016x (block/draw-order divergence)", hash, wantHash)
	}
}
