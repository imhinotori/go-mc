package server

// menu_security_test.go -- the per-tick ServerPlayer.tick container-validity sweep (menu_stillvalid.go)
// and the generic QUICK_CRAFT drag over a non-chest window (menu_quickcraft.go), plus the loom result
// shift-drain (loom_click.go quickMove loop). These lock the four bug fixes:
//   1. SECURITY: an open menu auto-closes when the backing block is broken / the player walks out of range.
//   2. QUICK_CRAFT: a drag distributes across a non-chest menu (dispenser).
//   3/4. loom shift-click drains the whole craftable stack + moves a full stack into an input.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestMenuStillValidClosesOnBlockBroken: open a chest, break the backing block, run the per-tick sweep --
// the window auto-closes (openContainer nil) and a ClientboundContainerClose is sent to the client.
func TestMenuStillValidClosesOnBlockBroken(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "", 0)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	if p.openContainer == nil {
		t.Fatal("chest did not open")
	}

	// Break the chest block.
	loop.only().world.SetBlock(pos, block.ToStateID[block.Air{}], dimMinY)

	// The stillValid gate now fails (block no longer a chest). The sweep closes the window.
	loop.sweepContainerStillValid()
	if p.openContainer != nil {
		t.Fatal("open menu survived the block being broken (stillValid not enforced)")
	}
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundContainerClose); n != 1 {
		t.Fatalf("ClientboundContainerClose sent %d times after auto-close, want 1", n)
	}
}

// TestMenuStillValidClosesOnPlayerMovesAway: open a chest, move the player far past block-interaction
// range, run the sweep -- the window auto-closes.
func TestMenuStillValidClosesOnPlayerMovesAway(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "", 0)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	if p.openContainer == nil {
		t.Fatal("chest did not open")
	}

	// In range: the sweep keeps it open.
	loop.sweepContainerStillValid()
	if p.openContainer == nil {
		t.Fatal("in-range chest was wrongly auto-closed")
	}

	// Teleport 100 blocks away (far past the 8.5-block block-interaction reach).
	p.x, p.z = 101.5, 101.5
	loop.sweepContainerStillValid()
	if p.openContainer != nil {
		t.Fatal("open menu survived the player walking out of range (stillValid not enforced)")
	}
}

// TestMenuDragDistributesOnNonChestMenu: open a dispenser (a non-chest, non-player window) and left-drag a
// stack of 8 across two empty grid slots -- each receives floor(8/2)=4, proving the generic QUICK_CRAFT
// reaches menus other than chest/inventory (bug #2).
func TestMenuDragDistributesOnNonChestMenu(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	loop.only().world.SetBlock(pos, block.ToStateID[block.Dispenser{Facing: block.Up, Triggered: false}], dimMinY)

	if !loop.openDispenser(p, pos) {
		t.Fatal("openDispenser returned false")
	}
	if p.openContainer == nil || p.openContainer.kind != containerKindDispenser {
		t.Fatal("dispenser did not open")
	}
	d := loop.dispensers[pos]
	win := int32(p.openContainer.windowID)

	// Stack of 8 cobblestone on the cursor.
	p.inventory.setCarried(component.SlotData{ItemID: pk.VarInt(item.Cobblestone.ID), Count: 8})

	start := int8((0 << 2) | 0) // type 0 (even split), START header 0
	add := int8((0 << 2) | 1)   // ADD header 1
	end := int8((0 << 2) | 2)   // END header 2
	loop.handleContainerClick(p, chestClickPacket(win, 0, -999, start, containerInputQuickCraft))
	for _, slot := range []int16{0, 1} { // dispenser grid slots 0 and 1
		loop.handleContainerClick(p, chestClickPacket(win, 0, slot, add, containerInputQuickCraft))
	}
	loop.handleContainerClick(p, chestClickPacket(win, 0, -999, end, containerInputQuickCraft))

	for _, slot := range []int{0, 1} {
		if got := int(d.items[slot].Count); got != 4 {
			t.Fatalf("dispenser slot %d = %d after drag, want 4 (floor(8/2))", slot, got)
		}
	}
	if c := int(p.inventory.getCarried().Count); c != 0 {
		t.Fatalf("cursor = %d after drag, want 0 (all distributed)", c)
	}
}

// TestLoomShiftDrainsStack: with 5 banners + 5 dyes + a pattern selected, a SINGLE shift-click on the loom
// result weaves banners until an input runs out -- the whole stack drains in one QUICK_MOVE (the vanilla
// doClick loop), not a single banner (bug #3).
func TestLoomShiftDrainsStack(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	ensureInventory(p)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	oc := &openContainer{windowID: 1, kind: containerKindLoom, loomPos: pos, loomSelected: -1}
	p.openContainer = oc

	oc.loomBanner = component.SlotData{ItemID: pk.VarInt(item.WhiteBanner.ID), Count: 5}
	oc.loomDye = component.SlotData{ItemID: pk.VarInt(item.RedDye.ID), Count: 5}
	oc.loomPattern = component.SlotData{ItemID: pk.VarInt(item.CreeperBannerPattern.ID), Count: 1}
	loop.loomInputsChanged(oc)
	if stackEmpty(oc.loomResult) {
		t.Fatal("loom produced no result with banner+dye+pattern")
	}

	// A shift-click (QUICK_MOVE) on the result slot (3): the doClick loop weaves as many banners as the
	// inputs allow (5), draining both the banner and dye stacks.
	loop.clickedLoom(p, oc, 3, 0, containerInputQuickMove)

	if oc.loomBanner.Count != 0 {
		t.Fatalf("banner slot = %d after shift-drain, want 0 (all 5 woven)", oc.loomBanner.Count)
	}
	if oc.loomDye.Count != 0 {
		t.Fatalf("dye slot = %d after shift-drain, want 0 (all 5 consumed)", oc.loomDye.Count)
	}
	total := 0
	for i := int16(0); i < 46; i++ {
		s := p.inventory.get(i)
		if !stackEmpty(s) && int32(s.ItemID) == int32(item.WhiteBanner.ID) {
			total += int(s.Count)
		}
	}
	if total != 5 {
		t.Fatalf("player inventory holds %d woven banners after shift-drain, want 5", total)
	}
}

// TestLoomMoveIntoInputMergesFullStack: shift-clicking a stack of dye from the player inventory into the
// loom moves the WHOLE stack into the dye input, not a single item (bug #4).
func TestLoomMoveIntoInputMergesFullStack(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	inv := ensureInventory(p)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	oc := &openContainer{windowID: 1, kind: containerKindLoom, loomPos: pos, loomSelected: -1}
	p.openContainer = oc

	// Put 12 red dye into a player MAIN slot (window slot 9 == loom menu index 4).
	inv.set(int16(windowMainFirst), component.SlotData{ItemID: pk.VarInt(item.RedDye.ID), Count: 12})

	loop.clickedLoom(p, oc, 4, 0, containerInputQuickMove)

	if got := int(oc.loomDye.Count); got != 12 {
		t.Fatalf("loom dye input = %d after shift-in, want 12 (whole stack, not 1)", got)
	}
	if got := int(inv.get(int16(windowMainFirst)).Count); got != 0 {
		t.Fatalf("player dye slot = %d after shift-in, want 0 (fully moved)", got)
	}
}
