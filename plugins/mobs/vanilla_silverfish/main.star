# vanilla_silverfish — a 1:1 vanilla-Silverfish dogfood (MOB-HOST-05), the Silverfish built as a Starlark
# plugin. A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar,
# javap -c -p). It declares net.minecraft.world.entity.monster.Silverfish.registerGoals, reusing the
# proven Go goal runtime: the FloatGoal is the SAME mob-agnostic .star callback the cow/pig run (COPIED
# VERBATIM), and the COMBAT goals are the Go-native 35-01 ports via the kind= seam.
#
# Silverfish.registerGoals() (javap-verified this session):
#   goalSelector:
#     @1 FloatGoal(this)                          <-- .star (the shared passive float — swim-jump)
#     @1 ClimbOnTopOfPowderSnowGoal(this, level)  <-- DEFERRED (no powder-snow subsystem in v1)
#     @3 SilverfishWakeUpFriendsGoal(this)        <-- DEFERRED (no stone-infest / wake-friends subsystem)
#     @4 MeleeAttackGoal(this, 1.0, false)        <-- kind="melee_attack" (the 35-01 meleeAttackGoal; CORE melee)
#     @5 SilverfishMergeWithStoneGoal(this)       <-- DEFERRED (no stone-infest / hide subsystem)
#   targetSelector:
#     @1 HurtByTargetGoal(this).setAlertOthers()  <-- kind="hurt_by_target" (alertOthers cite-deferred, no-op)
#     @2 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true) <-- kind="nearest_attackable_target"
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - ClimbOnTopOfPowderSnowGoal@1: no powder-snow block behavior in v1 — a cited no-op goal.
#   - SilverfishWakeUpFriendsGoal@3 / SilverfishMergeWithStoneGoal@5: the stone-infestation subsystem
#     (infested blocks, hide-in-stone, summon-friends-on-hurt) does not exist in v1. The CORE hunt+melee
#     (the phase goal) is fully wired; the infest bits land with that subsystem.
#   - HurtByTargetGoal.setAlertOthers(): no alert-burst in v1 — the base hurtByTargetGoal carries no
#     alertSameType (cited no-op, 35-01 ai_goals_target.go), the same treatment the zombie uses.
#   - NearestAttackableTargetGoal's mustSee (line-of-sight): the cited "visible" stub (no sensing).

# --- constants (jar-confirmed) -----------------------------------------------------------------
FLUID_JUMP_THRESHOLD = 0.4     # Entity.getFluidJumpThreshold (the silverfish eye is above 0.4 -> 0.4)
FLOAT_JUMP_PROBABILITY = 0.8   # FloatGoal.tick: getRandom().nextFloat() < 0.8f (the swim-jump chance)

# ============================================================================================
# @1  FloatGoal(mob)   flags {JUMP}   requiresUpdateEveryTick=true — COPIED VERBATIM from vanilla_cow
# ports net.minecraft.world.entity.ai.goal.FloatGoal (the SAME goal the cow/pig run — mob-agnostic)
# ============================================================================================
def float_can_use(entity, world, nav):
    return (entity.in_water and entity.fluid_height > FLUID_JUMP_THRESHOLD) or entity.in_lava

def float_tick(entity, world, nav):
    if entity.rand_float() < FLOAT_JUMP_PROBABILITY:   # DRAW: nextFloat()<0.8 (the swim-jump chance)
        nav.jump()

# --- the declaration ---------------------------------------------------------------------------
# base_type "silverfish" -> renders as entity.Silverfish.ID. Real Silverfish attributes
# (Monster.createMonsterAttributes + Silverfish overrides) seeded with the jar values
# (Silverfish.createAttributes: MAX_HEALTH 8.0, MOVEMENT_SPEED 0.25, ATTACK_DAMAGE 1.0). The combat goals
# are the Go-native 35-01 ports (kind=); the FloatGoal is a .star callback. Cite Silverfish.registerGoals
# + Silverfish.createAttributes.
declare_mob(
    name = "vanilla_silverfish",
    base_type = "silverfish",
    attributes = {
        "max_health": 8.0,       # Silverfish.createAttributes: MAX_HEALTH 8.0
        "movement_speed": 0.25,  # Silverfish.createAttributes: MOVEMENT_SPEED 0.25
        "attack_damage": 1.0,    # Silverfish.createAttributes: ATTACK_DAMAGE 1.0 (the melee damage)
    },
    goals = [
        # @1 FloatGoal [JUMP] — requiresUpdateEveryTick=true (jar-confirmed). Cite Silverfish.registerGoals @1 FloatGoal.
        goal(
            priority = 1,
            flags = ["JUMP"],
            can_use = float_can_use,
            tick = float_tick,
            requires_update_every_tick = True,
        ),
        # @4 MeleeAttackGoal(mob, 1.0, false) [MOVE] — kind="melee_attack" (the 35-01 meleeAttackGoal):
        # chases the target + fires doHurtTarget through the Phase-29 keystone (REAL ATTACK_DAMAGE). Cite
        # Silverfish.registerGoals @4 MeleeAttackGoal.
        goal(priority = 4, flags = ["MOVE"], kind = "melee_attack"),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] — kind="hurt_by_target" (retaliate, NO RNG). The
        # setAlertOthers burst is cite-deferred. Cite Silverfish.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(mob) [TARGET] — kind="nearest_attackable_target"
        # (the nextInt(10) acquire gate + findTarget bounded by FOLLOW_RANGE). The mustSee LoS is the cited
        # "visible" stub. Cite Silverfish.registerGoals targetSelector @2 NearestAttackableTargetGoal<Player>.
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
