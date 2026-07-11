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

// crafter_test.go -- CRAFTER (redstone auto-crafter) validation gates against the 26.2 jar
// (temp/cache/26.2-inner.jar): a powered crafter with a valid 3x3 recipe crafts on the 4-tick TRIGGERED
// pulse, ejects the assembled result out the FRONT face, consumes one from each filled slot, latches
// CRAFTING for 6 ticks, and reports the fill count as its comparator output.

// newCrafterLoop wires a TickLoop with one ready all-air chunk + a registered block-tick container + the
// embedded crafting matcher, so a crafter TRIGGERED tick routes into a live container and can assemble.
func newCrafterLoop(t *testing.T) (*TickLoop, *world.ChunkManager) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	loop.SetPlugins(craftingManager(t))
	return loop, mgr
}

// crafterEastState returns a crafter block state oriented so ORIENTATION.front() == EAST (NorthUp keeps
// front NORTH; we need EAST-facing front, which is EastUp per FrontAndTop). CITE: FrontAndTop.
func crafterEastState() block.StateID {
	return block.ToStateID[block.Crafter{Orientation: block.EastUp, Crafting: false, Triggered: false}]
}

// TestCrafterComparatorSignal locks CrafterBlockEntity.getRedstoneSignal: the count of grid slots that are
// non-empty OR disabled (0..9). CITE: CrafterBlockEntity.getRedstoneSignal.
func TestCrafterComparatorSignal(t *testing.T) {
	c := &crafterBE{}
	if got := c.crafterGetRedstoneSignal(); got != 0 {
		t.Fatalf("empty crafter signal = %d, want 0", got)
	}
	c.items[0] = component.SlotData{Count: 1, ItemID: pk.VarInt(item.OakPlanks.ID)}
	c.items[5] = component.SlotData{Count: 1, ItemID: pk.VarInt(item.OakPlanks.ID)}
	if got := c.crafterGetRedstoneSignal(); got != 2 {
		t.Fatalf("2 filled slots signal = %d, want 2", got)
	}
	// A disabled EMPTY slot also counts.
	c.disabled[8] = true
	if got := c.crafterGetRedstoneSignal(); got != 3 {
		t.Fatalf("2 filled + 1 disabled signal = %d, want 3", got)
	}
}

// TestCrafterSlotDisableRules locks slotCanBeDisabled + setSlotState: only an EMPTY, in-range slot may be
// toggled disabled; an occupied slot cannot. CITE: CrafterBlockEntity.slotCanBeDisabled/setSlotState.
func TestCrafterSlotDisableRules(t *testing.T) {
	c := &crafterBE{}
	c.crafterSetSlotState(0, false) // disable empty slot 0
	if !c.crafterIsSlotDisabled(0) {
		t.Fatal("empty slot 0 should be disable-able")
	}
	c.items[1] = component.SlotData{Count: 1, ItemID: pk.VarInt(item.OakPlanks.ID)}
	c.crafterSetSlotState(1, false) // occupied -> no-op
	if c.crafterIsSlotDisabled(1) {
		t.Fatal("occupied slot 1 must NOT be disable-able")
	}
	c.crafterSetSlotState(0, true) // re-enable
	if c.crafterIsSlotDisabled(0) {
		t.Fatal("slot 0 should re-enable")
	}
}

// TestCrafterCraftsOnRedstonePulse is the headline gate: a crafter facing EAST with 2 planks in a vertical
// column (slots 0 and 3 of the 3x3), powered by an adjacent redstone_block, latches TRIGGERED on the rising
// edge, schedules a 4-tick craft, and on tick 4 assembles the 2-plank->4-stick recipe: a loose stick
// ItemEntity is ejected out the +X (EAST) face, each plank slot shrinks by 1, and CRAFTING latches true.
// CITE: CrafterBlock.neighborChanged + tick -> dispenseFrom + CraftingRecipe.assemble.
func TestCrafterCraftsOnRedstonePulse(t *testing.T) {
	loop, mgr := newCrafterLoop(t)

	pos := pk.Position{X: 5, Y: 64, Z: 5}
	state := crafterEastState()
	mgr.SetBlock(pos, state, dimMinY)

	// 2 planks stacked vertically: 3x3 slots 0 (top-left) and 3 (mid-left) -> the shaped 2-high stick recipe.
	c := loop.resolveCrafter(pos, state)
	c.items[0] = component.SlotData{Count: 2, ItemID: pk.VarInt(item.OakPlanks.ID)}
	c.items[3] = component.SlotData{Count: 2, ItemID: pk.VarInt(item.OakPlanks.ID)}

	// Power it with a redstone_block below (constant-15 source, hasNeighborSignal(pos) true).
	mgr.SetBlock(pk.Position{X: 5, Y: 63, Z: 5}, block.ToStateID[block.RedstoneBlock{}], dimMinY)

	loop.crafterNeighborChanged(pos, state)

	latched, _ := mgr.GetBlock(pos, dimMinY)
	if !block.CrafterTriggered(latched) {
		t.Fatal("crafter did not latch TRIGGERED=true on the rising power edge")
	}
	if !loop.hasScheduledBlockTick(pos, crafterTickType) {
		t.Fatal("crafter should schedule a TRIGGERED tick when it becomes powered")
	}

	before := loop.only().entities.len()

	// Advance 3 ticks: no craft yet (CRAFTING_TICK_DELAY == 4).
	for i := 0; i < 3; i++ {
		loop.gametime++
		loop.tickScheduledBlocks()
	}
	if got := loop.only().entities.len(); got != before {
		t.Fatalf("crafter crafted after only 3 ticks, want no craft before tick 4")
	}

	// Tick 4: the craft fires -> a loose stick ItemEntity appears.
	loop.gametime++
	loop.tickScheduledBlocks()
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("crafter did not craft on tick 4: %d new entities, want 1", got-before)
	}

	// Each plank slot shrank by exactly 1 (be.getItems().forEach(shrink 1)).
	if c.items[0].Count != 1 || c.items[3].Count != 1 {
		t.Fatalf("crafter grid slots not consumed: [0]=%d [3]=%d, want 1 each", c.items[0].Count, c.items[3].Count)
	}

	// CRAFTING latched true on the successful craft.
	nowState, _ := mgr.GetBlock(pos, dimMinY)
	if !block.CrafterCrafting(nowState) {
		t.Fatal("crafter did not latch CRAFTING=true on a successful craft")
	}

	// The ejected item is a loose STICK shot out the +X (EAST) face.
	var shot *Entity
	for _, e := range loop.only().entities.near(float64(pos.X)+1, float64(pos.Z), pickupMergeScanChunks) {
		if e.isItem {
			shot = e
			break
		}
	}
	if shot == nil {
		t.Fatal("no ItemEntity found near the crafter after crafting")
	}
	if shot.vx <= 0 {
		t.Fatalf("crafted item vx = %v, want > 0 (ejected out the EAST face)", shot.vx)
	}

	// CRAFTING clears after the 6-tick countdown (CrafterBlockEntity.serverTick).
	for i := 0; i < crafterMaxCraftingTicks+1; i++ {
		s, _ := mgr.GetBlock(pos, dimMinY)
		loop.crafterServerTick(pos, s, c)
	}
	doneState, _ := mgr.GetBlock(pos, dimMinY)
	if block.CrafterCrafting(doneState) {
		t.Fatal("crafter CRAFTING should clear after the 6-tick countdown")
	}
}

// TestCrafterEjectsIntoContainer locks the CrafterBlock.dispenseItem container path: with a chest in the
// FACING cell, the crafted result is inserted into the chest (HopperBlockEntity.addItem) instead of being
// dropped loose. CITE: CrafterBlock.dispenseItem.
func TestCrafterEjectsIntoContainer(t *testing.T) {
	loop, mgr := newCrafterLoop(t)

	pos := pk.Position{X: 5, Y: 64, Z: 5}
	state := crafterEastState()
	mgr.SetBlock(pos, state, dimMinY)

	c := loop.resolveCrafter(pos, state)
	c.items[0] = component.SlotData{Count: 1, ItemID: pk.VarInt(item.OakPlanks.ID)}
	c.items[3] = component.SlotData{Count: 1, ItemID: pk.VarInt(item.OakPlanks.ID)}

	// A chest in the EAST cell (front): the result should land inside it, not drop loose.
	frontPos := pk.Position{X: 6, Y: 64, Z: 5}
	mgr.SetBlock(frontPos, block.ToStateID[block.Chest{Facing: block.North}], dimMinY)

	before := loop.only().entities.len()
	loop.crafterDispenseFrom(state, pos)

	// No loose item spawned (it went into the chest).
	if got := loop.only().entities.len(); got != before {
		t.Fatalf("crafter dropped a loose item (%d new) despite a chest in front; want insert", got-before)
	}
	// The chest now holds 4 sticks.
	chestView := loop.getContainerAt(frontPos)
	if chestView == nil {
		t.Fatal("no chest container resolved in front of the crafter")
	}
	total := 0
	for i := 0; i < chestView.getContainerSize(); i++ {
		s := chestView.getItem(i)
		if int(s.ItemID) == int(item.Stick.ID) {
			total += int(s.Count)
		}
	}
	if total != 4 {
		t.Fatalf("chest holds %d sticks after craft, want 4", total)
	}
}
