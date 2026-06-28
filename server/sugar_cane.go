package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// sugar_cane.go — SUB-BLOCKTICK's first end-to-end consumer: the 1:1 port of
// net.minecraft.world.level.block.SugarCaneBlock (tick / canSurvive / randomTick / updateShape).
// It proves the scheduled-block-tick pipeline schedule -> drain-at-target -> tickBlock -> handler
// works against real vanilla logic.
//
// CITE (temp/cache/26.2-inner.jar, `javap -c -p` this session):
//   SugarCaneBlock.tick(state, level, pos, random):
//       if (!state.canSurvive(level, pos)) level.destroyBlock(pos, true);
//   SugarCaneBlock.canSurvive(state, level, pos):
//       BlockState below = level.getBlockState(pos.below());
//       if (below.is(this)) return true;                                  // sugar cane on sugar cane
//       if (below.is(BlockTags.SUPPORTS_SUGAR_CANE)) {
//           for (Direction d : Direction.Plane.HORIZONTAL) {
//               BlockPos b = pos.below().relative(d);                     // NB: relative off pos.below()
//               BlockState bs = level.getBlockState(b);
//               FluidState fs = level.getFluidState(b);
//               if (fs.is(FluidTags.SUPPORTS_SUGAR_CANE_ADJACENTLY)
//                || bs.is(BlockTags.SUPPORTS_SUGAR_CANE_ADJACENTLY)) return true;
//           }
//       }
//       return false;
//   SugarCaneBlock.updateShape(state, level, ticker, pos, dir, neighborPos, neighborState, random):
//       if (!state.canSurvive(level, pos)) ticker.scheduleTick(pos, this, 1);
//       return super.updateShape(...);
//   SugarCaneBlock.randomTick(state, level, pos, random):
//       grow when the cell above is empty and the cane column is < 3 tall; at AGE 15 place a new
//       cane above and reset AGE to 0, else AGE++ (setBlock flag 0x104 == 260).

// sugarCaneTickDelay is the updateShape reschedule delay — SugarCaneBlock.updateShape passes
// `1` to scheduleTick(pos, this, 1). CITE: SugarCaneBlock.updateShape (iconst_1).
const sugarCaneTickDelay = 1

// sugarCaneTick is SugarCaneBlock.tick: destroy (and drop) the cane if it can no longer survive.
// destroyBlock(pos, /*dropBlock=*/true) -> setBlock(air) + popResource(self) + neighbor updates.
// We mirror Sulfur's destroyBlock seam (SetBlock air, broadcast, drop, then re-run the edit-time
// neighbor checks so a cane stacked above this one re-evaluates its own support). CITE:
// SugarCaneBlock.tick.
func (t *TickLoop) sugarCaneTick(state block.StateID, pos pk.Position) {
	if t.only().world == nil {
		return
	}
	if t.sugarCaneCanSurvive(pos) {
		return // still supported: tick does nothing
	}
	// destroyBlock(pos, true): set air, broadcast, drop the cane (dropBlock=true, entity=null so a
	// nil player always drops). spawnBlockDrop rolls sugar_cane's loot table for the broken state.
	if !t.only().world.SetBlock(pos, t.airState(), dimMinY) {
		return // already air/unloaded: destroyBlock returned false
	}
	t.broadcastBlockUpdate(pos, t.airState())
	t.spawnBlockDrop(nil, pos, state)
	// destroyBlock -> updateNeighborsAt(pos): re-run the edit-time neighbor reconciliation so a
	// cane in the cell ABOVE (whose support — this cell — just became air) schedules its own
	// destroy tick via updateShape. This is the cascade that topples a tall cane column.
	t.onBlockTickEdit(pos)
}

// sugarCaneCanSurvive is SugarCaneBlock.canSurvive(state, level, pos). A read on an unloaded/
// unreadable neighbor is treated as "not present" (vanilla getBlockState on an unloaded chunk
// returns air / getFluidState returns empty — neither supports cane), which is the faithful,
// non-destructive default: a cane never spuriously survives on an unreadable neighbor, and it
// only destroys on a positively-read invalid support. CITE: SugarCaneBlock.canSurvive.
func (t *TickLoop) sugarCaneCanSurvive(pos pk.Position) bool {
	belowPos := below(pos)
	belowState, ok := t.only().world.GetBlock(belowPos, dimMinY)
	if !ok {
		return false // below unreadable -> not sugar cane, not a support block -> cannot survive
	}
	// below.is(this): sugar cane stacked on sugar cane always survives.
	if block.IsSugarCane(belowState) {
		return true
	}
	// below.is(SUPPORTS_SUGAR_CANE): sand/dirt-family/etc. Then require an adjacent water (or
	// frosted ice) to the cell BELOW the cane (NOT to the cane itself). CITE: the loop iterates
	// Direction.Plane.HORIZONTAL relative off `pos.below()`.
	if !block.IsSupportsSugarCane(belowState) {
		return false
	}
	for _, d := range horizontalDirections {
		nb := pk.Position{X: belowPos.X + d.dx, Y: belowPos.Y, Z: belowPos.Z + d.dz}
		nbState, ok := t.only().world.GetBlock(nb, dimMinY)
		if !ok {
			continue // unreadable neighbor: not a supporting fluid/block (non-destructive skip)
		}
		// fs.is(FluidTags.SUPPORTS_SUGAR_CANE_ADJACENTLY) -> water (any level);
		// bs.is(BlockTags.SUPPORTS_SUGAR_CANE_ADJACENTLY) -> frosted_ice.
		if block.IsSupportsSugarCaneAdjacentlyFluid(nbState) || block.IsSupportsSugarCaneAdjacentlyBlock(nbState) {
			return true
		}
	}
	return false
}

// horizontalDir is one of the four cardinal horizontal directions (Direction.Plane.HORIZONTAL).
// Vanilla's iteration order is NORTH, SOUTH, WEST, EAST (Direction.Plane.HORIZONTAL backing
// array); the order only affects which adjacent water is FOUND first, and canSurvive returns on
// the first match, so the result is order-independent (any one adjacent water suffices). We list
// the vanilla order for fidelity. CITE: Direction.Plane.HORIZONTAL.
type horizontalDir struct{ dx, dz int }

var horizontalDirections = []horizontalDir{
	{dx: 0, dz: -1}, // NORTH (-z)
	{dx: 0, dz: 1},  // SOUTH (+z)
	{dx: -1, dz: 0}, // WEST (-x)
	{dx: 1, dz: 0},  // EAST (+x)
}

// updateShapeSugarCane is the SugarCaneBlock.updateShape branch run from the edit-time neighbor
// reconciliation (Level.updateNeighborsAt): if the cane at pos can no longer survive, SCHEDULE a
// destroy tick 1 tick out (rather than destroying immediately) — the block-tick subsystem fires
// it next tick via tickBlock -> sugarCaneTick. This is the schedule HALF of the round-trip the
// tick subsystem exists to drive. CITE: SugarCaneBlock.updateShape (`if (!canSurvive)
// scheduleTick(pos, this, 1)`).
func (t *TickLoop) updateShapeSugarCane(pos pk.Position) {
	if t.only().world == nil {
		return
	}
	if t.sugarCaneCanSurvive(pos) {
		return // still supported: updateShape returns the state unchanged, no tick scheduled
	}
	// Guard against piling up duplicate ticks (LevelChunkTicks dedups by (type,pos) anyway, but
	// the hasScheduledTick check mirrors how vanilla blocks avoid redundant scheduleTick calls).
	if t.hasScheduledBlockTick(pos, sugarCaneTickType) {
		return
	}
	t.scheduleBlockTick(pos, sugarCaneTickType, sugarCaneTickDelay)
}

// onBlockTickEdit is the SUB-BLOCKTICK slice of Level.updateNeighborsAt: after a cell at `pos`
// changes (a break/place, or a scheduled destroy), re-evaluate the support of the block ABOVE —
// if it is sugar cane that can no longer survive, schedule its destroy tick (updateShape). This
// is the hook reconcileEdit and sugarCaneTick call to wake the cane above a changed cell. CITE:
// ServerLevel.updateNeighborsAt -> SugarCaneBlock.updateShape.
func (t *TickLoop) onBlockTickEdit(pos pk.Position) {
	if t.only().world == nil {
		return
	}
	abovePos := above(pos)
	aboveState, ok := t.only().world.GetBlock(abovePos, dimMinY)
	if !ok {
		return
	}
	if block.IsSugarCane(aboveState) {
		t.updateShapeSugarCane(abovePos)
	}
}

// sugarCaneRandomTick is SugarCaneBlock.randomTick: grow the cane. If the cell ABOVE is empty
// and the cane column (counting downward) is shorter than 3, advance AGE; at AGE 15 place a new
// cane in the empty cell above (AGE 0) and reset this cell's AGE to 0; otherwise AGE++. setBlock
// flag 260 (0x104) == UPDATE_CLIENTS|UPDATE_KNOWN_SHAPE (no neighbor notify) — we mirror it with
// SetBlock + broadcast (no neighbor reconcile, matching the flag's intent). This is wired for
// completeness; random ticking is driven by the world's random-tick pass (a separate subsystem),
// so it is exposed for that caller and unit-tested here. CITE: SugarCaneBlock.randomTick.
func (t *TickLoop) sugarCaneRandomTick(state block.StateID, pos pk.Position) {
	if t.only().world == nil {
		return
	}
	abovePos := above(pos)
	// level.isEmptyBlock(pos.above()): the cell above must be air to grow.
	aboveState, ok := t.only().world.GetBlock(abovePos, dimMinY)
	if !ok || !block.IsAir(aboveState) {
		return
	}
	// Count the cane column height (this cell + every sugar cane directly below), capped at the
	// `< 3` check. Vanilla: `int i = 1; while (level.getBlockState(pos.below(i)).is(this)) i++;
	// if (i < 3) { ... }`. i starts at 1 (this cell), increments per sugar cane below.
	height := 1
	for {
		bp := pk.Position{X: pos.X, Y: pos.Y - height, Z: pos.Z}
		bs, ok := t.only().world.GetBlock(bp, dimMinY)
		if !ok || !block.IsSugarCane(bs) {
			break
		}
		height++
	}
	if height >= 3 {
		return // column already 3 tall: no growth
	}
	age := block.SugarCaneAge(state)
	if age < 0 {
		return // not sugar cane (defensive)
	}
	if age == 15 {
		// Place a new cane above (default AGE 0) and reset this cell to AGE 0.
		if newCane, ok := block.SugarCaneState(0); ok {
			if t.only().world.SetBlock(abovePos, newCane, dimMinY) {
				t.broadcastBlockUpdate(abovePos, newCane)
			}
			if reset, ok := block.SugarCaneState(0); ok {
				if t.only().world.SetBlock(pos, reset, dimMinY) {
					t.broadcastBlockUpdate(pos, reset)
				}
			}
		}
		return
	}
	// AGE++.
	if grown, ok := block.SugarCaneState(age + 1); ok {
		if t.only().world.SetBlock(pos, grown, dimMinY) {
			t.broadcastBlockUpdate(pos, grown)
		}
	}
}
