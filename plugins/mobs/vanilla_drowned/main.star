# vanilla_drowned - a 1:1 vanilla-Drowned dogfood (MOB-VARIANT), the Drowned built as a Starlark plugin.
# net.minecraft.world.entity.monster.zombie.Drowned extends Zombie. Drowned OVERRIDES createAttributes
# (javap-verified: super Zombie.createAttributes() then add STEP_HEIGHT 1.0 -- no combat/health change),
# so its HOSTILE AI + attributes match a Zombie's (FOLLOW_RANGE 35, MOVEMENT_SPEED 0.23, ATTACK_DAMAGE 3,
# ARMOR 2). This plugin is the vanilla_zombie declaration with base_type "drowned" (renders as
# entity.Drowned.ID) PLUS the DrownedTridentAttackGoal (a Drowned holding a TRIDENT throws it). Cite
# net.minecraft.world.entity.monster.zombie.Drowned.
#
# Drowned.addBehaviourGoals (javap-verified):
#   @1 DrownedGoToWaterGoal(this, 1.0)                    <-- DEFERRED (no water/fluid nav subsystem in v1)
#   @2 DrownedTridentAttackGoal(this, 1.0, 40, 10.0)      <-- kind="drowned_trident_attack" (Go-native ranged)
#   @2 DrownedAttackGoal(this, 1.0, false)                <-- kind="melee_attack" (the base zombie melee)
#   @5 DrownedGoToBeachGoal(this, 1.0)                    <-- DEFERRED (no beach/water nav in v1)
#   @6 DrownedSwimUpGoal(this, 1.0, seaLevel)             <-- DEFERRED (no swim/fluid subsystem in v1)
#   @7 RandomStrollGoal(this, 1.0)                        <-- .star (the shared passive stroll)
#   + Zombie.registerGoals @8 LookAtPlayerGoal / RandomLookAroundGoal   <-- .star (the shared passive look/around)
#   targetSelector @1 HurtByTargetGoal / @2 NearestAttackableTarget<Player>  <-- kind= (35-01 Go-native)
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - The water-nav goals (DrownedGoToWaterGoal@1 / DrownedGoToBeachGoal@5 / DrownedSwimUpGoal@6): no
#     water/fluid navigation subsystem in v1 -- the submerge/beach/swim-up behaviors land with fluids. The
#     Drowned still burns in daylight (Zombie.isSunSensitive returns true, NO Drowned override), so it is
#     a normal ground zombie on land in v1. Cite Drowned.addBehaviourGoals @1/@5/@6.
#   - The trident THROW is Go-native (drownedAiStep, zombie_variants.go): a Drowned equipped with a TRIDENT
#     (populateDefaultEquipmentSlots roll) throws it at the target (DrownedTridentAttackGoal / Drowned
#     .performRangedAttack: ThrownTrident, velocity 1.6, inaccuracy 14 - difficulty*4). See kind= below.
# WaterAvoidingRandomStrollGoal(mob, 1.0) — Zombie addBehaviourGoals @7 (speed 1.0, the want-multiplier
# is cited-deferred; the gate/candidate draws match the cow/pig stroll).
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(DEFAULT_INTERVAL=120) == positiveCeilDiv(120,2) == 60
STROLL_H = 10                  # LandRandomPos/DefaultRandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7                   # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (2-arg ctor default)

LOOK_DIST = 8.0           # LookAtPlayerGoal lookDistance (Zombie: 8.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# @7  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}   — COPIED VERBATIM from vanilla_cow
# ports net.minecraft.world.entity.ai.goal.RandomStrollGoal (the SAME goal the cow/pig run)
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(reducedTickDelay(120)=60) gate
        return False
    roll = entity.rand_float()   # DRAW 2: nextFloat() probability gate (WaterAvoidingRandomStrollGoal.getPosition)
    land_mode = 1.0 if roll >= STROLL_WATER_AVOID_PROBABILITY else 0.0
    flat = []
    for _ in range(10):   # RandomPos.generateRandomPos: 10 unconditional candidates (NO break)
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(land_mode)   # 31st float: wantLandMode (1.0 Land / 0.0 Default)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()

# ============================================================================================
# @8  LookAtPlayerGoal(mob, Player.class, 8.0f)   flags {LOOK}   — COPIED from vanilla_cow (dist 8.0)
# ============================================================================================
def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:   # DRAW: nextFloat() < probability (>= => false)
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, LOOK_DIST)
    if p == None:
        return False
    entity.set_state("look_x", p[0])
    entity.set_state("look_y", p[1])
    entity.set_state("look_z", p[2])
    return True

def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))   # lookTime = adjustedTickDelay(40 + nextInt(40))

def look_continue(entity, world, nav):
    if entity.get_state("look_time") <= 0:
        return False
    dx = entity.get_state("look_x") - entity.x
    dy = entity.get_state("look_y") - entity.y
    dz = entity.get_state("look_z") - entity.z
    d2 = dx * dx + dy * dy + dz * dz
    return d2 <= LOOK_DIST * LOOK_DIST

def look_tick(entity, world, nav):
    entity.set_look_at(entity.get_state("look_x"), entity.get_state("look_y"), entity.get_state("look_z"))
    entity.set_state("look_time", entity.get_state("look_time") - 1)

def look_stop(entity, world, nav):
    entity.set_state("look_time", 0)

# ============================================================================================
# @8  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
# ============================================================================================
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()           # nextDouble() heading (DRAW 1)
    entity.set_state("rel_x", math.cos(d))      # relX = cos(d)
    entity.set_state("rel_z", math.sin(d))      # relZ = sin(d)
    entity.set_state("look_time", 20 + entity.rand_int(20))   # lookTime = 20 + nextInt(20) (DRAW 2)

def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "zombie" -> renders as entity.Zombie.ID (custom = BEHAVIOR, not a new wire type). Real
# Zombie attributes (Monster.createMonsterAttributes + Zombie overrides) seeded with the jar values

# --- the declaration ---------------------------------------------------------------------------
# base_type "drowned" -> renders as entity.Drowned.ID. Real Zombie attributes (Drowned.createAttributes
# adds only STEP_HEIGHT 1.0, no combat/health change): FOLLOW_RANGE 35.0, MOVEMENT_SPEED 0.23,
# ATTACK_DAMAGE 3.0, ARMOR 2.0. Cite Drowned.createAttributes -> Zombie.createAttributes.
declare_mob(
    name = "vanilla_drowned",
    base_type = "drowned",
    attributes = {
        "follow_range": 35.0,    # Zombie.createAttributes: FOLLOW_RANGE 35.0
        "movement_speed": 0.23,  # Zombie.createAttributes: MOVEMENT_SPEED 0.23
        "attack_damage": 3.0,    # Zombie.createAttributes: ATTACK_DAMAGE 3.0
        "armor": 2.0,            # Zombie.createAttributes: ARMOR 2.0
    },
    goals = [
        # @2 DrownedAttackGoal(mob, 1.0, false) [MOVE] -- kind="melee_attack" (the base zombie melee).
        goal(priority = 2, flags = ["MOVE"], kind = "melee_attack"),
        # @7 RandomStrollGoal(mob, 1.0) [MOVE] -- .star (passive). Cite Drowned.addBehaviourGoals @7.
        goal(
            priority = 7,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @8 LookAtPlayerGoal(Player, 8.0) [LOOK] -- .star (passive). Cite Zombie.registerGoals @8.
        goal(
            priority = 8,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @8 RandomLookAroundGoal [MOVE, LOOK] -- .star (passive). Cite Zombie.registerGoals @8.
        goal(
            priority = 8,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] -- kind="hurt_by_target". Cite Drowned targetSelector @1.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(mob) [TARGET] -- kind="nearest_attackable_target".
        # Cite Drowned targetSelector @2 NearestAttackableTargetGoal<Player>.
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
