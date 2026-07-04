package server

// dispenser_be.go — the DISPENSER / DROPPER BLOCK-ENTITY (redstone tier-4): the 9-slot container the
// dispense drive shoots from, a 1:1 port of net.minecraft.world.level.block.entity.DispenserBlockEntity
// (+ DropperBlockEntity) over the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this
// session). This is the "SAME shape as the furnace BE" the task calls for: a flat container keyed by
// world position (t.dispensers, the t.furnaces twin), a 3x3 menu (dispenser_menu.go), and the FACING
// ejection logic (dispenser.go).
//
// FIELD/METHOD ANCHORS (VERIFIED CFR DispenserBlockEntity this session):
//   CONTAINER_SIZE = 9; items = NonNullList.withSize(9, EMPTY).
//   getRandomSlot(RandomSource random):
//       unpackLootTable(null);                                  // (v1: no loot-table on a placed dispenser)
//       int replaceSlot = -1; int replaceOdds = 1;
//       for (i = 0..items.size()) {
//           if (items.get(i).isEmpty() || random.nextInt(replaceOdds++) != 0) continue;
//           replaceSlot = i;
//       }
//       return replaceSlot;
//   getItem(slot) / setItem(slot, stack) — the plain Container accessors.
//   insertItem(ItemStack) — the dropper's "eject into a container in front" merge (used only by the
//       HopperBlockEntity.addItem path, which is DEFERRED, see dispenser.go).
//
// SCOPE (cited deferrals):
//   - The DispenseItemBehavior REGISTRY (spawn eggs / buckets / TNT / fireworks / armor / shears /
//     bonemeal / etc.): DEFERRED. The DEFAULT projectile-drop behavior (the common case) is REAL + 1:1
//     (dispenser.go dispenseDefaultBehavior). CITE: DispenserBlock.DISPENSER_REGISTRY (an IdentityHashMap
//     populated by DispenseItemBehavior.bootStrap, not ported).
//   - loot-table containers (RandomizableContainerBlockEntity.unpackLootTable): a placed dispenser/dropper
//     carries no loot table (only structure-placed ones do, and v1 places none), so getRandomSlot's
//     unpackLootTable(null) is a no-op — cited, matching the empty-container placed case.
//   - BE persistence matches the FURNACE level (real via the shared saveAllItems seam): see
//     dispenser_persist.go (the furnace-BE twin). CITE: DispenserBlockEntity.saveAdditional/loadAdditional.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// dispenserContainerSize is DispenserBlockEntity.CONTAINER_SIZE (9) — the 3x3 grid. CITE:
// DispenserBlockEntity.CONTAINER_SIZE.
const dispenserContainerSize = 9

// dispenserBE is the tick-owned state of one dispenser/dropper block-entity — the Sulfur analogue of
// DispenserBlockEntity narrowed to the 9-slot container the dispense drive + menu read/write. isDropper
// discriminates the DropperBlockEntity subclass (which uses the plain default behavior + the eject-into-
// container-in-front dispenseFrom override; see dispenser.go). items is the NonNullList<ItemStack>.
type dispenserBE struct {
	items     [dispenserContainerSize]component.SlotData
	isDropper bool // true for a DropperBlockEntity (minecraft:dropper), false for a DispenserBlockEntity
}

// dispenserGetRandomSlot ports DispenserBlockEntity.getRandomSlot(RandomSource): a reservoir-sampling
// pick of a uniformly-random NON-EMPTY slot, or -1 when every slot is empty. The RNG draw order is
// EXACT: for each slot i, if it is non-empty the loop draws random.nextInt(replaceOdds) with
// replaceOdds POST-INCREMENTED, and selects i when that draw is 0. An EMPTY slot short-circuits
// (`isEmpty() || ...`) and draws NOTHING (replaceOdds is NOT incremented for empty slots). The
// unpackLootTable(null) is a no-op in v1 (a placed dispenser carries no loot table — cited deferral).
//
// 1:1 net.minecraft.world.level.block.entity.DispenserBlockEntity.getRandomSlot
func (d *dispenserBE) getRandomSlot(random interface{ NextIntN(int32) int32 }) int {
	replaceSlot := -1
	replaceOdds := int32(1)
	for i := 0; i < len(d.items); i++ {
		if stackEmpty(d.items[i]) {
			continue // items.get(i).isEmpty() -> `||` short-circuits, replaceOdds NOT incremented, no draw
		}
		// random.nextInt(replaceOdds++) != 0 -> continue; else replaceSlot = i. The post-increment
		// advances replaceOdds AFTER the draw uses the current value (Java `replaceOdds++`).
		draw := random.NextIntN(replaceOdds)
		replaceOdds++
		if draw != 0 {
			continue
		}
		replaceSlot = i
	}
	return replaceSlot
}

// dispenserInsertItem ports DispenserBlockEntity.insertItem(ItemStack): merge the stack into the
// container's existing same-item slots (up to each slot's max stack size), placing into empty slots
// too, and return the LEFTOVER stack (empty if fully absorbed). Used by the dropper's eject-into-
// container path (DEFERRED — no Hopper container in front yet), and by the DEFAULT behavior's
// overflow re-insert (addToInventoryOrDispense). Ported for completeness so the seam is real when
// Hopper lands. CITE: DispenserBlockEntity.insertItem.
func (d *dispenserBE) dispenserInsertItem(stack component.SlotData) component.SlotData {
	maxStackSize := stackMaxSize(stack)
	for i := 0; i < len(d.items) && stack.Count > 0; i++ {
		target := d.items[i]
		// if (!target.isEmpty() && !isSameItemSameComponents(itemStack, target)) continue;
		if !stackEmpty(target) && !stackSameItemSameComponents(stack, target) {
			continue
		}
		transfer := min(int(stack.Count), maxStackSize-int(target.Count))
		if transfer > 0 {
			if stackEmpty(target) {
				// setItem(i, itemStack.split(transfer)).
				d.items[i] = stackSplit(&stack, transfer)
			} else {
				// itemStack.shrink(transfer); target.grow(transfer).
				stack.Count = toVar(int(stack.Count) - transfer)
				target.Count = toVar(int(target.Count) + transfer)
				d.items[i] = target
			}
		}
	}
	if stack.Count <= 0 {
		return component.SlotData{Count: 0}
	}
	return stack
}

// resolveDispenser returns the tick-owned dispenserBE for pos, creating an EMPTY one (with the block's
// isDropper flag) on first access — the analogue of a freshly-placed DispenserBlockEntity's empty
// container. Tries to restore persisted state from the chunk's BlockEntity list first (the furnace twin
// of resolveFurnace→loadDispenserBE). Returns nil only when pos is not a dispenser-family block (or the
// world is unloaded). Tick-owned (t.dispensers, the t.furnaces twin).
func (t *TickLoop) resolveDispenser(pos pk.Position, state block.StateID) *dispenserBE {
	if t.dispensers == nil {
		t.dispensers = make(map[pk.Position]*dispenserBE)
	}
	if d, ok := t.dispensers[pos]; ok {
		return d // already live
	}
	if !block.IsDispenserFamily(state) {
		return nil
	}
	isDropper := block.IsDropper(state)
	d := t.loadDispenserBE(pos, isDropper)
	if d == nil {
		d = &dispenserBE{isDropper: isDropper}
	}
	t.dispensers[pos] = d
	return d
}

// tickBlockDispenserGuard is the ServerLevel.tickBlock `state.is(block)` stale-tick guard for a
// dispenser/dropper scheduled tick: only run the dispense if the block at pos is still a dispenser-
// family block. A dispenser broken/replaced since the TRIGGERED tick was scheduled fires nothing.
// CITE: ServerLevel.tickBlock.
func (t *TickLoop) tickBlockDispenserGuard(state block.StateID) bool {
	return block.IsDispenserFamily(state)
}
