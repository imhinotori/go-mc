package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// mansionSurfaceSampler is a flat surface sampler well above the Y<60 gate (so the surface gate
// passes and the mansion assembles).
type mansionSurfaceSampler struct{}

func (mansionSurfaceSampler) SampleSurfaceY(int, int) int { return 80 }

// findMansionChunk locates the first woodland_mansions structure chunk + a placement-failing chunk.
func findMansionChunk(t *testing.T, seed int64, g *woodlandMansionStartGen) (passCX, passCZ, failCX, failCZ int) {
	t.Helper()
	pass, fail := false, false
	for cx := -300; cx < 300 && !(pass && fail); cx++ {
		for cz := -300; cz < 300 && !(pass && fail); cz++ {
			place := g.placement.IsStructureChunk(seed, cx, cz)
			if place && !pass {
				passCX, passCZ, pass = cx, cz, true
			} else if !place && !fail {
				failCX, failCZ, fail = cx, cz, true
			}
		}
	}
	if !pass {
		t.Fatalf("no woodland_mansions chunk found near origin at seed %d", seed)
	}
	if !fail {
		t.Fatalf("no placement-failing chunk found")
	}
	return
}

// TestWoodlandMansionRegisteredAndConfig proves the mansion decodes its TRIANGULAR placement +
// dark_forest biomes.
func TestWoodlandMansionRegisteredAndConfig(t *testing.T) {
	g, err := NewWoodlandMansionStartGen()
	if err != nil {
		t.Fatalf("NewWoodlandMansionStartGen: %v", err)
	}
	mg := g.(*woodlandMansionStartGen)
	if mg.placement.Salt != 10387319 || mg.placement.Spacing != 80 || mg.placement.Separation != 20 {
		t.Fatalf("placement: salt=%d spacing=%d separation=%d; want 10387319/80/20",
			mg.placement.Salt, mg.placement.Spacing, mg.placement.Separation)
	}
	if mg.placement.SpreadType != SpreadTriangular {
		t.Fatalf("spread_type = %d; want triangular(%d)", mg.placement.SpreadType, SpreadTriangular)
	}
	if !mg.biomeAllow["minecraft:dark_forest"] {
		t.Fatalf("biome allow-set missing minecraft:dark_forest")
	}
	if mg.biomeAllow["minecraft:plains"] {
		t.Fatalf("mansion must NOT allow minecraft:plains")
	}
}

// TestMansionGridDeterministic proves the grid RNG produces a stable layout for a fixed seed: the
// baseGrid + thirdFloorGrid + floorRooms are identical across two builds with the same rng seeding,
// and the entrance/start-room cells are as the jar initializes them.
func TestMansionGridDeterministic(t *testing.T) {
	build := func() *mansionGrid {
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(0x4A115, 12, -7)
		return newMansionGrid(rng)
	}
	a := build()
	b := build()
	// The entrance + start room are fixed (entranceX=7, entranceY=4; startRoom 2x2 at (7,4)).
	if a.entranceX != 7 || a.entranceY != 4 {
		t.Fatalf("entrance = (%d,%d); want (7,4)", a.entranceX, a.entranceY)
	}
	if a.baseGrid.get(7, 4) != mgStartRoom {
		t.Fatalf("baseGrid[7][4] = %d; want START_ROOM(%d)", a.baseGrid.get(7, 4), mgStartRoom)
	}
	// The whole grid must be identical across two same-seed builds (determinism keystone).
	for x := 0; x < a.baseGrid.width; x++ {
		for y := 0; y < a.baseGrid.height; y++ {
			if a.baseGrid.get(x, y) != b.baseGrid.get(x, y) {
				t.Fatalf("baseGrid mismatch at (%d,%d): %d vs %d", x, y, a.baseGrid.get(x, y), b.baseGrid.get(x, y))
			}
			if a.thirdFloorGrid.get(x, y) != b.thirdFloorGrid.get(x, y) {
				t.Fatalf("thirdFloorGrid mismatch at (%d,%d)", x, y)
			}
			for f := 0; f < 3; f++ {
				if a.floorRooms[f].get(x, y) != b.floorRooms[f].get(x, y) {
					t.Fatalf("floorRooms[%d] mismatch at (%d,%d)", f, x, y)
				}
			}
		}
	}
}

// TestWoodlandMansionPlacement proves the mansion places real pieces at a known seed's structure
// chunk (above the Y<60 gate + a dark_forest origin), and yields nothing on a non-dark-forest biome,
// a below-gate surface, and a placement-failing chunk. Also proves determinism.
func TestWoodlandMansionPlacement(t *testing.T) {
	const seed = int64(0x4A115)
	g, err := NewWoodlandMansionStartGen()
	if err != nil {
		t.Fatalf("NewWoodlandMansionStartGen: %v", err)
	}
	mg := g.(*woodlandMansionStartGen)
	passCX, passCZ, failCX, failCZ := findMansionChunk(t, seed, mg)

	sampler := mansionSurfaceSampler{}
	biome := fixedBiome(t, "minecraft:dark_forest")

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(starts) != 1 {
		t.Fatalf("expected 1 mansion start at (%d,%d), got %d", passCX, passCZ, len(starts))
	}
	ss := starts[0]
	if ss.Structure != "minecraft:mansion" {
		t.Fatalf("start structure = %q", ss.Structure)
	}
	// A mansion is a big multi-piece structure (walls + roof + corridors + rooms): dozens of pieces.
	if len(ss.Pieces) < 20 {
		t.Fatalf("mansion produced only %d pieces; expected many (walls/roof/rooms)", len(ss.Pieces))
	}

	// Non-dark-forest biome -> no start.
	wrong := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, fixedBiome(t, "minecraft:plains"))
	if len(wrong) != 0 {
		t.Fatalf("non-dark-forest biome produced %d mansion starts; want 0", len(wrong))
	}

	// Below-gate surface (Y<60) -> no start.
	lowStarts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, lowSurfaceSampler{}, biome)
	if len(lowStarts) != 0 {
		t.Fatalf("below-gate surface produced %d mansion starts; want 0", len(lowStarts))
	}

	none := g.GenerateStarts(seed, level.ChunkPos{int32(failCX), int32(failCZ)}, sampler, biome)
	if len(none) != 0 {
		t.Fatalf("placement-failing chunk produced %d starts", len(none))
	}

	again := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(again) != 1 || len(again[0].Pieces) != len(ss.Pieces) || again[0].BBox != ss.BBox {
		t.Fatalf("non-deterministic mansion start")
	}
}

// lowSurfaceSampler reports a surface below the Y<60 mansion gate.
type lowSurfaceSampler struct{}

func (lowSurfaceSampler) SampleSurfaceY(int, int) int { return 40 }
