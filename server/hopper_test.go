package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// hopper_test.go — HOPPER block-entity validation gates. Each asserts the ported behaviour against the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar):
//   - a hopper under a chest PULLS one item per 8 ticks into itself (suckInItems);
//   - a hopper pointing into a chest PUSHES one item per 8 ticks (ejectItems);
//   - a redstone-powered hopper (ENABLED=false) transfers NOTHING (the ENABLED gate);
//   - a comparator behind a filled chest reads the analog fullness (getRedstoneSignalFromContainer);
//   - the dropper (loop-10 seam) ejects into a chest/hopper in front (the FILLED getContainerAt seam);
//   - the 8-tick cooldown + the analog fullness formula match the jar exactly.

// newHopperLoop wires a TickLoop with one ready all-air chunk + a registered block-tick container for
// column (0,0). Mirrors newDispenserLoop / newRedstoneLoop.
func newHopperLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

func cobble(n int) component.SlotData {
	return component.SlotData{Count: pk.VarInt(n), ItemID: pk.VarInt(item.Cobblestone.ID)}
}

// countHopper sums the item counts across a hopper's 5 slots.
func countHopper(h *hopperBE) int {
	total := 0
	for _, s := range h.items {
		total += int(s.Count)
	}
	return total
}

// countChest sums the item counts across a chest container's slots.
func countChest(cl *chestLoot) int {
	total := 0
	for _, s := range cl.items {
		total += int(s.Count)
	}
	return total
}

// TestHopperEnabledOnPlace locks HopperBlock.getStateForPlacement + the store default: a placed hopper
// starts ENABLED=true (unpowered) with an empty 5-slot container and cooldown -1. CITE: HopperBlock
// defaultBlockState (ENABLED default true) + HopperBlockEntity.cooldownTime = -1.
func TestHopperEnabledOnPlace(t *testing.T) {
	loop, mgr := newHopperLoop()
	pos := pk.Position{X: 5, Y: 64, Z: 5}
	state := block.ToStateID[block.Hopper{Facing: block.Down, Enabled: true}]
	mgr.SetBlock(pos, state, dimMinY)

	h := loop.resolveHopper(pos, state)
	if h == nil {
		t.Fatal("resolveHopper returned nil for a placed hopper")
	}
	if h.cooldownTime != hopperNoCooldown {
		t.Fatalf("fresh hopper cooldownTime = %d, want %d", h.cooldownTime, hopperNoCooldown)
	}
	if !h.isEmpty() {
		t.Fatal("fresh hopper is not empty")
	}
}

// TestHopperPullFromChestAbove is the SUCK gate: a hopper (facing DOWN) with a chest directly above pulls
// exactly ONE item per 8-tick cooldown from the chest into itself. CITE: HopperBlockEntity.suckInItems ->
// getSourceContainer(pos.above()) -> tryTakeInItemFromSlot (single item) + setCooldown(8).
func TestHopperPullFromChestAbove(t *testing.T) {
	loop, mgr := newHopperLoop()

	hopperPos := pk.Position{X: 5, Y: 64, Z: 5}
	chestPos := pk.Position{X: 5, Y: 65, Z: 5} // directly above
	mgr.SetBlock(hopperPos, block.ToStateID[block.Hopper{Facing: block.Down, Enabled: true}], dimMinY)
	mgr.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)

	h := loop.resolveHopper(hopperPos, block.ToStateID[block.Hopper{Facing: block.Down, Enabled: true}])
	cl := loop.resolveChest(chestPos)
	cl.items[0] = cobble(5) // 5 cobblestone in the chest above

	// First tick: cooldown -1 -> --=-2, !isOnCooldown -> setCooldown(0) -> tryMoveItems pulls ONE item and
	// sets cooldown to 8.
	loop.tickHoppers()
	if got := countHopper(h); got != 1 {
		t.Fatalf("after 1 tick hopper holds %d items, want 1 (single-item suck)", got)
	}
	if got := countChest(cl); got != 4 {
		t.Fatalf("after 1 tick chest holds %d items, want 4", got)
	}
	if h.cooldownTime != hopperMoveItemSpeed {
		t.Fatalf("after a transfer cooldownTime = %d, want %d", h.cooldownTime, hopperMoveItemSpeed)
	}

	// The next 8 ticks: the first 7 tick cooldown 8->1 (isOnCooldown true, no transfer); the 8th brings
	// cooldown to 0 (--) and fires the next single-item move (one item per 8 ticks).
	for i := 0; i < 8; i++ {
		loop.tickHoppers()
	}
	if got := countHopper(h); got != 2 {
		t.Fatalf("after 9 ticks total hopper holds %d items, want 2 (one item per 8 ticks)", got)
	}
	if got := countChest(cl); got != 3 {
		t.Fatalf("after 9 ticks total chest holds %d items, want 3", got)
	}
}

// TestHopperPushIntoChest is the EJECT gate: a hopper facing EAST with a chest in FACING pushes exactly
// ONE item per 8-tick cooldown from itself into the chest. CITE: HopperBlockEntity.ejectItems ->
// getAttachedContainer(pos.relative(FACING)) -> addItem (single item) + setCooldown(8).
func TestHopperPushIntoChest(t *testing.T) {
	loop, mgr := newHopperLoop()

	hopperPos := pk.Position{X: 5, Y: 64, Z: 5}
	chestPos := pk.Position{X: 6, Y: 64, Z: 5} // EAST of the hopper
	hopperState := block.ToStateID[block.Hopper{Facing: block.East, Enabled: true}]
	mgr.SetBlock(hopperPos, hopperState, dimMinY)
	mgr.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)

	h := loop.resolveHopper(hopperPos, hopperState)
	h.items[0] = cobble(3)
	cl := loop.resolveChest(chestPos)

	loop.tickHoppers()
	if got := countHopper(h); got != 2 {
		t.Fatalf("after 1 tick hopper holds %d items, want 2 (single-item eject)", got)
	}
	if got := countChest(cl); got != 1 {
		t.Fatalf("after 1 tick chest holds %d items, want 1", got)
	}
	if h.cooldownTime != hopperMoveItemSpeed {
		t.Fatalf("after a transfer cooldownTime = %d, want %d", h.cooldownTime, hopperMoveItemSpeed)
	}

	for i := 0; i < 8; i++ {
		loop.tickHoppers()
	}
	if got := countHopper(h); got != 1 {
		t.Fatalf("after 8 ticks total hopper holds %d items, want 1", got)
	}
	if got := countChest(cl); got != 2 {
		t.Fatalf("after 8 ticks total chest holds %d items, want 2 (one item per 8 ticks)", got)
	}
}

// TestHopperDisabledByRedstone is the ENABLED-gate gate: a hopper with ENABLED=false (powered) transfers
// NOTHING even with items to push and a container in front. CITE: HopperBlockEntity.tryMoveItems
// (`!isOnCooldown() && state.getValue(ENABLED)`).
func TestHopperDisabledByRedstone(t *testing.T) {
	loop, mgr := newHopperLoop()

	hopperPos := pk.Position{X: 5, Y: 64, Z: 5}
	chestPos := pk.Position{X: 6, Y: 64, Z: 5}
	// ENABLED=false (the redstone lock).
	hopperState := block.ToStateID[block.Hopper{Facing: block.East, Enabled: false}]
	mgr.SetBlock(hopperPos, hopperState, dimMinY)
	mgr.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)

	h := loop.resolveHopper(hopperPos, hopperState)
	h.items[0] = cobble(3)
	cl := loop.resolveChest(chestPos)

	for i := 0; i < 20; i++ {
		loop.tickHoppers()
	}
	if got := countHopper(h); got != 3 {
		t.Fatalf("disabled hopper transferred: holds %d, want 3 (no transfer)", got)
	}
	if got := countChest(cl); got != 0 {
		t.Fatalf("disabled hopper pushed into chest: chest holds %d, want 0", got)
	}
}

// TestHopperCheckPoweredStateLock locks HopperBlock.checkPoweredState: a redstone_block adjacent to a
// hopper flips ENABLED to false; removing it flips ENABLED back to true. CITE: HopperBlock.checkPoweredState
// (shouldBeOn = !hasNeighborSignal(pos)).
func TestHopperCheckPoweredStateLock(t *testing.T) {
	loop, mgr := newHopperLoop()

	hopperPos := pk.Position{X: 5, Y: 64, Z: 5}
	hopperState := block.ToStateID[block.Hopper{Facing: block.Down, Enabled: true}]
	mgr.SetBlock(hopperPos, hopperState, dimMinY)

	// Power it: a redstone_block below is a constant-15 source -> hasNeighborSignal(pos) true -> ENABLED=false.
	mgr.SetBlock(pk.Position{X: 5, Y: 63, Z: 5}, block.ToStateID[block.RedstoneBlock{}], dimMinY)
	loop.hopperCheckPoweredState(hopperPos, hopperState)

	now, _ := mgr.GetBlock(hopperPos, dimMinY)
	if block.HopperEnabled(now) {
		t.Fatal("powered hopper still ENABLED=true, want false")
	}

	// Unpower: replace the redstone_block with air -> ENABLED flips back to true.
	mgr.SetBlock(pk.Position{X: 5, Y: 63, Z: 5}, loop.airState(), dimMinY)
	now, _ = mgr.GetBlock(hopperPos, dimMinY)
	loop.hopperCheckPoweredState(hopperPos, now)
	now, _ = mgr.GetBlock(hopperPos, dimMinY)
	if !block.HopperEnabled(now) {
		t.Fatal("unpowered hopper ENABLED=false, want true")
	}
}

// TestComparatorReadsChestFullness is the container-analog-output gate: a comparator facing a chest reads
// the chest's fullness via getRedstoneSignalFromContainer. The exact formula is
// lerpDiscrete(fillFraction, 0, 15) = floor(f*14) + (f>0?1:0), where f = sum(count/maxStack)/size. A chest
// with one full stack (64) in one of 27 slots => f = (64/64)/27 = 1/27 => floor(14/27)=0, +1 => signal 1.
// CITE: ChestBlock.getAnalogOutputSignal -> AbstractContainerMenu.getRedstoneSignalFromContainer.
func TestComparatorReadsChestFullness(t *testing.T) {
	loop, mgr := newHopperLoop()

	chestPos := pk.Position{X: 5, Y: 64, Z: 5}
	mgr.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)
	cl := loop.resolveChest(chestPos)

	// One full 64-stack in one of 27 slots.
	cl.items[0] = cobble(64)
	sig, has := loop.containerAnalogOutputSignal(chestPos)
	if !has {
		t.Fatal("containerAnalogOutputSignal(chest) has=false, want true")
	}
	// f = (64/64)/27 = 0.037037; floor(0.037037*14)=floor(0.5185)=0; f>0 => +1 => 1.
	if sig != 1 {
		t.Fatalf("chest with 1 full stack: analog = %d, want 1", sig)
	}

	// Fill EVERY slot with a full stack => f = 27*(64/64)/27 = 1.0; floor(1.0*14)=14; +1 => 15.
	for i := range cl.items {
		cl.items[i] = cobble(64)
	}
	sig, _ = loop.containerAnalogOutputSignal(chestPos)
	if sig != 15 {
		t.Fatalf("full chest: analog = %d, want 15", sig)
	}

	// Empty chest => f = 0; floor(0)=0; f>0 false => 0.
	for i := range cl.items {
		cl.items[i] = component.SlotData{Count: 0}
	}
	sig, _ = loop.containerAnalogOutputSignal(chestPos)
	if sig != 0 {
		t.Fatalf("empty chest: analog = %d, want 0", sig)
	}
}

// TestComparatorBehindChestInputSignal wires a real comparator facing a chest and checks its input signal
// reads the chest fullness. A comparator with FACING=EAST at pos reads the block at pos.relative(EAST); a
// half-full chest there feeds the comparator input. CITE: ComparatorBlock.getInputSignal.
func TestComparatorBehindChestInputSignal(t *testing.T) {
	loop, mgr := newHopperLoop()

	compPos := pk.Position{X: 5, Y: 64, Z: 5}
	chestPos := pk.Position{X: 6, Y: 64, Z: 5} // EAST of the comparator (the block it reads)
	comp := block.ToStateID[block.Comparator{Facing: block.East, Mode: block.ComparatorModeCompare, Powered: false}]
	mgr.SetBlock(compPos, comp, dimMinY)
	mgr.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)

	cl := loop.resolveChest(chestPos)
	for i := range cl.items {
		cl.items[i] = cobble(64) // full chest -> analog 15
	}
	if got := loop.comparatorGetInputSignal(comp, compPos); got != 15 {
		t.Fatalf("comparator behind full chest input = %d, want 15", got)
	}
}

// TestDropperEjectsIntoChest is the FILLED dropper seam: a dropper facing EAST with a chest in front, when
// triggered, pushes ONE item into the chest (getContainerAt now resolves the chest, so the addItem branch
// fires instead of the loose shoot). CITE: DropperBlock.dispenseFrom (into != null -> HopperBlockEntity.addItem).
func TestDropperEjectsIntoChest(t *testing.T) {
	loop, mgr := newHopperLoop()

	dropPos := pk.Position{X: 5, Y: 64, Z: 5}
	chestPos := pk.Position{X: 6, Y: 64, Z: 5} // EAST, in front of the dropper
	dropState := block.ToStateID[block.Dropper{Facing: block.East, Triggered: false}]
	mgr.SetBlock(dropPos, dropState, dimMinY)
	mgr.SetBlock(chestPos, block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle}], dimMinY)

	d := loop.resolveDispenser(dropPos, dropState)
	d.items[0] = cobble(3)
	cl := loop.resolveChest(chestPos)

	// Fire the dispense drive directly (bypassing the 4-tick trigger schedule — the eject logic is what we
	// assert). getRandomSlot picks slot 0 (only candidate); dropperContainerInFront resolves the chest.
	loop.dispenseFrom(dropPos, dropState)

	// ONE item moved from the dropper into the chest; NO loose ItemEntity spawned.
	dropCount := 0
	for _, s := range d.items {
		dropCount += int(s.Count)
	}
	if dropCount != 2 {
		t.Fatalf("after dropper eject, dropper holds %d, want 2 (one item moved)", dropCount)
	}
	if got := countChest(cl); got != 1 {
		t.Fatalf("after dropper eject, chest holds %d, want 1 (item ejected into container)", got)
	}
	// No loose item spawned (the container branch, not the default shoot).
	items := 0
	for _, r := range loop.regions {
		if r.entities == nil {
			continue
		}
		for _, e := range r.entities.byID {
			if e.isItem {
				items++
			}
		}
	}
	if items != 0 {
		t.Fatalf("dropper spawned %d loose items, want 0 (ejected into container instead)", items)
	}
}

// TestHopperSucksLooseItemEntity is the loose-item suck gate: a hopper facing DOWN (nothing above) with an
// ItemEntity resting in the SUCK_AABB above it pulls that item into the hopper. CITE:
// HopperBlockEntity.suckInItems (getItemsAtAndAbove -> addItem(hopper, entity)).
func TestHopperSucksLooseItemEntity(t *testing.T) {
	loop, mgr := newHopperLoop()

	hopperPos := pk.Position{X: 5, Y: 64, Z: 5}
	hopperState := block.ToStateID[block.Hopper{Facing: block.Down, Enabled: true}]
	mgr.SetBlock(hopperPos, hopperState, dimMinY)
	h := loop.resolveHopper(hopperPos, hopperState)

	// A loose cobblestone item sitting just above the hopper (inside SUCK_AABB: y in [64.6875, 66]).
	ie := NewItemEntity(loop.idAlloc.AllocID(), 5.5, 65.0, 5.5, cobble(4))
	loop.only().entities.add(ie)

	loop.tickHoppers()
	if got := countHopper(h); got != 4 {
		t.Fatalf("hopper sucked %d loose items, want 4 (whole stack absorbed in one addItem)", got)
	}
	// The item entity was fully absorbed -> discarded.
	if _, ok := loop.only().entities.byID[ie.id]; ok {
		t.Fatal("loose item entity not discarded after full absorption")
	}
}
