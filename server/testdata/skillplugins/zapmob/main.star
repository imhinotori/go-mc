# zapmob — the SKILLS-01 test plugin: a CUSTOM mob (base_type pig, renders as the pig wire id)
# whose behavior is DECLARED SKILL DATA the Go hot path interprets (zero starlark.Calls per tick —
# stronger than goals: no callable is even captured).
#
# Two skills:
#   1. ~onTimer:10 @PlayersInRadius{r=10} -> damage 5 + poison 100 ticks (the MythicMobs
#      "StaticallyChargedSheep" shape: `lightning @LivingInRadius{r=10} ~onTimer:100`).
#   2. ~onDamaged @NearestPlayer{r=16} -> damage 2 (the retaliation zap).
#
# The manifest grants skills.damage + skills.effects — a mechanic without its grant is a LOAD error
# (plugin_skill_decl.go, the load-time LOCKED-capability enforcement).

declare_mob(
    name = "zapper",
    base_type = "pig",
    attributes = {
        "max_health": 30.0,
        "movement_speed": 0.25,
    },
    skills = [
        skill(
            trigger = "timer",
            interval = 10,
            targeter = targeter("players_in_radius", r = 10.0),
            mechanics = [
                mechanic("damage", amount = 5.0),
                mechanic("effect", effect = "poison", duration = 100, amplifier = 0),
            ],
        ),
        skill(
            trigger = "damaged",
            targeter = targeter("nearest_player", r = 16.0),
            mechanics = [
                mechanic("damage", amount = 2.0),
            ],
        ),
    ],
)
