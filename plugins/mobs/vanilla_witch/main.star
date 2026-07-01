# vanilla_witch — a 1:1 vanilla-Witch dogfood (MOB-HOST-07), the Witch built as a Starlark plugin. A
# LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares net.minecraft.world.entity.monster.Witch.registerGoals, reusing the proven Go goal runtime: the
# FloatGoal + the passive stroll/look/around are the SAME mob-agnostic .star callbacks the cow/zombie run
# (COPIED VERBATIM), and the RangedAttackGoal is the Go-native witch_ranged_attack kind (ai_goals_witch.go)
# that throws SPLASH POTIONS (mob_effect.go applies the effects on impact).
#
# Witch.registerGoals() (javap-verified this session):
#   super.registerGoals()                             <-- Raider/PatrollingMonster base patrol goals: DEFERRED (no raid subsystem)
#   goalSelector:
#     @1 FloatGoal(this)                              <-- .star (the shared passive float)
#     @2 RangedAttackGoal(this, 1.0, 60, 10.0)        <-- kind="witch_ranged_attack" (throws splash potions)
#     @2 WaterAvoidingRandomStrollGoal(this, 1.0)     <-- .star (the shared passive stroll)
#     @3 LookAtPlayerGoal(this, Player, 8.0)          <-- .star (the shared passive look)
#     @3 RandomLookAroundGoal(this)                   <-- .star (the shared passive around)
#   targetSelector:
#     @1 HurtByTargetGoal(this, Raider.class)         <-- kind="hurt_by_target" (Raider-alert exclusion cite-deferred)
#     @2 NearestHealableRaiderTargetGoal              <-- DEFERRED (no raid subsystem / no raider heal)
#     @3 NearestAttackableWitchTargetGoal<Player>     <-- kind="nearest_attackable_target" (the witch hunts the player)
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - super.registerGoals (Raider/PatrollingMonster patrol) + NearestHealableRaiderTargetGoal: no raid
#     subsystem in v1. The CORE hunt + splash-potion attack (the phase goal) is fully wired.
#   - Witch.performRangedAttack's Raider heal/regeneration branch (HEALING/REGENERATION on a hurt raider):
#     no raiders in v1, so the witch only ever throws the HARMING/SLOWNESS/POISON/WEAKNESS harmful ladder
#     at the player (ai_goals_witch.go). The drinking-potion self-buff + WITCH_THROW sound are cite-deferred.
#   - NearestAttackableWitchTargetGoal's mustSee: the cited "visible" stub (no sensing subsystem).

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4
FLOAT_JUMP_PROBABILITY = 0.8
STROLL_REDUCED_INTERVAL = 60
STROLL_H = 10
STROLL_V = 7
STROLL_WATER_AVOID_PROBABILITY = 0.001
LOOK_DIST = 8.0            # LookAtPlayerGoal lookDistance (Witch: 8.0f)
LOOK_PROBABILITY = 0.02
LOOK_AROUND_PROBABILITY = 0.02
TWO_PI = 2.0 * 3.141592653589793

# ============================================================================================
# @1  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true — COPIED VERBATIM from vanilla_cow
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:
        nav.jump()

# ============================================================================================
# @2  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}   — COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:
        return False
    roll = entity.rand_float()
    land_mode = 1.0 if roll >= STROLL_WATER_AVOID_PROBABILITY else 0.0
    flat = []
    for _ in range(10):
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(land_mode)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()

# ============================================================================================
# @3  LookAtPlayerGoal(mob, Player, 8.0)   flags {LOOK}   — COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, LOOK_DIST)
    if p == None:
        return False
    entity.set_state("look_x", p[0])
    entity.set_state("look_y", p[1])
    entity.set_state("look_z", p[2])
    return True

def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))

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
# @3  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
# ============================================================================================
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()
    entity.set_state("rel_x", math.cos(d))
    entity.set_state("rel_z", math.sin(d))
    entity.set_state("look_time", 20 + entity.rand_int(20))

def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "witch" -> renders as entity.Witch.ID. Real Witch attributes (Monster.createMonsterAttributes +
# Witch overrides MAX_HEALTH 26.0, MOVEMENT_SPEED 0.25). The RangedAttackGoal + targets are Go-native
# (kind=); float + stroll/look/around are .star callbacks. Cite Witch.registerGoals + Witch.createAttributes.
declare_mob(
    name = "vanilla_witch",
    base_type = "witch",
    attributes = {
        "max_health": 26.0,      # Witch.createAttributes: MAX_HEALTH 26.0
        "movement_speed": 0.25,  # Witch.createAttributes: MOVEMENT_SPEED 0.25
    },
    goals = [
        # @1 FloatGoal [JUMP] — requiresUpdateEveryTick=true. Cite Witch.registerGoals @1 FloatGoal.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @2 RangedAttackGoal(mob, 1.0, 60, 10.0) [MOVE, LOOK] — kind="witch_ranged_attack" (throws splash
        # potions at the target). Cite Witch.registerGoals @2 RangedAttackGoal.
        goal(priority = 2, flags = ["MOVE", "LOOK"], kind = "witch_ranged_attack"),
        # @2 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE] — .star (passive). Cite Witch.registerGoals @2.
        goal(
            priority = 2,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @3 LookAtPlayerGoal(Player, 8.0) [LOOK] — .star (passive). Cite Witch.registerGoals @3 LookAtPlayerGoal.
        goal(
            priority = 3,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @3 RandomLookAroundGoal [MOVE, LOOK] — .star (passive), requiresUpdateEveryTick=true. Cite
        # Witch.registerGoals @3 RandomLookAroundGoal.
        goal(
            priority = 3,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] — kind="hurt_by_target". Cite Witch.registerGoals
        # targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @3 NearestAttackableWitchTargetGoal<Player>(mob) [TARGET] — kind="nearest_attackable_target"
        # (the witch hunts the player). Cite Witch.registerGoals targetSelector @3.
        goal(priority = 3, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
