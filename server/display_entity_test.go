package server

// display_entity_test.go — the validation gates for the ITEM FRAME + GLOW ITEM FRAME + ARMOR STAND
// 1:1 port (display_entity.go), asserting the jar-verified behavior against
// net.minecraft.world.entity.decoration.{ItemFrame, GlowItemFrame, ArmorStand} + ArmorStandItem
// (temp/cache/26.2-inner.jar):
//   - an item_frame item on a solid wall face SPAWNS an ItemFrame attached to that wall;
//   - right-click a frame with an item -> it goes into the frame (setItem, count 1) + the held item shrinks;
//   - right-click again -> the frame rotates, cycling 0→1→…→7→0 (the 8 steps, %8 wrap);
//   - break a frame that holds an item -> the framed item pops (frame stays, empties);
//   - break an empty frame -> the frame item (item_frame / glow_item_frame) pops + the frame is removed;
//   - an armor_stand item on a block spawns an ArmorStand at the adjacent bottom-center;
//   - right-click a stand with a helmet -> the HEAD slot is equipped + the held helmet is taken;
//   - a DOUBLE hit within 5 ticks breaks the stand -> the armor_stand item + the equipped items drop.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// displayLoop builds a physics loop with a one-chunk all-air world (a solid wall block placed per-test).
// Mirrors newBoatLoop/newPhysicsLoop; block-tick registration is unnecessary (display entities only
// store-add, never schedule a block tick).
func displayLoop(t *testing.T) (*TickLoop, *level.Chunk) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	return loop, ch
}

// displayPlayer builds a tickPlayer at (x,y,z) holding `count` of item `it` in the selected hotbar slot,
// tracking `trackID` with a capturing client. gameMode survival (drops + consume active).
func displayPlayer(loop *TickLoop, x, y, z float64, it item.Item, count int32, trackID int32) *tickPlayer {
	p := &tickPlayer{
		x: x, y: y, z: z,
		center:   level.ChunkPos{0, 0},
		client:   captureClient(64),
		entityID: 88000,
		gameMode: gameModeSurvival,
		tracked:  map[int32]bool{trackID: true},
	}
	inv := ensureInventory(p)
	if count > 0 {
		inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: pk.VarInt(count), ItemID: pk.VarInt(it.ID)})
	}
	loop.players = append(loop.players, p)
	return p
}

// countItemDrops returns the number of dropped Item entities carrying the given item id across every
// region store (the break drops land via NewItemEntity -> entities.add).
func countItemDrops(loop *TickLoop, itemID int32) int {
	n := 0
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isItem && int32(e.itemStack.ItemID) == itemID {
				n++
			}
		}
	}
	return n
}

// regionColOf resolves the region owning the column of (x,z) for a withRegion wrap in tests.
func (t *TickLoop) regionColOf(x, z float64) *region {
	return t.regionForColumn(columnOf(x, z))
}

// =================================================================================================
// ITEM FRAME
// =================================================================================================

// TestItemFramePlacesOnWall: an item_frame item used on the SOUTH face (3) of a solid wall block spawns
// an ItemFrame in the adjacent cell, marked isFrame with direction SOUTH and rotation 0.
func TestItemFramePlacesOnWall(t *testing.T) {
	loop, ch := displayLoop(t)
	// A solid wall block at (8, 64, 8) — the block the frame hangs on. Clicking its SOUTH face (+Z) puts
	// the frame in cell (8, 64, 9).
	setBlock(ch, 8, 64, 8)
	wall := pk.Position{X: 8, Y: 64, Z: 8}

	p := displayPlayer(loop, 8.5, 64, 9.5, item.ItemFrame, 1, 0)
	inv := ensureInventory(p)

	// Direction SOUTH == 3D-data value 3 (the +Z face).
	var placed bool
	loop.withRegion(loop.regionColOf(9.5, 9.5), func() {
		placed = loop.tryPlaceItemFrame(p, inv, wall, 3)
	})
	if !placed {
		t.Fatal("tryPlaceItemFrame returned false on a solid wall SOUTH face (want a spawned frame)")
	}

	var frame *Entity
	for _, r := range loop.regions {
		for _, e := range r.entities.byID {
			if e.isFrame {
				frame = e
			}
		}
	}
	if frame == nil {
		t.Fatal("no ItemFrame spawned after placement")
	}
	if frame.typ != entity.ItemFrame.ID {
		t.Fatalf("frame typ = %d, want entity.ItemFrame.ID %d", frame.typ, entity.ItemFrame.ID)
	}
	if frame.frameDirection != 3 {
		t.Fatalf("frame direction = %d, want SOUTH (3)", frame.frameDirection)
	}
	if frame.frameBlockX != 8 || frame.frameBlockY != 64 || frame.frameBlockZ != 9 {
		t.Fatalf("frame cell = (%d,%d,%d), want the adjacent cell (8,64,9)", frame.frameBlockX, frame.frameBlockY, frame.frameBlockZ)
	}
	if frame.spawnData != 3 {
		t.Fatalf("frame AddEntity data = %d, want the direction 3D value 3", frame.spawnData)
	}
	// Survival: the frame item is consumed.
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 0 {
		t.Fatalf("frame item not consumed after placement: held count = %d, want 0", got.Count)
	}
}

// TestItemFramePlaceFailsOnAir: a frame item clicked on a NON-solid (air) target does not spawn a frame
// (ItemFrame.survives fails when the support block is not solid).
func TestItemFramePlaceFailsOnAir(t *testing.T) {
	loop, _ := displayLoop(t)
	airBlock := pk.Position{X: 8, Y: 64, Z: 8} // all-air chunk: not a solid support

	p := displayPlayer(loop, 8.5, 64, 9.5, item.ItemFrame, 1, 0)
	inv := ensureInventory(p)

	var placed bool
	loop.withRegion(loop.regionColOf(9.5, 9.5), func() {
		placed = loop.tryPlaceItemFrame(p, inv, airBlock, 3)
	})
	if placed {
		t.Fatal("tryPlaceItemFrame returned true on an air support (want !survives -> no spawn)")
	}
	frames := 0
	for _, r := range loop.regions {
		for _, e := range r.entities.byID {
			if e.isFrame {
				frames++
			}
		}
	}
	if frames != 0 {
		t.Fatalf("a frame spawned on an air support: %d frames, want 0", frames)
	}
}

// TestItemFramePlaceItemThenRotate: right-clicking an empty frame with an item PLACES it (setItem, count
// 1, held shrinks); right-clicking again ROTATES it, cycling through all 8 steps and wrapping 7→0.
func TestItemFramePlaceItemThenRotate(t *testing.T) {
	loop, _ := displayLoop(t)
	var frame *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		frame = loop.spawnItemFrame(8, 64, 8, 3, false)
	})

	// A player holding 5 diamonds (a stack > 1) — placing consumes exactly one.
	p := displayPlayer(loop, 8.5, 64, 8.5, item.Diamond, 5, frame.id)
	inv := ensureInventory(p)

	// First interact: empty frame + held item -> setItem + consume(1).
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if !loop.tryItemFrameInteract(p, frame) {
			t.Fatal("first frame interact returned false (want the frame's SUCCESS)")
		}
	})
	if fi := frame.getFrameItem(); fi.Count != 1 || int32(fi.ItemID) != int32(item.Diamond.ID) {
		t.Fatalf("frame item after place = %+v, want one diamond (count 1)", fi)
	}
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 4 {
		t.Fatalf("held diamonds after place = %d, want 4 (one consumed)", got.Count)
	}
	if frame.getFrameRotation() != 0 {
		t.Fatalf("rotation after place = %d, want 0", frame.getFrameRotation())
	}

	// Now rotate: each subsequent interact (frame already holds an item) increments rotation %8, cycling
	// 0→1→2→…→7→0.
	want := []int32{1, 2, 3, 4, 5, 6, 7, 0}
	for i, w := range want {
		loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
			if !loop.tryItemFrameInteract(p, frame) {
				t.Fatalf("rotate interact %d returned false", i)
			}
		})
		if got := frame.getFrameRotation(); got != w {
			t.Fatalf("rotation step %d = %d, want %d (the 8-step %%8 cycle)", i, got, w)
		}
	}
	// The item must still be framed (rotation never removes it) and the held stack unchanged from 4.
	if fi := frame.getFrameItem(); fi.Count != 1 {
		t.Fatalf("frame lost its item during rotation: %+v", fi)
	}
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 4 {
		t.Fatalf("rotation consumed held items: held = %d, want 4", got.Count)
	}
}

// TestItemFrameBreakWithItemDropsContents: attacking a frame that HOLDS an item pops the framed item (the
// frame survives, emptied) — the ItemFrame.hurtServer shouldDamageDropItem path (withFrame=false).
func TestItemFrameBreakWithItemDropsContents(t *testing.T) {
	loop, _ := displayLoop(t)
	var frame *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		frame = loop.spawnItemFrame(8, 64, 8, 3, false)
		frame.setFrameItem(component.SlotData{Count: 1, ItemID: pk.VarInt(item.Diamond.ID)})
	})
	p := displayPlayer(loop, 8.5, 64, 8.5, item.Item{}, 0, frame.id)

	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		loop.breakItemFrame(frame, p)
	})

	// The framed diamond dropped; the frame item did NOT (the frame survives, only its contents pop).
	if got := countItemDrops(loop, int32(item.Diamond.ID)); got != 1 {
		t.Fatalf("diamond drops after breaking a filled frame = %d, want 1 (the framed contents)", got)
	}
	if got := countItemDrops(loop, int32(item.ItemFrame.ID)); got != 0 {
		t.Fatalf("item_frame drops after a filled-frame attack = %d, want 0 (frame survives)", got)
	}
	// The frame is still present but emptied.
	if _, ok := loop.regionColOf(8.5, 8.5).entities.get(frame.id); !ok {
		t.Fatal("frame was removed on a filled-frame attack (want it to survive, emptied)")
	}
	if fi := frame.getFrameItem(); fi.Count != 0 {
		t.Fatalf("frame still holds an item after the contents-drop: %+v", fi)
	}
}

// TestItemFrameBreakEmptyDropsFrame: attacking an EMPTY frame drops the frame item and REMOVES the frame
// (BlockAttachedEntity.hurtServer -> dropItem withFrame=true). A GLOW frame drops the glow_item_frame item.
func TestItemFrameBreakEmptyDropsFrame(t *testing.T) {
	loop, _ := displayLoop(t)
	var frame, glow *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		frame = loop.spawnItemFrame(8, 64, 8, 3, false)
	})
	loop.withRegion(loop.regionColOf(2.5, 2.5), func() {
		glow = loop.spawnItemFrame(2, 64, 2, 3, true)
	})
	p := displayPlayer(loop, 8.5, 64, 8.5, item.Item{}, 0, frame.id)

	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		loop.breakItemFrame(frame, p)
	})
	loop.withRegion(loop.regionColOf(2.5, 2.5), func() {
		loop.breakItemFrame(glow, p)
	})

	if got := countItemDrops(loop, int32(item.ItemFrame.ID)); got != 1 {
		t.Fatalf("item_frame drops after breaking an empty frame = %d, want 1", got)
	}
	if got := countItemDrops(loop, int32(item.GlowItemFrame.ID)); got != 1 {
		t.Fatalf("glow_item_frame drops after breaking an empty glow frame = %d, want 1 (getFrameItemStack)", got)
	}
	// Both frames removed.
	if _, ok := loop.regionColOf(8.5, 8.5).entities.get(frame.id); ok {
		t.Fatal("empty frame not removed after break")
	}
	if _, ok := loop.regionColOf(2.5, 2.5).entities.get(glow.id); ok {
		t.Fatal("empty glow frame not removed after break")
	}
}

// =================================================================================================
// ARMOR STAND
// =================================================================================================

// TestArmorStandPlaces: an armor_stand item used on the UP face (1) of a block spawns an ArmorStand at the
// adjacent cell's bottom-center, with the default flags (0) and no equipment.
func TestArmorStandPlaces(t *testing.T) {
	loop, ch := displayLoop(t)
	setBlock(ch, 8, 63, 8) // the floor block; click its top (UP) face -> spawn at (8,64,8)
	floor := pk.Position{X: 8, Y: 63, Z: 8}

	p := displayPlayer(loop, 8.5, 64, 8.5, item.ArmorStand, 1, 0)
	inv := ensureInventory(p)

	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if !loop.tryPlaceArmorStand(p, inv, floor, 1) { // UP face
			t.Fatal("tryPlaceArmorStand returned false on a block UP face (want a spawned stand)")
		}
	})

	var stand *Entity
	for _, r := range loop.regions {
		for _, e := range r.entities.byID {
			if e.isArmorStand {
				stand = e
			}
		}
	}
	if stand == nil {
		t.Fatal("no ArmorStand spawned after placement")
	}
	if stand.typ != entity.ArmorStand.ID {
		t.Fatalf("stand typ = %d, want entity.ArmorStand.ID %d", stand.typ, entity.ArmorStand.ID)
	}
	if stand.x != 8.5 || stand.y != 64 || stand.z != 8.5 {
		t.Fatalf("stand pos = (%v,%v,%v), want bottom-center of (8,64,8) = (8.5,64,8.5)", stand.x, stand.y, stand.z)
	}
	if stand.armorStandFlags != 0 {
		t.Fatalf("fresh stand flags = %d, want 0 (full-size, no arms, baseplate, not marker)", stand.armorStandFlags)
	}
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 0 {
		t.Fatalf("armor_stand item not consumed: held = %d, want 0", got.Count)
	}
}

// TestArmorStandPlaceFailsOnDownFace: clicking the DOWN face (0) with an armor_stand fails (ArmorStandItem
// .useOn: clickedFace == DOWN -> FAIL) — no stand spawns.
func TestArmorStandPlaceFailsOnDownFace(t *testing.T) {
	loop, ch := displayLoop(t)
	setBlock(ch, 8, 65, 8)
	block := pk.Position{X: 8, Y: 65, Z: 8}
	p := displayPlayer(loop, 8.5, 64, 8.5, item.ArmorStand, 1, 0)
	inv := ensureInventory(p)

	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if loop.tryPlaceArmorStand(p, inv, block, 0) { // DOWN face
			t.Fatal("tryPlaceArmorStand returned true on the DOWN face (want FAIL)")
		}
	})
	for _, r := range loop.regions {
		for _, e := range r.entities.byID {
			if e.isArmorStand {
				t.Fatal("a stand spawned on the DOWN face (want none)")
			}
		}
	}
}

// TestArmorStandEquipHelmet: right-clicking a stand with a helmet (clickY in the HEAD band, ≥1.6) equips
// the HEAD slot and TAKES the held helmet (the empty-slot swapItem: setItemSlot(HEAD, held), hand cleared).
func TestArmorStandEquipHelmet(t *testing.T) {
	loop, _ := displayLoop(t)
	var stand *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		stand = loop.spawnArmorStand(8.5, 64, 8.5, 0)
	})

	// A player holding ONE iron helmet (a single item -> the straight-swap branch).
	p := displayPlayer(loop, 8.5, 64, 8.5, item.IronHelmet, 1, stand.id)
	inv := ensureInventory(p)

	// clickY 1.8 is in the HEAD band (≥ 1.6); but getClickedSlot's HEAD branch requires the slot to ALREADY
	// hold an item (it is a TAKE picker). For an EQUIP (held helmet), the slot is chosen by
	// getEquipmentSlotForItem(held) == HEAD (iron_helmet), independent of clickY. So the equip lands in HEAD.
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if !loop.tryArmorStandInteract(p, stand, 1.8) {
			t.Fatal("armor-stand equip interact returned false (want the stand's SUCCESS)")
		}
	})

	head := stand.getItemBySlot(eqSlotHead)
	if head.Count != 1 || int32(head.ItemID) != int32(item.IronHelmet.ID) {
		t.Fatalf("HEAD slot after equip = %+v, want one iron_helmet", head)
	}
	// The single helmet was taken from the hand (straight swap: the empty old slot goes to hand -> empty).
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 0 {
		t.Fatalf("held item after equip = %+v, want empty (the helmet moved to the stand)", got)
	}
}

// TestArmorStandTakeHelmet: an EMPTY-hand right-click at the HEAD band (clickY ≥ 1.6) on a stand wearing a
// helmet TAKES it back (getClickedSlot -> HEAD; swapItem: slot's item -> hand, empty held -> slot).
func TestArmorStandTakeHelmet(t *testing.T) {
	loop, _ := displayLoop(t)
	var stand *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		stand = loop.spawnArmorStand(8.5, 64, 8.5, 0)
		stand.setItemSlot(eqSlotHead, itemStackOf(item.IronHelmet))
	})

	p := displayPlayer(loop, 8.5, 64, 8.5, item.Item{}, 0, stand.id) // empty hand
	inv := ensureInventory(p)

	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if !loop.tryArmorStandInteract(p, stand, 1.8) { // HEAD band
			t.Fatal("armor-stand take interact returned false")
		}
	})

	if h := stand.getItemBySlot(eqSlotHead); h.Count != 0 {
		t.Fatalf("HEAD slot after take = %+v, want empty", h)
	}
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 1 || int32(got.ItemID) != int32(item.IronHelmet.ID) {
		t.Fatalf("hand after take = %+v, want the iron_helmet", got)
	}
}

// TestArmorStandDoubleHitBreaks: a SINGLE punch only wobbles (records lastHit, no break); a SECOND punch
// within 5 ticks breaks the stand -> the armor_stand item + every equipped item drop.
func TestArmorStandDoubleHitBreaks(t *testing.T) {
	loop, _ := displayLoop(t)
	var stand *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		stand = loop.spawnArmorStand(8.5, 64, 8.5, 0)
		stand.setItemSlot(eqSlotHead, itemStackOf(item.IronHelmet))
		stand.setItemSlot(eqSlotFeet, itemStackOf(item.DiamondBoots))
	})
	p := displayPlayer(loop, 8.5, 64, 8.5, item.Item{}, 0, stand.id)

	// FIRST hit at gametime 100: wobble only, no break, no drops.
	loop.gametime = 100
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() { loop.hitArmorStand(stand, p) })
	if _, ok := loop.regionColOf(8.5, 8.5).entities.get(stand.id); !ok {
		t.Fatal("stand broke on the FIRST hit (want only a wobble)")
	}
	if got := countItemDrops(loop, int32(item.ArmorStand.ID)); got != 0 {
		t.Fatalf("armor_stand drops after ONE hit = %d, want 0", got)
	}
	if stand.armorStandLastHit != 100 {
		t.Fatalf("lastHit after first punch = %d, want 100", stand.armorStandLastHit)
	}

	// SECOND hit at gametime 103 (within 5 ticks) -> break + drop the stand + equipment.
	loop.gametime = 103
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() { loop.hitArmorStand(stand, p) })
	if _, ok := loop.regionColOf(8.5, 8.5).entities.get(stand.id); ok {
		t.Fatal("stand did not break on the SECOND hit within 5 ticks")
	}
	if got := countItemDrops(loop, int32(item.ArmorStand.ID)); got != 1 {
		t.Fatalf("armor_stand item drops after break = %d, want 1 (brokenByPlayer)", got)
	}
	if got := countItemDrops(loop, int32(item.IronHelmet.ID)); got != 1 {
		t.Fatalf("iron_helmet drops after break = %d, want 1 (equipment)", got)
	}
	if got := countItemDrops(loop, int32(item.DiamondBoots.ID)); got != 1 {
		t.Fatalf("diamond_boots drops after break = %d, want 1 (equipment)", got)
	}
}

// TestArmorStandGetClickedSlotThresholds asserts the exact y-band slot picker (ArmorStand.getClickedSlot)
// for a full-size stand with all slots occupied — the FEET/LEGS/CHEST/HEAD bands.
func TestArmorStandGetClickedSlotThresholds(t *testing.T) {
	loop, _ := displayLoop(t)
	var stand *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		stand = loop.spawnArmorStand(8.5, 64, 8.5, 0)
	})
	// Occupy every slot so each band's hasItemInSlot gate passes.
	stand.setItemSlot(eqSlotFeet, itemStackOf(item.DiamondBoots))
	stand.setItemSlot(eqSlotLegs, itemStackOf(item.IronLeggings))
	stand.setItemSlot(eqSlotChest, itemStackOf(item.IronChestplate))
	stand.setItemSlot(eqSlotHead, itemStackOf(item.IronHelmet))
	stand.setItemSlot(eqSlotMainHand, itemStackOf(item.Diamond))

	cases := []struct {
		y    float64
		want int
		name string
	}{
		{0.2, eqSlotFeet, "FEET band (>=0.1, <0.55)"},
		{0.5, eqSlotLegs, "LEGS band (>=0.4, <1.2) wins over FEET upper at 0.5? FEET is <0.55 so 0.5->FEET"},
		{1.0, eqSlotChest, "CHEST band (>=0.9, <1.6)"},
		{1.7, eqSlotHead, "HEAD band (>=1.6)"},
		{0.05, eqSlotMainHand, "below all armor bands -> MAINHAND"},
	}
	for _, c := range cases {
		got := stand.getClickedSlot(c.y)
		// The overlapping bands are order-sensitive (FEET checked first, then CHEST, then LEGS, then HEAD);
		// assert the jar order exactly.
		_ = c.name
		if c.y == 0.5 {
			// 0.5 is in FEET's [0.1,0.55) AND LEGS' [0.4,1.2); FEET is checked FIRST -> FEET.
			if got != eqSlotFeet {
				t.Fatalf("getClickedSlot(0.5) = %d, want FEET (checked before LEGS)", got)
			}
			continue
		}
		if got != c.want {
			t.Fatalf("getClickedSlot(%.2f) = %d, want %d (%s)", c.y, got, c.want, c.name)
		}
	}
}

// =================================================================================================
// ITEM FRAME - COMPARATOR ANALOG OUTPUT
// =================================================================================================

// TestItemFrameAnalogOutput asserts ItemFrame.getAnalogOutput: 0 for an empty frame, else rotation%8 + 1
// (1..8) as the framed item is rotated through all eight steps. CITE ItemFrame.getAnalogOutput.
func TestItemFrameAnalogOutput(t *testing.T) {
	loop, _ := displayLoop(t)
	var frame *Entity
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		frame = loop.spawnItemFrame(8, 64, 8, 3, false)
	})

	// Empty frame -> analog 0.
	if got := frame.frameGetAnalogOutput(); got != 0 {
		t.Fatalf("empty-frame analog output = %d, want 0", got)
	}

	// Put an item -> rotation 0 -> analog 1. Then each rotation step yields rotation+1, wrapping 7 -> 8.
	frame.setFrameItem(component.SlotData{Count: 1, ItemID: pk.VarInt(item.Diamond.ID)})
	want := []int{1, 2, 3, 4, 5, 6, 7, 8}
	for r, w := range want {
		frame.setFrameRotation(int32(r))
		if got := frame.frameGetAnalogOutput(); got != w {
			t.Fatalf("analog output at rotation %d = %d, want %d (rotation%%8 + 1)", r, got, w)
		}
	}
	// Rotation 8 wraps to 0 (%8) -> analog 1 again.
	frame.setFrameRotation(8)
	if got := frame.frameGetAnalogOutput(); got != 1 {
		t.Fatalf("analog output at rotation 8 (wraps to 0) = %d, want 1", got)
	}
}

// TestComparatorReadsItemFrameThroughConductor asserts ComparatorBlock.getInputSignal's item-frame branch:
// a comparator FACING a solid conductor, with an item frame hung on the FAR face of that conductor (facing
// the same direction as the comparator), reads the frame's analog output THROUGH the block. An empty frame
// gives 0; a framed item at rotation r gives r%8 + 1. CITE ComparatorBlock.getInputSignal + getItemFrame +
// ItemFrame.getAnalogOutput.
func TestComparatorReadsItemFrameThroughConductor(t *testing.T) {
	loop, ch := displayLoop(t)
	_ = ch

	// Comparator C at (8,64,8) FACING South. C+South is a solid conductor (stone). The frame lives in cell
	// C+2*South, facing South (3) -- hung on the far face of the conductor, pointing away from C.
	c := pk.Position{X: 8, Y: 64, Z: 8}
	mgr := loop.world()
	mgr.SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, stoneState(), dimMinY)       // sturdy floor under C
	mgr.SetBlock(c, block.ToStateID[block.Comparator{Facing: block.South, Mode: block.ComparatorModeCompare, Powered: false}], dimMinY)
	mgr.SetBlock(pk.Position{X: 8, Y: 64, Z: 9}, stoneState(), dimMinY)       // the conductor at C+South
	comp := loop.redstoneBlockAt(c)

	// Spawn the frame in cell (8,64,10) == C+2*South, facing South.
	var frame *Entity
	loop.withRegion(loop.regionColOf(8.5, 10.5), func() {
		frame = loop.spawnItemFrame(8, 64, 10, 3, false)
	})

	// Empty frame: getAnalogOutput 0 -> the frame branch's max is 0 (a real value), so input becomes 0.
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if got := loop.comparatorGetInputSignal(comp, c); got != 0 {
			t.Fatalf("comparator input with an EMPTY frame behind the conductor = %d, want 0", got)
		}
	})

	// Framed diamond at rotation 0 -> analog 1 -> comparator input 1, output 1 (compare, no side).
	frame.setFrameItem(component.SlotData{Count: 1, ItemID: pk.VarInt(item.Diamond.ID)})
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if got := loop.comparatorGetInputSignal(comp, c); got != 1 {
			t.Fatalf("comparator input with a framed item (rotation 0) = %d, want 1", got)
		}
		if got := loop.comparatorCalculateOutputSignal(comp, c); got != 1 {
			t.Fatalf("comparator output with a framed item (rotation 0) = %d, want 1", got)
		}
	})

	// Rotate to 4 -> analog 5 -> comparator output 5.
	frame.setFrameRotation(4)
	loop.withRegion(loop.regionColOf(8.5, 8.5), func() {
		if got := loop.comparatorCalculateOutputSignal(comp, c); got != 5 {
			t.Fatalf("comparator output with a framed item (rotation 4) = %d, want 5 (rotation%%8 + 1)", got)
		}
	})
}
