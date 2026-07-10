package block

// randomtick_extra.go -- block-family predicates and state accessors for the NETHER-WART,
// CHORUS-FLOWER, TURTLE-EGG, BUDDING-AMETHYST (cluster growth), and CAVE-VINES random-tick
// handlers, ported 1:1 from the unobfuscated 26.2 jar. Each predicate/accessor is the Go
// realization of the vanilla state.getValue / state.is / setValue the corresponding server handler
// needs. A small new file (rather than editing the generated blocks.go) keeps the generated data
// untouched. CITE per function.

// ---- NETHER WART (NetherWartBlock) ----

// NetherWartMaxAge is NetherWartBlock.MAX_AGE (3). CITE: NetherWartBlock.MAX_AGE.
const NetherWartMaxAge = 3

// IsNetherWart reports Blocks.NETHER_WART (any AGE). CITE: NetherWartBlock.
func IsNetherWart(s StateID) bool { return isType[NetherWart](s) }

// NetherWartAge is state.getValue(NetherWartBlock.AGE) (0..3), or -1 for a non-wart. CITE:
// NetherWartBlock.AGE (IntegerProperty 0..3).
func NetherWartAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if w, ok := StateList[s].(NetherWart); ok {
		return int(w.Age)
	}
	return -1
}

// NetherWartWithAge returns the nether wart state with AGE set to age (0..3). ok=false for a
// non-wart s or an out-of-range age. CITE: NetherWartBlock.randomTick (setValue(AGE, age+1)).
func NetherWartWithAge(s StateID, age int) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || age < 0 || age > NetherWartMaxAge {
		return s, false
	}
	if _, ok := StateList[s].(NetherWart); !ok {
		return s, false
	}
	id, ok := ToStateID[NetherWart{Age: Integer(age)}]
	return id, ok
}

// ---- CHORUS FLOWER (ChorusFlowerBlock) ----

// ChorusFlowerDeadAge is ChorusFlowerBlock.DEAD_AGE (5) -- also the AGE property max. A flower at
// AGE 5 is dead and no longer randomly ticks. CITE: ChorusFlowerBlock.DEAD_AGE.
const ChorusFlowerDeadAge = 5

// IsChorusFlower reports Blocks.CHORUS_FLOWER (any AGE). CITE: ChorusFlowerBlock.
func IsChorusFlower(s StateID) bool { return isType[ChorusFlower](s) }

// ChorusFlowerAge is state.getValue(ChorusFlowerBlock.AGE) (0..5), or -1 for a non-flower. CITE:
// ChorusFlowerBlock.AGE (IntegerProperty 0..5).
func ChorusFlowerAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if c, ok := StateList[s].(ChorusFlower); ok {
		return int(c.Age)
	}
	return -1
}

// ChorusFlowerWithAge returns the flower state with AGE set to age (0..5). ok=false for a
// non-flower s or an out-of-range age. CITE: ChorusFlowerBlock.placeGrownFlower / placeDeadFlower
// (defaultBlockState().setValue(AGE, age)).
func ChorusFlowerWithAge(s StateID, age int) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || age < 0 || age > ChorusFlowerDeadAge {
		return s, false
	}
	if _, ok := StateList[s].(ChorusFlower); !ok {
		return s, false
	}
	id, ok := ToStateID[ChorusFlower{Age: Integer(age)}]
	return id, ok
}

// ChorusFlowerDefaultState returns Blocks.CHORUS_FLOWER.defaultBlockState() == ChorusFlower{AGE:0}.
// CITE: ChorusFlowerBlock.defaultBlockState (AGE default 0).
func ChorusFlowerDefaultState() StateID {
	return ToStateID[ChorusFlower{Age: 0}]
}

// IsChorusPlant reports Blocks.CHORUS_PLANT -- the stem block a chorus flower converts into when it
// grows (ChorusFlowerBlock.plant). CITE: ChorusFlowerBlock.plant (Blocks.CHORUS_PLANT).
func IsChorusPlant(s StateID) bool { return isType[ChorusPlant](s) }

// ChorusPlantDefaultState returns Blocks.CHORUS_PLANT.defaultBlockState() (all connections false).
// randomTick converts a grown flower via ChorusPlantBlock.getStateWithConnections on this default;
// the connection reconcile is a cited deferral, so the default is the load-bearing stem placement.
// CITE: ChorusFlowerBlock.randomTick (getStateWithConnections(level, pos, plant.defaultBlockState())).
func ChorusPlantDefaultState() StateID {
	return ToStateID[ChorusPlant{}]
}

// IsEndStone reports Blocks.END_STONE -- the block in #minecraft:supports_chorus_flower a chorus
// flower may root on. The 26.2 tag #supports_chorus_flower is exactly { end_stone }. CITE:
// BlockTags.SUPPORTS_CHORUS_FLOWER (tags/block/supports_chorus_flower.json -> end_stone).
func IsEndStone(s StateID) bool { return isType[EndStone](s) }

// ---- TURTLE EGG (TurtleEggBlock) ----

// TurtleEggMaxHatch is TurtleEggBlock.MAX_HATCH_LEVEL (2). CITE: TurtleEggBlock.MAX_HATCH_LEVEL.
const TurtleEggMaxHatch = 2

// IsTurtleEgg reports Blocks.TURTLE_EGG (any EGGS/HATCH). CITE: TurtleEggBlock.
func IsTurtleEgg(s StateID) bool { return isType[TurtleEgg](s) }

// TurtleEggHatch is state.getValue(TurtleEggBlock.HATCH) (0..2), or -1 for a non-egg. CITE:
// TurtleEggBlock.HATCH (IntegerProperty 0..2).
func TurtleEggHatch(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if e, ok := StateList[s].(TurtleEgg); ok {
		return int(e.Hatch)
	}
	return -1
}

// TurtleEggEggs is state.getValue(TurtleEggBlock.EGGS) (1..4), or -1 for a non-egg. CITE:
// TurtleEggBlock.EGGS (IntegerProperty 1..4).
func TurtleEggEggs(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if e, ok := StateList[s].(TurtleEgg); ok {
		return int(e.Eggs)
	}
	return -1
}

// TurtleEggWithHatch returns the egg state with HATCH set to hatch (0..2), PRESERVING EGGS.
// ok=false for a non-egg s or an out-of-range hatch. CITE: TurtleEggBlock.randomTick
// (setValue(HATCH, hatch+1)).
func TurtleEggWithHatch(s StateID, hatch int) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || hatch < 0 || hatch > TurtleEggMaxHatch {
		return s, false
	}
	e, ok := StateList[s].(TurtleEgg)
	if !ok {
		return s, false
	}
	id, ok := ToStateID[TurtleEgg{Eggs: e.Eggs, Hatch: Integer(hatch)}]
	return id, ok
}

// ---- BUDDING AMETHYST (BuddingAmethystBlock) + amethyst clusters ----

// AmethystGrowthChance is BuddingAmethystBlock.GROWTH_CHANCE (5). The growth gate is
// random.nextInt(5) == 0 (a 1-in-5 roll drawn via INT rng, NOT nextFloat < 0.2f -- 26.2 replaced
// the float gate). CITE: BuddingAmethystBlock.GROWTH_CHANCE / randomTick (random.nextInt(5) == 0).
const AmethystGrowthChance = 5

// IsBuddingAmethyst reports Blocks.BUDDING_AMETHYST -- the block that grows amethyst buds each
// random tick. CITE: BuddingAmethystBlock.
func IsBuddingAmethyst(s StateID) bool { return isType[BuddingAmethyst](s) }

// CanClusterGrowAtState is BuddingAmethystBlock.canClusterGrowAtState(state): state.isAir() or
// (state.is(Blocks.WATER) and state.getFluidState().isFull()). A bud/cluster grows into an empty
// cell or a full water source. CITE: BuddingAmethystBlock.canClusterGrowAtState.
func CanClusterGrowAtState(s StateID) bool {
	if IsAir(s) {
		return true
	}
	return IsWaterBlock(s) && FluidIsFull(s)
}

// AmethystClusterKind identifies which amethyst-cluster stage a state is (or none). The growth
// chain is SMALL -> MEDIUM -> LARGE -> CLUSTER (each stage advances only when the neighbour is the
// previous stage AND its FACING equals the drawn direction). CITE: BuddingAmethystBlock.randomTick.
type AmethystClusterKind int

const (
	AmethystNone AmethystClusterKind = iota
	AmethystSmall
	AmethystMedium
	AmethystLarge
	AmethystClusterStage
)

// AmethystKindOf reports which amethyst cluster stage a state is, and its FACING. For a non-cluster
// state it returns (AmethystNone, Down, false). CITE: Blocks.SMALL/MEDIUM/LARGE_AMETHYST_BUD /
// AMETHYST_CLUSTER; AmethystClusterBlock.FACING.
func AmethystKindOf(s StateID) (AmethystClusterKind, Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return AmethystNone, Down, false
	}
	switch b := StateList[s].(type) {
	case SmallAmethystBud:
		return AmethystSmall, b.Facing, true
	case MediumAmethystBud:
		return AmethystMedium, b.Facing, true
	case LargeAmethystBud:
		return AmethystLarge, b.Facing, true
	case AmethystCluster:
		return AmethystClusterStage, b.Facing, true
	default:
		return AmethystNone, Down, false
	}
}

// AmethystNextStateForNeighbour is the grow-target selector inside BuddingAmethystBlock.randomTick:
// given the neighbour state at pos.relative(dir) and the drawn direction, it returns the new bud/
// cluster state to place there (FACING=dir, WATERLOGGED = neighbour fluid is full water), or
// ok=false when no stage advances. Chain: canClusterGrowAtState -> SMALL; SMALL and FACING==dir ->
// MEDIUM; MEDIUM and FACING==dir -> LARGE; LARGE and FACING==dir -> CLUSTER. CITE:
// BuddingAmethystBlock.randomTick (grow-target if/else chain + setValue(FACING)/setValue(WATERLOGGED)).
func AmethystNextStateForNeighbour(neighbour StateID, dir Direction) (StateID, bool) {
	waterlogged := IsWaterBlock(neighbour) && FluidIsFull(neighbour)
	if CanClusterGrowAtState(neighbour) {
		return amethystBuildState(AmethystSmall, dir, waterlogged)
	}
	kind, facing, ok := AmethystKindOf(neighbour)
	if !ok || facing != dir {
		return 0, false
	}
	switch kind {
	case AmethystSmall:
		return amethystBuildState(AmethystMedium, dir, waterlogged)
	case AmethystMedium:
		return amethystBuildState(AmethystLarge, dir, waterlogged)
	case AmethystLarge:
		return amethystBuildState(AmethystClusterStage, dir, waterlogged)
	default:
		return 0, false
	}
}

// amethystBuildState builds the bud/cluster state with FACING=dir and WATERLOGGED=waterlogged.
// CITE: growInto.defaultBlockState().setValue(FACING, dir).setValue(WATERLOGGED, ...).
func amethystBuildState(kind AmethystClusterKind, dir Direction, waterlogged bool) (StateID, bool) {
	var out Block
	switch kind {
	case AmethystSmall:
		out = SmallAmethystBud{Facing: dir, Waterlogged: Boolean(waterlogged)}
	case AmethystMedium:
		out = MediumAmethystBud{Facing: dir, Waterlogged: Boolean(waterlogged)}
	case AmethystLarge:
		out = LargeAmethystBud{Facing: dir, Waterlogged: Boolean(waterlogged)}
	case AmethystClusterStage:
		out = AmethystCluster{Facing: dir, Waterlogged: Boolean(waterlogged)}
	default:
		return 0, false
	}
	id, ok := ToStateID[out]
	return id, ok
}

// ---- CAVE VINES (CaveVinesBlock / GrowingPlantHeadBlock) ----

// CaveVinesMaxAge is GrowingPlantHeadBlock.MAX_AGE (25) -- the AGE cap for the cave-vines HEAD.
// CITE: GrowingPlantHeadBlock.MAX_AGE (25).
const CaveVinesMaxAge = 25

// CaveVinesGrowChance is CaveVinesBlock's growPerTickProbability (0.1). The head grows a cell down
// with a nextDouble() < 0.1 roll. CITE: CaveVinesBlock ctor (growPerTickProbability = 0.1d).
const CaveVinesGrowChance = 0.1

// CaveVinesBerryChance is CaveVinesBlock.CHANCE_OF_BERRIES_ON_GROWTH (0.11f) -- the nextFloat gate
// that decides whether a newly-grown head carries glow berries. CITE:
// CaveVinesBlock.CHANCE_OF_BERRIES_ON_GROWTH (0.11f).
const CaveVinesBerryChance float32 = 0.11

// IsCaveVines reports Blocks.CAVE_VINES -- the cave-vines HEAD (the growing tip). NOT
// CAVE_VINES_PLANT (the body). CITE: CaveVinesBlock (Blocks.CAVE_VINES).
func IsCaveVines(s StateID) bool { return isType[CaveVines](s) }

// CaveVinesAge is state.getValue(GrowingPlantHeadBlock.AGE) (0..25), or -1 for a non-head. CITE:
// GrowingPlantHeadBlock.AGE (0..25).
func CaveVinesAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if c, ok := StateList[s].(CaveVines); ok {
		return int(c.Age)
	}
	return -1
}

// CaveVinesGrowInto returns the new cave-vines HEAD state to place at the cell below: AGE cycled +1
// (state.cycle(AGE)) with BERRIES = berries (the nextFloat<0.11 outcome). ok=false for a non-head s
// or an out-of-range age. CITE: CaveVinesBlock.getGrowIntoState (super.cycle(AGE).setValue(BERRIES,
// nextFloat<0.11)).
func CaveVinesGrowInto(s StateID, age int, berries bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || age < 0 || age > CaveVinesMaxAge {
		return s, false
	}
	if _, ok := StateList[s].(CaveVines); !ok {
		return s, false
	}
	id, ok := ToStateID[CaveVines{Age: Integer(age), Berries: Boolean(berries)}]
	return id, ok
}
