# vanilla_polar_bear -- a 1:1 vanilla-PolarBear dogfood, the NEUTRAL polar bear built as a Starlark plugin.
# A LITERAL port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p):
# net.minecraft.world.entity.animal.polarbear.PolarBear extends Animal. It is NEUTRAL -- it only attacks
# when provoked (hurt_by_target retaliation) or when protecting a cub; otherwise it strolls/looks like any
# passive. The bounded port declares the mob-agnostic PASSIVE goals (float/stroll/look/around) as .star
# callbacks + the Go-native COMBAT goals (melee_attack + hurt_by_target) as kind=. The float/stroll/look/
# around callbacks are COPIED VERBATIM from vanilla_evoker (the same RandomStrollGoal DefaultRandomPos +
# FloatGoal + LookAtPlayerGoal + RandomLookAroundGoal machinery, mob-agnostic).
#
# PolarBear.registerGoals() (javap-verified):
#   goalSelector:
#     @0 FloatGoal(this)                          <-- .star (the shared passive float)
#     @1 PolarBear.PolarBearMeleeAttackGoal(...)  <-- kind="melee_attack" (the maul; setStanding client visual deferred)
#     @1 PolarBear.PolarBearPanicGoal(this, 2.0)  <-- DEFERRED (PanicGoal needs the panic_causes damage-tag read)
#     @4 FollowParentGoal(this, 1.25)             <-- DEFERRED (no follow_parent plugin kind; lands with breeding)
#     @5 RandomStrollGoal(this, 1.0)              <-- .star (the shared passive stroll)
#     @6 LookAtPlayerGoal(this, Player, 6.0)      <-- .star (the shared passive look, dist 6.0)
#     @7 RandomLookAroundGoal(this)               <-- .star (the shared passive around)
#   targetSelector:
#     @1 PolarBear.PolarBearHurtByTargetGoal(...) <-- kind="hurt_by_target" (retaliate when provoked -- the neutral maul trigger)
#     @2 PolarBear.PolarBearAttackPlayersGoal()   <-- DEFERRED (the protect-cub attack-players scan needs a nearby-cub read)
#
# DEFERRED (cite-recorded, NEVER silently dropped): PolarBearPanicGoal@1 (panic_causes tag read); FollowParentGoal@4
# (no follow_parent plugin kind -- lands with breeding); PolarBearAttackPlayersGoal@2 (protect-cub aggro needs a
# nearby-cub read); the DATA_STANDING_ID stand-up-maul client visual (PolarBearMeleeAttackGoal.setStanding, no
# metadata seam). The NEUTRAL maul (hurt_by_target retaliation + melee 6.0) is fully wired. Cite
# net.minecraft.world.entity.animal.polarbear.PolarBear.registerGoals + PolarBear.createAttributes.

# math is a host-predeclared global (starlark math.Module) -- used by RandomLookAroundGoal.start cos/sin.

# --- constants (jar-confirmed) -----------------------------------------------------------------
LOOK_DIST = 6.0   # PolarBear LookAtPlayerGoal(Player, 6.0f) lookDistance
FLUID_JUMP_THRESHOLD = 0.4
FLOAT_JUMP_PROBABILITY = 0.8
STROLL_REDUCED_INTERVAL = 60      # reducedTickDelay(DEFAULT_INTERVAL=120) == positiveCeilDiv(120,2) == 60
STROLL_H = 10                     # (Default/Land)RandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7                      # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (ravager only)
LOOK_PROBABILITY = 0.02           # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02    # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793  # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true -- COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:
        nav.jump()

# ============================================================================================
# RandomStrollGoal(mob, speed)   flags {MOVE}   -- DefaultRandomPos.getPos (NO nextFloat probability
# draw; land_mode 0.0 == Default). Cite RandomStrollGoal.getPosition -> DefaultRandomPos.getPos(mob,10,7).
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW: nextInt(reducedTickDelay(120)=60) gate
        return False
    flat = []
    for _ in range(10):   # RandomPos.generateRandomPos: 10 unconditional candidates
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x offset (DRAW 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y offset (DRAW 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z offset (DRAW 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(0.0)   # 31st float: wantLandMode 0.0 (Default -- RandomStrollGoal, no up-snap probability draw)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()
# ============================================================================================
# LookAtPlayerGoal(mob, Player, dist)   flags {LOOK}   -- COPIED VERBATIM from vanilla_zombie. The look
# distance is per-mob (LOOK_DIST, set in each mob's constants block below).
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
# RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true -- COPIED VERBATIM
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
# base_type "polar_bear" -> renders as entity.PolarBear.ID (id 104; custom = BEHAVIOR, not a new wire type).
# Real PolarBear attributes (Animal.createAnimalAttributes + PolarBear overrides) seeded with the jar values
# (PolarBear.createAttributes: MAX_HEALTH 30.0, FOLLOW_RANGE 20.0, MOVEMENT_SPEED 0.25, ATTACK_DAMAGE 6.0).
# FOLLOW_RANGE bounds the hurt_by_target reacquire scan; ATTACK_DAMAGE is the maul doHurtTarget damage. The
# combat goals (melee/hurt_by) are the Go-native ports (kind=); the passive goals (float/stroll/look/around)
# are .star callbacks. NEUTRAL: no un-provoked nearest_attackable_target -- the bear acquires a target ONLY
# via hurt_by_target (retaliation). Cite PolarBear.registerGoals + PolarBear.createAttributes.
declare_mob(
    name = "vanilla_polar_bear",
    base_type = "polar_bear",
    attributes = {
        "max_health": 30.0,     # PolarBear.createAttributes: MAX_HEALTH 30.0
        "follow_range": 20.0,   # PolarBear.createAttributes: FOLLOW_RANGE 20.0
        "movement_speed": 0.25, # PolarBear.createAttributes: MOVEMENT_SPEED 0.25
        "attack_damage": 6.0,   # PolarBear.createAttributes: ATTACK_DAMAGE 6.0 (the maul doHurtTarget damage)
    },
    goals = [
        # @0 FloatGoal [JUMP] -- .star (passive). Cite PolarBear.registerGoals @0 FloatGoal.
        goal(priority = 0, flags = ["JUMP"], can_use = float_can_use, tick = float_tick, requires_update_every_tick = True),
        # @1 PolarBearMeleeAttackGoal [MOVE] -- kind="melee_attack" (the maul): chases + fires doHurtTarget
        # (REAL ATTACK_DAMAGE 6.0). The setStanding stand-up client visual is cite-deferred. Cite
        # PolarBear.registerGoals @1 PolarBearMeleeAttackGoal.
        goal(priority = 1, flags = ["MOVE"], kind = "melee_attack"),
        # @1 PolarBearPanicGoal(2.0) -- DEFERRED (panic_causes tag read; see header).
        # @4 FollowParentGoal(1.25) -- DEFERRED (no follow_parent plugin kind; see header).
        # @5 RandomStrollGoal(mob, 1.0) [MOVE] -- .star (passive). Cite PolarBear.registerGoals @5 RandomStrollGoal(1.0).
        goal(priority = 5, flags = ["MOVE"], can_use = stroll_can_use, stop = stroll_stop, can_continue = stroll_continue),
        # @6 LookAtPlayerGoal(Player, 6.0) [LOOK] -- .star (passive). Cite PolarBear.registerGoals @6.
        goal(priority = 6, flags = ["LOOK"], can_use = look_can_use, start = look_start, tick = look_tick, stop = look_stop, can_continue = look_continue),
        # @7 RandomLookAroundGoal [MOVE, LOOK] -- .star (passive), requiresUpdateEveryTick=true. Cite PolarBear.registerGoals @7.
        goal(priority = 7, flags = ["MOVE", "LOOK"], can_use = around_can_use, start = around_start, tick = around_tick, can_continue = around_continue, requires_update_every_tick = True),
        # targetSelector @1 PolarBearHurtByTargetGoal [TARGET] -- kind="hurt_by_target" (the NEUTRAL maul trigger):
        # retaliate against the last attacker (NO RNG). Cite PolarBear.registerGoals targetSelector @1.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 PolarBearAttackPlayersGoal -- DEFERRED (protect-cub aggro needs a nearby-cub read).
    ],
)
