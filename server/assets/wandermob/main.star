# wandermob — the PLUGIN-03 GATE plugin: a trivial CUSTOM mob (base_type pig) with ONE MOVE goal
# that nav-targets a fixed nearby point so it WALKS via the real Go nav. "custom" = the BEHAVIOR;
# it renders as the existing pig wire id (base_type pig -> entity.Pig.ID). Free of the 1:1 mandate
# (a custom mob, not a vanilla one — v4-PLAN).
#
# declare_mob captures this declaration ONCE at load (the module body runs a single time). The goal
# tick callback is a frozen Starlark callable the server invokes ONLY while the goal is running
# (an idle declared mob makes zero starlark.Calls per tick).

# on_wander_tick fires while the MOVE goal runs. It receives (entity, world, nav) handles. If the
# mob has no active path it picks a FIXED nearby target (deterministic — Starlark has no rand in the
# sandbox) east of its current position and asks the Go nav to path there. The Go nav + moveEntity
# then walk the mob (the goal SETS a target; it never moves the mob).
def on_wander_tick(entity, world, nav):
    if not nav.has_path():
        # Deterministic target: 8 blocks east of the mob's current position along the floor. A fixed
        # offset (not random) keeps the gate reproducible; the Go A* finds the path over the floor.
        nav.path_to(entity.x + 8.0, entity.y, entity.z)

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
