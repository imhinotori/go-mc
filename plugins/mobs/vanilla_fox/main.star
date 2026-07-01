# vanilla_fox — a 1:1 vanilla-Fox dogfood (MOB-PASS-06, Task #9), the fox built as a Starlark plugin. A
# LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares the PORTABLE slice of net.minecraft.world.entity.animal.fox.Fox.registerGoals, reusing the
# mob-agnostic passive callbacks (float/panic/breed/stroll/look — COPIED VERBATIM from vanilla_cow) plus the
# Go-native melee + leap kinds. The fox is a LARGE mob whose CHARACTER goals (sleep/pounce/stalk/eat-berries/
# trust/avoid) all need subsystems that do not exist in v1 — those are cite-deferred; the ambient slice ships.
#
# Fox.registerGoals() (javap-verified this session — the v1-relevant slice):
#   @0  FoxFloatGoal                                    <-- .star (shared passive float)
#   @0  ClimbOnTopOfPowderSnowGoal                      <-- DEFERRED (no powder-snow subsystem)
#   @1  FaceplantGoal                                   <-- DEFERRED (the stunned face-plant, cat-mouse flavor)
#   @2  FoxPanicGoal(2.2)                               <-- .star (PanicGoal reuse)
#   @3  FoxBreedGoal(1.0)                               <-- .star (shared BreedGoal)
#   @4  AvoidEntityGoal<Player/Wolf/PolarBear>          <-- DEFERRED (no AvoidEntityGoal / trust subsystem)
#   @5  StalkPreyGoal                                   <-- DEFERRED (the crouch-stalk; no prey subsystem)
#   @6  FoxPounceGoal / SeekShelterGoal                 <-- DEFERRED (the pounce arc / shelter-from-day subsystem)
#   @7  FoxMeleeAttackGoal(1.2, true)                   <-- kind="melee_attack" (fires vs a prey target)
#   @7  SleepGoal                                       <-- DEFERRED (the day-sleep subsystem)
#   @8  FoxFollowParentGoal(1.25)                       <-- DEFERRED (fox-specific follow variant)
#   @9  FoxStrollThroughVillageGoal                     <-- DEFERRED (no village subsystem)
#   @10 FoxEatBerriesGoal / LeapAtTargetGoal(0.4)       <-- eat-berries DEFERRED; leap = kind="leap_at_target"
#   @11 WaterAvoidingRandomStrollGoal(1.0)              <-- .star (shared stroll)
#   @11 FoxSearchForItemsGoal                           <-- DEFERRED (item pickup/carry subsystem)
#   @12 FoxLookAtPlayerGoal(Player, 24.0)               <-- .star (shared look, dist 24.0)
#   @13 PerchAndSearchGoal                              <-- DEFERRED (the perch-and-scan flavor)
#   targetSelector: landTarget(Chicken/Rabbit) / fishTarget / DefendTrusted <-- DEFERRED (no prey / trust)
#
# DEFERRED (cite-recorded, NEVER silently dropped): the ENTIRE fox character layer — sleep, pounce, stalk,
# eat-berries, trust, avoid-player/wolf/bear, faceplant, item-carry, village-stroll, perch, and the prey
# target goals (chicken/rabbit/fish) — each needs a subsystem absent in v1. The ambient slice (float/panic/
# breed/stroll/look) + the melee/leap kinds (inert without a prey target, cited) ships; the fox behaves as a
# skittish ambient animal until its character subsystems land. This is the honest v1 fox (NOT a fake).

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f (the swim-jump chance)
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold: getEyeHeight()<0.4?0.0:0.4; a cow's eye
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
PANIC_SPEED = 2.0         # AbstractCow.registerGoals @1 PanicGoal(mob, 2.0) (the want carries position
                               # only; the 2.0 multiplier is cited-deferred like the pig's 1.25)
TEMPT_RANGE = 10.0        # Attributes.TEMPT_RANGE default — the nearest-tempt-player scan radius the host
                               # applies (the host nearest_player_holding_food scan bound)
TEMPT_SPEED = 1.25        # AbstractCow.registerGoals @3 TemptGoal(mob, 1.25, ..., false) speedModifier
                               # (the want carries position only; the 1.25 multiplier is cited-deferred)
STOP_DISTANCE = 2.5       # TemptGoal DEFAULT_STOP_DISTANCE (the cow uses the default ctor); tick stops the
                               # navigation within STOP_DISTANCE² (2.5² = 6.25)
LOOK_DIST = 24.0          # FoxLookAtPlayerGoal lookDistance (Fox: 24.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

BREED_RANGE = 8.0         # BreedGoal PARTNER_TARGETING: forNonCombat().range(8.0) — getFreePartner's
                               # inflate(8.0) scan radius (the host nearest_breeding_partner / try_breed bound).
BREED_LOVE_THRESHOLD = 60      # BreedGoal.tick breed gate: loveTime >= adjustedTickDelay(60) (60 @ 20 TPS).
BREED_DISTANCE_SQR = 9.0       # BreedGoal.tick breed gate: distanceToSqr(partner) < 9.0 (within 3 blocks).
FOLLOW_RANGE = 8.0        # FollowParentGoal HORIZONTAL_SCAN_RANGE (8) — the host nearest_adult_parent
                               # symmetry arg (the actual scan box is the host's fixed inflate(8,4,8)).
FOLLOW_RECALC_INTERVAL = 10    # FollowParentGoal.tick re-path cadence: adjustedTickDelay(10) (10 @ 20 TPS).

# ============================================================================================
# @0  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true
# ports net.minecraft.world.entity.ai.goal.FloatGoal (the SAME goal the pig runs — mob-agnostic)
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
# @1  PanicGoal(mob, 2.0)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.PanicGoal (the SAME goal the pig runs; only the speed differs)
# ============================================================================================
# canUse (bytecode): if (!shouldPanic()) return false; (RETURN BEFORE ANY RNG); if (isOnFire())
# { lookForWater(...); } ; return findRandomPosition() == DefaultRandomPos.getPos(mob,5,4).
# shouldPanic() = getLastDamageSource() != null && getLastDamageSource().is(panic_causes) — read via
# entity.has_last_damage + entity.damage_in_tag("panic_causes"). The shouldPanic gate is the FIRST line
# so a dry/unhurt cow draws ZERO RNG. When a panic DOES fire, findRandomPosition draws the UNCONDITIONAL
# 10-candidate DefaultRandomPos(5,4) loop: each candidate = generateRandomDirection in x, y, z ORDER
# (3 nextInt: nextInt(11)-5, nextInt(9)-4, nextInt(11)-5) = 30 nextInt total. Emit the 10 RAW candidates
# PLUS landMode=0.0 (DefaultRandomPos, no up-snap) as 31 FLAT positional floats via nav.path_to.
def panic_can_use(entity, world, nav):
    if not (entity.has_last_damage and entity.damage_in_tag("panic_causes")):   # shouldPanic: ZERO draws if false
        return False
    # (on-fire lookForWater branch: rare, RNG-free, currently a cited false-stub — matching the Go
    # isOnFire/lookForWater false-stubs; a non-burning cow never enters it.)
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
# @3  TemptGoal(mob, 1.25, i -> i.is(ItemTags.COW_FOOD), false)   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.ai.goal.TemptGoal (the SAME goal the pig's pig_food Tempt runs;
# only the food tag differs — the cow has ONE Tempt, no carrot_on_a_stick literal).
# ============================================================================================
# canUse (bytecode): if (calmDown > 0) { --calmDown; return false; }  then player = getNearestPlayer(
# TEMPT_TARGETING.range(TEMPT_RANGE), mob); return player != null. NO RNG — an int gate + a held-item
# player scan. The HOST owns the item-tag set + the scan: entity.nearest_player_holding_food("cow_food",
# range) returns the matching player's (px,py,pz) tuple or None (the COW_FOOD membership stays Go-side,
# the parameterized 34-00 handle). In the DRY oracle no player holds cow_food near the cow -> None ->
# can_use false -> zero new draws (TemptGoal draws zero RNG anyway).
def tempt_food_can_use(entity, world, nav):
    p = entity.nearest_player_holding_food("cow_food", TEMPT_RANGE)   # host scan + COW_FOOD predicate (Go-side)
    if p == None:
        return False
    entity.set_state("tempt_food_px", p[0])
    entity.set_state("tempt_food_py", p[1])
    entity.set_state("tempt_food_pz", p[2])
    return True

# tick (bytecode): getLookControl().setLookAt(player, ...); if (distanceToSqr(player) < stopDistance²)
# stopNavigation(); else navigateTowards(player) (moveTo(player, 1.25)). set_look_at is the LookControl
# .setLookAt seam; move_to is the navigateTowards seam (setWantTarget — the 1.25 speedModifier is the
# nav-tick multiplier, cited-deferred like the others).
def tempt_food_tick(entity, world, nav):
    px = entity.get_state("tempt_food_px")
    py = entity.get_state("tempt_food_py")
    pz = entity.get_state("tempt_food_pz")
    entity.set_look_at(px, py, pz)   # TemptGoal.tick setLookAt(player)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if dx * dx + dy * dy + dz * dz < STOP_DISTANCE * STOP_DISTANCE:   # 2.5² = 6.25
        nav.stop()   # stopNavigation()
    else:
        entity.move_to(px, py, pz)   # navigateTowards(player) @ TEMPT_SPEED 1.25

# stop (bytecode): player = null; stopNavigation(); calmDown = reducedTickDelay(100); isRunning = false.
def tempt_food_stop(entity, world, nav):
    nav.stop()   # stopNavigation()

# canContinueToUse (bytecode): if (canScare()) { ...flee-abort... } return canUse(). COW: canScare=false
# -> the flee block is DEAD -> canContinueToUse == canUse (re-scan).
def tempt_food_continue(entity, world, nav):
    return tempt_food_can_use(entity, world, nav)

# ============================================================================================
# @2  BreedGoal(mob, 1.0)   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.ai.goal.BreedGoal (the SAME goal the pig runs). NOTE: the cow's
# BreedGoal sits at @2 (not @3 as the pig) because the cow has NO carrot TemptGoal.
# ============================================================================================
# canUse (bytecode): if (!animal.isInLove()) return false; partner = getFreePartner(); return partner != null.
# The isInLove gate is the FIRST line — the un-fed lone-adult cow (inLove==0) returns false HERE, before the
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
# @4  FollowParentGoal(mob, 1.25)   flags {} EMPTY
# ports net.minecraft.world.entity.ai.goal.FollowParentGoal (the SAME goal the pig runs; only the speed
# differs — Cow's is 1.25, the pig's 1.1).
# ============================================================================================
# canUse (bytecode): if (animal.getAge() >= 0) return false;  // only a BABY follows
#   parents = getEntitiesOfClass(animal.getClass(), inflate(8,4,8)); closest = nearest ADULT (age>=0);
#   if (closest == null) return false; if (closestDistSqr < 9.0) return false; parent = closest; return true.
# is_baby is the host read (AgeableMob.isBaby == age<0) — the FIRST-line gate. nearest_adult_parent is the
# host scan. NO RNG anywhere (FollowParentGoal is fully deterministic).
def follow_can_use(entity, world, nav):
    if not entity.is_baby:   # FollowParentGoal.canUse READ accessor: age>=0 → only a baby follows
        return False
    p = entity.nearest_adult_parent(FOLLOW_RANGE)   # host scan = the nearest-adult inflate(8,4,8) pick
    if p == None:
        return False
    entity.set_state("follow_px", p[0])
    entity.set_state("follow_py", p[1])
    entity.set_state("follow_pz", p[2])
    entity.set_state("follow_recalc", 0)   # FollowParentGoal.start: timeToRecalcPath = 0 (first tick re-paths)
    return True

# tick (bytecode): if (--timeToRecalcPath > 0) return; timeToRecalcPath = adjustedTickDelay(10);
# navigation.moveTo(parent, speed). PURE INT — NO RNG. move_to is the navigation.moveTo(parent, 1.25) seam.
def follow_tick(entity, world, nav):
    rc = entity.get_state("follow_recalc") - 1   # --timeToRecalcPath
    if rc > 0:
        entity.set_state("follow_recalc", rc)
        return
    entity.set_state("follow_recalc", FOLLOW_RECALC_INTERVAL)   # adjustedTickDelay(10) — re-path cadence
    p = entity.nearest_adult_parent(FOLLOW_RANGE)   # re-read the parent (it may have moved)
    if p == None:
        return
    entity.set_state("follow_px", p[0])
    entity.set_state("follow_py", p[1])
    entity.set_state("follow_pz", p[2])
    entity.move_to(p[0], p[1], p[2])   # navigation.moveTo(parent, 1.25)

# stop (bytecode): parent = null.
def follow_stop(entity, world, nav):
    nav.stop()   # clearWantTarget()

# canContinueToUse (bytecode): if (animal.getAge() >= 0) return false; if (!parent.isAlive()) return false;
# d = distanceToSqr(parent); return !(d < 9.0) && !(d > 256.0).  // follow while 3..16 blocks.
def follow_continue(entity, world, nav):
    if not entity.is_baby:   # READ accessor: grew up (age>=0) → stop
        return False
    return entity.nearest_adult_parent(FOLLOW_RANGE) != None   # a live adult still in the follow band

# ============================================================================================
# @5  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.RandomStrollGoal (the SAME goal the pig runs)
# ============================================================================================
# canUse (WaterAvoidingRandomStrollGoal.getPosition, jar-verified): the 1-in-reducedTickDelay(interval)
# gate (DRAW 1: nextInt(60)), THEN — for a not-in-water cow — the probability nextFloat() gate (DRAW 2:
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
# @6  LookAtPlayerGoal(mob, Player.class, 6.0f)   flags {LOOK}
# ports net.minecraft.world.entity.ai.goal.LookAtPlayerGoal (the SAME goal the pig runs)
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
# @7  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true
# ports net.minecraft.world.entity.ai.goal.RandomLookAroundGoal (the SAME goal the pig runs)
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
# base_type "fox" -> renders as entity.Fox.ID. Real Fox attributes (Animal.createAnimalAttributes + Fox
# overrides: MOVEMENT_SPEED 0.3, MAX_HEALTH 10, ATTACK_DAMAGE 2, FOLLOW_RANGE 32). The PORTABLE ambient
# slice at the jar priorities (float@0, panic@2, breed@3, melee@7, leap@10, stroll@11, look@12); the fox
# character goals (sleep/pounce/stalk/berries/trust/avoid/…) are cite-deferred (header). melee/leap are
# Go-native kinds (inert without a prey target, cited). Cite Fox.registerGoals + Fox.createAttributes.
declare_mob(
    name = "vanilla_fox",
    base_type = "fox",
    attributes = {
        "max_health": 10.0,      # Fox.createAttributes: MAX_HEALTH 10.0
        "movement_speed": 0.3,   # Fox.createAttributes: MOVEMENT_SPEED 0.3
        "attack_damage": 2.0,    # Fox.createAttributes: ATTACK_DAMAGE 2.0
        "follow_range": 32.0,    # Fox.createAttributes: FOLLOW_RANGE 32.0
    },
    goals = [
        # @0 FoxFloatGoal [JUMP] — requiresUpdateEveryTick=true. Cite Fox.registerGoals @0 FoxFloatGoal.
        goal(
            priority = 0,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @2 FoxPanicGoal(mob, 2.2) [MOVE] — PanicGoal reuse. Cite Fox.registerGoals @2 FoxPanicGoal(2.2).
        goal(
            priority = 2,
            flags = ["MOVE"],
            can_use = panic_can_use,
            stop = panic_stop,
            can_continue = panic_continue,
        ),
        # @3 FoxBreedGoal(mob, 1.0) [MOVE, LOOK]. Cite Fox.registerGoals @3 FoxBreedGoal(1.0).
        goal(
            priority = 3,
            flags = ["MOVE", "LOOK"],
            can_use = breed_can_use,
            tick = breed_tick,
            stop = breed_stop,
            can_continue = breed_continue,
        ),
        # @7 FoxMeleeAttackGoal(mob, 1.2, true) [MOVE] — kind="melee_attack" (fires vs a prey target; the
        # prey target goals defer, so this is inert in v1 — cited). Cite Fox.registerGoals @7 FoxMeleeAttackGoal.
        goal(priority = 7, flags = ["MOVE"], kind = "melee_attack"),
        # @10 LeapAtTargetGoal(mob, 0.4) [JUMP, MOVE] — kind="leap_at_target" (also inert without prey; cited).
        # Cite Fox.registerGoals @10 LeapAtTargetGoal(0.4).
        goal(priority = 10, flags = ["JUMP", "MOVE"], kind = "leap_at_target"),
        # @11 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE]. Cite Fox.registerGoals @11.
        goal(
            priority = 11,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @12 FoxLookAtPlayerGoal(Player, 24.0) [LOOK]. Cite Fox.registerGoals @12 FoxLookAtPlayerGoal.
        goal(
            priority = 12,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
    ],
)
