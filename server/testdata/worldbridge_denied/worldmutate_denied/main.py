# worldmutate_denied — the CAPABILITY-GATE testdata plugin (Plan 26-03). It is
# identical to worldmutate EXCEPT it declares NO capabilities (no "world.write"). It
# issues the SAME set_block request off-tick, but the owner enforces the capSet at
# apply: the request is DROPPED with a capError-style log and the world is left
# UNCHANGED (threat T-26-09 — no silent elevation of privilege). The tagged test fires
# EventBlockBreak against this plugin and asserts GetBlock is unchanged after the
# round-trip (the denied request never reached SetBlock).

PLACE_STATE = 1


def on_break(x, y, z, state, player_id):
    # The SAME request a granted plugin makes — but this plugin lacks world.write, so
    # the owner drops it at apply. The producer is unconditional (the gate lives on the
    # owner), which is what proves the gate, not the producer, is the boundary.
    set_block(x, y + 1, z, PLACE_STATE)


register("on_block_break", on_break)
