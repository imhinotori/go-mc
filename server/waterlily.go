package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/world"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// waterlily.go -- WaterlilyItem (PlaceOnWaterBlockItem) right-click-air place, ported 1:1 from the
// unobfuscated 26.2 jar (net.minecraft.world.item.PlaceOnWaterBlockItem.use). A player right-clicking air
// with a lily_pad raytraces to a water SOURCE and places the lily_pad on the cell ABOVE that source (via
// BlockItem.useOn at hitPos.above), consuming 1 in survival. This is a right-click-AIR (Item.use) path, so
// it hooks useItemInHand BEFORE the food gate -- lily_pad is not food.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//	PlaceOnWaterBlockItem.use(level, player, hand):
//	    hit = getPlayerPOVHitResult(level, player, ClipContext.Fluid.SOURCE_ONLY);   // stop on a source
//	    hit2 = hit.withPosition(hit.getBlockPos().above());                          // place ABOVE it
//	    return super.useOn(new UseOnContext(player, hand, hit2));                    // BlockItem.useOn
//
//	BlockItem.useOn -> place: canPlace() (target replaceable) -> setBlock(getClickedPos, defaultState) ->
//	    consume(1, player). getClickedPos == hit2.getBlockPos() (a MISS on the fluid raycast never reaches
//	    a BLOCK hit, so the useOn is a PASS no-op).
//
// So: raytrace SOURCE_ONLY (reusing the bucket raycast), place lily_pad at hitPos.above() when that cell is
// replaceable (air/fluid), consuming 1 in survival (creative keeps it). A MISS or a non-replaceable target
// is a no-op that STILL belongs to the lily_pad (return true -- no food fall-through). Lily-pad-gated (a
// cheap id compare, no RNG draw -- the pig oracle levelRandom is unperturbed).

// tryUseWaterlily is the PlaceOnWaterBlockItem.use port for the right-click-air path. Returns true when the
// held item is a lily_pad (the use belongs to the lily_pad -- placed, or a MISS/FAIL no-op), false to fall
// through to the food/other-item resolution. Tick-owned.
func (t *TickLoop) tryUseWaterlily(p *tickPlayer, inv *Inventory, held component.SlotData, hand int32) bool {
	if slotIsEmpty(held) || int32(held.ItemID) != int32(item.LilyPad.ID) {
		return false // not a lily_pad: fall through
	}
	pmgr := t.dimWorld(p)
	pMinY := dimMinYFor(p.dimension)
	if pmgr == nil {
		return false
	}

	// getPlayerPOVHitResult(SOURCE_ONLY): raytrace to a fluid SOURCE (reuse the bucket raycast).
	blockPos, _, hit := t.bucketPovHitResult(p, clipFluidSourceOnly)
	if !hit {
		return true // MISS -> the raycast hit no source -> PASS no-op (still the lily_pad's use)
	}
	// hit.withPosition(hit.getBlockPos().above()): the lily_pad places on the cell ABOVE the source.
	placePos := pk.Position{X: blockPos.X, Y: blockPos.Y + 1, Z: blockPos.Z}

	// BlockItem.canPlace: the target (above the source) must be replaceable (air). A lily_pad over a
	// covered water surface (a block already above the source) fails to place -> FAIL no-op.
	targetState, ok := pmgr.GetBlock(placePos, pMinY)
	if !ok || !isReplaceableState(targetState) {
		return true // !canPlace() -> FAIL, but the interact still belonged to the lily_pad
	}
	if !t.withinReach(p, placePos) {
		return true
	}

	// LilyPadBlock.mayPlaceOn(below): getPlacementState/canSurvive gates the place on the cell BELOW the
	// lily (== blockPos, the ray-hit cell) supporting a lily. mayPlaceOn(state, level, below):
	//   (fluidState(below).is(FluidTags.SUPPORTS_LILY_PAD{water}) || state(below).is(BlockTags
	//     .SUPPORTS_LILY_PAD{ice,frosted_ice})) && fluidState(above==placePos).is(Fluids.EMPTY).
	// So the lily only lands on a water SOURCE/flowing (or ice/frosted_ice) with a fluid-free cell above.
	// This rejects placing on a stone floor top (below is stone, not water/ice). CITE: LilyPadBlock.mayPlaceOn.
	if !t.waterlilyMayPlaceOn(pmgr, pMinY, blockPos, placePos) {
		return true // !canSurvive() -> canPlace FAIL, still the lily_pad's use
	}

	placeState, ok := block.DefaultStateID["minecraft:lily_pad"]
	if !ok {
		return true
	}

	// placeBlock -> Level.setBlock(getClickedPos, defaultState). Run the mutation in the region owning the
	// placement column so the per-region schedule side of reconcileEdit lands in the right queue.
	placed := false
	t.withRegion(t.regionForColumn(columnOf(float64(placePos.X)+0.5, float64(placePos.Z)+0.5)), func() {
		if !pmgr.SetBlock(placePos, placeState, pMinY) {
			return
		}
		t.broadcastBlockUpdate(placePos, placeState)
		placed = true
	})
	if !placed {
		return true
	}

	// consume(1, player): shrink the lily_pad by 1 in survival (creative keeps it).
	if p.gameMode != gameModeCreative {
		slot := heldMenuSlot(p, hand)
		before := inv.snapshot()
		cur := inv.get(slot)
		cur.Count--
		if cur.Count <= 0 {
			cur = component.SlotData{Count: 0}
		}
		inv.set(slot, cur)
		t.broadcastInventoryChanges(p, inv, before)
	}
	return true
}

// waterlilyMayPlaceOn is LilyPadBlock.mayPlaceOn(state, level, below): the below cell must be a
// lily-supporting fluid (water, per FluidTags.SUPPORTS_LILY_PAD) OR a lily-supporting block (ice /
// frosted_ice, per BlockTags.SUPPORTS_LILY_PAD), AND the lily's own cell (above == placePos) must be
// fluid-free (Fluids.EMPTY). `below` is the ray-hit source cell; `placePos` == below.above(). CITE:
// LilyPadBlock.mayPlaceOn.
func (t *TickLoop) waterlilyMayPlaceOn(pmgr *world.ChunkManager, minY int, below, placePos pk.Position) bool {
	belowState, ok := pmgr.GetBlock(below, minY)
	if !ok {
		return false
	}
	belowFluid := decodeFluid(belowState)
	// FluidTags.SUPPORTS_LILY_PAD == { water } (source or flowing).
	supportFluid := belowFluid.isWater
	// BlockTags.SUPPORTS_LILY_PAD == { ice, frosted_ice }.
	supportBlock := false
	switch blockNameForState(belowState) {
	case "minecraft:ice", "minecraft:frosted_ice":
		supportBlock = true
	}
	if !supportFluid && !supportBlock {
		return false
	}
	// fluidState(placePos).is(Fluids.EMPTY): the lily's own cell must carry no fluid.
	if ps, ok := pmgr.GetBlock(placePos, minY); ok {
		f := decodeFluid(ps)
		if f.isWater || f.isLava {
			return false
		}
	}
	return true
}
