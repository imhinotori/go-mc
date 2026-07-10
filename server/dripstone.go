package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dripstone.go -- the POINTED DRIPSTONE random-tick GROWTH-CORE handler, ported 1:1 from the
// unobfuscated 26.2 jar (PointedDripstoneBlock.randomTick -> SpeleothemBlock.randomTick). Wired into
// the random-tick driver's dispatchRandomTick (random_tick.go).
//
// RNG DRAW ORDER (must match the jar exactly): PointedDripstoneBlock.randomTick draws ONE nextFloat
// for maybeTransferFluid FIRST (the fluid-transfer body is DEFERRED, but the draw is kept for stream
// fidelity), then delegates to SpeleothemBlock.randomTick which draws a SECOND nextFloat for the
// growth gate (< 0.011377778f). Two nextFloat draws per sampled pointed dripstone. CITE:
// PointedDripstoneBlock.randomTick; SpeleothemBlock.randomTick.
//
// DEFERRED (cited): maybeTransferFluid (dripstone-water/cauldron routing) and
// growStalactiteOrStalagmiteIfPossible's tip traversal + canTipGrow water gate + nextBoolean-gated
// grow -- the multi-block speleothem-tip + fluid subsystem is a follow-up. The load-bearing growth
// GATE (two nextFloat draws + isStalactiteStartPos + canGrow dripstone_block-above) IS ported. CITE:
// PointedDripstoneBlock.maybeTransferFluid; SpeleothemBlock.growStalactiteOrStalagmiteIfPossible.

func (t *TickLoop) dripstoneRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsPointedDripstone(state) {
		return
	}
	// PointedDripstoneBlock.randomTick: maybeTransferFluid(state, level, pos, random.nextFloat()).
	// The nextFloat is drawn UNCONDITIONALLY and FIRST, before the growth core. The fluid transfer
	// itself (dripstone-water drip into a cauldron below / honey/lava routing) is DEFERRED -- no
	// cauldron-fluid subsystem in v1 -- but the DRAW must still happen so the stream order matches the
	// jar. CITE: PointedDripstoneBlock.randomTick (maybeTransferFluid(..., nextFloat())).
	_ = r.levelRandom.NextFloat()
	t.speleothemRandomTick(r, state, pos)
}

func (t *TickLoop) speleothemRandomTick(r *region, state block.StateID, pos pk.Position) {
	// SpeleothemBlock.randomTick: if (nextFloat() < 0.011377778f && isStalactiteStartPos(state, level,
	// pos)) growStalactiteOrStalagmiteIfPossible(state, level, pos, random). The nextFloat is the
	// SECOND draw of the pointed-dripstone tick and is drawn UNCONDITIONALLY (before the position
	// check). CITE: SpeleothemBlock.randomTick.
	roll := r.levelRandom.NextFloat()
	if roll >= block.DripstoneGrowthProbability {
		return
	}
	if !t.dripstoneIsStalactiteStartPos(state, pos) {
		return
	}
	// growStalactiteOrStalagmiteIfPossible(state, level, pos, random): canGrow (dripstone_block
	// directly above) -> findTip traversal -> isFreeHangingStalactite / canTipGrow (the dripstone-
	// water-above gate) -> nextBoolean() -> grow(DOWN) or growStalagmiteBelow. The canGrow gate is
	// ported here (the load-bearing "is there a dripstone_block above to grow from" check); the tip
	// traversal + the dripstone-water/cauldron canTipGrow gate + the nextBoolean-gated placement are a
	// CITED DEFERRAL (the multi-block speleothem-tip + fluid subsystem is not yet wired). Because the
	// grow body is deferred, the nextBoolean it draws is NOT drawn here -- matching vanilla when
	// findTip returns null or canTipGrow fails (the common case), and the two nextFloat draws above
	// keep the stream faithful for every sampled dripstone. CITE:
	// SpeleothemBlock.growStalactiteOrStalagmiteIfPossible / canGrow / canTipGrow / grow.
	if !t.dripstoneCanGrow(pos) {
		return
	}
	// DEFERRED: growStalactiteOrStalagmiteIfPossible tip traversal + fluid gate + nextBoolean grow.
}

func (t *TickLoop) dripstoneIsStalactiteStartPos(state block.StateID, pos pk.Position) bool {
	// isStalactiteStartPos(state, level, pos): isStalactite(state) && !getBlockState(pos.above())
	// .is(state.getBlock()). isStalactite == a speleothem whose vertical_direction == DOWN. The
	// same-block check reduces to IsPointedDripstone(above) because state is always pointed dripstone.
	// CITE: SpeleothemBlock.isStalactiteStartPos; isStalactite; isSpeleothemWithDirection.
	if !block.IsStalactite(state) {
		return false
	}
	as, ok := t.world().GetBlock(above(pos), dimMinY)
	if !ok {
		return true
	}
	return !block.IsPointedDripstone(as)
}

func (t *TickLoop) dripstoneCanGrow(pos pk.Position) bool {
	// canGrow(level, pos): getBlockState(pos.above()).is(blockToGrowOn.getBlock()). For pointed
	// dripstone blockToGrowOn == DRIPSTONE_BLOCK. CITE: SpeleothemBlock.canGrow; PointedDripstoneBlock
	// ctor (DRIPSTONE_BLOCK).
	as, ok := t.world().GetBlock(above(pos), dimMinY)
	if !ok {
		return false
	}
	return block.IsDripstoneBlock(as)
}
