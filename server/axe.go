package server

import (
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// axe.go -- AXE strip (log/wood -> stripped) + copper de-oxidize (scrape), ported 1:1 from the
// unobfuscated 26.2 jar (net.minecraft.world.item.AxeItem.useOn -> evaluateNewBlockState). A player
// right-clicking a strippable log/wood/stem/hyphae/bamboo with an axe strips it (preserving AXIS), and
// right-clicking an oxidized copper de-oxidizes it one tier. This is a useOn-block action, so it hooks
// the handleUseItemOn (block) path BEFORE block placement -- an axe is not a block item.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//	AxeItem.useOn(ctx):
//	    if (playerHasBlockingItemUseIntent(ctx)) return PASS;   // (offhand shield-raise intent)
//	    Optional<BlockState> opt = evaluateNewBlockState(level, pos, player, getBlockState(pos));
//	    if (opt.isEmpty()) return PASS;
//	    ... (advancement trigger) ...
//	    level.setBlock(pos, opt.get(), 11); gameEvent(BLOCK_CHANGE);
//	    if (player != null) getItemInHand().hurtAndBreak(1, player, getHand().asEquipmentSlot());
//	    return SUCCESS;
//
//	AxeItem.evaluateNewBlockState(level, pos, player, state):
//	    Optional<BlockState> stripped = getStripped(state);       // STRIPPABLES + setValue(AXIS, ...)
//	    if (stripped.isPresent()) { playSound(AXE_STRIP); return stripped; }
//	    Optional<BlockState> scraped = WeatheringCopper.getPrevious(state);  // de-oxidize one tier
//	    if (scraped.isPresent()) { spawnSoundAndParticle(AXE_SCRAPE, 3005); return scraped; }
//	    Optional<BlockState> waxOff = Optional.ofNullable(WAX_OFF_BY_BLOCK.get(state.getBlock()))
//	        .map(b -> b.withPropertiesOf(state));
//	    if (waxOff.isPresent()) { spawnSoundAndParticle(AXE_WAX_OFF, 3004); return waxOff; }
//	    return Optional.empty();
//
// flag 11 mirrored as SetBlock + broadcastBlockUpdate. playSound / spawnSoundAndParticle / gameEvent are
// cited no-ops (no sound/particle/gameEvent bus). hurtAndBreak is DURABILITY -- no item-durability
// subsystem (cited follow-up). No RNG draw -- the pig oracle levelRandom stream is unperturbed.
//
// FOLLOW-UP (cited, not ported): the WAX_OFF arm (HoneycombItem.WAX_OFF_BY_BLOCK, waxed_* -> unwaxed_*)
// -- it needs the WAX_OFF BiMap port. playerHasBlockingItemUseIntent (offhand blocking-item raise) is
// also a v1 no-op: Sulfur has no shield-raise-while-axe-strip interaction, so the guard is always false
// (the common axe-strip case), matching a player with no blocking offhand item.

// isAxeItem reports whether an item id is any axe tier (AxeItem spans
// wooden/copper/stone/golden/iron/diamond/netherite). Stand-in for instanceof AxeItem. Tick-owned read.
func isAxeItem(itemID int32) bool {
	switch item.ID(itemID) {
	case item.WoodenAxe.ID, item.CopperAxe.ID, item.StoneAxe.ID, item.GoldenAxe.ID,
		item.IronAxe.ID, item.DiamondAxe.ID, item.NetheriteAxe.ID:
		return true
	}
	return false
}

// axeEvaluateNewBlockState is AxeItem.evaluateNewBlockState: try strip, then copper de-oxidize (scrape).
// Returns the new state + ok=true when a transform applies, ok=false (Optional.empty) otherwise. The
// wax-off arm is a cited follow-up.
func axeEvaluateNewBlockState(s block.StateID) (block.StateID, bool) {
	if stripped, ok := block.StrippedState(s); ok {
		return stripped, true
	}
	if scraped, ok := block.CopperGetPrevious(s); ok {
		return scraped, true
	}
	return s, false
}

// tryAxeUseOn is the AxeItem.useOn port, hooked in handleUseItemOn BEFORE block placement. Returns true
// when the held item is an axe AND the clicked block yields a new state (stripped or de-oxidized) -- the
// use consumed the action. Returns false when the held item is not an axe OR evaluateNewBlockState is
// empty (then placement continues -- a no-op for the non-block axe, matching vanilla PASS).
func (t *TickLoop) tryAxeUseOn(p *tickPlayer, inv *Inventory, held component.SlotData, pos pk.Position) bool {
	if slotIsEmpty(held) || !isAxeItem(int32(held.ItemID)) {
		return false // not an axe -> PASS
	}
	pmgr := t.dimWorld(p)
	pMinY := dimMinYFor(p.dimension)
	if pmgr == nil {
		return false
	}
	state, ok := pmgr.GetBlock(pos, pMinY)
	if !ok {
		return false
	}
	newState, ok := axeEvaluateNewBlockState(state)
	if !ok {
		return false // Optional.empty -> PASS (placement continues, a no-op for the axe)
	}
	if !t.withinReach(p, pos) {
		return false
	}
	t.withRegion(t.regionForColumn(columnOf(float64(pos.X)+0.5, float64(pos.Z)+0.5)), func() {
		if pmgr.SetBlock(pos, newState, pMinY) {
			t.broadcastBlockUpdate(pos, newState)
		}
	})
	// hurtAndBreak(1, player, ...): DURABILITY, not a stack shrink -- no item-durability subsystem yet.
	return true
}
