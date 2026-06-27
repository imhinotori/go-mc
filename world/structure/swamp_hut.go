package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// swamp_hut (witch hut) placement constants (cross-checked vs the embedded structure_set
// swamp_huts.json + has_structure/swamp_hut tag): salt 14357620, spacing 32, separation 8,
// LINEAR spread; biome allow-set {swamp}.
const (
	swampHutSalt       = 14357620
	swampHutSpacing    = 32
	swampHutSeparation = 8

	// SwampHutPiece ctor (javap -c): super(SWAMPLAND_HUT, west, 64, north, 7, 7, 9,
	// getRandomHorizontalDirection(random)) — a 7x7x9 box at floor y=64.
	swampHutWidth  = 7
	swampHutHeight = 7
	swampHutDepth  = 9
	swampHutFloor  = 64
)

// swampHutStartGen is the swamp_hut StartGenerator. Unlike the desert/jungle temples
// (SinglePieceStructure with a getLowestY sea-level gate), SwampHutStructure extends the base
// Structure: findGenerationPoint is just onTopOfChunkCenter(WORLD_SURFACE_WG) + the base
// biome predicate. So there is NO sea-level gate here — only the biome gate + surface project.
//
// Source: javap -c SwampHutStructure.findGenerationPoint (onTopOfChunkCenter, no getLowestY).
type swampHutStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

// NewSwampHutStartGen builds the generator from the embedded structure_set + biome tag.
func NewSwampHutStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:swamp_huts")
	if err != nil {
		return nil, err
	}
	allow, err := HasStructureBiomes("swamp_hut")
	if err != nil {
		return nil, err
	}
	return &swampHutStartGen{placement: set.Placement, biomeAllow: allow}, nil
}

// GenerateStarts ports SwampHutStructure's start decision for chunk pos (pure over (seed,pos)):
// isStructureChunk gate, the chunk-center biome gate (swamp allow-set, no accept-by-default),
// the SampleSurfaceY project, the orientation-draw piece, the baked terrain-height move
// (updateAverageGroundHeight, offset 0, NO RNG draw). No sea-level gate.
func (g *swampHutStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	minBlockX := cx * 16
	minBlockZ := cz * 16

	// Biome gate at chunk-center: a non-swamp origin yields NO start (no accept-by-default).
	centerX := minBlockX + 8
	centerZ := minBlockZ + 8
	centerSurfaceY := sampler.SampleSurfaceY(centerX, centerZ)
	if !g.biomeAllow[biomeAt(centerX, centerSurfaceY, centerZ).String()] {
		return nil
	}

	// The piece RNG = GenerationContext.makeRandom(seed, pos).
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, cx, cz)

	// Build the piece — the ctor draws getRandomHorizontalDirection(rng) (an RNG advance).
	piece := newSwampHutPiece(rng, minBlockX, minBlockZ)

	// Bake updateAverageGroundHeight (offset 0, no RNG draw) — average ground over the
	// footprint, moved into the bbox at START.
	groundAvg := averageCornerY(sampler, piece.bbox.MinX, piece.bbox.MinZ, piece.width, piece.depth)
	piece.Move(0, groundAvg-piece.bbox.MinY, 0)

	start := &StructureStart{
		Structure: "minecraft:swamp_hut",
		ChunkPos:  pos,
		Pieces:    []Piece{piece},
	}
	start.RecomputeBBox()
	return []*StructureStart{start}
}

// SwampHutPiece is the single hardcoded piece (0 .nbt) of the swamp (witch) hut: the
// spruce-plank hut on oak-log stilts + the cauldron/crafting-table/flower-pot-with-red-
// mushroom + the spruce-stair roof overhang. The witch + black-cat ENTITIES are DEFERRED v3
// (entities are a v3 subsystem; the visible hut is fully delivered).
//
// Source: javap -c net.minecraft.world.level.levelgen.structure.structures.SwampHutPiece
// .postProcess (transcribed block-by-block + draw order).
type SwampHutPiece struct {
	StructurePiece
	width, depth, height int

	// spawnedWitch/spawnedCat are the jar's one-shot spawn guards (SwampHutPiece.spawnedWitch /
	// spawnedCat): once the witch (resp. cat) has been recorded, the flag flips true so a re-run
	// of PostProcess (a per-chunk re-pass, or a RELOADED piece) records no second mob — the
	// reload-no-double-spawn property (Pitfall 6). They round-trip in the piece NBT via the
	// 20-03 SpawnedWitch/SpawnedCat slots (piece_nbt.go), so a reloaded swamp hut reads them true.
	spawnedWitch bool
	spawnedCat   bool
}

// newSwampHutPiece ports the SwampHutPiece(random, west, north) ctor.
func newSwampHutPiece(rng levelgen.RandomSource, west, north int) *SwampHutPiece {
	dir := getRandomHorizontalDirection(rng)
	p := &SwampHutPiece{
		width:  swampHutWidth,
		height: swampHutHeight,
		depth:  swampHutDepth,
	}
	p.bbox = makeScatteredBoundingBox(west, swampHutFloor, north, dir, swampHutWidth, swampHutHeight, swampHutDepth)
	p.setOrientation(dir, true)
	return p
}

// PostProcess writes the full hardcoded swamp-hut geometry, JAR-EXACT block-by-block + draw
// order (SwampHutPiece.postProcess). All coords are piece-LOCAL; placeBlock/generateBox map
// them to world via the orientation + clip to the chunk's writable box.
//
// ENTITIES DEFERRED v3: the witch + black-cat spawns (the postProcess tail) are NOT placed
// (entities are a v3 subsystem). The VISIBLE hut (floor/walls/roof/stilts + cauldron +
// crafting-table + potted-red-mushroom) is fully delivered.
func (p *SwampHutPiece) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	planks := stateOf(block.SprucePlanks{})
	oakLog := stateOf(block.OakLog{Axis: block.Y})
	oakFence := stateOf(block.OakFence{})
	air := stateAir

	gb := func(x0, y0, z0, x1, y1, z1 int, st block.StateID) {
		p.generateBox(view, box, x0, y0, z0, x1, y1, z1, st, st, false)
	}

	// The floor + walls (spruce planks).
	gb(1, 1, 1, 5, 1, 7, planks)
	gb(1, 4, 2, 5, 4, 7, planks)
	gb(2, 1, 0, 4, 1, 0, planks)
	gb(2, 2, 2, 3, 3, 2, planks)
	gb(1, 2, 3, 1, 3, 6, planks)
	gb(5, 2, 3, 5, 3, 6, planks)
	gb(2, 2, 7, 4, 3, 7, planks)

	// The oak-log stilts (4 corners) down to terrain.
	gb(1, 0, 2, 1, 3, 2, oakLog)
	gb(5, 0, 2, 5, 3, 2, oakLog)
	gb(1, 0, 7, 1, 3, 7, oakLog)
	gb(5, 0, 7, 5, 3, 7, oakLog)

	// The two oak-fence railings.
	p.placeBlock(view, oakFence, 2, 3, 2, box)
	p.placeBlock(view, oakFence, 3, 3, 7, box)

	// The doorway airing.
	p.placeBlock(view, air, 1, 3, 4, box)
	p.placeBlock(view, air, 5, 3, 4, box)
	p.placeBlock(view, air, 5, 3, 5, box)

	// The interior furnishings.
	p.placeBlock(view, stateOf(block.PottedRedMushroom{}), 1, 3, 5, box)
	p.placeBlock(view, stateOf(block.CraftingTable{}), 3, 2, 6, box)
	p.placeBlock(view, stateOf(block.Cauldron{}), 4, 2, 6, box)
	p.placeBlock(view, oakFence, 1, 2, 1, box)
	p.placeBlock(view, oakFence, 5, 2, 1, box)

	// The spruce-stair roof overhang (4 facings; the OUTER corners get OUTER_LEFT/RIGHT shape).
	northStair := spruceStair(block.North, block.StairsShapeStraight)
	eastStair := spruceStair(block.East, block.StairsShapeStraight)
	westStair := spruceStair(block.West, block.StairsShapeStraight)
	southStair := spruceStair(block.South, block.StairsShapeStraight)
	gb6 := func(x0, y0, z0, x1, y1, z1 int, st block.StateID) {
		p.generateBox(view, box, x0, y0, z0, x1, y1, z1, st, st, false)
	}
	gb6(0, 4, 1, 6, 4, 1, northStair)
	gb6(0, 4, 2, 0, 4, 7, eastStair)
	gb6(6, 4, 2, 6, 4, 7, westStair)
	gb6(0, 4, 8, 6, 4, 8, southStair)

	// The 4 roof corners (the OUTER stair shapes).
	p.placeBlock(view, spruceStair(block.North, block.StairsShapeOuterRight), 0, 4, 1, box)
	p.placeBlock(view, spruceStair(block.North, block.StairsShapeOuterLeft), 6, 4, 1, box)
	p.placeBlock(view, spruceStair(block.South, block.StairsShapeOuterLeft), 0, 4, 8, box)
	p.placeBlock(view, spruceStair(block.South, block.StairsShapeOuterRight), 6, 4, 8, box)

	// The 4 corner stilts fill down to terrain (loop i in {2,7} step 5, j in {1,5} step 4).
	for i := 2; i <= 7; i += 5 {
		for j := 1; j <= 5; j += 4 {
			p.fillColumnDown(view, oakLog, j, -1, i, box, box.MinY)
		}
	}

	// The witch + black-cat spawns (the postProcess tail). Both are recorded as SpawnRequests
	// (live mobs the tick adds to the entity store — NOT off-tick entities, Pitfall 5), one-shot
	// guarded so a re-run / reload does not double-spawn (Pitfall 6). STRUCT-POLISH-02.
	p.spawnWitch(view, box)
	p.spawnCat(view, box)
}

// spawnWitch ports SwampHutPiece.postProcess's witch tail: guarded on !spawnedWitch, the witch
// spawns at getWorldPos(2,2,5) — block-center +0.5 on X/Z (snapTo(x+0.5, y, z+0.5, 0, 0)) —
// when that world pos is inside the chunk's writable box, with setPersistenceRequired +
// finalizeSpawn(STRUCTURE). It RECORDS a SpawnRequest through the view (the tick performs the
// add); the one-shot guard flips so a re-pass / reload spawns no second witch.
//
// Source: javap SwampHutPiece.postProcess (the spawnedWitch block: getWorldPos(2,2,5);
// box.isInside; spawnedWitch=true; WITCH.create + setPersistenceRequired + snapTo + finalizeSpawn
// + addFreshEntityWithPassengers).
func (p *SwampHutPiece) spawnWitch(view WorldGenView, box BoundingBox) {
	if p.spawnedWitch {
		return
	}
	wx := p.getWorldX(2, 5)
	wy := p.getWorldY(2)
	wz := p.getWorldZ(2, 5)
	if !box.IsInside(wx, wy, wz) {
		return // out of THIS chunk's box -> the witch belongs to a different chunk's pass
	}
	p.spawnedWitch = true
	view.RecordSpawn(SpawnRequest{
		EntityType:          "minecraft:witch",
		X:                   float64(wx) + 0.5,
		Y:                   float64(wy),
		Z:                   float64(wz) + 0.5,
		PersistenceRequired: true,
	})
}

// spawnCat ports SwampHutPiece.spawnCat: guarded on !spawnedCat, the cat spawns at the SAME
// getWorldPos(2,2,5)+0.5 when in-box, with setPersistenceRequired + finalizeSpawn(STRUCTURE).
// Recorded as a SpawnRequest (the tick adds it); the one-shot guard prevents a reload double.
//
// Source: javap SwampHutPiece.spawnCat (the spawnedCat block: getWorldPos(2,2,5); box.isInside;
// spawnedCat=true; CAT.create + setPersistenceRequired + snapTo + finalizeSpawn + add).
func (p *SwampHutPiece) spawnCat(view WorldGenView, box BoundingBox) {
	if p.spawnedCat {
		return
	}
	wx := p.getWorldX(2, 5)
	wy := p.getWorldY(2)
	wz := p.getWorldZ(2, 5)
	if !box.IsInside(wx, wy, wz) {
		return
	}
	p.spawnedCat = true
	view.RecordSpawn(SpawnRequest{
		EntityType:          "minecraft:cat",
		X:                   float64(wx) + 0.5,
		Y:                   float64(wy),
		Z:                   float64(wz) + 0.5,
		PersistenceRequired: true,
	})
}

// spruceStair resolves a spruce stair facing dir with shape (BOTTOM, non-waterlogged).
func spruceStair(dir block.Direction, shape block.StairsShape) block.StateID {
	return stateOf(block.SpruceStairs{Facing: dir, Half: block.Bottom, Shape: shape, Waterlogged: false})
}
