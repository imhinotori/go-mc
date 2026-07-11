package server

// shulker_box_container.go -- the containerView adapter for a ShulkerBoxBlockEntity, so the shared hopper
// transfer + comparator container-analog output (container.go) drive a shulker box the same way they drive
// a chest/dispenser. A 1:1 port of ShulkerBoxBlockEntity's WorldlyContainer surface (26.2 jar).
//
// 1:1 ANCHORS (VERIFIED javap):
//   ShulkerBoxBlockEntity implements WorldlyContainer:
//     getSlotsForFace(dir) = SLOTS (0..26, every slot on every face);
//     canPlaceItemThroughFace(i, stack, dir) = !(Block.byItem(stack.getItem()) instanceof ShulkerBoxBlock)
//         (a shulker box cannot be pushed into another shulker box by a hopper);
//     canTakeItemThroughFace(i, stack, dir) = true.
//   The comparator reads getRedstoneSignalFromContainer over the 27 slots (ShulkerBoxBlock.hasAnalogOutputSignal
//   == true, getAnalogOutputSignal == getRedstoneSignalFromBlockEntity) -- routed through getContainerAt ->
//   containerAnalogOutputSignal (redstone_diode.go), so a comparator behind a shulker box reads its fullness.

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// shulkerContainerView adapts a tick-owned shulkerBE to the containerView interface (container.go). A
// WorldlyContainer whose every face exposes all 27 slots, with the shulker nesting-reject on placement.
type shulkerContainerView struct {
	t   *TickLoop
	pos pk.Position
	s   *shulkerBE
}

func (c *shulkerContainerView) getContainerSize() int { return shulkerContainerSize }
func (c *shulkerContainerView) getItem(slot int) component.SlotData {
	if slot < 0 || slot >= len(c.s.items) {
		return component.SlotData{Count: 0}
	}
	return c.s.items[slot]
}
func (c *shulkerContainerView) setItem(slot int, stack component.SlotData) {
	if slot < 0 || slot >= len(c.s.items) {
		return
	}
	if stackEmpty(stack) {
		stack = component.SlotData{Count: 0}
	}
	c.s.items[slot] = stack
}
func (c *shulkerContainerView) isEmpty() bool { return c.s.isEmpty() }
func (c *shulkerContainerView) setChanged() {
	c.t.markShulkerDirty(c.pos)
	c.t.broadcastShulkerChange(c.pos, c.s)
}

// getSlotsForFace ports ShulkerBoxBlockEntity.getSlotsForFace -> SLOTS (all 27 on every face). CITE.
func (c *shulkerContainerView) getSlotsForFace(block.Direction) []int {
	return createFlatSlots(shulkerContainerSize)
}

// canPlaceItem ports the base Container.canPlaceItem (default true; the WorldlyContainer face gate is the
// real nesting reject, below). A menu ShulkerBoxSlot enforces the same reject via canFitInsideContainerItems.
func (c *shulkerContainerView) canPlaceItem(int, component.SlotData) bool { return true }

// canPlaceItemThroughFace ports ShulkerBoxBlockEntity.canPlaceItemThroughFace: reject a shulker box item
// (Block.byItem(stack) instanceof ShulkerBoxBlock) -- no nesting a shulker in a shulker via a hopper. CITE.
func (c *shulkerContainerView) canPlaceItemThroughFace(_ int, stack component.SlotData, _ block.Direction) bool {
	return !stackIsShulkerBoxItem(stack)
}
func (c *shulkerContainerView) canTakeItem(int, component.SlotData) bool { return true }
func (c *shulkerContainerView) canTakeItemThroughFace(int, component.SlotData, block.Direction) bool {
	return true
}
func (c *shulkerContainerView) isWorldly() bool     { return true }
func (c *shulkerContainerView) asHopper() *hopperBE { return nil }
