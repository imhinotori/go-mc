# vanilla_stray - a 1:1 vanilla-Stray dogfood (MOB-VARIANT), the Stray built as a Starlark plugin.
# net.minecraft.world.entity.monster.skeleton.Stray extends AbstractSkeleton and OVERRIDES neither
# registerGoals NOR createAttributes (javap-verified), so its HOSTILE AI + attributes are IDENTICAL to a
# Skeleton's (AbstractSkeleton.createAttributes: MOVEMENT_SPEED 0.25; MAX_HEALTH 20.0 Mob base,
# ATTACK_DAMAGE 2.0 Monster base). This plugin is the vanilla_skeleton declaration verbatim with
# base_type "stray" (renders as entity.Stray.ID). Cite net.minecraft.world.entity.monster.skeleton.Stray.
#
# THE SIGNATURE (Go-native, zombie_variants.go): Stray.getArrow adds a SLOWNESS 600 (30s) MobEffectInstance
# to the fired Arrow (amplifier 0). The ranged path is the shared kind="ranged_bow_attack"; the tipped
# effect is applied via the arrow's carried effects (strayArrowEffects), which the arrow-on-hit path
# addEntityEffect/addPlayerEffect applies to the victim. Cite Stray.getArrow -> arrow.addEffect(new
# MobEffectInstance(SLOWNESS, 600)).
# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start's cos/sin.

# --- constants (jar-confirmed) -----------------------------------------------------------------
# WaterAvoidingRandomStrollGoal(mob, 1.0) — AbstractSkeleton.registerGoals @5 (speed 1.0).
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(DEFAULT_INTERVAL=120) == positiveCeilDiv(120,2) == 60
STROLL_H = 10                  # LandRandomPos/DefaultRandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7                   # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (2-arg ctor default)

LOOK_DIST = 8.0           # LookAtPlayerGoal lookDistance (Skeleton: 8.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# @5  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}   — COPIED VERBATIM from vanilla_cow
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
# @6  LookAtPlayerGoal(mob, Player.class, 8.0f)   flags {LOOK}   — COPIED from vanilla_cow (dist 8.0)
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
# @6  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
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
declare_mob(
    name = "vanilla_stray",
    base_type = "stray",
    attributes = {
        "movement_speed": 0.25,  # AbstractSkeleton.createAttributes: MOVEMENT_SPEED 0.25
    },
    goals = [
        # @4 RangedBowAttackGoal(mob, 1.0, 20|40, 15.0) [MOVE, LOOK] -- kind="ranged_bow_attack": the
        # skeleton HOLDS A BOW (reassessWeaponGoal bow branch), charges 20 ticks and fires an Arrow. For
        # this variant the fired Arrow carries a tipped MobEffect (Go-native getArrow override,
        # zombie_variants.go). Cite AbstractSkeleton.reassessWeaponGoal @4 (bow branch) + <variant>.getArrow.
        goal(priority = 4, flags = ["MOVE", "LOOK"], kind = "ranged_bow_attack"),
        # @2 RestrictSunGoal(mob) [] -- kind="restrict_sun". Cite AbstractSkeleton.registerGoals @2.
        goal(priority = 2, flags = [], kind = "restrict_sun"),
        # @3 FleeSunGoal(mob, 1.0) [MOVE] -- kind="flee_sun". Cite AbstractSkeleton.registerGoals @3.
        goal(priority = 3, flags = ["MOVE"], kind = "flee_sun"),
        # @5 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE] -- .star (passive). Cite AbstractSkeleton.registerGoals @5.
        goal(
            priority = 5,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @6 LookAtPlayerGoal(Player, 8.0) [LOOK] -- .star (passive). Cite AbstractSkeleton.registerGoals @6.
        goal(
            priority = 6,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @6 RandomLookAroundGoal [MOVE, LOOK] -- .star (passive). Cite AbstractSkeleton.registerGoals @6.
        goal(
            priority = 6,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] -- kind="hurt_by_target". Cite AbstractSkeleton targetSelector @1.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(mob) [TARGET] -- kind="nearest_attackable_target".
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
