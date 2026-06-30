---
phase: 34-new-passive-mobs
plan: 00
subsystem: mob-shared-infra
tags: [mob, passive, sheep, chicken, cow, jar-port, eat-block, shear, data-wool, egg-lay, slow-fall, mob-pass]
requires:
  - "server/plugin_entity.go entity handle (Attr/AttrNames + nearestPlayerHolding + h.resolve region-bound)"
  - "server/entity_encode.go entityDataEntry framing (babyDataEntry pattern, byteSerializerID, encodeSetEntityData, encodeSoundEntity, encodeEntityEvent)"
  - "server/attack_dispatch.go handleInteract dispatcher + tryFeedAnimal (held-read + shrinkHeldItem)"
  - "server/plugin_mob_decl.go spawnDeclaredMob (reseedMobAI + baby DATA_BABY_ID splice)"
  - "server/tick_phases.go serverAiStep snapshot loop"
  - "level/loot LoadTable/Roll/NewEntityLootContext (shearing/sheep/white.json embedded)"
  - "level/block DefaultStateID (grass_block/dirt) + world.ChunkManager GetBlock/SetBlock"
  - "data/item Egg(1060)/Shears(1134); data/entity Sheep/Chicken/Cow; data/tag cow/sheep/chicken_food"
provides:
  - "entity.nearest_player_holding_food(tag,range) parameterized tempt-player scan handle"
  - "entity.eat_grass_block + entity.eat_broadcast_byte10 sheep eat handles"
  - "TickLoop.eatGrassBlock/sheepAte/setSheared/trySheepShear/shearSheep (sheep_eat.go)"
  - "entity_encode dataWoolIndex=18 + woolDataEntry (BYTE serializer 0) + soundSourcePlayers(7)"
  - "TickLoop.chickenAiStep/dropChickenEgg (chicken_aistep.go) + chickenIsChickenJockey const"
  - "Entity.sheared + Entity.eggTime fields + Entity.canAgeUp()"
  - "ai_goals_eat.go shared EatBlockGoal constants (eatAnimationTicks/eatActTick/eatGateBound*/eatBlockGoalFlags)"
affects:
  - "34-01 (cow): tryMilkCow slots into the SAME handleInteract spot the sheep shear gate uses (wave-ordered, no collision)"
  - "34-02 (sheep .star): calls nearest_player_holding_food('sheep_food'), eat_grass_block, eat_broadcast_byte10, the EatBlockGoal constants"
  - "34-03 (chicken .star): calls nearest_player_holding_food('chicken_food'); the host owns chickenAiStep already wired here"
  - "34-04 (gate): the new mob behavior + RNG tests; the -race Docker run"
tech-stack:
  added: []
  patterns:
    - "parameterized host handle (tag passed in) sibling to the per-mob hardcoded pig handle — pig handle left untouched"
    - "DATA_WOOL byte data-value mirroring babyDataEntry (index 18, BYTE serializer 0) — host derives the wire byte from a bool"
    - "shearing loot drop reusing dropMobLoot's LoadTable/Roll path with an event-time off-mob-stream seed + a 5-nextFloat/stack mob-stream scatter"
    - "per-type aiStep branch in the tick snapshot loop (chicken-gated), additive — no new generic customServerAiStep seam in ai_mob.go (pig oracle untouched)"
key-files:
  created:
    - "server/sheep_eat.go"
    - "server/chicken_aistep.go"
    - "server/ai_goals_eat.go"
    - "server/mob_extras_test.go"
  modified:
    - "server/plugin_entity.go"
    - "server/entity.go"
    - "server/entity_encode.go"
    - "server/attack_dispatch.go"
    - "server/plugin_mob_decl.go"
    - "server/tick_phases.go"
decisions:
  - "Used the Entity wire-type field e.typ == entity.Sheep.ID / entity.Chicken.ID for the mob gates (the plan's mob.baseType is not an Entity field — baseType lives on mobDecl; Entity carries typ). Faithful equivalent, additive + mob-gated so the pig path is a zero-cost skip."
  - "EDIBLE_FOR_SHEEP tall-grass/fern branch CITE-DEFERRED (block tag not extracted); eatGrassBlock ports ONLY the grass_block-below eat (the common case). Structure preserved for a later tag-extractor extension."
  - "v1 ships WHITE sheep ONLY: getColor always WHITE, shear table = shearing/sheep/white, DATA_WOOL low nibble always 0 (dye deferred). The shear ACTION + the eat-regrow together satisfy MOB-PASS-02."
  - "chicken_lay GIFT table is chicken/variant-gated (predicate not ported) -> dropChickenEgg drops a plain minecraft:egg DIRECTLY (TEMPERATE default); the egg-lay RNG (2 nextFloat + nextInt(6000)) is unchanged (JARNOTES:223-239)."
  - "itemStack.hurtAndBreak(1) realized as a held-item shrink-by-1 (no durability subsystem in v1) — documented in trySheepShear."
  - "adjustedTickDelay stays IDENTITY (no ceilDiv) — re-verified: our driver runs serverAiStep every tick (no (tickCount+id)%2 decimation), so the FULL bound is the faithful compensation."
  - "level.levelEvent(2001) block-break particle cite-deferred (no levelEvent seam; pure client cosmetic, no gameplay/RNG effect)."
metrics:
  duration: ~13min
  completed: 2026-06-30
---

# Phase 34 Plan 00: New-Passive-Mobs Shared Infrastructure Summary

The wave-1 shared-Go-file edits all three new passive mobs (cow/sheep/chicken) need, serialized into
one plan so the wave-2 mob plans stay disjoint and parallel: a parameterized `nearest_player_holding_food`
tempt handle, the sheep eat seam (`EatBlockGoal.tick` + `Sheep.ate` 1:1, with DATA_WOOL `setSheared`),
the sheep SHEAR interact (`Sheep.mobInteract`/`Sheep.shear` 1:1 — shears → white-wool drop + 5-nextFloat/stack
scatter + `setSheared(true)`), the DATA_WOOL encode (index 18, BYTE serializer 0), and the chicken
`aiStep` hook (slow-fall `vy *= 0.6` + egg-lay 2-nextFloat-then-nextInt(6000)) — all wired and tested
headless. The pig oracle (`TestPluginPigEqualsGoNativePig`) stays byte-identical.

## What shipped

**Task 1 — food handle + sheep eat seam + DATA_WOOL + shear** (`d01eac79`)
- `plugin_entity.go`: `nearest_player_holding_food(tag, range)` (parameterized sibling of the pig's
  hardcoded handle, which is left untouched) + `eat_grass_block` (capWorldWrite) + `eat_broadcast_byte10`
  (capEntitiesWrite) handles.
- `sheep_eat.go`: `eatGrassBlock` (grass_block-below → dirt) → `sheepAte` (`setSheared(false)` wool regrow
  + `canAgeUp() → ageUp(60)`); `setSheared` (DATA_WOOL bit 0x10 toggle + ClientboundSetEntityData
  broadcast); `trySheepShear` (server-side shears read; `readyForShearing` gate; CONSUME-vs-fall-through);
  `shearSheep` (SHEEP_SHEAR sound 1441 on SoundSource.PLAYERS + shearing/sheep/white loot drop +
  5-nextFloat/stack mob-stream scatter + `setSheared(true)` after the drop + held-item shrink).
- `entity_encode.go`: `dataWoolIndex = 18` + `woolDataEntry(woolByte)` (reuses the existing
  `byteSerializerID == 0`) + `soundSourcePlayers = 7`.
- `entity.go`: `sheared bool` + `eggTime int` fields + `canAgeUp()` method.
- `attack_dispatch.go`: `trySheepShear` wired into `handleInteract` BEFORE `tryFeedAnimal`,
  `entity.Sheep.ID`-gated (additive — the cow's `tryMilkCow` slots into the same spot in 34-01).
- `ai_goals_eat.go`: the shared EatBlockGoal constants home (identity `adjustedTickDelay`, no ceilDiv) +
  the full bytecode citation; the goal is plugin-expressed, so this is constants + docs (no Go goal type).

**Task 2 — chicken aiStep hook + spawn init + tick wiring** (`e24586c9`)
- `chicken_aistep.go`: `chickenAiStep` (slow-fall `vy = vy * 0.6` when `!onGround && vy<0`, no RNG;
  egg-lay: baby + `chickenIsChickenJockey` named-const gated; `--eggTime <= 0` → `dropChickenEgg` →
  2-nextFloat pitch sound (only if dropped) → `nextInt(6000)+6000` reset, in jar order) + `dropChickenEgg`
  (plain minecraft:egg, TEMPERATE cite-defer).
- `plugin_mob_decl.go`: chicken `eggTime = nextInt(6000)+6000` spawn init after `reseedMobAI`
  (`entity.Chicken.ID`-gated — the chicken's first mob-stream draw).
- `tick_phases.go`: `chickenAiStep` called in the serverAiStep snapshot loop, `entity.Chicken.ID`-gated +
  additive (runs in tickAI before tickPhysics; the pig and every non-chicken mob is a zero-cost skip).

## RNG fidelity (jar-faithful, draw-order pinned)
- Sheep shear scatter: exactly 5 `mobRandom(e).nextFloat()` per dropped stack — x: `(nF - nF)*0.1`,
  y: `nF*0.05`, z: `(nF - nF)*0.1` (the loot seed is event-time `rand.Int64()`, OFF the mob stream).
- Chicken egg-lay: 2 `nextFloat` (pitch, only if dropped) THEN `nextInt(6000)` (reset), in jar order;
  `eggTime > 0` draws ZERO; spawn init draws `nextInt(6000)` first.
- Sheep eat gate (`nextInt(adjustedTickDelay(50/1000))`) is plugin-drawn (the .star); `adjustedTickDelay`
  stays identity (verified by the existing untouched tests).
- Pig oracle: ZERO new draws — every edit to a pig-touching file (attack_dispatch, entity_encode,
  entity, plugin_mob_decl, tick_phases) is additive + mob-gated; the pig path no-ops through all of them.

## Verification
- `CGO_ENABLED=0 go build ./...` → exit 0.
- `CGO_ENABLED=0 go vet ./server/` → clean.
- `CGO_ENABLED=0 go test ./server/` → ok (full suite, 7.99s).
- New tests (13) all PASS: TestNearestPlayerHoldingFood, TestEatGrassBelowToDirt, TestEatGrassBabyAgesUp,
  TestEatNoGrassNoOp, TestSetShearedBroadcastsWool, TestWoolDataEntryLayout, TestShearReadyAdultDropsWool,
  TestShearBabyOrShearedNoOp, TestShearNonShearsFallsThrough, TestSlowFall, TestEggLayRNG,
  TestEggLayBabySkips, TestChickenEggTimeInit.
- **Pig oracle byte-identical**: `TestPluginPigEqualsGoNativePig` PASS (+ all TestVanillaPig*/TestPluginPig*).
- ceilDiv guard: 0 occurrences in sheep_eat.go / ai_goals_eat.go / chicken_aistep.go.

## Deviations from Plan

### Auto-fixed / API-reconciled (Rule 3 — blocking field/API mismatches)

1. **[Rule 3 - API] `mob.baseType` → `mob.typ == entity.Sheep.ID` / `entity.Chicken.ID`.** The plan
   gated on `mob.baseType`, but `baseType` is a `mobDecl` field, not an `Entity` field — `Entity` carries
   the wire type as `e.typ` (an `entity.ID`). Used `e.typ == entity.Sheep.ID` (and `.Chicken.ID`), the
   faithful equivalent. Additive + mob-gated, so the pig path is a zero-cost skip. Files: attack_dispatch.go,
   tick_phases.go, plugin_mob_decl.go.

2. **[Rule 3 - API] `shrinkHeldItem(p, 1)` → `shrinkHeldItem(p, inv)`.** The actual signature is
   `shrinkHeldItem(p *tickPlayer, inv *Inventory)` (shrinks the selected held slot by 1), not a count
   parameter. Used the real API in trySheepShear. File: sheep_eat.go.

3. **[Rule 3 - Blocking] `byteSerializerID` already existed.** entity_encode.go already declared
   `const byteSerializerID int32 = 0` (for DATA_LIVING_ENTITY_FLAGS). Removed my duplicate declaration
   and reuse the existing one in `woolDataEntry`. File: entity_encode.go.

4. **[Rule 2 - Missing helper] Added `Entity.canAgeUp()` + `soundSourcePlayers` const.** The plan
   referenced `e.canAgeUp()` (didn't exist) and `soundSourcePlayers` (didn't exist). Added `canAgeUp()` ==
   `isBaby()` (the v1 `!isAgeLocked()` stub, matching the tryFeedAnimal comment) and `soundSourcePlayers`
   == 7 (SoundSource.PLAYERS.ordinal()). Files: entity.go, entity_encode.go.

### Cited deferrals (as the plan locked them)
- Tall-grass/fern EatBlockGoal branch (EDIBLE_FOR_SHEEP block tag not extracted) → grass_block-below only.
- WHITE sheep only (shear table shearing/sheep/white; dye deferred).
- Chicken egg loot drops plain minecraft:egg directly (variant-gated gift table deferred; egg-lay RNG unchanged).
- `level.levelEvent(2001)` block-break particle (no levelEvent seam; client cosmetic, no gameplay/RNG effect).
- `hurtAndBreak(1)` shears durability → held-item shrink-by-1 (no durability subsystem in v1).

### Race detector
The `-race` gate could not be exercised in this environment (no cgo/gcc on PATH). Per
34-JARNOTES.md:315, the `-race` run is reserved for the Docker path in 34-04; the non-race full server
suite is green and all new entity mutations follow the established tick-owned / `regionForEntity`-routed
discipline (no new cross-goroutine sharing).

## Note (acceptance-criterion artifact, not a gap)
The plan criterion `grep -c 'mobRandom(e).nextFloat()' server/sheep_eat.go == 5` returns 3 because the 5
jar-faithful scatter draws are packed onto 3 lines (x: 2, y: 1, z: 2). `grep -o` confirms exactly 5
occurrences; the 5-draw count + order is pinned by `TestShearReadyAdultDropsWool`. The mismatch is a
line-vs-occurrence counting artifact, not a fidelity difference.

## Self-Check: PASSED
- server/sheep_eat.go, server/chicken_aistep.go, server/ai_goals_eat.go, server/mob_extras_test.go: FOUND
- server/plugin_entity.go, server/entity.go, server/entity_encode.go, server/attack_dispatch.go,
  server/plugin_mob_decl.go, server/tick_phases.go: FOUND (modified)
- Commit d01eac79 (Task 1): FOUND
- Commit e24586c9 (Task 2): FOUND
