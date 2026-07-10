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

// IsStalagmite is SpeleothemBlock.isStalagmite(state) == isSpeleothemWithDirection(state, UP): a
// SPELEOTHEMS-tagged block whose TIP_DIRECTION == UP (an upward stalagmite). Scoped to
// PointedDripstone for the pointed-dripstone-only growth handler. CITE: SpeleothemBlock.isStalagmite.
func IsStalagmite(s StateID) bool {
	d, ok := dripstoneOf(s)
	if !ok {
		return false
	}
	return d.VerticalDirection == Up
}

// PointedDripstoneVerticalDirection returns the TIP_DIRECTION (vertical_direction) of a pointed
// dripstone state, or (Down, false) if s is not pointed dripstone. CITE: SpeleothemBlock TIP_DIRECTION
// (BlockStateProperties.VERTICAL_DIRECTION).
func PointedDripstoneVerticalDirection(s StateID) (Direction, bool) {
	d, ok := dripstoneOf(s)
	if !ok {
		return Down, false
	}
	return d.VerticalDirection, true
}

// PointedDripstoneThickness returns the THICKNESS (speleothem_thickness) of a pointed dripstone
// state, or (SpeleothemThicknessTipMerge, false) if s is not pointed dripstone. CITE: SpeleothemBlock
// THICKNESS (BlockStateProperties.SPELEOTHEM_THICKNESS).
func PointedDripstoneThickness(s StateID) (SpeleothemThickness, bool) {
	d, ok := dripstoneOf(s)
	if !ok {
		return SpeleothemThicknessTipMerge, false
	}
	return d.Thickness, true
}

// IsPointedDripstoneTip is SpeleothemBlock.isTip(state, allowMerged): a SPELEOTHEMS-tagged block whose
// THICKNESS is TIP, or (when allowMerged) TIP_MERGE. Scoped to pointed dripstone. CITE:
// SpeleothemBlock.isTip.
func IsPointedDripstoneTip(s StateID, allowMerged bool) bool {
	d, ok := dripstoneOf(s)
	if !ok {
		return false
	}
	if d.Thickness == SpeleothemThicknessTip {
		return true
	}
	return allowMerged && d.Thickness == SpeleothemThicknessTipMerge
}

// IsFreeHangingStalactite is SpeleothemBlock.isFreeHangingStalactite(state): a downward stalactite
// whose THICKNESS is TIP and that is NOT waterlogged. CITE: SpeleothemBlock.isFreeHangingStalactite.
func IsFreeHangingStalactite(s StateID) bool {
	d, ok := dripstoneOf(s)
	if !ok {
		return false
	}
	return d.VerticalDirection == Down && d.Thickness == SpeleothemThicknessTip && !bool(d.Waterlogged)
}

// PointedDripstoneState builds the POINTED_DRIPSTONE state with the given TIP_DIRECTION, THICKNESS and
// WATERLOGGED -- the createSpeleothem target (defaultBlockState().setValue(...)). CITE:
// SpeleothemBlock.createSpeleothem.
func PointedDripstoneState(dir Direction, thickness SpeleothemThickness, waterlogged bool) (StateID, bool) {
	s, ok := ToStateID[PointedDripstone{
		Thickness:         thickness,
		VerticalDirection: dir,
		Waterlogged:       Boolean(waterlogged),
	}]
	return s, ok
}

// IsUnmergedPointedDripstoneTipWithDirection is SpeleothemBlock.isUnmergedTipWithDirection(state, dir):
// isTip(state, false) && getValue(TIP_DIRECTION) == dir && state.is(this). Scoped to pointed dripstone
// (state.is(this) == IsPointedDripstone). CITE: SpeleothemBlock.isUnmergedTipWithDirection.
func IsUnmergedPointedDripstoneTipWithDirection(s StateID, dir Direction) bool {
	d, ok := dripstoneOf(s)
	if !ok {
		return false
	}
	return d.Thickness == SpeleothemThicknessTip && d.VerticalDirection == dir
}

// IsPointedDripstoneWithDirection is SpeleothemBlock.isSpeleothemWithDirection(state, dir) scoped to
// POINTED_DRIPSTONE: is(#SPELEOTHEMS) AND getValue(TIP_DIRECTION) == dir. The findTip/findRootBlock
// chain predicates additionally require state.is(this) (this == POINTED_DRIPSTONE), which folds into
// the pointed-dripstone type check here. CITE: SpeleothemBlock.isSpeleothemWithDirection;
// SpeleothemBlock.findTip / findRootBlock chain lambdas.
func IsPointedDripstoneWithDirection(s StateID, dir Direction) bool {
	d, ok := dripstoneOf(s)
	if !ok {
		return false
	}
	return d.VerticalDirection == dir
}
