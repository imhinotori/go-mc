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
# prerequisites — mob jump control + fluid detection — landed in 30-01/30-02). @1 PanicGoal is PORTED
# (Phase 31-01: its prerequisite — the lastDamageSource keystone (Phase 29) + the candidate/snap
# machinery (Phase 30.1) — exist). @4 TemptGoal x2 is PORTED (Phase 32-01: its prerequisite — the S4
# held-item read + item-tag membership — landed; the carrot_on_a_stick literal + the pig_food tag).
# @3 BreedGoal(1.0) + @5 FollowParentGoal(1.1) are PORTED (Phase 33: their prerequisites — entity
# aging (33-01) + in-love/FEED (33-02) — landed, and the Go-native breedGoal/followParentGoal +
# TickLoop.breed shipped in 33-03). This plugin now ports ALL 9 goals @0/@1/@3/@4×2/@5/@6/@7/@8 1:1.
# The @3/@5 callbacks read the Task-1 host handles (is_in_love, is_baby, breed_age,
# nearest_breeding_partner, nearest_adult_parent) and route the actual breed through the ONE host
# try_breed (= TickLoop.breed) so the breed RNG draws (variant nextBoolean() then XP 1+nextInt(7)) are
# made HOST-SIDE in lockstep with the Go-native breedGoal — the .star NEVER draws breed RNG itself.
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
PANIC_H = 5               # PanicGoal.findRandomPosition: DefaultRandomPos.getPos(mob, 5, 4) horizontal radius
PANIC_V = 4               # vertical radius (DefaultRandomPos.getPos(mob, 5, 4))
PANIC_SPEED = 1.25        # Pig.registerGoals @1 PanicGoal(mob, 1.25) (the want carries position only; the
                               # 1.25 multiplier is cited-deferred like stroll's speedModifier, LOCKSTEP with
                               # the Go oracle's panicSpeedModifier)
TEMPT_RANGE = 10.0        # Attributes.TEMPT_RANGE default (level/attribute/attributes.go:87) — the
                               # nearest-tempt-player scan radius the host applies. LOCKSTEP with the Go
                               # oracle's e.getAttributeValue(attribute.TemptRange).
TEMPT_SPEED = 1.2         # Pig.registerGoals @4 TemptGoal(mob, 1.2, ..., false) speedModifier (the want
                               # carries position only; the 1.2 multiplier is cited-deferred like the others)
STOP_DISTANCE = 2.5       # TemptGoal DEFAULT_STOP_DISTANCE (the pig uses the default ctor); tick stops the
                               # navigation within STOP_DISTANCE² (2.5² = 6.25), LOCKSTEP with the Go oracle
LOOK_DIST = 6.0           # LookAtPlayerGoal lookDistance (Pig: 6.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02   # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793   # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

BREED_RANGE = 8.0         # BreedGoal PARTNER_TARGETING: forNonCombat().range(8.0) — getFreePartner's
                               # inflate(8.0) scan radius (the host nearest_breeding_partner / try_breed bound).
                               # LOCKSTEP with the Go oracle's breedRange.
BREED_LOVE_THRESHOLD = 60      # BreedGoal.tick breed gate: loveTime >= adjustedTickDelay(60) (60 @ 20 TPS,
                               # NOT the halved 30). LOCKSTEP with the Go oracle's breedLoveThreshold.
BREED_DISTANCE_SQR = 9.0       # BreedGoal.tick breed gate: distanceToSqr(partner) < 9.0 (within 3 blocks).
                               # LOCKSTEP with the Go oracle's breedDistanceSqr.
FOLLOW_RANGE = 8.0        # FollowParentGoal HORIZONTAL_SCAN_RANGE (8) — the host nearest_adult_parent
                               # symmetry arg (the actual scan box is the host's fixed inflate(8,4,8)).
FOLLOW_RECALC_INTERVAL = 10    # FollowParentGoal.tick re-path cadence: adjustedTickDelay(10) (10 @ 20 TPS,
                               # NOT the halved 5). LOCKSTEP with the Go oracle's followRecalcInterval.

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
# @1  PanicGoal(mob, 1.25)   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.PanicGoal — LOCKSTEP with the Go oracle (server/ai_goals_panic.go)
# ============================================================================================
# canUse (bytecode): if (!shouldPanic()) return false; (RETURN BEFORE ANY RNG); if (isOnFire())
# { lookForWater(level, mob, 5); ... } ; return findRandomPosition() == DefaultRandomPos.getPos(mob,5,4).
# shouldPanic() = getLastDamageSource() != null && getLastDamageSource().is(panic_causes) — read via the
# Task-1 handle attrs entity.has_last_damage (the faithful not-null signal) + entity.damage_in_tag(
# "panic_causes") (the host-side DamageSource.is membership read; the tag id set stays on the Go side).
# The shouldPanic gate is the FIRST line so a dry/unhurt pig draws ZERO RNG (the oracle contract — IDENTICAL
# to the Go oracle's canUse short-circuit). When a panic DOES fire, findRandomPosition draws the
# UNCONDITIONAL 10-candidate DefaultRandomPos(5,4) loop: each candidate = generateRandomDirection in x, y, z
# ORDER (3 nextInt: nextInt(11)-5, nextInt(9)-4, nextInt(11)-5) = 30 nextInt total, IDENTICAL to the Go
# oracle's stream. Emit the 10 RAW candidates PLUS landMode=0.0 (DefaultRandomPos, no up-snap) as 31 FLAT
# positional floats via nav.path_to(x0,y0,z0,...,x9,y9,z9, 0.0) — the SAME 31-float overload Phase 30.1
# added (setWantCandidates -> snapStrollWant). The commit lands in can_use (path_to fires on canUse
# success), matching the Go goal which draws in findRandomPosition (called from canUse).
def panic_can_use(entity, world, nav):
    if not (entity.has_last_damage and entity.damage_in_tag("panic_causes")):   # shouldPanic: ZERO draws if false
        return False
    # (on-fire lookForWater branch: rare, RNG-free, currently a cited false-stub — matching the Go
    # isOnFire/lookForWater false-stubs; a non-burning pig never enters it.)
    flat = []
    for _ in range(10):   # DefaultRandomPos.getPos: 10 unconditional candidates (RandomPos.generateRandomPos, NO break)
        xt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * PANIC_V + 1) - PANIC_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(0.0)   # 31st float: landMode = 0.0 (DefaultRandomPos, no up-snap — LOCKSTEP with the Go start())
    nav.path_to(*flat)   # 31 positional floats (x0,y0,z0,...,x9,y9,z9, landMode) -> setWantCandidates -> the snap
    return True

# stop (bytecode): isRunning = false. The Go oracle: clearWantTarget(). nav.stop() is that seam.
def panic_stop(entity, world, nav):
    nav.stop()

# canContinueToUse (bytecode): !navigation.isDone(). The Go oracle: e.ai.hasTarget. nav.has_path is that seam.
def panic_continue(entity, world, nav):
    return nav.has_path()

# ============================================================================================
# @4  TemptGoal(mob, 1.2, i -> i.is(Items.CARROT_ON_A_STICK), false)   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.ai.goal.TemptGoal — LOCKSTEP with the Go oracle (server/ai_goals_passive.go temptGoal)
# ============================================================================================
# canUse (bytecode): if (calmDown > 0) { --calmDown; return false; }  then player = getNearestPlayer(
# TEMPT_TARGETING.range(TEMPT_RANGE), mob); return player != null. NO RNG — an int gate + a held-item
# player scan. The HOST owns the item-id set + the scan: entity.nearest_player_holding_carrot_on_a_stick(
# range) returns the matching player's (px,py,pz) tuple or None (the carrot_on_a_stick id 887 stays
# Go-side, mirroring damage_in_tag). The calmDown cooldown lives Go-side in the oracle's temptGoal; the
# .star re-scans each can_use, which is the observable equivalent (a player leaving range fails
# can_use / can_continue — the same not-following result). Distinct state keys (tempt_c_*) so the two @4
# goals never clobber each other. In the DRY oracle no player holds a tempt item near the pig -> None ->
# can_use false -> zero new draws (the oracle contract; TemptGoal draws zero RNG anyway).
def tempt_carrot_can_use(entity, world, nav):
    p = entity.nearest_player_holding_carrot_on_a_stick(TEMPT_RANGE)   # host scan + predicate; item id stays Go-side
    if p == None:
        return False
    entity.set_state("tempt_c_px", p[0])
    entity.set_state("tempt_c_py", p[1])
    entity.set_state("tempt_c_pz", p[2])
    return True

# tick (bytecode): getLookControl().setLookAt(player, ...); if (distanceToSqr(player) < stopDistance²)
# stopNavigation(); else navigateTowards(player) (moveTo(player, 1.2)). set_look_at is the LookControl
# .setLookAt seam (host derives the same yawTowardDeg as the Go oracle); move_to is the navigateTowards
# seam (setWantTarget — the 1.2 speedModifier is the nav-tick multiplier, cited-deferred like the others).
def tempt_carrot_tick(entity, world, nav):
    px = entity.get_state("tempt_c_px")
    py = entity.get_state("tempt_c_py")
    pz = entity.get_state("tempt_c_pz")
    entity.set_look_at(px, py, pz)   # TemptGoal.tick setLookAt(player)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if dx * dx + dy * dy + dz * dz < STOP_DISTANCE * STOP_DISTANCE:   # 2.5² = 6.25
        nav.stop()   # stopNavigation()
    else:
        entity.move_to(px, py, pz)   # navigateTowards(player) @ TEMPT_SPEED 1.2

# stop (bytecode): player = null; stopNavigation(); calmDown = reducedTickDelay(100); isRunning = false.
# The Go oracle resets calmDown=50; the .star side stops the nav (the calmDown lives Go-side in the oracle).
def tempt_carrot_stop(entity, world, nav):
    nav.stop()   # stopNavigation()

# canContinueToUse (bytecode): if (canScare()) { ...flee-abort... } return canUse(). PIG: canScare=false
# -> the flee block is DEAD -> canContinueToUse == canUse (re-scan).
def tempt_carrot_continue(entity, world, nav):
    return tempt_carrot_can_use(entity, world, nav)

# ============================================================================================
# @4  TemptGoal(mob, 1.2, i -> i.is(ItemTags.PIG_FOOD), false)   flags {MOVE, LOOK}
# the SECOND @4 — identical callbacks calling the pig_food host handle. Distinct state keys (tempt_p_*).
# ============================================================================================
def tempt_pigfood_can_use(entity, world, nav):
    p = entity.nearest_player_holding_pig_food(TEMPT_RANGE)   # host scan + predicate (ItemTags.PIG_FOOD stays Go-side)
    if p == None:
        return False
    entity.set_state("tempt_p_px", p[0])
    entity.set_state("tempt_p_py", p[1])
    entity.set_state("tempt_p_pz", p[2])
    return True

def tempt_pigfood_tick(entity, world, nav):
    px = entity.get_state("tempt_p_px")
    py = entity.get_state("tempt_p_py")
    pz = entity.get_state("tempt_p_pz")
    entity.set_look_at(px, py, pz)   # TemptGoal.tick setLookAt(player)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if dx * dx + dy * dy + dz * dz < STOP_DISTANCE * STOP_DISTANCE:   # 2.5² = 6.25
        nav.stop()   # stopNavigation()
    else:
        entity.move_to(px, py, pz)   # navigateTowards(player) @ TEMPT_SPEED 1.2

def tempt_pigfood_stop(entity, world, nav):
    nav.stop()   # stopNavigation()

def tempt_pigfood_continue(entity, world, nav):
    return tempt_pigfood_can_use(entity, world, nav)   # canScare=false -> canContinueToUse == canUse

# ============================================================================================
# @3  BreedGoal(mob, 1.0)   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.ai.goal.BreedGoal — LOCKSTEP with the Go oracle (server/ai_goals_breed.go breedGoal)
# ============================================================================================
# canUse (bytecode): if (!animal.isInLove()) return false; partner = getFreePartner(); return partner != null.
# The isInLove gate is the FIRST line — the oracle-safety contract: the un-fed lone-adult oracle pig
# (inLove==0) returns false HERE, before the partner scan, so the breed path (and its host RNG draws via
# try_breed) NEVER fires on the oracle → byte-identical. is_in_love is the Task-1 host read (Animal.isInLove);
# nearest_breeding_partner is the Task-1 host scan (getFreePartner: nearest same-class in-love non-panicking
# within 8.0 — the same-class + canMate + isPanicking filter stays HOST-side, mirroring the tempt item-id
# predicate). The .star sees only a partner position tuple or None. start() resets loveTime=0 (mirrored
# here: the goal commits loveTime=0 on a fresh acquire). NO RNG drawn in the .star (the breed draws are
# made host-side inside try_breed = TickLoop.breed — the lockstep rule).
def breed_can_use(entity, world, nav):
    if not entity.is_in_love():   # Animal.isInLove() gate (line 1) — ZERO effect on the un-fed oracle
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
# adjustedTickDelay(60) && distanceToSqr(partner) < 9.0) breed(). set_look_at is the LookControl.setLookAt
# seam (host derives the same yawTowardDeg as the Go oracle); move_to is the navigation.moveTo seam
# (setWantTarget — the 1.0 speedModifier is the nav-tick multiplier, cited-deferred like the others).
# The ++loveTime happens BEFORE the breed check (matching the Go oracle's tick order). At the threshold
# within 3 blocks the .star calls try_breed(8.0) — the ONE host breed op (= TickLoop.breed) that draws
# the variant nextBoolean() FIRST then the XP 1+nextInt(7) SECOND on the initiator's per-mob RNG, EXACTLY
# the order the Go-native breedGoal.tick -> t.breed draws (Plan 33-03). The .star draws NO breed RNG.
def breed_tick(entity, world, nav):
    px = entity.get_state("breed_px")
    py = entity.get_state("breed_py")
    pz = entity.get_state("breed_pz")
    entity.set_look_at(px, py, pz)   # BreedGoal.tick lookAt(partner)
    entity.move_to(px, py, pz)       # navigation.moveTo(partner, 1.0)
    lt = entity.get_state("breed_love_time") + 1   # ++loveTime (BEFORE the breed check — Go tick order)
    entity.set_state("breed_love_time", lt)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if lt >= BREED_LOVE_THRESHOLD and dx * dx + dy * dy + dz * dz < BREED_DISTANCE_SQR:
        entity.try_breed(BREED_RANGE)   # THE one host breed (= TickLoop.breed) — variant then XP, the lockstep

# stop (bytecode): partner = null; loveTime = 0. The Go oracle: g.partner=nil; g.loveTime=0; clearWantTarget().
def breed_stop(entity, world, nav):
    entity.set_state("breed_love_time", 0)
    nav.stop()   # clearWantTarget()

# canContinueToUse (bytecode): partner.isAlive() && partner.isInLove() && loveTime < 60 && !partner.isPanicking().
# The .star cannot read the partner's inLove/panicking directly (the host hands it only a position) — the
# observable equivalent is a re-scan: nearest_breeding_partner still returns a tuple iff a same-class in-love
# non-panicking partner is still in range (the canMate+isPanicking filter is the SAME host scan getFreePartner
# applies for partner.isInLove() && !partner.isPanicking()). Paired with loveTime < 60 (the courting bound),
# this is behavior-identical to the Go oracle's canContinueToUse for the oracle (where the goal is dormant)
# and for the scenario (where the pair stays in love until breed at loveTime>=60).
def breed_continue(entity, world, nav):
    if entity.get_state("breed_love_time") >= BREED_LOVE_THRESHOLD:   # loveTime < 60 bound
        return False
    return entity.nearest_breeding_partner(BREED_RANGE) != None   # partner alive && isInLove && !isPanicking

# ============================================================================================
# @5  FollowParentGoal(mob, 1.1)   flags {} EMPTY
# ports net.minecraft.world.entity.ai.goal.FollowParentGoal — LOCKSTEP with the Go oracle (server/ai_goals_follow.go)
# ============================================================================================
# canUse (bytecode): if (animal.getAge() >= 0) return false;  // only a BABY follows
#   parents = getEntitiesOfClass(animal.getClass(), inflate(8,4,8)); closest = nearest ADULT (age>=0);
#   if (closest == null) return false; if (closestDistSqr < 9.0) return false; parent = closest; return true.
# is_baby is the Task-1 host read (AgeableMob.isBaby == age<0) — the FIRST-line gate: the lone-ADULT oracle
# pig (is_baby false) returns false HERE, so the follow path is dormant on the oracle → byte-identical.
# nearest_adult_parent is the Task-1 host scan (the nearest same-class ADULT in inflate(8,4,8) NOT within 3
# blocks — the adult filter + the DONT_FOLLOW_IF_CLOSER_THAN reject stay HOST-side). NO RNG anywhere
# (FollowParentGoal is fully deterministic). The .star sees only the parent position tuple or None.
def follow_can_use(entity, world, nav):
    if not entity.is_baby():   # FollowParentGoal.canUse: age>=0 returns false — only a baby follows
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
# navigation.moveTo(parent, speed). PURE INT — NO RNG. The decrement-then-gate matches the Go oracle's
# tick exactly: the first tick (--0 = -1, not >0) re-paths and resets the timer to 10; intermediate ticks
# count down without re-pathing. move_to is the navigation.moveTo(parent, 1.1) seam (setWantTarget). The
# parent position is re-read from the host scan on each re-path so the baby trails a moving parent.
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

# stop (bytecode): parent = null. The Go oracle: g.parent=nil; clearWantTarget().
def follow_stop(entity, world, nav):
    nav.stop()   # clearWantTarget()

# canContinueToUse (bytecode): if (animal.getAge() >= 0) return false; if (!parent.isAlive()) return false;
# d = distanceToSqr(parent); return !(d < 9.0) && !(d > 256.0).  // follow while 3..16 blocks.
# The .star re-scans for the parent (a live in-range adult still returns a tuple — the host scan already
# rejects an adult within 3 blocks via DONT_FOLLOW, and the 16-block upper bound is the same-region near()
# reach), gated on is_baby (grew-up → stop). Behavior-identical to the Go oracle's continue band for both
# the dormant oracle (no adult → false) and the scenario (the baby follows while the adult is 3..16 away).
def follow_continue(entity, world, nav):
    if not entity.is_baby():   # grew up (age>=0) → stop
        return False
    return entity.nearest_adult_parent(FOLLOW_RANGE) != None   # a live adult still in the follow band

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
        # @1 PanicGoal(mob, 1.25) [MOVE] — the flee consumer; can_use commits the candidates (via path_to)
        # so NO start kwarg; stop/continue carry the rest. Preempts stroll@6's MOVE flag (priority 1 < 6).
        # LOCKSTEP with the Go oracle's addGoal(1, newPanicGoal(1.25)). Cite Pig.registerGoals @1 PanicGoal.
        goal(
            priority = 1,
            flags = ["MOVE"],
            can_use = panic_can_use,
            stop = panic_stop,
            can_continue = panic_continue,
        ),
        # @3 BreedGoal(mob, 1.0) [MOVE, LOOK] — preempts the @4 tempts + @6 stroll for MOVE/LOOK when it
        # fires (priority 3 < 4 < 6). can_use gates on is_in_love (dormant on the un-fed oracle); tick
        # courts the partner and at the threshold routes the breed through the ONE host try_breed
        # (= TickLoop.breed) so the variant+XP draws are host-side in lockstep with the Go-native breedGoal.
        # LOCKSTEP with the Go oracle's addGoal(3, newBreedGoal(1.0)). Cite Pig.registerGoals @3 BreedGoal.
        goal(
            priority = 3,
            flags = ["MOVE", "LOOK"],
            can_use = breed_can_use,
            tick = breed_tick,
            stop = breed_stop,
            can_continue = breed_continue,
        ),
        # @4 TemptGoal(mob, 1.2, CARROT_ON_A_STICK, false) [MOVE, LOOK] — ADDED FIRST among the @4 pair
        # (the carrot goal wins the shared {MOVE,LOOK} flags; addGoal keeps insertion order among equals,
        # faithful "first-added wins"). LOCKSTEP with the Go oracle's first addGoal(4, newTemptGoal(...887...)).
        goal(
            priority = 4,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_carrot_can_use,
            tick = tempt_carrot_tick,
            stop = tempt_carrot_stop,
            can_continue = tempt_carrot_continue,
        ),
        # @4 TemptGoal(mob, 1.2, PIG_FOOD, false) [MOVE, LOOK] — the SECOND @4 (pig_food tag). LOCKSTEP
        # with the Go oracle's second addGoal(4, newTemptGoal(...pig_food...)).
        goal(
            priority = 4,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_pigfood_can_use,
            tick = tempt_pigfood_tick,
            stop = tempt_pigfood_stop,
            can_continue = tempt_pigfood_continue,
        ),
        # @5 FollowParentGoal(mob, 1.1) [] EMPTY flags — a baby trails the nearest adult; claims NO control
        # flag (the selector never blocks an empty-flag goal), re-paths every 10 ticks, draws NO RNG.
        # can_use gates on is_baby (dormant on the lone-ADULT oracle). LOCKSTEP with the Go oracle's
        # addGoal(5, newFollowParentGoal(1.1)). Cite Pig.registerGoals @5 FollowParentGoal.
        goal(
            priority = 5,
            flags = [],
            can_use = follow_can_use,
            tick = follow_tick,
            stop = follow_stop,
            can_continue = follow_continue,
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
