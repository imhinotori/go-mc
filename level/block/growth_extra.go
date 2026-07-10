package block

// growth_extra.go -- block-family predicates and state accessors for the CACTUS / BAMBOO / ICE /
// SNOW-layer random-tick handlers and the COMPOSTER, ported 1:1 from the unobfuscated 26.2 jar. Each
// predicate is the Go realization of the vanilla instanceof / state.getValue / state.is check the
// corresponding server handler needs. A small new file (rather than editing the big generated
// blocks.go) keeps the generated data untouched. CITE per function.
//
// NOTE: IsCactus already lives in path_types.go (isType[Cactus]); IsSnowLayerOne / IsSnowySetting /
// FluidIsFull live in growth.go; IsWaterFluid lives in crop.go. This file adds only the accessors
// those existing predicates do not already cover.

// ---- CACTUS (CactusBlock) ----

// CactusAge is state.getValue(CactusBlock.AGE) for a cactus state (0..15), or -1 for a non-cactus.
// CITE: CactusBlock.AGE (IntegerProperty 0..15).
func CactusAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if c, ok := StateList[s].(Cactus); ok {
		return int(c.Age)
	}
	return -1
}

// CactusWithAge returns the cactus state with AGE set to age (0..15). ok=false for a non-cactus s or
// an out-of-range age. CITE: state.setValue(CactusBlock.AGE, age).
func CactusWithAge(s StateID, age int) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || age < 0 || age > 15 {
		return s, false
	}
	if _, ok := StateList[s].(Cactus); !ok {
		return s, false
	}
	id, ok := ToStateID[Cactus{Age: Integer(age)}]
	return id, ok
}

// CactusDefaultState returns Blocks.CACTUS.defaultBlockState() == Cactus{AGE:0}. CITE:
// CactusBlock.defaultBlockState (AGE default 0).
func CactusDefaultState() StateID {
	return ToStateID[Cactus{Age: 0}]
}

// CactusFlowerState returns Blocks.CACTUS_FLOWER.defaultBlockState(). CITE: Blocks.CACTUS_FLOWER.
func CactusFlowerState() StateID {
	return ToStateID[CactusFlower{}]
}

// ---- COCOA (CocoaBlock) ----

// CocoaMaxAge is CocoaBlock.MAX_AGE (2). CITE: CocoaBlock.MAX_AGE.
const CocoaMaxAge = 2

// CocoaAge is state.getValue(CocoaBlock.AGE) (0..2) for a cocoa state, or -1 for a non-cocoa. CITE:
// CocoaBlock.AGE (IntegerProperty 0..2).
func CocoaAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if c, ok := StateList[s].(Cocoa); ok {
		return int(c.Age)
	}
	return -1
}

// CocoaWithAge returns the cocoa state with AGE set to age (0..2), PRESERVING the FACING (a cocoa pod
// is attached to a jungle log on one horizontal face; setValue(AGE) keeps FACING). ok=false for a
// non-cocoa s or an out-of-range age. CITE: CocoaBlock.randomTick (state.setValue(AGE, age+1)).
func CocoaWithAge(s StateID, age int) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || age < 0 || age > CocoaMaxAge {
		return s, false
	}
	c, ok := StateList[s].(Cocoa)
	if !ok {
		return s, false
	}
	id, ok := ToStateID[Cocoa{Age: Integer(age), Facing: c.Facing}]
	return id, ok
}

// ---- BAMBOO (BambooStalkBlock / BambooSaplingBlock) ----

// IsBamboo reports Blocks.BAMBOO (a bamboo STALK, any age/leaves/stage). CITE: BambooStalkBlock.
func IsBamboo(s StateID) bool { return isType[Bamboo](s) }

// IsBambooSapling reports Blocks.BAMBOO_SAPLING. CITE: BambooSaplingBlock.
func IsBambooSapling(s StateID) bool { return isType[BambooSapling](s) }

// BambooAge is state.getValue(BambooStalkBlock.AGE) (0..1) for a bamboo stalk, or -1 for non-bamboo.
// CITE: BambooStalkBlock.AGE (IntegerProperty 0..1).
func BambooAge(s StateID) int {
	if b, ok := bambooOf(s); ok {
		return int(b.Age)
	}
	return -1
}

// BambooStage is state.getValue(BambooStalkBlock.STAGE) (0..1) for a bamboo stalk, or -1 otherwise.
// STAGE 0 is the still-growing stalk (isRandomlyTicking); STAGE 1 is done growing. CITE:
// BambooStalkBlock.STAGE.
func BambooStage(s StateID) int {
	if b, ok := bambooOf(s); ok {
		return int(b.Stage)
	}
	return -1
}

// BambooLeavesOf is state.getValue(BambooStalkBlock.LEAVES) for a bamboo stalk (NONE for non-bamboo).
// CITE: BambooStalkBlock.LEAVES (BambooLeaves enum).
func BambooLeavesOf(s StateID) BambooLeaves {
	if b, ok := bambooOf(s); ok {
		return b.Leaves
	}
	return BambooLeavesNone
}

// BambooState builds the bamboo stalk state with the given AGE/LEAVES/STAGE. ok=false on an
// out-of-range age/stage. CITE: BambooStalkBlock.defaultBlockState().setValue(AGE/LEAVES/STAGE).
func BambooState(age int, leaves BambooLeaves, stage int) (StateID, bool) {
	if age < 0 || age > 1 || stage < 0 || stage > 1 {
		return 0, false
	}
	id, ok := ToStateID[Bamboo{Age: Integer(age), Leaves: leaves, Stage: Integer(stage)}]
	return id, ok
}

// bambooOf resolves s to a Bamboo struct value. CITE: instanceof BambooStalkBlock state.
func bambooOf(s StateID) (Bamboo, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return Bamboo{}, false
	}
	b, ok := StateList[s].(Bamboo)
	return b, ok
}

// ---- ICE (IceBlock) ----

// IsIce reports Blocks.ICE (the meltable clear ice ONLY -- NOT packed_ice / blue_ice / frosted_ice,
// which are distinct blocks with no melt randomTick). CITE: IceBlock (Blocks.ICE).
func IsIce(s StateID) bool { return isType[Ice](s) }

// WaterState returns Blocks.WATER.defaultBlockState() == Water{LEVEL:0} (a full water SOURCE). This
// is IceBlock.melt's meltsInto() target. CITE: IceBlock.melt (setBlockAndUpdate(pos, WATER));
// Blocks.WATER default (LEVEL 0 == source).
func WaterState() StateID {
	return ToStateID[Water{Level: 0}]
}

// ---- SNOW LAYER (SnowLayerBlock) ----

// IsSnowLayer reports Blocks.SNOW (the snow LAYER block, LAYERS 1..8), NOT snow_block/powder_snow.
// CITE: SnowLayerBlock (Blocks.SNOW).
func IsSnowLayer(s StateID) bool { return isType[Snow](s) }

// SnowLayers is state.getValue(SnowLayerBlock.LAYERS) (1..8) for a snow layer, or -1 otherwise.
// CITE: SnowLayerBlock.LAYERS.
func SnowLayers(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if sn, ok := StateList[s].(Snow); ok {
		return int(sn.Layers)
	}
	return -1
}

// ---- COMPOSTER (ComposterBlock) ----

// IsComposter reports Blocks.COMPOSTER (any LEVEL). CITE: ComposterBlock.
func IsComposter(s StateID) bool { return isType[Composter](s) }

// ComposterLevel is state.getValue(ComposterBlock.LEVEL) (0..8) for a composter, or -1 otherwise.
// LEVEL 8 == READY. CITE: ComposterBlock.LEVEL.
func ComposterLevel(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if c, ok := StateList[s].(Composter); ok {
		return int(c.Level)
	}
	return -1
}

// ComposterWithLevel returns the composter state with LEVEL set to lvl (0..8). ok=false for a
// non-composter s or an out-of-range level. CITE: state.setValue(ComposterBlock.LEVEL, lvl).
func ComposterWithLevel(s StateID, lvl int) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) || lvl < 0 || lvl > 8 {
		return s, false
	}
	if _, ok := StateList[s].(Composter); !ok {
		return s, false
	}
	id, ok := ToStateID[Composter{Level: Integer(lvl)}]
	return id, ok
}

// StateLiquid reports BlockState.liquid() -- true for a fluid block (water/lava at any level). Used
// by CactusBlock.canSurvive's `above.above().liquid()` guard. CITE: BlockBehaviour$BlockStateBase
// .liquid() (returns the fluidState-derived liquid flag; true for Water/Lava blocks).
func StateLiquid(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	return IsFluidBlock(StateList[s])
}

// StateFluidIsLava reports getFluidState(pos).is(FluidTags.LAVA) -- true for a lava block at any
// level. Used by CactusBlock.canSurvive's horizontal-neighbour lava guard. CITE: FluidTags.LAVA.
func StateFluidIsLava(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Lava)
	return ok
}
