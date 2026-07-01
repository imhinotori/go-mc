# vanilla_sulfur_cube - a 1:1 vanilla-SulfurCube dogfood (MOB-CUBE), the NEW 26.2 cube mob built as a
# Starlark plugin declaration. A LITERAL method-for-method port of the unobfuscated 26.2 jar
# (temp/cache/26.2-inner.jar, CFR/javap). It declares net.minecraft.world.entity.monster.cubemob
# .AbstractCubeMob.registerGoals (the shared slime/magmacube/sulfurcube base) reusing the proven Go goal
# runtime. UNLIKE the other mobs, the SulfurCube's WHOLE AI is Go-NATIVE (kind-goals): the cube does NOT
# use PathNavigation - its CubeMobMoveControl drives movement directly (jump-move state machine), so the
# three goals + the move control + the split-on-death + the size machine all live in Go
# (server/ai_goals_sulfur_cube.go); this .star only DECLARES them.
#
# AbstractCubeMob.registerGoals() (javap/CFR-verified this session):
#   goalSelector:
#     @1 CubeMobFloatGoal(this)          <-- kind="cube_float"           {JUMP,MOVE} requiresUpdateEveryTick
#     @4 CubeMobRandomDirectionGoal(this)<-- kind="cube_random_direction"{LOOK}
#     @5 CubeMobKeepOnJumpingGoal(this)  <-- kind="cube_keep_on_jumping" {JUMP,MOVE}
#     addBehaviourGoals (SulfurCube):
#       @2 SulfurCubeTemptGoal(...)      <-- DEFERRED (needs tempt / held-item on a non-pathfinder)
#       @3 SulfurCubeSearchForItemsGoal  <-- DEFERRED (needs item-pickup)
#     addTargetingGoals (SulfurCube):    <-- EMPTY (no target goals - the cube never hunts)
#
# SIZE MACHINE (SulfurCube overrides on AbstractCubeMob.setSize, Go: setSulfurCubeSize):
#   MAX_HEALTH base = 4 * size      (SulfurCube.setcubeMobHealth; size 2 -> 8, size 1 -> 4)
#   MOVEMENT_SPEED base = 0.2 + 0.1 * size  (AbstractCubeMob.setSize; size 2 -> 0.4, size 1 -> 0.3)
#   dims = base(0.49x0.49) * size   (getDefaultDimensions; size 2 -> 0.98, size 1 -> 0.49)
#   spawn size = 2 (adult) / 1 (baby)   (SulfurCube.setSpawnSize)  MAX_SIZE=2, MIN_SIZE=1
#   split-on-death: getSize()>1 -> spawn SPLIT_COUNT(2) baby size-1 cubes (AbstractCubeMob.remove)
#
# DEFERRED (cite-recorded, NEVER silently dropped - each needs an unbuilt subsystem):
#   - The item-swallowing character layer: SulfurCubeTemptGoal@2 (tempt/held-item on a non-pathfinder) +
#     SulfurCubeSearchForItemsGoal@3 (item-pickup).
#   - The body-item / SulfurCubeArchetype system (contact damage / explosion / knockback / buoyancy /
#     shear / bucket): needs item-pickup + block-entity + the SULFUR_CUBE_ARCHETYPE registry. Without a
#     body item SulfurCube.isDealsDamage() returns false (NO contact damage) - the faithful base cube.
#     The 12 archetype VALUES (jar SulfurCubeArchetypes.bootstrap, for the future body-item layer):
#       archetype(speed,bounce,friction,drag) -> KNOCKBACK_RESISTANCE/EXPLOSION_KB_RESISTANCE add(-speed),
#       BOUNCINESS add(bounce), FRICTION_MODIFIER multiply(friction), AIR_DRAG_MODIFIER multiply(drag):
#         regular (1.0,0.5,0.3,0.1)  bouncy (2.0,0.9,0.3,0.01)  slow_bouncy (-0.4,0.6,0.3,0.05)
#         slow_flat (-0.5,0.4,0.4,0.1)  fast_flat (1.0,0.5,0.2,0.01)  light (1.0,1.0,0.3,1.8)
#         fast_sliding (-0.5,0.1,0.05,0.01)  slow_sliding (-0.8,0.1,0.05,0.01)  sticky (2.0,0.0,2.0,0.01)
#         high_resistance (-0.7,0.2,1.0,0.01)  explosive (1.0,0.5,0.3,0.3) + ExplosionData(power 3, fire
#         false, fuse 120)  hot (1.0,0.5,0.3,0.1) + ContactDamage(SULFUR_CUBE_HOT, ConstantFloat 1.0).
#       knockback hit-scale + sound-settings per archetype also captured (jar SulfurCubeArchetypes).
#   - hasEffect(LEVITATION) in CubeMobRandomDirectionGoal.canUse (Go const-false, no mob-effect read).
#   - Squish/land particles + squish/jump sounds + the ID_SIZE client size metadata (client visuals/wire).
#   - Natural spawn: SulfurCube spawns ONLY in the sulfur_caves biome (MobSpawnSettings MONSTER weight 100,
#     pack 2-4). With no per-biome spawn weights in v1 it is kept OUT of the natural MONSTER pool (async.go)
#     to avoid spawning everywhere - the same biome-gating the husk/silverfish get. Spawnable via /dbg.

# --- the declaration ---------------------------------------------------------------------------
# base_type "sulfur_cube" -> renders as entity.SulfurCube.ID (id 130). The movement_speed here is the
# createMobAttributes base; setSize OVERRIDES it (0.2+0.1*size) at spawn, so the declared value is only the
# pre-size seed. All three goals are Go-native kind-goals (the cube's whole AI). Cite AbstractCubeMob
# .registerGoals + SulfurCube.createSulfurCubeAttributes.
declare_mob(
    name = "vanilla_sulfur_cube",
    base_type = "sulfur_cube",
    attributes = {
        "movement_speed": 0.4,  # createMobAttributes base seed; setSize overrides to 0.2+0.1*size at spawn
    },
    goals = [
        # @1 CubeMobFloatGoal [JUMP,MOVE] - kind="cube_float", requiresUpdateEveryTick handled by the Go goal.
        # Cite AbstractCubeMob.registerGoals @1 CubeMobFloatGoal.
        goal(priority = 1, flags = ["JUMP", "MOVE"], kind = "cube_float"),
        # @4 CubeMobRandomDirectionGoal [LOOK] - kind="cube_random_direction". Cite AbstractCubeMob.registerGoals @4.
        goal(priority = 4, flags = ["LOOK"], kind = "cube_random_direction"),
        # @5 CubeMobKeepOnJumpingGoal [JUMP,MOVE] - kind="cube_keep_on_jumping". Cite AbstractCubeMob.registerGoals @5.
        goal(priority = 5, flags = ["JUMP", "MOVE"], kind = "cube_keep_on_jumping"),
    ],
)
