# vanilla_spider — a 1:1 vanilla-Spider dogfood (MOB-HOST-03), the THIRD hostile mob built as a
# Starlark plugin (after zombie/skeleton). The vanilla Spider's HOSTILE AI re-expressed AS a Starlark
# plugin, staying a LITERAL method-for-method port of the unobfuscated 26.2 jar
# (temp/cache/26.2-inner.jar, javap -c -p). It declares the goal set
# net.minecraft.world.entity.monster.spider.Spider.registerGoals builds, reusing the proven Go goal
# runtime (the cow/pig goal callbacks are mob-agnostic — they read entity.* handles — so the SHARED
# passive callbacks are COPIED VERBATIM; only the constants + the mob-declaration block + the new
# combat goals differ).
#
# Spider.registerGoals() (javap-verified this session, 35-JARNOTES.md:57-71):
#   goalSelector:
#     @1 FloatGoal(this)
#     @2 AvoidEntityGoal<Armadillo>(this, Armadillo, 6.0, 1.0, 1.2, e -> !e.isScared())   <-- DEFERRED
#     @3 LeapAtTargetGoal(this, 0.4)
#     @4 SpiderAttackGoal(this)                              <-- MeleeAttackGoal subclass, daylight-flee
#     @5 WaterAvoidingRandomStrollGoal(this, 0.8)
#     @6 LookAtPlayerGoal(Player, 8.0)
#     @6 RandomLookAroundGoal
#   targetSelector:
#     @1 HurtByTargetGoal(this)
#     @2 SpiderTargetGoal<Player>(this, Player)              <-- NearestAttackableTargetGoal subclass, daylight-gated
#     @3 SpiderTargetGoal<IronGolem>(this, IronGolem)        <-- DEFERRED (no IronGolem entity yet)
#
# DEFERRED (cite-recorded, NEVER silently dropped — see the 35-05-SUMMARY):
#   - AvoidEntityGoal<Armadillo>@2: no Armadillo entity / no AvoidEntityGoal port in v1. The flee-from-
#     armadillo goal lands when the Armadillo + AvoidEntityGoal arrive. (35-JARNOTES.md:61, 128.)
#   - SpiderTargetGoal<IronGolem>@3: no IronGolem entity in v1 — the golem target variant defers with it.
#   - WALL-CLIMB (the spider's defining movement — Spider.tick sets the CLIMBING data flag from
#     horizontalCollision; the climb is a navigation/physics behavior, NOT a goal): no climb subsystem
#     exists in v1 (the nav is ground-only). Cite-deferred to the wall-climb physics plan; the spider
#     hunts/leaps/melees on the ground meanwhile. (Spider.tick / Spider.onClimbable.)
#   - getLightLevelDependentMagicValue (the SpiderAttackGoal/SpiderTargetGoal light read): no light
#     engine in v1 -> the gametime day/night proxy (35-02 isDarkEnoughToSpawn), the SAME FORCED decision
#     the spawn gate made. The daylight-flee RNG draw is STILL made when "bright" per the proxy (fidelity).
#
# Each goal callback receives (entity, world, nav) handles. The interpreter fires ONLY while a goal is
# RUNNING. The draw ORDER below matches the bytecode EXACTLY (the per-entity seeded RandomSource — 24-01).
# The spider reuses the proven goal runtime, so the focused per-mob test (TestSpiderBootLoads /
# TestSpiderBehavior) is a lighter spawn+behavior gate; the leap/daylight lockstep RNG is pinned by the
# Go-native goal tests (spider_test.go: TestLeapAtTargetGateRNG / TestSpiderAttackDaylightGate).

# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start's
# cos/sin + the leap's sqrt. No load() needed (the host injects it, plugin/starlark/runtime.go).

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f (the swim-jump chance)
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold: getEyeHeight()<0.4?0.0:0.4; a spider's eye
                               # is above 0.4 -> 0.4. FloatGoal.canUse compares fluid_height(WATER) > this.

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

# LeapAtTargetGoal(mob, 0.4) — Spider.registerGoals @3. THE leap goal (the ONE goal that IMPULSES).
LEAP_YD = 0.4                  # LeapAtTargetGoal.yd — the vertical leap component
LEAP_REDUCED_INTERVAL = 5      # ⚠ LeapAtTargetGoal.canUse gate: getRandom().nextInt(reducedTickDelay(5)).
                               # reducedTickDelay(5)==positiveCeilDiv(5,2)==3 in the jar, BUT our full-rate
                               # serverAiStep does not decimate, so the FAITHFUL Go value is the RAW 5 —
                               # the SAME identity rule NearestAttackableTargetGoal (rand_int(10), not 5)
                               # follows. (35-JARNOTES pre-decompile said "nextFloat"; the exec-time
                               # decompile CORRECTS that to nextInt(reducedTickDelay(5)) — the jar wins.)
LEAP_MIN_DIST_SQR = 4.0        # LeapAtTargetGoal.canUse: distanceToSqr(target) >= 4.0
LEAP_MAX_DIST_SQR = 16.0       # LeapAtTargetGoal.canUse: distanceToSqr(target) <= 16.0
LEAP_HORIZONTAL_SCALE = 0.4    # start(): v.normalize().scale(0.4)
LEAP_DELTA_CARRY = 0.2         # start(): .add(delta.scale(0.2))
LEAP_LENGTH_SQR_EPSILON = 0.0000001   # start(): normalize only if v.lengthSqr() > 1.0E-7

# SpiderAttackGoal(mob) — Spider.registerGoals @4 (= MeleeAttackGoal + the daylight-flee).
ATTACK_RANGE = 16.0            # the host nearest-player scan bound for the melee target re-resolve
SPIDER_DAYLIGHT_FLEE_CHANCE = 100   # Spider$SpiderAttackGoal.canContinueToUse: in bright light,
                               # nextInt(100)==0 -> setTarget(null) (1-in-100/tick daylight drop).

# SpiderTargetGoal<Player>(mob) — Spider.registerGoals targetSelector @2 (= NearestAttackableTargetGoal
# + the daylight-gate). NearestAttackableTargetGoal.canUse: nextInt(randomInterval)==0 then findTarget.
TARGET_RANDOM_INTERVAL = 10    # the FULL DEFAULT_RANDOM_INTERVAL (NOT reducedTickDelay's 5 — the same
                               # full-rate identity as the leap gate; 35-JARNOTES.md:149-164).
FOLLOW_RANGE = 16.0            # the host nearest-player scan bound (TargetingConditions.range = FOLLOW_RANGE)

# ============================================================================================
# @1  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true   — COPIED VERBATIM from vanilla_cow
# ports net.minecraft.world.entity.ai.goal.FloatGoal (mob-agnostic)
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:   # DRAW: nextFloat()<0.8 (the swim-jump chance)
        nav.jump()

# ============================================================================================
# @3  LeapAtTargetGoal(mob, 0.4)   flags {JUMP, MOVE}
# ports net.minecraft.world.entity.ai.goal.LeapAtTargetGoal — THE leap (the ONE goal that IMPULSES).
# ============================================================================================
# canUse (bytecode): if (hasControllingPassenger()) return false; target = getTarget(); if (target==null)
# return false; d = distanceToSqr(target); if (d<4.0 || d>16.0) return false; if (!onGround()) return
# false; return getRandom().nextInt(reducedTickDelay(5)) == 0. THE GATE IS THE LAST CHECK — the band +
# on-ground guards precede it (an out-of-band/airborne reject draws ZERO RNG). v1 resolves the target as
# the nearest player (the targetSelector's SpiderTargetGoal sets it Go-side; the .star re-resolves the
# nearest player as the leap target, the same anchor LookAtPlayer uses). The rand_int(5) is the FAITHFUL
# full-rate value (see LEAP_REDUCED_INTERVAL).
def leap_can_use(entity, world, nav):
    p = world.nearest_player(entity.x, entity.y, entity.z, FOLLOW_RANGE)   # the leap target (nearest player)
    if p == None:
        return False
    dx = p[0] - entity.x
    dy = p[1] - entity.y
    dz = p[2] - entity.z
    d = dx * dx + dy * dy + dz * dz
    if d < LEAP_MIN_DIST_SQR or d > LEAP_MAX_DIST_SQR:   # the leap distance band (BEFORE the gate)
        return False
    if not entity.on_ground:   # must be grounded to leap (BEFORE the gate)
        return False
    if entity.rand_int(LEAP_REDUCED_INTERVAL) != 0:   # DRAW (the gate, LAST): nextInt(reducedTickDelay(5)=>5)
        return False
    # capture the leap target for start()'s impulse.
    entity.set_state("leap_tx", p[0])
    entity.set_state("leap_tz", p[2])
    return True

# start (bytecode): delta = getDeltaMovement(); v = new Vec3(tx-x, 0, tz-z); if (v.lengthSqr()>1e-7)
# v = v.normalize().scale(0.4).add(delta.scale(0.2)); setDeltaMovement(v.x, yd, v.z). The IMPULSE (the
# ONE goal exception — it sets a velocity delta, it does NOT path). set_velocity is the setDeltaMovement seam.
def leap_start(entity, world, nav):
    tx = entity.get_state("leap_tx")
    tz = entity.get_state("leap_tz")
    delta = entity.velocity   # getDeltaMovement() (vx, vy, vz)
    vx = tx - entity.x        # v = (tx-x, 0, tz-z)
    vz = tz - entity.z
    if vx * vx + vz * vz > LEAP_LENGTH_SQR_EPSILON:   # normalize only above the epsilon (degenerate guard)
        length = math.sqrt(vx * vx + vz * vz)
        vx = vx / length * LEAP_HORIZONTAL_SCALE + delta[0] * LEAP_DELTA_CARRY
        vz = vz / length * LEAP_HORIZONTAL_SCALE + delta[2] * LEAP_DELTA_CARRY
    entity.set_velocity(vx, LEAP_YD, vz)   # setDeltaMovement(v.x, yd, v.z) — the impulse, NOT a nav want

# canContinueToUse (bytecode): !mob.onGround() — the leap continues while airborne (the pounce arc).
def leap_continue(entity, world, nav):
    return not entity.on_ground

# ============================================================================================
# @4  SpiderAttackGoal(mob)   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.monster.spider.Spider$SpiderAttackGoal (= MeleeAttackGoal + the
# daylight-flee). The base melee paths to the target + swings in reach; the SpiderAttackGoal delta is
# canContinueToUse's 1-in-100/tick daylight target-drop.
# ============================================================================================
# canUse (bytecode): super.canUse() && !mob.isVehicle(). super.canUse() = MeleeAttackGoal.canUse (the
# 20-tick lastCanUseCheck gate + getTarget()!=null + a buildable path / in reach). !isVehicle is a cited
# no-op in v1 (no vehicle subsystem). v1 re-resolves the nearest player as the melee target (the
# targetSelector commits it Go-side; the .star approximates "a target exists" by the nearest player).
def spider_attack_can_use(entity, world, nav):
    p = world.nearest_player(entity.x, entity.y, entity.z, ATTACK_RANGE)
    if p == None:
        return False
    entity.set_state("attack_px", p[0])
    entity.set_state("attack_py", p[1])
    entity.set_state("attack_pz", p[2])
    return True

# tick (bytecode): setLookAt(target); navigation.moveTo(target, speed); checkAndPerformAttack. The .star
# faces + paths toward the target (the host melee-hit is a HOST-side op the targetSelector/Go-native
# meleeAttackGoal performs through the player hurt path — NOT a plugin op; see the 35-05-SUMMARY). The
# leap+melee re-resolve the nearest player each tick.
def spider_attack_tick(entity, world, nav):
    px = entity.get_state("attack_px")
    py = entity.get_state("attack_py")
    pz = entity.get_state("attack_pz")
    entity.set_look_at(px, py, pz)   # setLookAt(target)
    entity.move_to(px, py, pz)       # navigation.moveTo(target, speed) — SET a want, never moves the mob

# canContinueToUse (bytecode): float br = getLightLevelDependentMagicValue(); if (br>=0.5f &&
# getRandom().nextInt(100)==0) { setTarget(null); return false; } return super.canContinueToUse(). In
# BRIGHT light (br>=0.5 — the day proxy, world.is_bright) the spider drops its target 1-in-100/tick. The
# nextInt(100) DRAW is made ONLY when bright (the proxy); at night the branch is skipped (no draw). The
# light read is the gametime day/night proxy (35-02), cite-deferred to a real getLightLevelDependentMagicValue.
def spider_attack_continue(entity, world, nav):
    # br >= 0.5f (daylight) per the day/night proxy: bright == NOT is_dark (the inverse of the spawn
    # gate's isDarkEnoughToSpawn). The host world.is_dark() reads the gametime night-window proxy (35-02).
    if not world.is_dark():   # bright (daylight)
        if entity.rand_int(SPIDER_DAYLIGHT_FLEE_CHANCE) == 0:   # DRAW (bright only): nextInt(100)==0 -> flee
            return False    # the daylight 1/100 target-drop (the Go-native goal does setTarget(null))
    return spider_attack_can_use(entity, world, nav)   # super.canContinueToUse(): the target still resolves

# ============================================================================================
# @5  WaterAvoidingRandomStrollGoal(mob, 0.8)   flags {MOVE}   — COPIED VERBATIM from vanilla_cow
# (only the speed differs: 0.8 vs the cow's 1.0 — a want-multiplier, cited-deferred; the draws match)
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

# ============================================================================================
# targetSelector @1  HurtByTargetGoal(mob)   flags {TARGET}   — the 35-01 hurtByTargetGoal (NO RNG)
# ports net.minecraft.world.entity.ai.goal.target.HurtByTargetGoal — retaliate against the last attacker.
# ============================================================================================
# canUse (bytecode): timestamp = getLastHurtByMobTimestamp(); last = getLastHurtByMob(); if (timestamp
# == this.timestamp || last == null) return false; ... canAttack(last). NO RNG. The lastHurtByMob
# bookkeeping is HOST-side (combat_mob.go flag2 store-point, 35-01); the .star gates on was_hurt as the
# observable retaliate signal (the Go-native hurtByTargetGoal carries the timestamp discipline + setTarget).
def hurt_by_can_use(entity, world, nav):
    return entity.was_hurt   # the host retaliate signal (LivingEntity.lastHurtByMob set this tick)

def hurt_by_continue(entity, world, nav):
    return entity.was_hurt

# ============================================================================================
# targetSelector @2  SpiderTargetGoal<Player>(mob)   flags {TARGET}
# ports net.minecraft.world.entity.monster.spider.Spider$SpiderTargetGoal (= NearestAttackableTargetGoal
# + the daylight gate). canUse: super.canUse() (the nextInt(10) gate + findTarget) only when... the
# SpiderTargetGoal narrows acquisition by light: a spider acquires a NEW player target only at night /
# keeps it via the SpiderAttackGoal flee. The NearestAttackableTargetGoal.canUse gate is nextInt(10).
# ============================================================================================
# canUse (bytecode, NearestAttackableTargetGoal): if (randomInterval>0 && getRandom().nextInt(10)!=0)
# return false; findTarget(); return target != null. The rand_int(10) is the FULL DEFAULT_RANDOM_INTERVAL
# (NOT reducedTickDelay's 5 — the full-rate identity, 35-JARNOTES.md:149-164). findTarget = the nearest
# player in FOLLOW_RANGE (the Go-native nearestAttackableTargetGoal sets attackTargetID; the .star
# re-resolves the nearest player). NO RNG beyond the gate.
def spider_target_can_use(entity, world, nav):
    if entity.rand_int(TARGET_RANDOM_INTERVAL) != 0:   # DRAW (the gate): nextInt(10)!=0 -> false
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, FOLLOW_RANGE)   # findTarget (the player branch)
    return p != None

def spider_target_continue(entity, world, nav):
    p = world.nearest_player(entity.x, entity.y, entity.z, FOLLOW_RANGE)
    return p != None

# --- the declaration ---------------------------------------------------------------------------
# base_type "spider" -> renders as entity.Spider.ID (custom = BEHAVIOR, not a new wire type). Real
# Spider attributes (Monster.createMonsterAttributes + Spider overrides) seeded with the jar values
# (Spider.createAttributes: MAX_HEALTH 16.0, MOVEMENT_SPEED 0.3 — 35-JARNOTES.md:77). The goals at the
# EXACT jar registerGoals indices + flags (the v1 subset; AvoidArmadillo@2 + IronGolem target@3 DEFERRED).
# Cite net.minecraft.world.entity.monster.spider.Spider.registerGoals + Spider.createAttributes.
declare_mob(
    name = "vanilla_spider",
    base_type = "spider",
    attributes = {
        "max_health": 16.0,     # Spider.createAttributes: Monster + MAX_HEALTH 16.0
        "movement_speed": 0.3,  # Spider.createAttributes: Monster + MOVEMENT_SPEED 0.3
    },
    goals = [
        # @1 FloatGoal [JUMP] — requiresUpdateEveryTick=true. COPIED VERBATIM from vanilla_cow.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @2 AvoidEntityGoal<Armadillo> — DEFERRED (no Armadillo / no AvoidEntityGoal in v1; see header).
        # @3 LeapAtTargetGoal(mob, 0.4) [JUMP, MOVE] — THE leap (impulse). can_use gates on the band +
        # on-ground + nextInt(5); start sets the velocity impulse. Cite Spider.registerGoals @3 LeapAtTargetGoal(0.4).
        goal(
            priority = 3,
            flags = ["JUMP", "MOVE"],
            can_use = leap_can_use,
            start = leap_start,
            can_continue = leap_continue,
        ),
        # @4 SpiderAttackGoal(mob) [MOVE, LOOK] — the daylight-gated melee (canContinueToUse drops the
        # target 1-in-100/tick in bright light). Cite Spider.registerGoals @4 SpiderAttackGoal.
        goal(
            priority = 4,
            flags = ["MOVE", "LOOK"],
            can_use = spider_attack_can_use,
            tick = spider_attack_tick,
            can_continue = spider_attack_continue,
        ),
        # @5 WaterAvoidingRandomStrollGoal(mob, 0.8) [MOVE] — COPIED VERBATIM (speed 0.8). can_use commits
        # the candidates (path_to), so NO start kwarg. Cite Spider.registerGoals @5 WaterAvoidingRandomStrollGoal(0.8).
        goal(
            priority = 5,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @6 LookAtPlayerGoal(Player, 8.0) [LOOK]. Cite Spider.registerGoals @6 LookAtPlayerGoal(Player, 8.0).
        goal(
            priority = 6,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @6 RandomLookAroundGoal [MOVE, LOOK] — requiresUpdateEveryTick=true. Cite Spider.registerGoals @6 RandomLookAroundGoal.
        goal(
            priority = 6,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] — retaliate against the last attacker (NO RNG).
        # Cite Spider.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(
            priority = 1,
            flags = ["TARGET"],
            can_use = hurt_by_can_use,
            can_continue = hurt_by_continue,
        ),
        # targetSelector @2 SpiderTargetGoal<Player>(mob) [TARGET] — the daylight-gated nearest-player
        # acquire (the nextInt(10) gate). Cite Spider.registerGoals targetSelector @2 SpiderTargetGoal<Player>.
        # (targetSelector @3 SpiderTargetGoal<IronGolem> DEFERRED — no IronGolem entity in v1; see header.)
        goal(
            priority = 2,
            flags = ["TARGET"],
            can_use = spider_target_can_use,
            can_continue = spider_target_continue,
        ),
    ],
)
