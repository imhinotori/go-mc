package server

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// decorated_pot_be.go -- the DECORATED_POT BLOCK-ENTITY: a 1:1 port of net.minecraft.world.level.block
// .entity.DecoratedPotBlockEntity (the ContainerSingleItem storage) + the DecoratedPotBlock.useItemOn
// insert path + the comparator getAnalogOutputSignal, over the 26.2 jar (temp/cache/26.2-inner.jar, javap
// -c -p this session). A decorated pot holds ONE item (getTheItem/setTheItem); right-clicking it with a
// matching or empty slot INSERTS one item (growing an existing same-item stack up to its max size). The
// redstone comparator reads the single-slot container fill fraction.
//
// 1:1 jar (VERIFIED javap this session):
//   DecoratedPotBlock.useItemOn(stack, ...): be = getBlockEntity; if not a DecoratedPotBlockEntity -> PASS.
//       existing = be.getTheItem();
//       if (!stack.isEmpty() && (existing.isEmpty() ||
//               (isSameItemSameComponents(existing, stack) && existing.getCount() < existing.getMaxStackSize()))) {
//           be.wobble(POSITIVE); player.awardStat(ITEM_USED.get(stack.getItem()));
//           returned = stack.consumeAndReturn(1, player);
//           if (be.isEmpty()) be.setTheItem(returned); else existing.grow(1);
//           playSound(DECORATED_POT_INSERT, 0.7, 0.5 + 0.5*fillFraction);  [client cue]
//           sendParticles(DUST_PLUME, ...);                                 [client cue]
//           be.setChanged(); level.gameEvent(BLOCK_CHANGE); return SUCCESS;
//       }
//       return TRY_WITH_EMPTY_HAND;
//   DecoratedPotBlock.getAnalogOutputSignal(state, level, pos, dir): AbstractContainerMenu
//       .getRedstoneSignalFromBlockEntity(getBlockEntity) == getRedstoneSignalFromContainer over the single
//       slot (container.go). hasAnalogOutputSignal == true.
//
// DEFERRED (cited -- client cosmetics / not-yet-built subsystems, NOT paraphrase):
//   - wobble(WobbleStyle) + the DECORATED_POT_INSERT sound + DUST_PLUME particles: client cues with no
//     gameplay effect (the same no-op seam the other BEs defer).
//   - playerWillDestroy shatter (a non-BREAKS_DECORATED_POTS tool sets CRACKED then the drop yields the
//     sherds + the stored item as SHATTER loot): the break-loot/sherd-drop path is a cited follow-up (no
//     BE-aware block-loot seam wired for decorated_pot yet). The stored item + comparator + insert are the
//     server-authoritative gameplay and are ported here.
//   - loot-table fill (RandomizableContainer): a placed pot carries no loot table (structure-placed only),
//     so the loot path is a no-op here -- cited, matching the placed-empty case.

// decoratedPotBE is the tick-owned state of one decorated_pot block-entity -- the Go analogue of
// DecoratedPotBlockEntity narrowed to the single held item (empty Count 0 == ItemStack.EMPTY). Registered
// on placement; read by the insert-use path + the comparator analog-output seam. The wobble/decorations
// (client render data) are cite-deferred.
type decoratedPotBE struct {
	item component.SlotData
}

// isEmpty is DecoratedPotBlockEntity.isEmpty() (ContainerSingleItem): the single slot is empty.
func (d *decoratedPotBE) isEmpty() bool { return stackEmpty(d.item) }


// decoratedPotComparator ports the single-slot form of AbstractContainerMenu.getRedstoneSignalFromContainer
// for a decorated pot (containerSize 1): f = item.getCount() / container.getMaxStackSize(item); return
// Mth.lerpDiscrete(f, 0, 15). For an empty pot f == 0 -> 0. Reuses mthLerpDiscrete (container.go). CITE:
// AbstractContainerMenu.getRedstoneSignalFromContainer (single-slot) + DecoratedPotBlock.getAnalogOutputSignal.
func decoratedPotComparator(item component.SlotData) int {
	if stackEmpty(item) {
		return mthLerpDiscrete(0, 0, 15)
	}
	f := float32(int(item.Count)) / float32(stackMaxSize(item))
	return mthLerpDiscrete(f, 0, 15)
}

// resolveDecoratedPot returns the tick-owned decoratedPotBE for pos, creating an EMPTY one on first access
// (a freshly-placed pot's empty single slot). Returns nil when pos is not a decorated_pot (or the world is
// unloaded). Tick-owned (t.decoratedPots, the t.jukeboxes twin). CITE: DecoratedPotBlock (BaseEntityBlock).
func (t *TickLoop) resolveDecoratedPot(pos pk.Position) *decoratedPotBE {
	if t.decoratedPots == nil {
		t.decoratedPots = make(map[pk.Position]*decoratedPotBE)
	}
	if d, ok := t.decoratedPots[pos]; ok {
		return d
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsDecoratedPot(state) {
		return nil
	}
	d := &decoratedPotBE{}
	t.decoratedPots[pos] = d
	return d
}

// useDecoratedPot ports DecoratedPotBlock.useItemOn: right-clicking a pot with a held item INSERTS one when
// the pot is empty OR already holds the same item with room (grows the existing stack by 1). Consumes 1
// from the held slot (creative-exempt). Returns true when the interaction was consumed (SUCCESS -- no
// block-place fall-through); false (PASS / TRY_WITH_EMPTY_HAND) when the held item cannot be inserted, so
// placement continues. CITE: DecoratedPotBlock.useItemOn.
func (t *TickLoop) useDecoratedPot(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	if t.world() == nil {
		return false
	}
	d := t.resolveDecoratedPot(pos)
	if d == nil {
		return false // not a DecoratedPotBlockEntity -> PASS.
	}
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))

	// if (!stack.isEmpty() && (existing.isEmpty() || (isSameItemSameComponents(existing, stack) &&
	//     existing.getCount() < existing.getMaxStackSize())))
	existing := d.item
	if stackEmpty(held) {
		return false // empty hand -> TRY_WITH_EMPTY_HAND / useWithoutItem (no insert); placement continues.
	}
	if !stackEmpty(existing) {
		if !stackSameItemSameComponents(existing, held) || int(existing.Count) >= stackMaxSize(existing) {
			return false // full or mismatched -> TRY_WITH_EMPTY_HAND.
		}
	}

	// be.wobble(POSITIVE): client cue (cited no-op). player.awardStat(ITEM_USED): stat (cited no-op).
	// returned = stack.consumeAndReturn(1, player); if (be.isEmpty()) be.setTheItem(returned) else existing.grow(1).
	if d.isEmpty() {
		one := held
		one.Count = 1
		d.item = one
	} else {
		existing.Count++
		d.item = existing
	}
	if p.gameMode != gameModeCreative {
		t.shrinkHeldItem(p, inv) // stack.consumeAndReturn(1, player): shrink the held stack by 1 + sync.
	}
	// playSound(DECORATED_POT_INSERT) + sendParticles(DUST_PLUME): client cues (cited no-ops).
	// be.setChanged() + level.gameEvent(BLOCK_CHANGE): BE-data sync / vibration (cited -- in-memory BE).
	return true // SUCCESS -- the pot consumed the interaction.
}

// decoratedPotAnalogOutputSignal ports DecoratedPotBlock.getAnalogOutputSignal (hasAnalogOutputSignal ==
// true): AbstractContainerMenu.getRedstoneSignalFromBlockEntity over the single slot. Returns (signal,
// true) for a decorated pot, (0, false) otherwise (so the comparator falls to its container/super path).
// CITE: DecoratedPotBlock.getAnalogOutputSignal.
func (t *TickLoop) decoratedPotAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsDecoratedPot(state) {
		return 0, false
	}
	d := t.resolveDecoratedPot(pos)
	if d == nil {
		return 0, true
	}
	return decoratedPotComparator(d.item), true
}
