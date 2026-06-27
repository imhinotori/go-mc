package block

func IsAir(s StateID) bool {
	return IsAirBlock(StateList[s])
}

func IsAirBlock(b Block) bool {
	switch b.(type) {
	case Air, CaveAir, VoidAir:
		return true
	default:
		return false
	}
}

// IsFluid reports whether a state id is a fluid (water or lava) — i.e. its vanilla
// getFluidState() is non-empty. This is the predicate behind the chunk section's
// fluidCount short (LevelChunkSection.nonEmptyFluidCount): the client uses that count to
// decide whether a section has fluid to TICK/simulate. A water/lava block at ANY level
// (source or flowing) counts. Used by the section encoder so generated water is reported
// as fluid to the client (without it the client treats the section as fluid-free and a
// player will not float in generated/aquifer water until a block update wakes the cell).
func IsFluid(s StateID) bool {
	return IsFluidBlock(StateList[s])
}

// IsFluidBlock is the Block-typed form of IsFluid (water or lava, any level).
func IsFluidBlock(b Block) bool {
	switch b.(type) {
	case Water, Lava:
		return true
	default:
		return false
	}
}

// IsVegetation reports whether a state id is a SINGLE-block support-needing vegetation feature
// whose vanilla canSurvive is the BASE VegetationBlock.canSurvive — i.e. it survives iff the
// block directly BELOW it is in #minecraft:supports_vegetation (the IsVegetationGround set). This
// is the block family that must break the instant its ground support is removed (the user-reported
// "rompe un bloque con flores arriba, las flores no se rompen"). The set is jar-confirmed against
// temp/cache/26.2-inner.jar: each Go type below maps to a concrete block whose Java class extends
// VegetationBlock and does NOT override canSurvive/mayPlaceOn (so it inherits
// VegetationBlock.canSurvive == belowState.is(BlockTags.SUPPORTS_VEGETATION)).
//
// IN SCOPE (base VegetationBlock.canSurvive, verified `javap -p` shows no canSurvive/mayPlaceOn override):
//   - FlowerBlock family (and EyeblossomBlock extends FlowerBlock, no override): Dandelion,
//     GoldenDandelion, Torchflower, Poppy, BlueOrchid, Allium, AzureBluet, RedTulip, OrangeTulip,
//     WhiteTulip, PinkTulip, OxeyeDaisy, Cornflower, WitherRose, LilyOfTheValley, OpenEyeblossom,
//     ClosedEyeblossom.
//   - TallGrassBlock: ShortGrass, Fern.
//   - SaplingBlock (base; NOT MangrovePropaguleBlock which overrides both): OakSapling,
//     SpruceSapling, BirchSapling, JungleSapling, AcaciaSapling, CherrySapling, DarkOakSapling,
//     PaleOakSapling.
//   - BushBlock: Bush. FireflyBushBlock: FireflyBush.
//
// DELIBERATELY EXCLUDED (different ground predicate — a FOLLOW-UP, not this vegetation pass):
//   DeadBush/ShortDryGrass/TallDryGrass (DryVegetationBlock overrides mayPlaceOn -> #dry_vegetation_
//   may_place_on), LeafLitter/PinkPetals/Wildflowers (FlowerBedBlock/LeafLitterBlock override),
//   MangrovePropagule (override), CactusFlower (override), Seagrass/TallSeagrass/LilyPad,
//   mushrooms, crops, nether vegetation. The 2-TALL plants (DoublePlantBlock) are handled by the
//   IsDoublePlant/DoublePlantLowerHalf predicates below, since their canSurvive is half-dependent.
//
// CITE: VegetationBlock.canSurvive / VegetationBlock.mayPlaceOn (BlockTags.SUPPORTS_VEGETATION);
// per-block `javap -p` confirming no override.
func IsVegetation(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	// FlowerBlock family (single-cell flowers + EyeblossomBlock subclass).
	case Dandelion, GoldenDandelion, Torchflower, Poppy, BlueOrchid, Allium, AzureBluet,
		RedTulip, OrangeTulip, WhiteTulip, PinkTulip, OxeyeDaisy, Cornflower, WitherRose,
		LilyOfTheValley, OpenEyeblossom, ClosedEyeblossom,
		// TallGrassBlock (single-cell grass/fern).
		ShortGrass, Fern,
		// SaplingBlock (base saplings; MangrovePropagule excluded).
		OakSapling, SpruceSapling, BirchSapling, JungleSapling, AcaciaSapling,
		CherrySapling, DarkOakSapling, PaleOakSapling,
		// BushBlock / FireflyBushBlock.
		Bush, FireflyBush:
		return true
	default:
		return false
	}
}

// IsDoublePlant reports whether a state id is a 2-tall DoublePlantBlock vegetation (sunflower,
// lilac, rose_bush, peony, tall_grass, large_fern). These need bespoke survival handling because
// DoublePlantBlock.canSurvive is HALF-dependent (jar-verified): the LOWER half delegates to
// VegetationBlock.canSurvive (belowState in #supports_vegetation), while the UPPER half survives
// iff the block BELOW it is the SAME DoublePlantBlock in its LOWER half. So breaking the ground
// destroys the lower half, whose destroy then makes the upper half lose its support and cascade.
// CITE: DoublePlantBlock.canSurvive (HALF==UPPER ? belowIsSameLowerHalf : super.canSurvive).
func IsDoublePlant(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Sunflower, Lilac, RoseBush, Peony, TallGrass, LargeFern:
		return true
	default:
		return false
	}
}

// DoublePlantLowerHalf reports whether a DoublePlantBlock state is its LOWER half (HALF==LOWER).
// For a non-double-plant state it returns false. CITE: DoublePlantBlock HALF EnumProperty
// (DoubleBlockHalf.LOWER / .UPPER); the LOWER half is the one that sits on the ground and so is
// the one whose canSurvive consults IsVegetationGround(below).
func DoublePlantLowerHalf(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case Sunflower:
		return b.Half == DoubleBlockHalfLower
	case Lilac:
		return b.Half == DoubleBlockHalfLower
	case RoseBush:
		return b.Half == DoubleBlockHalfLower
	case Peony:
		return b.Half == DoubleBlockHalfLower
	case TallGrass:
		return b.Half == DoubleBlockHalfLower
	case LargeFern:
		return b.Half == DoubleBlockHalfLower
	default:
		return false
	}
}

// SameDoublePlant reports whether two double-plant states are the SAME DoublePlantBlock kind
// (ignoring the HALF property) — the predicate behind DoublePlantBlock.canSurvive's UPPER-half
// check `belowState.is(this)` (same block, irrespective of half). It is true only when both states
// are double plants of the identical Go type. CITE: DoublePlantBlock.canSurvive UPPER branch
// (belowState.is(this) && belowState.HALF == LOWER).
func SameDoublePlant(a, b StateID) bool {
	if !IsDoublePlant(a) || !IsDoublePlant(b) {
		return false
	}
	switch StateList[a].(type) {
	case Sunflower:
		_, ok := StateList[b].(Sunflower)
		return ok
	case Lilac:
		_, ok := StateList[b].(Lilac)
		return ok
	case RoseBush:
		_, ok := StateList[b].(RoseBush)
		return ok
	case Peony:
		_, ok := StateList[b].(Peony)
		return ok
	case TallGrass:
		_, ok := StateList[b].(TallGrass)
		return ok
	case LargeFern:
		_, ok := StateList[b].(LargeFern)
		return ok
	default:
		return false
	}
}

// IsVegetationGround reports whether a state id is a block a vegetation feature (sapling/tree,
// grass, flowers) may sit ON — the vanilla `#minecraft:substrate_overworld` tag (the ground
// half of `SUPPORTS_VEGETATION`) plus farmland. substrate_overworld nests
// #dirt {dirt, coarse_dirt, rooted_dirt} + #grass_blocks {grass_block, podzol, mycelium} +
// #moss_blocks {moss_block, pale_moss_block} + #mud {mud, muddy_mangrove_roots}. Crucially this
// EXCLUDES logs and leaves, so the worldgen tree `would_survive` filter using this correctly
// rejects a tree origin that landed on another tree's log/leaf column — the fix for trees
// generating on top of trees. CITE: BlockTags.substrate_overworld (extracted tag JSON) +
// VegetationBlock.mayPlaceOn (state.is(#dirt) style ground check) + Blocks.FARMLAND.
func IsVegetationGround(s StateID) bool {
	switch StateList[s].(type) {
	case Dirt, CoarseDirt, RootedDirt, // #dirt
		GrassBlock, Podzol, Mycelium, // #grass_blocks
		MossBlock, PaleMossBlock, // #moss_blocks
		Mud, MuddyMangroveRoots, // #mud
		Farmland: // SUPPORTS_VEGETATION adds farmland
		return true
	default:
		return false
	}
}
