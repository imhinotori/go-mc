# vanilla_cat — a 1:1 vanilla-Cat dogfood (MOB-NEUT-03, Task #9), the SECOND tameable mob (after the wolf).
# A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares the PORTABLE slice of net.minecraft.world.entity.animal.feline.Cat.registerGoals, reusing the
# mob-agnostic passive callbacks (float/panic/breed/tempt/stroll/look — COPIED VERBATIM from vanilla_cow) AND
# the Phase-36 Go-native TAME/OWNER goals (sit/follow_owner) via the kind= seam.
#
# Cat.registerGoals() (javap-verified this session):
#   @1  FloatGoal                                       <-- .star (shared passive float)
#   @1  TamableAnimal.TamableAnimalPanicGoal(1.5)       <-- .star (the cow PanicGoal reuse; extends PanicGoal)
#   @2  SitWhenOrderedToGoal                            <-- kind="sit" (36-01 sitWhenOrderedToGoal)
#   @3  CatRelaxOnOwnerGoal                             <-- DEFERRED (cat-specific: lie on the owner in bed)
#   @4  CatTemptGoal(0.6, is(CAT_FOOD), true)           <-- .star (shared TemptGoal; cat_food tag)
#   @5  CatLieOnBedGoal                                 <-- DEFERRED (cat-specific bed subsystem)
#   @6  FollowOwnerGoal(1.0, 10.0, 5.0)                 <-- kind="follow_owner" (36-01 followOwnerGoal)
#   @7  CatSitOnBlockGoal                               <-- DEFERRED (cat-specific chest/furnace sit)
#   @8  LeapAtTargetGoal(0.3)                           <-- DEFERRED (defers WITH its prey target)
#   @9  OcelotAttackGoal                                <-- DEFERRED (defers WITH its prey target)
#   @10 BreedGoal(0.8)                                  <-- .star (shared BreedGoal)
#   @11 WaterAvoidingRandomStrollGoal(0.8)              <-- .star (shared stroll)
#   @12 LookAtPlayerGoal(Player, 10.0)                  <-- .star (shared look)
#   targetSelector @1 NonTameRandomTargetGoal<Rabbit/Turtle> <-- DEFERRED (defers WITH its prey; a tamed cat has none)
#
# THE TAMING: fish-tames (CAT_FOOD = cod/salmon) via the Go-native Cat.mobInteract port (tryCatInteract /
# tryToTameCat, attack_dispatch.go) — the wolf sibling, EXCEPT no health bump (Cat.applyTamingSideEffects is
# the base no-op: MAX_HEALTH stays 10). The tamed sit-toggle + FollowOwner reuse the wolf's sit/follow_owner.
#
# DEFERRED (cite-recorded, NEVER silently dropped). This session jar-verified the comfort goals + the
# morning-gift + the collar-dye down to their exact constants and RNG draw order (below). They are NOT
# landed because EVERY ONE of their gates is an unbuilt subsystem, and the 1:1 mandate forbids stubbing a
# whole GATE to a constant (constant-false = a silent disabled goal; constant-true = a wrong-firing goal --
# both violations). What is landed here is the build-ready spec: when the named subsystem arrives, the goal
# drops in verbatim from these citations with no further jar reads.
#
#   - @3 Cat$CatRelaxOnOwnerGoal (lie on the owner sleeping in bed). Cite Cat$CatRelaxOnOwnerGoal.
#       BLOCKED BY: player-sleep state. canUse gate order (CFR):
#         isTame() && !isOrderedToSit() && getOwner() instanceof Player && owner.isSleeping()   <-- MISSING
#         && distanceToSqr(owner) <= 100.0 && level.getBlockState(owner.blockPosition()).is(BlockTags.BEDS)
#         && !spaceIsOccupied().  goalPos = ownerPos.relative(BedBlock.FACING.getOpposite()) (or ownerPos).
#       spaceIsOccupied: getEntitiesOfClass(Cat, AABB(goalPos).inflate(2.0)); occupied iff any OTHER cat
#         isLying()||isRelaxStateOne().  start: setInSittingPose(false); nav.moveTo(goalPos, 1.1f).
#       tick: setInSittingPose(false); nav.moveTo(goalPos, 1.1f); if distanceToSqr(owner) < 2.5 {
#         ++onBedTicks; if onBedTicks > adjustedTickDelay(16) { setLying(true); setRelaxStateOne(false) }
#         else { lookAt(owner,45,45); setRelaxStateOne(true) } } else setLying(false).  NO RNG in canUse/tick.
#       stop: setLying(false); THEN the morning-gift roll (below); onBedTicks=0; setRelaxStateOne(false);
#         nav.stop().  Needs synched IS_LYING + RELAX_STATE_ONE (Cat.defineSynchedData define(...,false)).
#   - The morning-gift (Cat$CatRelaxOnOwnerGoal.stop -> giveMorningGift). Cite giveMorningGift.
#       BLOCKED BY: player-sleep (getSleepTimer) + the CAT_MORNING_GIFT loot table + randomTeleport.
#       GATE (stop): owner.getSleepTimer() >= 100 && level.getRandom().nextFloat() <
#         environmentAttributes().getValue(EnvironmentAttributes.CAT_WAKING_UP_GIFT_CHANCE, position()).
#         JAR FINDING: CAT_WAKING_UP_GIFT_CHANCE defaultValue == 0.0f (26.2 EnvironmentAttributes) -- so at
#         the vanilla default the gift NEVER fires (nextFloat() < 0.0f is always false); it is a datapack/
#         dimension override knob. The nextFloat() draw is on the LEVEL rng (getRandom()), not the cat rng.
#       giveMorningGift RNG (cat.getRandom(), IN ORDER): randomTeleport(x + nextInt(11)-5, y + nextInt(5)-2,
#         z + nextInt(11)-5, false); then dropFromGiftLootTable(CAT_MORNING_GIFT) spawns an ItemEntity at
#         (catX - sin(yBodyRot*pi/180), catY, catZ + cos(yBodyRot*pi/180)) (leash holder pos if leashed).
#   - @5 CatLieOnBedGoal (extends MoveToBlockGoal(cat, speed, searchRange=8, verticalSearchRange=6);
#       verticalSearchStart=-2; flags {JUMP,MOVE}). Cat.registerGoals @5 ctor (Cat, D, I) with I(searchRange)
#       = bipush 8. Cite CatLieOnBedGoal + MoveToBlockGoal.  BLOCKED BY: BlockTags.BEDS block-state scan.
#       canUse: isTame() && !isOrderedToSit() && !isLying() && super.canUse().  isValidTarget(level,pos):
#         level.isEmptyBlock(pos.above()) && level.getBlockState(pos).is(BlockTags.BEDS)   <-- MISSING (no
#         generated BlockTags map; data/tag.tags.go `beds` is an ITEM tag, not the block tag).
#       nextStartTick override = 40 (constant, NOT the base 200+nextInt(200)).  tick: super.tick();
#         setInSittingPose(false); if !isReachedTarget() setLying(false) else if !isLying() setLying(true).
#   - @7 CatSitOnBlockGoal (extends MoveToBlockGoal(cat, speed, searchRange=8); flags {MOVE,JUMP} base).
#       Cite CatSitOnBlockGoal + MoveToBlockGoal.  BLOCKED BY: BlockTags.BEDS + Blocks.CHEST/FURNACE +
#       ChestBlockEntity.getOpenCount + FurnaceBlock.LIT -- none queryable from the AI layer in v1.
#       canUse: isTame() && !isOrderedToSit() && super.canUse().  isValidTarget(level,pos): isEmptyBlock(
#         pos.above()) && ( is(Blocks.CHEST) ? ChestBlockEntity.getOpenCount(level,pos) < 1
#         : is(Blocks.FURNACE) && FurnaceBlock.LIT ? true
#         : is(BlockTags.BEDS, s -> s.getOptionalValue(BedBlock.PART).map(v -> v != HEAD).orElse(true)) ).
#       tick: super.tick(); setInSittingPose(isReachedTarget()).
#   - MoveToBlockGoal base RNG (shared by @5/@7 once BlockTags land) -- TWO draws, IN ORDER: (1) canUse when
#       nextStartTick==0 -> nextStartTick = nextStartTick(mob) (CatLieOnBed overrides to a CONSTANT 40, so
#       NO draw; CatSitOnBlock uses the base reducedTickDelay(200 + nextInt(200)) -> ONE nextInt(200));
#       (2) start -> maxStayTicks = nextInt(nextInt(1200) + 1200) + 1200 (TWO nextInt, inner first).
#       findNearestBlock is the deterministic ring scan (NO RNG). requiresUpdateEveryTick == true.
#   - Collar-dye (Cat.mobInteract tamed+owned branch, BEFORE the feed/sit-toggle). Cite Cat.mobInteract.
#       BLOCKED BY: ItemTags.CAT_COLLAR_DYES + DataComponents.DYE + the synched DATA_COLLAR_COLOR field +
#       setPersistenceRequired -- none exist in v1 (entity_encode.go carries the default WHITE collar only).
#       BRANCH (CFR): if stack.is(ItemTags.CAT_COLLAR_DYES) { DyeColor c = stack.get(DataComponents.DYE);
#         if (c != null && c != getCollarColor()) { setCollarColor(c); stack.consume(1, player);
#         setPersistenceRequired(); return SUCCESS } }.  NO RNG.  DATA_COLLAR_COLOR default = DEFAULT_COLLAR
#         _COLOR.getId() (RED == 14) per Cat.defineSynchedData.  When wired: slot it into tryCatInteract
#         (attack_dispatch.go) as the FIRST tamed+owned check, before the cat_food feed short-circuit.
#   - @8 LeapAtTargetGoal(0.3) / @9 OcelotAttackGoal / targetSelector @1 NonTameRandomTargetGoal<Rabbit/
#       Turtle>: no rabbit/turtle-as-prey selector for the cat in v1 (defers WITH those prey). A tamed cat
#       does not hunt anyway. Cite Cat.registerGoals @8/@9 + targetSelector @1.

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
LOOK_DIST = 10.0          # LookAtPlayerGoal lookDistance (Cat: 10.0f)
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
# player scan. The HOST owns the item-tag set + the scan: entity.nearest_player_holding_food("cat_food",
# range) returns the matching player's (px,py,pz) tuple or None (the COW_FOOD membership stays Go-side,
# the parameterized 34-00 handle). In the DRY oracle no player holds cow_food near the cow -> None ->
# can_use false -> zero new draws (TemptGoal draws zero RNG anyway).
def tempt_food_can_use(entity, world, nav):
    p = entity.nearest_player_holding_food("cat_food", TEMPT_RANGE)   # host scan + COW_FOOD predicate (Go-side)
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
# base_type "cat" -> renders as entity.Cat.ID. Real Cat attributes (Animal.createAnimalAttributes + Cat
# overrides: MAX_HEALTH 10.0, MOVEMENT_SPEED 0.3, ATTACK_DAMAGE 3.0). The portable goal slice at the jar
# priorities (float@1, panic@1, sit@2, tempt@4, follow_owner@6, breed@10, stroll@11, look@12); the
# cat-specific comfort/prey goals are cite-deferred (header). sit/follow_owner are Go-native kinds (the
# wolf's). Cite net.minecraft.world.entity.animal.feline.Cat.registerGoals + Cat.createAttributes.
declare_mob(
    name = "vanilla_cat",
    base_type = "cat",
    attributes = {
        "max_health": 10.0,      # Cat.createAttributes: MAX_HEALTH 10.0 (tamed stays 10 — no bump)
        "movement_speed": 0.3,   # Cat.createAttributes: MOVEMENT_SPEED 0.3
        "attack_damage": 3.0,    # Cat.createAttributes: ATTACK_DAMAGE 3.0
    },
    goals = [
        # @1 FloatGoal [JUMP] — requiresUpdateEveryTick=true. Cite Cat.registerGoals @1 FloatGoal.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @1 TamableAnimalPanicGoal(mob, 1.5) [MOVE] — the cow PanicGoal reuse. Cite Cat.registerGoals @1.
        goal(
            priority = 1,
            flags = ["MOVE"],
            can_use = panic_can_use,
            stop = panic_stop,
            can_continue = panic_continue,
        ),
        # @2 SitWhenOrderedToGoal [JUMP, MOVE] — kind="sit" (the wolf's Go-native sit). Cite Cat.registerGoals @2.
        goal(priority = 2, flags = ["JUMP", "MOVE"], kind = "sit"),
        # @4 CatTemptGoal(mob, 0.6, CAT_FOOD, true) [MOVE, LOOK] — the .star tempt on the cat_food tag. Cite
        # Cat.registerGoals @4 CatTemptGoal.
        goal(
            priority = 4,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_food_can_use,
            tick = tempt_food_tick,
            stop = tempt_food_stop,
            can_continue = tempt_food_continue,
        ),
        # @6 FollowOwnerGoal(mob, 1.0, 10.0, 5.0) [MOVE] — kind="follow_owner" (the wolf's Go-native follow).
        # Cite Cat.registerGoals @6 FollowOwnerGoal.
        goal(priority = 6, flags = ["MOVE"], kind = "follow_owner"),
        # @10 BreedGoal(mob, 0.8) [MOVE, LOOK]. Cite Cat.registerGoals @10 BreedGoal(0.8).
        goal(
            priority = 10,
            flags = ["MOVE", "LOOK"],
            can_use = breed_can_use,
            tick = breed_tick,
            stop = breed_stop,
            can_continue = breed_continue,
        ),
        # @11 WaterAvoidingRandomStrollGoal(mob, 0.8) [MOVE]. Cite Cat.registerGoals @11.
        goal(
            priority = 11,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @12 LookAtPlayerGoal(Player, 10.0) [LOOK]. Cite Cat.registerGoals @12 LookAtPlayerGoal.
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
