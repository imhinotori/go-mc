# vanilla_spider — a 1:1 vanilla-Spider dogfood (MOB-HOST-03), the THIRD hostile mob built as a
# Starlark plugin (after zombie/skeleton). The vanilla Spider's HOSTILE AI re-expressed AS a Starlark
# plugin, staying a LITERAL method-for-method port of the unobfuscated 26.2 jar
# (temp/cache/26.2-inner.jar, javap -c -p). It declares the goal set
# net.minecraft.world.entity.monster.spider.Spider.registerGoals builds, reusing the proven Go goal
# runtime (the cow/pig PASSIVE goal callbacks are mob-agnostic — they read entity.* handles — so the
# SHARED passive callbacks are COPIED VERBATIM) AND the 35-01 Go-native COMBAT goals (the leap, the
# daylight-gated melee, the float, the targetSelector) via the 35-01b kind= seam.
#
# THE 35-01b REWIRE (why the hand-rolled combat .star bodies are GONE): 35-05 shipped a spider whose
# .star LEAPS + faces but whose melee DAMAGE was unwired (the comment admitted "the host melee-hit is a
# HOST-side op the Go-native meleeAttackGoal performs" — but that goal was NEVER instantiated for a
# declared mob: there was no seam). 35-01b added the kind= seam, so the spider's combat goals are now
# the verbatim 35-01 Go-native ports (the SAME class every hostile uses), routed by kind=. This both
# fixes the unwired-melee gap (the kind-routed meleeAttackGoal fires doHurtTarget through the Phase-29
# keystone — REAL player damage) AND removes the lockstep-drift risk of re-expressing the combat RNG in
# Starlark. The combat goals carry NO .star body now; only the PASSIVE goals (stroll/look/around) do.
#
# Spider.registerGoals() (javap-verified, 35-JARNOTES.md:57-71):
#   goalSelector:
#     @1 FloatGoal(this)                                    <-- kind="float" (35-01 floatGoal)
#     @2 AvoidEntityGoal<Armadillo>(this, Armadillo, 6.0, 1.0, 1.2, e -> !e.isScared())   <-- DEFERRED
#     @3 LeapAtTargetGoal(this, 0.4)                         <-- kind="leap_at_target" (35-01 leapAtTargetGoal, yd=0.4)
#     @4 SpiderAttackGoal(this)                              <-- kind="spider_attack" (35-01 meleeAttackGoal + daylight-flee)
#     @5 WaterAvoidingRandomStrollGoal(this, 0.8)            <-- .star (the shared passive stroll)
#     @6 LookAtPlayerGoal(Player, 8.0)                       <-- .star (the shared passive look)
#     @6 RandomLookAroundGoal                                <-- .star (the shared passive around)
#   targetSelector:
#     @1 HurtByTargetGoal(this)                              <-- kind="hurt_by_target" (35-01 hurtByTargetGoal)
#     @2 SpiderTargetGoal<Player>(this, Player)              <-- kind="nearest_attackable_target" (NearestAttackableTargetGoal subclass)
#     @3 SpiderTargetGoal<IronGolem>(this, IronGolem)        <-- DEFERRED (no IronGolem entity yet)
#
# DEFERRED (cite-recorded, NEVER silently dropped — see the SUMMARY):
#   - AvoidEntityGoal<Armadillo>@2: no Armadillo entity / no AvoidEntityGoal port in v1. The flee-from-
#     armadillo goal lands when the Armadillo + AvoidEntityGoal arrive. (35-JARNOTES.md:61, 128.)
#   - SpiderTargetGoal<IronGolem>@3: no IronGolem entity in v1 — the golem target variant defers with it.
#   - WALL-CLIMB (the spider's defining movement — Spider.tick sets the CLIMBING data flag from
#     horizontalCollision; the climb is a navigation/physics behavior, NOT a goal): no climb subsystem
#     exists in v1 (the nav is ground-only). Cite-deferred to the wall-climb physics plan; the spider
#     hunts/leaps/melees on the ground meanwhile. (Spider.tick / Spider.onClimbable.)
#   - getLightLevelDependentMagicValue (the SpiderAttackGoal/SpiderTargetGoal light read): no light
#     engine in v1 -> the gametime day/night proxy (35-02 isDarkEnoughToSpawn), the SAME FORCED decision
#     the spawn gate made. The daylight-flee RNG draw is STILL made when "bright" per the proxy (the
#     Go-native spiderAttackGoal owns it now — meleeAttackGoal.canContinueToUse + isBright).
#   - SpiderTargetGoal's daylight ACQUIRE narrowing: the v1 kind="nearest_attackable_target" routes to
#     the base NearestAttackableTargetGoal (the nextInt(10) gate + findTarget). The SpiderTargetGoal
#     subclass's light-gated acquire narrowing collapses to the base acquire (cite-deferred with the
#     light engine); the daylight-FLEE (the observable "spiders calm in daylight") IS modeled (the
#     spider_attack kind's canContinueToUse 1/100 drop). Cite Spider$SpiderTargetGoal.
#
# Each PASSIVE goal callback receives (entity, world, nav) handles; the interpreter fires ONLY while a
# passive goal is RUNNING. The COMBAT goals are Go-native (no callback). The draw ORDER below matches the
# bytecode EXACTLY (the per-entity seeded RandomSource — 24-01); the leap/daylight/acquire lockstep RNG
# is owned + pinned by the Go-native goal tests (ai_goals_target_test.go + spider_test.go).

# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start's
# cos/sin. No load() needed (the host injects it, plugin/starlark/runtime.go).

# --- constants (jar-confirmed) -----------------------------------------------------------------
# WaterAvoidingRandomStrollGoal(mob, 0.8) — Spider.registerGoals @5 (the spider's stroll speed is 0.8,
# NOT the cow's 1.0; the speed is the want-multiplier, cited-deferred — the gate/candidate draws match).
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(DEFAULT_INTERVAL=120) == positiveCeilDiv(120,2) == 60
STROLL_H = 10                  # LandRandomPos/DefaultRandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7                   # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (2-arg ctor default)

LOOK_DIST = 8.0           # LookAtPlayerGoal lookDistance (Spider: 8.0f — NOT the cow's 6.0)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# @5  WaterAvoidingRandomStrollGoal(mob, 0.8)   flags {MOVE}   — COPIED VERBATIM from vanilla_cow
# (only the speed differs: 0.8 vs the cow's 1.0 — a want-multiplier, cited-deferred; the draws match)
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
# base_type "spider" -> renders as entity.Spider.ID (custom = BEHAVIOR, not a new wire type). Real
# Spider attributes (Monster.createMonsterAttributes + Spider overrides) seeded with the jar values
# (Spider.createAttributes: MAX_HEALTH 16.0, MOVEMENT_SPEED 0.3 — 35-JARNOTES.md:77). The combat goals
# are the Go-native 35-01 ports (kind=); the passive goals are .star callbacks. The goals at the EXACT
# jar registerGoals indices + flags (the v1 subset; AvoidArmadillo@2 + IronGolem target@3 DEFERRED).
# Cite net.minecraft.world.entity.monster.spider.Spider.registerGoals + Spider.createAttributes.
declare_mob(
    name = "vanilla_spider",
    base_type = "spider",
    attributes = {
        "max_health": 16.0,     # Spider.createAttributes: Monster + MAX_HEALTH 16.0
        "movement_speed": 0.3,  # Spider.createAttributes: Monster + MOVEMENT_SPEED 0.3
    },
    goals = [
        # @1 FloatGoal [JUMP] — kind="float" (the 35-01 Go-native floatGoal, requiresUpdateEveryTick owned
        # by the Go goal). Cite Spider.registerGoals @1 FloatGoal.
        goal(priority = 1, flags = ["JUMP"], kind = "float"),
        # @2 AvoidEntityGoal<Armadillo> — DEFERRED (no Armadillo / no AvoidEntityGoal in v1; see header).
        # @3 LeapAtTargetGoal(mob, 0.4) [JUMP, MOVE] — kind="leap_at_target" (the 35-01 leapAtTargetGoal,
        # yd=0.4): the band + on-ground + nextInt(5) gate, the velocity impulse on start. THE leap (the ONE
        # goal that IMPULSES). Cite Spider.registerGoals @3 LeapAtTargetGoal(0.4).
        goal(priority = 3, flags = ["JUMP", "MOVE"], kind = "leap_at_target"),
        # @4 SpiderAttackGoal(mob) [MOVE] — kind="spider_attack" (the 35-01 meleeAttackGoal + the daylight
        # flee): paths to + swings at the target through the Phase-29 doHurtTarget keystone (REAL damage),
        # and in bright light drops the target 1-in-100/tick. Cite Spider.registerGoals @4 SpiderAttackGoal.
        goal(priority = 4, flags = ["MOVE"], kind = "spider_attack"),
        # @5 WaterAvoidingRandomStrollGoal(mob, 0.8) [MOVE] — .star (passive). can_use commits the
        # candidates (path_to), so NO start kwarg. Cite Spider.registerGoals @5 WaterAvoidingRandomStrollGoal(0.8).
        goal(
            priority = 5,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @6 LookAtPlayerGoal(Player, 8.0) [LOOK] — .star (passive). Cite Spider.registerGoals @6 LookAtPlayerGoal(Player, 8.0).
        goal(
            priority = 6,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @6 RandomLookAroundGoal [MOVE, LOOK] — .star (passive), requiresUpdateEveryTick=true. Cite Spider.registerGoals @6 RandomLookAroundGoal.
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
        # retaliate against the last attacker (NO RNG). Cite Spider.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 SpiderTargetGoal<Player>(mob) [TARGET] — kind="nearest_attackable_target" (the
        # 35-01 nearestAttackableTargetGoal: the nextInt(10) acquire gate + findTarget, sets attackTargetID).
        # The SpiderTargetGoal subclass's daylight-ACQUIRE narrowing collapses to the base acquire (cite-
        # deferred with the light engine; the daylight-FLEE IS modeled in spider_attack). Cite Spider
        # .registerGoals targetSelector @2 SpiderTargetGoal<Player>. (@3 SpiderTargetGoal<IronGolem> DEFERRED.)
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
