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
# @7 LookAtPlayerGoal(Player,6.0f), @8 RandomLookAroundGoal. @0/@1/@3/@4/@5 are DEFERRED with a
# cited reason (deferred-goals.md): their prerequisites (mob jump control + fluid detection; a mob
# damage source; entity aging/breeding; held-item tags) are unbuilt — porting them now would be
# built-but-unwired or faked, both forbidden. This plugin ports @6/@7/@8 1:1.
#
# Each goal callback receives (entity, world, nav) handles. The interpreter fires ONLY while a goal
# is RUNNING (an idle pig makes zero starlark.Calls per tick). The draw ORDER below matches the
# bytecode + the Go oracle EXACTLY (the per-entity seeded RandomSource — 24-01 — makes a fixed seed
# reproduce the identical stream, so TestPluginPigEqualsGoNativePig can prove plugin == Go pig).

# --- constants (jar-confirmed) -----------------------------------------------------------------
STROLL_INTERVAL = 120     # RandomStrollGoal.DEFAULT_INTERVAL (the 1-in-N chance gate)
STROLL_H = 10             # DefaultRandomPos.getPos horizontal radius (Pig: getPosition()=getPos(mob,10,7))
STROLL_V = 7              # DefaultRandomPos.getPos vertical radius
LOOK_DIST = 6.0           # LookAtPlayerGoal lookDistance (Pig: 6.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# @6  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.RandomStrollGoal (WaterAvoidingRandomStrollGoal base)
# ============================================================================================
# canUse (bytecode): [passenger/noActionTime gates omitted — a v1 passive pig has no rider/combat,
# matching the Go oracle's documented faithful-scope] -> getRandom().nextInt(reducedTickDelay(interval))
# != 0 ? false : getPosition() (DefaultRandomPos.getPos = 3 nextInt draws) -> stash wantedX/Y/Z.
# DRAW ORDER: the interval gate FIRST (DRAW 1), THEN the 3 offset draws (DRAWS 2,3,4) — never the
# offset before the gate (Pitfall 1). reducedTickDelay(120)==120 at 20 TPS (matches the Go oracle's
# raw nextInt(120)).
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_INTERVAL) != 0:   # DRAW 1: the 1-in-interval gate
        return False
    dx = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # DRAW 2: x offset in [-10,10]
    dz = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # DRAW 3: z offset in [-10,10]
    dy = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # DRAW 4: y offset in [-7,7]
    entity.set_state("want_x", entity.x + dx)   # vanilla wantedX/Y/Z (stashed for start())
    entity.set_state("want_y", entity.y + dy)
    entity.set_state("want_z", entity.z + dz)
    return True

# start (bytecode): navigation.moveTo(wantedX, wantedY, wantedZ, speed). The Go oracle commits the
# stashed want via setWantTarget; here nav.path_to is that seam.
def stroll_start(entity, world, nav):
    nav.path_to(entity.get_state("want_x"), entity.get_state("want_y"), entity.get_state("want_z"))

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
        # @6 WaterAvoidingRandomStrollGoal [MOVE] — empty tick (start/continue carry it).
        goal(
            priority = 6,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            start = stroll_start,
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
