# heavylogger — a runtime="python" plugin exercised by the off-tick lane tests
# (Plan 26-02). It uses the SAME register(event, fn) concept as a .star plugin
# (the host routes by manifest.Runtime, not by API) — only the execution lane
# differs: a python hook runs OFF-TICK on an ants-pool worker (GIL-held), never
# on the tick goroutine.
#
# The hook does HEAVY off-tick work (an aggregation a hot tick must never run
# inline): it counts breaks per block column and accumulates a running total. The
# tagged off-tick test (offtick_python_test.go, on the python3.14 Docker image)
# asserts this aggregation ran AND that it ran off the calling goroutine while the
# GIL was held. On the DEFAULT (no-tag) build this file is never executed (the
# python runtime is not built in; the manifest is skipped) — the default-build test
# asserts only that the skip is graceful and the starlark path is unaffected.

counts = {}
total = 0


def on_break(x, y, z, state, player_id):
    # HEAVY off-tick work would live here (aggregate, write a log, hit an external
    # service, run ML). This runs on a pool worker, so latency here does NOT stall
    # the server tick.
    global total
    key = (x, y, z)
    counts[key] = counts.get(key, 0) + 1
    total += 1


register("on_block_break", on_break)
