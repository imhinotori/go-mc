package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/plugin/host"
)

// crafting_click_test.go — PLUGIN-05 (Plan 25-02) Task 1: the un-stubbed ResultSlot.onTake consume +
// the 2x2 player-grid recompute through the plugin matcher. Every assertion checks EXACT counts so a
// regression in the 1-per-cell consume (the dupe/eat-bug pitfall) is caught. The matcher is the REAL
// embedded vanilla crafting plugin (boot-loaded via LoadCraftingPlugin), so these prove crafting
// THROUGH the plugin path, not a hardcoded Go table.

// Real protocol item ids (resolved from registryid.Item this session).
const (
	idOakPlanks = 63
	idStick     = 974
	idOakLog    = 161
	idDirt      = 55
)

// craftingManager boot-loads the embedded vanilla crafting plugin into a fresh Manager (the same
// LoadCraftingPlugin the runtime uses). It is the test analogue of cmd/sulfur/main.go's boot-wire.
func craftingManager(t *testing.T) *host.Manager {
	t.Helper()
	m := host.New()
	if err := LoadCraftingPlugin(m); err != nil {
		t.Fatalf("LoadCraftingPlugin: %v", err)
	}
	if !m.HasRecipeMatcher() {
		t.Fatalf("LoadCraftingPlugin: no recipe matcher registered")
	}
	return m
}

// craftLoop builds a survival player + an inventory + the embedded crafting matcher wired into the tick
// (the full 2x2-craft harness).
func craftLoop(t *testing.T) (*TickLoop, *tickPlayer, *Inventory) {
	t.Helper()
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	p.gameMode = gameModeSurvival
	inv := ensureInventory(p)
	loop.SetPlugins(craftingManager(t))
	return loop, p, inv
}

// TestBootLoadVanillaPlugin: LoadCraftingPlugin materializes + loads the embedded crafting plugin, sets
// the recipe table from level/recipe.ParseAll, and registers the matcher. A Match over a known vanilla
// recipe (2 planks vertical -> 4 sticks) returns the jar-correct result via the plugin Call.
func TestBootLoadVanillaPlugin(t *testing.T) {
	m := craftingManager(t)

	// 2x2 grid with planks in the left column (slots 0 and 2) -> the vertical 2-high stick pattern.
	v := craftView{
		w: 2, h: 2,
		getCell: func(i int) component.SlotData {
			if i == 0 || i == 2 {
				return component.SlotData{ItemID: idOakPlanks, Count: 1}
			}
			return component.SlotData{}
		},
		setCell: func(int, component.SlotData) {},
		getRes:  func() component.SlotData { return component.SlotData{} },
		setRes:  func(component.SlotData) {},
	}
	id, count, ok := m.Match(craftGridPayload(v))
	if !ok {
		t.Fatalf("Match(2 planks vertical): ok=false, want a stick recipe match")
	}
	if id != idStick || count != 4 {
		t.Fatalf("Match(2 planks vertical) = id=%d count=%d, want id=%d count=4", id, count, idStick)
	}
}

// TestResultSlotConsume: place 2 planks in the 2x2 player grid (vertical column), assert slot 0 (result)
// populates 4 sticks via slotChangedCraftingGrid -> Match; take the result and assert EXACTLY 1 is
// removed from each used cell (not the whole stack), then the result recomputes (chained crafting).
func TestResultSlotConsume(t *testing.T) {
	loop, p, inv := craftLoop(t)

	// Player 2x2 grid = window slots 1-4 (1,2 = top row; 3,4 = bottom row). The stick pattern is a
	// vertical 2-high column: planks at grid cell 0 (slot 1) and cell 2 (slot 3), 3 in each so we can
	// verify a chained re-craft after the first take.
	inv.set(1, stk(idOakPlanks, 3)) // grid cell 0 (top-left)
	inv.set(3, stk(idOakPlanks, 3)) // grid cell 2 (bottom-left)

	// slotChangedCraftingGrid: the result slot 0 should populate with 4 sticks.
	loop.slotChangedCraftingGrid(playerCraftView(inv))
	if r := inv.get(0); r.ItemID != idStick || r.Count != 4 {
		t.Fatalf("result after grid change = id=%d count=%d, want id=%d count=4", r.ItemID, r.Count, idStick)
	}

	// Take the result (PICKUP primary on slot 0): the consume removes EXACTLY 1 from each used cell.
	loop.doClick(p, inv, 0, 0, containerInputPickup)

	if c := inv.getCarried(); c.ItemID != idStick || c.Count != 4 {
		t.Fatalf("carried after take = id=%d count=%d, want id=%d count=4", c.ItemID, c.Count, idStick)
	}
	if s := inv.get(1); s.Count != 2 {
		t.Fatalf("grid cell 0 (slot 1) after take = count=%d, want 2 (1 consumed from 3)", s.Count)
	}
	if s := inv.get(3); s.Count != 2 {
		t.Fatalf("grid cell 2 (slot 3) after take = count=%d, want 2 (1 consumed from 3)", s.Count)
	}
	// Untouched grid cells stay empty.
	if s := inv.get(2); s.Count != 0 {
		t.Fatalf("grid cell 1 (slot 2) after take = count=%d, want 0 (unused)", s.Count)
	}

	// Chained crafting: the result recomputed and still shows 4 sticks (2 planks remain in each column).
	if r := inv.get(0); r.ItemID != idStick || r.Count != 4 {
		t.Fatalf("result after take (chained) = id=%d count=%d, want id=%d count=4", r.ItemID, r.Count, idStick)
	}
}

// TestConsumePositioned: a recipe placed in the BOTTOM-LEFT corner (not the top-left) consumes the
// POSITIONED cells (mapped via left/top), not slots 1-2 blindly. We use a horizontal 1x2 stick... no —
// the stick pattern is vertical 2x1. Place it in the RIGHT column (cells 1 and 3) and assert the consume
// hits the right column, proving the positioned-region mapping (Left offset) is correct.
func TestConsumePositioned(t *testing.T) {
	loop, p, inv := craftLoop(t)

	// Vertical stick pattern in the RIGHT column: grid cell 1 (slot 2) + cell 3 (slot 4).
	inv.set(2, stk(idOakPlanks, 1)) // grid cell 1 (top-right)
	inv.set(4, stk(idOakPlanks, 1)) // grid cell 3 (bottom-right)

	loop.slotChangedCraftingGrid(playerCraftView(inv))
	if r := inv.get(0); r.ItemID != idStick || r.Count != 4 {
		t.Fatalf("result (right column) = id=%d count=%d, want id=%d count=4 (shrink matches anywhere)", r.ItemID, r.Count, idStick)
	}

	loop.doClick(p, inv, 0, 0, containerInputPickup)

	// The consume must hit the RIGHT column (cells 1,3 -> slots 2,4), emptying them — NOT the left column.
	if s := inv.get(2); s.Count != 0 {
		t.Fatalf("right-top (slot 2) after take = count=%d, want 0 (consumed)", s.Count)
	}
	if s := inv.get(4); s.Count != 0 {
		t.Fatalf("right-bottom (slot 4) after take = count=%d, want 0 (consumed)", s.Count)
	}
	// The left column was always empty and must remain so (the consume did not touch the wrong cells).
	if s := inv.get(1); s.Count != 0 {
		t.Fatalf("left-top (slot 1) after take = count=%d, want 0 (untouched)", s.Count)
	}
	if s := inv.get(3); s.Count != 0 {
		t.Fatalf("left-bottom (slot 3) after take = count=%d, want 0 (untouched)", s.Count)
	}
}

// TestNoMatchEmptyResult: a non-matching grid leaves result slot 0 empty. (A single dirt and two dirt
// have no vanilla recipe — verified against the level/recipe Go oracle, which the plugin mirrors 1:1.)
func TestNoMatchEmptyResult(t *testing.T) {
	loop, _, inv := craftLoop(t)

	// A single dirt does not match any recipe.
	inv.set(1, stk(idDirt, 1))
	loop.slotChangedCraftingGrid(playerCraftView(inv))
	if r := inv.get(0); r.Count != 0 {
		t.Fatalf("result for single dirt = count=%d/id=%d, want 0 (no match)", r.Count, r.ItemID)
	}

	// Two dirt (diagonal) also yields no result.
	inv.set(4, stk(idDirt, 1))
	loop.slotChangedCraftingGrid(playerCraftView(inv))
	if r := inv.get(0); r.Count != 0 {
		t.Fatalf("result for dirt+dirt = count=%d/id=%d, want 0 (no match)", r.Count, r.ItemID)
	}
}
