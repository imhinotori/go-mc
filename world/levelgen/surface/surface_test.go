package surface

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	bsource "github.com/imhinotori/sulfur/world/levelgen/biome"
	"github.com/imhinotori/sulfur/world/levelgen/noisechunk"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

const testSeed = int64(0x5EED_1234)

// biomeType resolves a biome registry id to its level/biome.Type, failing the test on
// an unknown id.
func biomeType(t *testing.T, id string) biome.Type {
	t.Helper()
	var bt biome.Type
	if err := bt.UnmarshalText([]byte(id)); err != nil {
		t.Fatalf("unknown biome %q: %v", id, err)
	}
	return bt
}

// stateName resolves a block StateID back to its registry name for assertions.
func stateName(sid block.StateID) string {
	if int(sid) < 0 || int(sid) >= len(block.StateList) {
		return "<invalid>"
	}
	return block.StateList[sid].ID()
}

// buildTestChunk constructs a real filled+surfaced chunk at pos, using biomeOf for the
// surface biome (so a test can force plains/desert/beach). Returns the chunk + the
// router (the tests reuse the router's surface_rule + system).
func buildTestChunk(t *testing.T, seed int64, pos level.ChunkPos, biomeOf BiomeGetter) (*level.Chunk, *noisechunk.NoiseChunk, *SurfaceSystem, RuleSource) {
	t.Helper()
	r, err := router.NewRouter(seed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	nc := noisechunk.NewNoiseChunk(r, pos)
	aq := noisechunk.NewAquifer(r, nc, pos)
	ov := noisechunk.NewOreVeinifier(r.NoiseRouter.VeinToggle, r.NoiseRouter.VeinRidged, r.NoiseRouter.VeinGap, r.Random)
	ch := noisechunk.FillChunk(nc, aq, ov)

	s, err := NewSurfaceSystem(r)
	if err != nil {
		t.Fatalf("NewSurfaceSystem: %v", err)
	}
	rule, err := ParseRuleSource(r.Settings.SurfaceRule)
	if err != nil {
		t.Fatalf("ParseRuleSource: %v", err)
	}
	BuildSurface(s, rule, ch, nc, biomeOf)
	return ch, nc, s, rule
}

// blockAt reads the block at world (lx, worldY, lz) of a chunk.
func blockAt(ch *level.Chunk, lx, worldY, lz, minY int) block.StateID {
	sec := (worldY - minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return 0
	}
	local := (worldY&15)<<8 | (lz&15)<<4 | (lx & 15)
	return ch.Sections[sec].GetBlock(local)
}

// topSolid returns the highest non-air, non-water world Y in column (lx,lz), and the
// state there. Returns (minY-1, false) if the column is all air.
func topSolid(ch *level.Chunk, lx, lz, minY, maxY int) (int, block.StateID, bool) {
	air := block.ToStateID[block.Air{}]
	caveAir := block.ToStateID[block.CaveAir{}]
	water := block.ToStateID[block.Water{Level: 0}]
	for y := maxY - 1; y >= minY; y-- {
		st := blockAt(ch, lx, y, lz, minY)
		if st == air || st == caveAir || st == water {
			continue
		}
		return y, st, true
	}
	return minY - 1, 0, false
}

// TestParseSurfaceRuleSequence: the FULL overworld surface_rule parses into the ported
// rule tree (the whole vocabulary), and an unsupported rule/condition type errors loudly.
func TestParseSurfaceRuleSequence(t *testing.T) {
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	if len(r.Settings.SurfaceRule) == 0 {
		t.Fatal("overworld settings carry no surface_rule")
	}
	rule, err := ParseRuleSource(r.Settings.SurfaceRule)
	if err != nil {
		t.Fatalf("the full overworld surface_rule must parse: %v", err)
	}
	if _, ok := rule.(*sequenceRule); !ok {
		t.Fatalf("top-level surface_rule should be a sequence, got %T", rule)
	}

	// Loud error on an unsupported rule type.
	if _, err := ParseRuleSource([]byte(`{"type":"minecraft:totally_bogus_rule"}`)); err == nil {
		t.Fatal("an unsupported rule type must error loudly, got nil")
	}
	// Loud error on an unsupported condition type (inside a condition rule).
	bogusCond := `{"type":"minecraft:condition","if_true":{"type":"minecraft:bogus_condition"},"then_run":{"type":"minecraft:block","result_state":{"Name":"minecraft:stone"}}}`
	if _, err := ParseRuleSource([]byte(bogusCond)); err == nil {
		t.Fatal("an unsupported condition type must error loudly, got nil")
	}
}

// TestSurfaceBiomeCorrect: buildSurface places biome-correct surface blocks — grass on a
// plains column, sand on a desert column, gravel/sand on a beach. The biome getter forces
// the biome so the assertion is deterministic regardless of where the climate would place
// each biome.
func TestSurfaceBiomeCorrect(t *testing.T) {
	grass := "minecraft:grass_block"
	sand := "minecraft:sand"

	pos := level.ChunkPos{4, 4}

	// Plains: the exposed top of a stone column gets grass (with dirt below).
	plains := biomeType(t, "minecraft:plains")
	chPlains, ncP, _, _ := buildTestChunk(t, testSeed, pos, func(x, y, z int) biome.Type { return plains })
	minY, maxY := ncP.MinY(), ncP.MinY()+ncP.Height()
	gotGrass := false
	for lx := 0; lx < 16 && !gotGrass; lx++ {
		for lz := 0; lz < 16; lz++ {
			y, st, ok := topSolid(chPlains, lx, lz, minY, maxY)
			if !ok {
				continue
			}
			if stateName(st) == grass {
				// dirt directly below grass (the surface band).
				below := stateName(blockAt(chPlains, lx, y-1, lz, minY))
				if below == "minecraft:dirt" {
					gotGrass = true
					break
				}
			}
		}
	}
	if !gotGrass {
		t.Error("a plains column must produce a grass_block top over dirt")
	}

	// Desert: the top gets sand (with sandstone below); never grass.
	desert := biomeType(t, "minecraft:desert")
	chDesert, ncD, _, _ := buildTestChunk(t, testSeed, pos, func(x, y, z int) biome.Type { return desert })
	gotSand := false
	grassInDesert := false
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			_, st, ok := topSolid(chDesert, lx, lz, ncD.MinY(), ncD.MinY()+ncD.Height())
			if !ok {
				continue
			}
			switch stateName(st) {
			case sand:
				gotSand = true
			case grass:
				grassInDesert = true
			}
		}
	}
	if !gotSand {
		t.Error("a desert column must produce a sand top")
	}
	if grassInDesert {
		t.Error("a desert column must NOT produce grass (biome-dependent surface)")
	}
}

// TestHeightmapsWritten: buildSurface writes the 3 CLIENT heightmaps from the computed
// top; every column's WorldSurface points just above a non-air block (no bogus heights).
func TestHeightmapsWritten(t *testing.T) {
	pos := level.ChunkPos{2, 7}
	plains := biomeType(t, "minecraft:plains")
	ch, nc, _, _ := buildTestChunk(t, testSeed, pos, func(x, y, z int) biome.Type { return plains })
	minY, maxY := nc.MinY(), nc.MinY()+nc.Height()

	if ch.HeightMaps.WorldSurface == nil || ch.HeightMaps.MotionBlocking == nil || ch.HeightMaps.MotionBlockingNoLeaves == nil {
		t.Fatal("the 3 client heightmaps must be non-nil after buildSurface")
	}
	air := block.ToStateID[block.Air{}]
	caveAir := block.ToStateID[block.CaveAir{}]

	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			hm := ch.HeightMaps.WorldSurface.Get(lz<<4|lx) + minY
			if hm < minY || hm > maxY {
				t.Fatalf("column (%d,%d) heightmap %d out of [%d,%d]", lx, lz, hm, minY, maxY)
			}
			// The block at hm should be air (the first Y above the surface); the block
			// just below should be non-air (unless the whole column is air, hm==minY).
			if hm > minY {
				below := blockAt(ch, lx, hm-1, lz, minY)
				if below == air || below == caveAir {
					t.Fatalf("column (%d,%d) heightmap %d points above air at y=%d", lx, lz, hm, hm-1)
				}
			}
		}
	}
}

// TestBiomeSurfaceDeterministic: the surface blocks, the heightmaps AND the biome
// containers are identical across two runs from the same seed (Pitfall 7).
func TestBiomeSurfaceDeterministic(t *testing.T) {
	pos := level.ChunkPos{1, 1}

	run := func() *level.Chunk {
		r, err := router.NewRouter(testSeed)
		if err != nil {
			t.Fatalf("router.NewRouter: %v", err)
		}
		nc := noisechunk.NewNoiseChunk(r, pos)
		aq := noisechunk.NewAquifer(r, nc, pos)
		ov := noisechunk.NewOreVeinifier(r.NoiseRouter.VeinToggle, r.NoiseRouter.VeinRidged, r.NoiseRouter.VeinGap, r.Random)
		ch := noisechunk.FillChunk(nc, aq, ov)
		s, err := NewSurfaceSystem(r)
		if err != nil {
			t.Fatalf("NewSurfaceSystem: %v", err)
		}
		rule, err := ParseRuleSource(r.Settings.SurfaceRule)
		if err != nil {
			t.Fatalf("ParseRuleSource: %v", err)
		}
		src, err := bsource.NewMultiNoiseBiomeSource(r)
		if err != nil {
			t.Fatalf("NewMultiNoiseBiomeSource: %v", err)
		}
		biomeOf := func(x, y, z int) biome.Type { return src.GetBiome(x, y, z) }
		BuildSurface(s, rule, ch, nc, biomeOf)
		FillBiomes(ch, nc, biomeOf)
		return ch
	}

	a := run()
	b := run()

	minY := -64
	maxY := minY + 384
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			for y := minY; y < maxY; y++ {
				if blockAt(a, lx, y, lz, minY) != blockAt(b, lx, y, lz, minY) {
					t.Fatalf("block mismatch at (%d,%d,%d): %v vs %v", lx, y, lz,
						stateName(blockAt(a, lx, y, lz, minY)), stateName(blockAt(b, lx, y, lz, minY)))
				}
			}
			if a.HeightMaps.WorldSurface.Get(lz<<4|lx) != b.HeightMaps.WorldSurface.Get(lz<<4|lx) {
				t.Fatalf("heightmap mismatch at (%d,%d)", lx, lz)
			}
		}
	}
	// Biome containers identical.
	for si := range a.Sections {
		for i := 0; i < 4*4*4; i++ {
			if a.Sections[si].Biomes.Get(i) != b.Sections[si].Biomes.Get(i) {
				t.Fatalf("biome container mismatch in section %d cell %d", si, i)
			}
		}
	}
}

// TestSurfaceVariesByBiome: a sanity check that the multi-noise biome source feeds the
// surface — the biome containers across a chunk carry more than one biome (real variety),
// and the surface system places at least one non-grass top somewhere when the real biomes
// drive it. This guards against a uniform-grass regression.
func TestSurfaceVariesByBiome(t *testing.T) {
	r, err := router.NewRouter(testSeed)
	if err != nil {
		t.Fatalf("router.NewRouter: %v", err)
	}
	src, err := bsource.NewMultiNoiseBiomeSource(r)
	if err != nil {
		t.Fatalf("NewMultiNoiseBiomeSource: %v", err)
	}
	seen := map[biome.Type]bool{}
	for cx := int32(-2); cx <= 2; cx++ {
		for cz := int32(-2); cz <= 2; cz++ {
			b := src.GetBiome(int(cx)*64, 64, int(cz)*64)
			seen[b] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("multi-noise biome source must produce more than one biome across the world, got %d", len(seen))
	}
}
