# events: a server-side test fixture that registers ONE counting hook per core event.
# Each hook calls the host-injected `count(event_name)` test builtin so the server
# seam tests can assert that exactly one emit fired per discrete occurrence (and that
# the count is independent of entity count — THE GATE). The on_damage hook additionally
# calls `record_damage(amount)` so TestDamageIsPostMitigation can assert the FINAL
# post-mitigation value the seam emitted. register(...) captures each callable ONCE at
# load; the hooks run only when a real event is emitted from the server seam.

def on_tick(tick):
    count("on_tick")

def on_player_join(name, entity_id):
    count("on_player_join")

def on_player_leave(name, entity_id):
    count("on_player_leave")

def on_block_break(x, y, z, state, player_id):
    count("on_block_break")

def on_block_place(x, y, z, state, player_id):
    count("on_block_place")

def on_entity_spawn(entity_id, type_id, x, y, z):
    count("on_entity_spawn")

def on_entity_death(entity_id, type_id):
    count("on_entity_death")

def on_damage(entity_id, amount):
    count("on_damage")
    record_damage(amount)

register("on_tick", on_tick)
register("on_player_join", on_player_join)
register("on_player_leave", on_player_leave)
register("on_block_break", on_block_break)
register("on_block_place", on_block_place)
register("on_entity_spawn", on_entity_spawn)
register("on_entity_death", on_entity_death)
register("on_damage", on_damage)
