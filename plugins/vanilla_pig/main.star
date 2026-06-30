# vanilla_pig — the FIRST 1:1 vanilla-mob dogfood (PLUGIN-04). The vanilla Pig's PASSIVE-AMBIENT AI
# re-expressed AS a Starlark plugin, staying a LITERAL method-for-method port of the unobfuscated
# 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p, this session). It declares the SAME 3 passive
# goals the Go newPigAI builds (server/ai_mob.go / ai_goals_passive.go — the behavior-identical
# ORACLE), at the EXACT priorities + flags read from net.minecraft.world.entity.animal.pig.Pig
# .registerGoals, each consuming the Plan 24-01 handle seams (set_look_at, nearest_player, rand_int/
# rand_float, the per-goal get_state/set_state scratch) and the Plan 07 nav seam (path_to/has_path).
#
# Pig.registerGoals() (javap-verified): @0 FloatGoal, @1 PanicGoal(1.25), @3 BreedGoal(1.0),
# @4 TemptGoal x2, @5 FollowParentGoal(1.1), @6 WaterAvoidingRandomStrollGoal(1.0),
# @7 LookAtPlayerGoal(Player,6.0f), @8 RandomLookAroundGoal. @0 FloatGoal is PORTED (Phase 30-03: its
# prerequisites — mob jump control + fluid detection — landed in 30-01/30-02). @1/@3/@4/@5 remain
# DEFERRED with a cited reason (deferred-goals.md): their prerequisites (a mob damage source; entity
# aging/breeding; held-item tags) are unbuilt — porting them now would be built-but-unwired or faked,
# both forbidden. This plugin ports @0/@6/@7/@8 1:1.
#
# Each goal callback receives (entity, world, nav) handles. The interpreter fires ONLY while a goal
# is RUNNING (an idle pig makes zero starlark.Calls per tick). The draw ORDER below matches the
# bytecode + the Go oracle EXACTLY (the per-entity seeded RandomSource — 24-01 — makes a fixed seed
# reproduce the identical stream, so TestPluginPigEqualsGoNativePig can prove plugin == Go pig).

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f (the swim-jump chance)
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold for the pig: getEyeHeight()<0.4?0.0:0.4;
                               # pig eye 0.765 >= 0.4 -> 0.4 (Plan 30-01 jar finding, LOCKSTEP with the
                               # Go oracle's t.getFluidJumpThreshold(e) == 0.4). FloatGoal.canUse compares
                               # entity.fluid_height (WATER) > this. In the DRY oracle in_water is false so
                               # canUse short-circuits before this comparison -> the value never gates a draw.
STROLL_INTERVAL = 120     # RandomStrollGoal.DEFAULT_INTERVAL (before Goal.reducedTickDelay halves it)
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(120) == Mth.positiveCeilDiv(120,2) == 60: canUse rolls
                               # nextInt(60), NOT nextInt(120) (the faithful gate; LOCKSTEP with the Go oracle)
STROLL_H = 10             # LandRandomPos/DefaultRandomPos.getPos horizontal radius (Pig: getPos(mob,10,7))
STROLL_V = 7              # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (the Pig 2-arg ctor
                               # default): getPosition rolls nextFloat() — >= probability (~99.9% COMMON)
                               # picks LandRandomPos (up-snap), < probability (~0.1% RARE) picks
                               # DefaultRandomPos (no up-snap). The DRAW is required for lockstep; the
                               # Go runtime snap is consumed-as-Land (always up-snaps) so the chosen
                               # branch does not change the committed target today.
LOOK_DIST = 6.0           # LookAtPlayerGoal lookDistance (Pig: 6.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# @0  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true
# ports net.minecraft.world.entity.ai.goal.FloatGoal — LOCKSTEP with the Go oracle (server/ai_goals_float.go)
# ============================================================================================
# canUse (bytecode): isInWater() && getFluidHeight(WATER) > getFluidJumpThreshold() || isInLava(). NO RNG
# (the only FloatGoal draw is in tick()). The strict `>` matches the bytecode (dcmpl; ifgt). Reads the
# Plan-02 frozen-scalar handle attrs entity.in_water / entity.fluid_height (WATER) / entity.in_lava —
# the SAME predicates the Go oracle's t.mobInWater/mobFluidHeight/mobInLava compute, so the two halves
# agree. In the DRY oracle world in_water is false -> canUse false -> tick never runs -> zero new draws.
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

# tick (bytecode): if getRandom().nextFloat() < 0.8f -> getJumpControl().jump(). DRAW 1: the single new
# RNG in this subsystem, in tick() ONLY, drawn via entity.rand_float() (= e.ai.rng.nextFloat) — LOCKSTEP
# with the Go oracle's mobRandom(e).nextFloat(). On a draw < 0.8 it arms the jump via nav.jump()
# (capNav -> jumpControl.doJump). FloatGoal@0 runs FIRST in both goal walks, so this draw lands before
# stroll/look in both -> identical lockstep stream.
def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:   # DRAW 1: nextFloat()<0.8 (the swim-jump chance)
        nav.jump()

# ============================================================================================
# @6  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.RandomStrollGoal (WaterAvoidingRandomStrollGoal base)
# ============================================================================================
# canUse (WaterAvoidingRandomStrollGoal.getPosition, jar-verified): the 1-in-reducedTickDelay(interval)
# gate (DRAW 1: nextInt(60)), THEN — for a not-in-water pig — the probability nextFloat() gate (DRAW 2:
# nextFloat() >= 0.001 picks LandRandomPos (~99.9% COMMON, up-snap), < 0.001 picks DefaultRandomPos
# (~0.1% RARE, no up-snap); the draw is required for lockstep but the branch does not change the
# committed target today — the Go runtime snap is consumed-as-Land (always up-snaps)), THEN the
# RandomPos.generateRandomPos UNCONDITIONAL 10-candidate loop: each candidate = generateRandomDirection in
# x, y, z ORDER (3 nextInt). 31 RNG draws total (gate + probability + 30 offsets). Emit the 10 RAW
# candidates PLUS the wantLandMode flag as 31 FLAT positional floats via nav.path_to(x0,y0,z0,...,x9,y9,z9,
# landMode) → the Go runtime (snapStrollWant) validates + ground-snaps them (the architecture split — the
# .star does NO world read; the per-goal scratch is float-only so a list cannot be stashed). The commit
# lands in can_use (path_to fires on canUse success), matching the Go goal which draws in getPosition
# (called from canUse). DRAW ORDER must match the Go oracle (gate, probability, then xt/yt/zt per candidate)
# or the bit-fragile pig oracle desyncs on the first diverging tick.
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(reducedTickDelay(120)=60) gate
        return False
    roll = entity.rand_float()   # DRAW 2: nextFloat() probability gate (WaterAvoidingRandomStrollGoal.getPosition)
    # wantLandMode: >= 0.001 => LandRandomPos (~99.9% COMMON, up-snap), < 0.001 => DefaultRandomPos (~0.1%
    # RARE, no up-snap). Carry the SAME bool the Go goal computes so mobAI.wantLandMode is byte-identical
    # across the arms (WR-05). Pass it as the 31st float (1.0/0.0); the snap is consumed-as-Land today, so
    # the mode does not change the committed target yet.
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
    nav.path_to(*flat)   # 31 positional floats (x0,y0,z0,...,x9,y9,z9, landMode) -> setWantCandidates -> the snap
    return True

# start: the candidates are committed in can_use (above), so start is a no-op. The goal() builtin makes
# start optional; the stroll goal declaration drops the start kwarg (no stroll_start).

# canContinueToUse (bytecode): !navigation.isDone(). The Go oracle: e.ai.hasTarget. nav.has_path is
# that seam.
def stroll_continue(entity, world, nav):
    return nav.has_path()

# stop (bytecode): navigation.stop(). The Go oracle: clearWantTarget(). nav.stop() is that seam —
# clearing the pending want target on the running->stopped edge (behavior-identical to the oracle).
def stroll_stop(entity, world, nav):
    nav.stop()

# RandomStrollGoal.tick() is EMPTY in the jar (its behavior is start()=moveTo + continue=!isDone).
# The goal() builtin makes tick optional, so this goal declares NO tick (24-RESEARCH Open-Q §7).

# ============================================================================================
# @7  LookAtPlayerGoal(mob, Player.class, 6.0f)   flags {LOOK}
# ports net.minecraft.world.entity.ai.goal.LookAtPlayerGoal
# ============================================================================================
# canUse (bytecode): getRandom().nextFloat() < probability (else false), then getNearestPlayer in
# lookDistance -> lookAt; return lookAt != null. The Go oracle reads the nearest player from
# loop.players via nearestPlayerWithin(e, dist) using e.x/e.y/e.z (NOT eye_y) — so this plugin uses
# entity.y for the scan to stay behavior-identical to the oracle.
def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:   # DRAW: nextFloat() < probability (>= => false)
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, LOOK_DIST)
    if p == None:
        return False
    entity.set_state("look_x", p[0])   # capture the look target (vanilla lookAt)
    entity.set_state("look_y", p[1])
    entity.set_state("look_z", p[2])
    return True

# start (bytecode): lookTime = adjustedTickDelay(40 + getRandom().nextInt(40)). The Go oracle:
# lookTime = 40 + nextInt(40) (adjustedTickDelay(n)==n at 20 TPS). DRAW after the canUse find.
def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))

# canContinueToUse (bytecode): lookAt.isAlive() && mob.distanceToSqr(lookAt) <= lookDistance^2 &&
# lookTime > 0. The Go oracle: hasLook && lookTime>0 && d2 <= lookDistance^2 (aliveness implicit — a
# player in loop.players is live). Uses the SAME entity y the oracle uses.
def look_continue(entity, world, nav):
    if entity.get_state("look_time") <= 0:
        return False
    dx = entity.get_state("look_x") - entity.x
    dy = entity.get_state("look_y") - entity.y
    dz = entity.get_state("look_z") - entity.z
    d2 = dx * dx + dy * dy + dz * dz
    return d2 <= LOOK_DIST * LOOK_DIST

# tick (bytecode): getLookControl().setLookAt(lookAt.getX(), lookAt.getEyeY(), lookAt.getZ()); then
# lookTime = lookTime - 1. The Go oracle: e.headYaw=e.yaw=yawTowardDeg(lookX-e.x, lookZ-e.z) then
# lookTime--. set_look_at is the LookControl.setLookAt analogue (host derives the SAME yawTowardDeg).
# Order: set look FIRST, THEN decrement (matches the bytecode + the Go oracle).
def look_tick(entity, world, nav):
    entity.set_look_at(entity.get_state("look_x"), entity.get_state("look_y"), entity.get_state("look_z"))
    entity.set_state("look_time", entity.get_state("look_time") - 1)

# stop (bytecode): lookAt = null. The Go oracle: hasLook=false. Clearing the captured target.
def look_stop(entity, world, nav):
    entity.set_state("look_time", 0)

# ============================================================================================
# @8  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true
# ports net.minecraft.world.entity.ai.goal.RandomLookAroundGoal
# ============================================================================================
# canUse (bytecode): getRandom().nextFloat() < 0.02f.
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

# start (bytecode): d = 6.283185307179586d * getRandom().nextDouble(); relX = cos(d); relZ = sin(d);
# lookTime = 20 + getRandom().nextInt(20). DRAW ORDER: the nextDouble heading FIRST, THEN nextInt(20).
# entity.rand_double() is the nextDouble() draw (DISTINCT from rand_float()=nextFloat) — it matches the
# Go oracle's d = 2*pi*r.nextDouble() (ai_goals_passive.go) EXACTLY, so the RNG stream stays identical.
def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()           # nextDouble() heading (DRAW 1)
    entity.set_state("rel_x", math.cos(d))      # relX = cos(d)
    entity.set_state("rel_z", math.sin(d))      # relZ = sin(d)
    entity.set_state("look_time", 20 + entity.rand_int(20))   # lookTime = 20 + nextInt(20) (DRAW 2)

# canContinueToUse (bytecode): lookTime >= 0.
def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

# tick (bytecode): lookTime = lookTime - 1; getLookControl().setLookAt(getX()+relX, getEyeY(),
# getZ()+relZ). The Go oracle: lookTime--; e.headYaw=e.yaw=yawTowardDeg(relX, relZ). Order: decrement
# FIRST, then aim (matches the bytecode + the Go oracle).
def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "pig" -> renders as entity.Pig.ID (custom = BEHAVIOR, not a new wire type). Real pig
# attributes (Wave-1 SUB-ATTRIB) seeded with the jar values (Pig.createAttributes: max_health 10.0,
# movement_speed ~0.25). The 3 goals at the EXACT jar priorities + flags.
declare_mob(
    name = "vanilla_pig",
    base_type = "pig",
    attributes = {
        "max_health": 10.0,      # Pig.createAttributes: MAX_HEALTH 10.0
        "movement_speed": 0.25,  # Pig.createAttributes: MOVEMENT_SPEED 0.25
    },
    goals = [
        # @0 FloatGoal [JUMP] — requiresUpdateEveryTick=true (jar-confirmed). FIRST = highest precedence,
        # LOCKSTEP with the Go oracle's addGoal(0, newFloatGoal()). canUse=fluid predicate (no RNG);
        # tick=nextFloat()<0.8 -> nav.jump() (the only new RNG, in tick()).
        goal(
            priority = 0,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @6 WaterAvoidingRandomStrollGoal [MOVE] — can_use commits the candidates (via path_to), so NO
        # start kwarg; stop/continue carry the rest.
        goal(
            priority = 6,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @7 LookAtPlayerGoal [LOOK].
        goal(
            priority = 7,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @8 RandomLookAroundGoal [MOVE, LOOK] — requiresUpdateEveryTick=true (jar-confirmed).
        goal(
            priority = 8,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
    ],
)
