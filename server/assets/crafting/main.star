# crafting — the bundled vanilla recipe-PROVIDER plugin (PLUGIN-05, Plan 25-02). The vanilla
# crafting MATCH re-expressed AS a Starlark plugin: a LITERAL, method-for-method port of the same
# unobfuscated 26.2 jar algorithm the Go reference (level/recipe/match.go) implements, so a default
# Sulfur server crafts every vanilla recipe THROUGH the plugin API (set_recipe_matcher), not a
# hardcoded Go table. This is the SECOND-domain dogfood: recipe RESOLUTION through the same host the
# entity-AI plugin (vanilla_pig) uses, via the NEW value-returning Match seam (host READS {id,count}).
#
# The recipe DATA comes from the host: recipes() returns the Go-parsed table (server/recipe_embed.go
# injects it via Manager.SetRecipeTable). Starlark has no json/file builtin (sandbox), so the parse
# stays in Go (level/recipe.ParseAll) and the MATCH stays here — proving the plugin drives crafting.
#
# Jar citations (each algorithm, javap -c -p temp/cache/26.2-inner.jar this session):
#   _of_positioned  : net.minecraft.world.item.crafting.CraftingInput.ofPositioned (bounding-box SHRINK)
#   _matches_shaped : net.minecraft.world.item.crafting.ShapedRecipePattern.matches (MIRROR-then-unmirror)
#   _matches_shapeless : net.minecraft.world.item.crafting.ShapelessRecipe.matches (multiset cover)
#   _matches_single : net.minecraft.world.item.crafting.SingleItemRecipe.matches (cooking/stonecutting)
# Re-expressed idiomatically in Starlark (NO GPL paste); the observable accept/reject is identical to
# the bytecode and to the Go oracle (level/recipe.Match) — the test gate proves plugin == Go.

# The Go-parsed recipe table (server/recipe_embed.go -> Manager.SetRecipeTable). Each entry is a dict:
#   {"type": "shaped",    "w","h","ings": [set,...] (row-major), "icount", "rid","rcount"}
#   {"type": "shapeless", "ings": [set,...], "rid","rcount"}
#   {"type": "cooking",   "ing": set, "rid","rcount"}
#   {"type": "stonecutting","ing": set, "rid","rcount"}
# An ingredient `set` is a Starlark dict used as a membership set (id -> True). An EMPTY dict ({}) is
# the empty-optional pattern cell. recipes() is None until the host injects the table.
TABLE = recipes()


def _empty(cell):
    # ItemStack.isEmpty: id<=0 (air) or count<=0.
    return cell[0] <= 0 or cell[1] <= 0


def _ing_empty(ing):
    # Ingredient.Empty: the empty-optional cell (no members).
    return len(ing) == 0


def _ing_test(ing, id):
    # Ingredient.test: set membership.
    return id in ing


def _ingredient_count(cells):
    # CraftingInput.ingredientCount: the non-empty cell count.
    n = 0
    for c in cells:
        if not _empty(c):
            n += 1
    return n


def _of_positioned(cells, w, h):
    # 1:1 CraftingInput.ofPositioned — the bounding-box SHRINK. Returns
    # (trimmed_cells, tw, th, left, top, empty). Java init: minCol=w-1, maxCol=0, minRow=h-1, maxRow=0.
    if w == 0 or h == 0:
        return (None, 0, 0, 0, 0, True)
    min_col = w - 1
    max_col = 0
    min_row = h - 1
    max_row = 0
    for row in range(h):
        row_empty = True
        for col in range(w):
            if not _empty(cells[col + row * w]):
                if col < min_col:
                    min_col = col
                if col > max_col:
                    max_col = col
                row_empty = False
        if not row_empty:
            if row < min_row:
                min_row = row
            if row > max_row:
                max_row = row
    tw = max_col - min_col + 1
    th = max_row - min_row + 1
    if tw <= 0 or th <= 0:
        return (None, 0, 0, 0, 0, True)
    trimmed = []
    for r in range(th):
        for c in range(tw):
            trimmed.append(cells[(c + min_col) + (r + min_row) * w])
    return (trimmed, tw, th, min_col, min_row, False)


def _test_optional(opt, cell):
    # 1:1 Ingredient.testOptionalIngredient: an empty optional matches an empty cell; else membership.
    if _ing_empty(opt):
        return _empty(cell)
    if _empty(cell):
        return False
    return _ing_test(opt, cell[0])


def _shaped_scan(rec, tcells, tw, th, mirrored):
    # 1:1 ShapedRecipePattern.matches(input, mirrored): index (mirror?width-col-1:col)+row*width.
    rw = rec["w"]
    rh = rec["h"]
    ings = rec["ings"]
    for row in range(rh):
        for col in range(rw):
            if mirrored:
                idx = (rw - col - 1) + row * rw
            else:
                idx = col + row * rw
            opt = ings[idx]
            cell = tcells[col + row * tw]  # tw == rw here
            if not _test_optional(opt, cell):
                return False
    return True


def _matches_shaped(rec, tcells, tw, th):
    # 1:1 ShapedRecipePattern.matches: ingredientCount gate, w/h gate, mirror-then-unmirror.
    if _ingredient_count(tcells) != rec["icount"]:
        return False
    if tw != rec["w"] or th != rec["h"]:
        return False
    if (not rec["sym"]) and _shaped_scan(rec, tcells, tw, th, True):
        return True
    return _shaped_scan(rec, tcells, tw, th, False)


def _matches_shapeless(rec, cells):
    # 1:1 ShapelessRecipe.matches: ingredientCount==len gate, 1-ingredient fast path, then the
    # multiset cover (StackedItemContents.canCraft) as a greedy bipartite assignment.
    ings = rec["ings"]
    nonempty = [c for c in cells if not _empty(c)]
    if len(nonempty) != len(ings):
        return False
    if len(ings) == 1:
        return _ing_test(ings[0], nonempty[0][0])
    # Bipartite matching: ingredient i -> some distinct cell j with ings[i].test(cell[j]).
    cell_ids = [c[0] for c in nonempty]
    match_cell = [-1] * len(cell_ids)

    def try_assign(i, seen):
        for j in range(len(cell_ids)):
            if seen[j] or not _ing_test(ings[i], cell_ids[j]):
                continue
            seen[j] = True
            if match_cell[j] == -1 or try_assign(match_cell[j], seen):
                match_cell[j] = i
                return True
        return False

    for i in range(len(ings)):
        seen = [False] * len(cell_ids)
        if not try_assign(i, seen):
            return False
    return True


def _matches_single(rec, cell):
    # 1:1 SingleItemRecipe.matches (cooking + stonecutting): input.test(item).
    if _empty(cell):
        return False
    return _ing_test(rec["ing"], cell[0])


def match(grid):
    # The value-returning matcher (set_recipe_matcher target). grid is the host payload:
    #   {"w": int, "h": int, "cells": [(id,count), ...]}  (row-major).
    # Returns {"id","count"} on the FIRST matching recipe (Go reference Match order: shaped/shapeless
    # over the grid, then cooking/stonecutting for a 1x1 grid), or None.
    if TABLE == None:
        return None
    w = grid["w"]
    h = grid["h"]
    cells = grid["cells"]

    tcells, tw, th, left, top, empty = _of_positioned(cells, w, h)

    if not empty:
        for rec in TABLE:
            t = rec["type"]
            if t == "shaped":
                if _matches_shaped(rec, tcells, tw, th):
                    return {"id": rec["rid"], "count": rec["rcount"]}
            elif t == "shapeless":
                if _matches_shapeless(rec, tcells):
                    return {"id": rec["rid"], "count": rec["rcount"]}

    # 1x1 grid: cooking + stonecutting single-ingredient recipes (Go reference Match tail).
    if w == 1 and h == 1 and len(cells) == 1 and not _empty(cells[0]):
        for rec in TABLE:
            t = rec["type"]
            if t == "cooking" or t == "stonecutting":
                if _matches_single(rec, cells[0]):
                    return {"id": rec["rid"], "count": rec["rcount"]}

    return None


def remaining(grid):
    # 1:1 CraftingRecipe.defaultCraftingReminder: the default all-empty remainder (the gate recipes
    # carry no bucket-back ingredient). None == no leftover. The per-item getCraftingRemainder bucket-
    # back is the structured-but-default follow-up (mirrors level/recipe.Remaining).
    return None


set_recipe_matcher(match)
set_recipe_remaining(remaining)
