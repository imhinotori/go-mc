# runawaymatcher: a matcher whose body burns far more than the step budget
# (a huge comprehension) — the fresh budget-bounded thread halts it with an
# *EvalError ("too many steps") so Match returns ok=false and the caller is NOT
# hung. while-loops are off in the sandbox dialect, so the runaway is expressed
# as a giant range comprehension (deterministic, no I/O).

def match(grid):
    total = 0
    for x in [i * i for i in range(100000000)]:
        total += x
    return {"id": 1, "count": total}

set_recipe_matcher(match)
