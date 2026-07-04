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

// dispenser_test.go — REDSTONE TIER-4 (DISPENSER + DROPPER) validation gates. Each asserts the ported
// behaviour against the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar):
//   - a dispenser powered by redstone fires its item out the FACING after the 4-tick TRIGGERED delay
//     (a loose ItemEntity appears, velocity out the facing);
//   - the TRIGGERED latch is set on the rising edge and cleared on the falling edge;
//   - getRandomSlot picks a non-empty slot and returns -1 for an empty container;
//   - an empty dispenser plays the fail (no item spawns);
//   - a dropper with no container in front drops its item loose out the FACING (Hopper DEFERRED).

// newDispenserLoop wires a TickLoop with one ready all-air chunk + a registered block-tick container for
// column (0,0), so the dispenser's TRIGGERED scheduled tick routes into a live container. Mirrors
// newRedstoneLoop.
func newDispenserLoop() (*TickLoop, *world.ChunkManager) {
	loop := NewTickLoop(newFakeClock())
	mgr := world.NewChunkManager()
	loop.only().world = mgr
	ch := level.EmptyChunk(blockTestSecs)
	ch.Status = level.StatusFull
	mgr.Insert(level.ChunkPos{0, 0}, ch)
	loop.registerChunkBlockTicks(level.ChunkPos{0, 0}, ticks.NewLevelChunkTicks[blockTickType]())
	return loop, mgr
}

// TestDispenserGetRandomSlot locks DispenserBlockEntity.getRandomSlot: an all-empty container returns -1;
// a container with exactly one non-empty slot returns that slot deterministically (a single-candidate
// reservoir always selects it, since nextInt(1) == 0). CITE: DispenserBlockEntity.getRandomSlot.
func TestDispenserGetRandomSlot(t *testing.T) {
	loop, _ := newDispenserLoop()
	rng := loop.only().levelRandom

	// All-empty container -> -1.
	empty := &dispenserBE{}
	if got := empty.getRandomSlot(rng); got != -1 {
		t.Fatalf("getRandomSlot(empty) = %d, want -1", got)
	}

	// Exactly one non-empty slot (index 4) -> that slot (a single candidate always wins).
	one := &dispenserBE{}
	one.items[4] = component.SlotData{Count: 1, ItemID: pk.VarInt(item.Cobblestone.ID)}
	if got := one.getRandomSlot(rng); got != 4 {
		t.Fatalf("getRandomSlot(one at 4) = %d, want 4", got)
	}
}

// TestDispenserTriggerAndFire is the headline gate: a dispenser facing EAST with an item in a slot, powered
// by an adjacent redstone_block, latches TRIGGERED on the rising edge, schedules a 4-tick dispense, does
// NOT fire before tick 4, and on tick 4 shoots a loose ItemEntity out the +X (EAST) face. CITE:
// DispenserBlock.neighborChanged (scheduleTick 4 + TRIGGERED latch) + tick -> dispenseFrom +
// DefaultDispenseItemBehavior.
func TestDispenserTriggerAndFire(t *testing.T) {
	loop, mgr := newDispenserLoop()

	pos := pk.Position{X: 5, Y: 64, Z: 5}
	// Dispenser facing EAST, TRIGGERED=false initially.
	disp := block.ToStateID[block.Dispenser{Facing: block.East, Triggered: false}]
	mgr.SetBlock(pos, disp, dimMinY)

	// Seed its BE with an item so getRandomSlot has a candidate.
	d := loop.resolveDispenser(pos, disp)
	d.items[0] = component.SlotData{Count: 3, ItemID: pk.VarInt(item.Cobblestone.ID)}

	// Power it: place a redstone_block below (a constant-15 source) so hasNeighborSignal(pos) is true.
	mgr.SetBlock(pk.Position{X: 5, Y: 63, Z: 5}, block.ToStateID[block.RedstoneBlock{}], dimMinY)

	// neighborChanged on the rising edge: schedule a 4-tick dispense + latch TRIGGERED.
	loop.dispenserNeighborChanged(pos, disp)

	latched, _ := mgr.GetBlock(pos, dimMinY)
	if !block.DispenserTriggered(latched) {
		t.Fatal("dispenser did not latch TRIGGERED=true on the rising power edge")
	}
	if !loop.hasScheduledBlockTick(pos, dispenserTickType) {
		t.Fatal("dispenser should schedule a TRIGGERED tick when it becomes powered")
	}

	before := loop.only().entities.len()

	// Advance 3 ticks: the dispense must NOT fire yet (TRIGGER_DURATION is 4).
	for i := 0; i < 3; i++ {
		loop.gametime++
		loop.tickScheduledBlocks()
	}
	if got := loop.only().entities.len(); got != before {
		t.Fatalf("dispenser fired after only 3 ticks (%d entities), want no fire before tick 4", got-before)
	}

	// Tick 4: the dispense fires — a loose ItemEntity appears.
	loop.gametime++
	loop.tickScheduledBlocks()
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("dispenser did not fire on the 4th tick: %d new entities, want 1", got-before)
	}

	// The BE slot shrank by exactly 1 (DefaultDispenseItemBehavior split(1)).
	if d.items[0].Count != 2 {
		t.Fatalf("dispenser slot count = %d after firing, want 2 (split(1) of 3)", d.items[0].Count)
	}

	// The spawned item is a loose ITEM entity shot out the +X (EAST) face: vx must be positive.
	var shot *Entity
	for _, e := range loop.only().entities.near(float64(pos.X)+1, float64(pos.Z), trackRange) {
		if e.isItem {
			shot = e
			break
		}
	}
	if shot == nil {
		t.Fatal("no ItemEntity found near the dispenser after firing")
	}
	if shot.vx <= 0 {
		t.Fatalf("dispensed item vx = %v, want > 0 (shot out the EAST face)", shot.vx)
	}
	// The item spawns ~0.7 out the front face on X (center 5.5 + 0.7 = 6.2), and the Y is offset down by
	// 0.15625 for a horizontal facing (center 64.5 - 0.15625 = 64.34375).
	if shot.x < 6.0 || shot.x > 6.4 {
		t.Fatalf("dispensed item x = %v, want ~6.2 (center + 0.7*EAST)", shot.x)
	}
	if !shot.isItem {
		t.Fatal("dispensed entity not flagged isItem")
	}
}

// TestDispenserFallingEdgeClearsTriggered locks the TRIGGERED latch clear: once powered+latched, removing
// the power on a falling edge clears TRIGGERED (so a later re-power re-triggers). CITE:
// DispenserBlock.neighborChanged (!shouldTrigger && isTriggered -> setValue(TRIGGERED, false)).
func TestDispenserFallingEdgeClearsTriggered(t *testing.T) {
	loop, mgr := newDispenserLoop()
	pos := pk.Position{X: 3, Y: 64, Z: 3}
	disp := block.ToStateID[block.Dispenser{Facing: block.North, Triggered: false}]
	mgr.SetBlock(pos, disp, dimMinY)

	// Power on, latch.
	src := pk.Position{X: 3, Y: 63, Z: 3}
	mgr.SetBlock(src, block.ToStateID[block.RedstoneBlock{}], dimMinY)
	loop.dispenserNeighborChanged(pos, disp)
	latched, _ := mgr.GetBlock(pos, dimMinY)
	if !block.DispenserTriggered(latched) {
		t.Fatal("dispenser did not latch TRIGGERED on power")
	}

	// Remove power -> falling edge -> TRIGGERED cleared.
	mgr.SetBlock(src, loop.airState(), dimMinY)
	loop.dispenserNeighborChanged(pos, latched)
	cleared, _ := mgr.GetBlock(pos, dimMinY)
	if block.DispenserTriggered(cleared) {
		t.Fatal("dispenser did not clear TRIGGERED on the falling power edge")
	}
}

// TestEmptyDispenserFiresNothing locks the getRandomSlot < 0 fail path: an EMPTY dispenser that fires plays
// the fail (levelEvent 1001, a cited no-op) and spawns NO item. CITE: DispenserBlock.dispenseFrom (slot < 0).
func TestEmptyDispenserFiresNothing(t *testing.T) {
	loop, mgr := newDispenserLoop()
	pos := pk.Position{X: 7, Y: 64, Z: 7}
	disp := block.ToStateID[block.Dispenser{Facing: block.South, Triggered: false}]
	mgr.SetBlock(pos, disp, dimMinY)
	// Resolve an EMPTY BE (no items seeded).
	loop.resolveDispenser(pos, disp)

	before := loop.only().entities.len()
	loop.dispenseFrom(pos, disp) // fire directly (getRandomSlot returns -1)
	if got := loop.only().entities.len(); got != before {
		t.Fatalf("empty dispenser spawned %d entities, want 0 (fail path)", got-before)
	}
}

// TestDropperDropsLoose locks DropperBlock.dispenseFrom: with no container in front (Hopper DEFERRED,
// getContainerAt == null), the dropper falls to the loose-item DEFAULT shoot out the FACING. A loose
// ItemEntity appears and the slot shrinks by 1. CITE: DropperBlock.dispenseFrom (into == null branch).
func TestDropperDropsLoose(t *testing.T) {
	loop, mgr := newDispenserLoop()
	pos := pk.Position{X: 9, Y: 64, Z: 9}
	drop := block.ToStateID[block.Dropper{Facing: block.West, Triggered: false}]
	mgr.SetBlock(pos, drop, dimMinY)

	d := loop.resolveDispenser(pos, drop)
	if !d.isDropper {
		t.Fatal("resolveDispenser did not flag the dropper BE as a dropper")
	}
	d.items[8] = component.SlotData{Count: 2, ItemID: pk.VarInt(item.Cobblestone.ID)}

	before := loop.only().entities.len()
	loop.dispenseFrom(pos, drop)
	if got := loop.only().entities.len(); got != before+1 {
		t.Fatalf("dropper spawned %d entities, want 1 loose item (Hopper DEFERRED -> loose drop)", got-before)
	}
	if d.items[8].Count != 1 {
		t.Fatalf("dropper slot count = %d after dropping, want 1 (shot 1 of 2)", d.items[8].Count)
	}
	// Shot out the WEST face: vx must be negative.
	var shot *Entity
	for _, e := range loop.only().entities.near(float64(pos.X)-1, float64(pos.Z), trackRange) {
		if e.isItem {
			shot = e
			break
		}
	}
	if shot == nil {
		t.Fatal("no ItemEntity found near the dropper after dropping")
	}
	if shot.vx >= 0 {
		t.Fatalf("dropped item vx = %v, want < 0 (shot out the WEST face)", shot.vx)
	}
}
