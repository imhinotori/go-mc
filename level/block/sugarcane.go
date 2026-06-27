package block

// sugarcane.go — the support predicates for SugarCaneBlock.canSurvive, ported 1:1 from the
// unobfuscated 26.2 jar (net.minecraft.world.level.block.SugarCaneBlock.canSurvive). They back
// the SUB-BLOCKTICK sugar-cane consumer in the server package. The tag closures are resolved
// from the 26.2 datagen (temp/cache/26.2-datagen) so each predicate is a literal membership
// test over the exact vanilla tag.
//
// CITE: SugarCaneBlock.canSurvive (BlockTags.SUPPORTS_SUGAR_CANE,
// BlockTags.SUPPORTS_SUGAR_CANE_ADJACENTLY, FluidTags.SUPPORTS_SUGAR_CANE_ADJACENTLY).

// IsSugarCane reports whether a state id is sugar cane (any age). CITE: SugarCaneBlock.
func IsSugarCane(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(SugarCane)
	return ok
}

// SugarCaneAge returns the AGE property (0..15) of a sugar-cane state, or -1 if not sugar cane.
// CITE: SugarCaneBlock.AGE (IntegerProperty 0..15).
func SugarCaneAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	if sc, ok := StateList[s].(SugarCane); ok {
		return int(sc.Age)
	}
	return -1
}

// SugarCaneState resolves the sugar-cane state id for a given AGE (0..15). CITE:
// SugarCaneBlock.defaultBlockState().setValue(AGE, age).
func SugarCaneState(age int) (StateID, bool) {
	sid, ok := ToStateID[SugarCane{Age: Integer(age)}]
	return sid, ok
}

// IsSupportsSugarCane is BlockState.is(BlockTags.SUPPORTS_SUGAR_CANE): the set of blocks sugar
// cane may stand ON. Tag closure (26.2 datagen supports_sugar_cane.json ->
// #substrate_overworld + #sand):
//
//	#dirt          : dirt, coarse_dirt, rooted_dirt
//	#mud           : mud, muddy_mangrove_roots
//	#moss_blocks   : moss_block, pale_moss_block
//	#grass_blocks  : grass_block, podzol, mycelium
//	#sand          : sand, red_sand, suspicious_sand
//
// CITE: BlockTags.SUPPORTS_SUGAR_CANE (datagen tag closure).
func IsSupportsSugarCane(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Dirt, CoarseDirt, RootedDirt, // #dirt
		Mud, MuddyMangroveRoots, // #mud
		MossBlock, PaleMossBlock, // #moss_blocks
		GrassBlock, Podzol, Mycelium, // #grass_blocks
		Sand, RedSand, SuspiciousSand: // #sand
		return true
	default:
		return false
	}
}

// IsSupportsSugarCaneAdjacentlyBlock is BlockState.is(BlockTags.SUPPORTS_SUGAR_CANE_ADJACENTLY):
// the BLOCK set that, when adjacent (horizontally) to the cell below sugar cane, lets it survive.
// Tag closure (26.2 datagen): { frosted_ice }. CITE: BlockTags.SUPPORTS_SUGAR_CANE_ADJACENTLY.
func IsSupportsSugarCaneAdjacentlyBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case FrostedIce:
		return true
	default:
		return false
	}
}

// IsSupportsSugarCaneAdjacentlyFluid is FluidState.is(FluidTags.SUPPORTS_SUGAR_CANE_ADJACENTLY):
// the FLUID set (any water, source or flowing) that supports sugar cane when adjacent. Tag
// closure (26.2 datagen): #water == { water, flowing_water } — i.e. ANY water fluid level. In
// Sulfur the Water block carries the water fluid state at every level, so a Water state id is a
// member. Lava is NOT in the tag. CITE: FluidTags.SUPPORTS_SUGAR_CANE_ADJACENTLY (#water).
func IsSupportsSugarCaneAdjacentlyFluid(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Water:
		return true
	default:
		return false
	}
}
