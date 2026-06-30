# vanilla_sheep — the SECOND 1:1 vanilla-mob dogfood (MOB-PASS-02). The vanilla Sheep's PASSIVE-AMBIENT
# AI re-expressed AS a Starlark plugin, staying a LITERAL method-for-method port of the unobfuscated
# 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It declares the SAME 9 passive goals
# net.minecraft.world.entity.animal.sheep.Sheep.registerGoals builds, at the EXACT priorities + flags,
# each consuming the Plan 24-01 handle seams (set_look_at, nearest_player, rand_int/rand_float, the
# per-goal get_state/set_state scratch) + the Plan 07 nav seam (path_to/has_path) + the Phase-34
# host eat seam (entity.eat_grass_block / entity.eat_broadcast_byte10 from 34-00).
#
# Sheep.registerGoals() (javap-verified, 34-JARNOTES.md:21-32): @0 FloatGoal, @1 PanicGoal(1.25),
# @2 BreedGoal(1.0), @3 TemptGoal(1.1, is(SHEEP_FOOD), false), @4 FollowParentGoal(1.1),
# @5 this.eatBlockGoal (EatBlockGoal — the NEW goal: eat a grass block -> regrow wool),
# @6 WaterAvoidingRandomStrollGoal(1.0), @7 LookAtPlayerGoal(Player, 6.0f), @8 RandomLookAroundGoal.
#
# The shared goals (@0/@1/@2/@3/@4/@6/@7/@8) are the SAME bytecode-faithful callbacks the pig ports
# (mob-agnostic — they read entity.* handles); only the constants + the food tag + the NEW EatBlockGoal
# differ. The sheep has ONE Tempt (sheep_food), not the pig's carrot+pig_food pair. The EatBlockGoal RNG
# gate (the single nextInt) is drawn PLUGIN-side via entity.rand_int (mobRandom, lockstep) and the actual
# block eat + wool/age mutation goes through the 34-00 host seam — the .star NEVER reads blocks nor shears
# (shearing is a HOST interact, trySheepShear, wired into handleInteract in 34-00).
#
# Each goal callback receives (entity, world, nav) handles. The interpreter fires ONLY while a goal is
# RUNNING (an idle sheep makes zero starlark.Calls per tick). The draw ORDER below matches the bytecode
# EXACTLY (the per-entity seeded RandomSource — 24-01 — makes a fixed seed reproduce the identical stream).

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f (the swim-jump chance)
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold — sheep eye >= 0.4 -> 0.4. FloatGoal.canUse
                               # compares entity.fluid_height (WATER) > this. In the DRY oracle in_water is
                               # false so canUse short-circuits before this comparison -> the value never
                               # gates a draw.
STROLL_INTERVAL = 120     # RandomStrollGoal.DEFAULT_INTERVAL (before Goal.reducedTickDelay halves it)
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(120) == Mth.positiveCeilDiv(120,2) == 60: canUse rolls
                               # nextInt(60), NOT nextInt(120) (the faithful gate)
STROLL_H = 10             # LandRandomPos/DefaultRandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7              # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (the 2-arg ctor
                               # default): getPosition rolls nextFloat() — >= probability (~99.9% COMMON)
                               # picks LandRandomPos (up-snap), < probability (~0.1% RARE) picks
                               # DefaultRandomPos (no up-snap). The DRAW is required for lockstep; the
                               # Go runtime snap is consumed-as-Land (always up-snaps).
PANIC_H = 5               # PanicGoal.findRandomPosition: DefaultRandomPos.getPos(mob, 5, 4) horizontal radius
PANIC_V = 4               # vertical radius (DefaultRandomPos.getPos(mob, 5, 4))
PANIC_SPEED = 1.25        # Sheep.registerGoals @1 PanicGoal(mob, 1.25) (the want carries position only; the
                               # 1.25 multiplier is cited-deferred like stroll's speedModifier)
TEMPT_RANGE = 10.0        # Attributes.TEMPT_RANGE default — the nearest-tempt-player scan radius the host applies.
TEMPT_SPEED = 1.1         # Sheep.registerGoals @3 TemptGoal(mob, 1.1, is(SHEEP_FOOD), false) speedModifier
                               # (the want carries position only; the 1.1 multiplier is cited-deferred)
STOP_DISTANCE = 2.5       # TemptGoal DEFAULT_STOP_DISTANCE (the sheep uses the default ctor); tick stops the
                               # navigation within STOP_DISTANCE² (2.5² = 6.25)
LOOK_DIST = 6.0           # LookAtPlayerGoal lookDistance (Sheep: 6.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

BREED_RANGE = 8.0         # BreedGoal PARTNER_TARGETING: forNonCombat().range(8.0) — getFreePartner's
                               # inflate(8.0) scan radius (the host nearest_breeding_partner / try_breed bound).
BREED_LOVE_THRESHOLD = 60      # BreedGoal.tick breed gate: loveTime >= adjustedTickDelay(60) (60 @ 20 TPS).
BREED_DISTANCE_SQR = 9.0       # BreedGoal.tick breed gate: distanceToSqr(partner) < 9.0 (within 3 blocks).
FOLLOW_RANGE = 8.0        # FollowParentGoal HORIZONTAL_SCAN_RANGE (8) — the host nearest_adult_parent arg.
FOLLOW_RECALC_INTERVAL = 10    # FollowParentGoal.tick re-path cadence: adjustedTickDelay(10) (10 @ 20 TPS).

# EatBlockGoal constants (net.minecraft.world.entity.ai.goal.EatBlockGoal, 34-JARNOTES.md:149-176,198-221).
# adjustedTickDelay is IDENTITY at our every-tick driver (34-JARNOTES.md:198-221, RE-VERIFIED — no ceilDiv):
# the goalSelector runs canUse every tick here (no (tickCount+id)%2 decimation), so the FULL bound is the
# faithful compensation. The shared Go constants (ai_goals_eat.go) are eatAnimationTicks=40, eatActTick=4,
# eatGateBoundAdult=1000, eatGateBoundBaby=50 — mirrored here as the .star's literal source of truth.
EAT_ANIM_TICKS = 40       # EatBlockGoal.EAT_ANIMATION_TICKS — start() arms eatAnimationTick = adjustedTickDelay(40) = 40
EAT_GATE_ADULT = 1000     # canUse gate: nextInt(adjustedTickDelay(1000)) = nextInt(1000) for an adult
EAT_GATE_BABY = 50        # canUse gate: nextInt(adjustedTickDelay(50)) = nextInt(50) for a baby (eats far more often)
EAT_ACT_TICK = 4          # tick() acts ONLY when eatAnimationTick == adjustedTickDelay(4) = 4 (36 ticks in)

# ============================================================================================
# @0  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true
# ports net.minecraft.world.entity.ai.goal.FloatGoal
# ============================================================================================
# canUse (bytecode): isInWater() && getFluidHeight(WATER) > getFluidJumpThreshold() || isInLava(). NO RNG
# (the only FloatGoal draw is in tick()). In the DRY oracle world in_water is false -> canUse false.
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

# tick (bytecode): if getRandom().nextFloat() < 0.8f -> getJumpControl().jump(). DRAW 1 (the swim-jump).
def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:   # DRAW 1: nextFloat()<0.8 (the swim-jump chance)
        nav.jump()

# ============================================================================================
# @1  PanicGoal(mob, 1.25)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.PanicGoal
# ============================================================================================
# canUse (bytecode): if (!shouldPanic()) return false; (RETURN BEFORE ANY RNG); if (isOnFire()) {...} ;
# return findRandomPosition() == DefaultRandomPos.getPos(mob,5,4). shouldPanic gate is the FIRST line so a
# dry/unhurt sheep draws ZERO RNG. When a panic DOES fire, findRandomPosition draws the UNCONDITIONAL
# 10-candidate DefaultRandomPos(5,4) loop: each candidate = generateRandomDirection in x, y, z ORDER (3
# nextInt: nextInt(11)-5, nextInt(9)-4, nextInt(11)-5) = 30 nextInt. Emit the 10 RAW candidates PLUS
# landMode=0.0 (DefaultRandomPos, no up-snap) as 31 FLAT positional floats via nav.path_to.
def panic_can_use(entity, world, nav):
    if not (entity.has_last_damage and entity.damage_in_tag("panic_causes")):   # shouldPanic: ZERO draws if false
        return False
    flat = []
    for _ in range(10):   # DefaultRandomPos.getPos: 10 unconditional candidates (NO break)
        xt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * PANIC_V + 1) - PANIC_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(0.0)   # 31st float: landMode = 0.0 (DefaultRandomPos, no up-snap)
    nav.path_to(*flat)   # 31 positional floats -> setWantCandidates -> the snap
    return True

# stop (bytecode): isRunning = false. nav.stop() is that seam.
def panic_stop(entity, world, nav):
    nav.stop()

# canContinueToUse (bytecode): !navigation.isDone(). nav.has_path is that seam.
def panic_continue(entity, world, nav):
    return nav.has_path()

# ============================================================================================
# @2  BreedGoal(mob, 1.0)   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.ai.goal.BreedGoal
# ============================================================================================
# canUse (bytecode): if (!animal.isInLove()) return false; partner = getFreePartner(); return partner != null.
# The isInLove gate is the FIRST line — the un-fed lone-adult sheep (inLove==0) returns false HERE, before
# the partner scan, so the breed path (and its host RNG draws via try_breed) NEVER fires. NO RNG in the .star
# (the breed draws are host-side inside try_breed = TickLoop.breed).
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
# check. At the threshold within 3 blocks the .star calls try_breed(8.0) — the ONE host breed op (variant
# nextBoolean() FIRST then XP 1+nextInt(7)). The .star draws NO breed RNG.
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
def breed_continue(entity, world, nav):
    if entity.get_state("breed_love_time") >= BREED_LOVE_THRESHOLD:   # loveTime < 60 bound
        return False
    return entity.nearest_breeding_partner(BREED_RANGE) != None   # partner alive && isInLove && !isPanicking

# ============================================================================================
# @3  TemptGoal(mob, 1.1, i -> i.is(ItemTags.SHEEP_FOOD), false)   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.ai.goal.TemptGoal — the sheep's SINGLE tempt (sheep_food)
# ============================================================================================
# canUse (bytecode): if (calmDown > 0) { --calmDown; return false; }  then player = getNearestPlayer(
# TEMPT_TARGETING.range(TEMPT_RANGE), mob); return player != null. NO RNG — an int gate + a held-item
# player scan. The HOST owns the item-tag set + the scan: entity.nearest_player_holding_food("sheep_food",
# range) returns the matching player's (px,py,pz) tuple or None (the SHEEP_FOOD tag stays Go-side).
def tempt_sheepfood_can_use(entity, world, nav):
    p = entity.nearest_player_holding_food("sheep_food", TEMPT_RANGE)   # host scan + predicate (ItemTags.SHEEP_FOOD Go-side)
    if p == None:
        return False
    entity.set_state("tempt_s_px", p[0])
    entity.set_state("tempt_s_py", p[1])
    entity.set_state("tempt_s_pz", p[2])
    return True

# tick (bytecode): getLookControl().setLookAt(player, ...); if (distanceToSqr(player) < stopDistance²)
# stopNavigation(); else navigateTowards(player) (moveTo(player, 1.1)).
def tempt_sheepfood_tick(entity, world, nav):
    px = entity.get_state("tempt_s_px")
    py = entity.get_state("tempt_s_py")
    pz = entity.get_state("tempt_s_pz")
    entity.set_look_at(px, py, pz)   # TemptGoal.tick setLookAt(player)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if dx * dx + dy * dy + dz * dz < STOP_DISTANCE * STOP_DISTANCE:   # 2.5² = 6.25
        nav.stop()   # stopNavigation()
    else:
        entity.move_to(px, py, pz)   # navigateTowards(player) @ TEMPT_SPEED 1.1

# stop (bytecode): player = null; stopNavigation(); calmDown = reducedTickDelay(100); isRunning = false.
def tempt_sheepfood_stop(entity, world, nav):
    nav.stop()   # stopNavigation()

# canContinueToUse (bytecode): if (canScare()) { ...flee-abort... } return canUse(). SHEEP: canScare=false
# -> the flee block is DEAD -> canContinueToUse == canUse (re-scan).
def tempt_sheepfood_continue(entity, world, nav):
    return tempt_sheepfood_can_use(entity, world, nav)

# ============================================================================================
# @4  FollowParentGoal(mob, 1.1)   flags {} EMPTY
# ports net.minecraft.world.entity.ai.goal.FollowParentGoal
# ============================================================================================
# canUse (bytecode): if (animal.getAge() >= 0) return false;  // only a BABY follows
#   parents = getEntitiesOfClass(animal.getClass(), inflate(8,4,8)); closest = nearest ADULT (age>=0);
#   if (closest == null) return false; if (closestDistSqr < 9.0) return false; parent = closest; return true.
# is_baby is the FIRST-line gate (the lone-ADULT sheep returns false HERE). NO RNG anywhere.
def follow_can_use(entity, world, nav):
    if not entity.is_baby:   # FollowParentGoal.canUse READ accessor (no call): age>=0 → only a baby follows
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
# navigation.moveTo(parent, speed). PURE INT — NO RNG.
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
    entity.move_to(p[0], p[1], p[2])   # navigation.moveTo(parent, 1.1)

# stop (bytecode): parent = null.
def follow_stop(entity, world, nav):
    nav.stop()   # clearWantTarget()

# canContinueToUse (bytecode): if (animal.getAge() >= 0) return false; if (!parent.isAlive()) return false;
# d = distanceToSqr(parent); return !(d < 9.0) && !(d > 256.0).  // follow while 3..16 blocks.
def follow_continue(entity, world, nav):
    if not entity.is_baby:   # READ accessor (no call): grew up (age>=0) → stop
        return False
    return entity.nearest_adult_parent(FOLLOW_RANGE) != None   # a live adult still in the follow band

# ============================================================================================
# @5  EatBlockGoal(mob)   flags {MOVE, LOOK, JUMP}   <-- THE NEW GOAL
# ports net.minecraft.world.entity.ai.goal.EatBlockGoal — eat a grass block -> regrow wool + baby ageUp.
# VERBATIM bytecode (34-JARNOTES.md:149-176): the single nextInt canUse gate is drawn PLUGIN-side via
# entity.rand_int (mobRandom — lockstep); the block eat + wool/age mutation goes through the 34-00 host
# seam (entity.eat_grass_block / entity.eat_broadcast_byte10). The eatAnimationTick timer lives in the
# per-goal scratch (set_state/get_state — int fits). adjustedTickDelay is IDENTITY (no ceilDiv).
# ============================================================================================
# canUse (bytecode): if (random.nextInt(adjustedTickDelay(isBaby? 50 : 1000)) != 0) return false;  // ONE nextInt
#   pos = blockPosition(); if (IS_EDIBLE.test(...)) return true;          // tall-grass/fern (CITE-DEFERRED, 34-00)
#   return getBlockState(pos.below()).is(GRASS_BLOCK);                    // OR grass_block below (host re-checks at act)
# The ONLY RNG draw is the nextInt gate (via entity.rand_int) — lockstep with mobRandom(e). The grass
# block presence is RE-CHECKED HOST-side at the eat tick (entity.eat_grass_block no-ops if not grass), so
# canUse returns True on a 0-gate and lets the eat animation start; the host eat is the authoritative check.
def eat_can_use(entity, world, nav):
    gate = EAT_GATE_BABY if entity.is_baby else EAT_GATE_ADULT   # nextInt(adjustedTickDelay(isBaby?50:1000))
    if entity.rand_int(gate) != 0:                               # DRAW 1: the ONE nextInt gate
        return False
    return True                                                  # the host re-checks grass at the act tick

# start (bytecode): eatAnimationTick = adjustedTickDelay(40)=40; broadcastEntityEvent(mob,(byte)10);
# navigation.stop();
def eat_start(entity, world, nav):
    entity.set_state("eat_tick", EAT_ANIM_TICKS)   # eatAnimationTick = adjustedTickDelay(40) = 40
    entity.eat_broadcast_byte10()                  # level.broadcastEntityEvent(mob, (byte)10)
    nav.stop()                                     # navigation.stop()

# canContinueToUse (bytecode): eatAnimationTick > 0.
def eat_continue(entity, world, nav):
    return entity.get_state("eat_tick") > 0

# tick (bytecode): eatAnimationTick = max(0, eatAnimationTick - 1);
#   if (eatAnimationTick != adjustedTickDelay(4)=4) return;     // act ONLY at tick == 4
#   ...eat the block (grass_block below -> dirt + mob.ate(): wool regrow + baby ageUp)...
def eat_tick(entity, world, nav):
    t = entity.get_state("eat_tick") - 1   # eatAnimationTick - 1
    if t < 0:                              # Math.max(0, ...)
        t = 0
    entity.set_state("eat_tick", t)
    if t == EAT_ACT_TICK:                  # act ONLY at == adjustedTickDelay(4) = 4
        entity.eat_grass_block()           # host: grass_block-below -> dirt + Sheep.ate (regrow + ageUp)

# stop (bytecode): eatAnimationTick = 0.
def eat_stop(entity, world, nav):
    entity.set_state("eat_tick", 0)

# ============================================================================================
# @6  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.RandomStrollGoal (WaterAvoidingRandomStrollGoal base)
# ============================================================================================
# canUse (WaterAvoidingRandomStrollGoal.getPosition, jar-verified): the 1-in-reducedTickDelay(interval)
# gate (DRAW 1: nextInt(60)), THEN the probability nextFloat() gate (DRAW 2), THEN the generateRandomPos
# UNCONDITIONAL 10-candidate loop (3 nextInt each, x/y/z ORDER). 31 draws total (gate + probability + 30
# offsets). Emit the 10 RAW candidates PLUS the wantLandMode flag as 31 FLAT positional floats.
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
    nav.path_to(*flat)   # 31 positional floats -> setWantCandidates -> the snap
    return True

# start: the candidates are committed in can_use (above), so start is a no-op (no stroll_start).

# canContinueToUse (bytecode): !navigation.isDone(). nav.has_path is that seam.
def stroll_continue(entity, world, nav):
    return nav.has_path()

# stop (bytecode): navigation.stop(). nav.stop() is that seam.
def stroll_stop(entity, world, nav):
    nav.stop()

# RandomStrollGoal.tick() is EMPTY in the jar (start()=moveTo + continue=!isDone), so this goal declares NO tick.

# ============================================================================================
# @7  LookAtPlayerGoal(mob, Player.class, 6.0f)   flags {LOOK}
# ports net.minecraft.world.entity.ai.goal.LookAtPlayerGoal
# ============================================================================================
# canUse (bytecode): getRandom().nextFloat() < probability (else false), then getNearestPlayer in
# lookDistance -> lookAt; return lookAt != null. Uses entity.y for the scan (behavior-identical to the
# Go oracle's nearestPlayerWithin).
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

# canContinueToUse (bytecode): lookAt.isAlive() && mob.distanceToSqr(lookAt) <= lookDistance^2 && lookTime > 0.
def look_continue(entity, world, nav):
    if entity.get_state("look_time") <= 0:
        return False
    dx = entity.get_state("look_x") - entity.x
    dy = entity.get_state("look_y") - entity.y
    dz = entity.get_state("look_z") - entity.z
    d2 = dx * dx + dy * dy + dz * dz
    return d2 <= LOOK_DIST * LOOK_DIST

# tick (bytecode): getLookControl().setLookAt(lookAt.getX(), lookAt.getEyeY(), lookAt.getZ()); lookTime--.
# Order: set look FIRST, THEN decrement.
def look_tick(entity, world, nav):
    entity.set_look_at(entity.get_state("look_x"), entity.get_state("look_y"), entity.get_state("look_z"))
    entity.set_state("look_time", entity.get_state("look_time") - 1)

# stop (bytecode): lookAt = null.
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
def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()           # nextDouble() heading (DRAW 1)
    entity.set_state("rel_x", math.cos(d))      # relX = cos(d)
    entity.set_state("rel_z", math.sin(d))      # relZ = sin(d)
    entity.set_state("look_time", 20 + entity.rand_int(20))   # lookTime = 20 + nextInt(20) (DRAW 2)

# canContinueToUse (bytecode): lookTime >= 0.
def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

# tick (bytecode): lookTime = lookTime - 1; getLookControl().setLookAt(getX()+relX, getEyeY(), getZ()+relZ).
# Order: decrement FIRST, then aim.
def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "sheep" -> renders as entity.Sheep.ID (custom = BEHAVIOR, not a new wire type). Real sheep
# attributes (Sheep.createAttributes: max_health 8.0, movement_speed 0.23 — 34-JARNOTES.md:71,322). The
# 9 goals at the EXACT jar Sheep.registerGoals priorities + flags, with the NEW EatBlockGoal@5.
declare_mob(
    name = "vanilla_sheep",
    base_type = "sheep",
    attributes = {
        "max_health": 8.0,       # Sheep.createAttributes: MAX_HEALTH 8.0
        "movement_speed": 0.23,  # Sheep.createAttributes: MOVEMENT_SPEED 0.23
    },
    goals = [
        # @0 FloatGoal [JUMP] — requiresUpdateEveryTick=true (jar-confirmed). Cite Sheep.registerGoals @0.
        goal(
            priority = 0,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @1 PanicGoal(mob, 1.25) [MOVE]. can_use commits the candidates (via path_to) so NO start kwarg.
        # Cite Sheep.registerGoals @1 PanicGoal.
        goal(
            priority = 1,
            flags = ["MOVE"],
            can_use = panic_can_use,
            stop = panic_stop,
            can_continue = panic_continue,
        ),
        # @2 BreedGoal(mob, 1.0) [MOVE, LOOK]. can_use gates on is_in_love (dormant on the un-fed sheep);
        # tick routes the breed through the ONE host try_breed. Cite Sheep.registerGoals @2 BreedGoal.
        goal(
            priority = 2,
            flags = ["MOVE", "LOOK"],
            can_use = breed_can_use,
            tick = breed_tick,
            stop = breed_stop,
            can_continue = breed_continue,
        ),
        # @3 TemptGoal(mob, 1.1, SHEEP_FOOD, false) [MOVE, LOOK] — the sheep's SINGLE tempt (sheep_food tag).
        # Cite Sheep.registerGoals @3 TemptGoal.
        goal(
            priority = 3,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_sheepfood_can_use,
            tick = tempt_sheepfood_tick,
            stop = tempt_sheepfood_stop,
            can_continue = tempt_sheepfood_continue,
        ),
        # @4 FollowParentGoal(mob, 1.1) [] EMPTY flags — a baby trails the nearest adult; draws NO RNG.
        # can_use gates on is_baby (dormant on the lone-ADULT sheep). Cite Sheep.registerGoals @4 FollowParentGoal.
        goal(
            priority = 4,
            flags = [],
            can_use = follow_can_use,
            tick = follow_tick,
            stop = follow_stop,
            can_continue = follow_continue,
        ),
        # @5 EatBlockGoal(mob) [MOVE, LOOK, JUMP] — THE NEW GOAL. can_use draws the ONE nextInt gate (via
        # entity.rand_int, lockstep with mobRandom); start arms the 40-tick timer + broadcasts byte 10;
        # tick acts at ==4 routing the eat through the 34-00 host seam (eat_grass_block: grass->dirt +
        # wool regrow + baby ageUp). Cite net.minecraft.world.entity.ai.goal.EatBlockGoal (Sheep.registerGoals @5).
        goal(
            priority = 5,
            flags = ["MOVE", "LOOK", "JUMP"],
            can_use = eat_can_use,
            start = eat_start,
            tick = eat_tick,
            stop = eat_stop,
            can_continue = eat_continue,
        ),
        # @6 WaterAvoidingRandomStrollGoal [MOVE] — can_use commits the candidates (via path_to), so NO start kwarg.
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
