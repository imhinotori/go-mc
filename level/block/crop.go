package block

// crop.go — state predicates + property accessors for the CROP families (wheat, carrots,
// potatoes, beetroots) and FARMLAND, ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.CropBlock / BeetrootBlock / FarmlandBlock). They back the
// crop-growth + farmland-moisture random-tick handlers in the server package.
//
// CROP AGE per family (CITE: <Crop>Block.getMaxAge / getAgeProperty):
//   - WheatBlock / CarrotBlock / PotatoBlock: MAX_AGE 7, AGE property 0..7 (BlockStateProperties.AGE_7).
//   - BeetrootBlock: getMaxAge()==3, AGE property BlockStateProperties.AGE_3 (0..3).
//
// isRandomlyTicking (CITE: CropBlock.isRandomlyTicking == !isMaxAge(state)): a crop is randomly
// ticking ONLY while its age is below max — a max-age crop is NOT randomly ticked (no growth roll
// is drawn for it). The IsRandomlyTicking switch in utilfuncs.go therefore gates on age < maxAge.

// cropKind enumerates the ported crop families so the server-side dispatch can branch on family
// (they share CropBlock.randomTick but differ in MAX_AGE and — for beetroot — an extra nextInt(3)
// gate). It is derived from a StateID via cropKindOf.
type cropKind int

const (
	cropNone cropKind = iota
	cropWheat
	cropCarrots
	cropPotatoes
	cropBeetroots
)

// CropMaxAge returns the crop family's getMaxAge() for a StateID (7 for wheat/carrots/potatoes,
// 3 for beetroots), or -1 when the state is not one of the ported crops. CITE:
// CropBlock.getMaxAge (bipush 7); BeetrootBlock.getMaxAge (iconst_3).
func CropMaxAge(s StateID) int {
	switch cropKindOf(s) {
	case cropWheat, cropCarrots, cropPotatoes:
		return 7
	case cropBeetroots:
		return 3
	default:
		return -1
	}
}

// CropAge returns the crop's AGE property value (0..maxAge), or -1 when not a ported crop.
// CITE: CropBlock.getAge(state) == state.getValue(getAgeProperty()).
func CropAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	switch b := StateList[s].(type) {
	case Wheat:
		return int(b.Age)
	case Carrots:
		return int(b.Age)
	case Potatoes:
		return int(b.Age)
	case Beetroots:
		return int(b.Age)
	default:
		return -1
	}
}

// CropStateForAge resolves the crop's state id for a given age, in the SAME family as the input
// state — the port of CropBlock.getStateForAge(age) == defaultBlockState().setValue(AGE, age).
// Returns ok=false if the input is not a ported crop or age is out of the family's range.
// CITE: CropBlock.getStateForAge.
func CropStateForAge(s StateID, age int) (StateID, bool) {
	if age < 0 {
		return 0, false
	}
	switch StateList[s].(type) {
	case Wheat:
		if age > 7 {
			return 0, false
		}
		sid, ok := ToStateID[Wheat{Age: Integer(age)}]
		return sid, ok
	case Carrots:
		if age > 7 {
			return 0, false
		}
		sid, ok := ToStateID[Carrots{Age: Integer(age)}]
		return sid, ok
	case Potatoes:
		if age > 7 {
			return 0, false
		}
		sid, ok := ToStateID[Potatoes{Age: Integer(age)}]
		return sid, ok
	case Beetroots:
		if age > 3 {
			return 0, false
		}
		sid, ok := ToStateID[Beetroots{Age: Integer(age)}]
		return sid, ok
	default:
		return 0, false
	}
}

// IsCrop reports whether a state id is any ported crop (wheat/carrots/potatoes/beetroots).
func IsCrop(s StateID) bool { return cropKindOf(s) != cropNone }

// IsBeetroots reports whether a state id is beetroots (the family with the extra nextInt(3) gate).
// CITE: BeetrootBlock.randomTick.
func IsBeetroots(s StateID) bool { return cropKindOf(s) == cropBeetroots }

// SameCropBlock reports whether two crop states are the SAME crop block family (ignoring AGE) —
// the predicate behind CropBlock.getGrowthSpeed's `getBlockState(neighbor).is(block)` same-crop
// neighbor checks (`block` is the crop's Block, so the compare ignores the AGE state value).
// CITE: CropBlock.getGrowthSpeed (BlockState.is(Block)).
func SameCropBlock(a, b StateID) bool {
	ka := cropKindOf(a)
	return ka != cropNone && ka == cropKindOf(b)
}

// cropKindOf classifies a StateID into its crop family.
func cropKindOf(s StateID) cropKind {
	if int(s) < 0 || int(s) >= len(StateList) {
		return cropNone
	}
	switch StateList[s].(type) {
	case Wheat:
		return cropWheat
	case Carrots:
		return cropCarrots
	case Potatoes:
		return cropPotatoes
	case Beetroots:
		return cropBeetroots
	default:
		return cropNone
	}
}

// IsFarmland reports whether a state id is farmland. CITE: FarmlandBlock.
func IsFarmland(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Farmland)
	return ok
}

// FarmlandMoisture returns the MOISTURE property (0..7) of a farmland state, or -1 if not
// farmland. CITE: FarmlandBlock.MOISTURE (BlockStateProperties.MOISTURE, IntegerProperty 0..7).
func FarmlandMoisture(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if f, ok := StateList[s].(Farmland); ok {
		return int(f.Moisture)
	}
	return -1
}

// FarmlandState resolves the farmland state id for a given MOISTURE (0..7). CITE:
// FarmlandBlock state.setValue(MOISTURE, m).
func FarmlandState(moisture int) (StateID, bool) {
	if moisture < 0 || moisture > 7 {
		return 0, false
	}
	sid, ok := ToStateID[Farmland{Moisture: Integer(moisture)}]
	return sid, ok
}

// GrowsCrops is BlockState.is(BlockTags.GROWS_CROPS) — the tag CropBlock.getGrowthSpeed consults
// for a cell BELOW-and-around the crop to award the on-farmland growth bonus. Tag closure
// (26.2 datagen grows_crops.json): { farmland }. CITE: BlockTags.GROWS_CROPS.
func GrowsCrops(s StateID) bool { return IsFarmland(s) }

// MaintainsFarmland is BlockState.is(BlockTags.MAINTAINS_FARMLAND) — the tag
// FarmlandBlock.shouldMaintainFarmland consults for the block ABOVE farmland: if present, dry
// farmland is NOT turned to dirt (a crop/stem/fence-gate keeps the farmland alive). Tag closure
// (26.2 datagen maintains_farmland.json): pumpkin_stem, attached_pumpkin_stem, melon_stem,
// attached_melon_stem, beetroots, carrots, potatoes, torchflower_crop, torchflower, pitcher_crop,
// wheat, moving_piston, + #fence_gates. CITE: BlockTags.MAINTAINS_FARMLAND (datagen tag closure).
func MaintainsFarmland(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case PumpkinStem, AttachedPumpkinStem, MelonStem, AttachedMelonStem,
		Beetroots, Carrots, Potatoes,
		TorchflowerCrop, Torchflower, PitcherCrop,
		Wheat, MovingPiston,
		// #minecraft:fence_gates
		AcaciaFenceGate, BirchFenceGate, DarkOakFenceGate, PaleOakFenceGate,
		JungleFenceGate, OakFenceGate, SpruceFenceGate, CrimsonFenceGate,
		WarpedFenceGate, MangroveFenceGate, BambooFenceGate, CherryFenceGate:
		return true
	default:
		return false
	}
}

// IsWaterFluid is FluidState.is(FluidTags.WATER) for a block state — true for a water block (any
// level, source or flowing) AND for a WATERLOGGED block (whose getFluidState() is water). Tag
// closure (#water == { water, flowing_water }); Sulfur's Water carries water at every level, and a
// waterlogged block's fluid state is water. Backs FarmlandBlock.isNearWater. CITE:
// FluidTags.WATER; FarmlandBlock.isNearWater (getFluidState(p).is(FluidTags.WATER)).
func IsWaterFluid(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	b := StateList[s]
	if _, ok := b.(Water); ok {
		return true
	}
	return isWaterlogged(b)
}
