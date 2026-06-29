# Phase 24 — Per-new-goal build-OR-defer audit (the no-built-but-unwired justification artifact)

**Audited:** 2026-06-28 (Plan 24-02 Task 1)
**Jar:** `temp/cache/26.2-inner.jar` via `javap -c -p` (`/c/Program Files/Zulu/zulu-25/bin/javap`)
**Rule (CONTEXT decision 4 / no-built-but-unwired):** for EACH goal in `Pig.registerGoals()`,
either BUILD it 1:1 (its prerequisite subsystem exists faithfully) or DEFER it with a CITED reason
(the jar class + the exact missing subsystem + the codebase evidence it is absent). Never a silent
skip, never a faked subsystem.

## `Pig.registerGoals()` — the full set (javap-verified this session)

Disassembled `net.minecraft.world.entity.animal.pig.Pig.registerGoals` (bytecode confirmed):

| @ | Goal (jar class) | ctor args | Decision |
|---|------------------|-----------|----------|
| 0 | `FloatGoal(mob)` | — | **DEFER (cited)** |
| 1 | `PanicGoal(mob, 1.25)` | speedModifier 1.25 | **DEFER (cited)** |
| 3 | `BreedGoal(mob, 1.0)` | speedModifier 1.0 | **DEFER (cited)** |
| 4 | `TemptGoal(mob, 1.2, PIG_FOOD-pred, false)` | speed 1.2 | **DEFER (cited)** |
| 4 | `TemptGoal(mob, 1.2, CARROT_ON_A_STICK-pred, false)` | speed 1.2 | **DEFER (cited)** |
| 5 | `FollowParentGoal(mob, 1.1)` | speedModifier 1.1 | **DEFER (cited)** |
| 6 | `WaterAvoidingRandomStrollGoal(mob, 1.0)` | speed 1.0 | **BUILT 1:1** (plugin: stroll@6 MOVE) |
| 7 | `LookAtPlayerGoal(mob, Player.class, 6.0f)` | dist 6.0 | **BUILT 1:1** (plugin: lookAt@7 LOOK) |
| 8 | `RandomLookAroundGoal(mob)` | — | **BUILT 1:1** (plugin: lookAround@8 MOVE\|LOOK) |

## BUILT 1:1 (the 3 passive goals — all prerequisites exist)

These three are re-expressed 1:1 in `plugins/vanilla_pig/main.star`, each citing its jar class, each
matching the bytecode draw order. Their prerequisites all exist after Plan 24-01:

- **`entity.set_look(yaw, pitch)`** — the LOOK mutate seam (the `e.headYaw = e.yaw = yaw` analogue).
- **`world.nearest_player(x,y,z,range)`** — the player scan (reuses `nearestPlayerAt` over `t.players`).
- **`entity.rand_int(n)` / `entity.rand_float()`** — the per-entity seeded RandomSource (the
  `Mob.getRandom()` analogue), faithful draw order.
- **`nav.path_to` / `nav.has_path`** — the MOVE nav seam (stroll's `navigation.moveTo`/`isDone`).
- **`entity.state`** (NEW this plan) — per-goal mutable scratch (the `lookTime`/`relX`/`relZ`/`lookAt`
  struct-field analogue) across the frozen-Starlark boundary.

## DEFER (cited) — the 5 new goals whose prerequisite subsystem is unbuilt

### FloatGoal@0 — `net.minecraft.world.entity.ai.goal.FloatGoal`
- **What it does (javap):** ctor `setFlags(EnumSet.of(JUMP))` + `mob.getNavigation().setCanFloat(true)`.
  `canUse()` = `mob.isInWater() && getFluidHeight(FluidTags.WATER) > getFluidJumpThreshold()` **OR**
  `mob.isInLava()`. `requiresUpdateEveryTick()` = true. `tick()` = `if getRandom().nextFloat() < 0.8
  then getJumpControl().jump()`.
- **Missing prerequisite (two subsystems):**
  1. **A mob jump-control / jump-impulse seam.** FloatGoal's entire observable behavior is
     *jump-when-in-water* via `mob.getJumpControl().jump()`. `mobAI` (server/ai_mob.go) has a
     navigation field but NO jump control and NO jump-impulse seam — the JUMP control is explicitly
     documented DEFERRED ("moveControl/lookControl/jumpControl.tick — folded into navigation.tick",
     server/ai_mob.go line 96). With no jumpControl, a ported FloatGoal would claim the JUMP flag and
     `tick` would have nothing to call → built-but-unwired.
  2. **Mob water/lava detection.** `Mob.isInWater()` / `getFluidHeight(WATER)` /
     `getFluidJumpThreshold()` / `isInLava()` do not exist for `*Entity`/`mobAI`. The fluid seams in
     the codebase (`breath.go`, `suffocation.go`, `fluid.go`) are PLAYER-specific (`*tickPlayer`,
     `playerStandingEyeHeight`/`playerSwimmingEyeHeight`); the only position-based primitive,
     `TickLoop.fluidAt(pos)` (server/fluid.go:222), is a raw block read, not a mob `isInWater` predicate.
- **Codebase evidence:** `grep 'jumpControl|JumpControl|setJumping'` in server/ → matches only in
  comments (ai_mob.go:9,96 documenting the deferral). `grep 'isInWater|getFluidHeight'` in server/ →
  no mob predicate; fluid reads are player/position-based only.
- **Decision:** DEFER. Building a faithful FloatGoal needs a real mob JumpControl impulse + a mob
  `isInWater`/fluid-height predicate — two subsystems, larger than this validation phase. Faking a
  jump (e.g. a raw `vy` poke) would NOT be the vanilla `JumpControl.jump()` and is forbidden.

### PanicGoal@1 — `net.minecraft.world.entity.ai.goal.PanicGoal`
- **What it does (javap):** `canUse()` = `shouldPanic()` then look for water / a random flee position.
  `shouldPanic()` = `mob.getLastDamageSource() != null && lastDamageSource.is(panicCausingDamageTypes
  .apply(mob))` (plus `isOnFire`/`isFreezing` branches).
- **Missing prerequisite:** **a mob "was hurt" / last-damage-source.** `PanicGoal` panics on
  `mob.getLastDamageSource()`. The damage pipeline (server/combat.go) — `applyDamage` / `actuallyHurt`
  / the `on_damage` Emit — operates ONLY on `*tickPlayer` (`p.entityID`); mobs (`*Entity` with `.ai`)
  have NO `applyDamage` path and NO last-damage-source field. There is no mob hurt source to read.
- **Codebase evidence:** `grep 'getLastDamageSource|lastDamageSource'` in server/ → none.
  combat.go's `applyDamage(p *tickPlayer, ...)` / `actuallyHurt(p *tickPlayer, ...)` /
  `Emit(host.EventDamage, ...{EntityID: int(p.entityID)})` are all player-typed.
- **Decision:** DEFER. A faithful PanicGoal needs a mob damage pipeline + a per-mob last-damage-source
  + the panic damage-type tag. Faking a "was hurt" flag the goal reads is explicitly forbidden
  (24-CONTEXT decision: "do NOT fake a hurt flag").

### BreedGoal@3 — `net.minecraft.world.entity.ai.goal.BreedGoal`
- **Missing prerequisite:** entity **aging + breeding state** (`Animal.isInLove()`, `canMate`, the
  `inLove`/`age` ticks, partner search, baby spawning). None of this exists: `grep 'isBaby|getAge|
  breedingAge|BreedGoal|isInLove'` in server/ → none. `*Entity` carries no age/love state.
- **Decision:** DEFER. A large unbuilt subsystem (animal aging + breeding + child spawning).

### TemptGoal@4 (×2: PIG_FOOD + CARROT_ON_A_STICK) — `net.minecraft.world.entity.ai.goal.TemptGoal`
- **Missing prerequisite:** a **held-item read on the nearest player** + the `ItemTags.PIG_FOOD` tag /
  `Items.CARROT_ON_A_STICK` predicate (the ctor's `Predicate<ItemStack>` from the
  `lambda$registerGoals$0/1` → `ItemStack.is(PIG_FOOD)` / `is(CARROT_ON_A_STICK)`). The handle exposes
  no player held-item read and no item-tag membership test. `grep 'getMainHandItem|PIG_FOOD|
  CARROT_ON_A_STICK'` in server/ → none.
- **Decision:** DEFER (both Tempt instances). Needs a held-item + item-tag subsystem.

### FollowParentGoal@5 — `net.minecraft.world.entity.ai.goal.FollowParentGoal`
- **Missing prerequisite:** **age/parent state** (a baby following an adult of the same type) — the
  same aging subsystem BreedGoal needs. `grep 'isBaby|FollowParentGoal'` in server/ → none.
- **Decision:** DEFER. Depends on the unbuilt aging subsystem.

## Outcome

The full faithful PASSIVE-AMBIENT subset of the pig (the 3 goals that make a pig amble + look around
exactly like vanilla) is ported 1:1 this phase. The 5 combat/water/breeding/item goals are DEFERRED,
each with a cited jar class + the exact missing subsystem + the codebase evidence — to be ported when
their prerequisites land (mob jump control + fluid detection for Float; a mob damage pipeline for
Panic; entity aging/breeding for Breed/FollowParent; held-item + item tags for Tempt). No goal whose
subsystem would be faked is shipped; no goal is silently skipped.
