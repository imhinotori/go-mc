# vanilla_wolf — a 1:1 vanilla-Wolf dogfood (MOB-NEUT-01/02), the FIRST tameable/neutral mob built as a
# Starlark plugin. The vanilla Wolf's AI re-expressed AS a Starlark plugin, staying a LITERAL
# method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It declares
# the goal set net.minecraft.world.entity.animal.wolf.Wolf.registerGoals builds, the HIGHEST-reuse mob in
# v5: it consumes the Phase-35 Go-native COMBAT goals (LeapAtTargetGoal/MeleeAttackGoal/HurtByTargetGoal)
# AND the Phase-36 Go-native TAME/OWNER/ANGER goals (sit/follow_owner/owner_hurt_by/owner_hurt +
# angry_player_target/skeleton_target — all 36-01 buildNativeGoal kinds) via the 35-01b kind= seam, AND the
# mob-agnostic PASSIVE goal callbacks (float/panic/stroll/look/around/breed) COPIED VERBATIM from vanilla_cow.
#
# THE HYBRID kind=/callback PATTERN (why the combat/tame goals carry NO .star body): the combat goals
# (MeleeAttackGoal/LeapAtTargetGoal/HurtByTargetGoal) are the SAME CLASS every hostile uses, and the
# tame/owner/target goals (SitWhenOrderedToGoal/FollowOwnerGoal/OwnerHurt*/the parameterized
# NearestAttackableTargetGoal) are the Phase-36 Go-native ports — there is nothing per-mob to re-express in
# Starlark, and re-expressing combat/anger RNG in .star would risk lockstep drift. So the wolf DECLARES those
# goals' priority + flags + kind, and the verbatim 36-01 Go-native goals do the work. The PASSIVE goals
# (float/panic/stroll/look/around/breed) stay .star callbacks (the mob-agnostic shared port from vanilla_cow).
#
# Wolf.registerGoals() (javap-verified, 36-JARNOTES.md:13-37):
#   goalSelector:
#     @1  FloatGoal(this)                                                  <-- .star (the shared passive float)
#     @1  TamableAnimal.TamableAnimalPanicGoal(this, 1.5, PANIC_*_CAUSES)  <-- .star (the cow PanicGoal reuse @ speed 1.5;
#                                                                              TamableAnimalPanicGoal EXTENDS PanicGoal)
#     @2  SitWhenOrderedToGoal(this)                                       <-- kind="sit" (36-01 sitWhenOrderedToGoal)
#     @3  WolfAvoidEntityGoal<Llama>(this, ..., 24.0, 1.5, 1.5)            <-- DEFERRED (no Llama mob; defers WITH its target)
#     @4  LeapAtTargetGoal(this, 0.4)                                      <-- kind="leap_at_target" (35-01 leapAtTargetGoal)
#     @5  MeleeAttackGoal(this, 1.0, true)                                 <-- kind="melee_attack" (35-01 meleeAttackGoal)
#     @6  FollowOwnerGoal(this, 1.0, 10.0, 2.0)                            <-- kind="follow_owner" (36-01 followOwnerGoal)
#     @7  BreedGoal(this, 1.0)                                             <-- .star (the cow breed_* reuse, Phase 33)
#     @8  WaterAvoidingRandomStrollGoal(this, 1.0)                         <-- .star (the shared passive stroll)
#     @9  BegGoal(this, 8.0)                                               <-- DEFERRED (client head-tilt visual)
#     @10 LookAtPlayerGoal(Player, 8.0)                                    <-- .star (the shared passive look, dist 8.0)
#     @10 RandomLookAroundGoal                                             <-- .star (the shared passive around)
#   targetSelector:
#     @1  OwnerHurtByTargetGoal(this)                                      <-- kind="owner_hurt_by" (36-01 ownerHurtByTargetGoal)
#     @2  OwnerHurtTargetGoal(this)                                        <-- kind="owner_hurt" (36-01 ownerHurtTargetGoal)
#     @3  HurtByTargetGoal(this).setAlertOthers()                         <-- kind="hurt_by_target" (35-01; alertOthers cite-deferred)
#     @4  NearestAttackableTargetGoal<Player>(this, 10, true, false, this::isAngryAt)
#                                                                          <-- kind="angry_player_target" (36-01 B1: the anger-gated
#                                                                              PLAYER goal — a wild un-hit wolf does NOT aggro players;
#                                                                              NOT the bare nearest_attackable_target the hostiles use)
#     @5  NonTameRandomTargetGoal<Animal>(this, false, PREY_SELECTOR)      <-- DEFERRED (no prey mobs / PREY_SELECTOR)
#     @6  NonTameRandomTargetGoal<Turtle>(this, false, BABY_ON_LAND)       <-- DEFERRED (no Turtle mob)
#     @7  NearestAttackableTargetGoal<AbstractSkeleton>(this, false)       <-- kind="skeleton_target" (36-01 B2: the skeleton scan;
#                                                                              skeleton EXISTS from Phase 35 — IN SCOPE, not deferred)
#     @8  ResetUniversalAngerTargetGoal<Wolf>(this, true)                  <-- OMITTED (dead under the default UNIVERSAL_ANGER gamerule
#                                                                              FALSE; the per-mob anger is a gametime-endpoint that
#                                                                              expires automatically — W6 DISSOLVED, 36-JARNOTES.md:197-204,239-247)
#
# DEFERRED (cite-recorded, NEVER silently dropped — see the SUMMARY):
#   - WolfAvoidEntityGoal<Llama>@3: no Llama mob in v1 — the avoid-llama goal lands WITH the Llama mob
#     (it is an untamed-only flee gate against a target entity that does not exist). (36-JARNOTES.md:19,84,250.)
#   - BegGoal@9: the wolf head-tilt beg visual is a client-only display goal (an interested-flag + a
#     facing-a-food-holder scan) — no observable server-side behavior beyond the visual. (36-JARNOTES.md:25,99,250.)
#   - NonTameRandomTargetGoal<Animal>@5 + <Turtle>@6: the untamed prey-hunting goals need PREY_SELECTOR + the
#     prey/turtle target entities, none of which exist in v1 — they defer WITH those mobs. (36-JARNOTES.md:33-34,84,250.)
#   - ResetUniversalAngerTargetGoal@8: OMITTED — its canUse is UNIVERSAL_ANGER-gamerule (defaults FALSE) so it
#     NEVER fires under default gamerules, AND the gametime-endpoint anger model has no per-tick decrement for
#     it to reset (both dissolved). The PER-MOB anger timer (the wolf's own gametime endpoint) IS in scope —
#     isAngryAt expires automatically. (36-JARNOTES.md:197-204,239-247.)
#   - The TamableAnimalPanicGoal tamed/owner-guard refinement: TamableAnimalPanicGoal's tick override is a
#     minor water/away nudge over the base PanicGoal — v1 reuses the cow PanicGoal core at speed 1.5; the
#     tamed-specific guard is a cited-deferred refinement (no new RNG beyond PanicGoal's own). (36-JARNOTES.md:192-195.)
#
# Each PASSIVE goal callback receives (entity, world, nav) handles; the interpreter fires ONLY while a
# passive goal is RUNNING. The COMBAT/TAME/TARGET goals are Go-native (no callback). The draw ORDER below
# matches the bytecode EXACTLY (the per-entity seeded RandomSource — 24-01); the combat/tame lockstep RNG
# (the nextInt(10) acquire, the tame nextInt(3), the anger nextInt(381)) is owned + pinned by the Go-native
# goal tests (ai_goals_target_test.go + the Plan-D wolf tests), not re-expressed here.

# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start's cos/sin.

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f (the swim-jump chance)
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold: getEyeHeight()<0.4?0.0:0.4; a wolf's eye
                               # is well above 0.4 -> 0.4. FloatGoal.canUse compares entity.fluid_height
                               # (WATER) > this. In the DRY oracle in_water is false so canUse short-
                               # circuits before this comparison -> the value never gates a draw.
STROLL_INTERVAL = 120     # RandomStrollGoal.DEFAULT_INTERVAL (before Goal.reducedTickDelay halves it)
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(120) == Mth.positiveCeilDiv(120,2) == 60: canUse rolls
                               # nextInt(60), NOT nextInt(120) (the faithful gate)
STROLL_H = 10             # LandRandomPos/DefaultRandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7              # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (the 2-arg ctor
                               # default): getPosition rolls nextFloat() — >= probability (~99.9% COMMON)
                               # picks LandRandomPos (up-snap), < probability (~0.1% RARE) picks
                               # DefaultRandomPos (no up-snap). The DRAW is required for lockstep; the
                               # Go runtime snap is consumed-as-Land (always up-snaps) so the chosen
                               # branch does not change the committed target today.
PANIC_H = 5               # PanicGoal.findRandomPosition: DefaultRandomPos.getPos(mob, 5, 4) horizontal radius
PANIC_V = 4               # vertical radius (DefaultRandomPos.getPos(mob, 5, 4))
PANIC_SPEED = 1.5         # Wolf.registerGoals @1 TamableAnimal.TamableAnimalPanicGoal(this, 1.5, ...) — the
                               # tamable panic variant runs at speed 1.5 (vs the cow's 2.0). TamableAnimalPanicGoal
                               # EXTENDS PanicGoal; the want carries position only (the 1.5 multiplier is
                               # cited-deferred like the cow's 2.0), so this constant documents the jar speed.
LOOK_DIST = 8.0           # LookAtPlayerGoal lookDistance (Wolf: 8.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

BREED_RANGE = 8.0         # BreedGoal PARTNER_TARGETING: forNonCombat().range(8.0) — getFreePartner's
                               # inflate(8.0) scan radius (the host nearest_breeding_partner / try_breed bound).
BREED_LOVE_THRESHOLD = 60      # BreedGoal.tick breed gate: loveTime >= adjustedTickDelay(60) (60 @ 20 TPS).
BREED_DISTANCE_SQR = 9.0       # BreedGoal.tick breed gate: distanceToSqr(partner) < 9.0 (within 3 blocks).

# ============================================================================================
# @1  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true — COPIED VERBATIM from vanilla_cow
# ports net.minecraft.world.entity.ai.goal.FloatGoal (the SAME goal the cow/pig run — mob-agnostic)
# ============================================================================================
# canUse (bytecode): isInWater() && getFluidHeight(WATER) > getFluidJumpThreshold() || isInLava(). NO RNG
# (the only FloatGoal draw is in tick()). The strict `>` matches the bytecode (dcmpl; ifgt). Reads the
# frozen-scalar handle attrs entity.in_water / entity.fluid_height (WATER) / entity.in_lava. In the DRY
# oracle world in_water is false -> canUse false -> tick never runs -> zero new draws.
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

# tick (bytecode): if getRandom().nextFloat() < 0.8f -> getJumpControl().jump(). DRAW: the single new
# RNG in this subsystem, in tick() ONLY, drawn via entity.rand_float() (= e.ai.rng.nextFloat).
def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:   # DRAW: nextFloat()<0.8 (the swim-jump chance)
        nav.jump()

# ============================================================================================
# @1  TamableAnimal.TamableAnimalPanicGoal(mob, 1.5, ...)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.PanicGoal (TamableAnimalPanicGoal EXTENDS PanicGoal — the SAME
# goal the cow runs; only the speed differs, 1.5 vs the cow's 2.0). The tamed water/away tick refinement
# is cite-deferred (36-JARNOTES.md:192-195) — v1 reuses the PanicGoal core verbatim.
# ============================================================================================
# canUse (bytecode): if (!shouldPanic()) return false; (RETURN BEFORE ANY RNG); if (isOnFire())
# { lookForWater(...); } ; return findRandomPosition() == DefaultRandomPos.getPos(mob,5,4).
# shouldPanic() = getLastDamageSource() != null && getLastDamageSource().is(panic_causes) — read via
# entity.has_last_damage + entity.damage_in_tag("panic_causes"). The shouldPanic gate is the FIRST line
# so a dry/unhurt wolf draws ZERO RNG. When a panic DOES fire, findRandomPosition draws the UNCONDITIONAL
# 10-candidate DefaultRandomPos(5,4) loop: each candidate = generateRandomDirection in x, y, z ORDER
# (3 nextInt: nextInt(11)-5, nextInt(9)-4, nextInt(11)-5) = 30 nextInt total. Emit the 10 RAW candidates
# PLUS landMode=0.0 (DefaultRandomPos, no up-snap) as 31 FLAT positional floats via nav.path_to.
def panic_can_use(entity, world, nav):
    if not (entity.has_last_damage and entity.damage_in_tag("panic_causes")):   # shouldPanic: ZERO draws if false
        return False
    # (on-fire lookForWater branch: rare, RNG-free, currently a cited false-stub — matching the Go
    # isOnFire/lookForWater false-stubs; a non-burning wolf never enters it.)
    flat = []
    for _ in range(10):   # DefaultRandomPos.getPos: 10 unconditional candidates (RandomPos.generateRandomPos, NO break)
        xt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * PANIC_V + 1) - PANIC_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(0.0)   # 31st float: landMode = 0.0 (DefaultRandomPos, no up-snap)
    nav.path_to(*flat)   # 31 positional floats (x0,y0,z0,...,x9,y9,z9, landMode) -> setWantCandidates -> the snap
    return True

# stop (bytecode): isRunning = false. nav.stop() is that seam (clearWantTarget).
def panic_stop(entity, world, nav):
    nav.stop()

# canContinueToUse (bytecode): !navigation.isDone(). nav.has_path is that seam.
def panic_continue(entity, world, nav):
    return nav.has_path()

# ============================================================================================
# @7  BreedGoal(mob, 1.0)   flags {MOVE, LOOK}   — COPIED VERBATIM from vanilla_cow (Phase 33)
# ports net.minecraft.world.entity.ai.goal.BreedGoal (the SAME goal the cow/pig run — mob-agnostic).
# NOTE: the wolf's BreedGoal sits at @7 (vs the cow's @2) — the priority differs, the callbacks do not.
# ============================================================================================
# canUse (bytecode): if (!animal.isInLove()) return false; partner = getFreePartner(); return partner != null.
# The isInLove gate is the FIRST line — the un-fed lone-adult wolf (inLove==0) returns false HERE, before the
# partner scan, so the breed path (and its host RNG draws via try_breed) NEVER fires unless fed. is_in_love
# is the host read (Animal.isInLove); nearest_breeding_partner is the host scan (getFreePartner). start()
# resets loveTime=0. NO RNG drawn in the .star (the breed draws are host-side inside try_breed).
def breed_can_use(entity, world, nav):
    if not entity.is_in_love:   # Animal.isInLove() READ accessor (no call)
        return False
    p = entity.nearest_breeding_partner(BREED_RANGE)   # host scan = getFreePartner (filter stays Go-side)
    if p == None:
        return False
    entity.set_state("breed_px", p[0])
    entity.set_state("breed_py", p[1])
    entity.set_state("breed_pz", p[2])
    entity.set_state("breed_love_time", 0)   # BreedGoal.start: loveTime = 0 (fresh courting)
    return True

# tick (bytecode): lookAt(partner); navigation.moveTo(partner, speed); ++loveTime; if (loveTime >=
# adjustedTickDelay(60) && distanceToSqr(partner) < 9.0) breed(). The ++loveTime happens BEFORE the breed
# check. At the threshold within 3 blocks the .star calls try_breed(8.0) — the ONE host breed op
# (= TickLoop.breed) that draws the variant nextBoolean() FIRST then the XP 1+nextInt(7) SECOND. The
# .star draws NO breed RNG.
def breed_tick(entity, world, nav):
    px = entity.get_state("breed_px")
    py = entity.get_state("breed_py")
    pz = entity.get_state("breed_pz")
    entity.set_look_at(px, py, pz)   # BreedGoal.tick lookAt(partner)
    entity.move_to(px, py, pz)       # navigation.moveTo(partner, 1.0)
    lt = entity.get_state("breed_love_time") + 1   # ++loveTime (BEFORE the breed check)
    entity.set_state("breed_love_time", lt)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if lt >= BREED_LOVE_THRESHOLD and dx * dx + dy * dy + dz * dz < BREED_DISTANCE_SQR:
        entity.try_breed(BREED_RANGE)   # THE one host breed (= TickLoop.breed) — variant then XP

# stop (bytecode): partner = null; loveTime = 0.
def breed_stop(entity, world, nav):
    entity.set_state("breed_love_time", 0)
    nav.stop()   # clearWantTarget()

# canContinueToUse (bytecode): partner.isAlive() && partner.isInLove() && loveTime < 60 && !partner.isPanicking().
# The observable equivalent is a re-scan: nearest_breeding_partner still returns a tuple iff a same-class
# in-love non-panicking partner is still in range, paired with loveTime < 60.
def breed_continue(entity, world, nav):
    if entity.get_state("breed_love_time") >= BREED_LOVE_THRESHOLD:   # loveTime < 60 bound
        return False
    return entity.nearest_breeding_partner(BREED_RANGE) != None   # partner alive && isInLove && !isPanicking

# ============================================================================================
# @8  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}   — COPIED VERBATIM from vanilla_cow
# ports net.minecraft.world.entity.ai.goal.RandomStrollGoal (the SAME goal the cow/pig run)
# ============================================================================================
# canUse (WaterAvoidingRandomStrollGoal.getPosition, jar-verified): the 1-in-reducedTickDelay(interval)
# gate (DRAW 1: nextInt(60)), THEN — for a not-in-water wolf — the probability nextFloat() gate (DRAW 2:
# nextFloat() >= 0.001 picks LandRandomPos (~99.9% COMMON, up-snap), < 0.001 picks DefaultRandomPos
# (~0.1% RARE, no up-snap)), THEN the RandomPos.generateRandomPos UNCONDITIONAL 10-candidate loop: each
# candidate = generateRandomDirection in x, y, z ORDER (3 nextInt). 31 RNG draws total. Emit the 10 RAW
# candidates PLUS the wantLandMode flag as 31 FLAT positional floats via nav.path_to.
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(reducedTickDelay(120)=60) gate
        return False
    roll = entity.rand_float()   # DRAW 2: nextFloat() probability gate (WaterAvoidingRandomStrollGoal.getPosition)
    # wantLandMode: >= 0.001 => LandRandomPos (~99.9% COMMON, up-snap), < 0.001 => DefaultRandomPos (~0.1%
    # RARE, no up-snap). Pass it as the 31st float (1.0/0.0); the snap is consumed-as-Land today.
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

# canContinueToUse (bytecode): !navigation.isDone(). nav.has_path is that seam.
def stroll_continue(entity, world, nav):
    return nav.has_path()

# stop (bytecode): navigation.stop(). nav.stop() is that seam.
def stroll_stop(entity, world, nav):
    nav.stop()

# RandomStrollGoal.tick() is EMPTY in the jar (its behavior is start()=moveTo + continue=!isDone). The
# goal() builtin makes tick optional, so this goal declares NO tick.

# ============================================================================================
# @10  LookAtPlayerGoal(mob, Player.class, 8.0f)   flags {LOOK}   — COPIED from vanilla_cow (dist 8.0)
# ports net.minecraft.world.entity.ai.goal.LookAtPlayerGoal (the SAME goal the cow/pig run)
# ============================================================================================
# canUse (bytecode): getRandom().nextFloat() < probability (else false), then getNearestPlayer in
# lookDistance -> lookAt; return lookAt != null. The scan reads entity.y (NOT eye_y), matching the runtime.
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

# start (bytecode): lookTime = adjustedTickDelay(40 + getRandom().nextInt(40)). DRAW after the canUse find.
def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))

# canContinueToUse (bytecode): lookAt.isAlive() && mob.distanceToSqr(lookAt) <= lookDistance^2 &&
# lookTime > 0.
def look_continue(entity, world, nav):
    if entity.get_state("look_time") <= 0:
        return False
    dx = entity.get_state("look_x") - entity.x
    dy = entity.get_state("look_y") - entity.y
    dz = entity.get_state("look_z") - entity.z
    d2 = dx * dx + dy * dy + dz * dz
    return d2 <= LOOK_DIST * LOOK_DIST

# tick (bytecode): getLookControl().setLookAt(lookAt.getX(), lookAt.getEyeY(), lookAt.getZ()); then
# lookTime = lookTime - 1. Order: set look FIRST, THEN decrement.
def look_tick(entity, world, nav):
    entity.set_look_at(entity.get_state("look_x"), entity.get_state("look_y"), entity.get_state("look_z"))
    entity.set_state("look_time", entity.get_state("look_time") - 1)

# stop (bytecode): lookAt = null.
def look_stop(entity, world, nav):
    entity.set_state("look_time", 0)

# ============================================================================================
# @10  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
# ports net.minecraft.world.entity.ai.goal.RandomLookAroundGoal (the SAME goal the cow/pig run)
# ============================================================================================
# canUse (bytecode): getRandom().nextFloat() < 0.02f.
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

# start (bytecode): d = 6.283185307179586d * getRandom().nextDouble(); relX = cos(d); relZ = sin(d);
# lookTime = 20 + getRandom().nextInt(20). DRAW ORDER: the nextDouble heading FIRST, THEN nextInt(20).
def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()           # nextDouble() heading (DRAW 1)
    entity.set_state("rel_x", math.cos(d))      # relX = cos(d)
    entity.set_state("rel_z", math.sin(d))      # relZ = sin(d)
    entity.set_state("look_time", 20 + entity.rand_int(20))   # lookTime = 20 + nextInt(20) (DRAW 2)

# canContinueToUse (bytecode): lookTime >= 0.
def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

# tick (bytecode): lookTime = lookTime - 1; getLookControl().setLookAt(getX()+relX, getEyeY(),
# getZ()+relZ). Order: decrement FIRST, then aim.
def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "wolf" -> renders as entity.Wolf.ID (149, custom = BEHAVIOR, not a new wire type; the 36-01
# baseTypeByName resolver). Real Wolf attributes (Animal.createAnimalAttributes + Wolf overrides) seeded
# with the jar values (Wolf.createAttributes: MOVEMENT_SPEED 0.3, MAX_HEALTH 8.0, ATTACK_DAMAGE 4.0 —
# 36-JARNOTES.md:39-41). MAX_HEALTH 8.0 is the UNTAMED value; a tamed wolf bumps to 40.0 via setTame's
# applyTamingSideEffects (the host-side taming path, 36-02). ATTACK_DAMAGE 4.0 is the melee doHurtTarget
# damage. The combat goals (leap/melee/hurt_by/the target goals) are the Go-native 35-01/36-01 ports
# (kind=); the tame/owner/sit goals are the Go-native 36-01 ports (kind=); the passive goals
# (float/panic/stroll/look/around/breed) are .star callbacks copied verbatim from vanilla_cow. Cite
# net.minecraft.world.entity.animal.wolf.Wolf.registerGoals + Wolf.createAttributes.
declare_mob(
    name = "vanilla_wolf",
    base_type = "wolf",
    attributes = {
        "movement_speed": 0.3,   # Wolf.createAttributes: MOVEMENT_SPEED 0.3 (the float-widened 0.30000001192092896)
        "max_health": 8.0,       # Wolf.createAttributes: MAX_HEALTH 8.0 (untamed; tamed -> 40.0 via applyTamingSideEffects, 36-02)
        "attack_damage": 4.0,    # Wolf.createAttributes: ATTACK_DAMAGE 4.0 (the melee damage doHurtTarget deals)
    },
    goals = [
        # goalSelector — Wolf.registerGoals priorities EXACTLY (36-JARNOTES.md:13-27):
        # @1 FloatGoal [JUMP] — requiresUpdateEveryTick=true. canUse=fluid predicate (no RNG); tick=nextFloat()<0.8.
        # Cite Wolf.registerGoals @1 FloatGoal.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @1 TamableAnimal.TamableAnimalPanicGoal(mob, 1.5, ...) [MOVE] — the tamable PanicGoal variant @ speed
        # 1.5; reuses the cow PanicGoal core (TamableAnimalPanicGoal EXTENDS PanicGoal; the tamed water/away tick
        # refinement is cite-deferred, 36-JARNOTES.md:192-195). can_use commits the candidates (path_to), NO start
        # kwarg. Cite Wolf.registerGoals @1 TamableAnimal.TamableAnimalPanicGoal(1.5).
        goal(
            priority = 1,
            flags = ["MOVE"],
            can_use = panic_can_use,
            stop = panic_stop,
            can_continue = panic_continue,
        ),
        # @2 SitWhenOrderedToGoal(mob) [JUMP, MOVE] — kind="sit" (the 36-01 sitWhenOrderedToGoal): parks a
        # tamed/ordered wolf, NO RNG. The declared flags MUST agree with newSitWhenOrderedToGoal()'s {JUMP,MOVE}.
        # Cite Wolf.registerGoals @2 SitWhenOrderedToGoal.
        goal(priority = 2, flags = ["JUMP", "MOVE"], kind = "sit"),
        # @3 WolfAvoidEntityGoal<Llama> — DEFERRED (no Llama mob; defers WITH its target). Cite + OMIT.
        # @4 LeapAtTargetGoal(mob, 0.4) [JUMP, MOVE] — kind="leap_at_target" (the 35-01 leapAtTargetGoal): the
        # leap-toward-target impulse. Cite Wolf.registerGoals @4 LeapAtTargetGoal(0.4).
        goal(priority = 4, flags = ["JUMP", "MOVE"], kind = "leap_at_target"),
        # @5 MeleeAttackGoal(mob, 1.0, true) [MOVE] — kind="melee_attack" (the 35-01 meleeAttackGoal): chases
        # the target + fires doHurtTarget through the Phase-29 keystone (REAL ATTACK_DAMAGE 4.0).
        # Cite Wolf.registerGoals @5 MeleeAttackGoal(1.0, true).
        goal(priority = 5, flags = ["MOVE"], kind = "melee_attack"),
        # @6 FollowOwnerGoal(mob, 1.0, 10.0, 2.0) [MOVE] — kind="follow_owner" (the 36-01 followOwnerGoal): a
        # tamed wolf path-follows its owner (the teleport is cite-deferred; moveTo ships), NO RNG.
        # Cite Wolf.registerGoals @6 FollowOwnerGoal(1.0, 10.0, 2.0).
        goal(priority = 6, flags = ["MOVE"], kind = "follow_owner"),
        # @7 BreedGoal(mob, 1.0) [MOVE, LOOK] — .star (the cow breed_* reuse, Phase 33). can_use gates on
        # is_in_love; tick routes the breed through the ONE host try_breed. Cite Wolf.registerGoals @7 BreedGoal.
        goal(
            priority = 7,
            flags = ["MOVE", "LOOK"],
            can_use = breed_can_use,
            tick = breed_tick,
            stop = breed_stop,
            can_continue = breed_continue,
        ),
        # @8 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE] — .star (passive). can_use commits the candidates
        # (path_to), so NO start kwarg. Cite Wolf.registerGoals @8 WaterAvoidingRandomStrollGoal(1.0).
        goal(
            priority = 8,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @9 BegGoal(mob, 8.0) — DEFERRED (client head-tilt visual). Cite + OMIT.
        # @10 LookAtPlayerGoal(Player, 8.0) [LOOK] — .star (passive). Cite Wolf.registerGoals @10 LookAtPlayerGoal(Player, 8.0).
        goal(
            priority = 10,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @10 RandomLookAroundGoal [MOVE, LOOK] — .star (passive), requiresUpdateEveryTick=true.
        # Cite Wolf.registerGoals @10 RandomLookAroundGoal.
        goal(
            priority = 10,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),

        # targetSelector — Wolf.registerGoals priorities EXACTLY (36-JARNOTES.md:28-36):
        # @1 OwnerHurtByTargetGoal(mob) [TARGET] — kind="owner_hurt_by" (the 36-01 ownerHurtByTargetGoal): the
        # wolf targets whoever hit its owner, NO RNG. Cite Wolf.registerGoals targetSelector @1 OwnerHurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "owner_hurt_by"),
        # @2 OwnerHurtTargetGoal(mob) [TARGET] — kind="owner_hurt" (the 36-01 ownerHurtTargetGoal): the wolf
        # targets whoever the OWNER attacked (owner.getLastHurtMob), NO RNG. Cite Wolf.registerGoals targetSelector @2 OwnerHurtTargetGoal.
        goal(priority = 2, flags = ["TARGET"], kind = "owner_hurt"),
        # @3 HurtByTargetGoal(mob).setAlertOthers() [TARGET] — kind="hurt_by_target" (the 35-01 hurtByTargetGoal):
        # retaliate against the last attacker (NO RNG). The setAlertOthers pack-alert is cite-deferred (no alert
        # subsystem). Cite Wolf.registerGoals targetSelector @3 HurtByTargetGoal.
        goal(priority = 3, flags = ["TARGET"], kind = "hurt_by_target"),
        # @4 NearestAttackableTargetGoal<Player>(mob, 10, true, false, this::isAngryAt) [TARGET] —
        # kind="angry_player_target" (the 36-01 B1 anger-gated PLAYER goal): a wild un-hit wolf does NOT aggro
        # players; the isAngryAt gate (the wolf's gametime-endpoint anger) opens it only after a player hit. This
        # is a DISTINCT kind, NOT the bare nearest_attackable_target (which the hostiles use, un-gated — the wolf
        # does NOT use it). Cite Wolf.registerGoals targetSelector @4 NearestAttackableTargetGoal<Player>(isAngryAt).
        goal(priority = 4, flags = ["TARGET"], kind = "angry_player_target"),
        # @5 NonTameRandomTargetGoal<Animal>(PREY_SELECTOR) — DEFERRED (no prey mobs / PREY_SELECTOR). Cite + OMIT.
        # @6 NonTameRandomTargetGoal<Turtle>(BABY_ON_LAND) — DEFERRED (no Turtle mob). Cite + OMIT.
        # @7 NearestAttackableTargetGoal<AbstractSkeleton>(mob, false) [TARGET] — kind="skeleton_target" (the
        # 36-01 B2 skeleton scan): wolves attack skeletons on sight (NO anger gate); findTarget scans
        # entity.Skeleton.ID within FOLLOW_RANGE. The skeleton EXISTS from Phase 35 — IN SCOPE, NOT deferred.
        # Cite Wolf.registerGoals targetSelector @7 NearestAttackableTargetGoal<AbstractSkeleton>.
        goal(priority = 7, flags = ["TARGET"], kind = "skeleton_target"),
        # @8 ResetUniversalAngerTargetGoal<Wolf>(mob, true) — OMITTED (dead under the default UNIVERSAL_ANGER
        # gamerule FALSE; the gametime-endpoint anger expires automatically with no per-tick decrement to reset —
        # W6 DISSOLVED, 36-JARNOTES.md:197-204,239-247). Cite + OMIT.
    ],
)
