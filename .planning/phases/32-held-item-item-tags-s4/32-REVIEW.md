---
phase: 32-held-item-item-tags-s4
reviewed: 2026-06-30T00:00:00Z
depth: standard
files_reviewed: 9
files_reviewed_list:
  - server/item_tag.go
  - server/inventory.go
  - server/ai_goals_passive.go
  - server/ai_mob.go
  - server/plugin_entity.go
  - plugins/vanilla_pig/main.star
  - server/assets/vanilla_pig/main.star
  - server/ai_goal_tempt_test.go
  - server/ai_mob_test.go
findings:
  critical: 0
  warning: 1
  info: 3
  total: 4
status: issues_found
---

# Phase 32: Code Review Report

**Reviewed:** 2026-06-30
**Depth:** standard
**Files Reviewed:** 9
**Status:** issues_found

## Summary

Phase 32 ports `net.minecraft.world.entity.ai.goal.TemptGoal` onto the pig (two goals @4 — carrot_on_a_stick literal id 887 + pig_food tag {1257,1258,1317}, speed 1.2, canScare=false, stopDistance 2.5) plus the S4 held-item read surface (`itemInTag`, `offhandWindowSlot`, `nearestPlayerHolding`/`playerHoldsTempt`, two host handles), in Go-native `newPigAI` and both byte-identical `vanilla_pig/main.star` copies.

I re-disassembled the authoritative jar (`TemptGoal`, `Pig.registerGoals`, the two registerGoals lambdas, `Attributes.TEMPT_RANGE`, `Animal.createAnimalAttributes`, `PathNavigation.createPath(Entity)`) and traced the port against it. **Jar fidelity is sound on every load-bearing axis.** Verified against bytecode:

- **canUse** — `calmDown--` first (`if (this.calmDown > 0) { --this.calmDown; return false; }`), THEN nearest player in `TEMPT_RANGE` via the shouldFollow selector, NO RNG. The Go `temptGoal.canUse` (ai_goals_passive.go:478-491) mirrors this exactly.
- **shouldFollow** = `items.test(mainHand) || items.test(offhand)` → `playerHoldsTempt` (ai_goals_passive.go:405-416) reads main (`heldWindowSlot(heldSlot)`) + off (`offhandWindowSlot` 45), slotIsEmpty-guarded. Match.
- **canContinueToUse** — the `if (this.canScare())` block (jar lines confirmed: dist<36, player-moved/rotated abort) precedes `return this.canUse()`. For the pig canScare=false the whole block is dead; the Go port keeps it as a cited dead skip (ai_goals_passive.go:497-504). Structurally present, not silently dropped. Confirmed.
- **tick** — `setLookAt(player)` then `distanceToSqr < stopDistance*stopDistance` (2.5²=6.25) → stopNavigation else navigateTowards. Match (ai_goals_passive.go:517-531).
- **stop** — player=null, stopNavigation, `calmDown = reducedTickDelay(100)`, isRunning=false. `reducedTickDelay(100)=(100+1)/2=50`. Match.
- **flags** {MOVE,LOOK}, **priority 4**, **TWO goals carrot FIRST then pig_food** — verified via BootstrapMethods: first TemptGoal (offset 56) → `lambda$registerGoals$0` = CARROT_ON_A_STICK; second (offset 81) → `lambda$registerGoals$1` = PIG_FOOD. `newPigAI` (ai_mob.go:263-264) and the `.star` decls (lines 372-389) both register carrot first. The selector's insertion-sort (`priority <= priority`) keeps carrot ahead of pig_food among equals; `canBeReplacedBy` needs `other.priority < this.priority` (4<4 false), so no selector edit is needed — confirmed correct.
- **TEMPT_RANGE default 10.0** — jar `Attributes.<clinit>` `ldc2_w double 10.0d`; `Animal.createAnimalAttributes` adds TEMPT_RANGE, so a real pig carries it; `getAttributeValue` folds the registration default 10.0 even for a map-less entity. Verified.

The **move-target-Y question**: vanilla `navigateTowards(player)` = `navigation.moveTo(player, 1.2)` → `createPath(Entity, 1)` → `createPath(ImmutableSet.of(target.blockPosition()), 16, true, 1)`. The target IS `player.blockPosition()` (i.e. the FLOOR of the player position), and the A* resolves it via best-node-distance, returning a best-effort partial when the exact block is unreachable. The Go path floors the raw player Y (`floorI(m.wantY)`, ai_mob.go:191) and the pathfinder tracks `bestNode` for a best-effort partial (pathfinder.go:285-307,352-353). **Both vanilla and Go floor the player position and rely on best-effort node resolution — the raw-Y target is a faithful match, NOT a latent bug.** Live testing (pig tempted to 0.00) corroborates. See IN-01 for one genuine-but-cosmetic divergence (the `radiusOffset`/`above` search-region difference) that does not change the committed target.

Oracle safety holds: canUse draws zero RNG, the oracle's player (11.5,65,8.5 vs pig 8.5,65,8.5, 3.0 apart) holds no tempt item → `playerHoldsTempt` false → both TemptGoals' canUse false → stream preserved. Both `.star` files are byte-identical (diff empty). No new deps; `data/tag` already imported by damage_source.go; CGO=0 preserved (pure map lookup).

The one WARNING and three INFO items below are robustness/coverage/documentation gaps, not correctness defects in the common path.

## Warnings

### WR-01: `temptGoal.tick` / `temptGoal.stop` dereference `e.ai` without the nil-guard the sibling goals use

**File:** `server/ai_goals_passive.go:527,529,537`
**Issue:** `temptGoal.tick` calls `e.ai.clearWantTarget()` / `e.ai.setWantTarget(...)` and `temptGoal.stop` calls `e.ai.clearWantTarget()` with no `e.ai != nil` guard. Every sibling goal that touches the AI guards it — `randomStrollGoal.start` (`if e.ai == nil || ...`, line 245), `randomStrollGoal.stop` (`if e.ai != nil`, line 255), `randomStrollGoal.canContinueToUse` (`e.ai != nil && e.ai.hasTarget`, line 237). In production these methods are only reachable through `e.ai.goals`, so `e.ai` is non-nil by construction and there is no live crash today. But the inconsistency is a latent footgun: a future caller or a hand-built test `temptGoal` driven directly with a nil `e.ai` (the panic/stroll tests construct goals standalone) would nil-panic here where the same pattern is defensive everywhere else in the file. The plugin path is shielded differently — `moveTo` checks `e.ai == nil` and returns a clean error (plugin_entity.go:366) — which underlines that the Go-native goal is the only `setWantTarget`/`clearWantTarget` caller that omits the guard.
**Fix:** Mirror the sibling goals' guard:
```go
func (g *temptGoal) tick(_ *TickLoop, e *Entity) {
    if !g.hasPlayer || e.ai == nil {
        return
    }
    // ... existing body ...
}

func (g *temptGoal) stop(_ *TickLoop, e *Entity) {
    g.hasPlayer = false
    if e.ai != nil {
        e.ai.clearWantTarget()
    }
    g.calmDown = reducedTickDelay(100)
    g.isRunning = false
}
```
(In `stop`, keep the `calmDown`/`isRunning` writes outside the guard so the cooldown is set even if the AI is somehow detached — matching the bytecode order where `stopNavigation()` precedes the calmDown assignment but the field writes are unconditional.)

## Info

### IN-01: Go path search region differs from vanilla `createPath(Entity)`'s `radiusOffset=16, above=true` (does not change the committed target)

**File:** `server/ai_mob.go:190-194`, `server/navigation.go:126-137`
**Issue:** Vanilla `navigateTowards(player)` routes through `createPath(Entity target, 1)` = `createPath(targets, radiusOffset=16, above=true, reachRange=1)`, which sizes the `PathNavigationRegion` from `mob.blockPosition().above()` with `radius = maxPathLength + 16`. The Go `requestPath` always uses `navFollowRange`/`navReachRange` and the mob's own block position (no `above()`, no entity-specific `+16` radiusOffset). The committed target block is identical (floor of player pos in both), and the pig reliably reaches the player in live + unit tests, so this is not a behavioral defect for the flat-floor common case. It is a faithful-scope simplification worth a one-line cite so a future uneven-terrain pass (where the larger entity-path search region matters) does not assume the entity overload was reproduced.
**Fix:** Add a cite on `requestPath` (or the temptGoal tick move seam) noting that vanilla's `moveTo(Entity)` uses `createPath(Entity)` with `radiusOffset=16, above=true`, whereas the Go path uses the position overload's region sizing — equal committed target, deferred region-size parity.

### IN-02: Follow tests never exercise the `distSqr < 6.25` stop branch of `temptGoal.tick`

**File:** `server/ai_goal_tempt_test.go:84-135`
**Issue:** Both follow tests place the player at distSqr 16 (4 east) and 9 (3 north), i.e. always in the `else` (navigate) arm. The stop arm (`if dx*dx+dy*dy+dz*dz < g.stopDistance*g.stopDistance { e.ai.clearWantTarget() }`, ai_goals_passive.go:526-527) — the vanilla `distanceToSqr < 6.25 → stopNavigation()` behavior — has no direct assertion. A regression that inverted the comparison or swapped clear/set would not be caught by this suite (it would still pass the navigate-arm tests).
**Fix:** Add a case with the player within 2.5 blocks (e.g. `pig.x+1`, distSqr 1 < 6.25), assert `canUse` true, then after `start`+`tick` assert `pig.ai.hasTarget == false` (the stop arm cleared the want) and the head still aimed at the player.

### IN-03: `temptGoal.start` comment claims a re-capture the code does not perform (stale doc)

**File:** `server/ai_goals_passive.go:506-510`
**Issue:** The doc says "re-capture here per the bytecode (px = player.getX(); ...)" but `start` only sets `g.isRunning = true` — it does not re-capture px/py/pz. This is behaviorally correct (canUse already stashed the position on the same tick, and `canContinueToUse`→`canUse` refreshes it each subsequent tick before `tick` reads it, which faithfully reproduces vanilla's per-tick live-player read), but the comment describes code that isn't there, which will mislead the next reader auditing fidelity.
**Fix:** Reword to: "The player pos is captured in canUse and refreshed each tick by canContinueToUse→canUse (vanilla re-reads the live player ref in tick); start() only needs the isRunning flag — the px/py/pz re-read in vanilla start() is redundant given the canUse stash."

---

_Reviewed: 2026-06-30_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
