# vanilla_illusioner -- a 1:1 vanilla-Illusioner dogfood, the SpellcasterIllager built as a Starlark plugin.
# A LITERAL port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p):
# net.minecraft.world.entity.monster.illager.Illusioner extends SpellcasterIllager. It casts a BLINDNESS
# spell on its target and turns itself invisible while casting, fires a bow at range, and casts a MIRROR
# spell (clone images). The bounded port declares: the mob-agnostic PASSIVE goals (float/stroll/look/around)
# as .star callbacks (COPIED VERBATIM from vanilla_evoker); the SpellcasterCastingSpellGoal casting-lock as
# kind=evoker_casting_spell (the SAME SpellcasterIllager base goal, structurally present); the bow as
# kind=ranged_bow_attack; the targets as hurt_by_target + nearest_attackable_target<Player>.
#
# THE SIGNATURE (Go-native, variant_mobs2.go): the BLINDNESS spell + the casting INVISIBILITY are collapsed
# to the observable GAMEPLAY in illusionerAiStep (gated in tickAI on entity.Illusioner.ID) -- when the
# illusioner has a target in the blindness cast range it periodically applies BLINDNESS 400 to the target
# and sets itself invisible for the cast, on the IllusionerBlindnessSpellGoal cadence (warmup 20, interval
# 180, effect duration 400 amp 0). This CITED collapse of the full SpellcasterUseSpellGoal state machine to
# its observable effects keeps the blindness + invisibility gameplay; the MIRROR-image clones (no client
# clone-spawn seam) are cite-deferred (like the evoker fangs/vex spawn). Cite
# Illusioner.registerGoals + Illusioner IllusionerBlindnessSpellGoal.performSpellCasting +
# Illusioner.aiStep (setInvisible while casting) + Illusioner IllusionerMirrorSpellGoal (deferred).
#
# Illusioner.registerGoals() (javap-verified):
#   @0 FloatGoal                                <-- .star
#   @1 SpellcasterCastingSpellGoal              <-- kind=evoker_casting_spell (the casting lock)
#   @4 AvoidEntityGoal<Player>(8.0)             <-- DEFERRED (illusioner-flees-player AvoidEntity)
#   @5 IllusionerMirrorSpellGoal                <-- SIGNATURE (mirror clones deferred; see header)
#   @6 IllusionerBlindnessSpellGoal             <-- SIGNATURE (BLINDNESS 400 + invisibility, illusionerAiStep)
#   @6 RangedBowAttackGoal(speed, interval, 15.0) <-- kind=ranged_bow_attack
#   @8 RandomStrollGoal                         <-- .star
#   @8 LookAtPlayerGoal(Player, 3.0)            <-- .star
#   @8 LookAtPlayerGoal(Mob, 8.0)               <-- .star (around)
#   targetSelector: @1 HurtByTargetGoal + @2 NearestAttackableTargetGoal<Player>
#
# DEFERRED (cite-recorded): AvoidEntityGoal<Player>@4 (no AvoidEntity<Player> flee in scope); the MIRROR
# clones @5 (no clone-spawn seam); the ILLUSIONER_PREPARE_BLINDNESS/MIRROR sounds (client cues). The
# blindness effect + the casting invisibility (the observable spell) are wired via illusionerAiStep. Cite
# net.minecraft.world.entity.monster.illager.Illusioner.registerGoals + Illusioner.createAttributes.

LOOK_DIST = 3.0   # Illusioner LookAtPlayerGoal(Player, 3.0f) lookDistance (the passive look scan)
# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4
FLOAT_JUMP_PROBABILITY = 0.8
STROLL_REDUCED_INTERVAL = 60      # reducedTickDelay(DEFAULT_INTERVAL=120) == positiveCeilDiv(120,2) == 60
STROLL_H = 10                     # (Default/Land)RandomPos.getPos horizontal radius (getPos(mob,10,7))
STROLL_V = 7                      # vertical radius
STROLL_WATER_AVOID_PROBABILITY = 0.001   # WaterAvoidingRandomStrollGoal.PROBABILITY (ravager only)
LOOK_PROBABILITY = 0.02           # LookAtPlayerGoal.DEFAULT_PROBABILITY
LOOK_AROUND_PROBABILITY = 0.02    # RandomLookAroundGoal.canUse: nextFloat() < 0.02f
TWO_PI = 2.0 * 3.141592653589793  # RandomLookAroundGoal.start: d = 6.283185307179586d * nextDouble()

# ============================================================================================
# FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true -- COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:
        nav.jump()

# ============================================================================================
# RandomStrollGoal(mob, speed)   flags {MOVE}   -- DefaultRandomPos.getPos (NO nextFloat probability
# draw; land_mode 0.0 == Default). Cite RandomStrollGoal.getPosition -> DefaultRandomPos.getPos(mob,10,7).
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW: nextInt(reducedTickDelay(120)=60) gate
        return False
    flat = []
    for _ in range(10):   # RandomPos.generateRandomPos: 10 unconditional candidates
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # x offset (DRAW 1 of 3)
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V   # y offset (DRAW 2 of 3) <- y BEFORE z
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H   # z offset (DRAW 3 of 3)
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(0.0)   # 31st float: wantLandMode 0.0 (Default -- RandomStrollGoal, no up-snap probability draw)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()
# ============================================================================================
# LookAtPlayerGoal(mob, Player, dist)   flags {LOOK}   -- COPIED VERBATIM from vanilla_zombie. The look
# distance is per-mob (LOOK_DIST, set in each mob's constants block below).
# ============================================================================================
def look_can_use(entity, world, nav):
    if entity.rand_float() >= LOOK_PROBABILITY:   # DRAW: nextFloat() < probability
        return False
    p = world.nearest_player(entity.x, entity.y, entity.z, LOOK_DIST)
    if p == None:
        return False
    entity.set_state("look_x", p[0])
    entity.set_state("look_y", p[1])
    entity.set_state("look_z", p[2])
    return True

def look_start(entity, world, nav):
    entity.set_state("look_time", 40 + entity.rand_int(40))   # lookTime = adjustedTickDelay(40 + nextInt(40))

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
# RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true -- COPIED VERBATIM
# ============================================================================================
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()           # nextDouble() heading (DRAW 1)
    entity.set_state("rel_x", math.cos(d))
    entity.set_state("rel_z", math.sin(d))
    entity.set_state("look_time", 20 + entity.rand_int(20))   # lookTime = 20 + nextInt(20) (DRAW 2)

def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type illusioner -> renders as entity.Illusioner.ID (id 68; custom = BEHAVIOR, not a new wire type).
# Real Illusioner attributes (Monster.createMonsterAttributes + MOVEMENT_SPEED 0.5 + FOLLOW_RANGE 18.0 +
# MAX_HEALTH 32.0; NO ATTACK_DAMAGE -- the illusioner damage is the bow + the blindness). FOLLOW_RANGE
# bounds the nearest_attackable_target acquire; the spell/bow/casting goals are Go-native (kind=) + the
# Go-native illusionerAiStep signature; float + stroll/look/around are .star callbacks. Cite
# Illusioner.registerGoals + Illusioner.createAttributes.
declare_mob(
    name = "vanilla_illusioner",
    base_type = "illusioner",
    attributes = {
        "movement_speed": 0.5,   # Illusioner.createAttributes: MOVEMENT_SPEED 0.5
        "follow_range": 18.0,    # Illusioner.createAttributes: FOLLOW_RANGE 18.0
        "max_health": 32.0,      # Illusioner.createAttributes: MAX_HEALTH 32.0
    },
    goals = [
        # @0 FloatGoal [JUMP]. Cite Illusioner.registerGoals @0 FloatGoal.
        goal(priority = 0, flags = ["JUMP"], can_use = float_can_use, tick = float_tick, requires_update_every_tick = True),
        # @1 SpellcasterCastingSpellGoal [MOVE, LOOK] -- kind (the casting lock; the SAME SpellcasterIllager
        # base goal the evoker uses). Cite Illusioner.registerGoals @1 SpellcasterCastingSpellGoal.
        goal(priority = 1, flags = ["MOVE", "LOOK"], kind = "evoker_casting_spell"),
        # @6 RangedBowAttackGoal(speed, interval, 15.0) -- kind=ranged_bow_attack (the illusioner fires a bow
        # at range; the bow mainhand is set in finalizeSpawn -- cite-deferred equip, the goal charges+fires an
        # Arrow through the shared ranged path). Cite Illusioner.registerGoals @6 RangedBowAttackGoal.
        goal(priority = 6, flags = ["MOVE", "LOOK"], kind = "ranged_bow_attack"),
        # @8 RandomStrollGoal [MOVE] -- .star (passive). Cite Illusioner.registerGoals @8 RandomStrollGoal.
        goal(priority = 8, flags = ["MOVE"], can_use = stroll_can_use, stop = stroll_stop, can_continue = stroll_continue),
        # @8 LookAtPlayerGoal(Player, 3.0) [LOOK] -- .star (passive). Cite Illusioner.registerGoals @8.
        goal(priority = 8, flags = ["LOOK"], can_use = look_can_use, start = look_start, tick = look_tick, stop = look_stop, can_continue = look_continue),
        # @8 LookAtPlayerGoal(Mob, 8.0)/around [MOVE, LOOK] -- .star (passive). Cite Illusioner.registerGoals @8.
        goal(priority = 8, flags = ["MOVE", "LOOK"], can_use = around_can_use, start = around_start, tick = around_tick, can_continue = around_continue, requires_update_every_tick = True),
        # targetSelector @1 HurtByTargetGoal [TARGET] -- kind. Cite Illusioner.registerGoals targetSelector @1.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player> [TARGET] -- kind. Cite Illusioner.registerGoals targetSelector @2.
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
