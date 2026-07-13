package world

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
)

func TestSpawnSpiralOffsetsVanilla26_2(t *testing.T) {
	if got, want := len(spawnSpiralOffsets), 120; got != want {
		t.Fatalf("offset count = %d, want %d (origin + offsets must equal Mth.square(11))", got, want)
	}

	// This digest is pinned from the independently decoded MinecraftServer.setInitialSpawn
	// offset sequence, encoded as "x,z;". It validates all 120 positions and their order without
	// reimplementing the production cursor algorithm in the test.
	var encoded strings.Builder
	seen := make(map[[2]int32]bool, 121)
	seen[[2]int32{0, 0}] = true
	for i, off := range spawnSpiralOffsets {
		if off[0] < -5 || off[0] > 5 || off[1] < -5 || off[1] > 5 {
			t.Fatalf("offset[%d] = %v outside vanilla [-5,5] square", i, off)
		}
		if seen[off] {
			t.Fatalf("offset[%d] duplicates %v", i, off)
		}
		seen[off] = true
		fmt.Fprintf(&encoded, "%d,%d;", off[0], off[1])
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256([]byte(encoded.String()))), "18b58946132756a8a757d80ae0ab0ad6413e560243f95c31901650145f817df5"; got != want {
		t.Fatalf("ordered offset digest = %s, want %s", got, want)
	}
	if got := spawnSpiralOffsets[:8]; fmt.Sprint(got) != "[[1 0] [1 1] [0 1] [-1 1] [-1 0] [-1 -1] [0 -1] [1 -1]]" {
		t.Fatalf("first ring = %v", got)
	}
	if got, want := spawnSpiralOffsets[len(spawnSpiralOffsets)-1], [2]int32{5, -5}; got != want {
		t.Fatalf("last offset = %v, want %v", got, want)
	}
}

func TestFindSpawnInChunksStopsAtFirstVanillaCandidate(t *testing.T) {
	origin := level.ChunkPos{17, -23}
	wantIndex := 37
	visited := make([]level.ChunkPos, 0, wantIndex+1)
	want := SpawnPoint{X: 12.5, Y: 80, Z: -4.5, Found: true}
	got := findSpawnInChunks(origin, func(pos level.ChunkPos) (SpawnPoint, bool) {
		visited = append(visited, pos)
		return want, len(visited)-1 == wantIndex
	})
	if got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	if got, want := len(visited), wantIndex+1; got != want {
		t.Fatalf("lookup count = %d, want %d; search did not stop at first hit", got, want)
	}
	wantPos := level.ChunkPos{origin[0] + spawnSpiralOffsets[wantIndex-1][0], origin[1] + spawnSpiralOffsets[wantIndex-1][1]}
	if got := visited[wantIndex]; got != wantPos {
		t.Fatalf("hit position = %v, want %v", got, wantPos)
	}
}

// newSpawnTestGen builds a NoiseGenerator shell with ONLY the geometry fields levelRespawnY
// reads (minY/secs/air), avoiding the expensive full router build. levelRespawnY touches no
// generation state — only the chunk's heightmaps + section blocks — so this is sufficient.
func newSpawnTestGen() *NoiseGenerator {
	return &NoiseGenerator{minY: testMinY, secs: testSecs, air: block.ToStateID[block.Air{}]}
}

// setBlock writes a state at chunk-local (lx, worldY, lz) using the same index convention as
// blockStateAt / writeClientHeightmaps.
func setBlock(ch *level.Chunk, lx, worldY, lz, minY int, st block.StateID) {
	sec := (worldY - minY) >> 4
	local := (worldY&15)<<8 | (lz&15)<<4 | (lx & 15)
	ch.Sections[sec].SetBlock(local, st)
}

// setHeights sets the three heightmaps levelRespawnY reads (stored as worldY-minY) for column
// (lx,lz): motion-blocking, world-surface, ocean-floor tops (each as the first-air world-Y).
func setHeights(ch *level.Chunk, lx, lz, minY, mbFirstAir, wsFirstAir, ofFirstAir int) {
	col := lz<<4 | lx
	ch.HeightMaps.MotionBlocking.Set(col, mbFirstAir-minY)
	ch.HeightMaps.WorldSurface.Set(col, wsFirstAir-minY)
	ch.HeightMaps.OceanFloor.Set(col, ofFirstAir-minY)
}

// TestLevelRespawnYSemantics pins the ported getLevelRespawnPos edge cases on synthetic
// columns (fast — no worldgen): a clean floor, a plant-on-floor (descend past the plant), a
// fluid-covered column (reject), and a void column (reject).
func TestLevelRespawnYSemantics(t *testing.T) {
	minY := testMinY
	g := newSpawnTestGen()
	air := block.ToStateID[block.Air{}]
	grass := block.ToStateID[block.GrassBlock{Snowy: false}]
	shortGrass := block.ToStateID[block.ShortGrass{}]
	water := block.ToStateID[block.Water{Level: 0}]

	t.Run("clean floor", func(t *testing.T) {
		ch := level.EmptyChunk(testSecs)
		const lx, lz, floorY = 5, 6, 70
		setBlock(ch, lx, floorY, lz, minY, grass)
		// motion-blocking top = floorY (first-air = floorY+1); ws/of same.
		setHeights(ch, lx, lz, minY, floorY+1, floorY+1, floorY+1)
		feet, ok := g.levelRespawnY(ch, lx, lz)
		if !ok || feet != floorY+1 {
			t.Fatalf("clean floor: ok=%v feet=%d, want ok=true feet=%d", ok, feet, floorY+1)
		}
	})

	t.Run("plant on floor -> descend past plant", func(t *testing.T) {
		ch := level.EmptyChunk(testSecs)
		const lx, lz, floorY = 5, 6, 70
		setBlock(ch, lx, floorY, lz, minY, grass)
		setBlock(ch, lx, floorY+1, lz, minY, shortGrass) // passable plant on top
		// CLIENT MotionBlocking counts the non-air plant, so first-air = floorY+2.
		setHeights(ch, lx, lz, minY, floorY+2, floorY+2, floorY+2)
		feet, ok := g.levelRespawnY(ch, lx, lz)
		// Walk descends past short_grass to the grass floor; feet stand in the plant cell.
		if !ok || feet != floorY+1 {
			t.Fatalf("plant floor: ok=%v feet=%d, want ok=true feet=%d (on grass, not floating on plant)", ok, feet, floorY+1)
		}
	})

	t.Run("fluid column -> reject", func(t *testing.T) {
		ch := level.EmptyChunk(testSecs)
		const lx, lz = 5, 6
		// Water from y=62 down to 50 (motion-blocking top at the water surface). No solid above.
		for y := 50; y <= 62; y++ {
			setBlock(ch, lx, y, lz, minY, water)
		}
		// mY = water surface first-air = 63; ws (non-air top) = 63; of (motion-blocking-no-fluid)
		// has no solid -> minY. ws>mY false here (both 63), so it walks down and hits water -> null.
		setHeights(ch, lx, lz, minY, 63, 63, minY)
		if _, ok := g.levelRespawnY(ch, lx, lz); ok {
			t.Fatal("fluid column: want reject (ok=false), got a spawn")
		}
	})

	t.Run("void column -> reject", func(t *testing.T) {
		ch := level.EmptyChunk(testSecs)
		const lx, lz = 5, 6
		// Nothing placed; motion-blocking top at minY (mY < minY+1 sentinel). Encode mY = minY-1
		// is impossible (>=0), so use mY = minY (first-air == minY) meaning no block; the walk
		// finds only air -> null.
		setHeights(ch, lx, lz, minY, minY, minY, minY)
		_ = air
		if _, ok := g.levelRespawnY(ch, lx, lz); ok {
			t.Fatal("void column: want reject (ok=false), got a spawn")
		}
	})
}

// TestPassableDecorationPredicate locks the deny-set: known surface plants are passable (a
// floor walk descends past them), while real floor blocks are NOT.
func TestPassableDecorationPredicate(t *testing.T) {
	passable := []string{
		"minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:poppy",
		"minecraft:dandelion", "minecraft:oak_sapling", "minecraft:vine", "minecraft:dead_bush",
		"minecraft:red_mushroom", "minecraft:sugar_cane",
	}
	for _, n := range passable {
		if !isPassableDecorationName(n) {
			t.Errorf("%q should be passable decoration (descend past it)", n)
		}
	}
	solid := []string{
		"minecraft:grass_block", "minecraft:dirt", "minecraft:stone", "minecraft:sand",
		"minecraft:gravel", "minecraft:oak_log", "minecraft:cobblestone",
	}
	for _, n := range solid {
		if isPassableDecorationName(n) {
			t.Errorf("%q must NOT be passable — it is a standable floor", n)
		}
	}
}
