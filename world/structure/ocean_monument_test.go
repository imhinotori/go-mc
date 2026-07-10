package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// oceanMonumentSampler: a flat ocean floor at y=45 (below sea).
type oceanMonumentSampler struct{}

func (oceanMonumentSampler) SampleSurfaceY(int, int) int { return 45 }

// deepOceanBiomeAt returns minecraft:deep_ocean (a member of #has_structure/ocean_monument).
func deepOceanBiomeAt(int, int, int) levelbiome.Type { return mustBiome("minecraft:deep_ocean") }

// findOceanMonumentChunk scans a region for the seed's owning chunk (derived FROM the placement
// algorithm; TRIANGULAR spread).
func findOceanMonumentChunk(t *testing.T, seed int64) (level.ChunkPos, bool) {
	t.Helper()
	placement := RandomSpreadStructurePlacement{Spacing: oceanMonumentSpacing, Separation: oceanMonumentSeparation, Salt: oceanMonumentSalt, SpreadType: SpreadTriangular, Frequency: 1.0}
	for cx := 0; cx < oceanMonumentSpacing; cx++ {
		for cz := 0; cz < oceanMonumentSpacing; cz++ {
			if placement.IsStructureChunk(seed, cx, cz) {
				return level.ChunkPos{int32(cx), int32(cz)}, true
			}
		}
	}
	return level.ChunkPos{}, false
}

// TestOceanMonumentPlacementParams pins salt/spacing/separation + TRIANGULAR spread.
func TestOceanMonumentPlacementParams(t *testing.T) {
	set, err := LoadStructureSet("minecraft:ocean_monuments")
	if err != nil {
		t.Fatal(err)
	}
	if set.Placement.Salt != oceanMonumentSalt || set.Placement.Spacing != oceanMonumentSpacing || set.Placement.Separation != oceanMonumentSeparation {
		t.Fatalf("monument placement = salt %d spacing %d sep %d, want %d/%d/%d",
			set.Placement.Salt, set.Placement.Spacing, set.Placement.Separation, oceanMonumentSalt, oceanMonumentSpacing, oceanMonumentSeparation)
	}
	if set.Placement.SpreadType != SpreadTriangular {
		t.Fatalf("monument spread = %v, want TRIANGULAR", set.Placement.SpreadType)
	}
}

// TestOceanMonumentRoomGraphDeterministic: generateRoomGraph is deterministic for a fixed RNG seed
// (the load-bearing room-grid algorithm). Two runs give the identical room count + claimed set.
func TestOceanMonumentRoomGraphDeterministic(t *testing.T) {
	run := func() (int, int) {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(42, 3, 7)
		list, _, core := generateRoomGraph(rng)
		claimed := 0
		for _, rd := range list {
			if rd.claimed {
				claimed++
			}
		}
		return len(list), claimed*1000 + core.index
	}
	n1, s1 := run()
	n2, s2 := run()
	if n1 != n2 || s1 != s2 {
		t.Fatalf("room graph not deterministic: (%d,%d) vs (%d,%d)", n1, s1, n2, s2)
	}
	// The grid has 46 cells (5*4 + 5*4 + 3*2 = 20+20+6) plus the 3 specials = 49 entries.
	if n1 != 49 {
		t.Fatalf("room graph list len = %d, want 49 (46 grid cells + 3 special)", n1)
	}
}

// TestOceanMonumentBuilding: the start builds a MonumentBuilding with the 58x23x58 bbox + children.
func TestOceanMonumentBuilding(t *testing.T) {
	gen, err := NewOceanMonumentStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(555)
	owner, ok := findOceanMonumentChunk(t, seed)
	if !ok {
		t.Skip("no monument owner chunk in scan window")
	}
	starts := gen.GenerateStarts(seed, owner, oceanMonumentSampler{}, deepOceanBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected 1 start, got %d", len(starts))
	}
	building, ok := starts[0].Pieces[0].(*MonumentBuilding)
	if !ok {
		t.Fatalf("piece is %T, want *MonumentBuilding", starts[0].Pieces[0])
	}
	bb := building.BoundingBox()
	// A NORTH/SOUTH orientation keeps 58x58; EAST/WEST swaps but stays 58x58 (square). Height 23.
	w := bb.MaxX - bb.MinX + 1
	d := bb.MaxZ - bb.MinZ + 1
	h := bb.MaxY - bb.MinY + 1
	if w != monumentWidth || d != monumentDepth || h != monumentHeight {
		t.Fatalf("monument bbox = %dx%dx%d, want %dx%dx%d", w, h, d, monumentWidth, monumentHeight, monumentDepth)
	}
	if len(building.children) < 3 {
		t.Fatalf("monument has %d children, want >= 3 (entry + core + wings)", len(building.children))
	}
}

// TestOceanMonumentBiomeGate: a non-deep-ocean biome yields NO start.
func TestOceanMonumentBiomeGate(t *testing.T) {
	gen, err := NewOceanMonumentStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(555)
	owner, ok := findOceanMonumentChunk(t, seed)
	if !ok {
		t.Skip("no owner chunk")
	}
	badBiome := func(int, int, int) levelbiome.Type { return mustBiome("minecraft:plains") }
	if got := gen.GenerateStarts(seed, owner, oceanMonumentSampler{}, badBiome); len(got) != 0 {
		t.Fatalf("expected 0 starts in a non-ocean biome, got %d", len(got))
	}
}

// TestOceanMonumentPlacesPrismarine: the building places prismarine + water shell blocks.
func TestOceanMonumentPlacesPrismarine(t *testing.T) {
	gen, err := NewOceanMonumentStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(555)
	owner, ok := findOceanMonumentChunk(t, seed)
	if !ok {
		t.Skip("no owner chunk")
	}
	starts := gen.GenerateStarts(seed, owner, oceanMonumentSampler{}, deepOceanBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected 1 start, got %d", len(starts))
	}
	building := starts[0].Pieces[0].(*MonumentBuilding)
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, int(owner[0]), int(owner[1]))
	building.PostProcess(view, fullBox(), owner, rng)

	prismarine := stateOf(block.Prismarine{})
	bricks := stateOf(block.PrismarineBricks{})
	lantern := stateOf(block.SeaLantern{})
	var pris, brick, lamp int
	for _, st := range view.blocks {
		switch st {
		case prismarine:
			pris++
		case bricks:
			brick++
		case lantern:
			lamp++
		}
	}
	if pris == 0 {
		t.Fatalf("monument placed no prismarine (shell did not generate)")
	}
	if brick == 0 {
		t.Fatalf("monument placed no prismarine bricks (pillar grid / wing frame did not generate)")
	}
	if lamp == 0 {
		t.Fatalf("monument placed no sea lanterns")
	}
}

// TestOceanMonumentStartPure: pure over (seed,pos).
func TestOceanMonumentStartPure(t *testing.T) {
	gen, err := NewOceanMonumentStartGen()
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(555)
	owner, ok := findOceanMonumentChunk(t, seed)
	if !ok {
		t.Skip("no owner chunk")
	}
	a := gen.GenerateStarts(seed, owner, oceanMonumentSampler{}, deepOceanBiomeAt)
	b := gen.GenerateStarts(seed, owner, oceanMonumentSampler{}, deepOceanBiomeAt)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 start each")
	}
	if a[0].BBox != b[0].BBox {
		t.Fatalf("monument start not pure: %+v vs %+v", a[0].BBox, b[0].BBox)
	}
}
