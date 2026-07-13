package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

func composterAt(level int) block.StateID {
	s, ok := block.ToStateID[block.Composter{Level: block.Integer(level)}]
	if !ok {
		panic("no composter state")
	}
	return s
}

func newComposterPlayer(loop *TickLoop, itemID int32) *tickPlayer {
	p := &tickPlayer{x: 5, y: 65, z: 5}
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 4, ItemID: pk.VarInt(itemID)})
	loop.players = append(loop.players, p)
	return p
}

func TestComposterChancesMatchJar(t *testing.T) {
	cases := []struct {
		name   string
		chance float32
		key    bool
	}{
		{"minecraft:oak_leaves", 0.3, true},
		{"minecraft:cactus", 0.5, true},
		{"minecraft:pumpkin", 0.65, true},
		{"minecraft:hay_block", 0.85, true},
		{"minecraft:cake", 1.0, true},
		{"minecraft:pumpkin_pie", 1.0, true},
		{"minecraft:stone", 0, false},
		{"minecraft:diamond", 0, false},
	}
	for _, c := range cases {
		got, key := composterChance(c.name)
		if key != c.key {
			t.Fatalf("%s: containsKey=%v want %v", c.name, key, c.key)
		}
		if key && got != c.chance {
			t.Fatalf("%s: chance=%v want %v", c.name, got, c.chance)
		}
	}
	if len(composterCompostables) != 115 {
		t.Fatalf("COMPOSTABLES size=%d want 115", len(composterCompostables))
	}
}

func TestComposterFreeFillNoRNG(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	const seed = int64(0x1234)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(0), dimMinY)
	p := newComposterPlayer(loop, 209)

	if !loop.useComposter(p, pos, composterAt(0)) {
		t.Fatal("compostable click should consume the action")
	}
	if got := mustGet(t, mgr, pos); got != composterAt(1) {
		t.Fatalf("free-fill: LEVEL not bumped to 1 (got %d)", got)
	}
	fresh := levelgen.NewLegacyRandomSource(seed)
	if r.levelRandom.NextDouble() != fresh.NextDouble() {
		t.Fatal("free-fill drew RNG (should short-circuit before nextDouble)")
	}
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)).Count; got != 3 {
		t.Fatalf("held count=%d want 3", got)
	}
}

func TestComposterRollDrawsOneNextDouble(t *testing.T) {
	var passSeed int64
	for s := int64(1); s < 100000; s++ {
		if levelgen.NewLegacyRandomSource(s).NextDouble() < 0.3 {
			passSeed = s
			break
		}
	}
	if passSeed == 0 {
		t.Fatal("no passing seed found")
	}
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(passSeed)

	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(3), dimMinY)
	p := newComposterPlayer(loop, 209)

	if !loop.useComposter(p, pos, composterAt(3)) {
		t.Fatal("compostable click should consume the action")
	}
	if got := mustGet(t, mgr, pos); got != composterAt(4) {
		t.Fatalf("passing roll: LEVEL not bumped to 4 (got %d)", got)
	}
	fresh := levelgen.NewLegacyRandomSource(passSeed)
	fresh.NextDouble()
	if r.levelRandom.NextDouble() != fresh.NextDouble() {
		t.Fatal("fill did not draw exactly one nextDouble")
	}
}

func TestComposterRejectedRoll(t *testing.T) {
	var failSeed int64
	for s := int64(1); s < 100000; s++ {
		if levelgen.NewLegacyRandomSource(s).NextDouble() >= 0.3 {
			failSeed = s
			break
		}
	}
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(failSeed)

	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(3), dimMinY)
	p := newComposterPlayer(loop, 209)

	if !loop.useComposter(p, pos, composterAt(3)) {
		t.Fatal("compostable click should still consume the action on a rejected roll")
	}
	if got := mustGet(t, mgr, pos); got != composterAt(3) {
		t.Fatalf("rejected roll: LEVEL should stay 3 (got %d)", got)
	}
}

func TestComposterReadyTickCycle(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	var passSeed int64
	for s := int64(1); s < 100000; s++ {
		if levelgen.NewLegacyRandomSource(s).NextDouble() < 0.3 {
			passSeed = s
			break
		}
	}
	r.levelRandom = levelgen.NewLegacyRandomSource(passSeed)

	loop.ensureChunkBlockTicks(level.ChunkPos{0, 0})
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(6), dimMinY)
	p := newComposterPlayer(loop, 209)

	loop.useComposter(p, pos, composterAt(6))
	if got := mustGet(t, mgr, pos); got != composterAt(7) {
		t.Fatalf("fill to 7 failed (got %d)", got)
	}
	if !loop.hasScheduledBlockTick(pos, composterTickType) {
		t.Fatal("reaching LEVEL 7 did not schedule the READY tick")
	}
	loop.composterTick(composterAt(7), pos)
	if got := mustGet(t, mgr, pos); got != composterAt(8) {
		t.Fatalf("composterTick did not cycle 7 to 8 (got %d)", got)
	}
}

func TestComposterExtractProduce(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	r.levelRandom = levelgen.NewLegacyRandomSource(0x99)

	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(8), dimMinY)
	p := &tickPlayer{x: 5, y: 65, z: 5}
	ensureInventory(p)
	loop.players = append(loop.players, p)

	before := loop.only().entities.len()
	if !loop.useComposter(p, pos, composterAt(8)) {
		t.Fatal("READY composter click should consume the action")
	}
	if got := mustGet(t, mgr, pos); got != composterAt(0) {
		t.Fatalf("extract: LEVEL should reset to 0 (got %d)", got)
	}
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("extract: expected +1 ItemEntity, got %d (before %d)", got, before)
	}
}

func TestComposterNonCompostablePasses(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(2), dimMinY)
	p := newComposterPlayer(loop, 1)

	if loop.useComposter(p, pos, composterAt(2)) {
		t.Fatal("non-compostable on a non-READY composter should PASS")
	}
	if got := mustGet(t, mgr, pos); got != composterAt(2) {
		t.Fatalf("non-compostable click should not change LEVEL (got %d)", got)
	}
}

func TestComposterAnalogOutput(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	for lvl := 0; lvl <= 8; lvl++ {
		mgr.SetBlock(pos, composterAt(lvl), dimMinY)
		sig, has := loop.composterAnalogOutputSignal(pos)
		if !has || sig != lvl {
			t.Fatalf("LEVEL %d: analog=(%d,%v) want (%d,true)", lvl, sig, has, lvl)
		}
	}
	stone, _ := block.ToStateID[block.Stone{}]
	mgr.SetBlock(pos, stone, dimMinY)
	if _, has := loop.composterAnalogOutputSignal(pos); has {
		t.Fatal("stone should not produce composter analog output")
	}
}


// TestComposterHopperInsert drives the InputContainer path exactly as HopperBlockEntity.tryMoveInItem
// does: getContainer -> canPlaceItemThroughFace(UP) -> setItem(0) -> setChanged (addItem + clear). A
// compostable inserted from the UP face at LEVEL 0 (free-fill, chance>0) bumps LEVEL to 1 with no RNG.
func TestComposterHopperInsert(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	r := loop.only()
	const seed = int64(0x1234)
	r.levelRandom = levelgen.NewLegacyRandomSource(seed)

	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(0), dimMinY)

	cv := loop.getContainerAt(pos)
	if cv == nil {
		t.Fatal("getContainerAt returned nil for a LEVEL-0 composter")
	}
	if !cv.isWorldly() {
		t.Fatal("composter container must be a WorldlyContainer")
	}
	leaves := component.SlotData{ItemID: pk.VarInt(209), Count: 1}
	// UP face exposes slot 0; a non-UP face exposes none.
	if slots := cv.getSlotsForFace(block.Up); len(slots) != 1 || slots[0] != 0 {
		t.Fatalf("UP getSlotsForFace = %v, want [0]", slots)
	}
	if slots := cv.getSlotsForFace(block.Down); len(slots) != 0 {
		t.Fatalf("DOWN getSlotsForFace = %v, want []", slots)
	}
	if !cv.canPlaceItemThroughFace(0, leaves, block.Up) {
		t.Fatal("compostable should be placeable through the UP face")
	}
	if cv.canPlaceItemThroughFace(0, leaves, block.Down) {
		t.Fatal("compostable must NOT be placeable through the DOWN face")
	}
	if cv.canTakeItemThroughFace(0, leaves, block.Up) {
		t.Fatal("InputContainer must never allow taking")
	}
	// tryMoveInItem: setItem(0, one-item stack) then setChanged.
	cv.setItem(0, leaves)
	cv.setChanged()
	if got := mustGet(t, mgr, pos); got != composterAt(1) {
		t.Fatalf("hopper insert: LEVEL not bumped to 1 (got %d)", got)
	}
	// free-fill drew no RNG.
	fresh := levelgen.NewLegacyRandomSource(seed)
	if r.levelRandom.NextDouble() != fresh.NextDouble() {
		t.Fatal("hopper free-fill drew RNG (should short-circuit before nextDouble)")
	}
	// The changed guard now blocks a second insert on the SAME container view.
	if cv.canPlaceItemThroughFace(0, leaves, block.Up) {
		t.Fatal("changed InputContainer must reject a second insert")
	}
}

// TestComposterHopperExtract drives the OutputContainer path exactly as HopperBlockEntity does when a
// hopper below pulls: getContainer (LEVEL 8) -> canTakeItemThroughFace(DOWN, bone_meal) -> removeItem ->
// setChanged (empty -> LEVEL 0).
func TestComposterHopperExtract(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(8), dimMinY)

	cv := loop.getContainerAt(pos)
	if cv == nil {
		t.Fatal("getContainerAt returned nil for a READY composter")
	}
	if cv.getContainerSize() != 1 {
		t.Fatalf("OutputContainer size = %d, want 1", cv.getContainerSize())
	}
	boneMeal := cv.getItem(0)
	if boneMeal.Count != 1 || int32(boneMeal.ItemID) != int32(toItemID(boneMealItemID)) {
		t.Fatalf("OutputContainer slot 0 = %+v, want one bone_meal", boneMeal)
	}
	// DOWN face exposes slot 0; taking bone_meal through DOWN is allowed; placing is not.
	if slots := cv.getSlotsForFace(block.Down); len(slots) != 1 || slots[0] != 0 {
		t.Fatalf("DOWN getSlotsForFace = %v, want [0]", slots)
	}
	if !cv.canTakeItemThroughFace(0, boneMeal, block.Down) {
		t.Fatal("bone_meal should be takeable through the DOWN face")
	}
	if cv.canTakeItemThroughFace(0, boneMeal, block.Up) {
		t.Fatal("bone_meal must NOT be takeable through the UP face")
	}
	if cv.canPlaceItemThroughFace(0, boneMeal, block.Down) {
		t.Fatal("OutputContainer must never allow placing")
	}
	// tryMoveOutItem: removeItem then setChanged.
	cv.setItem(0, component.SlotData{Count: 0})
	cv.setChanged()
	if got := mustGet(t, mgr, pos); got != composterAt(0) {
		t.Fatalf("hopper extract: LEVEL not reset to 0 (got %d)", got)
	}
	// changed guard blocks a second take on the SAME view.
	if cv.canTakeItemThroughFace(0, boneMeal, block.Down) {
		t.Fatal("changed OutputContainer must reject a second take")
	}
}

// TestComposterEmptyContainerAtLevel7 verifies LEVEL 7 (full-but-not-READY) returns the 0-slot
// EmptyContainer: no face exposes a slot, nothing may be placed or taken. CITE ComposterBlock EmptyContainer.
func TestComposterEmptyContainerAtLevel7(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(7), dimMinY)

	cv := loop.getContainerAt(pos)
	if cv == nil {
		t.Fatal("getContainerAt returned nil for a LEVEL-7 composter")
	}
	if cv.getContainerSize() != 0 {
		t.Fatalf("EmptyContainer size = %d, want 0", cv.getContainerSize())
	}
	leaves := component.SlotData{ItemID: pk.VarInt(209), Count: 1}
	if len(cv.getSlotsForFace(block.Up)) != 0 || len(cv.getSlotsForFace(block.Down)) != 0 {
		t.Fatal("EmptyContainer must expose no slots on any face")
	}
	if cv.canPlaceItemThroughFace(0, leaves, block.Up) || cv.canTakeItemThroughFace(0, leaves, block.Down) {
		t.Fatal("EmptyContainer must reject place and take")
	}
}

// TestComposterHopperInsertRejectsNonUpAndNonCompostable verifies the InputContainer face + compostable
// gate: a non-compostable from UP, or a compostable from a non-UP face, is rejected.
func TestComposterHopperInsertRejectsNonUpAndNonCompostable(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	pos := pk.Position{X: 5, Y: 65, Z: 5}
	mgr.SetBlock(pos, composterAt(2), dimMinY)

	cv := loop.getContainerAt(pos)
	if cv == nil {
		t.Fatal("getContainerAt returned nil for a LEVEL-2 composter")
	}
	nonCompost := component.SlotData{ItemID: pk.VarInt(1), Count: 1}
	if cv.canPlaceItemThroughFace(0, nonCompost, block.Up) {
		t.Fatal("a non-compostable must not be placeable")
	}
	leaves := component.SlotData{ItemID: pk.VarInt(209), Count: 1}
	if cv.canPlaceItemThroughFace(0, leaves, block.North) {
		t.Fatal("a compostable from a non-UP face must be rejected")
	}
}
