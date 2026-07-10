package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dripstone.go -- the POINTED DRIPSTONE random-tick handler, ported 1:1 from the unobfuscated 26.2
// jar (net.minecraft.world.level.block.PointedDripstoneBlock.randomTick -> SpeleothemBlock.randomTick
// -> growStalactiteOrStalagmiteIfPossible). Wired into the random-tick driver's dispatchRandomTick
// (random_tick.go).
//
// RNG DRAW ORDER (must match the jar EXACTLY): PointedDripstoneBlock.randomTick draws ONE nextFloat
// for maybeTransferFluid FIRST, then delegates to SpeleothemBlock.randomTick which draws a SECOND
// nextFloat for the growth gate (< 0.011377778f). If (and only if) that gate passes AND
// isStalactiteStartPos, growStalactiteOrStalagmiteIfPossible runs; when its findTip locates a free-
// hanging stalactite tip that canTipGrow, it draws ONE nextBoolean to pick stalactite-down vs
// stalagmite-below growth. So a sampled pointed dripstone draws two nextFloat unconditionally, plus at
// most one nextBoolean only when the full growth chain reaches the grow decision -- matching vanilla.
// CITE: PointedDripstoneBlock.randomTick; SpeleothemBlock.randomTick;
// SpeleothemBlock.growStalactiteOrStalagmiteIfPossible.

// dripMaxGrowthLength is SpeleothemBlock.getMaxGrowthLength() == 7 (bipush 7). CITE:
// SpeleothemBlock.getMaxGrowthLength.
const dripMaxGrowthLength = 7

// dripWaterTransferProbability / dripLavaTransferProbability are
// PointedDripstoneBlock.WATER_TRANSFER_PROBABILITY_PER_RANDOM_TICK (0.17578125f) and
// LAVA_TRANSFER_PROBABILITY_PER_RANDOM_TICK (0.05859375f) -- the per-random-tick nextFloat ceilings
// for a cauldron fluid transfer. CITE: PointedDripstoneBlock.maybeTransferFluid (ldc 0.17578125f /
// 0.05859375f).
const (
	dripWaterTransferProbability = 0.17578125
	dripLavaTransferProbability  = 0.05859375
)

// dripStalagmiteMaxSearch is SpeleothemBlock.MAX_STALAGMITE_SEARCH_RANGE_WHEN_GROWING == 10 (the
// growStalagmiteBelow scan bound, bipush 10). CITE: SpeleothemBlock.growStalagmiteBelow.
const dripStalagmiteMaxSearch = 10

// dripTransferSearchLength is
// PointedDripstoneBlock.MAX_SEARCH_LENGTH_BETWEEN_STALACTITE_TIP_AND_CAULDRON == 11 (bipush 11), the
// findTip / findFillableCauldronBelowStalactiteTip bound in maybeTransferFluid, and the findRootBlock
// bound in getFluidAboveStalactite (also bipush 11). CITE: PointedDripstoneBlock.maybeTransferFluid;
// getFluidAboveStalactite.
const dripTransferSearchLength = 11

func (t *TickLoop) dripstoneRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	if !block.IsPointedDripstone(state) {
		return
	}
	// PointedDripstoneBlock.randomTick: maybeTransferFluid(state, level, pos, random.nextFloat()); then
	// super.randomTick. The nextFloat is drawn UNCONDITIONALLY and FIRST. CITE:
	// PointedDripstoneBlock.randomTick.
	t.dripstoneMaybeTransferFluid(state, pos, r.levelRandom.NextFloat())
	t.speleothemRandomTick(r, state, pos)
}

// dripstoneMaybeTransferFluid ports PointedDripstoneBlock.maybeTransferFluid(state, level, pos, chance)
// 1:1: the water/lava probability gate, isStalactiteStartPos, getFluidAboveStalactite, the MUD->CLAY
// evaporation branch (fully portable), and the cauldron drip-fill routing. CITE:
// PointedDripstoneBlock.maybeTransferFluid.
//
// CITED STUB: the cauldron drip-fill terminus -- findFillableCauldronBelowStalactiteTip followed by
// scheduleTick(cauldronPos, cauldronBlock, 50 + dy) that lands in the cauldron block's scheduled tick
// (AbstractCauldronBlock.canReceiveStalactiteDrip / LayeredCauldronBlock.receiveStalactiteDrip) -- is
// NOT wired: the cauldron drip-fill block-entity/tick subsystem is not built. The nextFloat gate,
// start-pos check, fluid detection, and the MUD->CLAY conversion (which needs no cauldron) ARE ported;
// the terminal scheduleTick is the sole deferral. This draws NO extra RNG (maybeTransferFluid consumes
// only the nextFloat already drawn by the caller), so the stream stays faithful. CITE:
// PointedDripstoneBlock.maybeTransferFluid (findFillableCauldronBelowStalactiteTip -> scheduleTick);
// AbstractCauldronBlock.canReceiveStalactiteDrip.
func (t *TickLoop) dripstoneMaybeTransferFluid(state block.StateID, pos pk.Position, chance float32) {
	// if (chance > WATER_TRANSFER_PROBABILITY && chance > LAVA_TRANSFER_PROBABILITY) return; -- the two
	// gates short-circuit the common case before any world reads.
	if chance > float32(dripWaterTransferProbability) && chance > float32(dripLavaTransferProbability) {
		return
	}
	if !t.dripstoneIsStalactiteStartPos(state, pos) {
		return
	}
	fluid, sourcePos, ok := t.dripstoneFluidAboveStalactite(state, pos)
	if !ok {
		return
	}
	// float transferProb = (fluid == WATER) ? WATER_... : (fluid == LAVA) ? LAVA_... : return;
	var transferProb float32
	switch fluid {
	case dripFluidWater:
		transferProb = float32(dripWaterTransferProbability)
	case dripFluidLava:
		transferProb = float32(dripLavaTransferProbability)
	default:
		return
	}
	// if (chance >= transferProb) return; -- the per-fluid ceiling.
	if chance >= transferProb {
		return
	}
	// findTip(state, level, pos, 11, false); if (tip == null) return;
	if _, ok := t.dripstoneFindTip(state, pos, dripTransferSearchLength, false); !ok {
		return
	}
	// MUD->CLAY branch: if (sourceState.is(MUD) && fluid == WATER) -> setBlockAndUpdate(sourcePos, CLAY)
	// (+ pushEntitiesUp + gameEvent + levelEvent). sourcePos == FluidInfo.pos == root.above(). CITE:
	// PointedDripstoneBlock.maybeTransferFluid (MUD -> CLAY).
	if ss, sok := t.world().GetBlock(sourcePos, dimMinY); sok && block.IsMud(ss) && fluid == dripFluidWater {
		clay := block.ToStateID[block.Clay{}]
		if t.world().SetBlock(sourcePos, clay, dimMinY) {
			t.broadcastBlockUpdate(sourcePos, clay)
			t.updateNeighborsAt(sourcePos, updateShapeRecursionLimit)
		}
		return
	}
	// CITED STUB: findFillableCauldronBelowStalactiteTip(level, tip, fluid) -> scheduleTick(cauldron,
	// cauldronBlock, 50 + (tip.getY() - cauldron.getY())). The cauldron drip-fill subsystem is not
	// built, so the scheduleTick terminus is deferred. No RNG is drawn here.
}

func (t *TickLoop) speleothemRandomTick(r *region, state block.StateID, pos pk.Position) {
	// SpeleothemBlock.randomTick: if (nextFloat() < 0.011377778f && isStalactiteStartPos(state, level,
	// pos)) growStalactiteOrStalagmiteIfPossible(state, level, pos, random). The nextFloat is the SECOND
	// draw of the pointed-dripstone tick and is drawn UNCONDITIONALLY (before the position check). CITE:
	// SpeleothemBlock.randomTick.
	roll := r.levelRandom.NextFloat()
	if roll >= block.DripstoneGrowthProbability {
		return
	}
	if !t.dripstoneIsStalactiteStartPos(state, pos) {
		return
	}
	t.dripstoneGrowStalactiteOrStalagmiteIfPossible(r, state, pos)
}

// dripstoneGrowStalactiteOrStalagmiteIfPossible ports
// SpeleothemBlock.growStalactiteOrStalagmiteIfPossible(state, level, pos, random) 1:1:
//
//	if (!canGrow(level, pos)) return;
//	tip = findTip(state, level, pos, getMaxGrowthLength(), false);
//	if (tip == null) return;
//	tipState = level.getBlockState(tip);
//	if (isFreeHangingStalactite(tipState) && !canTipGrow(tipState, level, tip)) return;
//	if (random.nextBoolean()) grow(level, tip, DOWN); else growStalagmiteBelow(level, tip);
//
// canGrow here is PointedDripstoneBlock's override (dripstone_block at above(1) AND water source at
// above(2)). CITE: SpeleothemBlock.growStalactiteOrStalagmiteIfPossible; PointedDripstoneBlock.canGrow.
func (t *TickLoop) dripstoneGrowStalactiteOrStalagmiteIfPossible(r *region, state block.StateID, pos pk.Position) {
	if !t.dripstoneCanGrow(pos) {
		return
	}
	tip, ok := t.dripstoneFindTip(state, pos, dripMaxGrowthLength, false)
	if !ok {
		return
	}
	tipState, tok := t.world().GetBlock(tip, dimMinY)
	if !tok {
		return
	}
	// if (isFreeHangingStalactite(tipState) && !canTipGrow(tipState, level, tip)) return; -- note the
	// short-circuit: canTipGrow is only evaluated for a free-hanging stalactite tip.
	if block.IsFreeHangingStalactite(tipState) && !t.dripstoneCanTipGrow(tipState, tip) {
		return
	}
	// The nextBoolean is drawn ONLY when the growth chain reaches this decision -- 1:1 with vanilla.
	if r.levelRandom.NextBoolean() {
		t.dripstoneGrow(tip, block.Down)
	} else {
		t.dripstoneGrowStalagmiteBelow(tip)
	}
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

// dripstoneCanGrow ports PointedDripstoneBlock.canGrow(level, pos) 1:1: super.canGrow (dripstone_block
// directly above -- blockToGrowOn == DRIPSTONE_BLOCK) AND getFluidState(pos.above(2)) is a WATER
// SOURCE. CITE: PointedDripstoneBlock.canGrow (super.canGrow(level,pos) && fluidState.is(WATER) &&
// fluidState.isSource()); SpeleothemBlock.canGrow.
func (t *TickLoop) dripstoneCanGrow(pos pk.Position) bool {
	as, ok := t.world().GetBlock(above(pos), dimMinY)
	if !ok {
		return false
	}
	if !block.IsDripstoneBlock(as) {
		return false
	}
	// getFluidState(pos.above(2)).is(WATER) && isSource().
	f := t.dripstoneGetFluidState(pk.Position{X: pos.X, Y: pos.Y + 2, Z: pos.Z})
	return f.isWater && f.source
}

// dripstoneFindTip ports SpeleothemBlock.findTip(state, level, pos, maxLen, allowMerged) 1:1: if the
// start state is already a tip, return pos; otherwise walk in TIP_DIRECTION's axis direction (a
// stalactite TIP_DIRECTION is DOWN so it walks DOWN toward the tip) via findBlockVertical, with the
// same-block + same-direction continue predicate and the isTip stop predicate. CITE:
// SpeleothemBlock.findTip; findBlockVertical; lambda findTip 0/1.
func (t *TickLoop) dripstoneFindTip(state block.StateID, pos pk.Position, maxLen int, allowMerged bool) (pk.Position, bool) {
	if block.IsPointedDripstoneTip(state, allowMerged) {
		return pos, true
	}
	dir, ok := block.PointedDripstoneVerticalDirection(state)
	if !ok {
		return pk.Position{}, false
	}
	// findBlockVertical walks along dir.getAxisDirection() on the Y axis: Direction.get(axisDir, Y) is
	// UP for POSITIVE, DOWN for NEGATIVE. For a stalactite TIP_DIRECTION == DOWN so it walks DOWN.
	step := dripStepForDirection(dir)
	// continue predicate (BiPredicate): the scanned block still belongs to this speleothem chain, i.e.
	// state.is(this) && getValue(TIP_DIRECTION) == dir (lambda findTip 0). stop predicate (Predicate):
	// isTip(state, allowMerged) (lambda findTip 1). CITE: SpeleothemBlock.findTip lambdas.
	cont := func(s block.StateID) bool { return block.IsPointedDripstoneWithDirection(s, dir) }
	stop := func(s block.StateID) bool { return block.IsPointedDripstoneTip(s, allowMerged) }
	return t.dripstoneFindBlockVertical(pos, step, cont, stop, maxLen)
}

// dripstoneFindBlockVertical ports SpeleothemBlock.findBlockVertical(level, start, axisDir, continue,
// stop, maxLen) 1:1: from i=1 up to (exclusive) maxLen, move one cell along step, read the state; if
// stop(state) return that pos; else if outsideBuildHeight OR !continue(pos, state) return empty; else
// keep going. Returns empty when the loop exhausts. The outsideBuildHeight-OR-!continue order and the
// [1, maxLen) bound are exact. CITE: SpeleothemBlock.findBlockVertical.
func (t *TickLoop) dripstoneFindBlockVertical(start pk.Position, step int, cont, stop func(block.StateID) bool, maxLen int) (pk.Position, bool) {
	cur := start
	for i := 1; i < maxLen; i++ {
		cur = pk.Position{X: cur.X, Y: cur.Y + step, Z: cur.Z}
		s, ok := t.world().GetBlock(cur, dimMinY)
		if !ok {
			s = block.ToStateID[block.Air{}]
		}
		if stop(s) {
			return cur, true
		}
		// isOutsideBuildHeight(y) || !continue(pos, state) -> empty. Build span is [dimMinY, dimMinY+384)
		// so the inclusive max block Y is dimMinY+383 (Level.isOutsideBuildHeight).
		if cur.Y < dripMinBuildY || cur.Y > dripMaxBuildY || !cont(s) {
			return pk.Position{}, false
		}
	}
	return pk.Position{}, false
}

// dripstoneCanTipGrow ports SpeleothemBlock.canTipGrow(tipState, level, tip) 1:1: look at the cell in
// front of the tip (relative(TIP_DIRECTION)); if it has a non-empty fluid, false; if it is air, true;
// otherwise it may only grow into an unmerged tip pointing back at us. CITE: SpeleothemBlock.canTipGrow.
func (t *TickLoop) dripstoneCanTipGrow(tipState block.StateID, tip pk.Position) bool {
	dir, ok := block.PointedDripstoneVerticalDirection(tipState)
	if !ok {
		return false
	}
	next := dripRelative(tip, dir)
	ns, nok := t.world().GetBlock(next, dimMinY)
	if !nok {
		ns = block.ToStateID[block.Air{}]
	}
	// if (!getFluidState().isEmpty()) return false; -- fluid (water OR lava, incl. waterlogged) blocks.
	if !t.dripstoneGetFluidState(next).isEmpty() {
		return false
	}
	if block.IsAir(ns) {
		return true
	}
	return block.IsUnmergedPointedDripstoneTipWithDirection(ns, dripOpposite(dir))
}

// dripstoneGrow ports SpeleothemBlock.grow(level, pos, dir) 1:1: look at pos.relative(dir); if it is an
// unmerged tip pointing back at us, createMergedTips; else if it is air OR water, createSpeleothem(TIP)
// there. CITE: SpeleothemBlock.grow; createSpeleothem; createMergedTips.
func (t *TickLoop) dripstoneGrow(pos pk.Position, dir block.Direction) {
	target := dripRelative(pos, dir)
	ts, ok := t.world().GetBlock(target, dimMinY)
	if !ok {
		ts = block.ToStateID[block.Air{}]
	}
	if block.IsUnmergedPointedDripstoneTipWithDirection(ts, dripOpposite(dir)) {
		t.dripstoneCreateMergedTips(target)
		return
	}
	// isAir || is(WATER) -> createSpeleothem(TIP).
	_, isWaterBlk := waterLevelOf(ts)
	if block.IsAir(ts) || isWaterBlk {
		t.dripstoneCreateSpeleothem(target, dir, block.SpeleothemThicknessTip)
	}
}

// dripstoneCreateSpeleothem ports SpeleothemBlock.createSpeleothem(level, pos, dir, thickness) 1:1:
// setBlock(pos, POINTED_DRIPSTONE.setValue(TIP_DIRECTION, dir).setValue(THICKNESS, thickness)
// .setValue(WATERLOGGED, getFluidState(pos).is(WATER)), flag 3). Flag 3 = UPDATE_NEIGHBORS|CLIENTS;
// mirrored here as SetBlock + broadcastBlockUpdate + updateNeighborsAt (the block-layer flag-3 shape).
// CITE: SpeleothemBlock.createSpeleothem.
func (t *TickLoop) dripstoneCreateSpeleothem(pos pk.Position, dir block.Direction, thickness block.SpeleothemThickness) {
	f := t.dripstoneGetFluidState(pos)
	waterlogged := f.isWater
	ns, ok := block.PointedDripstoneState(dir, thickness, waterlogged)
	if !ok {
		return
	}
	if t.world().SetBlock(pos, ns, dimMinY) {
		t.broadcastBlockUpdate(pos, ns)
		t.updateNeighborsAt(pos, updateShapeRecursionLimit)
	}
}

// dripstoneCreateMergedTips ports SpeleothemBlock.createMergedTips(state, level, pos) 1:1: place two
// TIP_MERGE speleothems -- the DOWN-facing one at the upper cell and the UP-facing one at the lower
// cell. grow() calls createMergedTips(upperState, level, upperPos) where upperState is the block at the
// merge target and upperPos is that target. Layout: if upperState.TIP_DIRECTION == UP { upper =
// upperPos.above(); lower = upperPos } else { upper = upperPos; lower = upperPos.below() }. CITE:
// SpeleothemBlock.createMergedTips.
func (t *TickLoop) dripstoneCreateMergedTips(target pk.Position) {
	ts, ok := t.world().GetBlock(target, dimMinY)
	if !ok {
		return
	}
	dir, dok := block.PointedDripstoneVerticalDirection(ts)
	if !dok {
		return
	}
	var upper, lower pk.Position
	if dir == block.Up {
		lower = target
		upper = above(target)
	} else {
		upper = target
		lower = below(target)
	}
	t.dripstoneCreateSpeleothem(upper, block.Down, block.SpeleothemThicknessTipMerge)
	t.dripstoneCreateSpeleothem(lower, block.Up, block.SpeleothemThicknessTipMerge)
}

// dripstoneGrowStalagmiteBelow ports SpeleothemBlock.growStalagmiteBelow(level, pos) 1:1: scan DOWN up
// to MAX_STALAGMITE_SEARCH_RANGE_WHEN_GROWING (10) cells; on each cell:
//   - if its fluid is non-empty, stop (return);
//   - if it is an unmerged UP-tip that canTipGrow, grow(level, cell, UP) and return;
//   - else if isValidSpeleothemPlacement(level, cell, UP) AND !isWaterAt(cell.below()),
//     grow(level, cell.below(), UP) and return;
//   - else if blocksStalagmiteScan(level, cell, cellState), return (blocked).
//
// blocksStalagmiteScan is the PointedDripstoneBlock override (!canDripThrough). CITE:
// SpeleothemBlock.growStalagmiteBelow; PointedDripstoneBlock.blocksStalagmiteScan; canDripThrough.
func (t *TickLoop) dripstoneGrowStalagmiteBelow(pos pk.Position) {
	cur := pos
	for i := 0; i < dripStalagmiteMaxSearch; i++ {
		cur = below(cur)
		s, ok := t.world().GetBlock(cur, dimMinY)
		if !ok {
			s = block.ToStateID[block.Air{}]
		}
		if !t.dripstoneGetFluidState(cur).isEmpty() {
			return
		}
		if block.IsUnmergedPointedDripstoneTipWithDirection(s, block.Up) && t.dripstoneCanTipGrow(s, cur) {
			t.dripstoneGrow(cur, block.Up)
			return
		}
		if t.dripstoneIsValidSpeleothemPlacement(cur, block.Up) && !t.dripstoneIsWaterAt(below(cur)) {
			t.dripstoneGrow(below(cur), block.Up)
			return
		}
		if t.dripstoneBlocksStalagmiteScan(cur, s) {
			return
		}
	}
}

// dripstoneIsValidSpeleothemPlacement ports SpeleothemBlock.isValidSpeleothemPlacement(level, pos, dir)
// for the UP case used by growStalagmiteBelow: the block behind the tip (pos.relative(dir.opposite) ==
// pos.below() for UP) must be face-sturdy on dir, OR be a speleothem-with-direction(dir) of this same
// block. CITE: SpeleothemBlock.isValidSpeleothemPlacement.
//
// CITED STUB: BlockState.isFaceSturdy is the full per-face collision-shape sturdiness test; the block
// layer models it here as isSolidAt (solid, non-air, non-fluid), the same subset used elsewhere in the
// port. The speleothem-chain fallback (a stacked dripstone below) IS ported exactly.
func (t *TickLoop) dripstoneIsValidSpeleothemPlacement(pos pk.Position, dir block.Direction) bool {
	behind := dripRelative(pos, dripOpposite(dir))
	bs, ok := t.world().GetBlock(behind, dimMinY)
	if !ok {
		return false
	}
	if t.isSolidAt(behind) {
		return true
	}
	// isSpeleothemWithDirection(behind, dir) AND behind.is(this).
	return block.IsPointedDripstoneWithDirection(bs, dir)
}

// dripstoneBlocksStalagmiteScan ports PointedDripstoneBlock.blocksStalagmiteScan(level, pos, state) ==
// !canDripThrough(level, pos, state). canDripThrough: air -> true; solidRender -> false; non-empty
// fluid -> false; otherwise the collision shape must leave the central 4..16 drip column clear.
// CITE: PointedDripstoneBlock.blocksStalagmiteScan; canDripThrough.
//
// CITED STUB: the VoxelShape collision-column test (Shapes.joinIsNotEmpty over
// REQUIRED_SPACE_TO_DRIP_THROUGH_NON_SOLID_BLOCK) is not modelled; the block layer resolves the
// air/solid/fluid cases exactly (which cover the overwhelmingly common cells) and treats a remaining
// non-solid, non-air, fluidless block as drip-through (canDripThrough true -> does NOT block), the
// conservative vanilla-leaning choice. CITE: canDripThrough (VoxelShape branch).
func (t *TickLoop) dripstoneBlocksStalagmiteScan(pos pk.Position, state block.StateID) bool {
	return !t.dripstoneCanDripThrough(pos, state)
}

func (t *TickLoop) dripstoneCanDripThrough(pos pk.Position, state block.StateID) bool {
	if block.IsAir(state) {
		return true
	}
	if t.isSolidAt(pos) { // isSolidRender subset: a solid block occludes the drip column.
		return false
	}
	if !t.dripstoneGetFluidState(pos).isEmpty() {
		return false
	}
	// VoxelShape column test DEFERRED (cited): default to drip-through for a non-solid, fluidless block.
	return true
}

// dripstoneIsWaterAt ports Level.isWaterAt(pos) == getFluidState(pos).is(FluidTags.WATER): a true water
// cell OR a waterlogged block (SimpleWaterloggedBlock.getFluidState -> WATER). CITE: Level.isWaterAt.
func (t *TickLoop) dripstoneIsWaterAt(pos pk.Position) bool {
	return t.dripstoneGetFluidState(pos).isWater
}

// dripFluidKind enumerates the fluid the drip carries (getFluidAboveStalactite FluidInfo.fluid).
type dripFluidKind int

const (
	dripFluidNone dripFluidKind = iota
	dripFluidWater
	dripFluidLava
)

// dripstoneFluidAboveStalactite ports PointedDripstoneBlock.getFluidAboveStalactite(level, pos, state)
// for its fluid KIND and the FluidInfo.pos (== root.above()): if !isStalactite return empty;
// findRootBlock(level, pos, state, 11); map the root to the FluidInfo built from root.above() -- MUD
// (not evaporating) -> WATER else the cell fluid. Returns (kind, root.above(), ok). CITE:
// PointedDripstoneBlock.getFluidAboveStalactite; lambda getFluidAboveStalactite 0; findRootBlock.
//
// CITED STUB: EnvironmentAttributes.WATER_EVAPORATES (the biome/dimension water-evaporates read that
// turns a MUD source into no-water in dry dimensions) is treated as its overworld default FALSE -- the
// vanilla default for the dripstone-caves overworld where dripstone grows -- so MUD above a stalactite
// yields WATER, matching vanilla behavior. Structured to become a real attribute read later.
func (t *TickLoop) dripstoneFluidAboveStalactite(state block.StateID, pos pk.Position) (dripFluidKind, pk.Position, bool) {
	if !block.IsStalactite(state) {
		return dripFluidNone, pk.Position{}, false
	}
	root, ok := t.dripstoneFindRootBlock(state, pos, dripTransferSearchLength)
	if !ok {
		return dripFluidNone, pk.Position{}, false
	}
	fluidPos := above(root)
	fs, fok := t.world().GetBlock(fluidPos, dimMinY)
	if !fok {
		fs = block.ToStateID[block.Air{}]
	}
	if block.IsMud(fs) {
		// !WATER_EVAPORATES (overworld default false) -> WATER.
		return dripFluidWater, fluidPos, true
	}
	f := t.dripstoneGetFluidState(fluidPos)
	switch {
	case f.isWater:
		return dripFluidWater, fluidPos, true
	case f.isLava:
		return dripFluidLava, fluidPos, true
	default:
		return dripFluidNone, fluidPos, true
	}
}

// dripstoneFindRootBlock ports PointedDripstoneBlock.findRootBlock(level, pos, state, maxLen) 1:1: walk
// in the OPPOSITE of TIP_DIRECTION (UP for a stalactite) via findBlockVertical, continuing while the
// scanned block is this speleothem with TIP_DIRECTION == dir, stopping at the first block that is NOT
// this block (the root the chain hangs from). CITE: PointedDripstoneBlock.findRootBlock;
// lambda findRootBlock 0/1.
func (t *TickLoop) dripstoneFindRootBlock(state block.StateID, pos pk.Position, maxLen int) (pk.Position, bool) {
	dir, ok := block.PointedDripstoneVerticalDirection(state)
	if !ok {
		return pk.Position{}, false
	}
	// findRootBlock walks along dir.getOpposite().getAxisDirection() -- i.e. toward the root (UP for a
	// DOWN stalactite). CITE: findRootBlock (Direction.getOpposite -> getAxisDirection).
	step := dripStepForDirection(dripOpposite(dir))
	// continue predicate: state.is(this) AND getValue(TIP_DIRECTION) == dir (lambda findRootBlock 0).
	cont := func(s block.StateID) bool { return block.IsPointedDripstoneWithDirection(s, dir) }
	// stop predicate: !state.is(this) -- the first non-dripstone cell is the root (lambda findRootBlock 1).
	stop := func(s block.StateID) bool { return !block.IsPointedDripstone(s) }
	return t.dripstoneFindBlockVertical(pos, step, cont, stop, maxLen)
}

// dripFluidState is the getFluidState result the dripstone port reads: whether it is water or lava and
// its source flag. It folds waterlogged blocks into WATER-source, matching
// SimpleWaterloggedBlock.getFluidState. Isolated from the flow-sim fluidState so this port only exposes
// what it needs.
type dripFluidState struct {
	isWater bool
	isLava  bool
	source  bool
}

func (f dripFluidState) isEmpty() bool { return !f.isWater && !f.isLava }

// dripstoneGetFluidState ports BlockState.getFluidState() for the dripstone reads: a water/lava fluid
// block yields that fluid (source flag preserved), a waterlogged block yields WATER source, everything
// else is empty. CITE: BlockState.getFluidState; SimpleWaterloggedBlock.getFluidState.
func (t *TickLoop) dripstoneGetFluidState(pos pk.Position) dripFluidState {
	f := t.fluidAt(pos)
	if f.isWater {
		return dripFluidState{isWater: true, source: f.source}
	}
	if f.isLava {
		return dripFluidState{isLava: true, source: f.source}
	}
	if s, ok := t.world().GetBlock(pos, dimMinY); ok && block.IsWaterloggedState(s) {
		return dripFluidState{isWater: true, source: true}
	}
	return dripFluidState{}
}

// dripStepForDirection maps a vertical Direction AxisDirection to a +1/-1 Y step. UP -> +1, DOWN -> -1.
// Only vertical directions reach here (speleothems are always vertical). CITE: Direction.get(axisDir,
// Y) in findBlockVertical.
func dripStepForDirection(dir block.Direction) int {
	if dir == block.Up {
		return 1
	}
	return -1
}

// dripOpposite returns the opposite of a vertical Direction (Direction.getOpposite for UP/DOWN).
func dripOpposite(dir block.Direction) block.Direction {
	if dir == block.Up {
		return block.Down
	}
	return block.Up
}

// dripRelative returns pos.relative(dir) for the vertical directions the dripstone port uses.
func dripRelative(pos pk.Position, dir block.Direction) pk.Position {
	if dir == block.Up {
		return above(pos)
	}
	return below(pos)
}

// dripMinBuildY / dripMaxBuildY bound Level.isOutsideBuildHeight: the buildable Y span is
// [dimMinY, dimMinY+384) so the inclusive max block Y is dimMinY+383. CITE: Level.isOutsideBuildHeight.
const (
	dripMinBuildY = dimMinY
	dripMaxBuildY = dimMinY + 384 - 1
)
