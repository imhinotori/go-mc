package world

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/structure"
)

func TestStructureFeaturePoolViewPlacesSupportedVillageFeature(t *testing.T) {
	view := testFeaturePoolNeighborhood()
	stone := block.ToStateID[block.Stone{}]
	for x := 3; x <= 13; x++ {
		for z := 3; z <= 13; z++ {
			view.SetBlock(x, 63, z, stone)
		}
	}

	var plains levelbiome.Type
	if err := plains.UnmarshalText([]byte("minecraft:plains")); err != nil {
		t.Fatalf("plains biome: %v", err)
	}
	adapter := structureFeaturePoolView{
		Neighborhood: view,
		registry:     feature.NewEmbeddedRegistry(),
		seaLevel:     63,
		biomeAt:      func(int, int, int) levelbiome.Type { return plains },
	}

	rng := levelgen.NewWorldgenRandom(12345)
	if !adapter.PlaceFeaturePoolElement("minecraft:pile_melon", structure.Pos{X: 8, Y: 64, Z: 8}, rng) {
		t.Fatal("PlaceFeaturePoolElement(pile_melon) returned false")
	}
	melon := block.ToStateID[block.Melon{}]
	placed := 0
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			if view.GetBlock(x, 64, z) == melon || view.GetBlock(x, 65, z) == melon {
				placed++
			}
		}
	}
	if placed == 0 {
		t.Fatal("PlaceFeaturePoolElement(pile_melon) placed no melon blocks")
	}
}

func TestStructureFeaturePoolViewDefersUnsupportedProviderWithoutRNG(t *testing.T) {
	view := testFeaturePoolNeighborhood()
	adapter := structureFeaturePoolView{
		Neighborhood: view,
		registry:     feature.NewEmbeddedRegistry(),
		seaLevel:     63,
		biomeAt:      func(int, int, int) levelbiome.Type { return 0 },
	}

	rng := levelgen.NewLegacyRandomSource(777)
	control := levelgen.NewLegacyRandomSource(777)
	if adapter.PlaceFeaturePoolElement("minecraft:flower_plain", structure.Pos{X: 8, Y: 64, Z: 8}, rng) {
		t.Fatal("deferred flower_plain unexpectedly reported placement")
	}
	if got, want := rng.NextLong(), control.NextLong(); got != want {
		t.Fatalf("deferred flower_plain consumed rng: nextLong=%d want %d", got, want)
	}
}

func testFeaturePoolNeighborhood() *Neighborhood {
	center := level.ChunkPos{0, 0}
	ch := level.EmptyChunk(8)
	return newNeighborhood(center, map[int64]*level.Chunk{packPos(center): ch}, 0, 128)
}
