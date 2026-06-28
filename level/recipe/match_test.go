package recipe

import "testing"

// loadOne parses a single embedded recipe by id (test helper).
func loadOne(t *testing.T, id string) Recipe {
	t.Helper()
	res, err := newResolver()
	if err != nil {
		t.Fatalf("newResolver: %v", err)
	}
	b, err := TableJSON(id)
	if err != nil {
		t.Fatalf("TableJSON %s: %v", id, err)
	}
	r, err := res.parseRecipe(id, b)
	if err != nil {
		t.Fatalf("parseRecipe %s: %v", id, err)
	}
	return r
}

// st builds a Stack (id, count) — count 1 by default for a filled cell.
func st(id int) Stack { return Stack{ID: id, Count: 1} }

var empty = Stack{}

// TestShapedShrink: a 2x1 stick pattern (two vertically-stacked planks) in the
// bottom-left corner of a 3x3 grid MATCHES via the bounding-box shrink; filling
// all 9 cells does NOT.
//
// 1:1 CraftingInput.ofPositioned (the bounding-box SHRINK) + ShapedRecipePattern.matches
func TestShapedShrink(t *testing.T) {
	stick := loadOne(t, "minecraft:stick")
	oak := idOf(t, "minecraft:oak_planks")

	// 3x3 grid, two oak planks stacked in the bottom-left (rows 1 and 2, col 0).
	grid := []Stack{
		empty, empty, empty,
		st(oak), empty, empty,
		st(oak), empty, empty,
	}
	if !MatchShaped(stick.Shaped, ofPositioned(grid, 3, 3)) {
		t.Error("a 2x1 plank column anywhere in a 3x3 should match stick (shrink)")
	}

	// All 9 cells filled with planks: ingredientCount 9 != 2 -> no match.
	full := make([]Stack, 9)
	for i := range full {
		full[i] = st(oak)
	}
	if MatchShaped(stick.Shaped, ofPositioned(full, 3, 3)) {
		t.Error("a full 3x3 of planks must NOT match the 2x1 stick recipe")
	}
}

// TestShapedMirror: an ASYMMETRIC shaped recipe matches BOTH its layout and its
// horizontal mirror; a symmetrical recipe matches without a separate mirror try.
//
// 1:1 ShapedRecipePattern.matches (the mirror-then-unmirror try order)
func TestShapedMirror(t *testing.T) {
	// Build a synthetic asymmetric 2-wide recipe: [A, empty] on row 0 (A left,
	// empty right). Its mirror is [empty, A].
	a := idOf(t, "minecraft:stick")
	asym := &Shaped{
		Width:           2,
		Height:          1,
		Ingredients:     []Ingredient{newIngredient([]int{a}), {}},
		IngredientCount: 1,
		Result:          Stack{ID: idOf(t, "minecraft:stone"), Count: 1},
	}
	asym.Symmetrical = isSymmetrical(asym.Width, asym.Height, asym.Ingredients)
	if asym.Symmetrical {
		t.Fatal("[A, empty] should be asymmetric")
	}
	// Layout: A on the left (matches unmirrored). But a 1-wide footprint shrinks
	// to width 1, not 2 -> use a 2-wide footprint by filling with a non-matching
	// marker is impossible (would change ingredientCount). Instead test the
	// scan directly with a 2-wide positioned input that keeps width 2.
	left := Positioned{Cells: []Stack{st(a), empty}, Width: 2, Height: 1, Left: 0, Top: 0}
	if !MatchShaped(asym, left) {
		t.Error("[A, empty] should match input [A, empty] (unmirrored)")
	}
	right := Positioned{Cells: []Stack{empty, st(a)}, Width: 2, Height: 1, Left: 0, Top: 0}
	if !MatchShaped(asym, right) {
		t.Error("[A, empty] should match the MIRROR input [empty, A]")
	}

	// A symmetrical recipe [A, A] matches [A, A] and is flagged symmetrical.
	sym := &Shaped{
		Width:           2,
		Height:          1,
		Ingredients:     []Ingredient{newIngredient([]int{a}), newIngredient([]int{a})},
		IngredientCount: 2,
		Result:          Stack{ID: idOf(t, "minecraft:stone"), Count: 1},
	}
	sym.Symmetrical = isSymmetrical(sym.Width, sym.Height, sym.Ingredients)
	if !sym.Symmetrical {
		t.Error("[A, A] should be symmetrical")
	}
	symInput := Positioned{Cells: []Stack{st(a), st(a)}, Width: 2, Height: 1}
	if !MatchShaped(sym, symInput) {
		t.Error("[A, A] should match [A, A]")
	}
}

// TestShapelessMultiset: a shapeless recipe matches iff the grid's item multiset
// covers the ingredients exactly; order-independent; one extra/missing fails.
//
// 1:1 ShapelessRecipe.matches (ingredientCount gate + StackedItemContents multiset)
func TestShapelessMultiset(t *testing.T) {
	// Synthetic 2-ingredient shapeless: {stick, stone} -> diamond (order free).
	stick := idOf(t, "minecraft:stick")
	stone := idOf(t, "minecraft:stone")
	r := &Shapeless{
		Ingredients: []Ingredient{newIngredient([]int{stick}), newIngredient([]int{stone})},
		Result:      Stack{ID: idOf(t, "minecraft:diamond"), Count: 1},
	}
	// Order A: stick, stone, empty...
	if !MatchShapeless(r, []Stack{st(stick), st(stone), empty}) {
		t.Error("{stick,stone} should match [stick, stone]")
	}
	// Order B (swapped): stone, stick.
	if !MatchShapeless(r, []Stack{st(stone), st(stick)}) {
		t.Error("{stick,stone} should match [stone, stick] (order-independent)")
	}
	// Missing one: only stick.
	if MatchShapeless(r, []Stack{st(stick)}) {
		t.Error("{stick,stone} must NOT match [stick] (missing stone)")
	}
	// Extra: stick, stone, stone.
	if MatchShapeless(r, []Stack{st(stick), st(stone), st(stone)}) {
		t.Error("{stick,stone} must NOT match [stick, stone, stone] (extra item)")
	}
}

// TestCookingMatch: a smelting recipe (iron_ore -> iron_ingot) matches a
// single-cell iron_ore; stonecutting matches its single input.
//
// 1:1 SingleItemRecipe.matches (input.test(item))
func TestCookingMatch(t *testing.T) {
	smelt := loadOne(t, "minecraft:iron_ingot_from_smelting_iron_ore")
	if smelt.Type != TypeCooking {
		t.Fatalf("iron smelting type = %v, want cooking", smelt.Type)
	}
	ironOre := idOf(t, "minecraft:iron_ore")
	if !MatchCooking(smelt.Cooking, st(ironOre)) {
		t.Error("iron_ore should smelt to iron_ingot")
	}
	if MatchCooking(smelt.Cooking, st(idOf(t, "minecraft:stone"))) {
		t.Error("stone must NOT match the iron smelting recipe")
	}
	if smelt.Cooking.Result.ID != idOf(t, "minecraft:iron_ingot") {
		t.Errorf("iron smelting result = %d, want iron_ingot", smelt.Cooking.Result.ID)
	}

	// Stonecutting: andesite -> andesite_slab.
	cut := loadOne(t, "minecraft:andesite_slab_from_andesite_stonecutting")
	if cut.Type != TypeStonecutting {
		t.Fatalf("andesite stonecutting type = %v, want stonecutting", cut.Type)
	}
	if !MatchStonecutting(cut.Stonecutting, st(idOf(t, "minecraft:andesite"))) {
		t.Error("andesite should cut to andesite_slab")
	}
}

// TestTagIngredientMatch: the stick recipe matches a grid of 2 OAK planks
// (the #minecraft:planks tag member-set test feeds the matcher) + the per-cell
// used mask covers the two real grid positions.
//
// 1:1 Ingredient.test (tag member set) through MatchShaped
func TestTagIngredientMatch(t *testing.T) {
	all := mustLoadAll(t)
	oak := idOf(t, "minecraft:oak_planks")
	stickID := idOf(t, "minecraft:stick")

	// 3x3 grid: two oak planks stacked at col 1, rows 0+1 (a 2x1 footprint).
	grid := []Stack{
		empty, st(oak), empty,
		empty, st(oak), empty,
		empty, empty, empty,
	}
	result, used, ok := Match(all, grid, 3, 3)
	if !ok {
		t.Fatal("two oak planks should craft a stick via the #minecraft:planks tag")
	}
	if result.ID != stickID || result.Count != 4 {
		t.Errorf("result = %+v, want {stick, 4}", result)
	}
	// used mask: cells 1 and 4 (the two planks) used.
	if !used[1] || !used[4] {
		t.Errorf("used mask should cover real cells 1 and 4; got %v", used)
	}
	if used[0] || used[2] {
		t.Errorf("used mask must not mark empty cells; got %v", used)
	}
}

func mustLoadAll(t *testing.T) []Recipe {
	t.Helper()
	all, err := ParseAll()
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	return all
}
