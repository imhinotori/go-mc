# gate_events — the EVENTS-FIRE dogfood (PLUGIN-07 / Plan 28-02, checklist item #4).
#
# It subscribes to two discrete gameplay events and reacts via the chat() host builtin so the
# reaction lands on EVERY player's wire as a ClientboundSystemChat. The gate bot connects, breaks
# a block (and joins), and asserts it observes a SystemChat carrying the "gate_events:" marker —
# the OBSERVABLE proof that a plugin event hook fired and crossed onto the wire (the event-bus +
# the chat sink, end to end).
#
# The marker prefix "gate_events:" is the bot's assertion key (it scans decoded SystemChat text
# for that substring, never a blanket "got some chat" — the false-pass guard, threat T-28-02).
#
# Event payloads are PLAIN FROZEN SCALARS (no live entity/world handles — that is the Phase-23
# capability API; this plugin declares NO capabilities). The positional arg order is the host's
# Event.toStarlark order (plugin/host/event.go):
#   on_block_break(x, y, z, state, player_id)   <- BlockBreakEvent
#   on_player_join(name, entity_id)             <- PlayerJoinEvent

MARKER = "gate_events:"   # the bot's assertion substring (do NOT change without the bot)

# on_break fires once per block actually removed. It echoes the broken cell + the breaker's entity
# id onto the wire via chat() — the on_block_break -> chat() -> broadcastSystemChat path the bot
# proves. The reaction is a benign event marker only (T-28-06 accept: chat() broadcasts to all).
def on_break(x, y, z, state, player_id):
    chat("%s block broken at (%d,%d,%d) state=%d by entity %d" % (MARKER, x, y, z, state, player_id))

# on_join fires once per player join. The bot's connect triggers it, so the join-chat is
# corroborating evidence the event bus + sink work even before the bot breaks a block.
def on_join(name, entity_id):
    chat("%s player %s joined as entity %d" % (MARKER, name, entity_id))

# register-once at module load (the body runs ONCE; the register builtin validates the event name
# against the closed known-events set and captures the hook). No per-tick subscription cost — these
# are DISCRETE events, fired only when a block breaks / a player joins.
register("on_block_break", on_break)
register("on_player_join", on_join)
