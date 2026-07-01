# vanilla_ravager — a 1:1 vanilla-Ravager dogfood (RAIDER Task), the raid beast built as a Starlark plugin.
# A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares net.minecraft.world.entity.monster.Ravager.registerGoals, reusing the proven Go goal runtime:
# FloatGoal + the passive wa_stroll/look/around are the SAME mob-agnostic .star callbacks the zombie runs
# (COPIED VERBATIM); the melee is the Go-native melee_attack kind; the ROAR/STUN/attack state machine + the
# roar() AoE is the Go-native ravagerAiStep per-type hook (ai_goals_ravager.go); targets are
# hurt_by_target/nearest_attackable_target; patrol is long_distance_patrol. In 26.2 the Ravager uses the
# PLAIN MeleeAttackGoal (NO RavagerMeleeAttackGoal inner class, NO charge — verified this session).
#
# Ravager.registerGoals() (javap-verified, net.minecraft.world.entity.monster.Ravager):
#   super.registerGoals()  <-- PatrollingMonster @4 LongDistancePatrolGoal (WIRED); Raider @1/@3/@4/@5
#     (ObtainRaidLeaderBanner/PathfindToRaid/RaiderMoveThroughVillage/RaiderCelebration) <-- DEFERRED (POI)
#   goalSelector:
#     @0 FloatGoal(this)                                          <-- .star
#     @4 MeleeAttackGoal(this, 1.0, true)                         <-- kind="melee_attack" (CORE melee; attackTick via ravagerAiStep)
#     @5 WaterAvoidingRandomStrollGoal(this, 0.4)                 <-- .star (wa_stroll)
#     @6 LookAtPlayerGoal(this, Player, 6.0)                      <-- .star (look, dist 6)
#     @10 LookAtPlayerGoal(this, Mob, 8.0)                        <-- .star (around)
#   targetSelector:
#     @2 HurtByTargetGoal(this, Raider).setAlertOthers()         <-- kind="hurt_by_target"
#     @3 NearestAttackableTargetGoal<Player>(this, true)         <-- kind="nearest_attackable_target"
#     @4 NearestAttackableTargetGoal<AbstractVillager/IronGolem> <-- DEFERRED (entities absent)
#
# THE ROAR/STUN (Go-native ravagerAiStep, ai_goals_ravager.go): doHurtTarget sets attackTick=10 (+event 4);
# aiStep lerps MOVEMENT_SPEED (immobile->0 / target?0.35:0.3), counts roarTick (roar() AoE at 10: every
# LivingEntity in BB.inflate(4.0) -> non-illager hurt 6.0 mobAttack, non-player strongKnockback), attackTick,
# stunnedTick (at 0 re-arms roarTick=20). NO RNG on the ravager stream. Cite Ravager.aiStep/roar/doHurtTarget.
#
# DEFERRED (cite-recorded, NEVER silently dropped): the Raider village/banner goals (no POI); the villager/
# iron-golem targets (entities absent); the STUN TRIGGER (blockedByItem shield-block — no shield subsystem,
# so a ravager is never stunned; the stun countdown + roar re-arm are ported); the LEAF-TRAMPLE block-break
# (no block-break subsystem; NO RNG, no desync); the stunEffect particle (client visual). The CORE hunt +
# melee + the ROAR/STUN state machine (the phase goal) is fully wired.

LOOK_DIST = 6.0   # Ravager LookAtPlayerGoal(Player, 6.0f) lookDistance

# math is a host-predeclared global (starlark math.Module) — used by RandomLookAroundGoal.start cos/sin.
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
# FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true — COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:
        nav.jump()

# ============================================================================================
# WaterAvoidingRandomStrollGoal(mob, speed)   flags {MOVE}   — the nextFloat water-avoid probability
# draw (Ravager @5, speed 0.4). Cite WaterAvoidingRandomStrollGoal.getPosition.
# ============================================================================================
def wa_stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:   # DRAW 1: nextInt(60) gate
        return False
    roll = entity.rand_float()   # DRAW 2: nextFloat() probability gate (WaterAvoidingRandomStrollGoal)
    land_mode = 1.0 if roll >= STROLL_WATER_AVOID_PROBABILITY else 0.0
    flat = []
    for _ in range(10):
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(land_mode)   # 31st float: wantLandMode (1.0 Land / 0.0 Default)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()

# ============================================================================================
# LookAtPlayerGoal(mob, Player, dist)   flags {LOOK}   — COPIED VERBATIM from vanilla_zombie. The look
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
# RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
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
# base_type "ravager" -> renders as entity.Ravager.ID (id 109). Real Ravager attributes
# (Monster.createMonsterAttributes + MAX_HEALTH 100, MOVEMENT_SPEED 0.3, KNOCKBACK_RESISTANCE 0.75,
# ATTACK_DAMAGE 12, ATTACK_KNOCKBACK 1.5, FOLLOW_RANGE 32, STEP_HEIGHT 1.0). Melee + targets + patrol are
# Go-native (kind=); the roar/stun is ravagerAiStep; float + wa_stroll/look/around are .star callbacks.
# Cite Ravager.registerGoals + Ravager.createAttributes.
declare_mob(
    name = "vanilla_ravager",
    base_type = "ravager",
    attributes = {
        "max_health": 100.0,             # MAX_HEALTH 100.0
        "movement_speed": 0.3,           # MOVEMENT_SPEED 0.3 (lerped toward 0.35 when chasing, ravagerAiStep)
        "knockback_resistance": 0.75,    # KNOCKBACK_RESISTANCE 0.75
        "attack_damage": 12.0,           # ATTACK_DAMAGE 12.0 (the melee damage)
        "attack_knockback": 1.5,         # ATTACK_KNOCKBACK 1.5
        "follow_range": 32.0,            # FOLLOW_RANGE 32.0 (the acquire scan bound)
        "step_height": 1.0,              # STEP_HEIGHT 1.0
    },
    goals = [
        # @0 FloatGoal [JUMP]. Cite Ravager.registerGoals @0 FloatGoal.
        goal(priority = 0, flags = ["JUMP"], can_use = float_can_use, tick = float_tick, requires_update_every_tick = True),
        # @4 (inherited) LongDistancePatrolGoal [MOVE] — kind. Cite PatrollingMonster @4.
        goal(priority = 4, flags = ["MOVE"], kind = "long_distance_patrol"),
        # @4 MeleeAttackGoal(this, 1.0, true) [MOVE] — kind. Cite Ravager.registerGoals @4.
        goal(priority = 4, flags = ["MOVE"], kind = "melee_attack"),
        # @5 WaterAvoidingRandomStrollGoal(this, 0.4) [MOVE] — .star (wa_stroll). Cite Ravager.registerGoals @5.
        goal(priority = 5, flags = ["MOVE"], can_use = wa_stroll_can_use, stop = stroll_stop, can_continue = stroll_continue),
        # @6 LookAtPlayerGoal(Player, 6.0) [LOOK] — .star. Cite Ravager.registerGoals @6.
        goal(priority = 6, flags = ["LOOK"], can_use = look_can_use, start = look_start, tick = look_tick, stop = look_stop, can_continue = look_continue),
        # @10 LookAtPlayerGoal(Mob, 8.0)/around [MOVE, LOOK] — .star. Cite Ravager.registerGoals @10.
        goal(priority = 10, flags = ["MOVE", "LOOK"], can_use = around_can_use, start = around_start, tick = around_tick, can_continue = around_continue, requires_update_every_tick = True),
        # targetSelector @2 HurtByTargetGoal(Raider) [TARGET] — kind. Cite Ravager.registerGoals targetSelector @2.
        goal(priority = 2, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @3 NearestAttackableTargetGoal<Player>(true) [TARGET] — kind. Cite Ravager.registerGoals targetSelector @3.
        goal(priority = 3, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
