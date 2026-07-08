package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// be4_test.go -- validation gates for the four self-contained block-entities ported 1:1 from the 26.2 jar
// (temp/cache/26.2-inner.jar): CampfireBlockEntity (cookTick + placeFood), BellBlockEntity (onHit + tick),
// LecternBlockEntity (page -> getRedstoneSignal), JukeboxBlockEntity (disc -> HAS_RECORD + comparator).

// newBELoop wires a TickLoop with ready all-air chunks around the origin + a block-tick container (the
// newConduitLoop twin). gametime starts at 0.
func newBELoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	for _, cp := range []level.ChunkPos{{0, 0}, {-1, 0}, {0, -1}, {-1, -1}, {1, 0}, {0, 1}, {1, 1}, {-1, 1}, {1, -1}} {
		ch := level.EmptyChunk(blockTestSecs)
		ch.Status = level.StatusFull
		mgr.Insert(cp, ch)
	}
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

// TestCampfireCooksAndDrops asserts CampfireBlockEntity.cookTick: a beef slot with cookingTime 600 cooks
// for exactly 600 ticks then drops cooked_beef and clears the slot.
func TestCampfireCooksAndDrops(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:campfire"], dimMinY)

	c := loop.resolveCampfire(pos)
	if c == nil {
		t.Fatal("resolveCampfire returned nil for a placed campfire")
	}
	beef := int32(itemNameToID("beef"))
	cookedBeef := int32(itemNameToID("cooked_beef"))
	if !loop.campfirePlaceFood(nil, c, component.SlotData{ItemID: toItemID(int(beef)), Count: 1}) {
		t.Fatal("campfirePlaceFood(beef) returned false; expected a campfire-cooking match")
	}
	if c.cookingTime[0] != 600 {
		t.Fatalf("cookingTime[0] = %d, want 600", c.cookingTime[0])
	}
	if stackEmpty(c.items[0]) {
		t.Fatal("slot 0 empty after placeFood")
	}

	before := loop.only().entities.len()
	for i := 0; i < 599; i++ {
		loop.campfireCookTick(pos, c)
	}
	if c.cookingProgress[0] != 599 {
		t.Fatalf("cookingProgress[0] = %d after 599 ticks, want 599", c.cookingProgress[0])
	}
	if stackEmpty(c.items[0]) {
		t.Fatal("slot 0 cleared before cookingTime reached")
	}
	if loop.only().entities.len() != before {
		t.Fatal("an item dropped before the cook finished")
	}
	loop.campfireCookTick(pos, c)
	if !stackEmpty(c.items[0]) {
		t.Fatal("slot 0 not cleared after cooking finished")
	}
	if loop.only().entities.len() != before+1 {
		t.Fatalf("entities len = %d, want %d", loop.only().entities.len(), before+1)
	}
	found := false
	for _, e := range loop.only().entities.near(float64(pos.X)+0.5, float64(pos.Z)+0.5, 2) {
		if e.isItem && int32(e.itemStack.ItemID) == cookedBeef {
			found = true
		}
	}
	if !found {
		t.Fatalf("no dropped cooked_beef (id %d) near the campfire", cookedBeef)
	}
}

// TestCampfirePlaceFoodFull asserts placeFood returns false when all 4 slots are full and for a non-recipe.
func TestCampfirePlaceFoodFull(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 2, Y: 5, Z: 2}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:campfire"], dimMinY)
	c := loop.resolveCampfire(pos)
	beef := component.SlotData{ItemID: toItemID(int(itemNameToID("beef"))), Count: 64}
	for i := 0; i < campfireNumSlots; i++ {
		if !loop.campfirePlaceFood(nil, c, beef) {
			t.Fatalf("placeFood #%d returned false", i)
		}
	}
	if loop.campfirePlaceFood(nil, c, beef) {
		t.Fatal("placeFood into a full campfire returned true; want false")
	}
	loop2, mgr2 := newBELoop()
	mgr2.SetBlock(pos, block.DefaultStateID["minecraft:campfire"], dimMinY)
	c2 := loop2.resolveCampfire(pos)
	stone := component.SlotData{ItemID: toItemID(int(itemNameToID("stone"))), Count: 1}
	if loop2.campfirePlaceFood(nil, c2, stone) {
		t.Fatal("placeFood(stone) returned true; want false")
	}
}

// TestLecternPageComparator asserts LecternBlockEntity.getRedstoneSignal via the analog-output seam: an
// empty lectern reads 0; a lectern with a placed book (pageCount 1 in v1) reads 15 (floor(1.0*14)+1); after
// taking the book it reads 0 again.
func TestLecternPageComparator(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 3, Y: 5, Z: 3}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:lectern"], dimMinY)

	if sig, has := loop.lecternAnalogOutputSignal(pos); !has || sig != 0 {
		t.Fatalf("empty lectern analog = (%d,%v), want (0,true)", sig, has)
	}

	book := component.SlotData{ItemID: toItemID(int(itemNameToID("writable_book"))), Count: 1}
	state, _ := mgr.GetBlock(pos, dimMinY)
	if !loop.lecternTryPlaceBook(nil, pos, state, book) {
		t.Fatal("lecternTryPlaceBook(writable_book) returned false")
	}
	newState, _ := mgr.GetBlock(pos, dimMinY)
	if !block.LecternHasBook(newState) {
		t.Fatal("HAS_BOOK not set after tryPlaceBook")
	}
	if sig, has := loop.lecternAnalogOutputSignal(pos); !has || sig != 15 {
		t.Fatalf("lectern-with-book analog = (%d,%v), want (15,true)", sig, has)
	}

	if !loop.takeLecternBook(pos, newState) {
		t.Fatal("takeLecternBook returned false for a book-bearing lectern")
	}
	clearedState, _ := mgr.GetBlock(pos, dimMinY)
	if block.LecternHasBook(clearedState) {
		t.Fatal("HAS_BOOK still set after takeLecternBook")
	}
	if sig, has := loop.lecternAnalogOutputSignal(pos); !has || sig != 0 {
		t.Fatalf("emptied lectern analog = (%d,%v), want (0,true)", sig, has)
	}
}

// TestJukeboxDiscComparator asserts JukeboxBlockEntity: inserting a music disc sets HAS_RECORD and the
// comparator reads the disc song comparator_output; ejecting clears HAS_RECORD and drops the disc.
func TestJukeboxDiscComparator(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 4, Y: 5, Z: 4}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:jukebox"], dimMinY)
	j := loop.resolveJukebox(pos)
	if j == nil {
		t.Fatal("resolveJukebox returned nil for a placed jukebox")
	}

	if sig, has := loop.jukeboxAnalogOutputSignal(pos); !has || sig != 0 {
		t.Fatalf("empty jukebox analog = (%d,%v), want (0,true)", sig, has)
	}

	disc := component.SlotData{ItemID: toItemID(int(itemNameToID("music_disc_cat"))), Count: 1}
	loop.jukeboxSetTheItem(pos, j, disc)
	loaded, _ := mgr.GetBlock(pos, dimMinY)
	if !block.JukeboxHasRecord(loaded) {
		t.Fatal("HAS_RECORD not set after inserting a disc")
	}
	if sig, has := loop.jukeboxAnalogOutputSignal(pos); !has || sig != 2 {
		t.Fatalf("jukebox-with-cat analog = (%d,%v), want (2,true)", sig, has)
	}

	before := loop.only().entities.len()
	if !loop.jukeboxPopOutTheItem(pos, j) {
		t.Fatal("jukeboxPopOutTheItem returned false for a loaded jukebox")
	}
	if loop.only().entities.len() != before+1 {
		t.Fatalf("entities len = %d, want %d", loop.only().entities.len(), before+1)
	}
	ejected, _ := mgr.GetBlock(pos, dimMinY)
	if block.JukeboxHasRecord(ejected) {
		t.Fatal("HAS_RECORD still set after popOutTheItem")
	}
	if sig, has := loop.jukeboxAnalogOutputSignal(pos); !has || sig != 0 {
		t.Fatalf("emptied jukebox analog = (%d,%v), want (0,true)", sig, has)
	}
}

// TestBellRingStartsShaking asserts BellBlockEntity.onHit + serverTick: ringing sets shaking=true + ticks=0,
// serverTick counts ticks up while shaking, and shaking stops at DURATION (50).
func TestBellRingStartsShaking(t *testing.T) {
	loop, mgr := newBELoop()
	pos := pk.Position{X: 5, Y: 5, Z: 5}
	mgr.SetBlock(pos, block.DefaultStateID["minecraft:bell"], dimMinY)
	b := loop.resolveBell(pos)
	if b == nil {
		t.Fatal("resolveBell returned nil for a placed bell")
	}
	if b.shaking {
		t.Fatal("a freshly-placed bell is already shaking")
	}
	loop.bellOnHit(b, 2)
	if !b.shaking || b.ticks != 0 {
		t.Fatalf("after onHit: shaking=%v ticks=%d, want true/0", b.shaking, b.ticks)
	}
	for i := 0; i < bellDuration-1; i++ {
		loop.bellServerTick(pos, b)
	}
	if !b.shaking || b.ticks != bellDuration-1 {
		t.Fatalf("after 49 ticks: shaking=%v ticks=%d, want true/49", b.shaking, b.ticks)
	}
	loop.bellServerTick(pos, b)
	if b.shaking || b.ticks != 0 {
		t.Fatalf("after DURATION: shaking=%v ticks=%d, want false/0", b.shaking, b.ticks)
	}
}
