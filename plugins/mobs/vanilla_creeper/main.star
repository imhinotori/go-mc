# vanilla_creeper — a 1:1 vanilla-Creeper dogfood (MOB-HOST-06), the Creeper built as a Starlark plugin.
# A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p).
# It declares net.minecraft.world.entity.monster.Creeper.registerGoals, reusing the proven Go goal runtime:
# the FloatGoal + the passive stroll/look/around are the SAME mob-agnostic .star callbacks the cow/zombie
# run (COPIED VERBATIM), the COMBAT goals (melee + targets) are the Go-native 35-01 ports (kind=), and the
# SwellGoal is the Go-native creeper_swell kind (ai_goals_creeper.go) that drives the fuse the Go-native
# Creeper.tick (creeperAiStep) advances to the explosion (explosion.go).
#
# Creeper.registerGoals() (javap-verified this session):
#   goalSelector:
#     @1 FloatGoal(this)                          <-- .star (the shared passive float)
#     @2 SwellGoal(this)                          <-- kind="creeper_swell" (arms the fuse; explode in Creeper.tick)
#     @3 AvoidEntityGoal<Ocelot>(this, 6, 1.0, 1.2) <-- DEFERRED (no "ocelot" base_type in v1)
#     @3 AvoidEntityGoal<Cat>(this, 6, 1.0, 1.2)  <-- kind="avoid_entity" avoid_type="cat" (flees cats)
#     @4 MeleeAttackGoal(this, 1.0, false)        <-- kind="melee_attack" (the creeper closes to blow up)
#     @5 WaterAvoidingRandomStrollGoal(this, 0.8) <-- .star (the shared passive stroll; speed 0.8 cited-deferred)
#     @6 LookAtPlayerGoal(this, Player, 8.0)      <-- .star (the shared passive look)
#     @6 RandomLookAroundGoal(this)               <-- .star (the shared passive around)
#   targetSelector:
#     @1 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true) <-- kind="nearest_attackable_target"
#     @2 HurtByTargetGoal(this)                   <-- kind="hurt_by_target"
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - AvoidEntityGoal<Ocelot>@3: no "ocelot" base_type registered in v1 (resolveBaseType). The
#     AvoidEntityGoal<Cat>@3 leg IS wired (kind="avoid_entity" avoid_type="cat", ai_goals_avoid.go) —
#     the creeper flees cats; the Ocelot leg lands when an ocelot base_type/plugin exists.
#   - The primed-fuse sound + the swell client metadata (DATA_SWELL_DIR/POWERED/IGNITED): cite-deferred
#     client visuals (the creeper still fuses + explodes with REAL entity damage — the gameplay).
#   - NearestAttackableTargetGoal's mustSee: the cited "visible" stub (no sensing subsystem).

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4
FLOAT_JUMP_PROBABILITY = 0.8
STROLL_REDUCED_INTERVAL = 60
STROLL_H = 10
STROLL_V = 7
STROLL_WATER_AVOID_PROBABILITY = 0.001
LOOK_DIST = 8.0            # LookAtPlayerGoal lookDistance (Creeper: 8.0f)
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
# @5  WaterAvoidingRandomStrollGoal(mob, 0.8)   flags {MOVE}   — COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(60) gate
        return False
    roll = entity.rand_float()   # DRAW 2: nextFloat() probability gate
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
# @6  LookAtPlayerGoal(mob, Player, 8.0)   flags {LOOK}   — COPIED VERBATIM from vanilla_zombie
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
# @6  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
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
# base_type "creeper" -> renders as entity.Creeper.ID. Real Creeper attributes
# (Monster.createMonsterAttributes + Creeper override MOVEMENT_SPEED 0.25); MAX_HEALTH is the Monster base
# 20.0 (no override). The SwellGoal + melee + targets are Go-native (kind=); float + stroll/look/around are
# .star callbacks. Cite Creeper.registerGoals + Creeper.createAttributes.
declare_mob(
    name = "vanilla_creeper",
    base_type = "creeper",
    attributes = {
        "movement_speed": 0.25,  # Creeper.createAttributes: MOVEMENT_SPEED 0.25
    },
    goals = [
        # @1 FloatGoal [JUMP] — requiresUpdateEveryTick=true. Cite Creeper.registerGoals @1 FloatGoal.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @2 SwellGoal [MOVE] — kind="creeper_swell" (arms/disarms the fuse; the explosion is in
        # creeperAiStep). Cite Creeper.registerGoals @2 SwellGoal.
        goal(priority = 2, flags = ["MOVE"], kind = "creeper_swell"),
        # @3 AvoidEntityGoal<Cat>(mob, 6.0f, 1.0, 1.2) [MOVE] — kind="avoid_entity" avoid_type="cat"
        # (the creeper flees nearby cats; ai_goals_avoid.go). Cite Creeper.registerGoals @3
        # AvoidEntityGoal<Cat>. (The Ocelot leg is deferred: no "ocelot" base_type in v1.)
        goal(priority = 3, flags = ["MOVE"], kind = "avoid_entity", avoid_type = "cat"),
        # @4 MeleeAttackGoal(mob, 1.0, false) [MOVE] — kind="melee_attack" (the creeper closes the gap so
        # the SwellGoal can arm within 3 blocks). Cite Creeper.registerGoals @4 MeleeAttackGoal.
        goal(priority = 4, flags = ["MOVE"], kind = "melee_attack"),
        # @5 WaterAvoidingRandomStrollGoal(mob, 0.8) [MOVE] — .star (passive). Cite Creeper.registerGoals @5.
        goal(
            priority = 5,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @6 LookAtPlayerGoal(Player, 8.0) [LOOK] — .star (passive). Cite Creeper.registerGoals @6 LookAtPlayerGoal.
        goal(
            priority = 6,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @6 RandomLookAroundGoal [MOVE, LOOK] — .star (passive), requiresUpdateEveryTick=true. Cite
        # Creeper.registerGoals @6 RandomLookAroundGoal.
        goal(
            priority = 6,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 NearestAttackableTargetGoal<Player>(mob) [TARGET] — kind="nearest_attackable_target".
        # Cite Creeper.registerGoals targetSelector @1 NearestAttackableTargetGoal<Player>.
        goal(priority = 1, flags = ["TARGET"], kind = "nearest_attackable_target"),
        # targetSelector @2 HurtByTargetGoal(mob) [TARGET] — kind="hurt_by_target". Cite Creeper.registerGoals
        # targetSelector @2 HurtByTargetGoal.
        goal(priority = 2, flags = ["TARGET"], kind = "hurt_by_target"),
    ],
)
