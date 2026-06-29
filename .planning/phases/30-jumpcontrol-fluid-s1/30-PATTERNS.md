# Phase 30: JumpControl + Fluid (S1) - Pattern Map

**Mapped:** 2026-06-29
**Files analyzed:** 8 (6 modified + 2 new)
**Analogs found:** 8 / 8 (every new/modified file has a concrete in-repo analog to mirror 1:1)

> This is a 1:1 jar-port. Every excerpt below is the EXISTING Sulfur code the planner should mirror
> method-for-method (idiomatic Go, cite the jar class/method, never paraphrase gameplay). Two
> load-bearing findings are flagged inline and repeated in **Critical Findings** at the bottom:
> (1) `decodeFluid`/`fluidState` is **WATER-ONLY today** — the planner MUST extend it for lava;
> (2) the oracle test world is **DRY** — FloatGoal draws zero RNG there, so the oracle stays
> byte-identical when FloatGoal is added to both pigs in lockstep.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `server/fluid_physics.go` (mod) — add `mobInWater`/`mobFluidHeight`/`mobInLava` | physics/predicate | transform (AABB scan) | `playerInWater` (same file, l.52-73) | exact |
| `server/fluid.go` (mod) — extend `decodeFluid`/`fluidState` for lava | model/decode | transform | `decodeFluid` + `fluidState` (same file, l.99-130) | exact (EXTEND) |
| `server/ai_mob.go` (mod) — add `jumpControl`+`noJumpDelay` to `mobAI`, JUMP slot in `serverAiStep`, `addGoal(0, …)` in `newPigAI` | controller (AI driver) | event-driven (tick) | `mobAI`/`serverAiStep`/`newPigAI` (same file, l.29-153) | exact |
| `server/entity.go` (mod) — add `jumping bool` (+`setJumping`) | model | — | the Phase-29 `hurtTime`/`dead` plain-value fields (l.190-210) | exact |
| `server/tick_phases.go` or new `server/jump.go` — the `aiStep` jump branch (jumpInLiquid/jumpFromGround vy impulse) | physics | transform (velocity) | `tickPhysics` gravity `e.vy -= …` (l.413-416) + `moveEntity` (physics.go l.265-296) | role-match |
| `server/ai_goals_passive.go` (mod) or new `server/ai_goals_float.go` — Go-native `floatGoal` | service (goal) | event-driven | `randomLookAroundGoal` (ai_goals_passive.go l.264-307) + `newWaterAvoidingRandomStrollGoal` | exact |
| `plugins/vanilla_pig/main.star` (mod) — FloatGoal@0 declaration | config/declaration | event-driven | the @8 around goal (main.star l.179-188) + `around_can_use`/`around_tick` (l.122-146) | exact |
| `server/plugin_entity.go` (mod) — `entity.in_water`/`fluid_height`/`in_lava` read attrs + `nav.jump()` (or `entity.jump()`) | bridge (handle) | request-response | `was_hurt`/`last_damage_type` frozen scalars (l.177-191) + `navHandle.stop` mutate (l.734-748) | exact |

---

## Pattern Assignments

### `server/fluid_physics.go` — mobInWater / mobFluidHeight / mobInLava (physics, AABB transform)

**Analog:** `playerInWater` (same file, l.52-73). The mob version mirrors the AABB scan over `fluidAt`
but uses the entity's `width`/`height` (entity.go l.75) instead of `playerWidth`/`playerHeight`, and
takes an `*Entity` instead of `*tickPlayer`. **Cite `EntityFluidInteraction.isInFluid`** as the jar source.

**Mirror this scan exactly** (l.52-73 — substitute `e.width/2` for `hw`, `e.y`/`e.y+e.height` for the
vertical span, and gate on `isWater` OR the lava flag once `fluidState` carries it):
```go
func (t *TickLoop) playerInWater(p *tickPlayer) bool {
	if t.world() == nil {
		return false
	}
	hw := playerWidth / 2
	minX := int(math.Floor(p.x - hw))
	maxX := int(math.Floor(p.x + hw))
	minY := int(math.Floor(p.y))
	maxY := int(math.Floor(p.y + playerHeight))
	minZ := int(math.Floor(p.z - hw))
	maxZ := int(math.Floor(p.z + hw))
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if t.fluidAt(pk.Position{X: x, Y: y, Z: z}).isWater {
					return true
				}
			}
		}
	}
	return false
}
```

**`mobFluidHeight(WATER)`** — reuse `fluidSurfaceHeight` (breath.go l.142-152) per cell, summed/maxed
over the mob AABB (the `FlowingFluid.getHeight` analogue). `fluidSurfaceHeight` is the EXACT per-cell
height helper to reuse:
```go
// breath.go l.142-152 — FlowingFluid.getHeight: 1.0 if same fluid above, else amount/9.
func (t *TickLoop) fluidSurfaceHeight(cell pk.Position, fs fluidState) float64 {
	above := t.fluidAt(pk.Position{X: cell.X, Y: cell.Y + 1, Z: cell.Z})
	if above.isWater {
		return 1.0 // hasSameAbove -> full cell height
	}
	amount := fs.amount
	if amount <= 0 {
		amount = 1
	}
	return float64(amount) / 9.0
}
```
> NOTE: the vanilla `Entity.getFluidHeight(tag)` is the MAX of per-cell surface-minus-cellY over the
> AABB (the eye-submersion math in `eyeInWater`, breath.go l.128-136, shows the `surface = float64(by) +
> height` pattern). Mirror that max, cite `Entity.getFluidHeight` / `Entity.updateFluidHeightAndDoFluidPushing`.

**`getFluidJumpThreshold()`** — a cited constant `0.0` (pig/most mobs default). Follow the
breath.go/fluid.go constant-with-citation convention (e.g. breath.go l.34 `maxAirSupply int32 = 300`,
fluid.go l.33-36): a named const with the jar citation in its doc comment, structured to become a
per-type read later. Cite `Mob.getFluidJumpThreshold` default `0.0`.

---

### `server/fluid.go` — EXTEND decodeFluid / fluidState for lava (model, decode)  ⚠ LOAD-BEARING

**Analog:** `decodeFluid` (l.117-130) + `fluidState` (l.104-109), same file.

**CRITICAL FINDING — fluid decode is WATER-ONLY today.** `fluidState` carries NO lava/type field:
```go
// fluid.go l.104-109 — water-only TODAY.
type fluidState struct {
	isWater bool
	source  bool
	falling bool
	amount  int // 1..8
}
```
`waterLevelOf` (l.78-86) only recognizes `block.Water`, and `decodeFluid` (l.117-130) only ever returns
`isWater:true`. There is NO `isLava` anywhere in the decode path. The CONTEXT.md decision is **full
lava** (do NOT stub lava as const-false), so the planner MUST:
1. Add an `isLava bool` (or a `kind` enum) to `fluidState`.
2. Add a `lavaLevelOf(id)` mirroring `waterLevelOf` (l.78-86) against `block.Lava` (verify the generated
   `block.Lava{Level}` type exists in `level/block`; cite the jar `LavaFluid`/fluid registry).
3. Branch `decodeFluid` (l.117-130) to recognize the lava block id and return `isLava:true`.
4. Audit every existing `fluidState` consumer that reads `.isWater` (fluid.go flow sim, breath.go
   `eyeInWater`/`fluidSurfaceHeight`, fluid_physics.go `playerInWater`) so the lava extension does NOT
   perturb the water-only paths (the water sim must keep treating lava as non-water).

**Decode pattern to mirror** (l.117-130 — add the parallel lava branch):
```go
func decodeFluid(id block.StateID) fluidState {
	legacy, ok := waterLevelOf(id)
	if !ok {
		return fluidState{}
	}
	switch {
	case legacy == 0:
		return fluidState{isWater: true, source: true, amount: waterSourceAmount}
	case legacy <= 7:
		return fluidState{isWater: true, amount: waterSourceAmount - legacy}
	default: // 8..15 falling
		return fluidState{isWater: true, falling: true, amount: waterSourceAmount - (legacy - 8)}
	}
}
```
> Lava's legacy levels differ (LavaFluid has a different getDropOff/amount mapping — verify via javap
> `LavaFluid` before writing). FloatGoal only READS `mobInLava`/`mobFluidHeight(LAVA)` — it does not flow
> lava — so a decode-only extension (no lava flow sim) satisfies Phase 30; cite the flow as deferred.

---

### `server/ai_mob.go` — jumpControl + noJumpDelay + serverAiStep JUMP slot + newPigAI addGoal(0)

**Analog:** the `mobAI` struct (l.29-54), `serverAiStep` (l.105-125), `newPigAI` (l.138-153) — same file.

**Add to `mobAI`** following the existing plain-field convention (l.44-53). `jumpControl` is the
`net.minecraft.world.entity.ai.control.JumpControl` analogue (`jump()` sets `jump=true`; `tick()` =
`mob.setJumping(jump); jump=false`). `noJumpDelay` is the per-mob int counter (RNG-free, decremented
each tick). Mirror the `rng`/`navigation` field doc-comment style:
```go
type mobAI struct {
	goals goalSelector
	navigation groundNavigation
	wantX, wantY, wantZ float64
	hasTarget bool
	rng *entityRandom
	// ADD: jumpControl (JumpControl analogue) + noJumpDelay (per-mob int, RNG-free).
}
```

**serverAiStep JUMP slot** — insert AFTER `navigation.tick` (l.123), at the documented deferral comment
(l.124). The jar order is `goals.tick → goals.tickRunningGoals → navigation.tick → moveControl/
lookControl/**jumpControl**.tick`. The current code:
```go
// ai_mob.go l.108-124
	m.goals.tick(t, e)                   // start/stop goals + per-flag locking
	m.goals.tickRunningGoals(t, e, true) // tick every running goal
	if m.hasTarget { … m.navigation.requestPath(t, e, tx, ty, tz) … }
	m.navigation.tick(t, e)
	// (moveControl/lookControl/jumpControl.tick — folded into navigation.tick's yaw + moveEntity.)
```
Insert `m.jumpControl.tick(...)` (which calls `e.setJumping(...)`) HERE, then the aiStep jump branch
runs. **CRITICAL (CONTEXT Grey Area 2): this slot is PURE — no RNG.** The ONLY new RNG in the phase is
FloatGoal.tick's `nextFloat`, inside the goal callback (drawn via `m.goals.tickRunningGoals`), NOT here.

**newPigAI** — add `m.goals.addGoal(0, newFloatGoal())` FIRST (priority 0 = highest precedence, runs
first in the goal walk — see ai_goal.go l.149-160 insertion-sort + l.25-26 "SMALLER priority = HIGHER").
Mirror the existing addGoal block (l.149-151):
```go
	m.goals.addGoal(6, newWaterAvoidingRandomStrollGoal(1.0))
	m.goals.addGoal(7, newLookAtPlayerGoal(6.0))
	m.goals.addGoal(8, newRandomLookAroundGoal())
```
The `addGoal(0, …)` line goes ABOVE the @6 line. Cite `Pig.registerGoals` @0 FloatGoal.

---

### `server/entity.go` — jumping bool (+ setJumping)

**Analog:** the Phase-29 plain-value combat fields (l.180-210: `health`, `hurtTime`, `dead`). Add
`jumping bool` as a plain value (snapshot-friendly — the l.30-36 contract: hot fields are plain
value types). `setJumping(b bool)` is a one-line setter mirroring the LivingEntity.setJumping field
write. Cite `LivingEntity.setJumping(boolean)`. The field placement should follow the MOB-SUB-01/02
field block (l.167-210) — a documented plain-value tick-owned field. If `noJumpDelay` lives on `*Entity`
instead of `mobAI`, it follows the same convention (`LivingEntity.noJumpDelay`, an int).

---

### the aiStep jump branch — jumpInLiquid (vy += 0.04) / jumpFromGround (vy = 0.42)  (physics, velocity transform)

**Analog:** `tickPhysics` gravity (tick_phases.go l.413-416) shows how `e.vy` is mutated each tick;
`moveEntity` (physics.go l.265-296) integrates it. The jump impulse must land BEFORE the
gravity/integration step (CONTEXT: "the jump impulse must land BEFORE this integration in the tick").

**vy-mutation pattern to mirror** (tick_phases.go l.413-425):
```go
	// Gravity: accelerate downward, then air drag.
	e.vy -= gravityPerTick   // 0.08
	e.vy *= airDrag          // 0.98
	e.vx *= horizontalFriction
	e.vz *= horizontalFriction
	t.moveEntity(e, e.vx, e.vy, e.vz)
```
The jump branch is a direct `e.vy` write (jumpFromGround: `e.vy = 0.42 + jumpBoost`) or increment
(jumpInLiquid: `e.vy += 0.04`), exactly the same field-mutation style. The xp_orb.go / item_entity.go
files show the established "cite the constant via javap, mutate e.vy" pattern (e.g. xp_orb.go l.55-58
`orbAirDrag = 0.98 // [VERIFIED javap …]`, l.127 `e.vy -= orbGravity`). Cite `LivingEntity.aiStep`
(the jump branch), `LivingEntity.jumpInLiquid` (0.04), `LivingEntity.jumpFromGround` (vy=0.42 +
jumpBoost). **Build BOTH** `jumpInLiquid` (the +0.04 swim impulse FloatGoal needs) AND `jumpFromGround`
(vy=0.42, used by future leap/panic goals). The full branch (CONTEXT l.42-47):
```
if jumping && isAffectedByFluids {
    fluidHeight = isInLava ? getFluidHeight(LAVA) : getFluidHeight(WATER)
    inWaterAndHasFluidHeight = isInWater && fluidHeight>0
    if inWaterAndHasFluidHeight && (!onGround || fluidHeight>threshold) jumpInLiquid(WATER) // vy += 0.04
    else if isInLava && ... jumpInLiquid(LAVA)
    else if (onGround || ...) && noJumpDelay==0 { jumpFromGround(); noJumpDelay=10 }
}
```
> `isAffectedByFluids` / `isPushedByFluid` do NOT exist yet (Grep confirms — none of food.go/
> fall_damage.go/breath.go/attack_dispatch.go DEFINE them). Port the MINIMUM the jump branch reads
> (CONTEXT Discretion: "port the minimum the jump branch reads, cite the rest"). `onGround` already
> exists on `*Entity` (entity.go l.70, set by `moveEntity` l.282).

---

### `server/ai_goals_float.go` (new) — Go-native floatGoal (service, goal)

**Analog:** `randomLookAroundGoal` (ai_goals_passive.go l.264-307) is the closest goal struct shape
(baseGoal embed, `requiresUpdateEveryTick() bool { return true }`, a `tick` that draws RNG via
`mobRandom(e)`). `newWaterAvoidingRandomStrollGoal` (l.86-92) shows the ctor + `newBaseGoal(flag)`.

**Goal struct + ctor pattern** (mirror l.264-273):
```go
type randomLookAroundGoal struct {
	baseGoal
	relX, relZ float64
	lookTime   int
	always     bool // test seam
}
func newRandomLookAroundGoal() *randomLookAroundGoal {
	return &randomLookAroundGoal{baseGoal: newBaseGoal(flagMove | flagLook)}
}
```
FloatGoal: `newBaseGoal(flagJump)` (flagJump ALREADY EXISTS — ai_goal.go l.41). Cite `FloatGoal` ctor:
`setFlags(JUMP)` + `navigation.setCanFloat(true)`.

**canUse** — mirror `randomLookAroundGoal.canUse` (l.276-278), but the condition is the fluid predicate,
NOT an RNG roll:
```go
func (g *randomLookAroundGoal) canUse(_ *TickLoop, e *Entity) bool {
	return g.always || mobRandom(e).nextFloat() < 0.02
}
```
FloatGoal.canUse (jar, javap-verified in CONTEXT l.126-128) = `isInWater() && getFluidHeight(WATER) >
getFluidJumpThreshold() || isInLava()` — NO RNG draw in canUse. It reads the new `mobInWater`/
`mobFluidHeight`/`mobInLava` predicates directly.

**tick** — THE RNG draw lives HERE (every tick while active), mirroring how `randomLookAroundGoal.tick`
(l.302-307) runs each tick via `requiresUpdateEveryTick`. FloatGoal.tick (jar):
`if random.nextFloat() < 0.8 jumpControl.jump()` — draw `mobRandom(e).nextFloat()` (the EXACT seam the
other goals use, ai_random.go l.80) and on `< 0.8` call `e.ai.jumpControl.jump()`.
```go
// ai_goals_passive.go l.302-307 — the requiresUpdateEveryTick tick shape to mirror.
func (g *randomLookAroundGoal) tick(_ *TickLoop, e *Entity) {
	g.lookTime--
	yaw := yawTowardDeg(g.relX, g.relZ)
	e.headYaw = yaw
	e.yaw = yaw
}
func (g *randomLookAroundGoal) requiresUpdateEveryTick() bool { return true }
```
**CRITICAL (CONTEXT Grey Area 3): the `nextFloat() < 0.8` draw MUST be in tick() only** — never in the
shared serverAiStep/navigation flow. It is drawn from `mobRandom(e)` = `e.ai.rng` (ai_goals_passive.go
l.46-51 `mobRandom` helper — reuse it verbatim).

---

### `plugins/vanilla_pig/main.star` — FloatGoal@0 declaration (config, declaration)

**Analog:** the @8 RandomLookAround declaration (l.179-188) + `around_can_use`/`around_tick` callbacks
(l.122-146). The FloatGoal slots at `priority=0, flags=["JUMP"], requires_update_every_tick=True`.

**Declaration block to mirror** (l.179-188):
```python
		# @8 RandomLookAroundGoal [MOVE, LOOK] — requiresUpdateEveryTick=true (jar-confirmed).
		goal(
			priority = 8,
			flags = ["MOVE", "LOOK"],
			can_use = around_can_use,
			start = around_start,
			tick = around_tick,
			can_continue = around_continue,
			requires_update_every_tick = True,
		),
```
The FloatGoal goal() declaration: `priority = 0, flags = ["JUMP"], can_use = float_can_use,
tick = float_tick, requires_update_every_tick = True`. (`flags=["JUMP"]` is parsed by
`parseGoalFlags`/`flagByName` — plugin_mob_decl.go l.193-198 — which ALREADY maps `"JUMP" → flagJump`,
no host change needed.)

**Callback pattern** — mirror `around_can_use` (l.122-123, a pure predicate) and `around_tick`
(l.142-146, the per-tick body):
```python
def around_can_use(entity, world, nav):
    return entity.rand_float() < LOOK_AROUND_PROBABILITY

def around_tick(entity, world, nav):
    entity.set_state("look_time", entity.get_state("look_time") - 1)
    rx = entity.get_state("rel_x")
    rz = entity.get_state("rel_z")
    entity.set_look_at(entity.x + rx, entity.y, entity.z + rz)
```
FloatGoal:
- `float_can_use(entity, world, nav)`: `return (entity.in_water and entity.fluid_height >
  FLUID_JUMP_THRESHOLD) or entity.in_lava` — reads the NEW handle attrs (no RNG draw, matching the Go
  oracle's canUse).
- `float_tick(entity, world, nav)`: `if entity.rand_float() < 0.8: nav.jump()` — the `rand_float()`
  draw is the `entity.rand_float()` seam (l.78 / l.122 already used; backed by `e.ai.rng.nextFloat()`,
  plugin_entity.go l.404-414). **The draw is in tick() — lockstep with the Go oracle's tick draw.**

> The `rand_float()` draw ORDER must match the Go FloatGoal exactly (one `nextFloat()` per tick while
> active). Since FloatGoal@0 runs FIRST in both goal walks, its draw lands before stroll's in both —
> identical lockstep. In the DRY oracle world it never draws at all (see Critical Findings).

---

### `server/plugin_entity.go` — entity.in_water / fluid_height / in_lava read attrs + nav.jump()

**Analog (read attrs):** the Phase-29 `was_hurt` / `last_damage_type` frozen-scalar reads (l.177-191).
These are host-COMPUTED frozen scalars, re-resolved through `h.store()` (NOT `t.cur()`), gated on
`capEntitiesRead`, added to BOTH the `Attr` switch (l.156-191) AND `AttrNames` (l.197-204).

**Frozen-scalar read pattern to mirror** (l.177-190):
```go
	case "was_hurt":
		return starlark.Bool(e.hurtTime > 0), nil
	case "last_damage_type":
		return starlark.MakeInt(int(e.lastDamageSource.typeTag)), nil
```
Add, after re-resolving `e` (the `e, ok := h.store().get(h.id)` at l.152):
```go
	case "in_water":
		return starlark.Bool(t.mobInWater(e)), nil          // host-computed predicate (l.177 pattern)
	case "fluid_height":
		return starlark.Float(t.mobFluidHeight(e, water)), nil
	case "in_lava":
		return starlark.Bool(t.mobInLava(e)), nil
```
> Resolve the predicate through the handle's TickLoop on the owner (the `was_hurt`/`last_damage_type`
> note l.183/l.189 — "re-resolved through the region-bound h.store(), never t.cur()"). Add the three
> names to `AttrNames` (l.197-204) alongside `"was_hurt", "last_damage_type"`.

**Analog (nav.jump mutate):** `navHandle.stop` (l.734-748) is the closest mutate seam — re-resolves on
the owner, errors cleanly on a removed/non-AI entity, routes through an `e.ai` tick-owned seam:
```go
// plugin_entity.go l.734-748 — the navHandle mutate shape to mirror for nav.jump().
func (h *navHandle) stop(_ *starlark.Thread, _ *starlark.Builtin,
	_ starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
	if !h.caps.has(capNav) {
		return nil, capError("nav")
	}
	e, ok := h.store().get(h.id)
	if !ok {
		return nil, fmt.Errorf("entity %d no longer exists", h.id)
	}
	if e.ai == nil {
		return nil, fmt.Errorf("entity %d has no AI (cannot nav.stop)", h.id)
	}
	e.ai.clearWantTarget()
	return starlark.None, nil
}
```
`nav.jump()` mirrors this exactly but calls `e.ai.jumpControl.jump()` instead of `clearWantTarget()`,
and registers in `navHandle.Attr` (l.716-726) + `AttrNames` (l.728) alongside `path_to`/`has_path`/
`stop`. Gate on `capNav` (a nav mutate, like `path_to` l.778 / `stop` l.736).

> CONTEXT offers `nav.jump()` OR `entity.jump()`. `nav.jump()` is the cleaner analog (it parallels the
> JumpControl living on the nav-adjacent mobAI; the navHandle already routes JUMP-flag-claimed nav
> intents). If the planner prefers `entity.jump()`, the `entityHandle` mutate analog is `setVelocity`
> (l.266-281) — same shape, route to `e.ai.jumpControl.jump()`.

---

## Shared Patterns

### Per-entity seeded RNG (the lockstep oracle contract)
**Source:** `mobRandom(e)` (ai_goals_passive.go l.46-51) → `e.ai.rng.nextFloat()` (ai_random.go l.80).
**Apply to:** Go-native floatGoal.tick AND (via `entity.rand_float()`, plugin_entity.go l.404-414) the
Starlark `float_tick`. BOTH halves draw the SAME `nextFloat()` from the SAME id-seeded source in the
SAME tick slot — the lockstep the oracle (`TestPluginPigEqualsGoNativePig`) enforces.
```go
func mobRandom(e *Entity) *entityRandom {
	if e != nil && e.ai != nil && e.ai.rng != nil {
		return e.ai.rng
	}
	return newEntityRandom(defaultEntityRandomSeed)
}
```

### Goal callbacks draw RNG; the shared flow does NOT
**Source:** the established pattern documented in ai_mob.go l.108-110 + ai_goal.go l.30-31 ("a goal's
tick() SETS a target … never crosses a goroutine"); CONTEXT l.108-110.
**Apply to:** floatGoal (RNG in tick only) AND the new jumpControl.tick / aiStep jump branch (PURE, no
RNG). The arbitration (`goalCanBeReplacedForAllFlags`, ai_goal.go l.182-191) is also PURE — adding a
JUMP-flag goal touches no RNG in the shared walk.

### Jar-cited constants (the 1:1 discipline)
**Source:** fluid.go l.25-48 (`// javap … getDropOff -> iconst_1`), breath.go l.30-51, xp_orb.go
l.55-58 (`// [VERIFIED javap …]`).
**Apply to:** every new constant — `getFluidJumpThreshold = 0.0`, `jumpInLiquid +0.04`,
`jumpFromGround vy=0.42`, `noJumpDelay = 10`, lava decode levels. Each gets a doc-comment citing the
jar class/method, verified via `javap -c -p temp/cache/26.2-inner.jar` BEFORE writing.

### Plain-value tick-owned fields (snapshot-friendly)
**Source:** entity.go l.30-36 (the snapshot contract) + the MOB-SUB-01/02 field block l.167-210.
**Apply to:** `jumping bool`, `noJumpDelay int` on `*Entity` (or mobAI) — plain values, tick-owned
(TICK-05), never a pointer/slice/map.

### Frozen-scalar handle reads + owner re-resolve
**Source:** plugin_entity.go l.177-191 (`was_hurt`/`last_damage_type`) + l.152 (`h.store().get(h.id)`).
**Apply to:** `entity.in_water`/`fluid_height`/`in_lava` — host-compute, re-resolve via `h.store()`,
gate `capEntitiesRead`, register in `Attr` + `AttrNames`.

---

## No Analog Found

None. Every file has a strong in-repo analog. The ONE thing with no existing implementation is the LAVA
decode path (`fluidState` is water-only) — but `decodeFluid`/`waterLevelOf` is the EXACT structure to
EXTEND (not a from-scratch build), so it is classed "exact (EXTEND)" above, not "no analog".

---

## Critical Findings (read before planning)

1. **`decodeFluid` / `fluidState` is WATER-ONLY today** (fluid.go l.104-130). No `isLava`, no
   `lavaLevelOf`, no `block.Lava` recognition anywhere in the decode path. CONTEXT decision is FULL
   lava (NOT const-false) → the planner MUST extend `fluidState` (+`isLava`/`kind`), add a
   `lavaLevelOf` mirroring `waterLevelOf` (l.78-86) against `block.Lava`, branch `decodeFluid`, and
   audit every `.isWater` consumer so the water sim is unperturbed. Verify `block.Lava{Level}` exists in
   `level/block` and the lava legacy-level mapping via `javap LavaFluid` before writing.

2. **None of the jump-branch predicates exist** (`isInWater`/`isInLava`/`getFluidHeight`/`jumpInLiquid`/
   `jumpFromGround`/`isAffectedByFluids`/`setCanFloat` — Grep found only incidental comment matches in
   food.go/fall_damage.go/breath.go). All are new ports this phase. Port the MINIMUM the jump branch
   reads; cite the rest (CONTEXT Discretion). `onGround` (entity.go l.70) and `e.vy` (l.62) DO exist.

3. **The oracle world is DRY** (plugin_pig_test.go l.222-232: `build()` only `fillFloor`s a solid
   floor — places ZERO water/lava). Therefore FloatGoal.canUse is always false in
   `TestPluginPigEqualsGoNativePig` → FloatGoal never ticks → ZERO new RNG draws → the oracle is
   byte-identical with FloatGoal@0 added to BOTH pigs in lockstep (the SAFEST outcome the CONTEXT
   predicted, l.61-66). The plan MUST add the goal to BOTH `newPigAI` AND `vanilla_pig/main.star` in
   the SAME wave and confirm the oracle stays green before closing; water-jump behavior is tested in a
   SEPARATE dedicated test (keep the oracle world dry).

4. **`flagJump` already exists** (ai_goal.go l.41) and `flagByName["JUMP"]` already parses
   (plugin_mob_decl.go l.196) — no flag-enum or parser change needed for the JUMP-flag goal.

5. **JUMP slot insertion is RNG-free** — insert `jumpControl.tick` after `navigation.tick`
   (ai_mob.go l.123-124, the documented deferral). The pure jump branch + `noJumpDelay` decrement draw
   no RNG; the ONLY new RNG is FloatGoal.tick's `nextFloat()`, confined to the goal callback.

---

## Metadata

**Analog search scope:** `D:\ender\server` (fluid_physics.go, fluid.go, breath.go, ai_mob.go,
ai_goal.go, ai_random.go, ai_goals_passive.go, physics.go, tick_phases.go, entity.go, plugin_entity.go,
plugin_mob_decl.go, plugin_pig_test.go), `D:\ender\plugins\vanilla_pig\main.star`.
**Files scanned (read in full or targeted):** 14
**Pattern extraction date:** 2026-06-29
