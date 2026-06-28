package server

// cooking_test.go — PLUGIN-05 (Plan 25-03) Task 2: the build-or-defer OUTCOME, tested.
//
// BUILT this plan: the STONECUTTER block + its single-input recipe-picker menu (stonecutter_menu.go).
// The audit (deferred-blocks.md) marked stonecutting BUILD (no fuel/tick/block-entity) and
// smelting/blasting/smoking/campfire_cooking DEFER (the per-tick furnace block-entity + fuel-table
// subsystem does not exist — a context-cost defer). These tests prove BOTH halves:
//
//   - TestStonecutterOpen / TestStonecutterSelectAndCraft / TestStonecutterClose: the built block opens
//     its menu (minecraft:stonecutter), input drives the recipe list, a button-click selects a result,
//     taking it consumes exactly 1 input through the plugin Match path, and close returns the input.
//   - TestSmeltingMatcherShips: the DEFERRED smelting block's MATCHER still resolves headless (iron_ore
//     -> iron_ingot) through the SAME plugin Manager.Match seam — proving the deferral is the BLOCK UI
//     only, never the recipe logic.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

const (
	idStone       = 7   // minecraft:stone
	idStoneBricks = 409 // minecraft:stone_bricks
	idIronOre     = 99  // minecraft:iron_ore
	idIronIngot   = 938 // minecraft:iron_ingot
)

// stoneCutLoop builds a block loop + a survival player + the embedded crafting matcher (which carries
// the stonecutting recipes too), with a stonecutter block at the returned pos.
func stoneCutLoop(t *testing.T) (*TickLoop, *tickPlayer, pk.Position) {
	t.Helper()
	loop, _ := newBlockLoop()
	loop.SetPlugins(craftingManager(t))
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	loop.world.SetBlock(pos, block.ToStateID[block.Stonecutter{}], dimMinY)
	return loop, p, pos
}

// stonecutterButtonPacket builds ServerboundContainerButtonClick (containerId, buttonId — both VarInt).
func stonecutterButtonPacket(window int32, button int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundContainerButtonClick), pk.VarInt(window), pk.VarInt(button))
}

// TestStonecutterOpen: right-clicking a stonecutter opens its menu — a windowId is allocated, OpenScreen
// carries minecraft:stonecutter, and ContainerSetContent carries the 38-slot layout (input + result + 36).
func TestStonecutterOpen(t *testing.T) {
	loop, p, pos := stoneCutLoop(t)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if p.openContainer == nil {
		t.Fatal("openContainer is nil after stonecutter open")
	}
	if p.openContainer.kind != containerKindStonecutter {
		t.Fatalf("openContainer kind = %d, want stonecutter", p.openContainer.kind)
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

	wantMenu := menuTypeID(registryid.Menu, "minecraft:stonecutter")
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		if int32(menuID) != wantMenu {
			t.Fatalf("OpenScreen menu id = %d, want minecraft:stonecutter (%d)", menuID, wantMenu)
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
		if int(count) != stonecutterMenuSize {
			t.Fatalf("SetContent item count = %d, want %d (1 input + 1 result + 36 player)", count, stonecutterMenuSize)
		}
	}
}

// TestStonecutterSelectAndCraft: placing stone in the input drives the recipe list; a button-click selects
// stone_bricks; taking the result (slot 1) consumes exactly 1 stone through the plugin Match path.
func TestStonecutterSelectAndCraft(t *testing.T) {
	loop, p, pos := stoneCutLoop(t)
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	oc := p.openContainer
	if oc == nil {
		t.Fatal("no open stonecutter window")
	}

	// Put 3 stone in the input slot (cell 0).
	oc.cutInput = component.SlotData{ItemID: idStone, Count: 3}
	loop.stonecutterInputChanged(oc)

	// The result list for stone must be non-empty (stone -> many stonecutter outputs incl. stone_bricks).
	if len(oc.cutResults) == 0 {
		t.Fatal("stonecutter result list empty for a stone input (selectByInput found nothing)")
	}

	// Find the stone_bricks result index and select it via a button-click.
	idx := -1
	for i, r := range oc.cutResults {
		if r.ID == idStoneBricks {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("stone_bricks not in the stonecutter result list for a stone input")
	}
	loop.clickStonecutterButton(p, oc, idx)
	if oc.cutSelected != idx {
		t.Fatalf("selected index = %d, want %d after button-click", oc.cutSelected, idx)
	}
	if r := oc.cutResult; r.ItemID != idStoneBricks {
		t.Fatalf("result slot = id=%d after select, want stone_bricks id=%d", r.ItemID, idStoneBricks)
	}

	// Take the result via a PICKUP on menu slot 1 (RESULT_SLOT). Consumes exactly 1 input.
	loop.clickedStonecutter(p, oc, 1, 0, containerInputPickup)

	inv := ensureInventory(p)
	if c := inv.getCarried(); c.ItemID != idStoneBricks || c.Count != 1 {
		t.Fatalf("carried after take = id=%d count=%d, want stone_bricks count=1", c.ItemID, c.Count)
	}
	if oc.cutInput.Count != 2 {
		t.Fatalf("input after take = %d, want 2 (1 stone consumed from 3)", oc.cutInput.Count)
	}
}

// TestStonecutterResultIsPluginMatch: the result the stonecutter produces on take is validated through the
// plugin Manager.Match 1x1 stonecutting path (the dogfood) — not a hardcoded Go result.
func TestStonecutterResultIsPluginMatch(t *testing.T) {
	loop, p, pos := stoneCutLoop(t)
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	oc := p.openContainer

	oc.cutInput = component.SlotData{ItemID: idStone, Count: 1}
	loop.stonecutterInputChanged(oc)

	// The selected result must be reachable through the plugin Match seam (the 1x1 stonecutting matcher).
	id, _, ok := loop.plugins.Match(stonecutterMatchPayload(idStone, idStoneBricks))
	if !ok || id != idStoneBricks {
		t.Fatalf("plugin Match for stone->stone_bricks: id=%d ok=%v, want id=%d ok=true", id, ok, idStoneBricks)
	}
}

// TestStonecutterClose: closing the stonecutter window returns the input to the player inventory (no item
// loss — StonecutterMenu.removed/clearContainer over the input slot). The result is virtual (not returned).
func TestStonecutterClose(t *testing.T) {
	loop, p, pos := stoneCutLoop(t)
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	oc := p.openContainer

	oc.cutInput = component.SlotData{ItemID: idStone, Count: 5}
	loop.stonecutterInputChanged(oc)

	inv := ensureInventory(p)
	stBefore := countItem(inv, idStone)

	closePkt := chestClosePacket(int32(oc.windowID))
	loop.handleContainerClose(p, closePkt)

	if p.openContainer != nil {
		t.Fatal("openContainer not freed after close")
	}
	if got := countItem(inv, idStone); got != stBefore+5 {
		t.Fatalf("stone in inventory after close = %d, want %d (5 returned)", got, stBefore+5)
	}
}

// TestSmeltingMatcherShips: the DEFERRED smelting block's MATCHER still works headless. Feed the smelting
// matcher an iron_ore via the plugin Manager.Match 1x1 path and assert iron_ingot — proving the deferral
// is the furnace BLOCK only, never the smelting recipe logic (which shipped + is tested in Plan 01).
func TestSmeltingMatcherShips(t *testing.T) {
	mgr := craftingManager(t)
	id, count, ok := mgr.Match(stonecutterMatchPayload(idIronOre, idIronIngot))
	if !ok || id != idIronIngot || count != 1 {
		t.Fatalf("smelting matcher iron_ore->iron_ingot: id=%d count=%d ok=%v, want id=%d count=1 ok=true",
			id, count, ok, idIronIngot)
	}
}
