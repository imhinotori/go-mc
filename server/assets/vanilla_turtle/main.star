# vanilla_turtle - a 1:1 vanilla-Turtle dogfood (MOB-PREY, Task #9), the Turtle built as a Starlark plugin.
# A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares the PORTABLE slice of net.minecraft.world.entity.animal.turtle.Turtle.registerGoals, reusing the
# mob-agnostic passive callbacks (panic/breed/tempt/look - COPIED VERBATIM from vanilla_cow) PLUS the turtle
# stroll (a plain RandomStrollGoal, interval 100 - NOT the WaterAvoiding variant).
#
# Turtle.registerGoals() (javap-verified this session):
#   @0 TurtlePanicGoal(this, 1.2)          <-- .star (extends PanicGoal - the shared panic)
#   @1 TurtleBreedGoal(this, 1.0)          <-- .star (extends BreedGoal - the shared breed)
#   @1 TurtleLayEggGoal(this, 1.0)         <-- BUILT (kind=turtle_lay_egg: TURTLE_EGG block + homePos subsystem)
#   @2 TemptGoal(this, 1.1, is(TURTLE_FOOD), false)  <-- .star (shared TemptGoal; turtle_food = seagrass)
#   @3 TurtleGoToWaterGoal(this, 1.0)      <-- BUILT (kind=turtle_goto_water: MoveToBlockGoal find-water)
#   @4 TurtleGoHomeGoal(this, 1.0)         <-- BUILT (kind=turtle_go_home: homePos-memory subsystem)
#   @7 TurtleTravelGoal(this, 1.0)         <-- BUILT (kind=turtle_travel: in-water deep-wander)
#   @8 LookAtPlayerGoal(this, Player, 8.0) <-- .star (shared look, dist 8.0)
#   @9 TurtleRandomStrollGoal(this, 1.0, 100) <-- .star (extends RandomStrollGoal, interval 100)
#
# BUILT this batch (the water-nav trio + egg-lay, kind-goals routed to Go-native ports in
# server/ai_goals_turtle.go — 1:1 with Turtle.registerGoals, RNG in lockstep via the mob's seeded rng):
#   - TurtleLayEggGoal@1 (kind=turtle_lay_egg): the minecraft:turtle_egg block (codegen'd, level/block) +
#     the sand-home-pos (hasEgg / homePos / layEggCounter) subsystem now EXIST. The turtle digs sand near
#     its scented home and places TURTLE_EGG (EGGS = nextInt(4)+1). hasEgg is still set only by a future
#     breed-override (the .star breed keeps spawning a baby — cited below); the lay/home/egg machinery is live.
#   - TurtleGoToWaterGoal@3 / TurtleGoHomeGoal@4 / TurtleTravelGoal@7 (kind=turtle_goto_water/go_home/travel):
#     the home-pos memory + the MoveToBlockGoal find-water/find-sand scans are BUILT. The underlying node
#     evaluator is WALKABLE-only (AmphibiousPathNavigation water-traversal is the ONE cited reduction —
#     sibling of navigation.go's canFloat deferral; the GOAL logic + RNG + targets are 1:1). See
#     server/ai_goals_turtle.go's WATER-NAV REDUCTION note.
#
# STILL DEFERRED (cite-recorded, NEVER silently dropped):
#   - TurtleBreedGoal.breed's hasEgg override: vanilla's turtle breed sets hasEgg=true instead of spawning a
#     baby. The .star keeps the SHARED breed (spawns a baby) — flipping to the egg-lay path is a breed-hook
#     override (a Plan-C concern); the hasEgg consumer goals are built so it slots in. Cite TurtleBreedGoal.breed.
#   - The turtle is prey for the fox/ocelot/wolf (NonTameRandomTarget<Turtle>); those PREDATOR target legs
#     are wired on the predator side (ocelot targetSelector), cite-deferred where the predator's prey-selector
#     is not yet built. The turtle itself has NO targetSelector (it never attacks).

# --- constants (jar-confirmed) -----------------------------------------------------------------
PANIC_H = 5               # PanicGoal.findRandomPosition: DefaultRandomPos.getPos(mob, 5, 4) horizontal radius
PANIC_V = 4               # vertical radius
TEMPT_RANGE = 10.0        # Attributes.TEMPT_RANGE default - the nearest-tempt-player scan radius
STOP_DISTANCE = 2.5       # TemptGoal DEFAULT_STOP_DISTANCE (2.5^2 = 6.25)
LOOK_DIST = 8.0           # LookAtPlayerGoal lookDistance (Turtle: 8.0f)
LOOK_PROBABILITY = 0.02   # LookAtPlayerGoal.DEFAULT_PROBABILITY
BREED_RANGE = 8.0         # BreedGoal PARTNER_TARGETING: forNonCombat().range(8.0)
BREED_LOVE_THRESHOLD = 60      # BreedGoal.tick: loveTime >= adjustedTickDelay(60)
BREED_DISTANCE_SQR = 9.0       # BreedGoal.tick: distanceToSqr(partner) < 9.0
# TurtleRandomStrollGoal extends RandomStrollGoal(mob, 1.0, 100): interval 100 -> reducedTickDelay(100) =
# Mth.positiveCeilDiv(100, 2) = 50 (canUse rolls nextInt(50)). A PLAIN RandomStrollGoal (NOT WaterAvoiding),
# so getPosition() = LandRandomPos.getPos(mob, 10, 7) with NO probability nextFloat roll - always up-snap.
STROLL_REDUCED_INTERVAL = 50   # reducedTickDelay(100)
STROLL_H = 10             # LandRandomPos.getPos horizontal radius (getPos(mob, 10, 7))
STROLL_V = 7              # vertical radius

# ============================================================================================
# @0  TurtlePanicGoal(mob, 1.2) extends PanicGoal   flags {MOVE} - COPIED VERBATIM from vanilla_cow
# ============================================================================================
def panic_can_use(entity, world, nav):
    if not (entity.has_last_damage and entity.damage_in_tag("panic_causes")):   # shouldPanic: ZERO draws if false
        return False
    flat = []
    for _ in range(10):   # DefaultRandomPos.getPos: 10 unconditional candidates
        xt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * PANIC_V + 1) - PANIC_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * PANIC_H + 1) - PANIC_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(0.0)   # 31st float: landMode = 0.0 (DefaultRandomPos, no up-snap)
    nav.path_to(*flat)
    return True

def panic_stop(entity, world, nav):
    nav.stop()

def panic_continue(entity, world, nav):
    return nav.has_path()

# ============================================================================================
# @2  TemptGoal(mob, 1.1, is(ItemTags.TURTLE_FOOD), false)   flags {MOVE, LOOK} - shared TemptGoal
# ============================================================================================
def tempt_food_can_use(entity, world, nav):
    p = entity.nearest_player_holding_food("turtle_food", TEMPT_RANGE)   # host scan + TURTLE_FOOD predicate (Go-side)
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
        entity.move_to(px, py, pz)   # navigateTowards(player) @ 1.1

def tempt_food_stop(entity, world, nav):
    nav.stop()

def tempt_food_continue(entity, world, nav):
    return tempt_food_can_use(entity, world, nav)

# ============================================================================================
# @1  TurtleBreedGoal(mob, 1.0) extends BreedGoal   flags {MOVE, LOOK} - COPIED VERBATIM from vanilla_cow
# ============================================================================================
# NOTE: TurtleBreedGoal.breed() sets hasEgg=true instead of spawning a baby (the egg-lay path). Since the
# TURTLE_EGG block is cite-deferred (header), try_breed keeps the shared breed (variant + XP draws); the
# hasEgg flag + egg placement defer. The COURTSHIP (approach + loveTime) lands faithfully.
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
# @8  LookAtPlayerGoal(mob, Player, 8.0)   flags {LOOK} - COPIED VERBATIM from vanilla_cow
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

# ============================================================================================
# @9  TurtleRandomStrollGoal(mob, 1.0, 100) extends RandomStrollGoal   flags {MOVE}
# ports net.minecraft.world.entity.ai.goal.RandomStrollGoal (interval 100). PLAIN RandomStrollGoal (NOT
# WaterAvoiding), so getPosition() = LandRandomPos.getPos(mob, 10, 7): NO probability nextFloat roll - just
# the interval gate then the 10-candidate loop (always up-snap = land_mode 1.0).
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(reducedTickDelay(100)=50) gate
        return False
    # PLAIN RandomStrollGoal.getPosition = LandRandomPos.getPos(mob, 10, 7) - NO nextFloat probability draw.
    flat = []
    for _ in range(10):   # RandomPos.generateRandomPos: 10 unconditional candidates
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x offset (DRAW order 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y offset (DRAW order 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z offset (DRAW order 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(1.0)   # 31st float: land_mode = 1.0 (LandRandomPos, up-snap) - always Land (no water-avoid roll)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()

# --- the declaration ---------------------------------------------------------------------------
# base_type "turtle" -> renders as entity.Turtle.ID. Real Turtle attributes (Animal.createAnimalAttributes
# + Turtle overrides: MAX_HEALTH 30.0, MOVEMENT_SPEED 0.25, STEP_HEIGHT 1.0). The portable slice at the jar
# priorities (panic@0, breed@1, tempt@2, look@8, stroll@9); the water-nav trio + egg-lay are cite-deferred
# (header). Cite net.minecraft.world.entity.animal.turtle.Turtle.registerGoals + Turtle.createAttributes.
declare_mob(
    name = "vanilla_turtle",
    base_type = "turtle",
    attributes = {
        "max_health": 30.0,      # Turtle.createAttributes: MAX_HEALTH 30.0
        "movement_speed": 0.25,  # Turtle.createAttributes: MOVEMENT_SPEED 0.25
        # STEP_HEIGHT 1.0 is on the turtleSupplier (defaults.go), not a plugin override - it has no
        # friendly-name alias and the supplier already applies it (Turtle.createAttributes STEP_HEIGHT 1.0).
    },
    goals = [
        # @0 TurtlePanicGoal(mob, 1.2) [MOVE] - extends PanicGoal (shared). Cite Turtle.registerGoals @0.
        goal(
            priority = 0,
            flags = ["MOVE"],
            can_use = panic_can_use,
            stop = panic_stop,
            can_continue = panic_continue,
        ),
        # @1 TurtleBreedGoal(mob, 1.0) [MOVE, LOOK] - extends BreedGoal (shared). Cite Turtle.registerGoals @1.
        goal(
            priority = 1,
            flags = ["MOVE", "LOOK"],
            can_use = breed_can_use,
            tick = breed_tick,
            stop = breed_stop,
            can_continue = breed_continue,
        ),
        # @1 TurtleLayEggGoal(mob, 1.0) [MOVE, JUMP] - kind="turtle_lay_egg" (MoveToBlockGoal: find sand
        # near home, dig, place the TURTLE_EGG block). Added AFTER breed so the @1 tie keeps breed's
        # precedence (vanilla registers breed@1 then layEgg@1). Cite Turtle.registerGoals @1 TurtleLayEggGoal.
        goal(priority = 1, flags = ["MOVE", "JUMP"], kind = "turtle_lay_egg"),
        # @2 TemptGoal(mob, 1.1, TURTLE_FOOD, false) [MOVE, LOOK] - the .star tempt on the turtle_food tag
        # (seagrass). Cite Turtle.registerGoals @2 TemptGoal.
        goal(
            priority = 2,
            flags = ["MOVE", "LOOK"],
            can_use = tempt_food_can_use,
            tick = tempt_food_tick,
            stop = tempt_food_stop,
            can_continue = tempt_food_continue,
        ),
        # @3 TurtleGoToWaterGoal(mob, 1.0) [MOVE, JUMP] - kind="turtle_goto_water" (MoveToBlockGoal: find the
        # nearest WATER cell within 24 and walk to it). Cite Turtle.registerGoals @3 TurtleGoToWaterGoal.
        goal(priority = 3, flags = ["MOVE", "JUMP"], kind = "turtle_goto_water"),
        # @4 TurtleGoHomeGoal(mob, 1.0) [MOVE] - kind="turtle_go_home" (bias toward homePos to lay). Cite
        # Turtle.registerGoals @4 TurtleGoHomeGoal.
        goal(priority = 4, flags = ["MOVE"], kind = "turtle_go_home"),
        # @7 TurtleTravelGoal(mob, 1.0) [MOVE] - kind="turtle_travel" (in-water deep-wander). Cite
        # Turtle.registerGoals @7 TurtleTravelGoal.
        goal(priority = 7, flags = ["MOVE"], kind = "turtle_travel"),
        # @8 LookAtPlayerGoal(Player, 8.0) [LOOK] - .star. Cite Turtle.registerGoals @8 LookAtPlayerGoal.
        goal(
            priority = 8,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @9 TurtleRandomStrollGoal(mob, 1.0, 100) [MOVE] - plain RandomStrollGoal, interval 100. Cite
        # Turtle.registerGoals @9 TurtleRandomStrollGoal.
        goal(
            priority = 9,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
    ],
)
