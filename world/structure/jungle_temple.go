package structure

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// jungle_temple (jungle_pyramid) placement constants (cross-checked vs the embedded
// structure_set jungle_temples.json + has_structure/jungle_temple tag): salt 14357619,
// spacing 32, separation 8, LINEAR spread; biome allow-set {bamboo_jungle, jungle}.
// The jungle_pyramid SHARES the desert set's spacing/separation but has its OWN salt
// 14357619 (verified, NOT the desert 14357617 — Pitfall #3).
const (
	jungleTempleSalt       = 14357619
	jungleTempleSpacing    = 32
	jungleTempleSeparation = 8

	// JungleTemplePiece ctor (javap -c): super(JUNGLE_PYRAMID_PIECE, west, 64, north, 12,
	// 10, 15, getRandomHorizontalDirection(random)) — a 12x10x15 box at floor y=64.
	jungleTempleWidth  = 12
	jungleTempleHeight = 10
	jungleTempleDepth  = 15
	jungleTempleFloor  = 64

	// jungleTempleMainLoot / jungleTempleHiddenLoot are the two chest loot-table ids
	// (BuiltInLootTables.JUNGLE_TEMPLE / JUNGLE_TEMPLE_DISPENSER for the dispensers). LOOT
	// DEFERRED v3: the chest/dispenser BLOCKS are placed + the table id recorded; no loot is
	// rolled and the dispensers hold no arrows (block entities / loot are a v3 subsystem).
	jungleTempleMainLoot      = "minecraft:chests/jungle_temple"
	jungleTempleDispenserLoot = "minecraft:chests/jungle_temple_dispenser"
)

// jungleTempleStartGen is the jungle_temple StartGenerator (mirrors desertPyramidStartGen):
// the salt-14357619 random_spread math decides WHERE, a REAL jungle biome check at the
// chosen point gates (NO accept-by-default), the surface Y projects, and a JungleTemplePiece
// wrapped in a StructureStart is built.
//
// Source: javap -c JungleTempleStructure (extends SinglePieceStructure -> findGenerationPoint:
// getLowestY sea-level gate + onTopOfChunkCenter WORLD_SURFACE_WG + isValidBiome).
type jungleTempleStartGen struct {
	placement  RandomSpreadStructurePlacement
	biomeAllow map[string]bool
}

// NewJungleTempleStartGen builds the generator from the embedded structure_set + biome tag.
func NewJungleTempleStartGen() (StartGenerator, error) {
	set, err := LoadStructureSet("minecraft:jungle_temples")
	if err != nil {
		return nil, err
	}
	allow, err := HasStructureBiomes("jungle_temple")
	if err != nil {
		return nil, err
	}
	return &jungleTempleStartGen{placement: set.Placement, biomeAllow: allow}, nil
}

// GenerateStarts ports JungleTempleStructure's start decision for chunk pos (pure over
// (seed,pos)). Identical shape to the desert pyramid (SinglePieceStructure): isStructureChunk
// gate, getLowestY sea-level gate, the chunk-center biome gate (jungle allow-set, no
// accept-by-default), the SampleSurfaceY project, the orientation-draw piece, the baked
// terrain-height move. The jungle temple's height move is updateAverageGroundHeight (offset 0,
// NO RNG draw — unlike the desert pyramid's -nextInt(3)), so we bake the AVERAGE ground height
// over the footprint and make no extra RNG draw.
func (g *jungleTempleStartGen) GenerateStarts(seed int64, pos level.ChunkPos, sampler SurfaceSampler, biomeAt BiomeAt) []*StructureStart {
	cx, cz := int(pos[0]), int(pos[1])
	if !g.placement.IsStructureChunk(seed, cx, cz) {
		return nil
	}

	minBlockX := cx * 16
	minBlockZ := cz * 16

	// getLowestY over the 12x15 footprint corners; below sea level -> reject (dry land).
	lowest := lowestCornerY(sampler, minBlockX, minBlockZ, jungleTempleWidth, jungleTempleDepth)
	if lowest < overworldSeaLevel {
		return nil
	}

	// Biome gate at chunk-center: a non-jungle origin yields NO start (no accept-by-default).
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
	piece := newJungleTemplePiece(rng, minBlockX, minBlockZ)

	// Bake updateAverageGroundHeight: the bbox minY is moved to the AVERAGE ground height
	// over the footprint + offset 0 (NO RNG draw — vanilla's updateAverageGroundHeight passes
	// i=0 and averages MOTION_BLOCKING_NO_LEAVES, drawing nothing). We approximate the average
	// from the surface sampler over the footprint corners (the heightmap-at-STARTS preliminary
	// surface) and move the bbox there, RNG-faithful + order-independent.
	groundAvg := averageCornerY(sampler, piece.bbox.MinX, piece.bbox.MinZ, piece.width, piece.depth)
	piece.Move(0, groundAvg-piece.bbox.MinY, 0)

	start := &StructureStart{
		Structure: "minecraft:jungle_pyramid",
		ChunkPos:  pos,
		Pieces:    []Piece{piece},
	}
	start.RecomputeBBox()
	return []*StructureStart{start}
}

// averageCornerY ports the spirit of updateAverageGroundHeight (the integer-average ground
// height) over the 4 footprint corners. Vanilla averages the heightmap over the WHOLE
// footprint; the corner average is the order-independent, sampler-cheap approximation the
// heightmap-at-STARTS surface supports — deterministic over (seed,pos).
func averageCornerY(sampler SurfaceSampler, minX, minZ, sizeX, sizeZ int) int {
	a := sampler.SampleSurfaceY(minX, minZ)
	b := sampler.SampleSurfaceY(minX, minZ+sizeZ)
	c := sampler.SampleSurfaceY(minX+sizeX, minZ)
	d := sampler.SampleSurfaceY(minX+sizeX, minZ+sizeZ)
	return (a + b + c + d) / 4
}

// mossStoneSelector ports JungleTemplePiece$MossStoneSelector: next() draws rng.nextFloat()
// and picks COBBLESTONE if < 0.4 else MOSSY_COBBLESTONE. The x/y/z/isEdge args are ignored
// (the jar's selector ignores them). The per-cell draw is the load-bearing RNG advance.
type mossStoneSelector struct {
	cobble      block.StateID
	mossyCobble block.StateID
}

func (s mossStoneSelector) next(rng levelgen.RandomSource, _, _, _ int, _ bool) block.StateID {
	if rng.NextFloat() < 0.4 {
		return s.cobble
	}
	return s.mossyCobble
}

// JungleTemplePiece is the single hardcoded piece (0 .nbt) of the jungle temple: the
// cobblestone/mossy-cobblestone stepped tower + the two hidden chambers + the
// tripwire/redstone/dispenser trap + the lever/sticky-piston puzzle + 2 chests (blocks +
// loot tag, loot deferred). Built via the 14-02 base-piece helpers + the MossStoneSelector.
//
// Source: javap -c net.minecraft.world.level.levelgen.structure.structures.JungleTemplePiece
// .postProcess (transcribed block-by-block + draw order, incl. the per-cell moss-selector
// draws which are the determinism contract).
type JungleTemplePiece struct {
	StructurePiece
	width, depth, height int
	// LootChests records the 2 chest world positions + the loot-table id (loot deferred v3).
	LootChests []LootChest
}

// newJungleTemplePiece ports the JungleTemplePiece(random, west, north) ctor.
func newJungleTemplePiece(rng levelgen.RandomSource, west, north int) *JungleTemplePiece {
	dir := getRandomHorizontalDirection(rng)
	p := &JungleTemplePiece{
		width:  jungleTempleWidth,
		height: jungleTempleHeight,
		depth:  jungleTempleDepth,
	}
	p.bbox = makeScatteredBoundingBox(west, jungleTempleFloor, north, dir, jungleTempleWidth, jungleTempleHeight, jungleTempleDepth)
	p.setOrientation(dir, true)
	return p
}

// PostProcess writes the full hardcoded jungle-temple geometry, JAR-EXACT block-by-block +
// draw order (JungleTemplePiece.postProcess). All coords are piece-LOCAL; placeBlock/
// generateBox map them to world via the orientation + clip to the chunk's writable box. The
// vanilla updateAverageGroundHeight move was baked into the bbox at START time (no RNG draw),
// so PostProcess uses fixed local coords and is order-independent across overlapping chunks.
//
// LOOT/REDSTONE DEFERRED v3: the 2 chests place as the chest BLOCK + the loot-table tag (no
// loot rolled); the 2 dispensers place as dispenser BLOCKS (no arrows); the tripwire/redstone/
// lever/sticky-piston puzzle places as BLOCKS (no redstone runtime). The VISIBLE temple is
// fully delivered. The per-cell MossStoneSelector nextFloat draws are RNG-faithful.
func (p *JungleTemplePiece) PostProcess(view WorldGenView, box BoundingBox, _ level.ChunkPos, rng levelgen.RandomSource) {
	w := p.width
	d := p.depth
	sel := mossStoneSelector{cobble: stateOf(block.Cobblestone{}), mossyCobble: stateOf(block.MossyCobblestone{})}
	air := stateAir
	mossy := stateOf(block.MossyCobblestone{})
	chiseled := stateOf(block.ChiseledStoneBricks{})

	gb := func(x0, y0, z0, x1, y1, z1 int) {
		p.generateBoxSelector(view, box, x0, y0, z0, x1, y1, z1, false, rng, sel)
	}

	// The foundation + the outer shell (cobble/mossy selector fill).
	gb(0, -4, 0, w-1, 0, d-1)
	gb(2, 1, 2, 9, 2, 2)
	gb(2, 1, 12, 9, 2, 12)
	gb(2, 1, 3, 2, 2, 11)
	gb(9, 1, 3, 9, 2, 11)
	gb(1, 3, 1, 10, 6, 1)
	gb(1, 3, 13, 10, 6, 13)
	gb(1, 3, 2, 1, 6, 12)
	gb(10, 3, 2, 10, 6, 12)
	gb(2, 3, 2, 9, 3, 12)
	gb(2, 6, 2, 9, 6, 12)
	gb(3, 7, 3, 8, 7, 11)
	gb(4, 8, 4, 7, 8, 10)

	// Hollow out the interior.
	p.generateAirBox(view, box, 3, 1, 3, 8, 2, 11)
	p.generateAirBox(view, box, 4, 3, 6, 7, 3, 9)
	p.generateAirBox(view, box, 2, 4, 2, 9, 5, 12)
	p.generateAirBox(view, box, 4, 6, 5, 7, 6, 9)
	p.generateAirBox(view, box, 5, 7, 6, 6, 7, 8)
	p.generateAirBox(view, box, 5, 1, 2, 6, 2, 2)
	p.generateAirBox(view, box, 5, 2, 12, 6, 2, 12)
	p.generateAirBox(view, box, 5, 5, 1, 6, 5, 1)
	p.generateAirBox(view, box, 5, 5, 13, 6, 5, 13)
	p.placeBlock(view, air, 1, 5, 5, box)
	p.placeBlock(view, air, 10, 5, 5, box)
	p.placeBlock(view, air, 1, 5, 9, box)
	p.placeBlock(view, air, 10, 5, 9, box)

	// The 4 stepped corner pillars (loop x in {0,14} clamped: the jar loops i=0;i<=14;i+=14).
	for x := 0; x <= 14; x += 14 {
		gb(2, 4, x, 2, 5, x)
		gb(4, 4, x, 4, 5, x)
		gb(7, 4, x, 7, 5, x)
		gb(9, 4, x, 9, 5, x)
	}
	gb(5, 6, 0, 6, 6, 0)

	// The roof bands (nested loop: i in {0,11}, j in [2..12] step 2).
	for i := 0; i <= 11; i += 11 {
		for j := 2; j <= 12; j += 2 {
			gb(i, 4, j, i, 5, j)
		}
		gb(i, 6, 5, i, 6, 5)
		gb(i, 6, 9, i, 6, 9)
	}

	// The corner spires.
	gb(2, 7, 2, 2, 9, 2)
	gb(9, 7, 2, 9, 9, 2)
	gb(2, 7, 12, 2, 9, 12)
	gb(9, 7, 12, 9, 9, 12)
	gb(4, 9, 4, 4, 9, 4)
	gb(7, 9, 4, 7, 9, 4)
	gb(4, 9, 10, 4, 9, 10)
	gb(7, 9, 10, 7, 9, 10)
	gb(5, 9, 7, 6, 9, 7)

	// The roof cobblestone stairs.
	p.placeBlock(view, cobbleStair(block.North), 5, 9, 6, box)
	p.placeBlock(view, cobbleStair(block.North), 6, 9, 6, box)
	p.placeBlock(view, cobbleStair(block.South), 5, 9, 8, box)
	p.placeBlock(view, cobbleStair(block.South), 6, 9, 8, box)
	p.placeBlock(view, cobbleStair(block.North), 4, 0, 0, box)
	p.placeBlock(view, cobbleStair(block.North), 5, 0, 0, box)
	p.placeBlock(view, cobbleStair(block.North), 6, 0, 0, box)
	p.placeBlock(view, cobbleStair(block.North), 7, 0, 0, box)
	p.placeBlock(view, cobbleStair(block.North), 4, 1, 8, box)
	p.placeBlock(view, cobbleStair(block.North), 4, 2, 9, box)
	p.placeBlock(view, cobbleStair(block.North), 4, 3, 10, box)
	p.placeBlock(view, cobbleStair(block.North), 7, 1, 8, box)
	p.placeBlock(view, cobbleStair(block.North), 7, 2, 9, box)
	p.placeBlock(view, cobbleStair(block.North), 7, 3, 10, box)
	gb(4, 1, 9, 4, 1, 9)
	gb(7, 1, 9, 7, 1, 9)
	gb(4, 1, 10, 7, 2, 10)
	gb(5, 4, 5, 6, 4, 5)
	p.placeBlock(view, cobbleStair(block.East), 4, 4, 5, box)
	p.placeBlock(view, cobbleStair(block.West), 7, 4, 5, box)

	// The entrance steps (loop i in [0..3]).
	for i := 0; i < 4; i++ {
		p.placeBlock(view, cobbleStair(block.South), 5, 0-i, 6+i, box)
		p.placeBlock(view, cobbleStair(block.South), 6, 0-i, 6+i, box)
		p.generateAirBox(view, box, 5, 0-i, 7+i, 6, 0-i, 9+i)
	}

	// Hollow out the basement chamber.
	p.generateAirBox(view, box, 1, -3, 12, 10, -1, 13)
	p.generateAirBox(view, box, 1, -3, 1, 3, -1, 13)
	p.generateAirBox(view, box, 1, -3, 1, 9, -1, 5)

	// The basement floor/ceiling bands (two loops over i step 2).
	for i := 1; i <= 13; i += 2 {
		gb(1, -3, i, 1, -2, i)
	}
	for i := 2; i <= 12; i += 2 {
		gb(1, -1, i, 3, -1, i)
	}

	// The basement walls.
	gb(2, -2, 1, 5, -2, 1)
	gb(7, -2, 1, 9, -2, 1)
	gb(6, -3, 1, 6, -3, 1)
	gb(6, -1, 1, 6, -1, 1)

	// === Trap 1 (the west tripwire -> dispenser arrow trap; loot/redstone deferred). ===
	p.placeBlock(view, tripwireHook(block.East), 1, -3, 8, box)
	p.placeBlock(view, tripwireHook(block.West), 4, -3, 8, box)
	p.placeBlock(view, tripwireEW(), 2, -3, 8, box)
	p.placeBlock(view, tripwireEW(), 3, -3, 8, box)
	p.placeBlock(view, redstoneNS(), 5, -3, 7, box)
	p.placeBlock(view, redstoneNS(), 5, -3, 6, box)
	p.placeBlock(view, redstoneNS(), 5, -3, 5, box)
	p.placeBlock(view, redstoneNS(), 5, -3, 4, box)
	p.placeBlock(view, redstoneNS(), 5, -3, 3, box)
	p.placeBlock(view, redstoneNS(), 5, -3, 2, box)
	p.placeBlock(view, redstoneNW(), 5, -3, 1, box)
	p.placeBlock(view, redstoneEW(), 4, -3, 1, box)
	p.placeBlock(view, mossy, 3, -3, 1, box)
	// The dispenser (block only; arrows deferred v3).
	p.createDispenser(view, box, 3, -2, 1, block.North, jungleTempleDispenserLoot)
	p.placeBlock(view, vineSouth(), 3, -2, 2, box)

	// === Trap 2 (the east tripwire -> dispenser trap). ===
	p.placeBlock(view, tripwireHook(block.North), 7, -3, 1, box)
	p.placeBlock(view, tripwireHook(block.South), 7, -3, 5, box)
	p.placeBlock(view, tripwireNS(), 7, -3, 2, box)
	p.placeBlock(view, tripwireNS(), 7, -3, 3, box)
	p.placeBlock(view, tripwireNS(), 7, -3, 4, box)
	p.placeBlock(view, redstoneEW(), 8, -3, 6, box)
	p.placeBlock(view, redstoneWS(), 9, -3, 6, box)
	p.placeBlock(view, redstoneNSU(), 9, -3, 5, box)
	p.placeBlock(view, mossy, 9, -3, 4, box)
	p.placeBlock(view, redstoneNS(), 9, -2, 4, box)
	p.createDispenser(view, box, 9, -2, 3, block.West, jungleTempleDispenserLoot)
	p.placeBlock(view, vineEast(), 8, -1, 3, box)
	p.placeBlock(view, vineEast(), 8, -2, 3, box)

	// The main hidden chest (block + loot tag, loot deferred).
	p.createChest(view, box, 8, -3, 3, jungleTempleMainLoot, &p.LootChests)

	// The chamber framing.
	p.placeBlock(view, mossy, 9, -3, 2, box)
	p.placeBlock(view, mossy, 8, -3, 1, box)
	p.placeBlock(view, mossy, 4, -3, 5, box)
	p.placeBlock(view, mossy, 5, -2, 5, box)
	p.placeBlock(view, mossy, 5, -1, 5, box)
	p.placeBlock(view, mossy, 6, -3, 5, box)
	p.placeBlock(view, mossy, 7, -2, 5, box)
	p.placeBlock(view, mossy, 7, -1, 5, box)
	p.placeBlock(view, mossy, 8, -3, 5, box)
	gb(9, -1, 1, 9, -1, 5)

	// === The sticky-piston / lever treasure puzzle (blocks only; redstone deferred). ===
	p.generateAirBox(view, box, 8, -3, 8, 10, -1, 10)
	p.placeBlock(view, chiseled, 8, -2, 11, box)
	p.placeBlock(view, chiseled, 9, -2, 11, box)
	p.placeBlock(view, chiseled, 10, -2, 11, box)
	p.placeBlock(view, leverWall(block.North), 8, -2, 12, box)
	p.placeBlock(view, leverWall(block.North), 9, -2, 12, box)
	p.placeBlock(view, leverWall(block.North), 10, -2, 12, box)
	gb(8, -3, 8, 8, -3, 10)
	gb(10, -3, 8, 10, -3, 10)
	p.placeBlock(view, mossy, 10, -2, 9, box)
	p.placeBlock(view, redstoneNS(), 8, -2, 9, box)
	p.placeBlock(view, redstoneNS(), 8, -2, 10, box)
	p.placeBlock(view, redstoneNSEW(), 10, -1, 9, box)
	p.placeBlock(view, stickyPiston(block.Up), 9, -2, 8, box)
	p.placeBlock(view, stickyPiston(block.West), 10, -2, 8, box)
	p.placeBlock(view, stickyPiston(block.West), 10, -1, 8, box)
	p.placeBlock(view, repeaterNorth(), 10, -2, 10, box)

	// The hidden treasure chest behind the piston (block + loot tag).
	p.createChest(view, box, 9, -3, 10, jungleTempleMainLoot, &p.LootChests)
}

// createDispenser ports JungleTemplePiece.createDispenser as a BLOCK placement (arrows
// DEFERRED v3 — the dispenser block faces `dir`, transformed by the piece orientation; no
// arrow loot is loaded, that is a v3 block-entity subsystem). The lootTable id is ignored
// here (recorded only for chests); the dispenser block is the deliverable.
func (p *JungleTemplePiece) createDispenser(view WorldGenView, box BoundingBox, x, y, z int, dir block.Direction, _ string) {
	st := stateOf(block.Dispenser{Facing: dir, Triggered: false})
	p.placeBlock(view, st, x, y, z, box)
}

// --- Jungle-temple block-state helpers (resolved via block.ToStateID). ---

func cobbleStair(dir block.Direction) block.StateID {
	return stateOf(block.CobblestoneStairs{Facing: dir, Half: block.Bottom, Shape: block.StairsShapeStraight, Waterlogged: false})
}

func tripwireHook(dir block.Direction) block.StateID {
	return stateOf(block.TripwireHook{Facing: dir, Attached: true, Powered: false})
}

func tripwireEW() block.StateID {
	return stateOf(block.Tripwire{East: true, West: true, Attached: true})
}

func tripwireNS() block.StateID {
	return stateOf(block.Tripwire{North: true, South: true, Attached: true})
}

func redstoneNS() block.StateID {
	return stateOf(block.RedstoneWire{North: block.RedstoneSideSide, South: block.RedstoneSideSide})
}

func redstoneEW() block.StateID {
	return stateOf(block.RedstoneWire{East: block.RedstoneSideSide, West: block.RedstoneSideSide})
}

func redstoneNW() block.StateID {
	return stateOf(block.RedstoneWire{North: block.RedstoneSideSide, West: block.RedstoneSideSide})
}

func redstoneWS() block.StateID {
	return stateOf(block.RedstoneWire{West: block.RedstoneSideSide, South: block.RedstoneSideSide})
}

func redstoneNSU() block.StateID {
	return stateOf(block.RedstoneWire{North: block.RedstoneSideSide, South: block.RedstoneSideUp})
}

func redstoneNSEW() block.StateID {
	return stateOf(block.RedstoneWire{North: block.RedstoneSideSide, South: block.RedstoneSideSide, East: block.RedstoneSideSide, West: block.RedstoneSideSide})
}

func vineSouth() block.StateID { return stateOf(block.Vine{South: true}) }
func vineEast() block.StateID  { return stateOf(block.Vine{East: true}) }

func leverWall(dir block.Direction) block.StateID {
	return stateOf(block.Lever{Facing: dir, Face: block.AttachFaceWall, Powered: false})
}

func stickyPiston(dir block.Direction) block.StateID {
	return stateOf(block.StickyPiston{Facing: dir, Extended: false})
}

func repeaterNorth() block.StateID {
	return stateOf(block.Repeater{Facing: block.North, Delay: 1, Locked: false, Powered: false})
}
