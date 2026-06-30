# Phase 34: New Passive Mobs (cow / sheep / chicken) - Context

**Gathered:** 2026-06-30
**Status:** Ready for planning
**Mode:** Auto-generated (user AFK; all bytecode jar-verified in 34-JARNOTES.md)

<domain>
## Phase Boundary

Cow, sheep, and chicken exist as jar-faithful Starlark plugins reusing the now-proven 8-goal subsystem
set (Float/Panic/Breed/Tempt/FollowParent/Stroll/Look/LookAround — built in Phases 30.1–33, the dogfood
gate). The SHARED goals are cheap (declare_mob plugin declarations); the PER-MOB extras are the real work:
- **Cow**: milking (right-click empty bucket → milk_bucket). NO new goal. (AbstractCow goal set.)
- **Sheep**: EatBlockGoal@5 (eat grass, RNG-gated) + shear/wool (DATA_WOOL + setSheared + wool regrow).
- **Chicken**: aiStep egg-lay (eggTime → drop egg) + slow-fall (deltaMovement.y *= 0.6 falling) + flap.

The pig oracle stays GREEN (the pig is UNTOUCHED). Each new mob gets a per-mob spawn+behavior test (the
pig oracle is THE bit-exact one; the new mobs reuse proven goals, so a lighter per-mob test suffices —
EXCEPT any NEW RNG goal the mob declares as a plugin must keep its own draw-order discipline).

OUT OF SCOPE: MushroomCow; sheep color-dye mechanics beyond the spawn color; hostiles/wolf (35/36).
</domain>

<decisions>
## Implementation Decisions (jar-verified — see 34-JARNOTES.md for the verbatim bytecode)

### The shared goal sets (Cow/Sheep/Chicken registerGoals — all confirmed):
| prio | Cow | Sheep | Chicken |
|------|-----|-------|---------|
| 0 | FloatGoal | FloatGoal | FloatGoal |
| 1 | PanicGoal(2.0) | PanicGoal(1.25) | PanicGoal(1.4) |
| 2 | BreedGoal(1.0) | BreedGoal(1.0) | BreedGoal(1.0) |
| 3 | TemptGoal(1.25, cow_food) | TemptGoal(1.1, sheep_food) | TemptGoal(1.0, chicken_food) |
| 4 | FollowParentGoal(1.25) | FollowParentGoal(1.1) | FollowParentGoal(1.1) |
| 5 | Stroll(1.0) | **EatBlockGoal** | Stroll(1.0) |
| 6 | LookAtPlayer(6) | Stroll(1.0) | LookAtPlayer(6) |
| 7 | LookAround | LookAtPlayer(6) | LookAround |
| 8 | — | LookAround | — |
isFood: cow=is(cow_food), sheep=is(sheep_food), chicken=is(chicken_food) — ALL THREE TAGS PRESENT in
data/tag/tags.go. The food predicate = itemInTag(id, "<x>_food") (Phase 32, parameterized).

### Attributes (createAttributes): cow MH 10.0 speed 0.2; sheep MH 8.0 speed 0.23; chicken MH 4.0 speed 0.25.
### Wire types Cow/Sheep/Chicken present in data/entity/entity.go. categoryOf → CREATURE (like pig).
### Hitboxes (SC#3 — per-mob sizing): each mob's adult dims from the data/entity table (Width/Height);
### the FloatGoal/nav must read the RIGHT dims (the pig's were 0.9×0.9; cow/sheep differ, chicken ~0.4×0.7).
### The baby-scale (×0.5) reuses Phase-33's babyDimensionScale (derived, not hardcoded).

### THE PLUGIN DECLARATION PATH (how the mobs register — reuse vanilla_pig's):
- The mob registry is `byName map[string]*mobDecl` (plugin_mob_decl.go:81) — ALREADY multi-mob.
- vanilla_pig is `//go:embed`'d + boot-loaded via LoadVanillaPigRegistry (vanilla_pig.go) → SetMobRegistry.
- Phase 34: add `vanilla_cow`/`vanilla_sheep`/`vanilla_chicken` .star plugins (declare_mob with the goal
  list + food tag + attributes), embed them (server/assets/), and EXTEND the boot-load to load ALL FOUR
  into the one registry (the byName map holds all). Each declares its goals reusing the Go-native goal
  runtime (the SAME goal types the pig uses — FloatGoal/PanicGoal/etc. + the NEW EatBlockGoal for sheep).
- Spawn paths: `/dbg cow|sheep|chicken` (extend commands_dbg.go) + natural spawn (async.go currently
  hardcodes spawnVanillaPig — generalize to pick by mob name / category). The test-kit egg spawns pig;
  add per-mob spawn levers for the bot to verify.
- Like the pig, each new mob's .star copies must be byte-identical (repo-root + server/assets/embed).

### Per-mob extras (the real work):
- **Cow milking** (Cow.mobInteract): right-click with an empty bucket → consume bucket, give milk_bucket,
  COW_MILK sound. NO RNG. Wire into handleInteract (the FEED path's sibling — check held item is a bucket).
  Decompile Cow.mobInteract at exec for the exact bucket→milk swap.
- **Sheep EatBlockGoal** (34-JARNOTES): {MOVE,LOOK,JUMP}, canUse draws nextInt(adjustedTickDelay(baby?50:1000))
  — THE RNG GATE (lockstep if the sheep is a plugin drawing it). Eats tall-grass-at-mob OR grass_block-below.
  EDIBLE_FOR_SHEEP is a BLOCK tag NOT extracted (only damage-type + item tags are in data/tag) → EITHER
  extend the tag extractor for block tags, OR cite-defer the tall-grass branch and check GRASS_BLOCK below
  directly (the common case; document the deferral). Shear/wool: DATA_WOOL byte + setSheared + regrow-on-eat.
- **Chicken aiStep** (34-JARNOTES): slow-fall (deltaMovement.y *= 0.6 when falling, !onGround) — a per-mob
  physics override (the chicken's tick hook); egg-lay (--eggTime<=0 → drop egg via loot + CHICKEN_EGG sound
  (2 nextFloat pitch) + reset eggTime=nextInt(6000)+6000). eggTime inits nextInt(6000)+6000 at spawn. RNG:
  nextInt(6000) + 2 nextFloat (lockstep if the chicken is a plugin drawing it). Egg item drop via loot table.

### RNG / oracle discipline:
The PIG oracle is untouched (the pig keeps its 9 goals; cow/sheep/chicken are SEPARATE mobs) → it stays
byte-identical trivially. The NEW RNG goals (sheep EatBlockGoal nextInt gate; chicken egg nextInt+nextFloat)
are on the new mobs only. IF a new mob is dogfooded as a plugin (Go-native oracle vs plugin) like the pig,
its EatBlockGoal/egg draws must be lockstep on BOTH halves. DECIDE per mob: does cow/sheep/chicken get a
full byte-identical Go-vs-plugin oracle (like the pig), or a lighter per-mob spawn+behavior test? The pig
PROVED the subsystems; the new mobs reuse them, so a per-mob behavior test (spawns, walks, panics, tempts,
breeds, + the mob's unique extra) is likely sufficient — but any NEW RNG path needs deterministic coverage.
The planner decides; lean toward a per-mob behavior test + a focused EatBlockGoal/egg-lay RNG test.
</decisions>

<code_context>
## Existing surfaces (reuse)
- `server/plugin_mob_decl.go` — the mobRegistry (byName, multi-mob) + declare_mob/goal builtins. The new
  mobs declare here. spawnDeclaredMob (the generic spawn) + the spawn-time baby-metadata carry.
- `server/vanilla_pig.go` — LoadVanillaPigRegistry / SetMobRegistry / spawnVanillaPig — the boot-load +
  spawn pattern to GENERALIZE (LoadVanillaMobRegistry loading all 4 embeds; spawnVanillaMob(name,...)).
- `plugins/vanilla_pig/main.star` + `server/assets/vanilla_pig/main.star` — the plugin pattern to copy
  for vanilla_cow/sheep/chicken (declare_mob + the shared goal callbacks reusing the host handles).
- `cmd/sulfur/main.go:347` — the boot-load (extend to load all 4 mob plugins).
- `server/commands_dbg.go` — `/dbg pig`; add `/dbg cow|sheep|chicken`.
- `server/async.go:336` — natural spawn (generalize from spawnVanillaPig to a category/name pick).
- `server/attack_dispatch.go handleInteract` — the FEED path; add cow milking (bucket→milk) sibling.
- The goal types: ai_goals_float/panic/passive (Float/Panic/Tempt/Stroll/Look) + ai_goals_breed/follow
  (Breed/Follow) — all REUSED. NEW: an EatBlockGoal type (server/ai_goals_eat.go or similar).
- Phase-33 babyDimensionScale (0.5) + refreshDimensions — reused for each mob's baby.
- data/tag itemInTag (cow_food/sheep_food/chicken_food). data/item bucket/milk_bucket/egg.
- The chicken slow-fall: the per-mob physics hook (the mob's serverAiStep / a customAiStep seam).
- Loot: the egg drop (chicken) — the v3 loot evaluator (level/loot) + a CHICKEN_LAY-style drop; or a
  direct egg spawnAtLocation. Check the loot surface.

## Verification
- `CGO_ENABLED=0 go build ./...` + `go vet ./server/` (gopls STALE — trust the compiler).
- `CGO_ENABLED=0 go test ./server/ -run 'TestCow|TestSheep|TestChicken|TestEatBlock|TestMilk|TestEggLay|TestSlowFall|TestPluginPigEqualsGoNativePig|TestVanillaPig|TestPigGoalSet' -v`.
- Per-mob behavior tests: each spawns + walks + panics + tempts + breeds (reusing the proven goals) + its
  extra (cow milk, sheep eat-grass, chicken egg+slow-fall). The PIG oracle stays byte-identical.
- Docker -race + strictRegion clean.
- LIVE bot: `/dbg cow|sheep|chicken` → each appears + wanders + (cow) milk on bucket-interact, (chicken)
  spawns + slow-falls, (sheep) eats grass. The bot's Interact/UseItem already work (Phase 32/33).
- javap each mob's registerGoals + mobInteract + aiStep BEFORE writing (the 1:1 mandate).

## Multi-plan split (planner decides via source-audit):
Likely: Plan A cow (decl + milk + spawn + test), Plan B sheep (decl + EatBlockGoal + shear/wool + test),
Plan C chicken (decl + egg-lay + slow-fall + test), Plan D the gate (all 3 spawn/behave + the boot-load
generalization + natural-spawn pick + -race + live bot). OR group the decls + generalize-boot-load in one
plan and the per-mob extras in another. The pig oracle stays green throughout.
</code_context>

<specifics>
## Specific Ideas
- 3 mobs as plugins reusing the 8-goal set; per-mob extras (cow milk, sheep EatBlock+wool, chicken egg+float).
- Generalize LoadVanillaPigRegistry → load all 4 embeds; spawnVanillaPig → spawnVanillaMob(name).
- Generalize natural spawn + `/dbg <mob>` + the bot spawn levers.
- EDIBLE_FOR_SHEEP block tag: extend extractor OR cite-defer to grass_block-below.
- Per-mob behavior tests (lighter than the pig oracle) + focused RNG tests for EatBlockGoal + chicken egg.
- The PIG ORACLE must stay byte-identical (the pig is untouched).
</specifics>

<deferred>
## Deferred Ideas
- MushroomCow; sheep dye-color mechanics; the chicken jockey; full block-tag extraction (if EatBlockGoal
  uses the grass_block-below shortcut).
- Cross-region breeding (the Phase-33 same-region cut carries forward).
- A full byte-identical Go-vs-plugin oracle per new mob (vs a per-mob behavior test) — the planner decides;
  the pig oracle already proved the subsystems.
</deferred>
