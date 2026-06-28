# worldmutate — the WORLD-BRIDGE testdata plugin (Plan 26-03). It exercises the
# highest-risk surface in the phase: a runtime="python" plugin running OFF-TICK that
# REQUESTS a world mutation, which the owner applies on the tick goroutine through the
# Phase-23 seam (ChunkManager.SetBlock + broadcastBlockUpdate). The plugin NEVER
# touches the world directly — set_block constructs a request that crosses the
# off-tick -> owner boundary as plain data (coords + state id), and the capability
# gate (world.write, declared above) is enforced on the owner at apply.
#
# The hook does HEAVY off-tick aggregation (the kind a hot tick must never run inline)
# then issues a SINGLE mutation request: on a block break at (x,y,z), it requests a
# block placed at (x, y+1, z). The tagged off-tick test (python_bridge_test.go, on the
# python3.14 Docker image) fires EventBlockBreak, drains the request on the owner, and
# asserts GetBlock reflects the new state AND a BlockUpdate was broadcast.
#
# On the DEFAULT (no-tag) build this file is never executed (the python runtime is not
# built in; the manifest is skipped) — the default-build test asserts only that the
# skip is graceful and that the Go-side bridge round-trip (a request injected directly
# onto the queue) applies through the seam.

# The block state id the hook requests. 1 is a stable non-air state across the run
# (the test passes a known-loaded column); the test asserts GetBlock returns exactly
# this id after the round-trip.
PLACE_STATE = 1

breaks = 0


def on_break(x, y, z, state, player_id):
    # HEAVY off-tick work would live here (aggregate, score, hit an external service).
    # This runs on a pool worker, so latency here does NOT stall the server tick.
    global breaks
    breaks += 1
    # A SINGLE mutation request: place a block one above where the break happened. This
    # does NOT mutate the world here — it enqueues a request the owner applies on the
    # tick goroutine through the Phase-23 seam (the request/apply indirection is the
    # safety boundary; no live handle crosses off-tick).
    set_block(x, y + 1, z, PLACE_STATE)


register("on_block_break", on_break)
