package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// snowyType resolves a "minecraft:snowy_plains" biome Type (in the igloo allow-set).
var snowyType = mustBiome("minecraft:snowy_plains")

func snowyBiomeAt(int, int, int) levelbiome.Type { return snowyType }

// igloo test anchors (derived FROM the placement algorithm + the basement-present draw):
//   - BASEMENT: seed 7 chunk (0,2) -> nextDouble()<0.5 true -> dome + basement + ladder (3 pieces)
//   - DOMEONLY: seed 190 chunk (2,1) -> nextDouble()>=0.5 -> dome only (1 piece)
const (
	iglooBasementSeed   = int64(7)
	iglooBasementChunkX = 0
	iglooBasementChunkZ = 2

	iglooDomeSeed   = int64(190)
	iglooDomeChunkX = 2
	iglooDomeChunkZ = 1
)

// iglooSurfaceSampler is a fixed y=80 stub (snowy mountains sit high; above the no sea-gate).
type iglooSurfaceSampler struct{}

func (iglooSurfaceSampler) SampleSurfaceY(int, int) int { return 80 }

func placeIgloo(t *testing.T, seed int64, cx, cz int) (*mapView, *StructureStart) {
	t.Helper()
	gen, err := NewIglooStartGen()
	if err != nil {
		t.Fatal(err)
	}
	starts := gen.GenerateStarts(seed, level.ChunkPos{int32(cx), int32(cz)}, iglooSurfaceSampler{}, snowyBiomeAt)
	if len(starts) != 1 {
		t.Fatalf("expected exactly 1 igloo start at (%d,%d), got %d", cx, cz, len(starts))
	}
	view := newMapView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)
	for _, pc := range starts[0].Pieces {
		pc.PostProcess(view, fullBox(), level.ChunkPos{int32(cx), int32(cz)}, rng)
	}
	return view, starts[0]
}

// TestIglooExpectedChunk: the owning chunk is derived FROM the placement algorithm with the
// IGLOO salt 14357618 (cross-checked vs the embedded structure_set).
func TestIglooExpectedChunk(t *testing.T) {
	placement := RandomSpreadStructurePlacement{Spacing: iglooSpacing, Separation: iglooSeparation, Salt: iglooSalt, SpreadType: SpreadLinear}
	got := placement.PotentialStructureChunk(iglooBasementSeed, iglooBasementChunkX, iglooBasementChunkZ)
	if got != (level.ChunkPos{iglooBasementChunkX, iglooBasementChunkZ}) {
		t.Fatalf("seed %d region of (%d,%d) -> potential chunk %v, want the owning chunk itself", iglooBasementSeed, iglooBasementChunkX, iglooBasementChunkZ, got)
	}
	if !placement.IsStructureChunk(iglooBasementSeed, iglooBasementChunkX, iglooBasementChunkZ) {
		t.Fatalf("(%d,%d) is not its own region's structure chunk for seed %d", iglooBasementChunkX, iglooBasementChunkZ, iglooBasementSeed)
	}
	if iglooSalt == desertPyramidSalt || iglooSalt == jungleTempleSalt || iglooSalt == swampHutSalt {
		t.Fatalf("igloo salt %d must be unique vs the other temples", iglooSalt)
	}
}

// TestIglooBiomeGate: a non-snowy origin yields NO start (the biome check is load-bearing).
func TestIglooBiomeGate(t *testing.T) {
	gen, err := NewIglooStartGen()
	if err != nil {
		t.Fatal(err)
	}
	outOfBiome := func(int, int, int) levelbiome.Type { return mustBiome("minecraft:plains") }
	starts := gen.GenerateStarts(iglooBasementSeed, level.ChunkPos{iglooBasementChunkX, iglooBasementChunkZ}, iglooSurfaceSampler{}, outOfBiome)
	if len(starts) != 0 {
		t.Fatalf("a non-snowy origin produced %d starts, want 0 (accept-by-default leaked)", len(starts))
	}
}

// TestIglooStartPure: the start is PURE over (seed,pos) — including the basement-present draw.
func TestIglooStartPure(t *testing.T) {
	gen, err := NewIglooStartGen()
	if err != nil {
		t.Fatal(err)
	}
	a := gen.GenerateStarts(iglooBasementSeed, level.ChunkPos{iglooBasementChunkX, iglooBasementChunkZ}, iglooSurfaceSampler{}, snowyBiomeAt)
	b := gen.GenerateStarts(iglooBasementSeed, level.ChunkPos{iglooBasementChunkX, iglooBasementChunkZ}, iglooSurfaceSampler{}, snowyBiomeAt)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 start each, got %d and %d", len(a), len(b))
	}
	if a[0].ChunkPos != b[0].ChunkPos || a[0].BBox != b[0].BBox || len(a[0].Pieces) != len(b[0].Pieces) {
		t.Fatalf("start not pure over (seed,pos): %+v vs %+v", a[0], b[0])
	}
}

// TestIglooDomeAlways: the dome (snow_block) is ALWAYS placed (both the basement + dome-only
// seeds), with the full dome fingerprint.
func TestIglooDomeAlways(t *testing.T) {
	view, start := placeIgloo(t, iglooDomeSeed, iglooDomeChunkX, iglooDomeChunkZ)
	if len(start.Pieces) != 1 {
		t.Fatalf("dome-only igloo (seed %d) has %d pieces, want 1 (no basement)", iglooDomeSeed, len(start.Pieces))
	}
	// A snow_block must be present (the dome is delivered).
	snow := stateOf(block.SnowBlock{})
	found := false
	for _, st := range view.blocks {
		if st == snow {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("dome-only igloo placed no snow_block (dome truncated)")
	}
	// The dome-only fingerprint (seed 190, (2,1), NORTH orientation = identity).
	count, hash := fingerprint(view)
	const wantCount = 152
	const wantHash = uint64(0x705ebd3260550b81)
	if count != wantCount || hash != wantHash {
		t.Fatalf("dome-only fingerprint = (%d, %#016x), want pinned (%d, %#016x)", count, hash, wantCount, wantHash)
	}
}

// TestIglooBasementProbabilistic: a seed drawing nextDouble()<0.5 adds the ladder + basement
// pieces (the first addChildren multi-piece set); the basement chest block is placed (loot
// deferred). The full 3-piece fingerprint is pinned.
func TestIglooBasementProbabilistic(t *testing.T) {
	view, start := placeIgloo(t, iglooBasementSeed, iglooBasementChunkX, iglooBasementChunkZ)
	if len(start.Pieces) != 3 {
		t.Fatalf("basement igloo (seed %d) has %d pieces, want 3 (dome + basement + ladder)", iglooBasementSeed, len(start.Pieces))
	}
	// The basement chest is recorded (loot deferred) + placed as a chest block.
	chests := 0
	for _, pc := range start.Pieces {
		if ip, ok := pc.(*IglooPiece); ok {
			for _, lc := range ip.LootChests {
				chests++
				if lc.LootTable != iglooBasementLoot {
					t.Fatalf("igloo chest loot table = %q, want %q", lc.LootTable, iglooBasementLoot)
				}
				if block.StateList[view.GetBlock(lc.X, lc.Y, lc.Z)].ID() != "minecraft:chest" {
					t.Fatalf("igloo chest (%d,%d,%d) does not hold a chest block", lc.X, lc.Y, lc.Z)
				}
			}
		}
	}
	if chests != 1 {
		t.Fatalf("basement igloo placed %d chests, want 1", chests)
	}
	// The full 3-piece fingerprint (seed 7, (0,2), EAST orientation).
	count, hash := fingerprint(view)
	const wantCount = 357
	const wantHash = uint64(0x8b891d28bdfa0c7b)
	if count != wantCount || hash != wantHash {
		t.Fatalf("basement fingerprint = (%d, %#016x), want pinned (%d, %#016x)", count, hash, wantCount, wantHash)
	}
}
