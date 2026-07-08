package block

import "reflect"

func IsAir(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false // out-of-range id (corrupt/garbled section): not air, never panic
	}
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

// IsRandomlyTicking is the 1:1 port of
// net.minecraft.world.level.block.state.BlockBehaviour$BlockStateBase.isRandomlyTicking() — the
// per-state boolean flag the random-tick driver gates on before calling randomTick. In vanilla the
// flag is baked into each BlockState at construction from the block's Properties (a block that calls
// `.randomTicks()` in its properties builder, or whose BlockBehaviour overrides isRandomlyTicking to
// return true, e.g. GrowingPlant/Crop/Sapling/Leaves/Grass/Farmland/SugarCane). This is the
// per-block-family membership test that reproduces that flag.
//
// SCOPE (v1): the only ported block whose randomTick is wired is SugarCaneBlock — its properties call
// randomTicks() so isRandomlyTicking()==true for every sugar-cane state. Other randomly-ticking
// families (crops, saplings, leaves, grass spread, farmland moisture, etc.) are FOLLOW-UPS: add each
// Go block type to the switch below AND its randomTick handler to the server-side dispatch as it is
// ported. Returning false for a not-yet-ported family is correct-by-omission (the driver simply does
// not tick it yet — no wrong behavior, only a missing one), never a baked-away value.
//
// CITE: BlockBehaviour$BlockStateBase.isRandomlyTicking() (returns the isRandomlyTicking flag);
// SugarCaneBlock properties (.randomTicks()).
func IsRandomlyTicking(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case SugarCane:
		return true // SugarCaneBlock: .randomTicks() -> isRandomlyTicking()==true (any AGE)
	case Wheat, Carrots, Potatoes, Beetroots:
		// CropBlock/BeetrootBlock override isRandomlyTicking() to return !isMaxAge(state) — a crop is
		// randomly ticking ONLY while its age is below the family's MAX_AGE. So a max-age crop is NOT
		// sampled for growth (no roll drawn), matching CropBlock.isRandomlyTicking. CITE:
		// CropBlock.isRandomlyTicking (== !isMaxAge); BeetrootBlock inherits it (MAX_AGE 3).
		return CropAge(s) < CropMaxAge(s)
	case Farmland:
		// FarmlandBlock uses the Properties.randomTicks() flag -> isRandomlyTicking()==true for every
		// MOISTURE (the moisture drop / turnToDirt logic runs each random tick). CITE: FarmlandBlock
		// randomTick + BlockBehaviour$Properties.randomTicks().
		return true
	case OakSapling, SpruceSapling, BirchSapling, JungleSapling,
		AcaciaSapling, CherrySapling, DarkOakSapling, PaleOakSapling:
		// SaplingBlock: Properties.randomTicks() -> isRandomlyTicking()==true for every STAGE (the
		// advanceTree growth roll runs each random tick until the tree grows). CITE: SaplingBlock
		// properties (.randomTicks()); SaplingBlock.randomTick.
		return true
	case OakLeaves, SpruceLeaves, BirchLeaves, JungleLeaves, AcaciaLeaves,
		CherryLeaves, DarkOakLeaves, PaleOakLeaves, MangroveLeaves,
		AzaleaLeaves, FloweringAzaleaLeaves:
		// LeavesBlock overrides isRandomlyTicking() to return DISTANCE==7 && !PERSISTENT — leaves are
		// randomly ticked ONLY when at the max decay distance and non-persistent (so only decaying
		// leaves are sampled; a well-anchored or persistent leaf draws no roll). CITE:
		// LeavesBlock.isRandomlyTicking.
		return LeavesDecaying(s)
	case GrassBlock, Mycelium:
		// SpreadingSnowyBlock (GrassBlock/MyceliumBlock): Properties.randomTicks() ->
		// isRandomlyTicking()==true for every state (the die/spread logic runs each random tick).
		// CITE: GrassBlock/MyceliumBlock properties (.randomTicks()); SpreadingSnowyBlock.randomTick.
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
//
//	DeadBush/ShortDryGrass/TallDryGrass (DryVegetationBlock overrides mayPlaceOn -> #dry_vegetation_
//	may_place_on), LeafLitter/PinkPetals/Wildflowers (FlowerBedBlock/LeafLitterBlock override),
//	MangrovePropagule (override), CactusFlower (override), Seagrass/TallSeagrass/LilyPad,
//	mushrooms, crops, nether vegetation. The 2-TALL plants (DoublePlantBlock) are handled by the
//	IsDoublePlant/DoublePlantLowerHalf predicates below, since their canSurvive is half-dependent.
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

// DoublePlantWithHalf returns the SAME DoublePlantBlock state as s but with its HALF property set
// to LOWER (upper==false) or UPPER (upper==true). It is the Go realization of
// state.setValue(DoublePlantBlock.HALF, DoubleBlockHalf.LOWER/UPPER) -- the exact two states
// DoublePlantBlock.placeAt writes (LOWER at pos, UPPER at pos.above). Each double-plant block
// carries ONLY the Half property, so reconstructing the struct with the chosen half yields the
// target state deterministically (matching SameDoublePlant/DoublePlantLowerHalf above). For a
// non-double-plant s it returns (s, false) -- the caller must gate on IsDoublePlant first.
// CITE: DoublePlantBlock.placeAt (setValue(HALF, LOWER) at pos, setValue(HALF, UPPER) at pos.above);
// DoublePlantBlock HALF EnumProperty (DoubleBlockHalf.LOWER/.UPPER).
func DoublePlantWithHalf(s StateID, upper bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	half := DoubleBlockHalfLower
	if upper {
		half = DoubleBlockHalfUpper
	}
	var out Block
	switch StateList[s].(type) {
	case Sunflower:
		out = Sunflower{Half: half}
	case Lilac:
		out = Lilac{Half: half}
	case RoseBush:
		out = RoseBush{Half: half}
	case Peony:
		out = Peony{Half: half}
	case TallGrass:
		out = TallGrass{Half: half}
	case LargeFern:
		out = LargeFern{Half: half}
	default:
		return s, false
	}
	id, ok := ToStateID[out]
	return id, ok
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

// StateExplosionResistance is the 1:1 port of
// net.minecraft.world.level.ExplosionDamageCalculator.getBlockExplosionResistance's non-empty
// branch: max(block.getExplosionResistance(), fluidState.getExplosionResistance()). The block
// half reads the generated ExplosionResistance table (keyed by Block.ID(), constant per block).
// The fluid half is the state's fluid: air/most blocks carry EmptyFluid (0.0), water/lava carry
// their fluid (100.0), and a WATERLOGGED block carries water (100.0). The empty (air + no fluid)
// case never reaches here — the collection loop only calls this on a present resistance.
//
// CITE: ExplosionDamageCalculator.getBlockExplosionResistance (Optional.of(max(block, fluid)));
// Block.getExplosionResistance (the explosionResistance field, extracted into ExplosionResistance);
// WaterFluid/LavaFluid.getExplosionResistance == 100.0f; EmptyFluid == 0.0f.
func StateExplosionResistance(s StateID) float32 {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0
	}
	b := StateList[s]
	blockR := ExplosionResistance[b.ID()] // 0 if unmapped (never for a registered block)
	fluidR := stateFluidExplosionResistance(b)
	if fluidR > blockR {
		return fluidR
	}
	return blockR
}

// stateFluidExplosionResistance is the fluid half of getBlockExplosionResistance: the state's
// FluidState.getExplosionResistance(). Water/lava blocks ARE their fluid (100.0); a block with a
// waterlogged=true property carries water (100.0); everything else carries EmptyFluid (0.0). The
// waterlogged read is reflective because the block structs each declare their own Waterlogged
// field — matching how BlockBehaviour reads the WATERLOGGED property generically.
//
//	[VERIFIED javap: FluidState.getExplosionResistance -> Fluid.getExplosionResistance;
//	 WaterFluid/LavaFluid == 100.0f, EmptyFluid == 0.0f.]
func stateFluidExplosionResistance(b Block) float32 {
	if IsFluidBlock(b) {
		return 100.0 // water/lava block: the fluid IS the block (WaterFluid/LavaFluid == 100.0f)
	}
	if isWaterlogged(b) {
		return 100.0 // waterlogged block: getFluidState() is water (100.0f)
	}
	return 0.0 // EmptyFluid
}

// isWaterlogged reports whether a block state has a waterlogged=true property (the generated
// block structs declare a `Waterlogged Boolean` field). Read reflectively so every waterloggable
// block (stairs/slabs/fences/…) is covered without enumerating them. A block with no Waterlogged
// field returns false.
func isWaterlogged(b Block) bool {
	v := reflect.ValueOf(b)
	if v.Kind() != reflect.Struct {
		return false
	}
	f := v.FieldByName("Waterlogged")
	if !f.IsValid() || f.Kind() != reflect.Bool {
		return false
	}
	return f.Bool()
}

// IsWaterloggedState reports whether a block state id resolves to a waterlogged=true block state — the
// exported StateID front-end of isWaterlogged. Used by callers outside this package (e.g. the conduit's
// Level.isWaterAt port, which must count a waterlogged block's cell as water because
// SimpleWaterloggedBlock.getFluidState returns Fluids.WATER). An out-of-range id returns false.
func IsWaterloggedState(id StateID) bool {
	if id < 0 || int(id) >= len(StateList) {
		return false
	}
	return isWaterlogged(StateList[id])
}
