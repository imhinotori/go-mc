package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
)

// deepUndergroundSampler is a flat surface sampler for the underground jigsaw structures. Trial
// chambers + ancient city do NOT project to the surface (projectStartToHeightmap=empty), so the
// surface Y is never consulted for their placement; a fixed value keeps the sampler total.
type deepUndergroundSampler struct{}

func (deepUndergroundSampler) SampleSurfaceY(int, int) int { return 64 }

// fixedBiome returns a BiomeAt that reports the same biome everywhere (the deep-underground
// structures gate on the chunk-center biome at the fixed start Y).
func fixedBiome(t *testing.T, id string) BiomeAt {
	t.Helper()
	var bt levelbiome.Type
	if err := bt.UnmarshalText([]byte(id)); err != nil {
		t.Fatalf("bad biome id %q: %v", id, err)
	}
	return func(int, int, int) levelbiome.Type { return bt }
}

// findTrialChambersChunk locates the first trial_chambers structure chunk (isStructureChunk pass)
// and a placement-failing chunk in a bounded scan.
func findTrialChambersChunk(t *testing.T, seed int64, g *trialChambersStartGen) (passCX, passCZ, failCX, failCZ int) {
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
		t.Fatalf("no trial_chambers chunk found in 400x400 at seed %d", seed)
	}
	if !fail {
		t.Fatalf("no placement-failing chunk found")
	}
	return
}

// TestTrialChambersRegisteredAndConfig proves the trial_chambers structure decodes its placement,
// size, max_distance, uniform start-height band, biomes, and start_pool from the embedded data.
func TestTrialChambersRegisteredAndConfig(t *testing.T) {
	g, err := NewTrialChambersStartGen()
	if err != nil {
		t.Fatalf("NewTrialChambersStartGen: %v", err)
	}
	tg := g.(*trialChambersStartGen)

	if tg.placement.Salt != 94251327 || tg.placement.Spacing != 34 || tg.placement.Separation != 12 {
		t.Fatalf("placement: salt=%d spacing=%d separation=%d; want 94251327/34/12",
			tg.placement.Salt, tg.placement.Spacing, tg.placement.Separation)
	}
	if tg.maxDepth != 20 || tg.maxDistance != 116 {
		t.Fatalf("config: maxDepth=%d maxDistance=%d; want 20/116", tg.maxDepth, tg.maxDistance)
	}
	if tg.startYMin != -40 || tg.startYMax != -20 {
		t.Fatalf("uniform start band: [%d,%d]; want [-40,-20]", tg.startYMin, tg.startYMax)
	}
	if !tg.biomeAllow["minecraft:plains"] {
		t.Fatalf("biome allow-set missing minecraft:plains")
	}
	if tg.startPool == nil || tg.startPool.Size() == 0 {
		t.Fatalf("trial_chambers start pool empty (chamber/end must have 2 corridor elements)")
	}
}

// TestTrialChambersPlacement proves it places real jigsaw pieces at a known seed's structure
// chunk, at the low uniform start Y, and yields nothing on a placement-failing chunk. Also proves
// determinism.
func TestTrialChambersPlacement(t *testing.T) {
	const seed = int64(0x7C1A)
	g, err := NewTrialChambersStartGen()
	if err != nil {
		t.Fatalf("NewTrialChambersStartGen: %v", err)
	}
	tg := g.(*trialChambersStartGen)
	passCX, passCZ, failCX, failCZ := findTrialChambersChunk(t, seed, tg)

	sampler := deepUndergroundSampler{}
	biome := fixedBiome(t, "minecraft:plains")

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(starts) != 1 {
		t.Fatalf("expected 1 trial_chambers start at (%d,%d), got %d", passCX, passCZ, len(starts))
	}
	ss := starts[0]
	if ss.Structure != "minecraft:trial_chambers" {
		t.Fatalf("start structure = %q", ss.Structure)
	}
	if len(ss.Pieces) == 0 {
		t.Fatalf("trial_chambers start has no pieces")
	}
	// The uniform start band is [-40,-20]; the root sits in that band (rigid, no projection).
	if ss.BBox.MinY < -60 || ss.BBox.MinY > 0 {
		t.Fatalf("trial_chambers bbox minY=%d not near the [-40,-20] start band", ss.BBox.MinY)
	}

	// A wrong biome (deep_dark is NOT in the trial_chambers set... actually it is not) -> no start.
	wrong := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, fixedBiome(t, "minecraft:the_void"))
	if len(wrong) != 0 {
		t.Fatalf("non-allowed biome produced %d starts; want 0", len(wrong))
	}

	none := g.GenerateStarts(seed, level.ChunkPos{int32(failCX), int32(failCZ)}, sampler, biome)
	if len(none) != 0 {
		t.Fatalf("placement-failing chunk produced %d starts", len(none))
	}

	again := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(again) != 1 || len(again[0].Pieces) != len(ss.Pieces) || again[0].BBox != ss.BBox {
		t.Fatalf("non-deterministic trial_chambers start")
	}
}
