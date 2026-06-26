package world

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
)

// noiseGenSeed is the fixed seed used across the NoiseGenerator tests (determinism +
// the full-parity feature assertions are all keyed off it).
const noiseGenSeed = int64(0x5EED_C0DE)

// stateNameOf resolves a block StateID back to its registry name for assertions.
func stateNameOf(sid block.StateID) string {
	if int(sid) < 0 || int(sid) >= len(block.StateList) {
		return "<invalid>"
	}
	return block.StateList[sid].ID()
}

// blockAtWorld reads the block at chunk-local (lx, worldY, lz) of a generated chunk.
func blockAtWorld(ch *level.Chunk, lx, worldY, lz, minY int) block.StateID {
	sec := (worldY - minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return 0
	}
	local := (worldY&15)<<8 | (lz&15)<<4 | (lx & 15)
	return ch.Sections[sec].GetBlock(local)
}

// TestNoiseGenImplementsGenerator: *NoiseGenerator is a drop-in world.Generator (the
// off-tick worker seam is reused untouched).
func TestNoiseGenImplementsGenerator(t *testing.T) {
	var _ Generator = (*NoiseGenerator)(nil)

	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	if g == nil {
		t.Fatal("NewNoiseGenerator returned nil")
	}
	// It satisfies the split interface (asserted above); the concrete single-chunk
	// Generate is what single-chunk callers use (Generate is NOT on the interface after
	// the GEN2-02 split — it stays a concrete method = GenerateTerrain then Decorate).
	ch := g.Generate(level.ChunkPos{0, 0})
	if ch == nil {
		t.Fatal("Generate returned nil chunk")
	}
	if got := len(ch.Sections); got != testSecs {
		t.Fatalf("section count = %d, want %d", got, testSecs)
	}
	if ch.Status != level.StatusFull {
		t.Fatalf("status = %q, want %q", ch.Status, level.StatusFull)
	}
}

// TestNoiseGenDeterministic: Generate is PURE (WORLD-04 / Pitfall 7). The same generator
// called twice for the same pos encodes to identical bytes, and two generators from the
// same seed agree.
func TestNoiseGenDeterministic(t *testing.T) {
	pos := level.ChunkPos{3, 7}

	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)

	var a, b bytes.Buffer
	if _, err := g.Generate(pos).WriteTo(&a); err != nil {
		t.Fatalf("first WriteTo: %v", err)
	}
	if _, err := g.Generate(pos).WriteTo(&b); err != nil {
		t.Fatalf("second WriteTo: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("Generate(%v) is not deterministic: %d vs %d bytes", pos, a.Len(), b.Len())
	}

	// A second generator from the same seed must agree byte-for-byte.
	g2 := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	var c bytes.Buffer
	if _, err := g2.Generate(pos).WriteTo(&c); err != nil {
		t.Fatalf("g2 WriteTo: %v", err)
	}
	if !bytes.Equal(a.Bytes(), c.Bytes()) {
		t.Fatalf("two generators from seed %d disagree on %v", noiseGenSeed, pos)
	}
}

// TestNoiseGenChunkComplete: a generated chunk is a complete, valid overworld chunk —
// solid ground (not all-air), water at sea level in low columns, varied biome containers
// (more than one biome across several chunks), valid 3 CLIENT heightmaps, and it
// round-trips the Phase-4 encoder without error.
func TestNoiseGenChunkComplete(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	minY := testMinY
	maxY := testMinY + testSecs*16

	air := block.ToStateID[block.Air{}]
	caveAir := block.ToStateID[block.CaveAir{}]
	water := block.ToStateID[block.Water{Level: 0}]
	seaLevel := 63

	anySolid := false
	anyWaterAtSea := false
	biomes := map[biome.Type]bool{}

	// Sample a span of chunks so the climate (and thus the biome containers + water)
	// genuinely varies.
	for cx := int32(-2); cx <= 2; cx++ {
		for cz := int32(-2); cz <= 2; cz++ {
			pos := level.ChunkPos{cx, cz}
			ch := g.Generate(pos)

			// Heightmaps present and in range.
			if ch.HeightMaps.WorldSurface == nil || ch.HeightMaps.MotionBlocking == nil || ch.HeightMaps.MotionBlockingNoLeaves == nil {
				t.Fatalf("chunk %v missing a client heightmap", pos)
			}
			for col := 0; col < 16*16; col++ {
				h := ch.HeightMaps.WorldSurface.Get(col) + minY
				if h < minY || h > maxY {
					t.Fatalf("chunk %v col %d heightmap %d out of [%d,%d]", pos, col, h, minY, maxY)
				}
			}

			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := minY; y < maxY; y++ {
						st := blockAtWorld(ch, lx, y, lz, minY)
						if st != air && st != caveAir && st != water {
							anySolid = true
						}
						if st == water && y >= seaLevel-2 && y <= seaLevel {
							anyWaterAtSea = true
						}
					}
				}
			}

			for si := range ch.Sections {
				bc := ch.Sections[si].Biomes
				if bc == nil {
					t.Fatalf("chunk %v section %d has nil biome container", pos, si)
				}
				for i := 0; i < 4*4*4; i++ {
					biomes[bc.Get(i)] = true
				}
			}

			// Round-trip the encoder.
			var buf bytes.Buffer
			if _, err := ch.WriteTo(&buf); err != nil {
				t.Fatalf("chunk %v WriteTo: %v", pos, err)
			}
			if buf.Len() == 0 {
				t.Fatalf("chunk %v encoded to 0 bytes", pos)
			}
		}
	}

	if !anySolid {
		t.Error("no solid ground produced across the sampled chunks (all-air world)")
	}
	if !anyWaterAtSea {
		t.Error("no water near sea level across the sampled chunks (terrain never dips to ocean)")
	}
	if len(biomes) < 2 {
		t.Errorf("biome containers carry only %d biome(s) across the sampled chunks; want >= 2 (varied, not single-plains)", len(biomes))
	}
}

// TestTerrainSanity is the Plan 09-09 (PARITY-01) automatable gate: across a sampling of
// generated chunks it asserts the FULL-PARITY terrain invariants AND the load-bearing
// spawn-standable property (T-9-26) that the visual gate depends on:
//
//   - the spawn column (chunk (0,0), block-center x=8 z=8) is STANDABLE: the derived
//     SpawnSurfaceY block is solid-or-water and the block two above (where the player is
//     placed) is air — the player lands on the terrain, not buried in a hill or in void;
//   - the WorldSurface heightmap VARIES across columns (hills/valleys, not flat);
//   - water sits at sea level in low columns (oceans/lakes, not dry basins);
//   - more than one biome appears across chunks (biome-varied, not single-plains);
//   - underground cave_air + air pockets exist (noise caves + carved tunnels/ravines);
//   - ore-vein blocks appear in the rock (OreVeinifier ran);
//   - a perched/deep aquifer fluid (water/lava away from sea level) appears;
//   - the 3 CLIENT heightmaps are present, in range, and the chunk encodes non-empty.
//
// It is keyed off the fixed noiseGenSeed, so it is fully reproducible.
func TestTerrainSanity(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	minY := testMinY
	maxY := testMinY + testSecs*16
	seaLevel := 63

	air := block.ToStateID[block.Air{}]
	caveAir := block.ToStateID[block.CaveAir{}]
	water := block.ToStateID[block.Water{Level: 0}]
	lava := block.ToStateID[block.Lava{Level: 0}]
	stone := block.ToStateID[block.Stone{}]
	deepslate := block.ToStateID[block.Deepslate{Axis: block.Y}]

	isRock := func(st block.StateID) bool { return st == stone || st == deepslate }
	isStandableTop := func(st block.StateID) bool {
		// A standable spawn surface is solid OR water (an ocean spawn floats on the
		// surface, not in void): anything that is not an air variant.
		return st != air && st != caveAir
	}

	// --- Spawn-standable check (T-9-26). ---
	// Terrain sanity: the raw WorldSurface heightmap for the origin column is in range.
	spawnY := g.SpawnSurfaceY(level.ChunkPos{0, 0})
	if spawnY <= minY || spawnY >= maxY {
		t.Fatalf("spawn surface y=%d out of world range [%d,%d)", spawnY, minY, maxY)
	}
	// Spawn-safety: the player is placed via SpawnPos (17-06 PlayerSpawnFinder), which reads
	// the FULLY-DECORATED chunk and scans the WHOLE chunk for the first standable column —
	// NOT the fixed (8,8) column. This is decoration-aware ON PURPOSE: a tree can grow over
	// any single column (e.g. a jungle tree at (8,8) now correctly drapes its foliage DOWNWARD
	// to within ~2 blocks of the ground after the upside-down-foliage fix), so the finder must
	// pick a clear neighbor column. Assert the real finder lands the player on a standable
	// floor with air at the feet and head — never inside terrain OR decoration (leaves/logs).
	spawnCh := g.Generate(level.ChunkPos{0, 0})
	sp := g.SpawnPos(level.ChunkPos{0, 0})
	if !sp.Found {
		t.Fatalf("SpawnPos found no standable column in the origin chunk")
	}
	lx, lz := int(sp.X)&15, int(sp.Z)&15
	feetY := int(sp.Y)
	floorBlock := blockAtWorld(spawnCh, lx, feetY-1, lz, minY) // the block the feet stand ON
	feetBlock := blockAtWorld(spawnCh, lx, feetY, lz, minY)    // feet cell (must be clear)
	headBlock := blockAtWorld(spawnCh, lx, feetY+1, lz, minY)  // head cell (must be clear)
	if !isStandableTop(floorBlock) {
		t.Errorf("spawn floor block at y=%d is %q (air) — player would spawn in void", feetY-1, stateNameOf(floorBlock))
	}
	if feetBlock != air && feetBlock != caveAir && feetBlock != water {
		t.Errorf("spawn feet cell y=%d is %q (solid) — player would spawn inside terrain/decoration", feetY, stateNameOf(feetBlock))
	}
	if headBlock != air && headBlock != caveAir && headBlock != water {
		t.Errorf("spawn head cell y=%d is %q (solid) — player would spawn with head in terrain/decoration", feetY+1, stateNameOf(headBlock))
	}

	// --- Full-parity feature scan across a span of chunks. ---
	heightSet := map[int]bool{}
	biomes := map[biome.Type]bool{}
	anyWaterAtSea := false
	undergroundAirPocket := false
	carvedCaveAir := false
	oreVein := false
	perchedFluid := false

	oreVeinStates := map[block.StateID]bool{
		block.ToStateID[block.CopperOre{}]:        true,
		block.ToStateID[block.RawCopperBlock{}]:   true,
		block.ToStateID[block.Granite{}]:          true,
		block.ToStateID[block.DeepslateIronOre{}]: true,
		block.ToStateID[block.RawIronBlock{}]:     true,
		block.ToStateID[block.Tuff{}]:             true,
	}

	for cx := int32(-1); cx <= 2; cx++ {
		for cz := int32(-1); cz <= 2; cz++ {
			ch := g.Generate(level.ChunkPos{cx, cz})

			if ch.HeightMaps.WorldSurface == nil || ch.HeightMaps.MotionBlocking == nil || ch.HeightMaps.MotionBlockingNoLeaves == nil {
				t.Fatalf("chunk %v,%v missing a client heightmap", cx, cz)
			}
			for col := 0; col < 16*16; col++ {
				h := ch.HeightMaps.WorldSurface.Get(col) + minY
				if h < minY || h > maxY {
					t.Fatalf("chunk %v,%v col %d heightmap %d out of [%d,%d]", cx, cz, col, h, minY, maxY)
				}
			}

			for si := range ch.Sections {
				bc := ch.Sections[si].Biomes
				if bc == nil {
					t.Fatalf("chunk %v,%v section %d has nil biome container", cx, cz, si)
				}
				for i := 0; i < 4*4*4; i++ {
					biomes[bc.Get(i)] = true
				}
			}

			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					heightSet[ch.HeightMaps.WorldSurface.Get(lz<<4|lx)] = true

					surfaceTop := minY
					for y := maxY - 1; y >= minY; y-- {
						st := blockAtWorld(ch, lx, y, lz, minY)
						if st != air && st != caveAir && st != water {
							surfaceTop = y
							break
						}
					}

					for y := minY + 1; y < maxY; y++ {
						st := blockAtWorld(ch, lx, y, lz, minY)
						if st == water && y >= seaLevel-2 && y <= seaLevel {
							anyWaterAtSea = true
						}
						if oreVeinStates[st] {
							oreVein = true
						}
						if st == caveAir {
							carvedCaveAir = true
						}
						if (st == air || st == caveAir) && y < surfaceTop-3 {
							if isRock(blockAtWorld(ch, lx, y+1, lz, minY)) {
								undergroundAirPocket = true
							}
						}
						if st == lava || (st == water && y < seaLevel-6) {
							perchedFluid = true
						}
					}
				}
			}

			var buf bytes.Buffer
			if _, err := ch.WriteTo(&buf); err != nil {
				t.Fatalf("chunk %v,%v WriteTo: %v", cx, cz, err)
			}
			if buf.Len() == 0 {
				t.Fatalf("chunk %v,%v encoded to 0 bytes", cx, cz)
			}
		}
	}

	if len(heightSet) < 2 {
		t.Errorf("WorldSurface heightmap is flat (only %d distinct heights); terrain has no hills/valleys", len(heightSet))
	}
	if len(biomes) < 2 {
		t.Errorf("biome containers carry only %d biome(s); want >= 2 (varied, not single-plains)", len(biomes))
	}
	if !anyWaterAtSea {
		t.Error("no water near sea level across the sampled chunks (terrain never dips to ocean)")
	}
	if !undergroundAirPocket {
		t.Error("no underground air pocket found (noise caves / carved tunnels missing)")
	}
	if !carvedCaveAir {
		t.Error("no cave_air found (the carve pass never ran or never reached the sampled chunks)")
	}
	if !oreVein {
		t.Error("no ore-vein blocks found in the rock (OreVeinifier did not run)")
	}
	if !perchedFluid {
		t.Error("no perched/deep aquifer fluid found (the aquifer placed no water/lava away from sea level)")
	}
}

// TestNoiseGenFullParityFeatures: across a sampling of columns the chunk shows the
// full-parity features proving the whole pipeline ran:
//   - the WorldSurface heightmap VARIES across columns (hills/valleys, not flat),
//   - underground air pockets exist (noise caves + carved tunnels: air surrounded by
//     stone below the surface),
//   - carved cave_air appears (the carve pass replaced stone),
//   - ore-vein blocks appear in the rock,
//   - a perched/deep aquifer fluid (water or lava away from sea level) appears.
func TestNoiseGenFullParityFeatures(t *testing.T) {
	g := NewNoiseGenerator(noiseGenSeed, testSecs, testMinY)
	minY := testMinY
	maxY := testMinY + testSecs*16
	seaLevel := 63

	air := block.ToStateID[block.Air{}]
	caveAir := block.ToStateID[block.CaveAir{}]
	water := block.ToStateID[block.Water{Level: 0}]
	lava := block.ToStateID[block.Lava{Level: 0}]
	stone := block.ToStateID[block.Stone{}]
	deepslate := block.ToStateID[block.Deepslate{Axis: block.Y}]

	isRock := func(st block.StateID) bool { return st == stone || st == deepslate }

	heightSet := map[int]bool{}
	undergroundAirPocket := false
	carvedCaveAir := false
	oreVein := false
	perchedFluid := false

	// Ore vein blocks (OreVeinifier output: copper/iron ore, raw blocks, granite/tuff fillers).
	oreVeinStates := map[block.StateID]bool{
		block.ToStateID[block.CopperOre{}]:        true,
		block.ToStateID[block.RawCopperBlock{}]:   true,
		block.ToStateID[block.Granite{}]:          true,
		block.ToStateID[block.DeepslateIronOre{}]: true,
		block.ToStateID[block.RawIronBlock{}]:     true,
		block.ToStateID[block.Tuff{}]:             true,
	}

	for cx := int32(0); cx <= 3; cx++ {
		for cz := int32(0); cz <= 3; cz++ {
			ch := g.Generate(level.ChunkPos{cx, cz})

			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					heightSet[ch.HeightMaps.WorldSurface.Get(lz<<4|lx)] = true

					surfaceTop := minY
					for y := maxY - 1; y >= minY; y-- {
						st := blockAtWorld(ch, lx, y, lz, minY)
						if st != air && st != caveAir && st != water {
							surfaceTop = y
							break
						}
					}

					for y := minY + 1; y < maxY; y++ {
						st := blockAtWorld(ch, lx, y, lz, minY)
						if oreVeinStates[st] {
							oreVein = true
						}
						if st == caveAir {
							carvedCaveAir = true
						}
						// An air pocket well below the surface, with rock above it.
						if (st == air || st == caveAir) && y < surfaceTop-3 {
							above := blockAtWorld(ch, lx, y+1, lz, minY)
							if isRock(above) {
								undergroundAirPocket = true
							}
						}
						// A perched/deep aquifer fluid away from the ocean sea level.
						if st == lava {
							perchedFluid = true
						}
						if st == water && y < seaLevel-6 {
							perchedFluid = true
						}
					}
				}
			}
		}
	}

	if len(heightSet) < 2 {
		t.Errorf("WorldSurface heightmap is flat (only %d distinct heights); terrain has no hills/valleys", len(heightSet))
	}
	if !undergroundAirPocket {
		t.Error("no underground air pocket found (noise caves / carved tunnels missing)")
	}
	if !carvedCaveAir {
		t.Error("no cave_air found (the carve pass never ran or never reached the sampled chunks)")
	}
	if !oreVein {
		t.Error("no ore-vein blocks found in the rock (OreVeinifier did not run)")
	}
	if !perchedFluid {
		t.Error("no perched/deep aquifer fluid found (the aquifer placed no water/lava away from sea level)")
	}
}
