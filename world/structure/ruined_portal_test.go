package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// ruinedPortalSurfaceSampler: a flat surface at y=70 (above sea) for the on_land_surface variant.
type ruinedPortalSurfaceSampler struct{}

func (ruinedPortalSurfaceSampler) SampleSurfaceY(int, int) int { return 70 }

// rpPlainsBiomeAt returns minecraft:plains (a member of #has_structure/ruined_portal_standard).
func rpPlainsBiomeAt(int, int, int) levelbiome.Type { return mustBiome("minecraft:plains") }

// findRuinedPortalChunk scans a region for the seed's owning ruined_portal chunk (derived FROM the
// placement algorithm, not eyeballed).
func findRuinedPortalChunk(t *testing.T, seed int64) (level.ChunkPos, bool) {
	t.Helper()
	placement := RandomSpreadStructurePlacement{Spacing: ruinedPortalSpacing, Separation: ruinedPortalSeparation, Salt: ruinedPortalSalt, SpreadType: SpreadLinear, Frequency: 1.0}
	for cx := 0; cx < ruinedPortalSpacing; cx++ {
		for cz := 0; cz < ruinedPortalSpacing; cz++ {
			if placement.IsStructureChunk(seed, cx, cz) {
				return level.ChunkPos{int32(cx), int32(cz)}, true
			}
		}
	}
	return level.ChunkPos{}, false
}

// TestRuinedPortalPlacementParams pins the salt/spacing/separation from the structure_set JSON.
func TestRuinedPortalPlacementParams(t *testing.T) {
	set, err := LoadStructureSet("minecraft:ruined_portals")
	if err != nil {
		t.Fatal(err)
	}
	if set.Placement.Salt != ruinedPortalSalt || set.Placement.Spacing != ruinedPortalSpacing || set.Placement.Separation != ruinedPortalSeparation {
		t.Fatalf("ruined_portal placement = salt %d spacing %d sep %d, want %d/%d/%d",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation, ruinedPortalSalt, ruinedPortalSpacing, ruinedPortalSeparation)
	}
	if set.Placement.SpreadType != SpreadLinear {
		t.Fatalf("ruined_portal spread = %v, want LINEAR", set.Placement.SpreadType)
	}
}

// TestRuinedPortalStartPure: the start decision is pure over (seed,pos).
func TestRuinedPortalStartPure(t *testing.T) {
	gen, err := NewRuinedPortalStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(1234)
	owner, ok := findRuinedPortalChunk(t, seed)
	if !ok {
		t.Skip("no ruined_portal owner chunk in scan window for this seed")
	}
	a := gen.GenerateStarts(seed, owner, ruinedPortalSurfaceSampler{}, rpPlainsBiomeAt)
	b := gen.GenerateStarts(seed, owner, ruinedPortalSurfaceSampler{}, rpPlainsBiomeAt)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 start each, got %d and %d", len(a), len(b))
	}
	if a[0].ChunkPos != b[0].ChunkPos || a[0].BBox != b[0].BBox {
		t.Fatalf("start not pure: %+v vs %+v", a[0], b[0])
	}
}

// TestRuinedPortalBiomeGate: a non-allowed biome yields NO start (no accept-by-default).
func TestRuinedPortalBiomeGate(t *testing.T) {
	gen, err := NewRuinedPortalStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(1234)
	owner, ok := findRuinedPortalChunk(t, seed)
	if !ok {
		t.Skip("no owner chunk")
	}
	badBiome := func(int, int, int) levelbiome.Type { return mustBiome("minecraft:the_void") }
	if got := gen.GenerateStarts(seed, owner, ruinedPortalSurfaceSampler{}, badBiome); len(got) != 0 {
		t.Fatalf("expected 0 starts in a non-allowed biome, got %d", len(got))
	}
}

// TestRuinedPortalPlaces: the start places the netherrack ground scar (the blob + base). Asserts a
// non-trivial count of netherrack/magma blocks land in a covering view.
func TestRuinedPortalPlaces(t *testing.T) {
	gen, err := NewRuinedPortalStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(1234)
	owner, ok := findRuinedPortalChunk(t, seed)
	if !ok {
		t.Skip("no owner chunk")
	}
	starts := gen.GenerateStarts(seed, owner, ruinedPortalSurfaceSampler{}, rpPlainsBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected 1 start, got %d", len(starts))
	}
	start := starts[0]
	piece, ok := start.Pieces[0].(*RuinedPortalPiece)
	if !ok {
		t.Fatalf("piece is %T, want *RuinedPortalPiece", start.Pieces[0])
	}
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, int(owner[0]), int(owner[1]))
	piece.PostProcess(view, fullBox(), owner, rng)

	nr := stateOf(block.Netherrack{})
	mg := stateOf(block.MagmaBlock{})
	count := 0
	for _, st := range view.blocks {
		if st == nr || st == mg {
			count++
		}
	}
	if count == 0 {
		t.Fatalf("ruined_portal placed no netherrack/magma (blob + base did not generate)")
	}
	// The bbox floor base slab alone is spanX*spanZ netherrack; the blob adds more.
	bb := piece.BoundingBox()
	baseArea := (bb.MaxX - bb.MinX + 1) * (bb.MaxZ - bb.MinZ + 1)
	if count < baseArea {
		t.Fatalf("netherrack/magma count %d < base slab area %d (base did not fully place)", count, baseArea)
	}
}

// TestRuinedPortalDeterministicBBox: two builds at the same (seed,pos) give the identical bbox +
// vertical placement (the 6-draw RNG selection is deterministic).
func TestRuinedPortalDeterministicBBox(t *testing.T) {
	gen, err := NewRuinedPortalStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(777)
	owner, ok := findRuinedPortalChunk(t, seed)
	if !ok {
		t.Skip("no owner chunk")
	}
	a := gen.GenerateStarts(seed, owner, ruinedPortalSurfaceSampler{}, rpPlainsBiomeAt)
	b := gen.GenerateStarts(seed, owner, ruinedPortalSurfaceSampler{}, rpPlainsBiomeAt)
	if len(a) != 1 || len(b) != 1 {
		t.Skip("no start")
	}
	pa := a[0].Pieces[0].(*RuinedPortalPiece)
	pb := b[0].Pieces[0].(*RuinedPortalPiece)
	if pa.bbox != pb.bbox || pa.verticalPlacement != pb.verticalPlacement {
		t.Fatalf("non-deterministic: %+v/%v vs %+v/%v", pa.bbox, pa.verticalPlacement, pb.bbox, pb.verticalPlacement)
	}
}
