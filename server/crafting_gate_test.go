package server

// crafting_gate_test.go — PLUGIN-05 (Plan 25-02) Task 3: THE GATE. Proves crafting works THROUGH the
// plugin Match path (not a hardcoded Go table) for BOTH a vanilla recipe AND a custom operator recipe,
// and that a default server (no plugins/ dir) crafts via the embedded boot-load. The signature
// assertion: the result comes from Manager.Match (a plugin Call) and the consume is exactly
// 1-per-used-cell (grid counts asserted before/after).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/plugin/host"
)

// idDiamond is the custom-recipe result (1 dirt -> 1 diamond).
const idDiamond = 926

// customRecipeManager boot-loads the embedded vanilla crafting plugin, then loads the operator
// customrecipe plugin (copied from ../plugins/customrecipe into a temp dir) ON TOP — exactly the runtime
// order (embedded vanilla first, operator plugins/ second; the customrecipe matcher wins as the final
// registration and falls through to the vanilla table).
func customRecipeManager(t *testing.T) *host.Manager {
	t.Helper()
	m := host.New()
	if err := LoadCraftingPlugin(m); err != nil {
		t.Fatalf("LoadCraftingPlugin: %v", err)
	}
	// Materialize ../plugins/customrecipe into a temp plugins dir and load it.
	root := t.TempDir()
	dst := filepath.Join(root, "customrecipe")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"plugin.toml", "main.star"} {
		data, err := os.ReadFile(filepath.Join("..", "plugins", "customrecipe", name))
		if err != nil {
			t.Fatalf("read ../plugins/customrecipe/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := m.LoadDir(root); err != nil {
		t.Fatalf("LoadDir(customrecipe): %v", err)
	}
	if !m.HasRecipeMatcher() {
		t.Fatalf("no recipe matcher after loading customrecipe")
	}
	return m
}

// TestVanillaRecipeCrafts: a full end-to-end vanilla craft through the plugin Match path. Place 2 planks
// (vertical) in the 2x2 player grid, assert result slot 0 == the jar-correct stick recipe via the plugin
// matcher (NOT a hardcoded Go table), take it, assert the consume is exactly 1-per-used-cell.
func TestVanillaRecipeCrafts(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	p.gameMode = gameModeSurvival
	inv := ensureInventory(p)
	loop.SetPlugins(craftingManager(t)) // the embedded vanilla matcher only

	// Vertical stick pattern in the left column (grid cells 0,2 = window slots 1,3).
	inv.set(1, stk(idOakPlanks, 1))
	inv.set(3, stk(idOakPlanks, 1))

	loop.slotChangedCraftingGrid(playerCraftView(inv))
	r := inv.get(0)
	if r.ItemID != idStick || r.Count != 4 {
		t.Fatalf("vanilla result = id=%d count=%d, want id=%d count=4 (from Manager.Match)", r.ItemID, r.Count, idStick)
	}

	// Take it: exactly 1 from each used cell, both emptied (started with 1 each).
	loop.doClick(p, inv, 0, 0, containerInputPickup)
	if c := inv.getCarried(); c.ItemID != idStick || c.Count != 4 {
		t.Fatalf("carried = id=%d count=%d, want 4 sticks", c.ItemID, c.Count)
	}
	if inv.get(1).Count != 0 || inv.get(3).Count != 0 {
		t.Fatalf("consume not 1-per-cell: slot1=%d slot3=%d, want both 0", inv.get(1).Count, inv.get(3).Count)
	}
}

// TestCustomRecipeWorks: the operator customrecipe plugin adds 1 dirt -> 1 diamond (non-vanilla). After
// loading it alongside the vanilla plugin, the custom recipe crafts the SAME way through the SAME Match
// path — AND a vanilla recipe still crafts (the matcher-fallthrough model).
func TestCustomRecipeWorks(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	p.gameMode = gameModeSurvival
	inv := ensureInventory(p)
	loop.SetPlugins(customRecipeManager(t)) // vanilla + custom (custom matcher wins, falls through)

	// CUSTOM: 1 dirt in the 2x2 grid -> 1 diamond.
	inv.set(1, stk(idDirt, 1))
	loop.slotChangedCraftingGrid(playerCraftView(inv))
	if r := inv.get(0); r.ItemID != idDiamond || r.Count != 1 {
		t.Fatalf("custom result = id=%d count=%d, want id=%d count=1 (dirt->diamond)", r.ItemID, r.Count, idDiamond)
	}
	loop.doClick(p, inv, 0, 0, containerInputPickup)
	if c := inv.getCarried(); c.ItemID != idDiamond || c.Count != 1 {
		t.Fatalf("custom carried = id=%d count=%d, want 1 diamond", c.ItemID, c.Count)
	}
	if inv.get(1).Count != 0 {
		t.Fatalf("custom consume: slot1=%d, want 0 (1 dirt consumed)", inv.get(1).Count)
	}

	// VANILLA still works through the same (fallthrough) matcher: 2 planks vertical -> 4 sticks.
	inv.set(0, component.SlotData{Count: 0})
	inv.set(1, stk(idOakPlanks, 1))
	inv.set(3, stk(idOakPlanks, 1))
	loop.slotChangedCraftingGrid(playerCraftView(inv))
	if r := inv.get(0); r.ItemID != idStick || r.Count != 4 {
		t.Fatalf("vanilla (via fallthrough) = id=%d count=%d, want id=%d count=4", r.ItemID, r.Count, idStick)
	}
}

// TestEmbeddedBootLoadDefault: with NO operator plugins/ dir, a default server still crafts — the
// embedded vanilla plugin boot-loads into the Manager so vanilla crafting works out-of-the-box (the
// embedded-default decision, CONTEXT D-4). craftingManager loads ONLY the embedded plugin (no plugins/).
func TestEmbeddedBootLoadDefault(t *testing.T) {
	m := craftingManager(t) // embedded only — no operator plugins/ dir touched
	if !m.HasRecipeMatcher() {
		t.Fatal("default (embedded-only) server has no recipe matcher")
	}

	loop := NewTickLoop(newFakeClock())
	p := invPlayer(loop)
	p.gameMode = gameModeSurvival
	inv := ensureInventory(p)
	loop.SetPlugins(m)

	inv.set(1, stk(idOakPlanks, 1))
	inv.set(3, stk(idOakPlanks, 1))
	loop.slotChangedCraftingGrid(playerCraftView(inv))
	if r := inv.get(0); r.ItemID != idStick || r.Count != 4 {
		t.Fatalf("default-server craft = id=%d count=%d, want id=%d count=4 (embedded boot-load)", r.ItemID, r.Count, idStick)
	}
}
