# vanilla_vindicator — a 1:1 vanilla-Vindicator dogfood (RAIDER Task), the axe illager built as a Starlark
# plugin. A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap
# -c -p). It declares net.minecraft.world.entity.monster.illager.Vindicator.registerGoals, reusing the
# proven Go goal runtime: FloatGoal + the passive stroll/look/around are the SAME mob-agnostic .star
# callbacks the zombie runs (COPIED VERBATIM), the melee is the Go-native melee_attack kind (doHurtTarget
# through the Phase-29 keystone), targets are hurt_by_target/nearest_attackable_target, and the patrol is
# the Go-native long_distance_patrol.
#
# Vindicator.registerGoals() (javap-verified, net.minecraft.world.entity.monster.illager.Vindicator):
#   super.registerGoals()  <-- PatrollingMonster @4 LongDistancePatrolGoal(this, 0.7, 0.595) (WIRED);
#     Raider @1 ObtainRaidLeaderBannerGoal / @3 PathfindToRaidGoal / @4 RaiderMoveThroughVillageGoal /
#            @5 RaiderCelebration  <-- DEFERRED (village/POI/banner subsystems)
#   goalSelector:
#     @0 FloatGoal(this)                                           <-- .star
#     @1 AvoidEntityGoal<Creaking>(this, 8.0, 1.0, 1.2)           <-- DEFERRED (no Creaking)
#     @2 VindicatorBreakDoorGoal(this)                            <-- DEFERRED (no door-break/BlockState)
#     @3 AbstractIllager.RaiderOpenDoorGoal(this, this)           <-- DEFERRED (no door subsystem)
#     @4 Raider.HoldGroundAttackGoal(this, 10.0)                  <-- DEFERRED (raid hold-ground)
#     @5 MeleeAttackGoal(this, 1.0, false)                        <-- kind="melee_attack" (the CORE melee, iron axe)
#     @8 RandomStrollGoal(this, 0.6)                              <-- .star
#     @9 LookAtPlayerGoal(this, Player, 3.0, 1.0)                 <-- .star (dist 3)
#     @10 LookAtPlayerGoal(this, Mob, 8.0)                        <-- .star (around)
#   targetSelector:
#     @1 HurtByTargetGoal(this, Raider).setAlertOthers()         <-- kind="hurt_by_target"
#     @2 NearestAttackableTargetGoal<Player>(this, true)         <-- kind="nearest_attackable_target"
#     @3 NearestAttackableTargetGoal<AbstractVillager/IronGolem> <-- DEFERRED (entities absent)
#     @4 VindicatorJohnnyAttackGoal(this)                        <-- DEFERRED (needs custom-name "Johnny" read)
#
# DEFERRED (cite-recorded, NEVER silently dropped): the Raider village/banner goals; AvoidEntity<Creaking>;
# the door-break/open goals (no door BlockState subsystem); Raider.HoldGroundAttackGoal; the villager/iron-
# golem targets (entities absent); the Johnny name-check goal (VindicatorJohnnyAttackGoal — needs the
# customName=="Johnny" read; a vanilla vindicator is not Johnny by default, so this is inert until custom
# names land); the IRON AXE item + applyRaidBuffs sharpness enchant (no item/enchant subsystem — reach uses
# DEFAULT_ATTACK_REACH). The CORE hunt + melee (the phase goal) is fully wired.

LOOK_DIST = 3.0   # Vindicator LookAtPlayerGoal(Player, 3.0f) lookDistance

# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start cos/sin.
# --- constants (jar-confirmed) -----------------------------------------------------------------
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
# FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true — COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:
        nav.jump()

# ============================================================================================
# RandomStrollGoal(mob, speed)   flags {MOVE}   — DefaultRandomPos.getPos (NO nextFloat probability
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
    flat.append(0.0)   # 31st float: wantLandMode 0.0 (Default — RandomStrollGoal, no up-snap probability draw)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()
# ============================================================================================
# LookAtPlayerGoal(mob, Player, dist)   flags {LOOK}   — COPIED VERBATIM from vanilla_zombie. The look
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
# RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
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
# base_type "vindicator" -> renders as entity.Vindicator.ID (id 141). Real Vindicator attributes
# (Monster.createMonsterAttributes + MOVEMENT_SPEED 0.35f, FOLLOW_RANGE 12, MAX_HEALTH 24, ATTACK_DAMAGE 5).
# Melee + targets + patrol are Go-native (kind=); float + stroll/look/around are .star callbacks. Cite
# Vindicator.registerGoals + Vindicator.createAttributes.
declare_mob(
    name = "vanilla_vindicator",
    base_type = "vindicator",
    attributes = {
        "movement_speed": 0.3499999940395355,  # MOVEMENT_SPEED 0.35f (float-widened)
        "follow_range": 12.0,                   # FOLLOW_RANGE 12.0
        "max_health": 24.0,                     # MAX_HEALTH 24.0
        "attack_damage": 5.0,                   # ATTACK_DAMAGE 5.0 (the melee damage)
    },
    goals = [
        # @0 FloatGoal [JUMP]. Cite Vindicator.registerGoals @0 FloatGoal.
        goal(priority = 0, flags = ["JUMP"], can_use = float_can_use, tick = float_tick, requires_update_every_tick = True),
        # @4 (inherited) LongDistancePatrolGoal [MOVE] — kind. Cite PatrollingMonster @4.
        goal(priority = 4, flags = ["MOVE"], kind = "long_distance_patrol"),
        # @5 MeleeAttackGoal(this, 1.0, false) [MOVE] — kind. Cite Vindicator.registerGoals @5.
        goal(priority = 5, flags = ["MOVE"], kind = "melee_attack"),
        # @8 RandomStrollGoal(this, 0.6) [MOVE] — .star. Cite Vindicator.registerGoals @8.
        goal(priority = 8, flags = ["MOVE"], can_use = stroll_can_use, stop = stroll_stop, can_continue = stroll_continue),
        # @9 LookAtPlayerGoal(Player, 3.0) [LOOK] — .star. Cite Vindicator.registerGoals @9.
        goal(priority = 9, flags = ["LOOK"], can_use = look_can_use, start = look_start, tick = look_tick, stop = look_stop, can_continue = look_continue),
        # @10 LookAtPlayerGoal(Mob, 8.0)/around [MOVE, LOOK] — .star. Cite Vindicator.registerGoals @10.
        goal(priority = 10, flags = ["MOVE", "LOOK"], can_use = around_can_use, start = around_start, tick = around_tick, can_continue = around_continue, requires_update_every_tick = True),
        # targetSelector @1 HurtByTargetGoal(Raider) [TARGET] — kind. Cite Vindicator.registerGoals targetSelector @1.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(true) [TARGET] — kind. Cite Vindicator.registerGoals targetSelector @2.
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
