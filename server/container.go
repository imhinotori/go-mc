package server

// container.go — the shared Container SEAM for the HOPPER transfer + the dropper eject-in-front + the
// comparator container-analog output. A 1:1 port of the net.minecraft.world.Container /
// net.minecraft.world.WorldlyContainer abstraction the hopper (HopperBlockEntity.getContainerAt /
// addItem / suckInItems / ejectItems) and the dropper (DropperBlock.dispenseFrom) resolve at a world
// position, over the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this session).
//
// Ender already carries five container block-entities (chest / furnace / dispenser+dropper /
// brewing-stand / hopper), each in its OWN tick-owned store keyed by world position. Vanilla resolves
// them uniformly through the Container interface; this file re-expresses that interface in Go
// (containerView) and adapts each concrete BE to it so getContainerAt returns ONE type the hopper /
// comparator can drive without knowing the block behind it.
//
// 1:1 ANCHORS (VERIFIED CFR this session):
//
//	net.minecraft.world.Container:
//	    getContainerSize / getItem(slot) / setItem(slot,stack) / isEmpty / setChanged /
//	    getMaxStackSize(itemStack) = min(getMaxStackSize()=99, itemStack.getMaxStackSize()) /
//	    canPlaceItem(slot,stack)=true (default) / canTakeItem(into,slot,stack)=true (default).
//	net.minecraft.world.WorldlyContainer (furnace + brewing stand):
//	    getSlotsForFace(dir) / canPlaceItemThroughFace(slot,stack,dir) /
//	    canTakeItemThroughFace(slot,stack,dir).
//	net.minecraft.world.level.block.entity.HopperBlockEntity.getBlockContainer(level,pos,state):
//	    WorldlyContainerHolder? -> getContainer; else hasBlockEntity && BlockEntity instanceof Container
//	    -> that container (ChestBlockEntity+ChestBlock -> ChestBlock.getContainer double-chest combine).
//
// SCOPE (cited deferrals):
//   - Container ENTITY (minecart / chest-boat): HopperBlockEntity.getEntityContainer /
//     EntitySelector.CONTAINER_ENTITY_SELECTOR. Ender has no minecart-with-chest entity, so
//     getContainerAt resolves ONLY the block container (getBlockContainer). CITE:
//     HopperBlockEntity.getEntityContainer (the `result == null` fallback). No minecart => faithful nil.
//   - DOUBLE CHEST as one 54-slot container: ChestBlock.getContainer(...).combine(CHEST_COMBINER). The
//     chestLoot store is a single-27 container per block; getContainerAt returns the SINGLE half at pos
//     (each half is a valid 27-slot Container). Cited allowed deferral — a hopper under/into one half of
//     a double chest still transfers correctly against that half. CITE: ChestBlock.getContainer combine.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// containerView is the Sulfur analogue of net.minecraft.world.Container augmented with the
// WorldlyContainer face methods (a plain container returns the flat-slots / always-true defaults). The
// hopper transfer (addItem / suckInItems / ejectItems) and the comparator container-analog output drive
// EVERY container block-entity through this one interface.
type containerView interface {
	// getContainerSize is Container.getContainerSize().
	getContainerSize() int
	// getItem is Container.getItem(slot).
	getItem(slot int) component.SlotData
	// setItem is Container.setItem(slot, stack).
	setItem(slot int, stack component.SlotData)
	// isEmpty is Container.isEmpty(): every slot empty.
	isEmpty() bool
	// setChanged is Container.setChanged(): marks the backing BE dirty for save + re-broadcasts any
	// open viewer window (the observable equivalent of BlockEntity.setChanged).
	setChanged()

	// getSlotsForFace is WorldlyContainer.getSlotsForFace(direction). A plain Container returns the
	// flat 0..size-1 slot list (HopperBlockEntity.createFlatSlots).
	getSlotsForFace(direction block.Direction) []int
	// canPlaceItem is Container.canPlaceItem(slot, stack) (default true; furnace/brewing override).
	canPlaceItem(slot int, stack component.SlotData) bool
	// canPlaceItemThroughFace is WorldlyContainer.canPlaceItemThroughFace(slot, stack, dir). A plain
	// container has no face restriction (the `!(container instanceof WorldlyContainer)` short-circuit
	// in canPlaceItemInContainer), so it returns true; only Worldly containers restrict.
	canPlaceItemThroughFace(slot int, stack component.SlotData, direction block.Direction) bool
	// canTakeItem is Container.canTakeItem(into, slot, stack) (default true).
	canTakeItem(slot int, stack component.SlotData) bool
	// canTakeItemThroughFace is WorldlyContainer.canTakeItemThroughFace(slot, stack, dir) (Worldly only).
	canTakeItemThroughFace(slot int, stack component.SlotData, direction block.Direction) bool

	// isWorldly reports whether this is a WorldlyContainer (drives the `container instanceof
	// WorldlyContainer` branches in addItem / getSlots / canPlaceItemInContainer / canTakeItemFromContainer).
	isWorldly() bool

	// asHopper returns the backing hopperBE if this view is a HopperBlockEntity (for the
	// tryMoveInItem custom-cooldown branch `container instanceof HopperBlockEntity`), else nil.
	asHopper() *hopperBE
}

// getRedstoneSignalFromContainer ports AbstractContainerMenu.getRedstoneSignalFromContainer(container):
//
//	if (container == null) return 0;
//	float f = 0.0f;
//	for (int slot = 0; slot < container.getContainerSize(); ++slot) {
//	    ItemStack stack = container.getItem(slot);
//	    if (!stack.isEmpty()) f += (float)stack.getCount() / (float)container.getMaxStackSize(stack);
//	}
//	f /= (float)container.getContainerSize();
//	return Mth.lerpDiscrete(f, 0, 15);
//
// The accumulation is FLOAT (float32) with the EXACT per-slot fraction (count / container.getMaxStackSize
// (stack) == min(99, item.getMaxStackSize())), then divided by container size, matching the vanilla float
// ops bit-for-bit. CITE (VERIFIED javap AbstractContainerMenu.getRedstoneSignalFromContainer): fconst_0;
// per-slot i2f count / i2f getMaxStackSize; fadd; fdiv by size; Mth.lerpDiscrete(f, 0, 15).
//
// 1:1 net.minecraft.world.inventory.AbstractContainerMenu.getRedstoneSignalFromContainer
func getRedstoneSignalFromContainer(container containerView) int {
	if container == nil {
		return 0
	}
	var f float32 = 0
	size := container.getContainerSize()
	for slot := 0; slot < size; slot++ {
		stack := container.getItem(slot)
		if !stackEmpty(stack) {
			// container.getMaxStackSize(stack) = min(getMaxStackSize()=99, stack.getMaxStackSize()) ==
			// stackMaxSize (the item's own max, since 99 > 64 always). VERIFIED: Container.getMaxStackSize.
			f += float32(int(stack.Count)) / float32(stackMaxSize(stack))
		}
	}
	f /= float32(size)
	return mthLerpDiscrete(f, 0, 15)
}

// mthLerpDiscrete ports net.minecraft.util.Mth.lerpDiscrete(delta, start, end):
//
//	int diff = end - start;
//	return start + Mth.floor(delta * (float)(diff - 1)) + (delta > 0.0f ? 1 : 0);
//
// For (delta, 0, 15) this is floor(delta*14) + (delta > 0 ? 1 : 0). CITE (VERIFIED javap Mth.lerpDiscrete).
func mthLerpDiscrete(delta float32, start, end int) int {
	diff := end - start
	// Mth.floor(float) == (int)Math.floor((double)value); reuse the existing float64 mthFloorF with the
	// widened product (the float32 delta*(diff-1) computed in float32 first, matching the Java float mul).
	base := start + mthFloorF(float64(delta*float32(diff-1)))
	if delta > 0 {
		return base + 1
	}
	return base
}

// createFlatSlots ports HopperBlockEntity.createFlatSlots(containerSize): the identity slot list
// {0,1,...,size-1} a plain (non-Worldly) container exposes to every face. CITE:
// HopperBlockEntity.createFlatSlots.
func createFlatSlots(size int) []int {
	slots := make([]int, size)
	for i := range slots {
		slots[i] = i
	}
	return slots
}

// getContainerAt ports HopperBlockEntity.getContainerAt(level, pos) narrowed to the BLOCK container
// (getBlockContainer): resolve the block-entity at pos to its Container view. Dispatches on the block
// at pos to the matching tick-owned BE store (chest -> resolveChest, furnace-family -> resolveFurnace,
// dispenser/dropper -> resolveDispenser, brewing_stand -> resolveBrewingStand, hopper -> resolveHopper),
// wrapping it in the containerView adapter. Returns nil when pos holds no container block-entity (the
// entity-container fallback is DEFERRED — no minecart, cited above). Tick-owned.
//
// 1:1 net.minecraft.world.level.block.entity.HopperBlockEntity.getContainerAt / getBlockContainer
func (t *TickLoop) getContainerAt(pos pk.Position) containerView {
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return nil
	}
	switch {
	case isChestBlock(state):
		// ChestBlockEntity is a plain Container. Double-chest combine is DEFERRED (cited): the single
		// 27-slot half at pos is returned. resolveChest synthesizes an empty container for a placed chest.
		if cl := t.resolveChest(pos); cl != nil {
			return &chestContainer{t: t, pos: pos, cl: cl}
		}
	case isAnyFurnaceBlock(state):
		if f := t.resolveFurnace(pos, state); f != nil {
			return &furnaceContainer{t: t, pos: pos, f: f}
		}
	case block.IsDispenserFamily(state):
		if d := t.resolveDispenser(pos, state); d != nil {
			return &dispenserContainer{t: t, pos: pos, d: d}
		}
	case isBrewingStandBlock(state):
		if b := t.resolveBrewingStand(pos, state); b != nil {
			return &brewingContainer{t: t, pos: pos, b: b}
		}
	case block.IsHopper(state):
		if h := t.resolveHopper(pos, state); h != nil {
			return &hopperContainer{t: t, pos: pos, h: h}
		}
	}
	// getEntityContainer FALLBACK (HopperBlockEntity.getContainerAt: `result == null -> getEntityContainer`).
	// No block container at pos → look for a CONTAINER-MINECART (chest/hopper minecart) whose box occupies the
	// block cell at pos, so a block hopper above/below a minecart-with-chest pulls/pushes from it, exactly as
	// vanilla. Formerly a cited DEFERRAL; now filled. CITE HopperBlockEntity.getEntityContainer +
	// EntitySelector.CONTAINER_ENTITY_SELECTOR.
	if mc := t.minecartContainerAt(pos); mc != nil {
		return mc
	}
	return nil
}

// -------------------------------------------------------------------------------------------------
// chestContainer — ChestBlockEntity (plain Container, 27 slots, single half — double-chest deferred)
// -------------------------------------------------------------------------------------------------

type chestContainer struct {
	t   *TickLoop
	pos pk.Position
	cl  *chestLoot
}

func (c *chestContainer) getContainerSize() int { return chestContainerSize }
func (c *chestContainer) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= len(c.cl.items) {
		return component.SlotData{Count: 0}
	}
	return c.cl.items[slot]
}
func (c *chestContainer) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= len(c.cl.items) {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.cl.items[slot] = stack
}
func (c *chestContainer) isEmpty() bool {
	for _, s := range c.cl.items {
		if !stackEmpty(s) {
			return false
		}
	}
	return true
}
func (c *chestContainer) setChanged() {
	c.t.markChestDirty(c.pos)
	c.t.broadcastChestChange(c.pos, c.cl)
}
func (c *chestContainer) getSlotsForFace(block.Direction) []int { return createFlatSlots(chestContainerSize) }
func (c *chestContainer) canPlaceItem(int, component.SlotData) bool { return true }
func (c *chestContainer) canPlaceItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *chestContainer) canTakeItem(int, component.SlotData) bool { return true }
func (c *chestContainer) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *chestContainer) isWorldly() bool     { return false }
func (c *chestContainer) asHopper() *hopperBE { return nil }

// -------------------------------------------------------------------------------------------------
// dispenserContainer — DispenserBlockEntity / DropperBlockEntity (plain Container, 9 slots)
// -------------------------------------------------------------------------------------------------

type dispenserContainer struct {
	t   *TickLoop
	pos pk.Position
	d   *dispenserBE
}

func (c *dispenserContainer) getContainerSize() int { return dispenserContainerSize }
func (c *dispenserContainer) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= len(c.d.items) {
		return component.SlotData{Count: 0}
	}
	return c.d.items[slot]
}
func (c *dispenserContainer) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= len(c.d.items) {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.d.items[slot] = stack
}
func (c *dispenserContainer) isEmpty() bool {
	for _, s := range c.d.items {
		if !stackEmpty(s) {
			return false
		}
	}
	return true
}
func (c *dispenserContainer) setChanged() {
	c.t.markDispenserDirty(c.pos)
	c.t.broadcastDispenserChange(c.pos, c.d)
}
func (c *dispenserContainer) getSlotsForFace(block.Direction) []int {
	return createFlatSlots(dispenserContainerSize)
}
func (c *dispenserContainer) canPlaceItem(int, component.SlotData) bool { return true }
func (c *dispenserContainer) canPlaceItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *dispenserContainer) canTakeItem(int, component.SlotData) bool { return true }
func (c *dispenserContainer) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *dispenserContainer) isWorldly() bool     { return false }
func (c *dispenserContainer) asHopper() *hopperBE { return nil }

// -------------------------------------------------------------------------------------------------
// hopperContainer — HopperBlockEntity (plain Container, 5 slots) — the hopper as a SOURCE/TARGET
// -------------------------------------------------------------------------------------------------

type hopperContainer struct {
	t   *TickLoop
	pos pk.Position
	h   *hopperBE
}

func (c *hopperContainer) getContainerSize() int { return hopperContainerSize }
func (c *hopperContainer) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= len(c.h.items) {
		return component.SlotData{Count: 0}
	}
	return c.h.items[slot]
}
func (c *hopperContainer) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= len(c.h.items) {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.h.items[slot] = stack
}
func (c *hopperContainer) isEmpty() bool { return c.h.isEmpty() }
func (c *hopperContainer) setChanged() {
	c.t.markHopperDirty(c.pos)
	c.t.broadcastHopperChange(c.pos, c.h)
}
func (c *hopperContainer) getSlotsForFace(block.Direction) []int {
	return createFlatSlots(hopperContainerSize)
}
func (c *hopperContainer) canPlaceItem(int, component.SlotData) bool { return true }
func (c *hopperContainer) canPlaceItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *hopperContainer) canTakeItem(int, component.SlotData) bool { return true }
func (c *hopperContainer) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *hopperContainer) isWorldly() bool     { return false }
func (c *hopperContainer) asHopper() *hopperBE { return c.h }

// -------------------------------------------------------------------------------------------------
// furnaceContainer — AbstractFurnaceBlockEntity (WorldlyContainer, 3 slots)
// -------------------------------------------------------------------------------------------------

type furnaceContainer struct {
	t   *TickLoop
	pos pk.Position
	f   *furnaceBE
}

func (c *furnaceContainer) getContainerSize() int { return furnaceContainerSize }
func (c *furnaceContainer) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= len(c.f.items) {
		return component.SlotData{Count: 0}
	}
	return c.f.items[slot]
}
func (c *furnaceContainer) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= len(c.f.items) {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.f.items[slot] = stack
}
func (c *furnaceContainer) isEmpty() bool {
	for _, s := range c.f.items {
		if !stackEmpty(s) {
			return false
		}
	}
	return true
}
func (c *furnaceContainer) setChanged() {
	c.t.markFurnaceDirty(c.pos)
	c.t.broadcastFurnaceChange(c.pos, c.f)
}

// getSlotsForFace ports AbstractFurnaceBlockEntity.getSlotsForFace: DOWN -> {2,1} (result then fuel),
// UP -> {0} (input), sides -> {1} (fuel). CITE (VERIFIED CFR): SLOTS_FOR_DOWN={2,1}, SLOTS_FOR_UP={0},
// SLOTS_FOR_SIDES={1}.
func (c *furnaceContainer) getSlotsForFace(direction block.Direction) []int {
	switch direction {
	case block.Down:
		return []int{2, 1}
	case block.Up:
		return []int{0}
	default:
		return []int{1}
	}
}

// canPlaceItem ports AbstractFurnaceBlockEntity.canPlaceItem(slot, stack): slot 2 (result) never;
// slot 1 (fuel) accepts a fuel item OR a bucket when the fuel slot is not already a bucket; slot 0
// (input) accepts anything. CITE (VERIFIED CFR).
func (c *furnaceContainer) canPlaceItem(slot int, stack component.SlotData) bool {
	if slot == furnaceSlotResult {
		return false
	}
	if slot == furnaceSlotFuel {
		id := int32(stack.ItemID)
		fuelSlot := c.f.items[furnaceSlotFuel]
		return furnaceIsFuel(id) || (id == itemNameToID("bucket") && int32(fuelSlot.ItemID) != itemNameToID("bucket"))
	}
	return true
}

func (c *furnaceContainer) canPlaceItemThroughFace(slot int, stack component.SlotData, _ block.Direction) bool {
	return c.canPlaceItem(slot, stack) // canPlaceItemThroughFace -> canPlaceItem
}
func (c *furnaceContainer) canTakeItem(int, component.SlotData) bool { return true }

// canTakeItemThroughFace ports AbstractFurnaceBlockEntity.canTakeItemThroughFace: from the DOWN face the
// fuel slot (1) only yields water_bucket / bucket (the emptied lava-bucket remainder); every other
// slot/face yields freely. CITE (VERIFIED CFR).
func (c *furnaceContainer) canTakeItemThroughFace(slot int, stack component.SlotData, direction block.Direction) bool {
	if direction == block.Down && slot == furnaceSlotFuel {
		id := int32(stack.ItemID)
		return id == itemNameToID("water_bucket") || id == itemNameToID("bucket")
	}
	return true
}
func (c *furnaceContainer) isWorldly() bool     { return true }
func (c *furnaceContainer) asHopper() *hopperBE { return nil }

// -------------------------------------------------------------------------------------------------
// brewingContainer — BrewingStandBlockEntity (WorldlyContainer, 5 slots)
// -------------------------------------------------------------------------------------------------

type brewingContainer struct {
	t   *TickLoop
	pos pk.Position
	b   *brewingStandBE
}

func (c *brewingContainer) getContainerSize() int { return brewContainerSize }
func (c *brewingContainer) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= len(c.b.items) {
		return component.SlotData{Count: 0}
	}
	return c.b.items[slot]
}
func (c *brewingContainer) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= len(c.b.items) {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.b.items[slot] = stack
}
func (c *brewingContainer) isEmpty() bool {
	for _, s := range c.b.items {
		if !stackEmpty(s) {
			return false
		}
	}
	return true
}
func (c *brewingContainer) setChanged() {
	c.t.markBrewingStandDirty(c.pos)
	c.t.broadcastBrewingStandChange(c.pos, c.b)
}

// getSlotsForFace ports BrewingStandBlockEntity.getSlotsForFace: UP -> {3} (ingredient), DOWN ->
// {0,1,2,3}, sides -> {0,1,2,4} (the three bottles + fuel). CITE (VERIFIED CFR): SLOTS_FOR_UP={3},
// SLOTS_FOR_DOWN={0,1,2,3}, SLOTS_FOR_SIDES={0,1,2,4}.
func (c *brewingContainer) getSlotsForFace(direction block.Direction) []int {
	switch direction {
	case block.Up:
		return []int{3}
	case block.Down:
		return []int{0, 1, 2, 3}
	default:
		return []int{0, 1, 2, 4}
	}
}

// canPlaceItem ports BrewingStandBlockEntity.canPlaceItem — reuses brewMayPlace (the same per-slot rule
// the brewing menu uses: slot 3 ingredient, slot 4 BREWING_FUEL, slots 0-2 potion/bottle). CITE
// (VERIFIED CFR): brewMayPlace == BrewingStandBlockEntity.canPlaceItem.
func (c *brewingContainer) canPlaceItem(slot int, stack component.SlotData) bool {
	if stackEmpty(stack) {
		return false
	}
	if slot == brewSlotBottle0 || slot == brewSlotBottle1 || slot == brewSlotBottle2 {
		// canPlaceItem's bottle branch ALSO requires the target slot be empty (getItem(slot).isEmpty()).
		return brewMayPlace(slot, stack) && stackEmpty(c.b.items[slot])
	}
	return brewMayPlace(slot, stack)
}

func (c *brewingContainer) canPlaceItemThroughFace(slot int, stack component.SlotData, _ block.Direction) bool {
	return c.canPlaceItem(slot, stack)
}
func (c *brewingContainer) canTakeItem(int, component.SlotData) bool { return true }

// canTakeItemThroughFace ports BrewingStandBlockEntity.canTakeItemThroughFace: slot 3 (ingredient)
// only yields glass_bottle; every other slot yields freely. CITE (VERIFIED CFR).
func (c *brewingContainer) canTakeItemThroughFace(slot int, stack component.SlotData, _ block.Direction) bool {
	if slot == brewSlotIngredient {
		return int32(stack.ItemID) == itemNameToID("glass_bottle")
	}
	return true
}
func (c *brewingContainer) isWorldly() bool     { return true }
func (c *brewingContainer) asHopper() *hopperBE { return nil }
