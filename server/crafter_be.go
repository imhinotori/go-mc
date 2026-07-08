package server

// crafter_be.go -- the CRAFTER BLOCK-ENTITY (redstone auto-crafter 3x3 grid): a 1:1 port of
// net.minecraft.world.level.block.entity.CrafterBlockEntity over the 26.2 jar (javap this session).
// A flat 9-slot container keyed by world position (t.crafters, the t.dispensers twin) plus the per-slot
// DISABLED bitmask (containerData 0..8) + craftingTicksRemaining. The craft drive lives in crafter.go;
// the block-state shape (ORIENTATION/CRAFTING/TRIGGERED) in level/block/crafter.go.
//
// ANCHORS (VERIFIED javap CrafterBlockEntity): CONTAINER_SIZE=9; SLOT_DISABLED=1; SLOT_ENABLED=0;
// DATA_TRIGGERED=9. getRedstoneSignal: n=0; for i: if (!getItem(i).isEmpty() OR isSlotDisabled(i)) n++.
// isSlotDisabled(i): 0<=i<9 and containerData.get(i)==1. slotCanBeDisabled(i): 0<=i<9 and getItem empty.
// serverTick: n=craftingTicksRemaining-1; if (n<0) return; store n; if (n==0) setBlock CRAFTING=false.
//
// SCOPE: canPlaceItem/smallerStackExist ported for the getContainerAt hopper-insert seam. The CrafterMenu
// GUI is DEFERRED (useWithoutItem -> no-op); the auto-craft drive + comparator + disabled bitmask are all
// faithful. CITE: CrafterBlockEntity.createMenu -> CrafterMenu.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const crafterContainerSize = 9

const (
	crafterSlotEnabled  = 0
	crafterSlotDisabled = 1
)

type crafterBE struct {
	items                  [crafterContainerSize]component.SlotData
	disabled               [crafterContainerSize]bool
	craftingTicksRemaining int
}

func (c *crafterBE) crafterIsSlotDisabled(i int) bool {
	if i < 0 || i >= crafterContainerSize {
		return false
	}
	return c.disabled[i]
}

func (c *crafterBE) crafterSlotCanBeDisabled(i int) bool {
	return i >= 0 && i < crafterContainerSize && stackEmpty(c.items[i])
}

func (c *crafterBE) crafterSetSlotState(i int, enabled bool) {
	if !c.crafterSlotCanBeDisabled(i) {
		return
	}
	c.disabled[i] = !enabled
}

func (c *crafterBE) crafterSmallerStackExist(currentCount int, stack component.SlotData, startSlot int) bool {
	for i := startSlot + 1; i < crafterContainerSize; i++ {
		if c.crafterIsSlotDisabled(i) {
			continue
		}
		s := c.items[i]
		if !stackEmpty(s) && int(s.Count) < currentCount && stackSameItemSameComponents(s, stack) {
			return true
		}
	}
	return false
}

func (c *crafterBE) crafterCanPlaceItem(i int, stack component.SlotData) bool {
	if i < 0 || i >= crafterContainerSize {
		return false
	}
	if c.disabled[i] {
		return false
	}
	target := c.items[i]
	count := int(target.Count)
	if count >= stackMaxSize(target) {
		return false
	}
	if stackEmpty(target) {
		return true
	}
	return !c.crafterSmallerStackExist(count, target, i)
}

func (c *crafterBE) crafterGetRedstoneSignal() int {
	n := 0
	for i := 0; i < crafterContainerSize; i++ {
		if !stackEmpty(c.items[i]) || c.crafterIsSlotDisabled(i) {
			n++
		}
	}
	return n
}

func (t *TickLoop) resolveCrafter(pos pk.Position, state block.StateID) *crafterBE {
	if t.crafters == nil {
		t.crafters = make(map[pk.Position]*crafterBE)
	}
	if c, ok := t.crafters[pos]; ok {
		return c
	}
	if !block.IsCrafter(state) {
		return nil
	}
	c := t.loadCrafterBE(pos)
	if c == nil {
		c = &crafterBE{}
	}
	t.crafters[pos] = c
	return c
}

type crafterContainer struct {
	t   *TickLoop
	pos pk.Position
	c   *crafterBE
}

func (cc *crafterContainer) getContainerSize() int { return crafterContainerSize }
func (cc *crafterContainer) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= crafterContainerSize {
		return component.SlotData{Count: 0}
	}
	return cc.c.items[slot]
}
func (cc *crafterContainer) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= crafterContainerSize {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	cc.c.items[slot] = stack
}
func (cc *crafterContainer) isEmpty() bool {
	for i := range cc.c.items {
		if !stackEmpty(cc.c.items[i]) {
			return false
		}
	}
	return true
}
func (cc *crafterContainer) setChanged() { cc.t.markCrafterDirty(cc.pos) }
func (cc *crafterContainer) getSlotsForFace(direction block.Direction) []int {
	slots := make([]int, crafterContainerSize)
	for i := range slots {
		slots[i] = i
	}
	return slots
}
func (cc *crafterContainer) canPlaceItem(slot int, stack component.SlotData) bool {
	return cc.c.crafterCanPlaceItem(slot, stack)
}
func (cc *crafterContainer) canPlaceItemThroughFace(slot int, stack component.SlotData, direction block.Direction) bool {
	return cc.c.crafterCanPlaceItem(slot, stack)
}
func (cc *crafterContainer) canTakeItem(slot int, stack component.SlotData) bool { return true }
func (cc *crafterContainer) canTakeItemThroughFace(slot int, stack component.SlotData, direction block.Direction) bool {
	return false
}
func (cc *crafterContainer) isWorldly() bool     { return true }
func (cc *crafterContainer) asHopper() *hopperBE { return nil }
