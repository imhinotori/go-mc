package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
)

// findAncientCityChunk locates the first ancient_cities structure chunk and a placement-failing
// chunk in a bounded scan.
func findAncientCityChunk(t *testing.T, seed int64, g *ancientCityStartGen) (passCX, passCZ, failCX, failCZ int) {
	t.Helper()
	pass, fail := false, false
	for cx := 0; cx < 400 && !(pass && fail); cx++ {
		for cz := 0; cz < 400 && !(pass && fail); cz++ {
			place := g.placement.IsStructureChunk(seed, cx, cz)
			if place && !pass {
				passCX, passCZ, pass = cx, cz, true
			} else if !place && !fail {
				failCX, failCZ, fail = cx, cz, true
			}
		}
	}
	if !pass {
		t.Fatalf("no ancient_city chunk found in 400x400 at seed %d", seed)
	}
	if !fail {
		t.Fatalf("no placement-failing chunk found")
	}
	return
}

// TestAncientCityRegisteredAndConfig proves the ancient_city structure decodes its placement,
// size, max_distance, constant start-height (-27), deep_dark biome, and start_pool.
func TestAncientCityRegisteredAndConfig(t *testing.T) {
	g, err := NewAncientCityStartGen()
	if err != nil {
		t.Fatalf("NewAncientCityStartGen: %v", err)
	}
	ag := g.(*ancientCityStartGen)

	if ag.placement.Salt != 20083232 || ag.placement.Spacing != 24 || ag.placement.Separation != 8 {
		t.Fatalf("placement: salt=%d spacing=%d separation=%d; want 20083232/24/8",
			ag.placement.Salt, ag.placement.Spacing, ag.placement.Separation)
	}
	if ag.maxDepth != 7 || ag.maxDistance != 116 {
		t.Fatalf("config: maxDepth=%d maxDistance=%d; want 7/116", ag.maxDepth, ag.maxDistance)
	}
	if ag.startY != -27 {
		t.Fatalf("constant start Y=%d; want -27", ag.startY)
	}
	if !ag.biomeAllow["minecraft:deep_dark"] {
		t.Fatalf("biome allow-set missing minecraft:deep_dark")
	}
	if ag.biomeAllow["minecraft:plains"] {
		t.Fatalf("ancient_city must NOT allow minecraft:plains")
	}
	if ag.startPool == nil || ag.startPool.Size() == 0 {
		t.Fatalf("ancient_city start pool empty (city_center must have 3 elements)")
	}
}

// TestAncientCityPlacement proves it places real jigsaw pieces at a known seed's structure chunk,
// at the fixed start Y (-27), gates on deep_dark, and is deterministic.
func TestAncientCityPlacement(t *testing.T) {
	const seed = int64(0x0DEC)
	g, err := NewAncientCityStartGen()
	if err != nil {
		t.Fatalf("NewAncientCityStartGen: %v", err)
	}
	ag := g.(*ancientCityStartGen)
	passCX, passCZ, failCX, failCZ := findAncientCityChunk(t, seed, ag)

	sampler := deepUndergroundSampler{}
	biome := fixedBiome(t, "minecraft:deep_dark")

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(starts) != 1 {
		t.Fatalf("expected 1 ancient_city start at (%d,%d), got %d", passCX, passCZ, len(starts))
	}
	ss := starts[0]
	if ss.Structure != "minecraft:ancient_city" {
		t.Fatalf("start structure = %q", ss.Structure)
	}
	if len(ss.Pieces) == 0 {
		t.Fatalf("ancient_city start has no pieces")
	}
	// Fixed start Y -27, rigid, no projection: the root sits near -27.
	if ss.BBox.MinY < -64 || ss.BBox.MinY > 0 {
		t.Fatalf("ancient_city bbox minY=%d not near start Y -27", ss.BBox.MinY)
	}

	// A non-deep_dark biome yields nothing.
	wrong := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, fixedBiome(t, "minecraft:plains"))
	if len(wrong) != 0 {
		t.Fatalf("non-deep_dark biome produced %d ancient_city starts; want 0", len(wrong))
	}

	none := g.GenerateStarts(seed, level.ChunkPos{int32(failCX), int32(failCZ)}, sampler, biome)
	if len(none) != 0 {
		t.Fatalf("placement-failing chunk produced %d starts", len(none))
	}

	again := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(again) != 1 || len(again[0].Pieces) != len(ss.Pieces) || again[0].BBox != ss.BBox {
		t.Fatalf("non-deterministic ancient_city start")
	}
}
