# greeter: registers an on_block_break hook ONCE at load. The hook calls the
# host-injected `count` test builtin so the test can assert it fired (and how
# many times). `register` captures the callable into the bus at load time —
# the hook itself only runs when a real on_block_break event is emitted.

def on_break(x, y, z, state, player_id):
    log("block broke at %d,%d,%d" % (x, y, z))
    count(1)

register("on_block_break", on_break)
