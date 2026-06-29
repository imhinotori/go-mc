---
phase: 30-jumpcontrol-fluid-s1
reviewed: 2026-06-29T00:00:00Z
depth: standard
files_reviewed: 11
files_reviewed_list:
  - server/fluid.go
  - server/fluid_physics.go
  - server/jump.go
  - server/ai_mob.go
  - server/ai_goals_float.go
  - server/ai_goal.go
  - server/entity.go
  - server/plugin_entity.go
  - server/navigation.go
  - plugins/vanilla_pig/main.star
  - server/assets/vanilla_pig/main.star
findings:
  critical: 1
  warning: 3
  info: 2
  total: 6
status: issues_found
---

# Phase 30: Code Review Report

**Reviewed:** 2026-06-29
**Depth:** standard
**Files Reviewed:** 11
**Status:** issues_found

## Summary

Phase 30 ports the mob JumpControl impulse seam, the four mob fluid predicates
(mobInWater/mobInLava/mobFluidHeight/getFluidJumpThreshold), a lava-aware fluid decode, and wires
FloatGoal@0 onto the pig in Go-native + plugin lockstep. The numeric ports are excellent: the exact
double `0.03999999910593033` (not `0.04`), `jumpFromGround` as `max(0.42, vy)` (not assignment), the
`noJumpDelay` full control flow (decrement at top + reset-to-0 on both falsey branches), and the
jar-correct `getFluidJumpThreshold = getEyeHeight()<0.4 ? 0.0 : 0.4` (catching the plan's wrong `0.0`
assumption) are all faithful and well-cited. The lava decode audit holds — every water-sim consumer
keeps its `if fs.isWater` guard, lava decodes `isWater:false`, and no `.isWater` consumer was
perturbed (build clean, no stray `isLava` reads). Tick ordering is correct: the jump impulse lands in
`tickAI` before `tickPhysics` integrates gravity. The JUMP slot and `entityJumpStep` are RNG-free; the
single new draw (`nextFloat()<0.8`) is confined to `floatGoal.tick`. Both `main.star` copies are
byte-identical.

There is, however, one genuine fidelity defect that the passing test suite cannot catch: the
Go-native `floatGoal` does NOT override `canContinueToUse`, so it inherits `baseGoal`'s `return true`,
while the plugin FloatGoal (no `can_continue` declared) correctly falls back to `canUse`. Vanilla
`FloatGoal` has no `canContinueToUse` override and thus inherits `Goal.canContinueToUse() { return
canUse(); }`. The Go pig therefore diverges from BOTH the jar AND its own plugin twin once a pig
leaves water. The DRY oracle world masks this (canUse is always false there, so the goal never
starts), but it is a real lockstep break in any wet world and a behavior bug in live gameplay.

## Critical Issues

### CR-01: Go-native floatGoal never stops once started — canContinueToUse diverges from jar and from the plugin twin

**File:** `server/ai_goals_float.go:40-81` (missing override) vs `server/ai_goal.go:95`
**Jar method it should match:** `net.minecraft.world.entity.ai.goal.FloatGoal` (no `canContinueToUse`
override) → inherits `net.minecraft.world.entity.ai.goal.Goal.canContinueToUse()` which is
`return this.canUse();`.

**Issue:** `floatGoal` embeds `baseGoal` and overrides only `canUse`, `requiresUpdateEveryTick`, and
`tick`. It does NOT override `canContinueToUse`, so it uses `baseGoal.canContinueToUse` which returns
a hardcoded `true` (`server/ai_goal.go:95`). That contradicts the codebase's own documented
convention (`ai_goal.go:82-87`: "every real goal here defines its own canContinueToUse") — every other
ported goal (`randomStrollGoal`, `lookAtPlayerGoal`, `randomLookAroundGoal`) overrides it, FloatGoal
is the lone exception.

Consequences once a pig is in water and then leaves it (e.g. swims to shore, or the water drains):
1. **Fidelity break vs jar.** Vanilla FloatGoal stops the instant `canUse()` (the fluid predicate)
   goes false, because `canContinueToUse` IS `canUse`. The Go pig keeps the goal running indefinitely.
2. **Lockstep break vs the plugin twin.** The plugin FloatGoal declares no `can_continue`, so
   `starlarkGoal.canContinueToUse` falls back to `canUse` (`server/plugin_mob_ai.go:126-129`). Plugin
   pig stops FloatGoal on leaving water; Go pig does not. `TestPluginPigEqualsGoNativePig` cannot see
   this only because its world is dry (`plugin_pig_test.go` uses `fillFloor` → canUse never true → goal
   never starts → `canContinueToUse` never evaluated). Run the oracle in a wet world and the streams
   diverge.
3. **RNG-stream divergence.** Because `floatGoal.requiresUpdateEveryTick()` is true, the stuck-running
   Go goal draws `nextFloat()` EVERY tick even on dry land, while the plugin pig (goal stopped) draws
   nothing. The two per-mob RNG streams desynchronize permanently after the first water exit.
4. **JUMP flag never released.** The Go pig holds the JUMP control flag forever, blocking any future
   lower-precedence JUMP goal (PanicGoal leap, etc.) from claiming it on land.

**Fix:** Add the explicit override delegating to `canUse`, matching every other ported goal and the
plugin fallback:
```go
// canContinueToUse: FloatGoal has no override in the jar, so it inherits
// Goal.canContinueToUse() { return canUse(); }. Mirror that here (baseGoal's default
// return-true does NOT replicate the virtual dispatch — ai_goal.go:82-87).
//	[VERIFIED javap FloatGoal: no canContinueToUse method -> inherits Goal.canContinueToUse = canUse.]
func (g *floatGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}
```
After fixing, add a regression test: start FloatGoal in water, move the pig to dry land, assert the
goal stops (frees JUMP, ceases drawing RNG) — and ideally a wet-world oracle assertion so the lockstep
canary actually covers this path.

## Warnings

### WR-01: getNewLiquid "falling" rule treats lava-above as water-above (latent, currently dormant)

**File:** `server/fluid.go:430-435`
**Jar method:** `net.minecraft.world.level.material.FlowingFluid.getNewLiquid` (the `getFluidAbove`
same-fluid check).

**Issue:** `getNewLiquid` is water-sim-only and gates its neighbor reads on `isWater`, but the
falling-above rule reads `af := t.fluidAt(abovePos); if af.isWater { ... }`. Now that `decodeFluid`
returns lava states, a water cell with LAVA directly above it correctly does NOT take the falling
branch here (because `af.isWater` is false for lava) — so this specific line is fine today. The latent
hazard is the inverse and the deferred lava flow sim: the comment at `fluid.go:177-182` says the
DEFERRED lava flow sim will "route through the same primitive." When that sim is built and calls
`getNewLiquid`/`spread`/`getSpread`/`isSolidAt`, every one of those functions hardcodes `isWater` and
will silently treat lava as either air or solid. This is correct ONLY because lava never enters the
water scheduler today; it is a trap for the deferred lava-flow work.

**Fix:** No change required this phase (decode-only is the stated scope). Before the deferred lava flow
sim lands, parameterize the flow primitives on `fluidKind` (as `fluidSurfaceHeightOf` already was)
rather than hardcoding `isWater`, or the lava sim will misread its own cells. Add a one-line TODO at
`fluid.go:401` and `fluid.go:452` pointing at this so the deferral is not lost.

### WR-02: isSolidAt treats lava as a solid wall to the water sim — water-into-lava interaction silently absent

**File:** `server/fluid.go:312-324`
**Jar method:** `net.minecraft.world.level.block.LiquidBlock` / `FlowingFluid` interaction +
`Block.shouldDisplaceLiquids` (water flowing into lava → stone/cobblestone/obsidian).

**Issue:** `isSolidAt` returns `true` for any non-air, non-water block — which now includes lava (it
only excludes via `waterLevelOf`, not `lavaLevelOf`/`isLava`). So flowing water hits a lava cell and
stops as if against stone; no stone/cobble/obsidian is generated. This matches pre-Phase-30 behavior
(lava previously decoded as a zero fluidState and was likewise non-water → "solid"), so it is NOT a
regression, but the lava decode landing this phase makes the gap newly relevant and easy to mistake
for "done."

**Fix:** Out of scope for decode-only (cite it). When the lava flow sim / fluid-interaction lands,
`isSolidAt` (and `canReplace`) must distinguish lava from a true solid so the displacement reaction can
fire. Add an explicit cited deferral note on `isSolidAt` so a future reader does not assume
water/lava interaction works.

### WR-03: entityJumpStep re-scans the AABB four times per jumping mob via independent full-box walks

**File:** `server/jump.go:82-95` (calls `mobInLava`, `mobFluidHeight`, `mobInWater`,
`getFluidJumpThreshold`, each a fresh AABB scan) and `isInShallowFluid` (`jump.go:121-122`) re-scans
again.
**Jar method:** `net.minecraft.world.entity.LivingEntity.aiStep` reads the cached
`EntityFluidInteraction` height/in-fluid state computed once per tick in `baseTick`, not by
re-scanning at jump time.

**Issue:** Vanilla computes the fluid state ONCE per tick (`Entity.updateFluidInteraction` /
`EntityFluidInteraction.update`) and the jump branch reads the cached `fluidHeight`/`isInWater`/
`isInLava`. The port recomputes by independently walking the entity AABB in `mobInLava`,
`mobInWater`, and twice in `mobFluidHeight` (once for the height, again inside `isInShallowFluid`). This
is a correctness-adjacent fidelity gap, not just perf: if any cell in the AABB straddles a chunk edge
that is mid-load, the separate scans could observe different world snapshots within the same tick
(though on the single tick goroutine that window is currently nil). Performance is explicitly
out-of-scope for v1, so this is flagged for the fidelity/structure angle.

**Fix:** Acceptable for v1 (single-threaded tick, correct values). When the
`EntityFluidInteraction`-style per-tick cache lands, compute `inWater`/`inLava`/`fluidHeight(WATER)`/
`fluidHeight(LAVA)` once at the top of the entity tick and have `entityJumpStep` read the cached
values, matching the jar's single-update model. Note this as a cited deferral on `entityJumpStep`.

## Info

### IN-01: fluidSurfaceHeightOf duplicates breath.go's fluidSurfaceHeight body

**File:** `server/fluid_physics.go:149-159` vs `server/breath.go:142-152`

**Issue:** `fluidSurfaceHeightOf(cell, fs, kind)` is `fluidSurfaceHeight(cell, fs)` with the
above-check parameterized on `kind`. The water path is now expressed twice. The summary deliberately
left `breath.go`'s helper untouched "so no water consumer is perturbed," which is a reasonable
conservative choice, but the bodies are otherwise identical.

**Fix:** Once the kind-parameterized version is trusted, `breath.go`'s `fluidSurfaceHeight(cell, fs)`
could delegate: `return t.fluidSurfaceHeightOf(cell, fs, fluidWater)`. Single source of truth, zero
behavior change for the water path (lower priority — the duplication is small and well-commented).

### IN-02: getFluidJumpThreshold/mobEyeHeight nil-entity returns 0.0 → would classify a nil mob as "below cutoff"

**File:** `server/fluid_physics.go:215-244`

**Issue:** `mobEyeHeight(nil)` returns `0`, so `getFluidJumpThreshold(nil)` returns `0.0` (the
`< 0.4` branch). All current callers (`entityJumpStep`, `floatGoal.canUse`, `isInShallowFluid`) guard
`e == nil` upstream or never pass nil, so this is not reachable today — but the function silently
treats a nil mob as a valid short entity rather than signaling the programming error. Cosmetic/defensive
only.

**Fix:** Low priority. If desired, document that a nil entity yields the conservative `0.0` threshold,
or assert non-nil at the call boundary. No behavior change needed for the shipped callers.

---

_Reviewed: 2026-06-29_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
