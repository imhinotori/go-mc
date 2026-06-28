# customrecipe — an OPERATOR plugin that adds a NON-vanilla crafting recipe (1 dirt -> 1 diamond,
# shapeless, direct item ids), proving a custom recipe crafts THROUGH the same plugin Match path as the
# vanilla recipes (PLUGIN-05 gate, Plan 25-02).
#
# REGISTRATION MODEL — matcher-fallthrough (documented per the plan's "decide the registration model"):
# the host's set_recipe_matcher captures a SINGLE matcher (last-loaded wins — manager.go). Plugins load
# in directory order, so `customrecipe` (loaded AFTER `crafting`) registers the FINAL matcher. To keep
# every vanilla recipe working, this matcher checks its CUSTOM recipes FIRST, then FALLS THROUGH to the
# SAME 1:1 vanilla algorithm over the host recipe table (recipes()). The vanilla match helpers are
# re-included here (Starlark plugins cannot import one another) — identical to assets/crafting/main.star,
# so the fallthrough is byte-for-byte the vanilla behavior; only the custom-first check is added.
#
# This is the operator-extension dogfood: an operator drops this plugin into plugins/ and a brand-new
# recipe crafts with zero server changes, through the exact Match seam the vanilla recipes use.

TABLE = recipes()  # the host-injected vanilla recipe table (the fallthrough source)

# --- CUSTOM recipes (non-vanilla) -----------------------------------------------------------------
# 1 dirt (id 55) -> 1 diamond (id 926), shapeless single-ingredient. Direct ids (no tags) to sidestep
# tag resolution — purely to prove a custom recipe crafts.
CUSTOM = [
    {"kind": "shapeless1", "ing_id": 55, "rid": 926, "rcount": 1},
]


def _empty(cell):
    return cell[0] <= 0 or cell[1] <= 0


def _ing_empty(ing):
    return len(ing) == 0


def _ing_test(ing, id):
    return id in ing


def _ingredient_count(cells):
    n = 0
    for c in cells:
        if not _empty(c):
            n += 1
    return n


def _of_positioned(cells, w, h):
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
    if _ing_empty(opt):
        return _empty(cell)
    if _empty(cell):
        return False
    return _ing_test(opt, cell[0])


def _shaped_scan(rec, tcells, tw, th, mirrored):
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
            cell = tcells[col + row * tw]
            if not _test_optional(opt, cell):
                return False
    return True


def _matches_shaped(rec, tcells, tw, th):
    if _ingredient_count(tcells) != rec["icount"]:
        return False
    if tw != rec["w"] or th != rec["h"]:
        return False
    if (not rec["sym"]) and _shaped_scan(rec, tcells, tw, th, True):
        return True
    return _shaped_scan(rec, tcells, tw, th, False)


def _matches_shapeless(rec, cells):
    ings = rec["ings"]
    nonempty = [c for c in cells if not _empty(c)]
    if len(nonempty) != len(ings):
        return False
    if len(ings) == 1:
        return _ing_test(ings[0], nonempty[0][0])
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
    if _empty(cell):
        return False
    return _ing_test(rec["ing"], cell[0])


def _match_custom(cells, w, h):
    # The custom recipes: a 1x1 shapeless single-ingredient (1 dirt -> 1 diamond). Checked first.
    tcells, tw, th, left, top, empty = _of_positioned(cells, w, h)
    if empty:
        return None
    for rec in CUSTOM:
        if rec["kind"] == "shapeless1":
            # Exactly one non-empty cell of the custom ingredient id.
            if _ingredient_count(tcells) == 1 and tcells[0][0] == rec["ing_id"]:
                return {"id": rec["rid"], "count": rec["rcount"]}
    return None


def _match_vanilla(cells, w, h):
    # The 1:1 vanilla fallthrough over the host recipe table (identical to assets/crafting/main.star).
    if TABLE == None:
        return None
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
    if w == 1 and h == 1 and len(cells) == 1 and not _empty(cells[0]):
        for rec in TABLE:
            t = rec["type"]
            if t == "cooking" or t == "stonecutting":
                if _matches_single(rec, cells[0]):
                    return {"id": rec["rid"], "count": rec["rcount"]}
    return None


def match(grid):
    w = grid["w"]
    h = grid["h"]
    cells = grid["cells"]
    # CUSTOM first, then vanilla fallthrough (the registration model: custom-first + vanilla delegate).
    out = _match_custom(cells, w, h)
    if out != None:
        return out
    return _match_vanilla(cells, w, h)


def remaining(grid):
    return None


set_recipe_matcher(match)
set_recipe_remaining(remaining)
