# vanilla_enderman — a 1:1 vanilla-EnderMan dogfood (MOB-HOST-08), the Enderman built as a Starlark plugin.
# A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p). It
# declares the PORTABLE slice of net.minecraft.world.entity.monster.EnderMan.registerGoals, reusing the
# proven Go goal runtime: float + the passive stroll/look/around are the SAME mob-agnostic .star callbacks
# the cow/zombie run (COPIED VERBATIM), the COMBAT goals are the Go-native 35-01 kinds, and the TELEPORT
# (daylight-flee + hurt-dodge) is the Go-native endermanAiStep/endermanHurtTeleport (ai_goals_enderman.go).
#
# EnderMan.registerGoals() (javap-verified this session):
#   goalSelector:
#     @0  FloatGoal                              <-- .star (shared passive float)
#     @1  EndermanFreezeWhenLookedAt            <-- kind="enderman_freeze_when_looked_at" (gaze-freeze, BUILT)
#     @2  MeleeAttackGoal(1.0)                   <-- kind="melee_attack" (the CORE melee)
#     @7  WaterAvoidingRandomStrollGoal(1.0)     <-- .star (shared stroll)
#     @8  LookAtPlayerGoal(Player, 8.0)          <-- .star (shared look)
#     @8  RandomLookAroundGoal                   <-- .star (shared around)
#     @10 EndermanLeaveBlockGoal                <-- kind="enderman_leave_block" (block put-down, BUILT)
#     @11 EndermanTakeBlockGoal                 <-- kind="enderman_take_block" (block pick-up, BUILT)
#   targetSelector:
#     @1  EndermanLookForPlayerGoal(isAngryAt)  <-- kind="enderman_look_for_player" (gaze-aggro, BUILT)
#     @2  HurtByTargetGoal                       <-- kind="hurt_by_target"
#     @3  NearestAttackableTargetGoal<Endermite> <-- DEFERRED (no Endermite entity)
#     @4  ResetUniversalAngerTargetGoal         <-- DEFERRED (no universal-anger subsystem)
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - The GAZE subsystem (EndermanFreezeWhenLookedAt@1 + EndermanLookForPlayerGoal@1 "look at the enderman
#     to aggro it") is now BUILT as the Go-native kinds enderman_freeze_when_looked_at +
#     enderman_look_for_player (ai_goals_enderman_gaze.go), reading the player's tracked yaw/pitch as the
#     view vector (Entity.calculateViewVector) for the isBeingStaredBy dot-product cone (coneSize 0.025,
#     distance-adjusted). This REPLACES the earlier nearest_attackable_target substitution. Cite
#     EnderMan.isBeingStaredBy / LivingEntity.isLookingAtMe. Residual gaze deferrals (cited in
#     ai_goals_enderman_gaze.go): the hasLineOfSight raycast (no LoS subsystem), the DATA_CREEPY/
#     DATA_STARED_AT synched render flags, and the in-combat teleport-management branch of
#     LookForPlayerGoal.tick (folded into the Go-native endermanAiStep/endermanHurtTeleport teleport).
#   - Block-carry (LeaveBlock@10 / TakeBlock@11) is now BUILT as the Go-native kinds enderman_leave_block +
#     enderman_take_block (ai_goals_enderman_carry.go): the enderman picks up a #minecraft:enderman_holdable
#     block (setCarriedBlock, DATA_CARRY_STATE index 16 OPTIONAL_BLOCK_STATE) and later puts it back down,
#     both gated on the MOB_GRIEFING gamerule + the vanilla nextInt(reducedTickDelay(20|2000)) rolls. Cite
#     EnderMan$EndermanTakeBlockGoal / EnderMan$EndermanLeaveBlockGoal. Residual carry deferrals (cited in
#     ai_goals_enderman_carry.go): the take-goal clip/reachability raycast, the leave-goal
#     updateFromNeighbourShapes + canSurvive, and the BLOCK_DESTROY/BLOCK_PLACE game-events (no clip / neighbour-
#     shape / support / game-event subsystems in v1). Endermite target@3, ResetUniversalAnger@4: no endermite /
#     universal-anger subsystems in v1.
#   - The TELEPORT is Go-native (endermanAiStep daylight-flee + endermanHurtTeleport dodge, ai_goals_enderman.go)
#     — the distinctive enderman mechanic IS wired; only the gaze + block-carry flavor is deferred.

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4
FLOAT_JUMP_PROBABILITY = 0.8
STROLL_REDUCED_INTERVAL = 60
STROLL_H = 10
STROLL_V = 7
STROLL_WATER_AVOID_PROBABILITY = 0.001
LOOK_DIST = 8.0            # LookAtPlayerGoal lookDistance (EnderMan: 8.0f)
LOOK_PROBABILITY = 0.02
LOOK_AROUND_PROBABILITY = 0.02
TWO_PI = 2.0 * 3.141592653589793

# ============================================================================================
# @0  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true — COPIED VERBATIM from vanilla_cow
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:
        nav.jump()

# ============================================================================================
# @7  WaterAvoidingRandomStrollGoal(mob, 1.0)   flags {MOVE}   — COPIED VERBATIM from vanilla_zombie
# ============================================================================================
def stroll_can_use(entity, world, nav):
    if entity.rand_int(STROLL_REDUCED_INTERVAL) != 0:
        return False
    roll = entity.rand_float()
    land_mode = 1.0 if roll >= STROLL_WATER_AVOID_PROBABILITY else 0.0
    flat = []
    for _ in range(10):
        xt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H
        yt = entity.rand_int(2 * STROLL_V + 1) - STROLL_V
        zt = entity.rand_int(2 * STROLL_H + 1) - STROLL_H
        flat.append(entity.x + xt)
        flat.append(entity.y + yt)
        flat.append(entity.z + zt)
    flat.append(land_mode)
    nav.path_to(*flat)
    return True

def stroll_continue(entity, world, nav):
    return nav.has_path()

def stroll_stop(entity, world, nav):
    nav.stop()

# ============================================================================================
# @8  LookAtPlayerGoal(mob, Player, 8.0)   flags {LOOK}   — COPIED VERBATIM from vanilla_zombie
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
# @8  RandomLookAroundGoal(mob)   flags {MOVE, LOOK}   requiresUpdateEveryTick=true — COPIED VERBATIM
# ============================================================================================
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

def around_start(entity, world, nav):
    d = TWO_PI * entity.rand_double()
    entity.set_state("rel_x", math.cos(d))
    entity.set_state("rel_z", math.sin(d))
    entity.set_state("look_time", 20 + entity.rand_int(20))

def around_continue(entity, world, nav):
    return entity.get_state("look_time") >= 0

def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)

# --- the declaration ---------------------------------------------------------------------------
# base_type "enderman" -> renders as entity.Enderman.ID. Real Enderman attributes
# (Monster.createMonsterAttributes + EnderMan overrides: MAX_HEALTH 40, MOVEMENT_SPEED 0.3, ATTACK_DAMAGE 7,
# FOLLOW_RANGE 64). The combat goals are Go-native (kind=); float + stroll/look/around are .star callbacks;
# the teleport is Go-native (endermanAiStep/endermanHurtTeleport). Cite EnderMan.registerGoals + createAttributes.
declare_mob(
    name = "vanilla_enderman",
    base_type = "enderman",
    attributes = {
        "max_health": 40.0,      # EnderMan.createAttributes: MAX_HEALTH 40.0
        "movement_speed": 0.3,   # EnderMan.createAttributes: MOVEMENT_SPEED 0.3
        "attack_damage": 7.0,    # EnderMan.createAttributes: ATTACK_DAMAGE 7.0 (the melee damage)
        "follow_range": 64.0,    # EnderMan.createAttributes: FOLLOW_RANGE 64.0 (the long acquire scan)
    },
    goals = [
        # @0 FloatGoal [JUMP] — requiresUpdateEveryTick=true. Cite EnderMan.registerGoals @0 FloatGoal.
        goal(
            priority = 0,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @1 EndermanFreezeWhenLookedAt [JUMP, MOVE] — kind="enderman_freeze_when_looked_at". Freezes the
        # enderman (stops its nav, stares back) while its player target is staring at it within 16 blocks.
        # Cite EnderMan.registerGoals @1 EndermanFreezeWhenLookedAt.
        goal(priority = 1, flags = ["JUMP", "MOVE"], kind = "enderman_freeze_when_looked_at"),
        # @2 MeleeAttackGoal(mob, 1.0, false) [MOVE] — kind="melee_attack". Cite EnderMan.registerGoals @2.
        goal(priority = 2, flags = ["MOVE"], kind = "melee_attack"),
        # @7 WaterAvoidingRandomStrollGoal(mob, 1.0) [MOVE] — .star. Cite EnderMan.registerGoals @7.
        goal(
            priority = 7,
            flags = ["MOVE"],
            can_use = stroll_can_use,
            stop = stroll_stop,
            can_continue = stroll_continue,
        ),
        # @8 LookAtPlayerGoal(Player, 8.0) [LOOK] — .star. Cite EnderMan.registerGoals @8 LookAtPlayerGoal.
        goal(
            priority = 8,
            flags = ["LOOK"],
            can_use = look_can_use,
            start = look_start,
            tick = look_tick,
            stop = look_stop,
            can_continue = look_continue,
        ),
        # @8 RandomLookAroundGoal [MOVE, LOOK] — .star, requiresUpdateEveryTick=true. Cite EnderMan.registerGoals @8.
        goal(
            priority = 8,
            flags = ["MOVE", "LOOK"],
            can_use = around_can_use,
            start = around_start,
            tick = around_tick,
            can_continue = around_continue,
            requires_update_every_tick = True,
        ),
        # @10 EndermanLeaveBlockGoal [] — kind="enderman_leave_block". A carrying enderman occasionally puts
        # its carried block back down (nextInt(reducedTickDelay(2000))==0 + a placeable random cell). Cite
        # EnderMan.registerGoals @10 EndermanLeaveBlockGoal (ai_goals_enderman_carry.go).
        goal(priority = 10, flags = [], kind = "enderman_leave_block"),
        # @11 EndermanTakeBlockGoal [] — kind="enderman_take_block". A not-carrying enderman occasionally picks
        # up a nearby #minecraft:enderman_holdable block (nextInt(reducedTickDelay(20))==0). Cite
        # EnderMan.registerGoals @11 EndermanTakeBlockGoal (ai_goals_enderman_carry.go).
        goal(priority = 11, flags = [], kind = "enderman_take_block"),
        # targetSelector @1 EndermanLookForPlayerGoal(isAngryAt) [TARGET] — kind="enderman_look_for_player".
        # The REAL gaze-aggro: a player aggroes the enderman only by LOOKING at it (isBeingStaredBy, the
        # coneSize-0.025 distance-adjusted view-vector dot test) or by having already angered it (isAngryAt),
        # within FOLLOW_RANGE 64. Cite EnderMan.registerGoals targetSelector @1 EndermanLookForPlayerGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "enderman_look_for_player"),
        # targetSelector @2 HurtByTargetGoal(mob) [TARGET] — kind="hurt_by_target". Cite EnderMan.registerGoals @2.
        goal(priority = 2, flags = ["TARGET"], kind = "hurt_by_target"),
    ],
)
