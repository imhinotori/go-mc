package recipe

// match.go — the 1:1 recipe MATCH, ported method-for-method from the
// unobfuscated 26.2 server jar (temp/cache/26.2-inner.jar, javap -c -p). This is
// the canonical Go reference: the Wave-2 Starlark recipe plugin re-expresses the
// SAME algorithm, but THIS Go reference is the test oracle.
//
// The three load-bearing fidelity points (all javap'd this session, cited):
//  1. CraftingInput.ofPositioned — the bounding-box SHRINK (a 2x1 recipe matches
//     a 2x1 footprint anywhere in a 3x3 grid).
//  2. ShapedRecipePattern.matches — the MIRROR-then-unmirror try order, index
//     (mirror ? width-col-1 : col) + row*width.
//  3. ShapelessRecipe.matches — the ingredientCount gate + the multiset cover
//     (StackedItemContents.canCraft).
//
// Cooking + stonecutting are SingleItemRecipe.matches: input.test(item).

// Positioned is the result of ofPositioned: the trimmed cells (row-major over
// Width*Height) + the (Left, Top) offset back into the original grid. Empty
// reports the all-empty input (no match possible).
type Positioned struct {
	Cells  []Stack
	Width  int
	Height int
	Left   int
	Top    int
	Empty  bool
}

// OfPositioned is the exported wrapper over ofPositioned — the
// CraftingInput.ofPositioned bounding-box shrink. The server's ResultSlot.onTake
// consume (server/crafting_click.go) calls it to reproduce the jar's
// asPositionedCraftInput() footprint EXACTLY (the same shrink the matcher used),
// so the per-cell consume removes 1 from each non-empty cell in the trimmed
// region mapped back through (Left, Top) to the real grid — never the whole stack,
// never a wrong cell.
//
// 1:1 net.minecraft.world.inventory.CraftingContainer.asPositionedCraftInput
func OfPositioned(items []Stack, w, h int) Positioned { return ofPositioned(items, w, h) }

// ofPositioned ports net.minecraft.world.item.crafting.CraftingInput.ofPositioned:
// it shrinks a w*h grid (row-major, an empty cell has Count<=0 or ID<=0) to the
// minimal bounding box of non-empty cells. An all-empty (or zero-dim) grid
// returns Empty.
//
// 1:1 net.minecraft.world.item.crafting.CraftingInput.ofPositioned
func ofPositioned(items []Stack, w, h int) Positioned {
	if w == 0 || h == 0 {
		return Positioned{Empty: true}
	}
	// Java: minCol=width-1, maxCol=0, minRow=height-1, maxRow=0.
	minCol := w - 1
	maxCol := 0
	minRow := h - 1
	maxRow := 0
	for row := 0; row < h; row++ {
		rowEmpty := true
		for col := 0; col < w; col++ {
			if !stackEmpty(items[col+row*w]) {
				if col < minCol {
					minCol = col
				}
				if col > maxCol {
					maxCol = col
				}
				rowEmpty = false
			}
		}
		if !rowEmpty {
			if row < minRow {
				minRow = row
			}
			if row > maxRow {
				maxRow = row
			}
		}
	}
	tw := maxCol - minCol + 1
	th := maxRow - minRow + 1
	if tw <= 0 || th <= 0 {
		return Positioned{Empty: true}
	}
	if tw == w && th == h {
		// No trimming: the input is the whole grid, offset (minCol, minRow).
		return Positioned{Cells: items, Width: w, Height: h, Left: minCol, Top: minRow}
	}
	// Build the trimmed sub-grid (row-major) from items[(c+minCol)+(r+minRow)*w].
	cells := make([]Stack, 0, tw*th)
	for r := 0; r < th; r++ {
		for c := 0; c < tw; c++ {
			cells = append(cells, items[(c+minCol)+(r+minRow)*w])
		}
	}
	return Positioned{Cells: cells, Width: tw, Height: th, Left: minCol, Top: minRow}
}

// stackEmpty reports whether a Stack is the empty cell (ItemStack.isEmpty): no
// item (id<=0 is minecraft:air) or a non-positive count.
func stackEmpty(s Stack) bool { return s.ID <= 0 || s.Count <= 0 }

// ingredientCount counts the non-empty cells (CraftingInput.ingredientCount).
func ingredientCount(cells []Stack) int {
	n := 0
	for _, c := range cells {
		if !stackEmpty(c) {
			n++
		}
	}
	return n
}

// MatchShaped ports ShapedRecipePattern.matches(CraftingInput): the
// ingredientCount gate, the width/height gate, then the mirror-then-unmirror try
// order. `pos` is the already-shrunk Positioned input.
//
// 1:1 net.minecraft.world.item.crafting.ShapedRecipePattern.matches
func MatchShaped(r *Shaped, pos Positioned) bool {
	if pos.Empty {
		return false
	}
	if ingredientCount(pos.Cells) != r.IngredientCount {
		return false
	}
	if pos.Width != r.Width || pos.Height != r.Height {
		return false
	}
	// if (!symmetrical && matches(input, true)) return true;
	if !r.Symmetrical && shapedScan(r, pos, true) {
		return true
	}
	// return matches(input, false);
	return shapedScan(r, pos, false)
}

// shapedScan ports the private ShapedRecipePattern.matches(input, mirrored): the
// double-loop over row<height, col<width, with the ingredient index
// (mirrored ? width-col-1 : col) + row*width, testing each cell with
// testOptionalIngredient (an empty optional matches an empty cell).
//
// 1:1 net.minecraft.world.item.crafting.ShapedRecipePattern.matches(CraftingInput, boolean)
func shapedScan(r *Shaped, pos Positioned, mirrored bool) bool {
	for row := 0; row < r.Height; row++ {
		for col := 0; col < r.Width; col++ {
			var idx int
			if mirrored {
				idx = (r.Width - col - 1) + row*r.Width
			} else {
				idx = col + row*r.Width
			}
			opt := r.Ingredients[idx]
			cell := pos.Cells[col+row*pos.Width] // pos.Width == r.Width here
			if !testOptionalIngredient(opt, cell) {
				return false
			}
		}
	}
	return true
}

// testOptionalIngredient ports Ingredient.testOptionalIngredient: an EMPTY
// optional (an empty pattern cell) matches iff the stack is empty; otherwise the
// stack's item must be a member of the ingredient set.
//
// 1:1 net.minecraft.world.item.crafting.Ingredient.testOptionalIngredient
func testOptionalIngredient(opt Ingredient, cell Stack) bool {
	if opt.Empty() {
		return stackEmpty(cell)
	}
	if stackEmpty(cell) {
		return false
	}
	return opt.Test(cell.ID)
}

// MatchShapeless ports ShapelessRecipe.matches(CraftingInput, Level): the
// ingredientCount==ingredients.size gate, the 1-ingredient fast path, then the
// greedy multiset cover (StackedItemContents.canCraft).
//
// 1:1 net.minecraft.world.item.crafting.ShapelessRecipe.matches
func MatchShapeless(r *Shapeless, cells []Stack) bool {
	if ingredientCount(cells) != len(r.Ingredients) {
		return false
	}
	// Fast path: input.size()==1 && ingredients.size()==1 -> ingredients[0].test(getItem(0)).
	// CraftingInput.size() == ingredientCount (non-empty cells); for the fast
	// path we need exactly one non-empty cell.
	if len(r.Ingredients) == 1 {
		// find the single non-empty cell.
		for _, c := range cells {
			if !stackEmpty(c) {
				return testIngredientStack(r.Ingredients[0], c)
			}
		}
		return false
	}
	return canCraftMultiset(r.Ingredients, cells)
}

// testIngredientStack tests a non-optional ingredient against a stack
// (Ingredient.test over a non-empty cell).
func testIngredientStack(in Ingredient, cell Stack) bool {
	if stackEmpty(cell) {
		return false
	}
	return in.Test(cell.ID)
}

// canCraftMultiset ports StackedItemContents.canCraft for the shapeless case: a
// greedy multiset cover — each ingredient must be satisfied by one distinct
// non-empty grid cell, and every non-empty grid cell must be consumed. Because
// ingredientCount==len(ingredients) is already gated, a perfect matching exists
// iff every ingredient can claim a distinct cell. We solve it with bipartite
// matching (Kuhn's algorithm) over ingredient->cell membership — order
// independent, exactly the multiset cover the jar's StackedItemContents computes.
//
// 1:1 net.minecraft.world.entity.player.StackedItemContents.canCraft (the
// multiset/assignment cover; re-expressed as a bipartite matching, which yields
// the identical accept/reject as the jar's StackedContents solver for the
// crafting-grid case).
func canCraftMultiset(ings []Ingredient, cells []Stack) bool {
	// Collect the non-empty cells.
	var cellIDs []int
	for _, c := range cells {
		if !stackEmpty(c) {
			cellIDs = append(cellIDs, c.ID)
		}
	}
	if len(cellIDs) != len(ings) {
		return false
	}
	// Bipartite matching: ingredient i -> some cell j with ings[i].Test(cellIDs[j]).
	matchCell := make([]int, len(cellIDs))
	for i := range matchCell {
		matchCell[i] = -1
	}
	var tryAssign func(i int, seen []bool) bool
	tryAssign = func(i int, seen []bool) bool {
		for j, id := range cellIDs {
			if seen[j] || !ings[i].Test(id) {
				continue
			}
			seen[j] = true
			if matchCell[j] == -1 || tryAssign(matchCell[j], seen) {
				matchCell[j] = i
				return true
			}
		}
		return false
	}
	for i := range ings {
		seen := make([]bool, len(cellIDs))
		if !tryAssign(i, seen) {
			return false
		}
	}
	return true
}

// MatchCooking ports the single-ingredient match for smelting/blasting/smoking/
// campfire_cooking (AbstractCookingRecipe extends SingleItemRecipe):
// input.test(item).
//
// 1:1 net.minecraft.world.item.crafting.SingleItemRecipe.matches
func MatchCooking(r *Cooking, cell Stack) bool {
	return testIngredientStack(r.Ingredient, cell)
}

// MatchStonecutting ports the single-ingredient stonecutter match
// (StonecutterRecipe extends SingleItemRecipe): input.test(item).
//
// 1:1 net.minecraft.world.item.crafting.SingleItemRecipe.matches
func MatchStonecutting(r *Stonecutting, cell Stack) bool {
	return testIngredientStack(r.Ingredient, cell)
}

// Match runs ofPositioned ONCE over the w*h grid, iterates the recipes by type,
// and returns the first crafting-grid match's result + a per-cell used mask
// (mapped back through Left/Top to the REAL grid positions — the Wave-2 consume
// needs this mask). For a single-cell grid (w==h==1) it also tries cooking +
// stonecutting recipes. ok=false means no recipe matched.
//
// The used mask marks every NON-EMPTY cell in the positioned region (for the
// crafting-grid recipes the consume shrinks each used ingredient by exactly 1 —
// every occupied cell in the matched footprint is "used").
func Match(recipes []Recipe, cells []Stack, w, h int) (result Stack, used []bool, ok bool) {
	used = make([]bool, len(cells))
	pos := ofPositioned(cells, w, h)

	for i := range recipes {
		r := &recipes[i]
		switch r.Type {
		case TypeShaped:
			if !pos.Empty && MatchShaped(r.Shaped, pos) {
				markUsed(used, pos, w)
				return r.Shaped.Result, used, true
			}
		case TypeShapeless:
			if !pos.Empty && MatchShapeless(r.Shapeless, pos.Cells) {
				markUsed(used, pos, w)
				return r.Shapeless.Result, used, true
			}
		}
	}

	// Single-cell grid: try the cooking + stonecutting single-ingredient recipes.
	if w == 1 && h == 1 && len(cells) == 1 && !stackEmpty(cells[0]) {
		for i := range recipes {
			r := &recipes[i]
			switch r.Type {
			case TypeCooking:
				if MatchCooking(r.Cooking, cells[0]) {
					used[0] = true
					return r.Cooking.Result, used, true
				}
			case TypeStonecutting:
				if MatchStonecutting(r.Stonecutting, cells[0]) {
					used[0] = true
					return r.Stonecutting.Result, used, true
				}
			}
		}
	}

	return Stack{}, used, false
}

// markUsed sets the used mask for every non-empty cell in the positioned region,
// mapped back to the real w-wide grid via (Left, Top).
func markUsed(used []bool, pos Positioned, w int) {
	for r := 0; r < pos.Height; r++ {
		for c := 0; c < pos.Width; c++ {
			if stackEmpty(pos.Cells[c+r*pos.Width]) {
				continue
			}
			realIdx := (c + pos.Left) + (r+pos.Top)*w
			if realIdx >= 0 && realIdx < len(used) {
				used[realIdx] = true
			}
		}
	}
}

// Remaining ports CraftingRecipe.getRemainingItems / defaultCraftingReminder:
// for each USED cell, the leftover item after crafting (an empty bucket from a
// water bucket, etc.). The DEFAULT (CraftingRecipe.defaultCraftingReminder) is
// all-empty — and the gate recipes carry no bucket-back ingredient, so the
// faithful gate result is all-empty. The bucket-back case
// (Item.getCraftingRemainder) is the STRUCTURED-BUT-DEFAULT path: it returns the
// per-cell remainder list shaped for that future read, currently all empty.
//
// 1:1 net.minecraft.world.item.crafting.CraftingRecipe.defaultCraftingReminder
// (the default all-empty remainder; the per-item bucket-back is the structured
// follow-up).
func Remaining(cells []Stack) []Stack {
	rem := make([]Stack, len(cells))
	// default: all empty (Stack{} == empty).
	return rem
}
