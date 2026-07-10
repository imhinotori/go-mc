package block

// dripstone.go -- block-family predicates and state accessors for the POINTED DRIPSTONE random-tick
// growth handler (server/dripstone.go), ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.PointedDripstoneBlock / SpeleothemBlock). A small new file keeps
// the generated blocks.go untouched. CITE per function.

// DripstoneGrowthProbability is SpeleothemBlock.GROWTH_PROBABILITY_PER_RANDOM_TICK (0.011377778f) --
// the per-random-tick nextFloat gate for a stalactite/stalagmite growth attempt. CITE:
// SpeleothemBlock.randomTick (ldc_w 0.011377778f); SpeleothemBlock.GROWTH_PROBABILITY_PER_RANDOM_TICK.
const DripstoneGrowthProbability = 0.011377778

// IsPointedDripstone reports Blocks.POINTED_DRIPSTONE. CITE: PointedDripstoneBlock.
func IsPointedDripstone(s StateID) bool { return isType[PointedDripstone](s) }

// dripstoneOf resolves s to a PointedDripstone struct value. CITE: instanceof PointedDripstoneBlock.
func dripstoneOf(s StateID) (PointedDripstone, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return PointedDripstone{}, false
	}
	d, ok := StateList[s].(PointedDripstone)
	return d, ok
}

// IsStalactite is SpeleothemBlock.isStalactite(state) == isSpeleothemWithDirection(state, DOWN): a
// SPELEOTHEMS-tagged block whose TIP_DIRECTION (vertical_direction) == DOWN (a hanging stalactite,
// as opposed to an upward stalagmite). For v1 the SPELEOTHEMS membership is scoped to
// PointedDripstone (the growth handler is pointed-dripstone-only). CITE: SpeleothemBlock.isStalactite;
// isSpeleothemWithDirection (is(#SPELEOTHEMS) && getValue(TIP_DIRECTION) == dir); BlockTags.SPELEOTHEMS.
func IsStalactite(s StateID) bool {
	d, ok := dripstoneOf(s)
	if !ok {
		return false
	}
	return d.VerticalDirection == Down
}

// PointedDripstoneBlockOf returns the concrete Block behind s if it is a pointed dripstone, so a
// caller can compare `above.is(state.getBlock())` (the same-block check in isStalactiteStartPos:
// !getBlockState(pos.above()).is(state.getBlock())). Since s is always PointedDripstone here, the
// same-block test reduces to IsPointedDripstone(above). CITE: SpeleothemBlock.isStalactiteStartPos.
func PointedDripstoneBlockOf(s StateID) (Block, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return nil, false
	}
	if _, ok := StateList[s].(PointedDripstone); ok {
		return PointedDripstone{}, true
	}
	return nil, false
}

// IsDripstoneBlock reports Blocks.DRIPSTONE_BLOCK -- the block PointedDripstoneBlock.canGrow requires
// directly ABOVE the stalactite start for growth (blockToGrowOn == DRIPSTONE_BLOCK). CITE:
// SpeleothemBlock.canGrow (getBlockState(pos.above()).is(blockToGrowOn.getBlock()));
// PointedDripstoneBlock ctor (DRIPSTONE_BLOCK.defaultBlockState()).
func IsDripstoneBlock(s StateID) bool { return isType[DripstoneBlock](s) }
