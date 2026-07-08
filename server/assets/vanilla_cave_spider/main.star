# vanilla_cave_spider -- a 1:1 vanilla-CaveSpider dogfood, the CaveSpider built as a Starlark plugin. A
# LITERAL port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p):
# net.minecraft.world.entity.monster.spider.CaveSpider extends Spider and OVERRIDES neither registerGoals
# (its AI is Spider.registerGoals VERBATIM) NOR createAttributes except MAX_HEALTH (createCaveSpider =
# Spider.createAttributes with MAX_HEALTH 12.0). So this declaration is the vanilla_spider goal set VERBATIM
# with base_type "cave_spider" and max_health 12.0 (movement_speed 0.3 and every goal identical).
#
# THE SIGNATURE (Go-native, variant_mobs2.go): CaveSpider.doHurtTarget overrides Spider's -- after
# super.doHurtTarget lands, a NORMAL-difficulty hit adds POISON for i*20 ticks (i=7 NORMAL, i=15 HARD,
# i=0 EASY/PEACEFUL), amplifier 0. That per-type poison tail is caveSpiderApplyPoison, wired into
# checkAndPerformAttack (CaveSpider-gated), NOT in the .star. The smaller CaveSpider bbox is cite-deferred
# (no per-type bbox seam in the declared-mob spawn path; the AABB comes from the entity.CaveSpider dims).
# Cite CaveSpider.registerGoals (inherits Spider) + CaveSpider.createCaveSpider (MAX_HEALTH 12.0) +
# CaveSpider.doHurtTarget (POISON i*20).
#
# ------------------------------------------------------------------------------------------------
# The GOAL machinery below is COPIED VERBATIM from vanilla_spider (Spider.registerGoals: FloatGoal@1 +
# LeapAtTargetGoal@3 + SpiderAttackGoal@4 + stroll@5 + look@6 + around@6; targetSelector HurtByTargetGoal@1
# + SpiderTargetGoal<Player>@2). See vanilla_spider/main.star for the full bytecode citations per goal.
# ------------------------------------------------------------------------------------------------

# math is a host-predeclared global (starlark math.Module) -- used by RandomLookAroundGoal.start's
# cos/sin. No load() needed (the host injects it, plugin/starlark/runtime.go).

# --- constants (jar-confirmed) -----------------------------------------------------------------
# WaterAvoidingRandomStrollGoal(mob, 0.8) -- Spider.registerGoals @5 (the spider's stroll speed is 0.8,
# NOT the cow's 1.0; the speed is the want-multiplier, cited-deferred -- the gate/candidate draws match).
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(DEFAULT_INTERVAL=120) == positiveCeilDiv(120,2) == 60
STROLL_H = 10                  # LandRandomPos/DefaultRandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7                   # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (2-arg ctor default)

LOOK_DIST = 8.0           # LookAtPlayerGoal lookDistance (Spider: 8.0f -- NOT the cow's 6.0)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# @5  WaterAvoidingRandomStrollGoal(mob, 0.8)   flags {MOVE}   -- COPIED VERBATIM from vanilla_cow
# (only the speed differs: 0.8 vs the cow's 1.0 -- a want-multiplier, cited-deferred; the draws match)
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
# @6  LookAtPlayerGoal(mob, Player.class, 8.0f)   flags {LOOK}   -- COPIED from vanilla_cow (dist 8.0)
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
# @6  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true -- COPIED VERBATIM
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
# base_type "cave_spider" -> renders as entity.CaveSpider.ID (id 22; custom = BEHAVIOR, not a new wire
# type). Real CaveSpider attributes (Spider.createAttributes with the MAX_HEALTH 12.0 override) seeded with
# the jar values (CaveSpider.createCaveSpider: MAX_HEALTH 12.0, MOVEMENT_SPEED 0.3). The combat goals
# are the Go-native 35-01 ports (kind=); the passive goals are .star callbacks. The goals at the EXACT
# jar registerGoals indices + flags (the v1 subset; AvoidArmadillo@2 + IronGolem target@3 DEFERRED).
# Cite net.minecraft.world.entity.monster.spider.CaveSpider (inherits Spider.registerGoals) +
# CaveSpider.createCaveSpider (Spider.createAttributes, MAX_HEALTH 12.0).
declare_mob(
    name = "vanilla_cave_spider",
    base_type = "cave_spider",
    attributes = {
        "max_health": 12.0,     # CaveSpider.createCaveSpider: Spider.createAttributes with MAX_HEALTH 12.0 (override)
        "movement_speed": 0.3,  # Spider.createAttributes: Monster + MOVEMENT_SPEED 0.3
    },
    goals = [
        # @1 FloatGoal [JUMP] -- kind="float" (the 35-01 Go-native floatGoal, requiresUpdateEveryTick owned
        # by the Go goal). Cite Spider.registerGoals @1 FloatGoal.
        goal(priority = 1, flags = ["JUMP"], kind = "float"),
        # @2 AvoidEntityGoal<Armadillo> -- DEFERRED (no Armadillo / no AvoidEntityGoal in v1; see header).
        # @3 LeapAtTargetGoal(mob, 0.4) [JUMP, MOVE] -- kind="leap_at_target" (the 35-01 leapAtTargetGoal,
        # yd=0.4): the band + on-ground + nextInt(5) gate, the velocity impulse on start. THE leap (the ONE
        # goal that IMPULSES). Cite Spider.registerGoals @3 LeapAtTargetGoal(0.4).
        goal(priority = 3, flags = ["JUMP", "MOVE"], kind = "leap_at_target"),
        # @4 SpiderAttackGoal(mob) [MOVE] -- kind="spider_attack" (the 35-01 meleeAttackGoal + the daylight
        # flee): paths to + swings at the target through the Phase-29 doHurtTarget keystone (REAL damage),
        # and in bright light drops the target 1-in-100/tick. Cite Spider.registerGoals @4 SpiderAttackGoal.
        goal(priority = 4, flags = ["MOVE"], kind = "spider_attack"),
        # @5 WaterAvoidingRandomStrollGoal(mob, 0.8) [MOVE] -- .star (passive). can_use commits the
        # candidates (path_to), so NO start kwarg. Cite Spider.registerGoals @5 WaterAvoidingRandomStrollGoal(0.8).
        goal(
            priority = 5,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @6 LookAtPlayerGoal(Player, 8.0) [LOOK] -- .star (passive). Cite Spider.registerGoals @6 LookAtPlayerGoal(Player, 8.0).
        goal(
            priority = 6,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @6 RandomLookAroundGoal [MOVE, LOOK] -- .star (passive), requiresUpdateEveryTick=true. Cite Spider.registerGoals @6 RandomLookAroundGoal.
        goal(
            priority = 6,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] -- kind="hurt_by_target" (the 35-01 hurtByTargetGoal):
        # retaliate against the last attacker (NO RNG). Cite Spider.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 SpiderTargetGoal<Player>(mob) [TARGET] -- kind="nearest_attackable_target" (the
        # 35-01 nearestAttackableTargetGoal: the nextInt(10) acquire gate + findTarget, sets attackTargetID).
        # The SpiderTargetGoal subclass's daylight-ACQUIRE narrowing collapses to the base acquire (cite-
        # deferred with the light engine; the daylight-FLEE IS modeled in spider_attack). Cite Spider
        # .registerGoals targetSelector @2 SpiderTargetGoal<Player>. (@3 SpiderTargetGoal<IronGolem> DEFERRED.)
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
