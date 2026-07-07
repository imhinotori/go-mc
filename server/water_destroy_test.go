package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// water_destroy_test.go pins the D-F3 divergence fix: flowing water DESTROYS a replaceable non-solid
// block (tall grass, flowers, torches, ...) - dropping its resources - and flows through, instead of
// being blocked. Ported 1:1 from net.minecraft.world.level.material.FlowingFluid.spreadTo /
// canHoldAnyFluid + WaterFluid.beforeDestroyingBlock (dropResources). See fluid.go / fluid_replace.go.
//
// The flow environment is a walled basin (buildBasin): a source at the center settles to level-1
// flowing water on each cardinal ring-1 neighbor (8 - dropOff=1 -> amount 7 -> legacy level 1),
// exactly as TestWaterSettles pins. A REPLACEABLE block placed on a ring-1 cell is therefore flowed
// INTO on the first spread and must end up level-1 water (destroyed) rather than blocking the flow.

// countItemsNear returns the Item entities within range of (x,z) - the dropped-resource assertion.
func countItemsNear(loop *TickLoop, x, z float64) []*Entity {
	var items []*Entity
	for _, e := range loop.only().entities.near(x, z, trackRange) {
		if e.typ == entity.Item.ID {
			items = append(items, e)
		}
	}
	return items
}

// TestWaterDestroysReplaceableNonSolid: a settling water source flows into an adjacent dandelion,
// replacing it with level-1 flowing water AND dropping a dandelion item (beforeDestroyingBlock ->
// dropResources). Dandelion has a deterministic single-item loot pool, so the drop is exact.
func TestWaterDestroysReplaceableNonSolid(t *testing.T) {
	loop, mgr := newFluidLoop()

	const cx, y, cz, r = 8, 64, 8, 5
	buildBasin(mgr, cx, y, cz, r)

	flower := pk.Position{X: cx + 1, Y: y, Z: cz}
	mgr.SetBlock(flower, block.ToStateID[block.Dandelion{}], dimMinY)
	if id, _ := mgr.GetBlock(flower, dimMinY); !block.CanHoldAnyFluid(id) {
		t.Fatalf("precondition: dandelion should be CanHoldAnyFluid=true (replaceable non-solid)")
	}

	src := pk.Position{X: cx, Y: y, Z: cz}
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)
	drainAll(loop)

	lvl, isW := levelAt(mgr, flower)
	if !isW {
		id, _ := mgr.GetBlock(flower, dimMinY)
		t.Fatalf("water did NOT flow into the dandelion cell; still id=%d (D-F3: non-solid blocked water)", id)
	}
	if lvl != 1 {
		t.Fatalf("dandelion cell = legacy level %d, want 1 (settled flowing water on ring-1)", lvl)
	}

	items := countItemsNear(loop, float64(flower.X)+0.5, float64(flower.Z)+0.5)
	if len(items) == 0 {
		t.Fatalf("no dropped item after water destroyed the dandelion (beforeDestroyingBlock/dropResources missing)")
	}
	got := items[0].itemStack.ItemID
	if item.ID(got) != item.Dandelion.ID {
		t.Fatalf("dropped item id=%d, want dandelion item id=%d", got, item.Dandelion.ID)
	}
	t.Logf("dandelion destroyed -> level-1 flowing water + dropped dandelion item (%d item(s))", len(items))
}

// TestWaterDestroysShortGrass: water flowing into a short_grass cell replaces it with water (the
// destroy path). Short grass's no-tool loot is probabilistic (12.5% seeds), so this asserts the
// BLOCK REPLACEMENT (the destroy), not the drop - the drop path is covered by the dandelion test.
func TestWaterDestroysShortGrass(t *testing.T) {
	loop, mgr := newFluidLoop()

	const cx, y, cz, r = 8, 64, 8, 5
	buildBasin(mgr, cx, y, cz, r)

	grass := pk.Position{X: cx + 1, Y: y, Z: cz}
	mgr.SetBlock(grass, block.ToStateID[block.ShortGrass{}], dimMinY)

	src := pk.Position{X: cx, Y: y, Z: cz}
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)
	drainAll(loop)

	lvl, isW := levelAt(mgr, grass)
	if !isW {
		id, _ := mgr.GetBlock(grass, dimMinY)
		t.Fatalf("water did NOT destroy+flow into short_grass; cell still id=%d (D-F3)", id)
	}
	if lvl != 1 {
		t.Fatalf("short_grass cell = legacy level %d, want 1 (settled flowing water)", lvl)
	}
}

// TestWaterDoesNotReplaceSolid: water flowing toward a solid stone block does NOT replace it - a
// solid (blocksMotion) STOPS the fluid (canHoldAnyFluid=false). The guard against the fix
// over-reaching into solids.
func TestWaterDoesNotReplaceSolid(t *testing.T) {
	loop, mgr := newFluidLoop()

	const cx, y, cz, r = 8, 64, 8, 5
	buildBasin(mgr, cx, y, cz, r)

	solid := pk.Position{X: cx + 1, Y: y, Z: cz}
	setSolid(mgr, solid)
	if id, _ := mgr.GetBlock(solid, dimMinY); block.CanHoldAnyFluid(id) {
		t.Fatalf("precondition: stone should be CanHoldAnyFluid=false (blocksMotion)")
	}

	src := pk.Position{X: cx, Y: y, Z: cz}
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)
	drainAll(loop)

	if _, isW := levelAt(mgr, solid); isW {
		t.Fatalf("water REPLACED a solid block (should be stopped by canHoldAnyFluid=false)")
	}
	if id, _ := mgr.GetBlock(solid, dimMinY); id != block.ToStateID[block.Stone{}] {
		t.Fatalf("solid cell changed to id=%d, want stone unchanged", id)
	}
	if items := countItemsNear(loop, float64(solid.X)+0.5, float64(solid.Z)+0.5); len(items) != 0 {
		t.Fatalf("a solid block that stopped water still dropped %d item(s)", len(items))
	}
}

// TestWaterFlowsIntoAirUnchanged: the baseline air path is unchanged - water flows into an adjacent
// air cell and settles to level 1, and destroys nothing (no drop). Locks that the destroy path did
// not perturb the original air spread.
func TestWaterFlowsIntoAirUnchanged(t *testing.T) {
	loop, mgr := newFluidLoop()

	const cx, y, cz, r = 8, 64, 8, 5
	buildBasin(mgr, cx, y, cz, r)

	air := pk.Position{X: cx + 1, Y: y, Z: cz}
	if id, _ := mgr.GetBlock(air, dimMinY); !block.IsAir(id) {
		t.Fatalf("precondition: target cell should be air")
	}

	src := pk.Position{X: cx, Y: y, Z: cz}
	setWater(mgr, src, 0)
	loop.scheduleFluidTick(src)
	drainAll(loop)

	lvl, isW := levelAt(mgr, air)
	if !isW {
		t.Fatalf("water did not flow into the air cell (baseline spread broke)")
	}
	if lvl != 1 {
		t.Fatalf("air cell = legacy level %d, want 1 (settled flowing water)", lvl)
	}
	if items := countItemsNear(loop, float64(air.X)+0.5, float64(air.Z)+0.5); len(items) != 0 {
		t.Fatalf("water flowing into AIR dropped %d item(s); air destroy should be a no-op", len(items))
	}
}

// TestCanHoldAnyFluidPredicate pins the block-level replaceability predicate (block.CanHoldAnyFluid)
// against the vanilla canHoldAnyFluid cases: replaceable non-solids TRUE; a solid FALSE; air/water
// TRUE. A ladder is a SimpleWaterloggedBlock (LiquidBlockContainer), so it returns TRUE via the
// container branch BEFORE the ladder exception - faithful to the bytecode evaluation order.
func TestCanHoldAnyFluidPredicate(t *testing.T) {
	cases := []struct {
		name string
		id   block.StateID
		want bool
	}{
		{"air", block.ToStateID[block.Air{}], true},
		{"water source", waterStateID(0), true},
		{"dandelion (flower)", block.ToStateID[block.Dandelion{}], true},
		{"short_grass", block.ToStateID[block.ShortGrass{}], true},
		{"poppy", block.ToStateID[block.Poppy{}], true},
		{"stone (solid)", block.ToStateID[block.Stone{}], false},
		{"ladder (waterloggable container)", block.ToStateID[block.Ladder{Facing: block.North}], true},
	}
	for _, c := range cases {
		if got := block.CanHoldAnyFluid(c.id); got != c.want {
			t.Errorf("CanHoldAnyFluid(%s) = %v, want %v", c.name, got, c.want)
		}
	}
}
