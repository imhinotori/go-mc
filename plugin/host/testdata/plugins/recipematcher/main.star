# recipematcher: registers a value-returning recipe matcher via
# set_recipe_matcher(fn). The matcher receives the grid value and returns a
# {id, count} dict on a match, or None otherwise. It also reads back the
# Go-injected recipes() table to prove the host->plugin recipe channel works.
#
# This is the dogfood: set_recipe_matcher is the value-returning twin of
# register — the host READS the returned dict (Match), unlike Emit's void
# dispatch.

TABLE = recipes()  # Go-parsed recipe table (or None if the server didn't set it)

def match(grid):
    # grid is the host payload; the first element is the marker id the test feeds.
    # A grid whose first cell id == 1 (stick) crafts the result {id: 280, count: 4};
    # id == 0 (empty) returns None.
    first = grid[0]
    if first == 0:
        return None
    return {"id": 280, "count": 4}

def remaining(grid):
    # default getRemainingItems: no leftover (all-empty) -> None.
    return None

set_recipe_matcher(match)
set_recipe_remaining(remaining)
