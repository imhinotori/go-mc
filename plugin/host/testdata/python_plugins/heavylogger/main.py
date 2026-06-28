# heavylogger — a runtime="python" plugin used by the host routing test. On the
# default (no-tag) build it is SKIPPED (python runtime not built in); WITH the tag
# (or a fake PythonRuntime in tests) it routes to the python lane. The body uses
# the SAME register(event, fn) concept as a .star plugin (Wave 2 wires the real
# off-tick dispatch); the host routing test only needs the manifest to select the
# python lane, so the body is never executed by the default-build test.
counts = {}


def on_break(x, y, z, state, player_id):
    # HEAVY off-tick work would go here (aggregate, log, external IO, ML).
    counts[(x, y, z)] = counts.get((x, y, z), 0) + 1


register("on_block_break", on_break)
