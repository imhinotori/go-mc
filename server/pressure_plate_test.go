package server

// pressure_plate_test.go -- PLATE-01 validation gates for the pressure-plate redstone input, asserting the
// ported behavior against the unobfuscated 26.2 jar (BasePressurePlateBlock + PressurePlateBlock +
// WeightedPressurePlateBlock):
//   - an entity on a stone plate -> POWERED + a full signal 15 out every face (strong UP).
//   - the plate unpresses (POWERED false) once the entity leaves and the scheduled tick fires.
//   - a weighted plate scales POWER with the entity count (Mth.ceil(min(count,maxWeight)/maxWeight * 15)).
//   - a wooden plate triggers on a dropped ITEM (EVERYTHING sensitivity); a stone plate does NOT.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/ticks"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// newPlateLoop wires a TickLoop with a 3x3 ring of ready all-air chunks (so the radius-1 entity scan has
// loaded neighbor columns) + a registered block-tick container for the origin column.
func newPlateLoop() (*TickLoop, *world.ChunkManager) {
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

func plateState(name string) block.StateID { return block.DefaultStateID[name] }

// TestPlateStonePressAndSignal: a mob on a stone pressure plate presses it (POWERED) and the plate emits a
// full weak signal 15 out every face and a strong 15 straight UP.
func TestPlateStonePressAndSignal(t *testing.T) {
	loop, mgr := newPlateLoop()
	platePos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(platePos, plateState("minecraft:stone_pressure_plate"), dimMinY)

	// A pig standing on the plate.
	e := NewEntity(1, entity.Pig, 8.5, 64, 8.5)
	loop.only().entities.add(e)

	loop.tickPressurePlates()

	after, _ := mgr.GetBlock(platePos, dimMinY)
	if !block.PressurePlatePowered(after) {
		t.Fatalf("stone plate not POWERED with a mob on it")
	}
	// getSignal: 15 out every face (weak). getDirectSignal: 15 only out UP.
	if s := loop.stateGetSignal(after, platePos, block.North); s != 15 {
		t.Fatalf("stone plate weak signal (North) = %d, want 15", s)
	}
	if d := loop.stateGetDirectSignal(after, platePos, block.Up); d != 15 {
		t.Fatalf("stone plate strong signal (Up) = %d, want 15", d)
	}
	if d := loop.stateGetDirectSignal(after, platePos, block.North); d != 0 {
		t.Fatalf("stone plate strong signal (North) = %d, want 0 (UP-only)", d)
	}
}

// TestPlateUnpressAfterDelay: once the entity leaves, the scheduled 20-tick tick unpresses the plate.
func TestPlateUnpressAfterDelay(t *testing.T) {
	loop, mgr := newPlateLoop()
	platePos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(platePos, plateState("minecraft:stone_pressure_plate"), dimMinY)

	e := NewEntity(1, entity.Pig, 8.5, 64, 8.5)
	loop.only().entities.add(e)
	loop.tickPressurePlates()

	pressed, _ := mgr.GetBlock(platePos, dimMinY)
	if !block.PressurePlatePowered(pressed) {
		t.Fatalf("plate not pressed to begin with")
	}

	// Move the pig off the plate, then fire the scheduled unpress tick directly.
	e.x, e.z = 40.5, 40.5
	loop.plateTick(pressed, platePos)

	after, _ := mgr.GetBlock(platePos, dimMinY)
	if block.PressurePlatePowered(after) {
		t.Fatalf("plate still POWERED after the entity left + the unpress tick fired")
	}
}

// TestPlateWeightedScalesWithCount: a light weighted plate (maxWeight 15) scales POWER with the entity
// count -- one entity -> Mth.ceil(1/15 * 15) = 1; three entities -> Mth.ceil(3/15 * 15) = 3.
func TestPlateWeightedScalesWithCount(t *testing.T) {
	loop, mgr := newPlateLoop()
	platePos := pk.Position{X: 8, Y: 64, Z: 8}
	mgr.SetBlock(platePos, plateState("minecraft:light_weighted_pressure_plate"), dimMinY)

	// One entity on the plate.
	e1 := NewEntity(1, entity.Pig, 8.5, 64, 8.5)
	loop.only().entities.add(e1)
	loop.tickPressurePlates()
	after1, _ := mgr.GetBlock(platePos, dimMinY)
	if p := block.WeightedPressurePlatePower(after1); p != 1 {
		t.Fatalf("light weighted plate POWER with 1 entity = %d, want 1", p)
	}

	// Two more entities (3 total) -> ceil(3/15*15) = 3.
	e2 := NewEntity(2, entity.Pig, 8.4, 64, 8.6)
	e3 := NewEntity(3, entity.Pig, 8.6, 64, 8.4)
	loop.only().entities.add(e2)
	loop.only().entities.add(e3)
	// Re-press: the plate is already POWER 1, so re-run checkPressed directly to recompute.
	cur, _ := mgr.GetBlock(platePos, dimMinY)
	loop.plateCheckPressed(platePos, cur)
	after3, _ := mgr.GetBlock(platePos, dimMinY)
	if p := block.WeightedPressurePlatePower(after3); p != 3 {
		t.Fatalf("light weighted plate POWER with 3 entities = %d, want 3", p)
	}
}

// TestPlateWoodenTriggersOnItem: a wooden plate (EVERYTHING sensitivity) presses when a dropped ITEM sits
// on it; a stone plate (MOBS sensitivity) does NOT.
func TestPlateWoodenTriggersOnItem(t *testing.T) {
	loop, mgr := newPlateLoop()
	woodPos := pk.Position{X: 8, Y: 64, Z: 8}
	stonePos := pk.Position{X: 4, Y: 64, Z: 4}
	mgr.SetBlock(woodPos, plateState("minecraft:oak_pressure_plate"), dimMinY)
	mgr.SetBlock(stonePos, plateState("minecraft:stone_pressure_plate"), dimMinY)

	// A dropped item on the wooden plate.
	itemW := NewEntity(1, entity.Item, 8.5, 64, 8.5)
	itemW.isItem = true
	loop.only().entities.add(itemW)
	// A dropped item on the stone plate.
	itemS := NewEntity(2, entity.Item, 4.5, 64, 4.5)
	itemS.isItem = true
	loop.only().entities.add(itemS)

	loop.tickPressurePlates()

	wood, _ := mgr.GetBlock(woodPos, dimMinY)
	if !block.PressurePlatePowered(wood) {
		t.Fatalf("wooden plate (EVERYTHING) did NOT trigger on a dropped item")
	}
	stone, _ := mgr.GetBlock(stonePos, dimMinY)
	if block.PressurePlatePowered(stone) {
		t.Fatalf("stone plate (MOBS) wrongly triggered on a dropped item")
	}
}
