# vanilla_skeleton — a 1:1 vanilla-Skeleton dogfood (MOB-HOST-02), the SECOND hostile mob built as a
# Starlark plugin. The vanilla Skeleton's HOSTILE AI re-expressed AS a Starlark plugin, staying a
# LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p).
# It declares the goal set net.minecraft.world.entity.monster.skeleton.AbstractSkeleton.registerGoals
# builds, reusing the proven Go goal runtime (the cow/pig PASSIVE goal callbacks are mob-agnostic — they
# read entity.* handles — so the SHARED passive callbacks are COPIED VERBATIM) AND the 35-01 Go-native
# COMBAT goals (the melee, the targetSelector) via the 35-01b kind= seam.
#
# THE 35-01b SEAM (why the combat goals carry NO .star body): the combat goals are the SAME CLASS every
# hostile uses — nothing per-mob to re-express, and re-expressing the combat RNG in .star would risk
# lockstep drift. So the skeleton DECLARES its combat goals' priority + flags + kind, and the verbatim
# 35-01 Go-native goals do the work: nearestAttackableTargetGoal ACQUIRES the player (nextInt(10) gate
# + findTarget sets attackTargetID); meleeAttackGoal CHASES + fires doHurtTarget through the Phase-29
# keystone — REAL ATTACK_DAMAGE to the player. The PASSIVE goals (stroll/look/around) stay .star.
#
# AbstractSkeleton.registerGoals() (javap-verified, 35-JARNOTES.md:37-51):
#   goalSelector:
#     @2 RestrictSunGoal(this)                              <-- kind="restrict_sun" (avoid-sun path bias; malus deferred)
#     @3 FleeSunGoal(this, 1.0)                             <-- kind="flee_sun" (burning day-time run-for-shade)
#     @3 AvoidEntityGoal<Wolf>(this, Wolf, 6.0, 1.0, 1.2)   <-- DEFERRED (no Wolf / no AvoidEntityGoal in v1)
#     @5 WaterAvoidingRandomStrollGoal(this, 1.0)           <-- .star (the shared passive stroll)
#     @6 LookAtPlayerGoal(Player, 8.0)                      <-- .star (the shared passive look)
#     @6 RandomLookAroundGoal                               <-- .star (the shared passive around)
#   reassessWeaponGoal (held-item swap): bow -> RangedBowAttackGoal@4 (ACTIVE — the skeleton holds a bow)
#     @4 RangedBowAttackGoal(this, 1.0, 20|40, 15.0)        <-- kind="ranged_bow_attack" (PROJECTILE-01; fires Arrows)
#   targetSelector:
#     @1 HurtByTargetGoal(this)                             <-- kind="hurt_by_target" (35-01 hurtByTargetGoal)
#     @2 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true)  <-- kind="nearest_attackable_target"
#     @3 NearestAttackableTargetGoal<IronGolem>(this, true) <-- DEFERRED (no IronGolem entity)
#     @3 NearestAttackableTargetGoal<Turtle>(this, …)       <-- DEFERRED (no Turtle entity)
#
# DEFERRED (cite-recorded, NEVER silently dropped — see the SUMMARY):
#   - THE BOW (RangedBowAttackGoal + performRangedAttack + the Arrow projectile entity): the v1 skeleton
#     is MELEE-ONLY. AbstractSkeleton.reassessWeaponGoal swaps bowGoal@4 in when the held item is a bow,
#     else meleeGoal@4 — v1 has no bow item / no Arrow projectile entity, so the skeleton ALWAYS uses the
#     melee goal (the reassessWeaponGoal `else` branch). The ranged path lands with the projectile
#     subsystem. (35-JARNOTES.md:52-55.) This is the documented v1 deviation: melee, not ranged.
#   - AvoidEntityGoal<Wolf>@3: no Wolf entity / no AvoidEntityGoal port in v1.
#
# LANDED (this batch — daytime burn-avoidance, ai_goals_skeleton_sun.go):
#   - RestrictSunGoal@2 (kind="restrict_sun", {} no flags): while bright out, flips the navigation
#     avoid-sun bias (setAvoidSun). The avoid-sun pathfinding MALUS is node-evaluator DEFERRED + cited
#     (no path-malus subsystem); the flag write + the goal's ctor/start/stop are faithful, and holding
#     no MOVE flag it never fights the flee. Cite AbstractSkeleton.registerGoals @2 RestrictSunGoal.
#   - FleeSunGoal@3 (kind="flee_sun", {MOVE}): the UNMODIFIED base FleeSunGoal (KEEPS the isOnFire()
#     guard the Fox.SeekShelterGoal override drops). A burning (remainingFireTicks>0), sky-exposed,
#     day-time, target-less, bare-headed skeleton runs to a getHidePos shade tile (10 candidates,
#     nextInt(20)-10 / nextInt(6)-3 / nextInt(20)-10). isBrightOutside == !isDarkEnoughToSpawn (the day/
#     night proxy); canSeeSky == the superflat sky stub; head-empty + getWalkTargetValue>=0 are cited
#     constant-true. Cite AbstractSkeleton.registerGoals @3 FleeSunGoal(this, 1.0).
#   - The IronGolem/Turtle NearestAttackableTargetGoal variants: those entities do not exist in v1 — the
#     Player acquire (the phase goal "hunt the player") is the must-have.
#   - NearestAttackableTargetGoal's mustSee (line-of-sight): no LoS/sensing subsystem — the cited
#     "visible" stub (every player in FOLLOW_RANGE acquirable), 35-01 ai_goals_target.go findTarget.
#
# Each PASSIVE goal callback receives (entity, world, nav) handles; the interpreter fires ONLY while a
# passive goal is RUNNING. The COMBAT goals are Go-native (no callback). The draw ORDER below matches the
# bytecode EXACTLY (the per-entity seeded RandomSource — 24-01); the combat lockstep RNG is owned + pinned
# by the Go-native goal tests (ai_goals_target_test.go).

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
# base_type "skeleton" -> renders as entity.Skeleton.ID (custom = BEHAVIOR, not a new wire type). Real
# Skeleton attributes (Monster.createMonsterAttributes + AbstractSkeleton override) seeded with the jar
# values (AbstractSkeleton.createAttributes: Monster + MOVEMENT_SPEED 0.25; MAX_HEALTH 20.0 from the Mob
# base, ATTACK_DAMAGE 2.0 from the Monster base — 35-JARNOTES.md:76). The melee/target goals are the
# Go-native 35-01 ports (kind=); the passive goals are .star. Cite AbstractSkeleton.registerGoals +
# reassessWeaponGoal (the melee `else` branch) + AbstractSkeleton.createAttributes.
declare_mob(
    name = "vanilla_skeleton",
    base_type = "skeleton",
    attributes = {
        "movement_speed": 0.25,  # AbstractSkeleton.createAttributes: MOVEMENT_SPEED 0.25
        # MAX_HEALTH (20.0, Mob base) + ATTACK_DAMAGE (2.0, Monster.createMonsterAttributes base) are the
        # supplier defaults — NO override needed (seedAttributes leaves them at the registered base).
    },
    goals = [
        # @4 RangedBowAttackGoal(mob, 1.0, 20|40, 15.0) [MOVE, LOOK] — kind="ranged_bow_attack"
        # (the PROJECTILE-01 rangedBowAttackGoal): the skeleton HOLDS A BOW, so reassessWeaponGoal's bow
        # branch installs THIS goal (not the melee `else`). It closes to bow range, charges 20 ticks, and
        # fires an Arrow at the target (setBaseDamageFromMob(1.0) ≈ 2 + noise, velocity 1.6, inaccuracy 6 on
        # NORMAL). Replaces the v1 MELEE-ONLY stub. Cite AbstractSkeleton.reassessWeaponGoal @4 (bow branch).
        goal(priority = 4, flags = ["MOVE", "LOOK"], kind = "ranged_bow_attack"),
        # @2 RestrictSunGoal(mob) [] (NO flags) — kind="restrict_sun": while bright out, biases the
        # navigation away from sun (setAvoidSun; the pathfinding malus is node-evaluator deferred). Holds
        # no MOVE flag so it never contends with flee/stroll. Cite AbstractSkeleton.registerGoals @2 RestrictSunGoal.
        goal(priority = 2, flags = [], kind = "restrict_sun"),
        # @3 FleeSunGoal(mob, 1.0) [MOVE] — kind="flee_sun": a burning, sky-exposed, day-time skeleton
        # runs to a getHidePos shade tile (the UNMODIFIED base goal — keeps the isOnFire() guard). Cite
        # AbstractSkeleton.registerGoals @3 FleeSunGoal(this, 1.0).
        goal(priority = 3, flags = ["MOVE"], kind = "flee_sun"),
        # @5 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE] — .star (passive). can_use commits the
        # candidates (path_to), so NO start kwarg. Cite AbstractSkeleton.registerGoals @5.
        goal(
            priority = 5,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @6 LookAtPlayerGoal(Player, 8.0) [LOOK] — .star (passive). Cite AbstractSkeleton.registerGoals @6 LookAtPlayerGoal(Player, 8.0).
        goal(
            priority = 6,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @6 RandomLookAroundGoal [MOVE, LOOK] — .star (passive), requiresUpdateEveryTick=true. Cite AbstractSkeleton.registerGoals @6 RandomLookAroundGoal.
        goal(
            priority = 6,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] — kind="hurt_by_target" (the 35-01 hurtByTargetGoal):
        # retaliate against the last attacker (NO RNG). Cite AbstractSkeleton.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(mob) [TARGET] — kind="nearest_attackable_target"
        # (the 35-01 nearestAttackableTargetGoal: the nextInt(10) acquire gate + findTarget bounded by
        # FOLLOW_RANGE, sets attackTargetID). The mustSee LoS is the cited "visible" stub. Cite AbstractSkeleton
        # .registerGoals targetSelector @2 NearestAttackableTargetGoal<Player>. (The IronGolem/Turtle target
        # variants @3 are DEFERRED — those entities do not exist in v1; see header.)
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
