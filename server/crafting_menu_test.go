package server

// crafting_menu_test.go — PLUGIN-05 (Plan 25-02) Task 2: the crafting_table BLOCK + the 3x3 CRAFTING
// MENU (the chest-open clone). Verifies the open seam end-to-end (ClientboundOpenScreen(minecraft:
// crafting) + ContainerSetContent), the 3x3 grid crafting through the SAME plugin Match path, the
// take-only result slot, and the close-returns-grid (no item loss).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// craftBlockLoop builds a block loop + a player + the embedded crafting matcher wired in, with a
// crafting_table at the returned pos.
func craftBlockLoop(t *testing.T) (*TickLoop, *tickPlayer, pk.Position) {
	t.Helper()
	loop, _ := newBlockLoop()
	loop.SetPlugins(craftingManager(t))
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	loop.world.SetBlock(pos, block.ToStateID[block.CraftingTable{}], dimMinY)
	return loop, p, pos
}

// TestCraftingTableOpen: right-clicking a crafting_table opens the 3x3 menu — a windowId is allocated,
// OpenScreen carries minecraft:crafting, and ContainerSetContent carries the 46-slot layout.
func TestCraftingTableOpen(t *testing.T) {
	loop, p, pos := craftBlockLoop(t)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if p.openContainer == nil {
		t.Fatal("openContainer is nil after crafting_table open")
	}
	if p.openContainer.kind != containerKindCrafting {
		t.Fatalf("openContainer kind = %d, want crafting", p.openContainer.kind)
	}
	if p.openContainer.windowID < 1 || p.openContainer.windowID > 100 {
		t.Fatalf("windowId %d out of the vanilla 1..100 range", p.openContainer.windowID)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenScreen); n != 1 {
		t.Fatalf("ClientboundOpenScreen sent %d times, want 1", n)
	}
	if n := countID(got, packetid.ClientboundContainerSetContent); n != 1 {
		t.Fatalf("ClientboundContainerSetContent sent %d times, want 1", n)
	}

	wantMenu := menuTypeID(registryid.Menu, "minecraft:crafting")
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		if int32(menuID) != wantMenu {
			t.Fatalf("OpenScreen menu id = %d, want minecraft:crafting (%d)", menuID, wantMenu)
		}
		if int32(win) != int32(p.openContainer.windowID) {
			t.Fatalf("OpenScreen windowId = %d, want %d", win, p.openContainer.windowID)
		}
	}
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundContainerSetContent) {
			continue
		}
		var win, st, count pk.VarInt
		if err := packet.Scan(&win, &st, &count); err != nil {
			t.Fatalf("SetContent scan: %v", err)
		}
		if int(count) != craftingMenuSize {
			t.Fatalf("SetContent item count = %d, want %d (1 result + 9 grid + 36 player)", count, craftingMenuSize)
		}
	}
}

// TestCraftingTableCrafts: placing a recipe in the 3x3 grid populates result slot 0 via the SAME
// slotChangedCraftingGrid/Match path (w=h=3); taking it consumes 1-per-cell over the grid.
func TestCraftingTableCrafts(t *testing.T) {
	loop, p, pos := craftBlockLoop(t)
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	oc := p.openContainer
	if oc == nil {
		t.Fatal("no open crafting window")
	}

	// Place the vertical 2-high stick pattern in the LEFT column of the 3x3 grid: grid cells 0 and 3
	// (the top-left and middle-left), 2 planks each.
	oc.craftGrid[0] = component.SlotData{ItemID: idOakPlanks, Count: 2}
	oc.craftGrid[3] = component.SlotData{ItemID: idOakPlanks, Count: 2}

	// Recompute the result (a grid change would normally fire this on the next click).
	loop.slotChangedCraftingGrid(craftingTableView(oc))
	if r := oc.craftResult; r.ItemID != idStick || r.Count != 4 {
		t.Fatalf("3x3 result = id=%d count=%d, want id=%d count=4", r.ItemID, r.Count, idStick)
	}

	// Take the result via a PICKUP on menu slot 0.
	loop.clickedCrafting(p, oc, 0, 0, containerInputPickup)

	inv := ensureInventory(p)
	if c := inv.getCarried(); c.ItemID != idStick || c.Count != 4 {
		t.Fatalf("carried after take = id=%d count=%d, want id=%d count=4", c.ItemID, c.Count, idStick)
	}
	if oc.craftGrid[0].Count != 1 || oc.craftGrid[3].Count != 1 {
		t.Fatalf("grid after take = [0]=%d [3]=%d, want 1 each (1 consumed from 2)", oc.craftGrid[0].Count, oc.craftGrid[3].Count)
	}
}

// TestCraftingResultMayPlaceFalse: the result slot (0) is take-only — a client click that would place
// the cursor INTO slot 0 is rejected (the result is owned by the matcher).
func TestCraftingResultMayPlaceFalse(t *testing.T) {
	loop, p, pos := craftBlockLoop(t)
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	oc := p.openContainer

	inv := ensureInventory(p)
	inv.setCarried(component.SlotData{ItemID: idDirt, Count: 1}) // a dirt on the cursor

	// PICKUP on the (empty) result slot with a dirt cursor: must NOT place the dirt into slot 0.
	loop.clickedCrafting(p, oc, 0, 0, containerInputPickup)

	if !stackEmpty(oc.craftResult) {
		t.Fatalf("result slot received a placed item (count=%d) — must be take-only", oc.craftResult.Count)
	}
	if c := inv.getCarried(); c.ItemID != idDirt || c.Count != 1 {
		t.Fatalf("cursor changed (id=%d count=%d) — the rejected place must leave the cursor intact", c.ItemID, c.Count)
	}
}

// TestCraftingClose: closing the crafting window returns every grid cell to the player inventory (no
// item loss — CraftingMenu.removed/clearContainer), NOT the chest persist model.
func TestCraftingClose(t *testing.T) {
	loop, p, pos := craftBlockLoop(t)
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	oc := p.openContainer

	// Put items in the grid.
	oc.craftGrid[0] = component.SlotData{ItemID: idOakPlanks, Count: 5}
	oc.craftGrid[4] = component.SlotData{ItemID: idDirt, Count: 3}

	inv := ensureInventory(p)
	// Count planks/dirt in the player inventory before close (should be 0 in main/hotbar).
	plBefore, dtBefore := countItem(inv, idOakPlanks), countItem(inv, idDirt)

	closePkt := chestClosePacket(int32(oc.windowID))
	loop.handleContainerClose(p, closePkt)

	if p.openContainer != nil {
		t.Fatal("openContainer not freed after close")
	}
	// The grid items must now be in the player inventory (returned, not lost).
	if got := countItem(inv, idOakPlanks); got != plBefore+5 {
		t.Fatalf("planks in inventory after close = %d, want %d (5 returned)", got, plBefore+5)
	}
	if got := countItem(inv, idDirt); got != dtBefore+3 {
		t.Fatalf("dirt in inventory after close = %d, want %d (3 returned)", got, dtBefore+3)
	}
}

// countItem sums the count of a given item id across the whole player inventory window.
func countItem(inv *Inventory, id pk.VarInt) int {
	n := 0
	for i := 0; i < playerInventorySize; i++ {
		s := inv.get(int16(i))
		if !stackEmpty(s) && s.ItemID == id {
			n += int(s.Count)
		}
	}
	return n
}
