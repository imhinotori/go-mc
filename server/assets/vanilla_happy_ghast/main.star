# vanilla_happy_ghast — a 1:1 vanilla-HappyGhast dogfood, the Happy Ghast built as a Starlark plugin. A
# LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p /
# CFR). The Happy Ghast is a RIDEABLE FLYING mob that FLOATS (hovers) instead of falling. It is the FIRST
# brain-based mob attempted; the Brain drives ONLY the baby (customServerAiStep: if isBaby() brain.tick),
# while BOTH baby and adult run the classic goalSelector (registerGoals). So the classic goal set IS the
# faithful observable AI for the adult, and it is what we port; the Brain tree is substituted/deferred (see
# DEFERRALS).
#
# HappyGhast.registerGoals() (javap-verified — the SAME goals for baby and adult):
#   @3  HappyGhastFloatGoal (extends FloatGoal; canUse = not isOnStillTimeout() and super.canUse())  float
#   @4  TemptGoal.ForNonPathfinders(1.0, is(HAPPY_GHAST_FOOD), false, 7.0)                            look-only
#   @5  Ghast.RandomFloatAroundGoal(this, 16)                                                         HOST-NATIVE flight
#
# HappyGhast FLIGHT physics (javap/CFR-verified, ported HOST-side in server/ai_goals_happy_ghast.go):
#   - HappyGhast.travel(input): airSpeed = (float)getAttributeValue(FLYING_SPEED) * 5.0f / 3.0f; then
#     travelFlying(input, airSpeed, airSpeed, airSpeed). travelFlying (LivingEntity) in AIR: moveRelative(
#     airSpeed, input); move(deltaMovement); deltaMovement *= 0.91f. NO GRAVITY — the ghast hovers.
#   - Ghast.GhastMoveControl.tick: while operation==MOVE_TO and floatDuration-- <= 0: floatDuration +=
#     nextInt(5)+2; travel = wanted - pos; if canReach(travel) deltaMovement += travel.normalize().scale(
#     FLYING_SPEED * 5.0/3.0) else operation = WAIT. (careful=true ctor — AABB traversal; v1 simpler reach.)
#   - Ghast.RandomFloatAroundGoal(mob, 16).canUse: true when moveControl has no wanted, OR wanted <1 or
#     >3600 blocks-sq away. start(): getSuitableFlyToPosition (64 attempts of center + (nextFloat()*2-1)*16
#     per axis; keep first air-with-open-neighbor; else last raw; heightmap DOWN-clamp) then setWantedPosition
#     (x,y,z,1.0). canContinueToUse=false (re-rolls each cycle).
#
# ATTRIBUTES (HappyGhast.createAttributes) — see level/attribute/defaults.go happyGhastSupplier:
#   MAX_HEALTH 20.0, TEMPT_RANGE 16.0, FLYING_SPEED 0.05, MOVEMENT_SPEED 0.05, FOLLOW_RANGE 16.0,
#   CAMERA_DISTANCE 8.0. isBaby scaling: getAgeScale() = isBaby ? 0.2375f : 1.0f (BABY_SCALE 0.2375).
#
# DEFERRALS (cite-recorded, NEVER silently dropped — each needs a subsystem absent in v1):
#   - THE BRAIN (HappyGhastAi): memory modules + sensors (NEAREST_LIVING_ENTITIES, HURT_BY, FOOD_TEMPTATIONS,
#     NEAREST_ADULT_ANY_TYPE, NEAREST_PLAYERS) + activities. NO Brain subsystem exists in v1 (grep confirmed).
#     customServerAiStep only ticks the brain for the BABY; the ADULT is fully driven by the classic goals we
#     port here, so the adult observable AI is faithful. The baby-brain tree is SUBSTITUTED by the same classic
#     goalSelector goals (both baby+adult registerGoals), matching observable float/wander/tempt. Recorded in
#     .planning/FINAL-MILESTONE-PARITY.md Phase-A queue.
#   - RIDE / MULTI-PASSENGER + HARNESS: doPlayerRide/startRiding, MAX_PASSANGERS 4, getRiddenInput/tickRidden,
#     canUseSlot(BODY)=adult-only harness equip, the goggles-up/down states (lang subtitles), isFlyingVehicle.
#     No riding/vehicle subsystem in v1 -> deferred (Phase-A queue). The ghast still spawns + hovers + wanders
#     faithfully; it just cannot yet be mounted.
#   - DRIED-GHAST REHYDRATION spawn: the happy ghast is NOT a natural mob-spawn — it hatches from a dried
#     ghast block rehydrated in water (getMaxSpawnClusterSize=1; no natural checkSpawnRules path). No
#     dried-ghast block subsystem in v1 -> it stays OUT of the natural spawn pool (spawn via /dbg or egg).
#   - continuousHeal (heal 1 every 20t in clouds/rain else 600t), checkRestriction/home-radius, serverStill
#     Timeout, leash-holder — ambient upkeep; deferred (no clouds/precip/leash + no home-restriction nav).
#     The CORE (spawn + hover flight + baby scaling) is fully wired.

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4      # Entity.getFluidJumpThreshold default (FloatGoal.canUse gate; dry oracle: unused)
FLOAT_JUMP_PROBABILITY = 0.8    # FloatGoal.tick: nextFloat() < 0.8f (the swim-jump chance)
TEMPT_RANGE = 16.0              # HappyGhast TEMPT_RANGE attribute (createAttributes: 16.0); host scan bound.

# ============================================================================================
# @3  HappyGhastFloatGoal (extends FloatGoal)   flags {JUMP}   requiresUpdateEveryTick=true
# ports net.minecraft.world.entity.ai.goal.FloatGoal (the SAME mob-agnostic swim-float the cow/ghast run).
# HappyGhastFloatGoal ADDS a "not isOnStillTimeout()" guard to canUse; isOnStillTimeout is the ride/still
# subsystem (deferred, always false in v1), so the guard is a no-op -> this is the plain FloatGoal here.
# Cite HappyGhast.registerGoals @3 HappyGhastFloatGoal + HappyGhastFloatGoal.canUse.
# ============================================================================================
# canUse (bytecode): isInWater() and getFluidHeight(WATER) > getFluidJumpThreshold() or isInLava(). NO RNG.
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

# tick (bytecode): if getRandom().nextFloat() < 0.8f -> getJumpControl().jump(). The ONLY new RNG (tick only).
def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:
        nav.jump()

# ============================================================================================
# @4  TemptGoal.ForNonPathfinders(mob, 1.0, is(HAPPY_GHAST_FOOD), false, 7.0)   flags {MOVE, LOOK}
# ports the LOOK-only observable slice of net.minecraft.world.entity.ai.goal.TemptGoal. The base
# ForNonPathfinders navigates via the moveControl (folded into the native hover, cited header); here we wire
# the faithful LOOK: a ghast looks at a nearby player holding happy_ghast food. Cite HappyGhast.registerGoals
# @4 TemptGoal.ForNonPathfinders + TemptGoal.canUse/tick.
# ============================================================================================
# canUse (bytecode): if calmDown>0 { --calmDown; return false; } player = getNearestPlayer(TEMPT_TARGETING
# .range(TEMPT_RANGE), mob); return player != null. NO RNG. The host owns the food-tag scan.
def tempt_can_use(entity, world, nav):
    p = entity.nearest_player_holding_food("happy_ghast_food", TEMPT_RANGE)
    if p == None:
        return False
    entity.set_state("tempt_px", p[0])
    entity.set_state("tempt_py", p[1])
    entity.set_state("tempt_pz", p[2])
    return True

# tick (bytecode): getLookControl().setLookAt(player, ...); (the navigate-toward is the native hover, cited).
def tempt_tick(entity, world, nav):
    entity.set_look_at(entity.get_state("tempt_px"), entity.get_state("tempt_py"), entity.get_state("tempt_pz"))

# stop (bytecode): player = null; calmDown = reducedTickDelay(100); the observable seam is a re-scan.
def tempt_stop(entity, world, nav):
    pass

# canContinueToUse (bytecode): canScare()? flee-abort : canUse(). ForNonPathfinders canScare=false -> re-scan.
def tempt_continue(entity, world, nav):
    return tempt_can_use(entity, world, nav)

# --- the declaration ---------------------------------------------------------------------------
# base_type "happy_ghast" -> renders as entity.HappyGhast.ID (58). Real HappyGhast attributes (HappyGhast
# .createAttributes) seeded with the jar values (see happyGhastSupplier). The observable goal slice at the
# jar registerGoals priorities (float@3, tempt@4); the RandomFloatAroundGoal@5 FLIGHT is HOST-NATIVE
# (happyGhastAiStep — the GhastMoveControl + fly-to selection cannot go through the ground A* nav), so it is
# NOT declared as a nav goal here (it would mis-drive the ground pathfinder). The goal-count in
# vanilla_mob_test reflects the 2 declared observable goals; the native flight is the distinctive mechanic
# and IS wired (host-side). Cite net.minecraft.world.entity.animal.happyghast.HappyGhast.registerGoals +
# HappyGhast.createAttributes.
declare_mob(
    name = "vanilla_happy_ghast",
    base_type = "happy_ghast",
    attributes = {
        "max_health": 20.0,        # HappyGhast.createAttributes: MAX_HEALTH 20.0
        "movement_speed": 0.05,    # HappyGhast.createAttributes: MOVEMENT_SPEED 0.05
        "flying_speed": 0.05,      # HappyGhast.createAttributes: FLYING_SPEED 0.05 (the hover accel base)
        "follow_range": 16.0,      # HappyGhast.createAttributes: FOLLOW_RANGE 16.0
        "tempt_range": 16.0,       # HappyGhast.createAttributes: TEMPT_RANGE 16.0
        "camera_distance": 8.0,    # HappyGhast.createAttributes: CAMERA_DISTANCE 8.0
    },
    goals = [
        # @3 HappyGhastFloatGoal [JUMP] — requiresUpdateEveryTick=true. Cite HappyGhast.registerGoals @3.
        goal(
            priority = 3,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @4 TemptGoal.ForNonPathfinders(1.0, HAPPY_GHAST_FOOD, false, 7.0) [MOVE, LOOK] — LOOK-only observable
        # slice (the fly-toward is folded into the native hover, cited). Cite HappyGhast.registerGoals @4.
        goal(
            priority = 4,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_can_use,
            tick = tempt_tick,
            stop = tempt_stop,
            can_continue = tempt_continue,
        ),
    ],
)
