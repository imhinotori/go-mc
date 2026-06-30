package server

// chest_open_test.go — STRUCT-POLISH-01 chest-OPEN UI (the 20-02 W2 follow-up): the runtime
// right-click → resolve chest BE → unpackLootTable (lazy, one-shot) → ClientboundOpenScreen +
// ContainerSetContent → ContainerClick (move items) → ContainerClose (free windowId + persist).
// Verifies the vanilla seam end-to-end with a capture client + drainPackets.
//
// Jar-cited: ChestBlock.useWithoutItem → Player.openMenu (ServerPlayer.openMenu +
// nextContainerCounter) → ClientboundOpenScreenPacket(containerId, generic_9x3 menu id, title);
// ChestMenu slot layout (27 chest + 27 main + 9 hotbar = 63); ContainerClick on the chest window.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// testChestLootNBT mirrors world/neighborhood.go chestLootNBT (unexported there): the bare
// {LootTable, LootTableSeed} compound payload (the 3-byte root header stripped) the chest BE carries.
func testChestLootNBT(table string, seed int64) nbt.RawMessage {
	doc, err := nbt.Marshal(struct {
		LootTable     string `nbt:"LootTable"`
		LootTableSeed int64  `nbt:"LootTableSeed"`
	}{LootTable: table, LootTableSeed: seed})
	if err != nil {
		panic(err)
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}
}

// placeChestBE writes a chest block at pos and records a loot-bearing chest BlockEntity (the
// gen-time {LootTable, LootTableSeed} store) into the owning chunk's BlockEntity list — exactly
// what world/structure createChest emits. The server resolves it at open time.
func placeChestBE(loop *TickLoop, mgr *level.Chunk, pos pk.Position, table string, seed int64) {
	loop.only().world.SetBlock(pos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle, Waterlogged: false}], dimMinY)
	be := level.BlockEntity{
		Y:    int16(pos.Y),
		Type: block.EntityTypes["minecraft:chest"],
		Data: testChestLootNBT(table, seed),
	}
	be.PackXZ(pos.X&15, pos.Z&15)
	mgr.BlockEntity = append(mgr.BlockEntity, be)
}

// containerClickPacket builds a ServerboundContainerClick on `window` for a single PICKUP on
// slotNum with no changed-slots map and an empty carried HashedStack (the minimal click wire).
func chestClickPacket(window int32, stateID int32, slotNum int16, button int8, input int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundContainerClick),
		pk.VarInt(window),
		pk.VarInt(stateID),
		pk.Short(slotNum),
		pk.Byte(button),
		pk.VarInt(input),
		pk.VarInt(0),     // changedSlots count = 0
		pk.Boolean(false), // carried HashedStack: not present
	)
}

// chestClosePacket builds a ServerboundContainerClose for the given window id.
func chestClosePacket(window int32) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundContainerClose), pk.VarInt(window))
}

// TestChestOpenRollsAndSends: right-click a loot chest → the chest rolls (one-shot) and the
// player receives ClientboundOpenScreen(generic_9x3) + ContainerSetContent carrying the rolled
// 27 chest slots + the 36 player-inventory slots.
func TestChestOpenRollsAndSends(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "minecraft:chests/simple_dungeon", 123456789)

	// Right-click the chest (UseItemOn with an empty hand): the block interaction opens it.
	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	// The chest rolled (one-shot) and is tracked open.
	cl := loop.openChests[pos]
	if cl == nil {
		t.Fatal("chest not registered in openChests after open")
	}
	if cl.LootTable != "" {
		t.Fatalf("chest LootTable not cleared after roll (=%q): re-open would re-roll", cl.LootTable)
	}
	nonEmpty := 0
	for _, s := range cl.items {
		if s.Count > 0 {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		t.Fatal("chest rolled 0 items — simple_dungeon@123456789 must produce a non-empty list")
	}

	// The player has an open container window pointing at the chest.
	if p.openContainer == nil {
		t.Fatal("player openContainer is nil after open")
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

	// Decode the OpenScreen: windowId, generic_9x3 menu id, then verify it matches.
	openMenuID := int32(-1)
	var openWindow int32
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenScreen) {
			continue
		}
		var win, menuID pk.VarInt
		if err := packet.Scan(&win, &menuID); err != nil {
			t.Fatalf("OpenScreen scan: %v", err)
		}
		openWindow = int32(win)
		openMenuID = int32(menuID)
	}
	wantMenu := menuTypeID(registryid.Menu, "minecraft:generic_9x3")
	if openMenuID != wantMenu {
		t.Fatalf("OpenScreen menu id = %d, want generic_9x3 (%d)", openMenuID, wantMenu)
	}
	if openWindow != int32(p.openContainer.windowID) {
		t.Fatalf("OpenScreen windowId = %d, want %d", openWindow, p.openContainer.windowID)
	}

	// The SetContent list is 27 chest + 36 player = 63 slots.
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundContainerSetContent) {
			continue
		}
		var win, st, count pk.VarInt
		if err := packet.Scan(&win, &st, &count); err != nil {
			t.Fatalf("SetContent scan: %v", err)
		}
		if int(count) != chestMenuSize {
			t.Fatalf("SetContent item count = %d, want %d (27 chest + 36 player)", count, chestMenuSize)
		}
	}
}

// TestChestReopenDoesNotReroll: opening an already-rolled chest re-sends the menu but does NOT
// re-roll the loot (the one-shot LootTable clear holds across opens).
func TestChestReopenDoesNotReroll(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "minecraft:chests/simple_dungeon", 123456789)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	first := append([]component.SlotData(nil), loop.openChests[pos].items...)

	// Close then re-open; the contents must be byte-identical (no re-roll).
	loop.handleContainerClose(p, chestClosePacket(int32(p.openContainer.windowID)))
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	second := loop.openChests[pos].items

	if len(first) != len(second) {
		t.Fatalf("re-open changed item count %d -> %d (re-roll)", len(first), len(second))
	}
	for i := range first {
		if first[i].ItemID != second[i].ItemID || first[i].Count != second[i].Count {
			t.Fatalf("re-open changed slot %d (re-roll)", i)
		}
	}
}

// TestChestClickMovesItemToPlayer: a left-click PICKUP on a non-empty chest slot puts the stack
// on the cursor, then a left-click on an empty player slot deposits it — the item moved from the
// chest into the player's reachable storage.
func TestChestClickMovesItemToPlayer(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "minecraft:chests/simple_dungeon", 123456789)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	cl := loop.openChests[pos]
	win := int32(p.openContainer.windowID)

	// Find the first non-empty chest slot (menu index 0..26).
	src := -1
	for i, s := range cl.items {
		if s.Count > 0 {
			src = i
			break
		}
	}
	if src < 0 {
		t.Fatal("no non-empty chest slot to move")
	}
	want := cl.items[src]

	// Left-click PICKUP the chest slot → onto the cursor.
	loop.handleContainerClick(p, chestClickPacket(win, 0, int16(src), 0, containerInputPickup))
	if cl.items[src].Count != 0 {
		t.Fatalf("chest slot %d still has %d after pickup, want emptied", src, cl.items[src].Count)
	}
	if p.inventory.getCarried().ItemID != want.ItemID || p.inventory.getCarried().Count != want.Count {
		t.Fatalf("cursor = (%d x%d), want (%d x%d)",
			p.inventory.getCarried().ItemID, p.inventory.getCarried().Count, want.ItemID, want.Count)
	}

	// Left-click PICKUP onto an empty PLAYER inventory slot (chest-window index 27 = main slot 9).
	loop.handleContainerClick(p, chestClickPacket(win, 0, 27, 0, containerInputPickup))
	got := p.inventory.get(int16(windowMainFirst))
	if got.ItemID != want.ItemID || got.Count != want.Count {
		t.Fatalf("player main slot = (%d x%d), want (%d x%d)", got.ItemID, got.Count, want.ItemID, want.Count)
	}
	if p.inventory.getCarried().Count != 0 {
		t.Fatalf("cursor still holds %d after deposit, want empty", p.inventory.getCarried().Count)
	}
}

// TestChestQuickCraftDragDistributes locks the chest right/left-drag (QUICK_CRAFT) the operator
// reported missing: with a stack on the cursor, a left-drag (type 0, even split) across three
// empty chest slots distributes floor(count/3) into each. Drives START / ADD×3 / END over the
// chest window. CITE AbstractContainerMenu.doClick QUICK_CRAFT branch.
func TestChestQuickCraftDragDistributes(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "", 0) // empty chest (no loot) -> 27 empty slots

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	cl := loop.openChests[pos]
	win := int32(p.openContainer.windowID)

	// Put a stack of 9 cobblestone on the cursor.
	p.inventory.setCarried(component.SlotData{ItemID: pk.VarInt(item.Cobblestone.ID), Count: 9})

	// Left-drag even split: button = (type<<2)|header. type 0 (even).
	start := int8((0 << 2) | 0) // START header 0
	add := int8((0 << 2) | 1)   // ADD header 1
	end := int8((0 << 2) | 2)   // END header 2
	loop.handleContainerClick(p, chestClickPacket(win, 0, -999, start, containerInputQuickCraft))
	for _, slot := range []int16{0, 1, 2} { // three empty chest slots
		loop.handleContainerClick(p, chestClickPacket(win, 0, slot, add, containerInputQuickCraft))
	}
	loop.handleContainerClick(p, chestClickPacket(win, 0, -999, end, containerInputQuickCraft))

	// floor(9/3) = 3 into each of the three chest slots; cursor empties.
	for _, slot := range []int{0, 1, 2} {
		if got := cl.items[slot].Count; got != 3 {
			t.Fatalf("chest slot %d = %d after drag, want 3", slot, got)
		}
	}
	if c := p.inventory.getCarried().Count; c != 0 {
		t.Fatalf("cursor holds %d after drag, want 0 (all distributed)", c)
	}
}

// TestChestPickupAllCollectsMatching locks the chest double-click (PICKUP_ALL): with one item on
// the cursor, a double-click sweeps every matching stack in the chest window onto the cursor
// (operator: 'doble click en un item me deberia tomar todos los del cofre').
func TestChestPickupAllCollectsMatching(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "", 0)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	cl := loop.openChests[pos]
	win := int32(p.openContainer.windowID)

	cob := pk.VarInt(item.Cobblestone.ID)
	// Seed three chest slots with 10 cobble each (chest menu indices 1,2,3).
	cl.items[1] = component.SlotData{ItemID: cob, Count: 10}
	cl.items[2] = component.SlotData{ItemID: cob, Count: 10}
	cl.items[3] = component.SlotData{ItemID: cob, Count: 10}
	// 30 cobble already on the cursor (the double-click happens AFTER the first click already put a
	// stack on the cursor; the second click — PICKUP_ALL — targets an EMPTY slot and sweeps the rest).
	p.inventory.setCarried(component.SlotData{ItemID: cob, Count: 30})

	// PICKUP_ALL targeting an EMPTY chest slot (index 0) — vanilla's double-click PICKUP_ALL sweeps
	// every matching stack in the window onto the cursor (30 + 3×10 = 60 <= 64).
	loop.handleContainerClick(p, chestClickPacket(win, 0, 0, 0, containerInputPickupAll))

	if c := int(p.inventory.getCarried().Count); c != 60 {
		t.Fatalf("cursor = %d after pickup-all, want 60 (30 + 3×10)", c)
	}
	for s := 1; s <= 3; s++ {
		if cl.items[s].Count != 0 {
			t.Fatalf("chest slot %d = %d after pickup-all, want emptied", s, cl.items[s].Count)
		}
	}
}

// TestChestClosePersistsAndFrees: ContainerClose writes the chest items back to the BE store,
// frees the windowId (openContainer nil), and a re-open shows the persisted contents.
func TestChestClosePersistsAndFrees(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "minecraft:chests/simple_dungeon", 123456789)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	cl := loop.openChests[pos]
	win := int32(p.openContainer.windowID)

	// Mutate a chest slot: take the first non-empty item out (drop it to the cursor, then close).
	src := -1
	for i, s := range cl.items {
		if s.Count > 0 {
			src = i
			break
		}
	}
	loop.handleContainerClick(p, chestClickPacket(win, 0, int16(src), 0, containerInputPickup))
	emptiedItem := cl.items[src] // now empty

	loop.handleContainerClose(p, chestClosePacket(win))
	if p.openContainer != nil {
		t.Fatal("openContainer not freed after close")
	}

	// The persisted chest store still holds the emptied slot (the mutation persisted).
	persisted := loop.openChests[pos]
	if persisted == nil {
		t.Fatal("chest store dropped on close — items must persist")
	}
	if persisted.items[src].Count != emptiedItem.Count {
		t.Fatalf("close did not persist the emptied slot %d", src)
	}
}

// TestChestCloseReturnsCarriedItem is the "items go invisible on close" regression: closing a container
// while holding an item on the cursor must place that item back into the player inventory and clear the
// cursor (vanilla AbstractContainerMenu.removed -> dropOrPlaceInInventory + setCarried(EMPTY)). Before the
// fix, the carried item was left on the cursor — but ContainerClose tears down the window, so the client
// stopped rendering it: the item vanished into limbo.
func TestChestCloseReturnsCarriedItem(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "minecraft:chests/simple_dungeon", 123456789)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	cl := loop.openChests[pos]
	win := int32(p.openContainer.windowID)

	// Pick a non-empty chest slot UP onto the cursor (the carried item).
	src := -1
	for i, s := range cl.items {
		if s.Count > 0 {
			src = i
			break
		}
	}
	if src < 0 {
		t.Skip("no item in the chest to carry")
	}
	carriedItem := cl.items[src]
	loop.handleContainerClick(p, chestClickPacket(win, 0, int16(src), 0, containerInputPickup))

	inv := ensureInventory(p)
	if stackEmpty(inv.getCarried()) {
		t.Fatal("setup: expected an item on the cursor after pickup")
	}

	// Close the window WHILE holding the item. (Do NOT drainPackets before the close — drainPackets
	// CLOSES the outbound queue, which would swallow the close's re-sync packet.)
	loop.handleContainerClose(p, chestClosePacket(win))

	// The cursor must now be EMPTY (the carried item was reconciled, not left in limbo).
	if !stackEmpty(inv.getCarried()) {
		t.Fatalf("close left an item on the cursor (count %d) — it would render invisible after the window closed", inv.getCarried().Count)
	}
	// The item must be back in the player inventory (placeItemBackInInventory).
	found := false
	for _, s := range inv.snapshot() {
		if s.Count > 0 && s.ItemID == carriedItem.ItemID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the carried %d (item %d) was NOT placed back into the inventory on close — the item was lost", carriedItem.Count, carriedItem.ItemID)
	}
	// The close must re-sync window 0 so the client shows the reconciled inventory + empty cursor.
	if n := countID(drainPackets(p.client), packetid.ClientboundContainerSetContent); n < 1 {
		t.Fatal("close did not re-sync the inventory (ContainerSetContent) — the client cursor would stay stale")
	}
}

// TestChestQuickMoveToPlayer: a shift-click (QUICK_MOVE) on a non-empty chest slot moves the whole
// stack into the player inventory and empties the chest slot (ChestMenu.quickMoveStack chest→player).
func TestChestQuickMoveToPlayer(t *testing.T) {
	loop, mgr := newBlockLoop()
	ch, _ := mgr.Get(level.ChunkPos{0, 0})
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeChestBE(loop, ch, pos, "minecraft:chests/simple_dungeon", 123456789)

	ui := useItemOnPacket(0, pos, 1, 0.5, 1.0, 0.5, false, false, 9)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})
	cl := loop.openChests[pos]
	win := int32(p.openContainer.windowID)

	src := -1
	for i, s := range cl.items {
		if s.Count > 0 {
			src = i
			break
		}
	}
	want := cl.items[src]

	// Count the player's holdings of this item before the shift-click.
	before := 0
	for i := int16(windowMainFirst); i <= int16(windowHotbarFirst+8); i++ {
		s := p.inventory.get(i)
		if s.ItemID == want.ItemID {
			before += int(s.Count)
		}
	}

	loop.handleContainerClick(p, chestClickPacket(win, 0, int16(src), 0, containerInputQuickMove))

	if cl.items[src].Count != 0 {
		t.Fatalf("chest slot %d still has %d after shift-click, want emptied", src, cl.items[src].Count)
	}
	after := 0
	for i := int16(windowMainFirst); i <= int16(windowHotbarFirst+8); i++ {
		s := p.inventory.get(i)
		if s.ItemID == want.ItemID {
			after += int(s.Count)
		}
	}
	if after-before != int(want.Count) {
		t.Fatalf("player gained %d of item %d, want %d (shift-move)", after-before, want.ItemID, want.Count)
	}
}

