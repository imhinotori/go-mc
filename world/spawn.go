package world

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/biome"
)

// SpawnPoint is the safe fresh-spawn world position (the air cell where the player's feet
// stand, i.e. ON a standable floor block, never embedded in terrain or decoration). It is
// the ported result of net.minecraft.server.level.PlayerSpawnFinder.getSpawnPosInChunk:
// the FIRST column (scanned over the whole spawn chunk, not just (8,8)) whose
// getLevelRespawnPos is non-null. X/Z are the BLOCK center of that column, Y is the
// feet-Y (= one above the first full-up-face floor block found walking down from the
// MOTION_BLOCKING top).
//
// Found is false only when the entire spawn chunk is void/ocean-covered (no standable
// column), in which case callers fall back to the heightmap-derived spawn.
type SpawnPoint struct {
	X, Y, Z float64
	Found   bool
}

// SpawnPos finds the SAFE fresh-spawn position for chunk pos by porting vanilla's
// net.minecraft.server.level.PlayerSpawnFinder.getSpawnPosInChunk + getLevelRespawnPos
// (renamed from PlayerRespawnLogic in 26.2; decompiled from 26.2-inner.jar this session via
// `javap -c -p net.minecraft.server.level.PlayerSpawnFinder`).
//
// CRITICAL — this is the spawn-INSIDE-a-block fix (17-06): the OLD SpawnSurfaceY sampled the
// TERRAIN-ONLY WorldSurface heightmap (GenerateTerrain), computing the spawn Y BEFORE
// decoration/structures placed blocks in the spawn column (a tree trunk, a vine, a village
// house) — so the player ended up embedded. This finder reads the FULLY-DECORATED chunk
// (Generate, which runs decorateSingle: features + structures) and walks the column down,
// EXACTLY as vanilla does over a finished LevelChunk, so the spawn block reflects decoration.
//
// getSpawnPosInChunk (jar): scan every column x in [minBlockX,maxBlockX], z in
// [minBlockZ,maxBlockZ] and return the first non-null getLevelRespawnPos. The whole-chunk
// scan is why a tree at (8,8) does not break spawn — a clear neighbor column is found.
//
// VANILLA RADIUS FALLBACK: when the origin chunk is entirely void/ocean (every column null),
// MinecraftServer.setInitialSpawn scans the remaining positions of its 11x11 square spiral,
// returning the first chunk with a standable column. This keeps an
// ocean-origin world from floating the player on the sea surface — they land on the nearest
// real ground. The spiral order is deterministic (pure over seed), so the fallback spawn is
// reproducible. The common case (land at origin) returns on the very first chunk, so the ring
// scan adds zero cost there.
// InitialSpawnChunk ports the climate half of MinecraftServer.setInitialSpawn: it runs
// Climate$Sampler.findSpawnPosition over the overworld spawnTarget (OverworldBiomeBuilder.
// spawnTarget) to pick a spawn-suitable CHUNK, instead of always using chunk (0,0). Vanilla
// then does an 11x11 square spiral of getSpawnPosInChunk around that chunk and sets the world
// spawn at the first successful result. The returned ChunkPos is
// ChunkPos.containing(findSpawnPosition()). CITE:
// MinecraftServer.setInitialSpawn (randomState.sampler().findSpawnPosition() -> ChunkPos.
// containing); Climate$Sampler.findSpawnPosition; Climate$SpawnFinder.
func (g *NoiseGenerator) InitialSpawnChunk() level.ChunkPos {
	sp := g.biomes.Sampler().FindSpawnPosition(biome.OverworldSpawnTarget())
	// ChunkPos.containing(BlockPos) == (x>>4, z>>4).
	return level.ChunkPos{int32(sp.X >> 4), int32(sp.Z >> 4)}
}

func (g *NoiseGenerator) SpawnPos(pos level.ChunkPos) SpawnPoint {
	return findSpawnInChunks(pos, g.spawnPosInChunk)
}

// findSpawnInChunks is the search-order seam for MinecraftServer.setInitialSpawn. The origin
// is tested first, followed by the exact remaining 120 offsets, and lookup stops immediately
// after the first successful getSpawnPosInChunk result.
func findSpawnInChunks(origin level.ChunkPos, lookup func(level.ChunkPos) (SpawnPoint, bool)) SpawnPoint {
	if sp, ok := lookup(origin); ok {
		return sp
	}
	for _, off := range spawnSpiralOffsets {
		pos := level.ChunkPos{origin[0] + off[0], origin[1] + off[1]}
		if sp, ok := lookup(pos); ok {
			return sp
		}
	}
	return SpawnPoint{}
}

// spawnPosInChunk runs the ported getSpawnPosInChunk over ONE fully-decorated chunk: the
// whole-chunk column scan (x-major then z, matching the jar's nested loop order
// minBlockX..maxBlockX outer, minBlockZ..maxBlockZ inner) returning the first standable
// column. Pure over (seed, pos): Generate -> decorateSingle terrain-generates the center + its
// 8 neighbors and runs Decorate, so the spawn read sees the same blocks a real client receives.
func (g *NoiseGenerator) spawnPosInChunk(pos level.ChunkPos) (SpawnPoint, bool) {
	ch := g.Generate(pos)
	baseX := int(pos[0]) << 4
	baseZ := int(pos[1]) << 4
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			if feetY, ok := g.levelRespawnY(ch, lx, lz); ok {
				return SpawnPoint{
					X:     float64(baseX+lx) + 0.5,
					Y:     float64(feetY),
					Z:     float64(baseZ+lz) + 0.5,
					Found: true,
				}, true
			}
		}
	}
	return SpawnPoint{}, false
}

// MinecraftServer.setInitialSpawn uses Mth.square(11) iterations and accepts offsets in
// [-5,5], yielding the origin plus 120 fallback chunks.
const spawnSearchChunkRadius = 5

// spawnSpiralOffsets is the 26.2 MinecraftServer.setInitialSpawn square spiral, excluding the
// origin which SpawnPos checks first. Built once at init.
var spawnSpiralOffsets = buildSpawnSpiral(spawnSearchChunkRadius)

// buildSpawnSpiral ports the bytecode's (x,z) cursor and (dx,dz) quarter-turn conditions.
// For r=5 it emits the remaining 120 entries of the 121-position 11x11 traversal.
func buildSpawnSpiral(r int) [][2]int32 {
	diameter := 2*r + 1
	out := make([][2]int32, 0, diameter*diameter-1)
	x, z, dx, dz := 0, 0, 0, -1
	for i := 0; i < diameter*diameter; i++ {
		if x >= -r && x <= r && z >= -r && z <= r && (x != 0 || z != 0) {
			out = append(out, [2]int32{int32(x), int32(z)})
		}
		if x == z || (x < 0 && x == -z) || (x > 0 && x == 1-z) {
			dx, dz = -dz, dx
		}
		x, z = x+dx, z+dz
	}
	return out
}

// levelRespawnY ports net.minecraft.server.level.PlayerSpawnFinder.getLevelRespawnPos for the
// OVERWORLD (no ceiling — hasCeiling() == false, so the MOTION_BLOCKING-top branch is taken,
// NOT getSpawnHeight). It returns the FEET world-Y (the air cell directly above the first
// standable floor block) for column (lx,lz), or ok=false when the column is a void/ocean/
// fluid-covered reject.
//
// Verified bytecode (getLevelRespawnPos, 26.2-inner.jar):
//
//	mY := MOTION_BLOCKING.getHeight(lx,lz)
//	if mY < level.getMinY()            -> return null         // void column
//	wsY := WORLD_SURFACE.getHeight(lx,lz)
//	if wsY > mY && OCEAN_FLOOR.getHeight(lx,lz) > mY -> return null  // water/ocean covers it
//	for (y = mY+1; y >= minY; y--):
//	    state := getBlockState(lx,y,lz)
//	    if !state.getFluidState().isEmpty() -> break (return null)   // hit a fluid first
//	    if isFaceFull(state.getCollisionShape(), UP) -> return above(pos)  // standable -> feet above
//	return null
//
// Heightmap encoding (Sulfur): BitStorage stores firstAir-minY, so getHeight(col) world-Y of
// the first-air-above-top == Get(col)+minY, and the top solid/fluid block world-Y ==
// Get(col)+minY-1. Vanilla's Heightmap.getHeight returns exactly that firstAir world-Y, so
// mY/wsY/ofY here are Get(col)+minY — the directly-comparable vanilla values.
func (g *NoiseGenerator) levelRespawnY(ch *level.Chunk, lx, lz int) (feetY int, ok bool) {
	minY := g.minY
	col := lz<<4 | lx

	mY := ch.HeightMaps.MotionBlocking.Get(col) + minY
	if mY < minY {
		return 0, false // void column (no motion-blocking block at all)
	}

	wsY := ch.HeightMaps.WorldSurface.Get(col) + minY
	ofY := ch.HeightMaps.OceanFloor.Get(col) + minY
	if wsY > mY && ofY > mY {
		// Both the world-surface and the ocean-floor sit ABOVE the motion-blocking top, i.e.
		// the column is covered by a (non-motion-blocking) fluid — an ocean/lake. Reject it;
		// the scan moves on to a land column.
		return 0, false
	}

	// Walk DOWN from mY+1: the first block whose fluid state is non-empty aborts the column
	// (a fluid floor is not standable — return null), and the first block with a full UP
	// collision face is the floor; the player stands in the cell ABOVE it (feet = y+1).
	for y := mY + 1; y >= minY; y-- {
		st := g.blockStateAt(ch, lx, y, lz)
		if isFluidState(st) {
			// Hit a fluid before any standable floor -> reject (jar: goto 205, return null).
			return 0, false
		}
		if isStandableFloor(st) {
			return y + 1, true // feet stand in the air cell above the floor block
		}
	}
	return 0, false
}

// blockStateAt reads the block StateID at chunk-local (lx, worldY, lz) of ch, returning air
// for an out-of-range Y (matching vanilla getBlockState over the chunk's height bounds).
func (g *NoiseGenerator) blockStateAt(ch *level.Chunk, lx, worldY, lz int) block.StateID {
	sec := (worldY - g.minY) >> 4
	if sec < 0 || sec >= len(ch.Sections) {
		return g.air
	}
	local := (worldY&15)<<8 | (lz&15)<<4 | (lx & 15)
	return ch.Sections[sec].GetBlock(local)
}

// isFluidState ports state.getFluidState().isEmpty() == false: the block IS a fluid (water or
// lava). The mid-worldgen chunk has no live FluidState graph, so the canonical "fluid state
// empty" check is realized as a type test against the only two fluid blocks the generator
// emits (block.Water / block.Lava, the aquifer + sea fill).
func isFluidState(st block.StateID) bool {
	switch block.StateList[st].(type) {
	case block.Water, block.Lava:
		return true
	default:
		return false
	}
}

// isStandableFloor ports Block.isFaceFull(state.getCollisionShape(level,pos), UP): is this a
// block the player can stand ON (a full UP collision face). The full BlockBehaviour collision
// shape is unavailable mid-worldgen, so it is approximated CONSERVATIVELY: a block is standable
// iff it is not air, not a fluid, AND not one of the no-collision surface plants/decorations the
// generator places ON TOP of the terrain (grass, ferns, flowers, saplings, vines, mushrooms,
// bushes, leaf litter, ...). Excluding those makes the downward walk DESCEND past the vegetation
// to the real solid ground beneath it (grass_block/dirt/sand/stone), exactly as vanilla's
// full-up-face test does — so the player stands on the ground, not floating on a plant.
//
// Being conservative here is always SAFE: a misclassified passable block only makes the finder
// look one block deeper for a real floor (correct); it can never report a passable block AS a
// floor. The opposite error (treating real ground as passable) cannot happen because the
// deny-set is restricted to known vegetation/decoration names.
func isStandableFloor(st block.StateID) bool {
	if block.IsAir(st) {
		return false
	}
	if isFluidState(st) {
		return false
	}
	if isPassableDecoration(st) {
		return false
	}
	return true
}

// passableDecorationStates is the set of no-collision surface plant/decoration state ids the
// overworld generator can place on the terrain top. Built once from the block registry by an
// allow-list of name keywords (suffix/contains), so a new vegetation block added to the registry
// that matches a keyword is covered automatically. A standable floor walk descends PAST these.
var passableDecorationStates = buildPassableDecorationStates()

// buildPassableDecorationStates collects the no-collision plant/decoration blocks by name. The
// keywords cover the vanilla overworld surface decoration set: short/tall grass + ferns, the
// small + tall flowers, saplings, the bushes/dead bush/firefly bush, leaf litter + petals + moss
// carpet, vines, mushrooms, lily pad, sugar cane, sweet berry bush, dripleaf, hanging roots, and
// sea vegetation. It deliberately does NOT include full-cube blocks (logs, leaves count as
// passable-for-trees but are NOT a spawn floor concern at the surface top, and excluding leaves
// would also be safe — they are not a place to stand).
func buildPassableDecorationStates() map[block.StateID]bool {
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		name := b.ID()
		if isPassableDecorationName(name) {
			set[block.StateID(sid)] = true
		}
	}
	return set
}

// isPassableDecorationName reports whether a block id names a no-collision surface plant /
// decoration (the deny-set membership test, factored out for unit testing).
func isPassableDecorationName(name string) bool {
	switch name {
	case "minecraft:grass", "minecraft:short_grass", "minecraft:tall_grass",
		"minecraft:fern", "minecraft:large_fern",
		"minecraft:dandelion", "minecraft:golden_dandelion", "minecraft:poppy",
		"minecraft:blue_orchid", "minecraft:allium", "minecraft:azure_bluet",
		"minecraft:red_tulip", "minecraft:orange_tulip", "minecraft:white_tulip",
		"minecraft:pink_tulip", "minecraft:oxeye_daisy", "minecraft:cornflower",
		"minecraft:lily_of_the_valley", "minecraft:wither_rose", "minecraft:torchflower",
		"minecraft:dead_bush", "minecraft:bush", "minecraft:firefly_bush",
		"minecraft:vine", "minecraft:lily_pad", "minecraft:sugar_cane",
		"minecraft:sweet_berry_bush", "minecraft:leaf_litter", "minecraft:pink_petals",
		"minecraft:moss_carpet", "minecraft:pale_moss_carpet", "minecraft:hanging_roots",
		"minecraft:big_dripleaf", "minecraft:big_dripleaf_stem", "minecraft:small_dripleaf",
		"minecraft:pitcher_plant", "minecraft:spore_blossom", "minecraft:cactus",
		"minecraft:brown_mushroom", "minecraft:red_mushroom",
		"minecraft:seagrass", "minecraft:tall_seagrass", "minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:cave_vines", "minecraft:cave_vines_plant",
		"minecraft:crimson_roots", "minecraft:warped_roots", "minecraft:nether_sprouts":
		return true
	}
	// Saplings (any wood type) are 1-block, no-collision.
	if len(name) > len("_sapling") && name[len(name)-len("_sapling"):] == "_sapling" {
		return true
	}
	return false
}

// isPassableDecoration tests membership in the prebuilt no-collision decoration set.
func isPassableDecoration(st block.StateID) bool { return passableDecorationStates[st] }
