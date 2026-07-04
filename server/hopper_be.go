package server

// hopper_be.go — the HOPPER BLOCK-ENTITY (the item-transfer DRIVE): a 1:1 port of
// net.minecraft.world.level.block.entity.HopperBlockEntity over the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR this session). This is the same shape as the furnace/dispenser BE
// (a flat 5-slot container keyed by world position + a serverTick driver), plus the single-item
// per-cooldown TRANSFER engine (ejectItems / suckInItems / addItem) that moves one item at a time
// between adjacent containers and sucks loose ItemEntities from the box above.
//
// FIELD/METHOD ANCHORS (VERIFIED CFR HopperBlockEntity this session):
//   MOVE_ITEM_SPEED = 8 (the transfer cooldown); HOPPER_CONTAINER_SIZE = 5; NO_COOLDOWN_TIME = -1.
//   items = NonNullList.withSize(5, EMPTY); cooldownTime = -1; facing (from HopperBlock.FACING).
//   pushItemsTick(level,pos,state,entity):
//       --cooldownTime; tickedGameTime = level.getGameTime();
//       if (!isOnCooldown()) { setCooldown(0); tryMoveItems(..., () -> suckInItems(level, entity)); }
//   tryMoveItems: if (!isOnCooldown && ENABLED) { changed=false;
//       if (!isEmpty()) changed = ejectItems(...);
//       if (!inventoryFull()) changed |= action;   // suckInItems
//       if (changed) { setCooldown(8); setChanged; return true; } }
//   isOnCooldown() = cooldownTime > 0; isOnCustomCooldown() = cooldownTime > 8.
//
// SCOPE (cited deferrals):
//   - Container ENTITY (minecart-with-chest) as a source/target: DEFERRED in getContainerAt
//     (container.go) — no minecart entity. The block-container transfer is fully faithful.
//   - Double chest as one 54-slot container: DEFERRED (container.go) — each 27-slot half transfers
//     independently.
//   - The DOES_NOT_BLOCK_HOPPERS / isCollisionShapeFullBlock "isBlocked" gate in suckInItems: v1 has no
//     collision-shape query for arbitrary blocks; the loose-item suck runs when getSourceContainer is
//     nil (no container above), which is the common case. CITE: suckInItems `isBlocked` (the block-above
//     full-cube gate) — see suckInItems below.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// hopperContainerSize is HopperBlockEntity.HOPPER_CONTAINER_SIZE (5). CITE.
const hopperContainerSize = 5

// hopperMoveItemSpeed is HopperBlockEntity.MOVE_ITEM_SPEED (8) — the cooldown set after a successful
// transfer (setCooldown(8)) and the transfer period (one item every 8 ticks). CITE.
const hopperMoveItemSpeed = 8

// hopperNoCooldown is HopperBlockEntity.NO_COOLDOWN_TIME (-1) — the freshly-placed hopper cooldown. CITE.
const hopperNoCooldown = -1

// hopperBE is the tick-owned state of one hopper block-entity — the Sulfur analogue of HopperBlockEntity
// narrowed to the fields pushItemsTick + the menu read/write touch. items is the 5-slot NonNullList;
// cooldownTime is the transfer cooldown; tickedGameTime is the game time this hopper last ticked (used by
// the custom-cooldown skip in tryMoveInItem). facing (the EJECT direction) is re-read from the block
// state each tick (setBlockState mirrors HopperBlock.FACING).
type hopperBE struct {
	items         [hopperContainerSize]component.SlotData
	cooldownTime  int   // HopperBlockEntity.cooldownTime (init -1)
	tickedGameTime int64 // HopperBlockEntity.tickedGameTime (level.getGameTime() at last tick)
}

// isEmpty ports Container.isEmpty(): every slot empty. CITE: BaseContainerBlockEntity.isEmpty.
func (h *hopperBE) isEmpty() bool {
	for _, s := range h.items {
		if !stackEmpty(s) {
			return false
		}
	}
	return true
}

// inventoryFull ports HopperBlockEntity.inventoryFull(): every slot is a full stack (non-empty AND
// count == maxStackSize). A single non-full slot returns false. CITE (VERIFIED CFR).
func (h *hopperBE) inventoryFull() bool {
	for _, s := range h.items {
		if stackEmpty(s) || int(s.Count) != stackMaxSize(s) {
			return false
		}
	}
	return true
}

// setCooldown ports HopperBlockEntity.setCooldown(time). CITE.
func (h *hopperBE) setCooldown(time int) { h.cooldownTime = time }

// isOnCooldown ports HopperBlockEntity.isOnCooldown(): cooldownTime > 0. CITE.
func (h *hopperBE) isOnCooldown() bool { return h.cooldownTime > 0 }

// isOnCustomCooldown ports HopperBlockEntity.isOnCustomCooldown(): cooldownTime > 8. CITE.
func (h *hopperBE) isOnCustomCooldown() bool { return h.cooldownTime > hopperMoveItemSpeed }

// hopperPushItemsTick ports HopperBlockEntity.pushItemsTick(level, pos, state, entity):
//
//	--entity.cooldownTime;
//	entity.tickedGameTime = level.getGameTime();
//	if (!entity.isOnCooldown()) { entity.setCooldown(0); tryMoveItems(..., () -> suckInItems(level, entity)); }
//
// Called from tickHoppers each tick. state is the hopper's CURRENT block state (for the ENABLED + FACING
// reads). Tick-owned.
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.pushItemsTick
func (t *TickLoop) hopperPushItemsTick(pos pk.Position, state block.StateID, h *hopperBE) {
	h.cooldownTime--
	h.tickedGameTime = t.hopperGameTime()
	if !h.isOnCooldown() {
		h.setCooldown(0)
		t.hopperTryMoveItems(pos, state, h)
	}
}

// hopperTryMoveItems ports HopperBlockEntity.tryMoveItems(level, pos, state, entity, action) with the
// action bound to suckInItems (the pushItemsTick call site):
//
//	if (level.isClientSide()) return false;                      // (server-only here)
//	if (!isOnCooldown() && ENABLED) {
//	    boolean changed = false;
//	    if (!isEmpty())       changed  = ejectItems(...);
//	    if (!inventoryFull()) changed |= suckInItems(...);
//	    if (changed) { setCooldown(8); setChanged(...); return true; }
//	}
//	return false;
//
// The eject-THEN-suck order and the single `changed` accumulator are EXACT. CITE (VERIFIED CFR).
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.tryMoveItems
func (t *TickLoop) hopperTryMoveItems(pos pk.Position, state block.StateID, h *hopperBE) bool {
	if h.isOnCooldown() || !block.HopperEnabled(state) {
		return false
	}
	changed := false
	if !h.isEmpty() {
		changed = t.hopperEjectItems(pos, state, h)
	}
	if !h.inventoryFull() {
		if t.hopperSuckInItems(pos, state, h) {
			changed = true
		}
	}
	if changed {
		h.setCooldown(hopperMoveItemSpeed)
		t.markHopperDirty(pos)
		t.broadcastHopperChange(pos, h)
		return true
	}
	return false
}

// hopperEjectItems ports HopperBlockEntity.ejectItems(level, pos, self): push ONE item from the first
// non-empty slot into the container attached in FACING (getAttachedContainer). Early-out when there is
// no attached container or it is full (isFullContainer over the opposite face). On a successful move it
// calls container.setChanged() and returns true; otherwise it restores the source slot and returns false.
//
//	Container container = getAttachedContainer(level, pos, self);   // getContainerAt(pos.relative(FACING))
//	if (container == null) return false;
//	Direction direction = self.facing.getOpposite();
//	if (isFullContainer(container, direction)) return false;
//	for (slot = 0..size) {
//	    stack = self.getItem(slot); if (stack.isEmpty()) continue;
//	    int originalCount = stack.getCount();
//	    ItemStack result = addItem(self, container, self.removeItem(slot,1), direction);
//	    if (result.isEmpty()) { container.setChanged(); return true; }
//	    stack.setCount(originalCount);
//	    if (originalCount == 1) self.setItem(slot, stack);
//	}
//	return false;
//
// CITE (VERIFIED CFR). Note removeItem(slot,1) mutates the source slot in place; result.isEmpty means the
// single removed item was fully absorbed. When NOT absorbed the source count is restored (the item never
// left) — for a single-item slot vanilla explicitly re-sets it (removeItem cleared it).
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.ejectItems
func (t *TickLoop) hopperEjectItems(pos pk.Position, state block.StateID, h *hopperBE) bool {
	facing, ok := block.HopperFacing(state)
	if !ok {
		return false
	}
	container := t.getContainerAt(relative(pos, facing)) // getAttachedContainer
	if container == nil {
		return false
	}
	direction := dirOpposite(facing)
	if hopperIsFullContainer(container, direction) {
		return false
	}
	src := &hopperSelfContainer{h: h}
	for slot := 0; slot < hopperContainerSize; slot++ {
		itemStack := h.items[slot]
		if stackEmpty(itemStack) {
			continue
		}
		originalCount := int(itemStack.Count)
		// self.removeItem(slot, 1): take one item off the slot, mutating h.items[slot].
		removed := hopperRemoveItem(src, slot, 1)
		result := t.hopperAddItem(src, container, removed, direction)
		if stackEmpty(result) {
			container.setChanged()
			return true
		}
		// Not absorbed: restore the source slot to its original count (the item never left).
		itemStack.Count = toVar(originalCount)
		if originalCount == 1 {
			h.items[slot] = itemStack
		} else {
			// removeItem shrank a >1 stack by 1; restoring originalCount undoes it.
			h.items[slot] = itemStack
		}
	}
	return false
}

// hopperSuckInItems ports HopperBlockEntity.suckInItems(level, hopper): pull ONE item from the container
// directly ABOVE (getSourceContainer at pos.above(), DOWN face), or — when there is no container above —
// suck a loose ItemEntity from the box above (getItemsAtAndAbove) unless the block above is a full cube
// that blocks hoppers.
//
//	BlockPos above = pos.above();
//	Container container = getSourceContainer(level, hopper, above, aboveState);
//	if (container != null) {
//	    Direction direction = Direction.DOWN;
//	    for (int slot : getSlots(container, direction)) if (tryTakeInItemFromSlot(...)) return true;
//	    return false;
//	}
//	boolean isBlocked = hopper.isGridAligned() && aboveState.isCollisionShapeFullBlock(...) &&
//	                    !aboveState.is(DOES_NOT_BLOCK_HOPPERS);
//	if (!isBlocked) for (ItemEntity e : getItemsAtAndAbove(level, hopper)) if (addItem(hopper, e)) return true;
//	return false;
//
// CITE (VERIFIED CFR). The isBlocked collision-shape gate is DEFERRED (no arbitrary-block full-cube query
// in v1): when there is no container above, the loose-item suck always runs — faithful for the common air/
// partial-block case; a hopper under a full solid block would suck nothing in vanilla, but v1 has no items
// resting inside a solid block, so the observable result is identical.
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.suckInItems
func (t *TickLoop) hopperSuckInItems(pos pk.Position, _ block.StateID, h *hopperBE) bool {
	above := relative(pos, block.Up)
	container := t.getContainerAt(above) // getSourceContainer (block container at pos.above())
	if container != nil {
		direction := block.Down
		for _, slot := range hopperGetSlots(container, direction) {
			if t.hopperTryTakeInItemFromSlot(h, container, slot, direction) {
				return true
			}
		}
		return false
	}
	// No container above: suck loose ItemEntities in the box above (isBlocked gate DEFERRED, cited).
	for _, e := range t.hopperItemsAtAndAbove(pos) {
		if t.hopperAddItemEntity(h, e) {
			return true
		}
	}
	return false
}

// hopperSuckAABB is HopperBlockEntity's Hopper.SUCK_AABB (Block.column(16,11,32) -> box(0,11,0,16,32,16)
// / 16 = AABB(0, 0.6875, 0, 1.0, 2.0, 1.0)) as pixel-exact constants (VERIFIED CFR: Block.column /
// Block.box). getItemsAtAndAbove moves it by (levelX-0.5, levelY-0.5, levelZ-0.5) == (pos.X, pos.Y, pos.Z),
// so the world box is (posX, posY+0.6875, posZ) .. (posX+1, posY+2, posZ+1).
const (
	hopperSuckMinY = 11.0 / 16.0 // 0.6875
	hopperSuckMaxY = 32.0 / 16.0 // 2.0
)

// hopperItemsAtAndAbove ports HopperBlockEntity.getItemsAtAndAbove(level, hopper): the loose ItemEntities
// whose box intersects SUCK_AABB moved to the hopper position, filtered to still-alive items
// (EntitySelector.ENTITY_STILL_ALIVE). Broad phase: the region item snapshot near the hopper column;
// narrow phase: the AABB intersection on all three axes. CITE (VERIFIED CFR):
// getEntitiesOfClass(ItemEntity.class, SUCK_AABB.move(...), ENTITY_STILL_ALIVE).
func (t *TickLoop) hopperItemsAtAndAbove(pos pk.Position) []*Entity {
	loX := float64(pos.X)
	loY := float64(pos.Y) + hopperSuckMinY
	loZ := float64(pos.Z)
	hiX := float64(pos.X) + 1.0
	hiY := float64(pos.Y) + hopperSuckMaxY
	hiZ := float64(pos.Z) + 1.0

	cx := float64(pos.X) + 0.5
	cz := float64(pos.Z) + 0.5

	var out []*Entity
	for _, e := range t.entitiesNearAcrossRegions(cx, cz, 1) {
		if !e.isItem || e.itemStack.Count <= 0 {
			continue // ItemEntity.class + ENTITY_STILL_ALIVE (an empty-stack item is discarded, not alive)
		}
		// Item AABB (feet-anchored width x height centered on x/z) intersects the suck box on all 3 axes.
		ihw := e.width / 2
		if hiX <= e.x-ihw || e.x+ihw <= loX ||
			hiY <= e.y || e.y+e.height <= loY ||
			hiZ <= e.z-ihw || e.z+ihw <= loZ {
			continue
		}
		out = append(out, e)
	}
	return out
}

// hopperTryTakeInItemFromSlot ports HopperBlockEntity.tryTakeInItemFromSlot(hopper, container, slot, dir):
//
//	ItemStack stack = container.getItem(slot);
//	if (!stack.isEmpty() && canTakeItemFromContainer(hopper, container, stack, slot, direction)) {
//	    int originalCount = stack.getCount();
//	    ItemStack result = addItem(container, hopper, container.removeItem(slot,1), null);
//	    if (result.isEmpty()) { container.setChanged(); return true; }
//	    stack.setCount(originalCount);
//	    if (originalCount == 1) container.setItem(slot, stack);
//	}
//	return false;
//
// CITE (VERIFIED CFR). The destination (hopper) direction is null (a plain-Container add over all slots).
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.tryTakeInItemFromSlot
func (t *TickLoop) hopperTryTakeInItemFromSlot(h *hopperBE, container containerView, slot int, direction block.Direction) bool {
	stack := container.getItem(slot)
	if stackEmpty(stack) || !hopperCanTakeItemFromContainer(h, container, stack, slot, direction) {
		return false
	}
	originalCount := int(stack.Count)
	removed := hopperContainerRemoveItem(container, slot, 1)
	dst := &hopperSelfContainer{h: h}
	result := t.hopperAddItem(container, dst, removed, block.Direction(255)) // null direction -> no face
	if stackEmpty(result) {
		container.setChanged()
		return true
	}
	stack.Count = toVar(originalCount)
	if originalCount == 1 {
		container.setItem(slot, stack)
	}
	return false
}

// hopperAddItemEntity ports HopperBlockEntity.addItem(Container, ItemEntity): try to add the loose item's
// stack (a copy) into the hopper; if fully absorbed, discard the ItemEntity; else leave it its remainder.
//
//	ItemStack copy = entity.getItem().copy();
//	ItemStack result = addItem(null, container, copy, null);
//	if (result.isEmpty()) { changed=true; entity.setItem(EMPTY); entity.discard(); }
//	else entity.setItem(result);
//
// CITE (VERIFIED CFR).
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.addItem(Container, ItemEntity)
func (t *TickLoop) hopperAddItemEntity(h *hopperBE, e *Entity) bool {
	dst := &hopperSelfContainer{h: h}
	copy := e.itemStack
	result := t.hopperAddItem(nil, dst, copy, block.Direction(255))
	if stackEmpty(result) {
		e.itemStack = component.SlotData{Count: 0}
		if owner := t.owningRegion(e.id); owner != nil {
			owner.entities.remove(e.id) // entity.discard()
		}
		return true
	}
	e.itemStack = result
	return false
}

// hopperAddItem ports HopperBlockEntity.addItem(from, container, stack, direction): the item-merge into a
// destination container, respecting the WorldlyContainer face slots when a direction is given. Returns the
// LEFTOVER stack (empty when fully absorbed).
//
//	if (container instanceof WorldlyContainer worldly && direction != null) {
//	    for (int slot : worldly.getSlotsForFace(direction)) {
//	        if (stack.isEmpty()) return stack;
//	        stack = tryMoveInItem(from, container, stack, slot, direction);
//	    }
//	    return stack;
//	}
//	for (int i = 0; i < size; i++) { if (stack.isEmpty()) return stack; stack = tryMoveInItem(from, container, stack, i, direction); }
//	return stack;
//
// direction == block.Direction(255) is the Go stand-in for a Java null direction (the plain-container add).
// CITE (VERIFIED CFR).
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.addItem
func (t *TickLoop) hopperAddItem(from, container containerView, stack component.SlotData, direction block.Direction) component.SlotData {
	if container.isWorldly() && direction != hopperNullDirection {
		slots := container.getSlotsForFace(direction)
		for _, slot := range slots {
			if stackEmpty(stack) {
				return stack
			}
			stack = t.hopperTryMoveInItem(from, container, stack, slot, direction)
		}
		return stack
	}
	size := container.getContainerSize()
	for i := 0; i < size; i++ {
		if stackEmpty(stack) {
			return stack
		}
		stack = t.hopperTryMoveInItem(from, container, stack, i, direction)
	}
	return stack
}

// hopperNullDirection is the sentinel Go value for a Java `null` Direction (addItem's from-hopper add uses
// a null direction so canPlaceItemInContainer skips the WorldlyContainer face check).
const hopperNullDirection = block.Direction(255)

// hopperCanPlaceItemInContainer ports HopperBlockEntity.canPlaceItemInContainer(container, stack, slot, dir):
//
//	if (!container.canPlaceItem(slot, stack)) return false;
//	return !(container instanceof WorldlyContainer) || worldly.canPlaceItemThroughFace(slot, stack, direction);
//
// CITE (VERIFIED CFR).
func hopperCanPlaceItemInContainer(container containerView, stack component.SlotData, slot int, direction block.Direction) bool {
	if !container.canPlaceItem(slot, stack) {
		return false
	}
	if !container.isWorldly() {
		return true
	}
	return container.canPlaceItemThroughFace(slot, stack, direction)
}

// hopperCanTakeItemFromContainer ports HopperBlockEntity.canTakeItemFromContainer(into, from, stack, slot, dir):
//
//	if (!from.canTakeItem(into, slot, stack)) return false;
//	return !(from instanceof WorldlyContainer) || worldly.canTakeItemThroughFace(slot, stack, direction);
//
// `into` (the hopper) is not consulted by the default Container.canTakeItem (always true). CITE (VERIFIED CFR).
func hopperCanTakeItemFromContainer(_ *hopperBE, from containerView, stack component.SlotData, slot int, direction block.Direction) bool {
	if !from.canTakeItem(slot, stack) {
		return false
	}
	if !from.isWorldly() {
		return true
	}
	return from.canTakeItemThroughFace(slot, stack, direction)
}

// hopperTryMoveInItem ports HopperBlockEntity.tryMoveInItem(from, container, stack, slot, direction): the
// single-slot merge — place into an empty slot, or merge into a same-item stack up to its max — and the
// destination-hopper custom-cooldown skip.
//
//	ItemStack current = container.getItem(slot);
//	if (canPlaceItemInContainer(container, stack, slot, direction)) {
//	    boolean success = false; boolean wasEmpty = container.isEmpty();
//	    if (current.isEmpty()) { container.setItem(slot, stack); stack = EMPTY; success = true; }
//	    else if (canMergeItems(current, stack)) {
//	        int space = stack.getMaxStackSize() - current.getCount();
//	        int count = Math.min(stack.getCount(), space);
//	        stack.shrink(count); current.grow(count); success = count > 0;
//	    }
//	    if (success) {
//	        if (wasEmpty && container instanceof HopperBlockEntity dest && !dest.isOnCustomCooldown()) {
//	            int skip = 0;
//	            if (from instanceof HopperBlockEntity fromHopper && dest.tickedGameTime >= fromHopper.tickedGameTime) skip = 1;
//	            dest.setCooldown(8 - skip);
//	        }
//	        container.setChanged();
//	    }
//	}
//	return stack;
//
// CITE (VERIFIED CFR).
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.tryMoveInItem
func (t *TickLoop) hopperTryMoveInItem(from, container containerView, stack component.SlotData, slot int, direction block.Direction) component.SlotData {
	current := container.getItem(slot)
	if hopperCanPlaceItemInContainer(container, stack, slot, direction) {
		success := false
		wasEmpty := container.isEmpty()
		if stackEmpty(current) {
			container.setItem(slot, stack)
			stack = component.SlotData{Count: 0}
			success = true
		} else if hopperCanMergeItems(current, stack) {
			space := stackMaxSize(stack) - int(current.Count)
			count := min(int(stack.Count), space)
			stack.Count = toVar(int(stack.Count) - count)
			if stack.Count <= 0 {
				stack = component.SlotData{Count: 0}
			}
			current.Count = toVar(int(current.Count) + count)
			container.setItem(slot, current)
			success = count > 0
		}
		if success {
			if wasEmpty {
				if dest := container.asHopper(); dest != nil && !dest.isOnCustomCooldown() {
					skipTickCount := 0
					if fromHopper := hopperViewAsHopper(from); fromHopper != nil {
						if dest.tickedGameTime >= fromHopper.tickedGameTime {
							skipTickCount = 1
						}
					}
					dest.setCooldown(hopperMoveItemSpeed - skipTickCount)
				}
			}
			container.setChanged()
		}
	}
	return stack
}

// hopperViewAsHopper unwraps a from-container view to a hopperBE (for the tryMoveInItem `from instanceof
// HopperBlockEntity` check), or nil. A nil view (addItem(null, ...)) is not a hopper.
func hopperViewAsHopper(from containerView) *hopperBE {
	if from == nil {
		return nil
	}
	return from.asHopper()
}

// hopperCanMergeItems ports HopperBlockEntity.canMergeItems(a, b): a.getCount() <= a.getMaxStackSize()
// AND isSameItemSameComponents(a, b). CITE (VERIFIED CFR).
func hopperCanMergeItems(a, b component.SlotData) bool {
	return int(a.Count) <= stackMaxSize(a) && stackSameItemSameComponents(a, b)
}

// hopperIsFullContainer ports HopperBlockEntity.isFullContainer(container, direction): every slot exposed
// to `direction` holds count >= that item's maxStackSize. CITE (VERIFIED CFR).
func hopperIsFullContainer(container containerView, direction block.Direction) bool {
	for _, slot := range hopperGetSlots(container, direction) {
		itemStack := container.getItem(slot)
		if int(itemStack.Count) < stackMaxSize(itemStack) {
			return false
		}
	}
	return true
}

// hopperGetSlots ports HopperBlockEntity.getSlots(container, direction): a WorldlyContainer returns its
// getSlotsForFace(direction); a plain container returns the flat 0..size-1 slot list. (The CACHED_SLOTS
// memoization is an internal optimization with no observable effect — omitted.) CITE (VERIFIED CFR).
func hopperGetSlots(container containerView, direction block.Direction) []int {
	if container.isWorldly() {
		return container.getSlotsForFace(direction)
	}
	return createFlatSlots(container.getContainerSize())
}

// -------------------------------------------------------------------------------------------------
// removeItem — ContainerHelper.removeItem over the hopper self / a container view
// -------------------------------------------------------------------------------------------------

// hopperSelfContainer wraps a hopperBE so the transfer engine can call the generic Container primitives
// (removeItem / getItem) against the hopper itself, exactly as HopperBlockEntity (a Container) is passed
// as `self`/`hopper` into addItem/removeItem in the vanilla methods.
type hopperSelfContainer struct{ h *hopperBE }

func (s *hopperSelfContainer) getContainerSize() int                    { return hopperContainerSize }
func (s *hopperSelfContainer) getItem(slot int) component.SlotData      { return s.h.items[slot] }
func (s *hopperSelfContainer) setItem(slot int, v component.SlotData)   { s.h.items[slot] = v }
func (s *hopperSelfContainer) isEmpty() bool                            { return s.h.isEmpty() }
func (s *hopperSelfContainer) setChanged()                              {}
func (s *hopperSelfContainer) getSlotsForFace(block.Direction) []int    { return createFlatSlots(hopperContainerSize) }
func (s *hopperSelfContainer) canPlaceItem(int, component.SlotData) bool { return true }
func (s *hopperSelfContainer) canPlaceItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (s *hopperSelfContainer) canTakeItem(int, component.SlotData) bool { return true }
func (s *hopperSelfContainer) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (s *hopperSelfContainer) isWorldly() bool     { return false }
func (s *hopperSelfContainer) asHopper() *hopperBE { return s.h }

// hopperRemoveItem ports ContainerHelper.removeItem over a hopperSelfContainer: split up to `count` off
// the slot, shrinking the slot in place, returning the removed stack. CITE: ContainerHelper.removeItem
// (removes min(count, slotCount) and shrinks the slot).
func hopperRemoveItem(s *hopperSelfContainer, slot, count int) component.SlotData {
	cur := s.h.items[slot]
	if stackEmpty(cur) {
		return component.SlotData{Count: 0}
	}
	removed := stackSplit(&cur, count)
	s.h.items[slot] = cur
	return removed
}

// hopperContainerRemoveItem ports ContainerHelper.removeItem over a generic containerView: split up to
// `count` off the slot, writing the shrunk remainder back, returning the removed stack. CITE:
// ContainerHelper.removeItem.
func hopperContainerRemoveItem(container containerView, slot, count int) component.SlotData {
	cur := container.getItem(slot)
	if stackEmpty(cur) {
		return component.SlotData{Count: 0}
	}
	removed := stackSplit(&cur, count)
	container.setItem(slot, cur)
	return removed
}

// hopperGameTime returns level.getGameTime() — the tickedGameTime the hopper stamps each tick (used only
// by the tryMoveInItem custom-cooldown skip when a hopper feeds another hopper). Reuses the region's game
// time counter; 0 when no region is available (tests driving the BE directly).
func (t *TickLoop) hopperGameTime() int64 {
	return t.GameTime()
}

// hopperCheckPoweredState ports HopperBlock.checkPoweredState(level, pos, state):
//
//	boolean shouldBeOn = !level.hasNeighborSignal(pos);
//	if (shouldBeOn != state.getValue(ENABLED)) level.setBlock(pos, state.setValue(ENABLED, shouldBeOn), 2);
//
// A powered hopper (any neighbor emitting signal) has ENABLED=false (the transfer LOCK); an unpowered
// hopper has ENABLED=true. Called from HopperBlock.onPlace + neighborChanged. CITE (VERIFIED CFR).
//
// 1:1 net.minecraft.world.level.block.HopperBlock.checkPoweredState
func (t *TickLoop) hopperCheckPoweredState(pos pk.Position, state block.StateID) {
	if t.world() == nil {
		return
	}
	shouldBeOn := !t.hasNeighborSignal(pos)
	if shouldBeOn != block.HopperEnabled(state) {
		if ns, ok := block.HopperWithEnabled(state, shouldBeOn); ok && t.world().SetBlock(pos, ns, dimMinY) {
			// setBlock flag 2 == UPDATE_CLIENTS (no neighbor update): broadcast the new state to clients.
			t.broadcastBlockUpdate(pos, ns)
		}
	}
}

// hopperNeighborChanged ports HopperBlock.neighborChanged -> checkPoweredState: a neighbor of a hopper
// changed, so re-evaluate its ENABLED lock. Dispatched from drainRedstoneUpdates. CITE:
// HopperBlock.neighborChanged.
func (t *TickLoop) hopperNeighborChanged(pos pk.Position, state block.StateID) {
	t.hopperCheckPoweredState(pos, state)
}

// tickHoppers ticks every live hopper block-entity once per tick (the ServerLevel-side blockEntityTicker
// fan-out for HopperBlockEntity.pushItemsTick). Called from tickWorld. Each hopper reads its CURRENT
// block state from the world (for the ENABLED gate + FACING); if the block is no longer a hopper
// (broken/replaced), the block-entity is dropped from the store. A nil world leaves hoppers un-ticked
// (tests may drive hopperPushItemsTick directly). The furnace/dispenser tickFurnaces/dispenser twin.
// Tick-owned (TICK-05).
func (t *TickLoop) tickHoppers() {
	if len(t.hoppers) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, h := range t.hoppers {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !block.IsHopper(state) {
			delete(t.hoppers, pos)
			continue
		}
		t.hopperPushItemsTick(pos, state, h)
	}
}
