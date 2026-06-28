# badmatcher: a matcher that raises at call time (a runtime error in the body).
# Match must log+isolate it and return ok=false — never a panic, never a hung
# tick.

def match(grid):
    fail("matcher boom")  # the host-injected test fail() builtin raises

set_recipe_matcher(match)
