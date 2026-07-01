# vanilla_endermite - a 1:1 vanilla-Endermite dogfood (MOB-PREY, Task #9), the Endermite built as a Starlark
# plugin. A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar,
# javap -c -p). It declares net.minecraft.world.entity.monster.Endermite.registerGoals, reusing the proven
# Go goal runtime: the FloatGoal is the SAME mob-agnostic .star callback the cow/silverfish run (COPIED
# VERBATIM), the COMBAT goals are the Go-native 35-01 kinds, and the powder-snow climb is a Go-native kind.
#
# Endermite.registerGoals() (javap-verified this session):
#   goalSelector:
#     @1 FloatGoal(this)                          <-- .star (shared passive float - swim-jump)
#     @1 ClimbOnTopOfPowderSnowGoal(this, level)  <-- kind="climb_on_powder_snow" (JUMP; climb, do not sink)
#     @2 MeleeAttackGoal(this, 1.0, false)        <-- kind="melee_attack" (the CORE melee, doHurtTarget)
#     @3 WaterAvoidingRandomStrollGoal(this, 1.0) <-- .star (shared stroll)
#     @7 LookAtPlayerGoal(this, Player, 8.0)      <-- .star (shared look, dist 8.0)
#     @8 RandomLookAroundGoal(this)               <-- .star (shared around, requiresUpdateEveryTick)
#   targetSelector:
#     @1 HurtByTargetGoal(this).setAlertOthers()  <-- kind="hurt_by_target" (alertOthers cite-deferred, no-op)
#     @2 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true) <-- kind="nearest_attackable_target"
#
# THE DESPAWN TIMER (Endermite.aiStep, now wired - endermiteAiStep, ai_goals_endermite.go): server-side,
# a NON-persistent endermite increments `life` each tick and discard()s at life >= 2400 (MAX_LIFE, ~2 min).
# The client-side PORTAL particle burst is a visual (not ported - no particle subsystem). Cite Endermite.aiStep.
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - HurtByTargetGoal.setAlertOthers(): no alert-burst in v1 (the base hurtByTargetGoal carries no
#     alertSameType, a cited no-op - the same treatment zombie/silverfish use).
#   - NearestAttackableTargetGoal's mustSee (line-of-sight): the cited "visible" stub (no sensing).
#   - ClimbOnTopOfPowderSnowGoal@1: wired; isInPowderSnow reads the feet-block proxy (no inside-block
#     subsystem yet, ai_goals_powder_snow.go); the endermite IS in POWDER_SNOW_WALKABLE_MOBS (jar tag).
#   - The enderman->endermite natural spawn (EnderMan spawns an endermite on teleport, 5% roll) is a
#     SEPARATE enderman mechanic (cite-deferred there); this plugin is JUST the endermite mob.

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold (the endermite eye is above 0.4 -> 0.4)
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f (the swim-jump chance)
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(120) == 60 (WaterAvoidingRandomStrollGoal default interval)
STROLL_H = 10                  # LandRandomPos/DefaultRandomPos.getPos horizontal radius
STROLL_V = 7                   # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (2-arg ctor default)
LOOK_DIST = 8.0                # LookAtPlayerGoal lookDistance (Endermite: 8.0f)
LOOK_PROBABILITY = 0.02        # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02 # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# @1  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true - COPIED VERBATIM from vanilla_silverfish
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:   # DRAW: nextFloat()<0.8 (the swim-jump chance)
        nav.jump()

# ============================================================================================
# @3  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE} - COPIED VERBATIM from vanilla_enderman
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(60) gate
        return False
    roll = entity.rand_float()   # DRAW 2: nextFloat() probability gate
    land_mode = 1.0 if roll >= STROLL_WATER_AVOID_PROBABILITY else 0.0
    flat = []
    for _ in range(10):   # RandomPos.generateRandomPos: 10 unconditional candidates
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(land_mode)   # 31st float: wantLandMode
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()

# ============================================================================================
# @7  LookAtPlayerGoal(mob, Player, 8.0)   flags {LOOK} - COPIED VERBATIM from vanilla_enderman
# ============================================================================================
def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:   # DRAW: nextFloat() < probability
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, LOOK_DIST)
    if p == None:
        return False
    entity.set_state("look_x", p[0])
    entity.set_state("look_y", p[1])
    entity.set_state("look_z", p[2])
    return True

def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))   # lookTime = 40 + nextInt(40)

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
# @8  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true - COPIED VERBATIM
# ============================================================================================
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()           # nextDouble() heading (DRAW 1)
    entity.set_state("rel_x", math.cos(d))
    entity.set_state("rel_z", math.sin(d))
    entity.set_state("look_time", 20 + entity.rand_int(20))   # lookTime = 20 + nextInt(20) (DRAW 2)

def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "endermite" -> renders as entity.Endermite.ID. Real Endermite attributes
# (Monster.createMonsterAttributes + Endermite overrides: MAX_HEALTH 8.0, MOVEMENT_SPEED 0.25,
# ATTACK_DAMAGE 2.0). The combat goals are Go-native (kind=); float + stroll/look/around are .star
# callbacks; the despawn timer is Go-native (endermiteAiStep). Cite Endermite.registerGoals + createAttributes.
declare_mob(
    name = "vanilla_endermite",
    base_type = "endermite",
    attributes = {
        "max_health": 8.0,       # Endermite.createAttributes: MAX_HEALTH 8.0
        "movement_speed": 0.25,  # Endermite.createAttributes: MOVEMENT_SPEED 0.25
        "attack_damage": 2.0,    # Endermite.createAttributes: ATTACK_DAMAGE 2.0 (the melee damage)
    },
    goals = [
        # @1 FloatGoal [JUMP] - requiresUpdateEveryTick=true. Cite Endermite.registerGoals @1 FloatGoal.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @1 ClimbOnTopOfPowderSnowGoal(mob, level) [JUMP] - kind="climb_on_powder_snow" (the endermite is
        # in POWDER_SNOW_WALKABLE_MOBS): climb ON TOP of powder snow instead of sinking; NO RNG. Cite
        # Endermite.registerGoals @1 ClimbOnTopOfPowderSnowGoal.
        goal(priority = 1, flags = ["JUMP"], kind = "climb_on_powder_snow"),
        # @2 MeleeAttackGoal(mob, 1.0, false) [MOVE] - kind="melee_attack" (chases + doHurtTarget through the
        # Phase-29 keystone, REAL ATTACK_DAMAGE). Cite Endermite.registerGoals @2 MeleeAttackGoal.
        goal(priority = 2, flags = ["MOVE"], kind = "melee_attack"),
        # @3 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE] - .star. Cite Endermite.registerGoals @3.
        goal(
            priority = 3,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @7 LookAtPlayerGoal(Player, 8.0) [LOOK] - .star. Cite Endermite.registerGoals @7 LookAtPlayerGoal.
        goal(
            priority = 7,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @8 RandomLookAroundGoal [MOVE, LOOK] - .star, requiresUpdateEveryTick=true. Cite Endermite.registerGoals @8.
        goal(
            priority = 8,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] - kind="hurt_by_target" (retaliate, NO RNG). The
        # setAlertOthers burst is cite-deferred. Cite Endermite.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(mob) [TARGET] - kind="nearest_attackable_target"
        # (the nextInt(10) acquire gate + findTarget bounded by FOLLOW_RANGE). mustSee is the cited "visible"
        # stub. Cite Endermite.registerGoals targetSelector @2 NearestAttackableTargetGoal<Player>.
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
