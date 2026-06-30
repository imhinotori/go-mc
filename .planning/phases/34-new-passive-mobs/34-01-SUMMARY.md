---
phase: 34-new-passive-mobs
plan: 01
subsystem: mob-passive
tags: [mob, passive, cow, jar-port, milk, mob-interact, starlark-plugin, create-filled-result, mob-pass]

# Dependency graph
requires:
  - phase: 34-00
    provides: "entity.nearest_player_holding_food(tag,range) parameterized tempt handle; the handleInteract dispatcher slot the sheep shear gate established (tryMilkCow slots into the same spot, wave-ordered); encodeSoundEntity + broadcastToTrackers + soundSourceNeutral sound seam; spawnDeclaredMob reseed + baby splice"
provides:
  - "vanilla_cow plugin pair (plugins/ + server/assets/ byte-identical): the 8-goal AbstractCow.registerGoals subset reusing the proven Go goal runtime (no carrot Tempt; BreedGoal @2)"
  - "TickLoop.tryMilkCow (AbstractCow.mobInteract 1:1: empty BUCKET + adult -> MILK_BUCKET + entity.cow.milk sound; baby/non-bucket fall through to feed)"
  - "TickLoop.createFilledResult (ItemUtils.createFilledResult 1:1: consume 1; single -> replace hand, stack -> shrink + inventoryAdd, drop fallback)"
  - "categoryOf(entity.Cow.ID) -> CREATURE (the cow consumes the CREATURE spawn budget)"
  - "server/cow_test.go: cow boot-load + 8-goal-set + milk (single + stack) + baby/non-bucket fall-through + behavior tests"
affects: [34-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the pig .star template copies cleanly to a new passive mob: delete the carrot Tempt, retune attributes/speeds, point the tempt at the parameterized food handle, re-index the goals to the jar registerGoals order — the goal callbacks are mob-agnostic and copied verbatim"
    - "a per-mob interact extra (cow milk) slots into handleInteract BEFORE the feed path, mob-gated on e.typ == entity.<Mob>.ID + additive, so the pig oracle stream is byte-identical (zero new draws on the pig path)"
    - "ItemUtils.createFilledResult ported as a reusable TickLoop helper (the survival item-swap: consume + replace-hand-or-add-to-inventory) — a future bucket/bottle interact reuses it"

key-files:
  created:
    - "plugins/vanilla_cow/main.star"
    - "plugins/vanilla_cow/plugin.toml"
    - "server/assets/vanilla_cow/main.star"
    - "server/assets/vanilla_cow/plugin.toml"
    - "server/cow_test.go"
  modified:
    - "server/attack_dispatch.go"
    - "server/mob_category.go"

key-decisions:
  - "Gated tryMilkCow on mob.typ == entity.Cow.ID (Entity.typ), NOT the plan's mob.baseType — baseType is a mobDecl field, not an Entity field (the same wave-1 deviation 34-00 took for the sheep shear gate). Faithful equivalent, additive, mob-gated."
  - "COW_MILK emitted via the entity-attached broadcastToTrackers/encodeSoundEntity seam (the chicken egg-lay + mob hurt/death model) rather than a player-local sound — player.playSound on the server reaches the tracking players; NEUTRAL category (Cow/Animal getSoundSource), fixed 1.0/1.0 (NO RNG)."
  - "Ported ItemUtils.createFilledResult VERBATIM as TickLoop.createFilledResult (3-arg overload = grow=true): single bucket -> hand becomes milk; bucket stack -> consume 1 + inventoryAdd milk + playerDrop fallback. v1 is always survival (hasInfiniteMaterials const-false)."
  - "Added categoryOf(Cow) -> CREATURE (Rule 2 faithful): data/entity Cow.Type == 'creature' (the jar codegen source); vanilla EntityType.COW is MobCategory.CREATURE. Additive (pig + default arms untouched). The sibling sheep plan cite-deferred its own categoryOf edit; the cow plan needs it for TestCowBehavior + natural-spawn parity."

patterns-established:
  - "New passive mob = a pig .star subset + a per-mob interact extra wired mob-gated into the proven seams; the pig oracle stays the single bit-exact gate."

requirements-completed: [MOB-PASS-01]

# Metrics
duration: ~30min
completed: 2026-06-30
---

# Phase 34 Plan 01: Cow (new passive mob) Summary

**The vanilla Cow as a jar-faithful Starlark plugin (MOB-PASS-01): the 8-goal AbstractCow.registerGoals subset reusing the proven Go goal runtime, plus the cow-only milking interact (AbstractCow.mobInteract: an empty bucket on an adult -> a milk_bucket + the entity.cow.milk sound) ported 1:1 with NO RNG.**

## Performance

- **Duration:** ~30 min
- **Tasks:** 2 (Task 1: plugin pair; Task 2: tryMilkCow + tests, TDD)
- **Files created:** 5
- **Files modified:** 2

## Accomplishments

- **vanilla_cow plugin pair** (byte-identical repo-root + embed copy): ports `net.minecraft.world.entity.animal.cow.AbstractCow.registerGoals` 1:1 — FloatGoal@0, PanicGoal(2.0)@1, BreedGoal(1.0)@2, TemptGoal(1.25, COW_FOOD)@3, FollowParentGoal(1.25)@4, WaterAvoidingRandomStroll(1.0)@5, LookAtPlayer(6.0)@6, RandomLookAround@7. No carrot_on_a_stick Tempt (the cow has ONE Tempt -> BreedGoal sits at @2); the cow_food tempt routes through the 34-00 parameterized `nearest_player_holding_food("cow_food", range)` handle. Attributes max_health 10.0 / movement_speed 0.2 (Cow.createAttributes). The goal callbacks are copied verbatim from the pig (mob-agnostic).
- **tryMilkCow** (`AbstractCow.mobInteract` 1:1, NO RNG): a held empty `bucket` (1040) on an `!isBaby()` cow -> the `entity.cow.milk` sound (449, NEUTRAL, 1.0/1.0) + `createFilledResult` swaps the bucket for a `milk_bucket` (1046) -> SUCCESS; a non-bucket item or a baby returns false and falls through to the feed path (`super.mobInteract`). Wired into `handleInteract` BEFORE `tryFeedAnimal`, `entity.Cow.ID`-gated + additive.
- **createFilledResult** (`ItemUtils.createFilledResult` 1:1, grow=true): consume 1 of the bucket; a single bucket -> the hand becomes the milk_bucket; a bucket stack -> shrink 1 + `inventoryAdd` the milk (with a `playerDrop` inventory-full fallback).
- **categoryOf(Cow) -> CREATURE** so the cow consumes the CREATURE spawn budget (jar-cited: `data/entity` Cow.Type == "creature").
- **The pig oracle stays byte-identical**: every edit is additive + mob-gated; the pig path draws ZERO new RNG. `TestPluginPigEqualsGoNativePig` PASS.

## Task Commits

1. **Task 1: vanilla_cow plugin pair** — `d454ff57` (feat)
2. **Task 2 (TDD RED): failing cow tests** — `d429836d` (test)
3. **Task 2 (TDD GREEN): tryMilkCow + createFilledResult + categoryOf** — `ef8651ea` (feat)

_TDD gate: the `test(...)` RED commit (`d429836d`) precedes the `feat(...)` GREEN commit (`ef8651ea`). No REFACTOR commit needed (the GREEN implementation was clean)._

## Files Created/Modified

- `plugins/vanilla_cow/main.star` — the 8-goal AbstractCow.registerGoals declaration (no carrot Tempt; cow_food tempt; attrs 10.0/0.2)
- `plugins/vanilla_cow/plugin.toml` — least-privilege caps (entities.read/write, world.read, nav); milking is host-side, no widened cap
- `server/assets/vanilla_cow/main.star` — byte-identical embed copy (the swap source of truth)
- `server/assets/vanilla_cow/plugin.toml` — byte-identical embed copy
- `server/attack_dispatch.go` — `tryMilkCow` (AbstractCow.mobInteract) + `createFilledResult` (ItemUtils.createFilledResult), the milk gate wired into `handleInteract` before the feed path
- `server/mob_category.go` — `categoryOf(Cow) -> CREATURE`
- `server/cow_test.go` — boot-load + 8-goal-set + milk (single + stack) + baby/non-bucket fall-through + behavior tests

## Decisions Made

See `key-decisions` frontmatter. In brief: the milk gate is `e.typ`-based (Entity carries the wire type; `baseType` is a mobDecl field — the wave-1 convention); the sound rides the entity-attached broadcast seam; `createFilledResult` is a verbatim ItemUtils port; `categoryOf(Cow) -> CREATURE` is a jar-cited faithful addition.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - API] `mob.baseType` -> `mob.typ == entity.Cow.ID` for the milk gate**
- **Found during:** Task 2 (handleInteract wiring)
- **Issue:** The plan's wiring `if mob.baseType == entity.Cow && t.tryMilkCow(...)` does not compile — `baseType` is a field of `mobDecl`, not of `Entity`. `Entity` carries the wire type as `e.typ` (an `entity.ID`). This is the SAME reconciliation 34-00 took for the sheep shear gate (its SUMMARY decision #1).
- **Fix:** Gated on `mob.typ == entity.Cow.ID`, the faithful equivalent. Additive + mob-gated, so the pig/sheep/chicken paths are a zero-cost skip.
- **Files modified:** server/attack_dispatch.go
- **Verification:** `TestMilkCow*` pass; `TestPluginPigEqualsGoNativePig` byte-identical.
- **Committed in:** `ef8651ea`

**2. [Rule 2 - Missing Critical] Added `categoryOf(Cow) -> CREATURE`**
- **Found during:** Task 2 (TestCowBehavior)
- **Issue:** `categoryOf` only mapped Pig -> CREATURE; the cow (Cow.Type == "creature" in the jar-derived `data/entity`) fell to MISC, which is unfaithful — vanilla `EntityType.COW` is `MobCategory.CREATURE`, and the cow must consume the CREATURE spawn budget like the pig. The plan's `TestCowBehavior` asserts `categoryOf(Cow) -> CREATURE`.
- **Fix:** Added `case entity.Cow.ID: return categoryCreature` (additive — the pig + default arms are untouched). Cited `data/entity` Cow.Type == "creature".
- **Files modified:** server/mob_category.go
- **Verification:** `TestCowBehavior` PASS; the pig + default category accounting is unperturbed.
- **Committed in:** `ef8651ea`

**3. [Rule 3 - API] `createFilledResult` ported as a host helper using the real inventory APIs**
- **Found during:** Task 2 (tryMilkCow implementation)
- **Issue:** The plan sketched "use the existing give/replace + shrinkHeldItem helpers". There is no single helper that matches `ItemUtils.createFilledResult`'s exact replace-vs-add semantics (single -> replace hand, stack -> consume + inventoryAdd + drop). `shrinkHeldItem` only shrinks; `inventoryAdd` (the real `Inventory.add(ItemStack)` port) + `playerDrop` (the real `Player.drop` port) supply the rest.
- **Fix:** Ported `ItemUtils.createFilledResult(grow=true)` VERBATIM as `TickLoop.createFilledResult`, composing `inventoryAdd` + `playerDrop`, so the bucket->milk swap matches the jar exactly for both the single-bucket and the bucket-stack cases. Used the verbatim `ItemUtils` bytecode (javap'd this session) to pin the consume/isEmpty/add/drop order.
- **Files modified:** server/attack_dispatch.go
- **Verification:** `TestMilkCow` (single -> hand becomes milk) + `TestMilkCowStackShrinks` (stack -> shrink 1 + milk in inventory) PASS.
- **Committed in:** `ef8651ea`

---

**Total deviations:** 3 (1 missing-critical, 2 API-reconciliation). All necessary for faithfulness/compilation. No scope creep — the cow plugin pair, the milk interact, and the cited CREATURE category are exactly the plan's scope.
**Impact on plan:** None — every deviation is a faithful realization of the plan's intent against the real APIs/jar bytecode.

## Issues Encountered

- **Interim phase-suite RED from the TDD RED commit:** the 34-03 (chicken) executor recorded in `deferred-items.md` that the cow RED commit (`d429836d`) blocked the package-wide `go test ./server/` (cow_test.go referenced not-yet-landed symbols). This is the expected TDD RED state; the GREEN commit (`ef8651ea`) landed `tryMilkCow`/`createFilledResult`/`categoryOf(Cow)` and the suite is GREEN again. Resolution noted in `deferred-items.md`.

## Sibling-plan note

The wave-2 plans 34-02 (sheep) and 34-03 (chicken) landed commits on the shared `ender-776` branch interleaved with this plan's commits. This plan touched ONLY its 7 plan-scoped files (`plugins/vanilla_cow/*`, `server/assets/vanilla_cow/*`, `server/attack_dispatch.go`, `server/cow_test.go`) plus the cited `server/mob_category.go` cow case. No pig files were touched (`git diff` confirms no `vanilla_pig`/`newPigAI`/pig-goal edits).

## Next Phase Readiness

- The cow is a complete, jar-faithful new passive mob: boot-loads, renders as entity.Cow.ID, runs the 8 goals, milks. Ready for the 34-04 phase gate (the `-race` Docker run + the phase-wide suite).
- 34-04 should run the phase gate now that cow (this plan), sheep (34-02), and chicken (34-03) have all landed their Go symbols (the cow gap that blocked the suite is closed).

## Verification

- `CGO_ENABLED=0 go build ./...` -> exit 0.
- `CGO_ENABLED=0 go vet ./server/` -> clean.
- `CGO_ENABLED=0 go test ./server/ -count=1` -> ok (7.77s, full suite).
- Cow tests (6): TestCowBootLoads, TestMilkCow, TestMilkCowStackShrinks, TestMilkCowBabyFallsThrough, TestMilkCowNonBucketFallsThrough, TestCowBehavior -> all PASS.
- **Pig oracle byte-identical:** TestPluginPigEqualsGoNativePig PASS (+ all TestVanillaPig*/TestPluginPig*).
- `.star` + `.toml` copies byte-identical: `diff plugins/vanilla_cow/main.star server/assets/vanilla_cow/main.star` empty; `diff plugins/vanilla_cow/plugin.toml server/assets/vanilla_cow/plugin.toml` empty.

## Self-Check: PASSED
- plugins/vanilla_cow/main.star, plugins/vanilla_cow/plugin.toml: FOUND
- server/assets/vanilla_cow/main.star, server/assets/vanilla_cow/plugin.toml: FOUND
- server/attack_dispatch.go, server/mob_category.go, server/cow_test.go: FOUND (modified/created)
- Commit d454ff57 (Task 1): FOUND
- Commit d429836d (Task 2 RED): FOUND
- Commit ef8651ea (Task 2 GREEN): FOUND

---
*Phase: 34-new-passive-mobs*
*Completed: 2026-06-30*
