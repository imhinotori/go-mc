package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// desert_pyramid placement constants (cross-checked vs the embedded structure_set
// desert_pyramids.json + has_structure/desert_pyramid tag): salt 14357617, spacing 32,
// separation 8, LINEAR spread; biome allow-set {minecraft:desert}.
const (
	desertPyramidSalt       = 14357617
	desertPyramidSpacing    = 32
	desertPyramidSeparation = 8

	// desertPyramidWidth/Depth/Height port DesertPyramidPiece's super() ctor (21x15x21 at
	// floor y=64). WIDTH=DEPTH=21 also gate getLowestY's footprint.
	desertPyramidWidth  = 21
	desertPyramidDepth  = 21
	desertPyramidHeight = 15
	desertPyramidFloor  = 64

	// overworldSeaLevel is the router sea_level (63). SinglePieceStructure.findGenerationPoint
	// rejects a start whose lowest footprint corner is below sea level (the pyramid must sit
	// on dry land). The desert pyramid is overworld-only, so the constant is inlined.
	overworldSeaLevel = 63

	// desertPyramidLootTable is the chest loot-table id (BuiltInLootTables.DESERT_PYRAMID).
	// LOOT DEFERRED v3: the chest BLOCK is placed + this id recorded on the start; no loot is
	// rolled (block entities / loot tables are a v3 subsystem).
	desertPyramidLootTable = "minecraft:chests/desert_pyramid"
)

// desertPyramidStartGen is the desert_pyramid StartGenerator: it decides WHERE a desert
// pyramid lands (the salt-14357617 random_spread math), gates on a REAL desert biome check
// at the chosen point (NO accept-by-default), samples the surface Y, and builds a
// DesertPyramidPiece wrapped in a StructureStart.
//
// Source: javap -c / CFR DesertPyramidStructure (SinglePieceStructure.findGenerationPoint:
// getLowestY sea-level gate + onTopOfChunkCenter WORLD_SURFACE_WG + isValidBiome).
type desertPyramidStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

// NewDesertPyramidStartGen builds the generator from the embedded structure_set + biome tag.
// A build-data error (missing/garbled embed) is an asset bug, surfaced to the caller; the
// NoiseGenerator panics on it exactly like its other build-data loads.
func NewDesertPyramidStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:desert_pyramids")
	if err != nil {
		return nil, err
	}
	allow, err := HasStructureBiomes("desert_pyramid")
	if err != nil {
		return nil, err
	}
	return &desertPyramidStartGen{placement: set.Placement, biomeAllow: allow}, nil
}

// GenerateStarts ports DesertPyramidStructure's start decision for chunk pos (pure over
// (seed,pos)):
//
//  1. isStructureChunk(seed, pos): is pos the region's potential start chunk? (else no start)
//  2. makeRandom = SetLargeFeatureSeed(seed, chunkX, chunkZ): the piece RNG.
//  3. getLowestY: the min WORLD_SURFACE_WG over the 21x21 footprint corners; < seaLevel -> no
//     start (the pyramid needs dry land).
//  4. onTopOfChunkCenter: the surface Y at chunk-center -> the biome-check position.
//  5. isValidBiome: GetBiome(centerX, surfaceY, centerZ) in {desert}; else NO start (vanilla,
//     no accept-by-default).
//  6. build DesertPyramidPiece(rng, minBlockX, minBlockZ) (the orientation draw advances the
//     RNG), then bake the terrain-height move (updateHeightPositionToLowestGroundHeight's
//     lowest-ground + -nextInt(3) offset) into the bbox at START time.
func (g *desertPyramidStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	minBlockX := cx * 16
	minBlockZ := cz * 16

	// (3) getLowestY over the 21x21 footprint corners (Structure.getCornerHeights samples
	// (minX,minZ),(minX,minZ+21),(minX+21,minZ),(minX+21,minZ+21)). Below sea level -> reject.
	lowest := lowestCornerY(sampler, minBlockX, minBlockZ, desertPyramidWidth, desertPyramidDepth)
	if lowest < overworldSeaLevel {
		return nil
	}

	// (4)+(5) biome gate at chunk-center (onTopOfChunkCenter's blockX/Z = middle of the chunk;
	// the biome-check Y is the surface there). NO accept-by-default: a non-desert origin yields
	// NO start.
	centerX := minBlockX + 8
	centerZ := minBlockZ + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	if !g.biomeAllow[biomeAt(centerX, centerSurfaceY, centerZ).String()] {
		return nil
	}

	// (2) the piece RNG = GenerationContext.makeRandom(seed, pos).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	// (6) build the piece. The ctor draws getRandomHorizontalDirection(rng) (an RNG advance)
	// and anchors the bbox at floor=64.
	piece := newDesertPyramidPiece(rng, minBlockX, minBlockZ)

	// Bake the terrain-height move that vanilla does in postProcess via
	// updateHeightPositionToLowestGroundHeight(level, -random.nextInt(3)): the bbox minY is
	// moved to the LOWEST ground over its footprint + the offset. We compute the lowest ground
	// from the surface sampler (heightmap-at-STARTS, Pitfall #9) and draw the SAME -nextInt(3)
	// offset from the piece RNG (the first postProcess draw in vanilla) so the RNG stream + the
	// placement stay bit-faithful and order-independent.
	groundLow := lowestCornerY(sampler, piece.bbox.MinX, piece.bbox.MinZ, piece.width, piece.depth)
	offset := -int(rng.NextIntN(3))
	piece.Move(0, groundLow-piece.bbox.MinY+offset, 0)

	start := &StructureStart{
		Structure: "minecraft:desert_pyramid",
		ChunkPos:  pos,
		Pieces:    []Piece{piece},
	}
	start.RecomputeBBox()
	return []*StructureStart{start}
}

// lowestCornerY ports Structure.getLowestY/getCornerHeights: the minimum surface Y over the
// 4 corners (minX,minZ),(minX,minZ+sizeZ),(minX+sizeX,minZ),(minX+sizeX,minZ+sizeZ).
func lowestCornerY(sampler SurfaceSampler, minX, minZ, sizeX, sizeZ int) int {
	a := sampler.SampleSurfaceY(minX, minZ)
	b := sampler.SampleSurfaceY(minX, minZ+sizeZ)
	c := sampler.SampleSurfaceY(minX+sizeX, minZ)
	d := sampler.SampleSurfaceY(minX+sizeX, minZ+sizeZ)
	return min(min(a, b), min(c, d))
}

// DesertPyramidPiece is the single hardcoded piece (0 .nbt) of the desert pyramid: the
// stepped sandstone body + two towers + the entrance + the orange/blue terracotta floor
// mosaic + the hidden TNT-trap chamber with 4 chests + the cellar (the 26.2 suspicious-sand
// cellar — its sand records are tracked for v3 archaeology; the cellar sandstone IS placed).
//
// Source: javap -c / CFR net.minecraft.world.level.levelgen.structure.structures.
// DesertPyramidPiece.postProcess (transcribed block-by-block + draw order).
type DesertPyramidPiece struct {
	StructurePiece
	width, depth, height int
	// LootChests records the 4 chest world positions + the loot-table id (loot deferred v3).
	LootChests []LootChest
}

// newDesertPyramidPiece ports the DesertPyramidPiece(random, west, north) ctor ->
// ScatteredFeaturePiece super: bbox = makeBoundingBox(west, 64, north, dir, 21, 15, 21) with
// dir = getRandomHorizontalDirection(random) (the orientation draw advancing the RNG).
func newDesertPyramidPiece(rng levelgen.RandomSource, west, north int) *DesertPyramidPiece {
	dir := getRandomHorizontalDirection(rng)
	p := &DesertPyramidPiece{
		width:  desertPyramidWidth,
		height: desertPyramidHeight,
		depth:  desertPyramidDepth,
	}
	p.bbox = makeScatteredBoundingBox(west, desertPyramidFloor, north, dir, desertPyramidWidth, desertPyramidHeight, desertPyramidDepth)
	p.setOrientation(dir, true)
	return p
}

// makeScatteredBoundingBox ports StructurePiece.makeBoundingBox: a Z-axis facing keeps
// width along X / depth along Z; an X-axis facing swaps them (the bbox covers the rotated
// footprint). Inclusive max faces.
func makeScatteredBoundingBox(x, y, z int, dir block.Direction, width, height, depth int) BoundingBox {
	if dir == block.North || dir == block.South { // Axis.Z
		return BoundingBox{MinX: x, MinY: y, MinZ: z, MaxX: x + width - 1, MaxY: y + height - 1, MaxZ: z + depth - 1}
	}
	// Axis.X (EAST/WEST): swap width<->depth.
	return BoundingBox{MinX: x, MinY: y, MinZ: z, MaxX: x + depth - 1, MaxY: y + height - 1, MaxZ: z + width - 1}
}

// PostProcess writes the full hardcoded pyramid geometry, JAR-EXACT block-by-block + draw
// order (DesertPyramidPiece.postProcess). All coords are piece-LOCAL; placeBlock/generateBox
// map them to world via the orientation + clip to the chunk's writable box. The vanilla
// height-position move (updateHeightPositionToLowestGroundHeight + its -nextInt(3) draw) was
// baked into the bbox at START time (the offset draw is the SAME RNG draw), so PostProcess
// uses fixed local coords and is order-independent across overlapping chunks (Pitfall #2).
//
// LOOT/REDSTONE DEFERRED v3: chests are placed as the chest BLOCK + the loot-table tag (no
// loot rolled); the TNT trap places the tnt + pressure-plate BLOCKS (no redstone/entity
// logic); the cellar's suspicious-sand positions are tracked but NOT placed (afterPlace's
// archaeology is a v3 subsystem). The VISIBLE pyramid is fully delivered.
func (p *DesertPyramidPiece) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	w := p.width
	d := p.depth
	sandstone := stateOf(block.Sandstone{})
	cutSandstone := stateOf(block.CutSandstone{})
	chiseledSandstone := stateOf(block.ChiseledSandstone{})
	sandstoneSlab := stateOf(block.SandstoneSlab{Type: block.SlabTypeBottom, Waterlogged: false})
	air := stateAir
	orange := stateOf(block.OrangeTerracotta{})
	blue := stateOf(block.BlueTerracotta{})

	// The pyramid foundation: a solid sandstone slab under the footprint then the stepped body.
	p.generateBox(view, box, 0, -4, 0, w-1, 0, d-1, sandstone, sandstone, false)
	for pos := 1; pos <= 9; pos++ {
		p.generateBox(view, box, pos, pos, pos, w-1-pos, pos, d-1-pos, sandstone, sandstone, false)
		p.generateBox(view, box, pos+1, pos, pos+1, w-2-pos, pos, d-2-pos, air, air, false)
	}
	// Fill the foundation columns down to terrain.
	for x := 0; x < w; x++ {
		for z := 0; z < d; z++ {
			p.fillColumnDown(view, sandstone, x, -5, z, box, box.MinY)
		}
	}

	northStairs := stairFacing(block.North)
	southStairs := stairFacing(block.South)
	eastStairs := stairFacing(block.East)
	westStairs := stairFacing(block.West)

	// The two corner towers.
	p.generateBox(view, box, 0, 0, 0, 4, 9, 4, sandstone, air, false)
	p.generateBox(view, box, 1, 10, 1, 3, 10, 3, sandstone, sandstone, false)
	p.placeBlock(view, northStairs, 2, 10, 0, box)
	p.placeBlock(view, southStairs, 2, 10, 4, box)
	p.placeBlock(view, eastStairs, 0, 10, 2, box)
	p.placeBlock(view, westStairs, 4, 10, 2, box)
	p.generateBox(view, box, w-5, 0, 0, w-1, 9, 4, sandstone, air, false)
	p.generateBox(view, box, w-4, 10, 1, w-2, 10, 3, sandstone, sandstone, false)
	p.placeBlock(view, northStairs, w-3, 10, 0, box)
	p.placeBlock(view, southStairs, w-3, 10, 4, box)
	p.placeBlock(view, eastStairs, w-5, 10, 2, box)
	p.placeBlock(view, westStairs, w-1, 10, 2, box)

	// The entrance arch + door.
	p.generateBox(view, box, 8, 0, 0, 12, 4, 4, sandstone, air, false)
	p.generateBox(view, box, 9, 1, 0, 11, 3, 4, air, air, false)
	p.placeBlock(view, cutSandstone, 9, 1, 1, box)
	p.placeBlock(view, cutSandstone, 9, 2, 1, box)
	p.placeBlock(view, cutSandstone, 9, 3, 1, box)
	p.placeBlock(view, cutSandstone, 10, 3, 1, box)
	p.placeBlock(view, cutSandstone, 11, 3, 1, box)
	p.placeBlock(view, cutSandstone, 11, 2, 1, box)
	p.placeBlock(view, cutSandstone, 11, 1, 1, box)

	// The interior corridors.
	p.generateBox(view, box, 4, 1, 1, 8, 3, 3, sandstone, air, false)
	p.generateBox(view, box, 4, 1, 2, 8, 2, 2, air, air, false)
	p.generateBox(view, box, 12, 1, 1, 16, 3, 3, sandstone, air, false)
	p.generateBox(view, box, 12, 1, 2, 16, 2, 2, air, air, false)
	p.generateBox(view, box, 5, 4, 5, w-6, 4, d-6, sandstone, sandstone, false)
	p.generateBox(view, box, 9, 4, 9, 11, 4, 11, air, air, false)
	p.generateBox(view, box, 8, 1, 8, 8, 3, 8, cutSandstone, cutSandstone, false)
	p.generateBox(view, box, 12, 1, 8, 12, 3, 8, cutSandstone, cutSandstone, false)
	p.generateBox(view, box, 8, 1, 12, 8, 3, 12, cutSandstone, cutSandstone, false)
	p.generateBox(view, box, 12, 1, 12, 12, 3, 12, cutSandstone, cutSandstone, false)
	p.generateBox(view, box, 1, 1, 5, 4, 4, 11, sandstone, sandstone, false)
	p.generateBox(view, box, w-5, 1, 5, w-2, 4, 11, sandstone, sandstone, false)
	p.generateBox(view, box, 6, 7, 9, 6, 7, 11, sandstone, sandstone, false)
	p.generateBox(view, box, w-7, 7, 9, w-7, 7, 11, sandstone, sandstone, false)
	p.generateBox(view, box, 5, 5, 9, 5, 7, 11, cutSandstone, cutSandstone, false)
	p.generateBox(view, box, w-6, 5, 9, w-6, 7, 11, cutSandstone, cutSandstone, false)
	p.placeBlock(view, air, 5, 5, 10, box)
	p.placeBlock(view, air, 5, 6, 10, box)
	p.placeBlock(view, air, 6, 6, 10, box)
	p.placeBlock(view, air, w-6, 5, 10, box)
	p.placeBlock(view, air, w-6, 6, 10, box)
	p.placeBlock(view, air, w-7, 6, 10, box)
	p.generateBox(view, box, 2, 4, 4, 2, 6, 4, air, air, false)
	p.generateBox(view, box, w-3, 4, 4, w-3, 6, 4, air, air, false)
	p.placeBlock(view, northStairs, 2, 4, 5, box)
	p.placeBlock(view, northStairs, 2, 3, 4, box)
	p.placeBlock(view, northStairs, w-3, 4, 5, box)
	p.placeBlock(view, northStairs, w-3, 3, 4, box)
	p.generateBox(view, box, 1, 1, 3, 2, 2, 3, sandstone, sandstone, false)
	p.generateBox(view, box, w-3, 1, 3, w-2, 2, 3, sandstone, sandstone, false)
	p.placeBlock(view, sandstone, 1, 1, 2, box)
	p.placeBlock(view, sandstone, w-2, 1, 2, box)
	p.placeBlock(view, sandstoneSlab, 1, 2, 2, box)
	p.placeBlock(view, sandstoneSlab, w-2, 2, 2, box)
	p.placeBlock(view, westStairs, 2, 1, 2, box)
	p.placeBlock(view, eastStairs, w-3, 1, 2, box)
	p.generateBox(view, box, 4, 3, 5, 4, 3, 17, sandstone, sandstone, false)
	p.generateBox(view, box, w-5, 3, 5, w-5, 3, 17, sandstone, sandstone, false)
	p.generateBox(view, box, 3, 1, 5, 4, 2, 16, air, air, false)
	p.generateBox(view, box, w-6, 1, 5, w-5, 2, 16, air, air, false)
	for z := 5; z <= 17; z += 2 {
		p.placeBlock(view, cutSandstone, 4, 1, z, box)
		p.placeBlock(view, chiseledSandstone, 4, 2, z, box)
		p.placeBlock(view, cutSandstone, w-5, 1, z, box)
		p.placeBlock(view, chiseledSandstone, w-5, 2, z, box)
	}

	// The orange/blue terracotta floor MOSAIC (the X pattern, per-cell).
	p.placeBlock(view, orange, 10, 0, 7, box)
	p.placeBlock(view, orange, 10, 0, 8, box)
	p.placeBlock(view, orange, 9, 0, 9, box)
	p.placeBlock(view, orange, 11, 0, 9, box)
	p.placeBlock(view, orange, 8, 0, 10, box)
	p.placeBlock(view, orange, 12, 0, 10, box)
	p.placeBlock(view, orange, 7, 0, 10, box)
	p.placeBlock(view, orange, 13, 0, 10, box)
	p.placeBlock(view, orange, 9, 0, 11, box)
	p.placeBlock(view, orange, 11, 0, 11, box)
	p.placeBlock(view, orange, 10, 0, 12, box)
	p.placeBlock(view, orange, 10, 0, 13, box)
	p.placeBlock(view, blue, 10, 0, 10, box)

	// The terracotta-banded tower faces (x = 0 and x = w-1).
	for x := 0; x <= w-1; x += w - 1 {
		p.placeBlock(view, cutSandstone, x, 2, 1, box)
		p.placeBlock(view, orange, x, 2, 2, box)
		p.placeBlock(view, cutSandstone, x, 2, 3, box)
		p.placeBlock(view, cutSandstone, x, 3, 1, box)
		p.placeBlock(view, orange, x, 3, 2, box)
		p.placeBlock(view, cutSandstone, x, 3, 3, box)
		p.placeBlock(view, orange, x, 4, 1, box)
		p.placeBlock(view, chiseledSandstone, x, 4, 2, box)
		p.placeBlock(view, orange, x, 4, 3, box)
		p.placeBlock(view, cutSandstone, x, 5, 1, box)
		p.placeBlock(view, orange, x, 5, 2, box)
		p.placeBlock(view, cutSandstone, x, 5, 3, box)
		p.placeBlock(view, orange, x, 6, 1, box)
		p.placeBlock(view, chiseledSandstone, x, 6, 2, box)
		p.placeBlock(view, orange, x, 6, 3, box)
		p.placeBlock(view, orange, x, 7, 1, box)
		p.placeBlock(view, orange, x, 7, 2, box)
		p.placeBlock(view, orange, x, 7, 3, box)
		p.placeBlock(view, cutSandstone, x, 8, 1, box)
		p.placeBlock(view, cutSandstone, x, 8, 2, box)
		p.placeBlock(view, cutSandstone, x, 8, 3, box)
	}
	// The terracotta-banded front face (z = 0, two tower columns).
	for x := 2; x <= w-3; x += w - 3 - 2 {
		p.placeBlock(view, cutSandstone, x-1, 2, 0, box)
		p.placeBlock(view, orange, x, 2, 0, box)
		p.placeBlock(view, cutSandstone, x+1, 2, 0, box)
		p.placeBlock(view, cutSandstone, x-1, 3, 0, box)
		p.placeBlock(view, orange, x, 3, 0, box)
		p.placeBlock(view, cutSandstone, x+1, 3, 0, box)
		p.placeBlock(view, orange, x-1, 4, 0, box)
		p.placeBlock(view, chiseledSandstone, x, 4, 0, box)
		p.placeBlock(view, orange, x+1, 4, 0, box)
		p.placeBlock(view, cutSandstone, x-1, 5, 0, box)
		p.placeBlock(view, orange, x, 5, 0, box)
		p.placeBlock(view, cutSandstone, x+1, 5, 0, box)
		p.placeBlock(view, orange, x-1, 6, 0, box)
		p.placeBlock(view, chiseledSandstone, x, 6, 0, box)
		p.placeBlock(view, orange, x+1, 6, 0, box)
		p.placeBlock(view, orange, x-1, 7, 0, box)
		p.placeBlock(view, orange, x, 7, 0, box)
		p.placeBlock(view, orange, x+1, 7, 0, box)
		p.placeBlock(view, cutSandstone, x-1, 8, 0, box)
		p.placeBlock(view, cutSandstone, x, 8, 0, box)
		p.placeBlock(view, cutSandstone, x+1, 8, 0, box)
	}
	// The lintel over the entrance.
	p.generateBox(view, box, 8, 4, 0, 12, 6, 0, cutSandstone, cutSandstone, false)
	p.placeBlock(view, air, 8, 6, 0, box)
	p.placeBlock(view, air, 12, 6, 0, box)
	p.placeBlock(view, orange, 9, 5, 0, box)
	p.placeBlock(view, chiseledSandstone, 10, 5, 0, box)
	p.placeBlock(view, orange, 11, 5, 0, box)

	// The hidden TNT-trap chamber (below the mosaic floor).
	p.generateBox(view, box, 8, -14, 8, 12, -11, 12, cutSandstone, cutSandstone, false)
	p.generateBox(view, box, 8, -10, 8, 12, -10, 12, chiseledSandstone, chiseledSandstone, false)
	p.generateBox(view, box, 8, -9, 8, 12, -9, 12, cutSandstone, cutSandstone, false)
	p.generateBox(view, box, 8, -8, 8, 12, -1, 12, sandstone, sandstone, false)
	p.generateBox(view, box, 9, -11, 9, 11, -1, 11, air, air, false)
	p.placeBlock(view, stateOf(block.StonePressurePlate{Powered: false}), 10, -11, 10, box)
	p.generateBox(view, box, 9, -13, 9, 11, -13, 11, stateOf(block.Tnt{Unstable: false}), air, false)
	p.placeBlock(view, air, 8, -11, 10, box)
	p.placeBlock(view, air, 8, -10, 10, box)
	p.placeBlock(view, chiseledSandstone, 7, -10, 10, box)
	p.placeBlock(view, cutSandstone, 7, -11, 10, box)
	p.placeBlock(view, air, 12, -11, 10, box)
	p.placeBlock(view, air, 12, -10, 10, box)
	p.placeBlock(view, chiseledSandstone, 13, -10, 10, box)
	p.placeBlock(view, cutSandstone, 13, -11, 10, box)
	p.placeBlock(view, air, 10, -11, 8, box)
	p.placeBlock(view, air, 10, -10, 8, box)
	p.placeBlock(view, chiseledSandstone, 10, -10, 7, box)
	p.placeBlock(view, cutSandstone, 10, -11, 7, box)
	p.placeBlock(view, air, 10, -11, 12, box)
	p.placeBlock(view, air, 10, -10, 12, box)
	p.placeBlock(view, chiseledSandstone, 10, -10, 13, box)
	p.placeBlock(view, cutSandstone, 10, -11, 13, box)

	// The 4 chests in the chamber corners (loot deferred v3). Iterate Plane.HORIZONTAL
	// [N,E,S,W]; the chest sits 2 blocks out along each direction's step. hasPlacedChest[]
	// is per-piece (single placement here), indexed by get2DDataValue.
	var placed [4]bool
	for _, dir := range horizontalPlane {
		idx := get2DDataValue(dir)
		if placed[idx] {
			continue
		}
		xo := stepX(dir) * 2
		zo := stepZ(dir) * 2
		placed[idx] = p.createChest(view, box, rng, 10+xo, -11, 10+zo, desertPyramidLootTable, &p.LootChests)
	}

	// The cellar (the 26.2 suspicious-sand cellar). The cellar sandstone shell IS placed; the
	// stairs sand + the suspicious-sand positions are DEFERRED (v3 archaeology: afterPlace
	// resolves the suspicious-sand block-entities + loot — out of scope this plan). The
	// per-block level.getRandom() collapsed-roof variation is also deferred (it needs the
	// WorldGenLevel per-feature random; the BLOCKS the chamber places are the deliverable).
	p.addCellarRoom(view, box, cutSandstone, chiseledSandstone, orange, blue)
}

// addCellarRoom ports DesertPyramidPiece.addCellarRoom's BLOCK placements (the cut/chiseled
// sandstone shell + the terracotta floor pattern around roomCenter (16,-4,13)). The sand
// fill (placeSandBox/placeSand) + the collapsed-roof randomization are DEFERRED v3 (they only
// record suspicious-sand positions / need the per-feature random — no visible structural
// blocks). The cellar floor/ceiling/wall sandstone + the terracotta IS placed.
func (p *DesertPyramidPiece) addCellarRoom(view WorldGenView, box BoundingBox, cutSandstone, chiseled, orange, blue block.StateID) {
	x, y, z := 16, -4, 13
	// Floor + ceiling rings (cut sandstone) and the wall band (chiseled), skipAir=true.
	p.generateBox(view, box, x-3, y+1, z-3, x-3, y+1, z+2, cutSandstone, cutSandstone, true)
	p.generateBox(view, box, x+3, y+1, z-3, x+3, y+1, z+2, cutSandstone, cutSandstone, true)
	p.generateBox(view, box, x-3, y+1, z-3, x+3, y+1, z-2, cutSandstone, cutSandstone, true)
	p.generateBox(view, box, x-3, y+1, z+3, x+3, y+1, z+3, cutSandstone, cutSandstone, true)
	p.generateBox(view, box, x-3, y+2, z-3, x-3, y+2, z+2, chiseled, chiseled, true)
	p.generateBox(view, box, x+3, y+2, z-3, x+3, y+2, z+2, chiseled, chiseled, true)
	p.generateBox(view, box, x-3, y+2, z-3, x+3, y+2, z-2, chiseled, chiseled, true)
	p.generateBox(view, box, x-3, y+2, z+3, x+3, y+2, z+3, chiseled, chiseled, true)
	p.generateBox(view, box, x-3, -1, z-3, x-3, -1, z+2, cutSandstone, cutSandstone, true)
	p.generateBox(view, box, x+3, -1, z-3, x+3, -1, z+2, cutSandstone, cutSandstone, true)
	p.generateBox(view, box, x-3, -1, z-3, x+3, -1, z-2, cutSandstone, cutSandstone, true)
	p.generateBox(view, box, x-3, -1, z+3, x+3, -1, z+3, cutSandstone, cutSandstone, true)
	// The terracotta floor pattern.
	p.placeBlock(view, blue, x, y, z, box)
	p.placeBlock(view, orange, x+1, y, z-1, box)
	p.placeBlock(view, orange, x+1, y, z+1, box)
	p.placeBlock(view, orange, x-1, y, z-1, box)
	p.placeBlock(view, orange, x-1, y, z+1, box)
	p.placeBlock(view, orange, x+2, y, z, box)
	p.placeBlock(view, orange, x-2, y, z, box)
	p.placeBlock(view, orange, x, y, z+2, box)
	p.placeBlock(view, orange, x, y, z-2, box)
	p.placeBlock(view, orange, x+3, y, z, box)
	p.placeBlock(view, cutSandstone, x+4, y+1, z, box)
	p.placeBlock(view, chiseled, x+4, y+2, z, box)
	p.placeBlock(view, orange, x-3, y, z, box)
	p.placeBlock(view, cutSandstone, x-4, y+1, z, box)
	p.placeBlock(view, chiseled, x-4, y+2, z, box)
	p.placeBlock(view, orange, x, y, z+3, box)
	p.placeBlock(view, orange, x, y, z-3, box)
	p.placeBlock(view, cutSandstone, x, y+1, z-4, box)
	p.placeBlock(view, chiseled, x, -2, z-4, box)
}

// stepX/stepZ port Direction.getStepX/getStepZ for the horizontal compass (the chest offset).
func stepX(d block.Direction) int {
	switch d {
	case block.West:
		return -1
	case block.East:
		return 1
	}
	return 0
}

func stepZ(d block.Direction) int {
	switch d {
	case block.North:
		return -1
	case block.South:
		return 1
	}
	return 0
}

// stateOf resolves a block value to its StateID (panics on a missing state — a build-data
// bug, not runtime input, matching the generator's other panicking resolves).
func stateOf(b block.Block) block.StateID {
	id, ok := block.ToStateID[b]
	if !ok {
		panic("structure: desert pyramid block has no state id: " + b.ID())
	}
	return id
}

// stairFacing resolves a default (BOTTOM/STRAIGHT) sandstone stair facing dir. The desert
// pyramid places stairs in the un-transformed LOCAL frame; placeBlock then applies the piece
// orientation's mirror/rotate to the facing.
func stairFacing(dir block.Direction) block.StateID {
	return stateOf(block.SandstoneStairs{Facing: dir, Half: block.Bottom, Shape: block.StairsShapeStraight, Waterlogged: false})
}
