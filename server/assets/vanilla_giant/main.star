# vanilla_giant -- a 1:1 vanilla-Giant dogfood, the INERT giant zombie built as a Starlark plugin. A
# LITERAL port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p):
# net.minecraft.world.entity.monster.Giant extends Monster and registers NO goals -- Giant.registerGoals
# is NOT overridden and the class adds no goal method, so a Giant has an EMPTY goalSelector and
# targetSelector. It just STANDS (only getWalkTargetValue is overridden, an irrelevant nav weight). This
# declaration is therefore an EMPTY goals list -- the mob is inert by design.
#
# Giant.createAttributes (javap-verified): Monster.createMonsterAttributes + MAX_HEALTH 100.0 +
# MOVEMENT_SPEED 0.5 + ATTACK_DAMAGE 50.0 + CAMERA_DISTANCE 16.0. CAMERA_DISTANCE is a client-only camera
# attribute with no gameplay effect and has no seedAttributes alias (it is not among the gameplay
# attributes the attrAlias map exposes), so it is OMITTED here with this cite -- the three GAMEPLAY
# attributes (max_health/movement_speed/attack_damage) are seeded. Cite Giant.createAttributes +
# Giant (extends Monster, no registerGoals override).

# base_type "giant" -> renders as entity.Giant.ID (id 59; custom = BEHAVIOR, not a new wire type). NO goals
# (the Giant is inert). Cite Giant.registerGoals (none) + Giant.createAttributes.
declare_mob(
    name = "vanilla_giant",
    base_type = "giant",
    attributes = {
        "max_health": 100.0,    # Giant.createAttributes: MAX_HEALTH 100.0
        "movement_speed": 0.5,  # Giant.createAttributes: MOVEMENT_SPEED 0.5
        "attack_damage": 50.0,  # Giant.createAttributes: ATTACK_DAMAGE 50.0
        # CAMERA_DISTANCE 16.0 -- OMITTED (client camera attr, no gameplay effect, no seedAttributes alias).
    },
    goals = [],  # Giant.registerGoals is NOT overridden -> EMPTY goal set (the giant is INERT, it stands).
)
