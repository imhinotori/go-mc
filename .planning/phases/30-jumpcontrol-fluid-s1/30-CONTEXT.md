# Phase 30: JumpControl + Fluid (S1) - Context

**Gathered:** 2026-06-29
**Status:** Ready for planning

<domain>
## Phase Boundary

Build the mob (`*Entity`) JumpControl impulse seam + the mob fluid-detection predicates, then wire
FloatGoal@0 onto the pig (Go-native oracle AND plugin, in lockstep). A goal can claim the JUMP flag and
the mob performs the real jump impulse (consumed in `serverAiStep`'s JUMP slot after `navigation.tick`,
jar order); `mobIsInWater`/`mobFluidHeight`/`isInLava` return jar-correct values; a mob in water no
longer sinks/suffocates.

Delivers MOB-SUB-04 (mob JumpControl) + MOB-SUB-05 (mob fluid predicates), and lands the pig's
FloatGoal@0 (the first of the 5 deferred goals → contributes to MOB-GATE-01, formally closed Phase 33).

Standing constraints: 1:1 jar-verified (javap before writing, cite class/method); CGO=0 + no new Go
deps; the pig oracle `TestPluginPigEqualsGoNativePig` stays GREEN; Docker `-race` + `strictRegion` clean.
</domain>

<decisions>
## Implementation Decisions

### Fluid predicate scope (Grey Area 1)
- **Build the full water + lava fluid-for-mobs subsystem.** Port `mobIsInWater` (mirror `playerInWater`'s
  AABB scan over `fluidAt`, using the entity's width/height instead of player dims), `mobFluidHeight(WATER)`
  (the per-cell `fluidSurfaceHeight` summed/maxed over the mob AABB, the `FlowingFluid.getHeight` analogue),
  `mobIsInLava`, and `getFluidJumpThreshold()` (default 0.0 for the pig/most mobs — a cited constant that
  becomes a real per-type read later). FloatGoal.canUse = `isInWater() && getFluidHeight(WATER) >
  getFluidJumpThreshold() || isInLava()` works 1:1 for BOTH water and lava.
- **Lava decode:** verify `decodeFluid`/`fluidAt` already distinguishes lava (the fluidState carries an
  isLava or a fluid-type field) at plan time. If `fluidAt` is water-only today, extend `decodeFluid` to
  recognize the lava block id (cite the jar fluid registry) so `mobIsInLava`/`mobFluidHeight(LAVA)` are
  real reads — do NOT stub lava as const-false (the user chose full lava).

### Jump impulse chain (Grey Area 2)
- **Port the full jump chain 1:1.** Add `jumpControl{ jump bool }` to `mobAI` (the `JumpControl` analogue):
  `jump()` sets `jump=true`; `tick()` = `mob.setJumping(jump); jump=false`. Add `setJumping(bool)` +
  a `jumping` field to `*Entity` (or mobAI). Insert the JUMP slot in `serverAiStep` AFTER `navigation.tick`
  (jar order: goals.tick → goals.tickRunningGoals → navigation.tick → moveControl/lookControl/**jumpControl**.tick).
  Port the `aiStep` jump branch: `if jumping && isAffectedByFluids { fluidHeight = isInLava?
  getFluidHeight(LAVA):getFluidHeight(WATER); inWaterAndHasFluidHeight = isInWater && fluidHeight>0; if
  inWaterAndHasFluidHeight && (!onGround || fluidHeight>threshold) jumpInLiquid(WATER) [vy += 0.04]; else if
  isInLava && ... jumpInLiquid(LAVA); else if (onGround || ...) && noJumpDelay==0 { jumpFromGround() [vy =
  0.42 + jumpBoost]; noJumpDelay=10 } }`. Build BOTH `jumpInLiquid` (the water/lava +0.04 swim impulse FloatGoal
  needs) AND `jumpFromGround` (the land jump vy=0.42 — used by future leap/panic goals).
- **CRITICAL — where the jump branch runs without perturbing the oracle:** the `jumping`/`jumpControl.tick`
  + the aiStep jump branch are PURE (no RNG). They run in serverAiStep/aiStep AFTER the goal callbacks.
  Confirm the insertion does NOT draw RNG in the shared flow (the ONLY new RNG is FloatGoal.tick's
  nextFloat, inside the goal callback). `noJumpDelay` is a per-mob int counter (add to mobAI/Entity),
  decremented each tick — RNG-free.

### FloatGoal lockstep on the oracle (Grey Area 3)
- **Add FloatGoal@0 to BOTH `newPigAI` (Go-native) and `plugins/vanilla_pig/main.star` in the SAME plan,
  in lockstep.** Priority 0 (highest — runs FIRST in the goal walk), flags ["JUMP"], `requiresUpdateEveryTick`.
  The `nextFloat() < 0.8` draw is in `tick()` (drawn EVERY tick while active), confined to the goal callback
  — NEVER the shared serverAiStep/navigation flow.
- **Oracle determinism:** FloatGoal.canUse = isInWater/isInLava. The oracle test (`TestPluginPigEqualsGoNativePig`)
  runs the pig in its current world — VERIFY at plan time whether that world has water. If DRY (no water/lava):
  canUse is always false → FloatGoal never ticks → ZERO new RNG draws → the oracle is byte-identical with
  FloatGoal added (the safest outcome). If the oracle world has water, the FloatGoal draw lands FIRST (before
  stroll's) and BOTH pigs draw it identically in lockstep → still byte-identical. Either way the oracle stays
  green BECAUSE both halves are lockstep. The plan MUST add the goal to both pigs and confirm the oracle green
  before the wave closes; if a divergence appears, the world is the variable — keep the oracle world dry for
  FloatGoal (water-jump behavior tested in a separate dedicated test).
- The plugin needs `entity.in_water` / `entity.fluid_height` / `entity.in_lava` handle attrs (frozen scalars)
  + `nav.jump()` (or `entity.jump()`) so the Starlark FloatGoal can express canUse + tick. The Go-native
  FloatGoal calls the same predicates directly.

### Claude's Discretion
- The exact plan split (likely: fluid predicates + lava decode → jumpControl + aiStep jump branch + handle
  attrs → FloatGoal on both pigs lockstep + the dry/wet oracle decision).
- The mob AABB dims source for the fluid scan (entity.Width/Height from data/entity, as Phase 29's placement
  obstruction used).
- Whether `isAffectedByFluids`/`isPushedByFluid` need porting now (FloatGoal only needs the jump branch
  predicates) — port the minimum the jump branch reads, cite the rest.
</decisions>

<code_context>
## Existing Code Insights (scouted — file:line)

### Reusable Assets
- **`server/ai_mob.go`** — `mobAI{goals, navigation, wantX/Y/Z, hasTarget, rng}` (l.29-54); `serverAiStep`
  (l.105-125) order: `goals.tick → goals.tickRunningGoals → navigation.tick → (JUMP slot — the documented
  deferral l.124)`. `newPigAI` (l.138-153): `addGoal(6 stroll, 7 lookAt, 8 lookAround)` — ADD `addGoal(0,
  newFloatGoal())` FIRST. `reseedMobAI(e.ai, e.id)`.
- **`server/ai_goal.go`** — `goalFlag` enum `flagMove/flagLook/flagJump/flagTarget` (l.33-43); `flagJump`
  ALREADY EXISTS. Arbitration `goalCanBeReplacedForAllFlags` (l.182-191) is PURE LOGIC, no RNG (safe).
  goalSelector tick passes (l.221-272).
- **`server/entity.go`** — `vx, vy, vz float64` velocity (l.60-62); the jump impulse sets `e.vy`.
- **`server/physics.go`** — `moveEntity` per-axis sweep + onGround (l.265-296); `tickPhysics` gravity
  `vy -= 0.08; vy *= 0.98` (l.415-416). The jump impulse must land BEFORE this integration in the tick.
- **`server/fluid.go`** — `fluidAt(pos) fluidState{isWater, source, falling, amount}` (l.245-252).
- **`server/breath.go`** — `fluidSurfaceHeight(cell, fs)` = 1.0 if water above else amount/9 (l.139-152) —
  the `FlowingFluid.getHeight` analogue, reuse for mobFluidHeight.
- **`server/fluid_physics.go`** — `playerInWater(p)` AABB scan over fluidAt (l.47-73) — MIRROR for mobs
  with entity Width/Height.
- **`server/plugin_mob_decl.go`** — `flagByName{MOVE/LOOK/JUMP/TARGET}` (l.191-198), `parseGoalFlags`;
  the Starlark goal declares flags=["JUMP"]. `baseGoal{gflags, interruptable}`.
- **`plugins/vanilla_pig/main.star`** — the 3 passive goals (l.152-190): `goal(priority=6/7/8, flags, can_use,
  start, tick, stop, can_continue, requires_update_every_tick)`. FloatGoal slots at priority=0, flags=["JUMP"].
- **`server/ai_random.go`** — `entityRandom{r *rand.Rand}` (l.40-66): `nextFloat()`=Float32, `nextInt`,
  `nextDouble`. FloatGoal draws `nextFloat()<0.8` from `e.ai.rng` (mobRandom) in its tick callback.
- **`server/tick_phases.go`** — `tickAI` (serverAiStep + spawn) → `tickPhysics` (gravity + moveEntity)
  (l.298-427). The dead-mob `tickDeath` skip (Phase 29) is here too — FloatGoal/jump only for live mobs.

### Established Patterns
- Goal callbacks draw RNG; the shared serverAiStep arbitration + navigation.tick + the new jumpControl.tick
  do NOT. Keep FloatGoal's nextFloat in tick() only.
- Lockstep Go-native + plugin goal lists (identical priority + draw order) — the oracle's contract.
- Thin-id handle attrs return frozen scalars across the Starlark boundary.
- Mirror player fluid (AABB over fluidAt) for the mob predicates; reuse fluidSurfaceHeight.

### Integration Points
- `mobAI` — new `jumpControl` + `noJumpDelay`; `serverAiStep` JUMP slot after navigation.tick.
- `*Entity` — `jumping bool` (+ setJumping); the jump impulse on `e.vy`.
- `fluid.go`/`decodeFluid` — lava recognition for mobIsInLava/mobFluidHeight(LAVA).
- `newPigAI` + `vanilla_pig/main.star` — FloatGoal@0 lockstep.
- plugin handle — `entity.in_water/fluid_height/in_lava` + `nav.jump()`.
</code_context>

<specifics>
## Specific Ideas

- javap-verified this session: `FloatGoal` (canUse = isInWater && getFluidHeight(WATER)>getFluidJumpThreshold
  || isInLava; tick = if random.nextFloat()<0.8 jumpControl.jump(); ctor setFlags(JUMP) + navigation.setCanFloat(true);
  requiresUpdateEveryTick=true). `JumpControl` (jump()=jump=true; tick()=setJumping(jump); jump=false).
  `LivingEntity.aiStep` jump branch (jumping && isAffectedByFluids → jumpInLiquid(WATER) vy+=0.04 / jumpInLiquid(LAVA)
  / jumpFromGround vy=0.42 + noJumpDelay=10). getFluidJumpThreshold default 0.0.
- Verify EVERY ported method against the jar before writing. Cite class/method.
- The user is validating in-game after each phase (the server runs via run-debug.sh) — FloatGoal's observable
  is: a pig dropped in water bobs/jumps to stay afloat instead of sinking + suffocating.
</specifics>

<deferred>
## Deferred Ideas

- Per-type `getFluidJumpThreshold` (non-zero for some mobs) — cited constant 0.0 (pig default) now, real read later.
- `isPushedByFluid`/fluid push velocity (the current/flow shove) — FloatGoal doesn't need it; cite if the
  jump branch references it, port when a swimming mob needs the current.
- The pig's FloatGoal is the ONLY FloatGoal consumer in Phase 30; cow/sheep/chicken/wolf reuse it in 34/36.
- navigation.setCanFloat(true) effect on pathfinding-over-water — port the flag set; the nav float behavior
  (pathing across water) is a node-evaluator concern, cite if not trivially included.
</deferred>
