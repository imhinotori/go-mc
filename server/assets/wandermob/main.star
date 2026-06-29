# wandermob — the PLUGIN-03 GATE plugin: a trivial CUSTOM mob (base_type pig) with ONE MOVE goal
# that nav-targets a fixed nearby point so it WALKS via the real Go nav. "custom" = the BEHAVIOR;
# it renders as the existing pig wire id (base_type pig -> entity.Pig.ID). Free of the 1:1 mandate
# (a custom mob, not a vanilla one — v4-PLAN).
#
# declare_mob captures this declaration ONCE at load (the module body runs a single time). The goal
# tick callback is a frozen Starlark callable the server invokes ONLY while the goal is running
# (an idle declared mob makes zero starlark.Calls per tick).

# on_wander_tick fires while the MOVE goal runs. It receives (entity, world, nav) handles. Every
# ~40 ticks (≈2s) it re-rolls a RANDOM nearby target (a horizontal offset from the mob's per-entity
# seeded RNG — entity.rand_int, the Mob.getRandom() analogue, deterministic per mob seed) and asks
# the Go nav to path there; in between it lets the Go nav + moveEntity walk the mob toward the
# current target. A countdown in the goal's per-mob scratch (get_state/set_state) drives the cadence
# — it does NOT gate on nav.has_path(), because mobAI.hasTarget stays set until arrival, which would
# freeze a has_path()-gated mob after its first target. The periodic re-roll makes the mob wander
# (random direction each interval), not walk a fixed bearing and stop.
def on_wander_tick(entity, world, nav):
    cd = entity.get_state("wander_cd")
    if cd <= 0.0:
        # rand_int(n) -> [0, n). Map to a signed offset in [-7, +7] on each horizontal axis so the
        # target is a random nearby point (≈ RandomStroll's getPos horizontal spread). Both axes are
        # drawn so the mob can head any direction, not a fixed bearing.
        dx = entity.rand_int(15) - 7
        dz = entity.rand_int(15) - 7
        nav.path_to(entity.x + dx, entity.y, entity.z + dz)
        entity.set_state("wander_cd", 40.0) # re-roll roughly every 40 ticks (~2s)
    else:
        entity.set_state("wander_cd", cd - 1.0)

# The capture. base_type pig -> the mob gets real pig attributes (Wave-1 SUB-ATTRIB) + renders as the
# pig wire id. The declared overrides tweak max_health / movement_speed on top of the pig base. ONE
# goal: priority 6 (the vanilla stroll slot), claims MOVE, ticks on_wander_tick.
declare_mob(
    name = "wanderer",
    base_type = "pig",
    attributes = {
        "max_health": 12.0,
        "movement_speed": 0.25,
    },
    goals = [
        goal(priority = 6, flags = ["MOVE"], tick = on_wander_tick),
    ],
)
