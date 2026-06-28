# infinite_loop.star — a runaway loop INSIDE a def so the STEP BUDGET (not the
# parser) stops it. A TOP-LEVEL loop is a PARSE reject ("for loop not
# within a function", steps=0) — that would make the budget test pass for the
# WRONG reason (P1). Conditional loops are OFF by default in the safe dialect,
# so this uses `for i in range(...)` (P2).
def spin():
    x = 0
    for i in range(100000000):
        x = x + 1
    return x

spin()
