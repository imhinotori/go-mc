# badhook: an on_block_break hook that always fails. Emit must isolate this
# error (log + continue) so a co-subscribed well-behaved hook (greeter) still
# fires and the caller survives.

def boom(x, y, z, state, player_id):
    fail("boom")

register("on_block_break", boom)
