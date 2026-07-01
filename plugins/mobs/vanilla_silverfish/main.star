# vanilla_silverfish — a 1:1 vanilla-Silverfish dogfood (MOB-HOST-05), the Silverfish built as a Starlark
# plugin. A LITERAL method-for-method port of the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar,
# javap -c -p). It declares net.minecraft.world.entity.monster.Silverfish.registerGoals, reusing the
# proven Go goal runtime: the FloatGoal is the SAME mob-agnostic .star callback the cow/pig run (COPIED
# VERBATIM), and the COMBAT + INFEST goals are the Go-native ports via the kind= seam.
#
# Silverfish.registerGoals() (javap-verified this session):
#   goalSelector:
#     @1 FloatGoal(this)                          <-- .star (the shared passive float — swim-jump)
#     @1 ClimbOnTopOfPowderSnowGoal(this, level)  <-- kind="climb_on_powder_snow" (JUMP; climb, do not sink)
#     @3 SilverfishWakeUpFriendsGoal(this)        <-- kind="silverfish_wake_friends" (the hurt-armed spiral wake)
#     @4 MeleeAttackGoal(this, 1.0, false)        <-- kind="melee_attack" (the 35-01 meleeAttackGoal; CORE melee)
#     @5 SilverfishMergeWithStoneGoal(this)       <-- kind="silverfish_merge_stone" (the stone->infested merge)
#   targetSelector:
#     @1 HurtByTargetGoal(this).setAlertOthers()  <-- kind="hurt_by_target" (alertOthers cite-deferred, no-op)
#     @2 NearestAttackableTargetGoal<Player>(this, Player, mustSee=true) <-- kind="nearest_attackable_target"
#
# INFEST GOALS (MOB-HOST-05, now wired — ai_goals_silverfish.go + silverfish_infest.go):
#   - SilverfishMergeWithStoneGoal@5 (kind="silverfish_merge_stone", {MOVE}): canUse RNG-gates
#     (nextInt(reducedTickDelay(10))), picks a random Direction, and if the adjacent block is a compatible
#     host (InfestedBlock.isCompatibleHostBlock) converts it to its infested variant + spawnAnim + discard;
#     else it runs a bare RandomStroll. Cite Silverfish$SilverfishMergeWithStoneGoal.
#   - SilverfishWakeUpFriendsGoal@3 (kind="silverfish_wake_friends", {} no flags): notifyHurt (fired from
#     the Silverfish.hurtServer per-type hook, combat_mob.go) arms lookForFriends=adjustedTickDelay(20);
#     the spiral tick de-infests / summons nearby silverfish (InfestedBlock.hostStateByInfested /
#     spawnInfestation). Cite Silverfish$SilverfishWakeUpFriendsGoal.
#
# DEFERRED (cite-recorded, NEVER silently dropped):
#   - ClimbOnTopOfPowderSnowGoal@1 (kind="climb_on_powder_snow"): wired; isInPowderSnow reads the feet-block
#     proxy (no inside-block subsystem yet, ai_goals_powder_snow.go), POWDER_SNOW_WALKABLE_MOBS is the exact
#     jar entity-type set.
#   - The infest block mapping is a StateID table at defaultBlockState() granularity; the deepslate
#     axis-copy (InfestedRotatedPillarBlock) is DEFERRED to the default axis=y (silverfish_infest.go),
#     and the mobGriefing destroyBlock loot-drop is DEFERRED (the summon fires; the item drop does not).
#     GameRules.MOB_GRIEFING is a cited constant == vanilla default true.
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
# (Silverfish.createAttributes: MAX_HEALTH 8.0, MOVEMENT_SPEED 0.25, ATTACK_DAMAGE 1.0). The combat +
# infest goals are the Go-native ports (kind=); the FloatGoal is a .star callback. Cite
# Silverfish.registerGoals + Silverfish.createAttributes.
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
        # @1 ClimbOnTopOfPowderSnowGoal(mob, level) [JUMP] — kind="climb_on_powder_snow" (the silverfish
        # is in the POWDER_SNOW_WALKABLE_MOBS tag): climb ON TOP of powder snow instead of sinking; NO RNG.
        # Cite Silverfish.registerGoals @1 ClimbOnTopOfPowderSnowGoal.
        goal(priority = 1, flags = ["JUMP"], kind = "climb_on_powder_snow"),
        # @3 SilverfishWakeUpFriendsGoal(mob) [] (NO flags) — kind="silverfish_wake_friends": the hurt-armed
        # spiral that de-infests / summons nearby silverfish. Cite Silverfish.registerGoals @3.
        goal(priority = 3, flags = [], kind = "silverfish_wake_friends"),
        # @4 MeleeAttackGoal(mob, 1.0, false) [MOVE] — kind="melee_attack" (the 35-01 meleeAttackGoal):
        # chases the target + fires doHurtTarget through the Phase-29 keystone (REAL ATTACK_DAMAGE). Cite
        # Silverfish.registerGoals @4 MeleeAttackGoal.
        goal(priority = 4, flags = ["MOVE"], kind = "melee_attack"),
        # @5 SilverfishMergeWithStoneGoal(mob) [MOVE] — kind="silverfish_merge_stone": the stone->infested
        # merge (RNG-gated) with a bare-RandomStroll fallback. Cite Silverfish.registerGoals @5.
        goal(priority = 5, flags = ["MOVE"], kind = "silverfish_merge_stone"),
        # targetSelector @1 HurtByTargetGoal(mob) [TARGET] — kind="hurt_by_target" (retaliate, NO RNG). The
        # setAlertOthers burst is cite-deferred. Cite Silverfish.registerGoals targetSelector @1 HurtByTargetGoal.
        goal(priority = 1, flags = ["TARGET"], kind = "hurt_by_target"),
        # targetSelector @2 NearestAttackableTargetGoal<Player>(mob) [TARGET] — kind="nearest_attackable_target"
        # (the nextInt(10) acquire gate + findTarget bounded by FOLLOW_RANGE). The mustSee LoS is the cited
        # "visible" stub. Cite Silverfish.registerGoals targetSelector @2 NearestAttackableTargetGoal<Player>.
        goal(priority = 2, flags = ["TARGET"], kind = "nearest_attackable_target"),
    ],
)
