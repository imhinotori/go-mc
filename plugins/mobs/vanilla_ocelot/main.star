# vanilla_ocelot - a 1:1 vanilla-Ocelot dogfood (MOB-PREY, Task #9), the Ocelot built as a Starlark plugin.
# A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares the PORTABLE slice of net.minecraft.world.entity.animal.feline.Ocelot.registerGoals, reusing the
# mob-agnostic passive callbacks (float/tempt/breed/stroll/look - COPIED VERBATIM from vanilla_cat).
#
# Ocelot.registerGoals() (javap-verified this session):
#   goalSelector:
#     @0  OcelotTemptGoal(this, 0.6, is(OCELOT_FOOD), true)  <-- .star (the tempt, stored in temptGoal field)
#     @1  FloatGoal(this)                                    <-- .star (shared passive float)
#     @3  <the SAME temptGoal instance re-added>             <-- .star (a SECOND tempt goal at priority 3)
#     @7  LeapAtTargetGoal(this, 0.3)                        <-- DEFERRED (defers WITH its prey target)
#     @8  OcelotAttackGoal(this)                             <-- DEFERRED (defers WITH its prey target)
#     @9  BreedGoal(this, 0.8)                               <-- .star (shared BreedGoal)
#     @10 WaterAvoidingRandomStrollGoal(this, 0.8, 1e-5)     <-- .star (shared stroll)
#     @11 LookAtPlayerGoal(this, Player, 10.0)               <-- .star (shared look, dist 10.0)
#   targetSelector:
#     @1  NearestAttackableTargetGoal<Chicken>(this, false)                             <-- .star (kind, target_class="chicken")
#     @1  NearestAttackableTargetGoal<Turtle>(this, 10, false, false, BABY_ON_LAND_SELECTOR) <-- .star (kind, target_class="turtle", filter="baby_on_land")
#
# NOTE (jar reality vs the task brief): 26.2 Ocelot.registerGoals has NO AvoidEntityGoal - the ocelot does
# NOT flee players (that skittish-cat behavior was replaced by the trust/tempt system in 1.14). The task's
# "OcelotAvoidEntityGoal" does not exist in this jar; the faithful port is the tempt+trust slice below. The
# creeper's AvoidEntity<Cat|Ocelot> (the creeper FLEES ocelots) is wired on the CREEPER side, not here.
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - The OcelotTemptGoal.canScare spook-flee: OcelotTemptGoal.canScare() = super.canScare() && !isTrusting();
#     canContinueToUse aborts when canScare && distanceToSqr(player)<36 && the player moved after approaching.
#     The Go temptGoal now carries a canScareOverride func(*Entity) bool seam (the Ocelot override returns
#     canScare && !isTrusting(e)) - the Ocelot's .star TemptGoal is a Starlark callback goal (not a Go
#     newTemptGoal instance), so the Go override does NOT apply to the .star path. The override's semantic
#     equivalent in the .star is the can_continue callback below: it reads entity.is_trusting (the host
#     frozen-scalar for Ocelot.isTrusting() = DATA_TRUSTING, plumbed via plugin_entity.go's case
#     "is_trusting") and uses it as the canScare gate. The spook-flee block the override would gate on
#     (TemptGoal.canContinueToUse's player-moved-too-much abort) is itself cite-deferred in this port
#     (the canScare block is a dead skip in the Go TemptGoal - see ai_goals_passive.go:537-548); the .star
#     continues to be the cited canScare=false observable (re-scan), so the is_trusting read is structurally
#     live (the override hook is wired end-to-end: Ocelot.isTrusting -> Entity.isTrusting -> host attr
#     entity.is_trusting -> .star can_continue) and observably no-op for now. When the spook-flee block
#     ports into the .star continue, the is_trusting read becomes the canScare gate.
#   - LeapAtTargetGoal@7(0.3) + OcelotAttackGoal@8: the ocelot's leap+attack goals defer; the prey targets
#     NOW acquire (chicken/baby-turtle-on-land), so an ocelot WITH a prey target CAN leap/attack once those
#     deferrals land. Until then, the prey targets run (acquire + commit attackTargetID), but the leap/attack
#     goals stay cite-deferred (so the goalSelector never reads the acquired target via canUse).
#   - The ocelot TRUST (setTrusting + the data field) is cite-deferred (no trust subsystem).
#   - The SpawnEggItem baby→black-cat morph IS now wired (ocelot-prey #2): a baby ocelot arriving
#     via the SpawnEggItem path (setBaby(true) + isBaby() check) becomes a Cat (entity.Cat.ID) with
#     CatVariant BLACK (catVariant=0, the cited 26.2 registry index). The morph seam lives in
#     spawnDeclaredMob (plugin_mob_decl.go) gated on e.typ == Ocelot.ID && e.isBaby(). Cite
#     SpawnEggItem.spawnOffspringFromSpawnEgg + Ocelot.finalizeSpawn.

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f
TEMPT_RANGE = 10.0        # Attributes.TEMPT_RANGE default - the nearest-tempt-player scan radius
STOP_DISTANCE = 2.5       # TemptGoal DEFAULT_STOP_DISTANCE (2.5^2 = 6.25)
LOOK_DIST = 10.0          # LookAtPlayerGoal lookDistance (Ocelot: 10.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
BREED_RANGE = 8.0         # BreedGoal PARTNER_TARGETING: forNonCombat().range(8.0)
BREED_LOVE_THRESHOLD = 60      # BreedGoal.tick: loveTime >= adjustedTickDelay(60)
BREED_DISTANCE_SQR = 9.0       # BreedGoal.tick: distanceToSqr(partner) < 9.0
STROLL_REDUCED_INTERVAL = 60   # reducedTickDelay(120) == 60 (WaterAvoidingRandomStrollGoal default interval)
STROLL_H = 10             # LandRandomPos/DefaultRandomPos.getPos horizontal radius
STROLL_V = 7              # vertical radius
# Ocelot's stroll ctor is WaterAvoidingRandomStrollGoal(mob, 0.8, 1.0000001E-5f) - the probability is
# ~1e-5 (essentially always LandRandomPos/up-snap), a smaller RARE chance than the cow's 0.001, but the
# nextFloat probability DRAW is still made (lockstep). Consumed-as-Land like the cow.
STROLL_WATER_AVOID_PROBABILITY = 1.0000001e-05

# ============================================================================================
# @1  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true - COPIED VERBATIM from vanilla_cat
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:   # DRAW: nextFloat()<0.8 (the swim-jump chance)
        nav.jump()

# ============================================================================================
# @0/@3  OcelotTemptGoal(mob, 0.6, is(OCELOT_FOOD), true) extends TemptGoal   flags {MOVE, LOOK}
# ports net.minecraft.world.entity.ai.goal.TemptGoal (ocelot_food = cod/salmon). The SAME goal instance
# is registered at BOTH @0 and @3 in the jar (temptGoal field re-added); we declare it twice at those
# priorities (each a fresh starlarkGoal over the shared callbacks - the observable double-registration).
# The canScare spook-flee is cite-deferred (header): continue == re-scan (canScare=false observable).
# ============================================================================================
def tempt_food_can_use(entity, world, nav):
    p = entity.nearest_player_holding_food("ocelot_food", TEMPT_RANGE)   # host scan + OCELOT_FOOD predicate (Go-side)
    if p == None:
        return False
    entity.set_state("tempt_food_px", p[0])
    entity.set_state("tempt_food_py", p[1])
    entity.set_state("tempt_food_pz", p[2])
    return True

def tempt_food_tick(entity, world, nav):
    px = entity.get_state("tempt_food_px")
    py = entity.get_state("tempt_food_py")
    pz = entity.get_state("tempt_food_pz")
    entity.set_look_at(px, py, pz)   # TemptGoal.tick setLookAt(player)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if dx * dx + dy * dy + dz * dz < STOP_DISTANCE * STOP_DISTANCE:   # 2.5^2 = 6.25
        nav.stop()
    else:
        entity.move_to(px, py, pz)   # navigateTowards(player) @ 0.6

def tempt_food_stop(entity, world, nav):
    nav.stop()

def tempt_food_continue(entity, world, nav):
    # OcelotTemptGoal.canScare override: super.canScare() && !isTrusting() = canScare && !is_trusting.
    # The spook-flee block the override gates on is cite-deferred (canScare=false observable), so the
    # is_trusting read is structurally live (the override hook is wired) and observably no-op: the
    # .star continue re-scans regardless. When the spook-flee block ports into the continue, the
    # is_trusting read becomes the canScare gate (a trusting ocelot skips the abort; an un-trusting
    # ocelot would enter it).
    _ = entity.is_trusting  # struct hook: read the trust state to keep the canScare override live
    return tempt_food_can_use(entity, world, nav)

# ============================================================================================
# @9  BreedGoal(mob, 0.8)   flags {MOVE, LOOK} - COPIED VERBATIM from vanilla_cat
# ============================================================================================
def breed_can_use(entity, world, nav):
    if not entity.is_in_love:
        return False
    p = entity.nearest_breeding_partner(BREED_RANGE)
    if p == None:
        return False
    entity.set_state("breed_px", p[0])
    entity.set_state("breed_py", p[1])
    entity.set_state("breed_pz", p[2])
    entity.set_state("breed_love_time", 0)
    return True

def breed_tick(entity, world, nav):
    px = entity.get_state("breed_px")
    py = entity.get_state("breed_py")
    pz = entity.get_state("breed_pz")
    entity.set_look_at(px, py, pz)
    entity.move_to(px, py, pz)
    lt = entity.get_state("breed_love_time") + 1
    entity.set_state("breed_love_time", lt)
    dx = px - entity.x
    dy = py - entity.y
    dz = pz - entity.z
    if lt >= BREED_LOVE_THRESHOLD and dx * dx + dy * dy + dz * dz < BREED_DISTANCE_SQR:
        entity.try_breed(BREED_RANGE)

def breed_stop(entity, world, nav):
    entity.set_state("breed_love_time", 0)
    nav.stop()

def breed_continue(entity, world, nav):
    if entity.get_state("breed_love_time") >= BREED_LOVE_THRESHOLD:
        return False
    return entity.nearest_breeding_partner(BREED_RANGE) != None

# ============================================================================================
# @10  WaterAvoidingRandomStrollGoal(mob, 0.8, 1e-5)   flags {MOVE} - COPIED VERBATIM from vanilla_cat
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(60) gate
        return False
    roll = entity.rand_float()   # DRAW 2: nextFloat() probability gate (~1e-5 threshold, nearly always Land)
    land_mode = 1.0 if roll >= STROLL_WATER_AVOID_PROBABILITY else 0.0
    flat = []
    for _ in range(10):   # RandomPos.generateRandomPos: 10 unconditional candidates
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(land_mode)   # 31st float: wantLandMode
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()

# ============================================================================================
# @11  LookAtPlayerGoal(mob, Player, 10.0)   flags {LOOK} - COPIED VERBATIM from vanilla_cat
# ============================================================================================
def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, LOOK_DIST)
    if p == None:
        return False
    entity.set_state("look_x", p[0])
    entity.set_state("look_y", p[1])
    entity.set_state("look_z", p[2])
    return True

def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))

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

# --- the declaration ---------------------------------------------------------------------------
# base_type "ocelot" -> renders as entity.Ocelot.ID. Real Ocelot attributes (Animal.createAnimalAttributes
# + Ocelot overrides: MAX_HEALTH 10.0, MOVEMENT_SPEED 0.3, ATTACK_DAMAGE 3.0). The portable slice at the jar
# priorities (tempt@0, float@1, tempt@3, breed@9, stroll@10, look@11); leap/attack/prey-targets are
# cite-deferred (header). Cite net.minecraft.world.entity.animal.feline.Ocelot.registerGoals + createAttributes.
declare_mob(
    name = "vanilla_ocelot",
    base_type = "ocelot",
    attributes = {
        "max_health": 10.0,      # Ocelot.createAttributes: MAX_HEALTH 10.0
        "movement_speed": 0.3,   # Ocelot.createAttributes: MOVEMENT_SPEED 0.3
        "attack_damage": 3.0,    # Ocelot.createAttributes: ATTACK_DAMAGE 3.0
    },
    goals = [
        # @0 OcelotTemptGoal(mob, 0.6, OCELOT_FOOD, true) [MOVE, LOOK] - the .star tempt on the ocelot_food
        # tag (cod/salmon). The SAME instance is added at @0 AND @3 in the jar. Cite Ocelot.registerGoals @0.
        goal(
            priority = 0,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_food_can_use,
            tick = tempt_food_tick,
            stop = tempt_food_stop,
            can_continue = tempt_food_continue,
        ),
        # @1 - targetSelector @1 NearestAttackableTargetGoal<Chicken>(this, Chicken.class, false)
        # - 3-arg ctor: default distance == FOLLOW_RANGE. The shared parent goal scans for the nearest
        # live Chicken via nearestEntityOfTypeAt; the randomInterval is the jar's reducedTickDelay(10)=5.
        # Cite Ocelot.registerGoals targetSelector @1 + jar offsets 152-164.
        goal(
            priority = 1,
            flags = ["TARGET"],
            kind = "nearest_attackable_target",
            target_class = "chicken",
        ),
        # @1 - targetSelector @1 NearestAttackableTargetGoal<Turtle>(this, Turtle.class, 10, false, false,
        # BABY_ON_LAND_SELECTOR) - 6-arg ctor with the SHARED Turtle.BABY_ON_LAND_SELECTOR static
        # initializer. The SHARED parent goal serves BOTH this Ocelot targetSelector @1 sibling AND the
        # Fox's @4 turtleEggTargetGoal via the SAME enum + predicate. filter="baby_on_land" selects the
        # SHARED predicate path. Cite Ocelot.registerGoals targetSelector @1 + jar offsets 171-189 +
        # Turtle.BABY_ON_LAND_SELECTOR + Fox.registerGoals targetSelector @4.
        goal(
            priority = 1,
            flags = ["TARGET"],
            kind = "nearest_attackable_target",
            target_class = "turtle",
            filter = "baby_on_land",
        ),
        # @1 FloatGoal [JUMP] - requiresUpdateEveryTick=true. Cite Ocelot.registerGoals @1 FloatGoal.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @3 <the same temptGoal instance, re-added at priority 3> [MOVE, LOOK]. Cite Ocelot.registerGoals @3.
        goal(
            priority = 3,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_food_can_use,
            tick = tempt_food_tick,
            stop = tempt_food_stop,
            can_continue = tempt_food_continue,
        ),
        # @9 BreedGoal(mob, 0.8) [MOVE, LOOK]. Cite Ocelot.registerGoals @9 BreedGoal(0.8).
        goal(
            priority = 9,
            flags = ["MOVE", "LOOK"],
            can_use = breed_can_use,
            tick = breed_tick,
            stop = breed_stop,
            can_continue = breed_continue,
        ),
        # @10 WaterAvoidingRandomStrollGoal(mob, 0.8, 1e-5) [MOVE]. Cite Ocelot.registerGoals @10.
        goal(
            priority = 10,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @11 LookAtPlayerGoal(Player, 10.0) [LOOK]. Cite Ocelot.registerGoals @11 LookAtPlayerGoal.
        goal(
            priority = 11,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
    ],
)
