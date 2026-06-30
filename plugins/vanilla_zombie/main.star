# vanilla_zombie — a 1:1 vanilla-Zombie dogfood (MOB-HOST-01), the FIRST hostile mob built as a
# Starlark plugin. The vanilla Zombie's HOSTILE AI re-expressed AS a Starlark plugin, staying a LITERAL
# method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares the goal set net.minecraft.world.entity.monster.zombie.Zombie.registerGoals builds (Zombie
# .registerGoals + addBehaviourGoals), reusing the proven Go goal runtime (the cow/pig PASSIVE goal
# callbacks are mob-agnostic — they read entity.* handles — so the SHARED passive callbacks are COPIED
# VERBATIM) AND the 35-01 Go-native COMBAT goals (the melee, the targetSelector) via the 35-01b kind= seam.
#
# THE 35-01b SEAM (why the combat goals carry NO .star body): the combat goals (MeleeAttackGoal /
# HurtByTargetGoal / NearestAttackableTargetGoal) are the SAME CLASS every hostile uses — there is
# nothing per-mob to re-express in Starlark, and re-expressing the combat RNG in .star would risk
# lockstep drift. So the zombie DECLARES its combat goals' priority + flags + kind, and the verbatim
# 35-01 Go-native goals do the work: the kind-routed nearestAttackableTargetGoal ACQUIRES the player
# (sets attackTargetID via the nextInt(10) gate + findTarget); the kind-routed meleeAttackGoal CHASES
# the target and, in melee reach, fires doHurtTarget through the Phase-29 keystone — REAL ATTACK_DAMAGE
# to the player. The PASSIVE goals (stroll/look/around) stay .star callbacks (the mob-agnostic shared port).
#
# Zombie.registerGoals() (javap-verified, 35-JARNOTES.md:18-35):
#   goalSelector:
#     @4 ZombieAttackTurtleEggGoal(this, 1.0, 3)            <-- DEFERRED (no turtle-egg subsystem)
#     @8 LookAtPlayerGoal(Player, 8.0)                      <-- .star (the shared passive look)
#     @8 RandomLookAroundGoal                               <-- .star (the shared passive around)
#     + addBehaviourGoals():
#       @2 SpearUseGoal(this, 1.0, 1.0, 10.0, 2.0)          <-- DEFERRED (26.2-NEW; no spear/weapon system)
#       @3 ZombieAttackGoal(this, 1.0, false)               <-- kind="melee_attack" (35-01 meleeAttackGoal; the CORE melee)
#       @6 MoveThroughVillageGoal(this, 1.0, true, 4, …)    <-- DEFERRED (no village subsystem)
#       @7 WaterAvoidingRandomStrollGoal(this, 1.0)         <-- .star (the shared passive stroll)
#   targetSelector:
#     @1 HurtByTargetGoal(this).setAlertOthers(ZombifiedPiglin.class)   <-- kind="hurt_by_target" (alertOthers cite-deferred)
#     @2 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true) <-- kind="nearest_attackable_target"
#     @3 NearestAttackableTargetGoal<AbstractVillager>(this, false)     <-- DEFERRED (no villager-as-target / AbstractVillager target)
#     @3 NearestAttackableTargetGoal<IronGolem>(this, true)             <-- DEFERRED (no IronGolem entity)
#     @5 NearestAttackableTargetGoal<Turtle>(this, …)                   <-- DEFERRED (no Turtle entity)
#
# DEFERRED (cite-recorded, NEVER silently dropped — see the SUMMARY):
#   - SpearUseGoal@2 (26.2-NEW): no spear/weapon system in v1 — the weapon-use goal lands with the
#     weapon subsystem. (35-JARNOTES.md:25.)
#   - ZombieAttackTurtleEggGoal@4 / MoveThroughVillageGoal@6: no turtle-egg / village subsystem in v1.
#   - The non-Player NearestAttackableTargetGoal variants (Villager/IronGolem/Turtle): those target
#     entities do not exist in v1 — the Player acquire (the phase goal "hunt the player") is the must-have.
#   - ZombieAttackGoal's setAggressive (the raise-arm client visual): a metadata DATA_ZOMBIE bit; the
#     Go-native meleeAttackGoal is behaviorally identical (the aggressive flag is a cited client visual,
#     35-JARNOTES.md:194-200). The melee DAMAGE (the must-have) is fully wired.
#   - HurtByTargetGoal.setAlertOthers(ZombifiedPiglin): no alert-burst / ZombifiedPiglin in v1 — the base
#     hurtByTargetGoal carries no alertSameType (cited no-op, 35-01 ai_goals_target.go).
#   - NearestAttackableTargetGoal's mustSee (line-of-sight): no LoS/sensing subsystem in v1 — the cited
#     "visible" stub (every player in FOLLOW_RANGE acquirable), 35-01 ai_goals_target.go findTarget.
#
# Each PASSIVE goal callback receives (entity, world, nav) handles; the interpreter fires ONLY while a
# passive goal is RUNNING. The COMBAT goals are Go-native (no callback). The draw ORDER below matches the
# bytecode EXACTLY (the per-entity seeded RandomSource — 24-01); the combat lockstep RNG (nextInt(10)
# acquire) is owned + pinned by the Go-native goal tests (ai_goals_target_test.go).

# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start's cos/sin.

# --- constants (jar-confirmed) -----------------------------------------------------------------
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
# (Zombie.createAttributes: FOLLOW_RANGE 35.0, MOVEMENT_SPEED 0.23, ATTACK_DAMAGE 3.0, ARMOR 2.0 —
# 35-JARNOTES.md:75). FOLLOW_RANGE bounds the nearest_attackable_target acquire; ATTACK_DAMAGE is the
# melee damage doHurtTarget deals. The combat goals are the Go-native 35-01 ports (kind=); the passive
# goals are .star callbacks. Cite Zombie.registerGoals/addBehaviourGoals + Zombie.createAttributes.
declare_mob(
    name = "vanilla_zombie",
    base_type = "zombie",
    attributes = {
        "follow_range": 35.0,    # Zombie.createAttributes: FOLLOW_RANGE 35.0 (the acquire scan bound)
        "movement_speed": 0.23,  # Zombie.createAttributes: MOVEMENT_SPEED 0.23
        "attack_damage": 3.0,    # Zombie.createAttributes: ATTACK_DAMAGE 3.0 (the melee damage)
        "armor": 2.0,            # Zombie.createAttributes: ARMOR 2.0
    },
    goals = [
        # @3 ZombieAttackGoal(mob, 1.0, false) [MOVE] — kind="melee_attack" (the 35-01 meleeAttackGoal):
        # chases the target + fires doHurtTarget through the Phase-29 keystone (REAL ATTACK_DAMAGE). The
        # setAggressive raise-arm flag is a cite-deferred client visual. Cite Zombie addBehaviourGoals @3.
        goal(priority = 3, flags = ["MOVE"], kind = "melee_attack"),
        # @7 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE] — .star (passive). can_use commits the
        # candidates (path_to), so NO start kwarg. Cite Zombie addBehaviourGoals @7.
        goal(
            priority = 7,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @8 LookAtPlayerGoal(Player, 8.0) [LOOK] — .star (passive). Cite Zombie.registerGoals @8 LookAtPlayerGoal(Player, 8.0).
        goal(
            priority = 8,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @8 RandomLookAroundGoal [MOVE, LOOK] — .star (passive), requiresUpdateEveryTick=true. Cite Zombie.registerGoals @8 RandomLookAroundGoal.
        goal(
            priority = 8,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] — kind="hurt_by_target" (the 35-01 hurtByTargetGoal):
        # retaliate against the last attacker (NO RNG). The setAlertOthers(ZombifiedPiglin) burst is cite-
        # deferred (no alert subsystem). Cite Zombie.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(mob) [TARGET] — kind="nearest_attackable_target"
        # (the 35-01 nearestAttackableTargetGoal: the nextInt(10) acquire gate + findTarget bounded by
        # FOLLOW_RANGE, sets attackTargetID). The mustSee LoS is the cited "visible" stub. Cite Zombie
        # .registerGoals targetSelector @2 NearestAttackableTargetGoal<Player>. (The Villager/IronGolem/
        # Turtle target variants @3/@5 are DEFERRED — those entities do not exist in v1; see header.)
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
