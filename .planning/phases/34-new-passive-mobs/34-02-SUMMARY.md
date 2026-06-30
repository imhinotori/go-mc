---
phase: 34-new-passive-mobs
plan: 02
subsystem: mob-passive-sheep
tags: [mob, passive, sheep, jar-port, eat-block, shear, data-wool, mob-pass, starlark-plugin, lockstep-rng]
requires:
  - "34-00 host eat seam: entity.eat_grass_block / entity.eat_broadcast_byte10 (capWorldWrite / capEntitiesWrite)"
  - "34-00 entity.nearest_player_holding_food(tag,range) parameterized tempt handle"
  - "34-00 TickLoop.trySheepShear/shearSheep/setSheared/sheepAte/eatGrassBlock (sheep_eat.go) + DATA_WOOL (entity_encode.go)"
  - "34-00 ai_goals_eat.go shared EatBlockGoal constants (eatAnimationTicks/eatActTick/eatGateBoundAdult/eatGateBoundBaby)"
  - "plugin_mob_decl.go declare_mob/goal builtins + spawnDeclaredMob; plugin_mob_ai.go starlarkGoal/buildAIFromDecl"
  - "ai_random.go entityRandom (mobRandom per-entity seeded stream) + reseedMobAI"
provides:
  - "plugins/vanilla_sheep + server/assets/vanilla_sheep (byte-identical pair): the 9-goal Sheep.registerGoals declaration with EatBlockGoal@5"
  - "vanilla_sheep mob: entity.Sheep.ID, max_health 8.0, movement_speed 0.23, single sheep_food TemptGoal@3"
  - "the plugin-expressed EatBlockGoal (RNG gate via entity.rand_int, eat via the 34-00 host seam)"
  - "server/sheep_test.go: boot-load + EatBlockGoal RNG-gate + eat-grass + shear-through-plugin + behavior tests"
affects:
  - "34-04 (gate): the sheep behavior + RNG tests join the new-mob gate; the -race Docker run covers them"
  - "future natural-spawn wiring: categoryOf must map entity.Sheep.ID -> CREATURE (deferred to the spawner plan, NOT here)"
tech-stack:
  added: []
  patterns:
    - "RNG-gated plugin goal: the EatBlockGoal nextInt gate drawn PLUGIN-side via entity.rand_int (mobRandom, lockstep), the eat mutation routed HOST-side (eat_grass_block) — like the pig's stroll/panic gate + the breed's host try_breed"
    - "byte-identical embed pair (plugins/ + server/assets/), verified by identical git blob hashes (not just an on-disk diff)"
    - "EatBlockGoal RNG-gate test: a mirrorMobStream replay pins exactly ONE nextInt per canUse (no leaked draws) + the 1000<->50 baby bound flip (matched-sweep, not a single coincidence-prone draw)"
key-files:
  created:
    - "plugins/vanilla_sheep/main.star"
    - "plugins/vanilla_sheep/plugin.toml"
    - "server/assets/vanilla_sheep/main.star"
    - "server/assets/vanilla_sheep/plugin.toml"
    - "server/sheep_test.go"
  modified: []
decisions:
  - "The sheep test loads the .star through the plugin_mob_test.go LoadDirWith seam (host.New() + declare_mob/goal builtins into a fresh registry), materializing the on-disk plugins/vanilla_sheep into an isolated temp root — NO new Go loader/spawn generalization (that would be a shared Go edit, out of this file-disjoint wave-2 plan; the embed boot-load loader stays pig-specific until a spawner plan generalizes it)."
  - "TestSheepBehavior asserts entity.Sheep.ID + a live AI + the sheep_food tempt scan, NOT categoryOf->CREATURE: categoryOf (mob_category.go) is a SHARED Go file that today maps only the pig; extending it to sheep is a natural-spawn/spawner concern, not the MOB-PASS-02 plugin-declaration plan. Editing it here would be a forbidden shared wave-2 edit."
  - "The EatBlockGoal RNG-gate bound-flip cross-check uses a matched-sweep over 4000 calls (the baby nextInt(50) mirror matches ALL calls; the nextInt(1000) mirror DIVERGES) instead of a single-draw mismatch: math/rand/v2 IntN consumes the same PCG word regardless of bound, so a single draw can coincidentally agree — only the ==0 comparison diverges across a multi-draw sweep (89/5000 divergence empirically, baby fires ~14x more often)."
  - "EatBlockGoal expressed as plugin callbacks (eat_can_use/eat_start/eat_tick/eat_stop/eat_continue) with the eatAnimationTick timer in the per-goal set_state/get_state scratch — NO Go-native eatBlockGoal goalSelector type (the 34-00 ai_goals_eat.go is constants + citation only, the planned lean)."
  - "adjustedTickDelay IDENTITY (1000/50/40/4, no ceilDiv) carried into the .star literals, matching the 34-00 re-verified analysis (our every-tick driver needs the FULL bound)."
  - "Cited deferrals inherited from 34-00 (unchanged here): EDIBLE_FOR_SHEEP tall-grass/fern branch -> grass_block-below only; v1 WHITE sheep only (shear table shearing/sheep/white, DATA_WOOL low nibble 0)."
metrics:
  duration: ~22min
  completed: 2026-06-30
---

# Phase 34 Plan 02: New Passive Mobs — Sheep Summary

The vanilla Sheep as a jar-faithful Starlark plugin (MOB-PASS-02): the 9-goal
`net.minecraft.world.entity.animal.sheep.Sheep.registerGoals` set @0..@8 including the NEW
`EatBlockGoal@5` (RNG-gated grass eating + wool regrow), declared as a byte-identical embed pair, plus
the focused sheep tests — the EatBlockGoal nextInt lockstep gate, the eat-through-the-plugin grass→dirt +
wool regrow, and the shear-through-the-plugin (white wool + setSheared, then eat regrows) that closes the
MOB-PASS-02 shear/wool clause end-to-end on a real plugin sheep. NO shared Go file touched — the eat/shear
host seam + DATA_WOOL live in 34-00; the pig oracle stays byte-identical.

## What shipped

**Task 1 — vanilla_sheep plugin pair + sheep tests** (`f8b2a96b`)

- `plugins/vanilla_sheep/main.star` + `server/assets/vanilla_sheep/main.star` (byte-identical, same git
  blob hash `caaba833`): copied from the pig template, the carrot Tempt block + its @4 goal DELETED, the
  remaining tempt reparameterized to `entity.nearest_player_holding_food("sheep_food", TEMPT_RANGE)`, and
  the NEW `EatBlockGoal@5` added (eat_can_use/eat_start/eat_tick/eat_stop/eat_continue). The 9 goals at the
  jar `Sheep.registerGoals` indices: @0 FloatGoal[JUMP], @1 PanicGoal[MOVE], @2 BreedGoal[MOVE,LOOK],
  @3 TemptGoal[MOVE,LOOK] (sheep_food), @4 FollowParentGoal[] , @5 EatBlockGoal[MOVE,LOOK,JUMP],
  @6 WaterAvoidingRandomStroll[MOVE], @7 LookAtPlayer[LOOK], @8 RandomLookAround[MOVE,LOOK]. Attributes
  max_health 8.0 / movement_speed 0.23 (Sheep.createAttributes).
- `plugins/vanilla_sheep/plugin.toml` + `server/assets/vanilla_sheep/plugin.toml` (byte-identical, blob
  `fc67bf4f`): the pig caps + `world.write` for the `eat_grass_block` host seam (the .star passes no
  position, so the host seam can only touch the mob's own below-position — T-34-04/T-34-05, cited in the toml).
- `server/sheep_test.go`: 5 tests, all green — TestSheepBootLoads, TestEatBlockGoalRNGGate, TestSheepBehavior,
  TestSheepEatRegrowsWool, TestSheepShearThroughPlugin.

## EatBlockGoal RNG fidelity (jar-faithful, lockstep-pinned)

- The canUse gate draws EXACTLY ONE `nextInt(adjustedTickDelay(isBaby?50:1000))` per call via
  `entity.rand_int` (mobRandom — the per-entity seeded stream). adjustedTickDelay IDENTITY (1000/50, no
  ceilDiv) per the 34-00 re-verified analysis.
- `TestEatBlockGoalRNGGate` pins it via a `mirrorMobStream` replay: (a) after N canUse calls the sheep's
  stream is at EXACTLY the mirror's nextInt(1000)-advanced position (one draw per call, no leaks); (b)
  canUse returns True iff the seeded `nextInt(gate)==0`; (c) the bound flips 1000→50 for a baby — proven
  by a matched-sweep over 4000 calls (the nextInt(50) mirror matches ALL calls, the nextInt(1000) mirror
  diverges, and the baby fires True far more often).
- start arms eatAnimationTick=40 + broadcasts byte 10; tick acts at ==4 → `entity.eat_grass_block` (the
  34-00 host seam: grass_block-below → dirt + Sheep.ate wool regrow + baby ageUp).

## MOB-PASS-02 shear + regrow (end-to-end on a plugin sheep)

`TestSheepShearThroughPlugin` shears a plugin-spawned `vanilla_sheep` through the 34-00 `trySheepShear`
host interact (held SHEARS) → ≥1 white wool item drops + `setSheared(true)`; then runs the EatBlockGoal
eat to the act tick on a grass_block → `setSheared(false)` (the wool regrows). A baby plugin sheep with
shears falls through (no drop). The shear ACTION + the wool REGROW together close the MOB-PASS-02
shear/wool-with-regrow clause on a REAL plugin sheep (not just the 34-00 synthetic-entity unit test).

## Verification

- `CGO_ENABLED=0 go build ./...` → exit 0.
- `CGO_ENABLED=0 go vet ./server/` → clean (verified with the sibling 34-03 chicken_test.go WIP set aside;
  see Deferred Issues — that untracked file is broken and out of this plan's scope).
- `diff plugins/vanilla_sheep/main.star server/assets/vanilla_sheep/main.star` → empty; toml diff → empty;
  committed git blob hashes IDENTICAL for both pairs.
- Sheep tests: TestSheepBootLoads, TestEatBlockGoalRNGGate, TestSheepBehavior, TestSheepEatRegrowsWool,
  TestSheepShearThroughPlugin → all PASS.
- **Pig oracle byte-identical**: TestPluginPigEqualsGoNativePig PASS (+ all TestVanillaPig*/TestPluginPig*).
- 34-00 seam tests still green: TestEatGrass*, TestShear*, TestSetSheared*, TestWoolDataEntryLayout,
  TestNearestPlayerHoldingFood.
- Full `CGO_ENABLED=0 go test ./server/` → ok (8.3s, with the sibling chicken WIP set aside).
- All 11 acceptance-criterion greps pass (eat_can_use=1, eat handles match, sheep_food tempt, base_type
  sheep, attrs 8.0/0.23, world.write cap, 9 priorities, the shear test func).

## Deviations from Plan

### None to the plan's intent — two cited adaptations (Rule 3, blocking API/test-harness reconciliation)

1. **[Rule 3 - Test harness] No `spawnVanillaSheep`/generalized loader.** The plan's `read_first` referenced
   a `spawnVanillaMob` generalization (from 34-PATTERNS speculation), but 34-00 did NOT generalize the embed
   boot-load loader (`vanilla_pig_embed.go` stays pig-specific) — generalizing it is a shared Go edit, out of
   this file-disjoint wave-2 plan. The sheep test instead loads the `.star` through the existing
   `plugin_mob_test.go` LoadDirWith seam (host.New() + declare_mob/goal builtins → a fresh registry),
   materializing the on-disk `plugins/vanilla_sheep` into an isolated temp root and installing it via the
   existing `SetMobRegistry`. No production Go change.

2. **[Rule 3 - Test assertion] TestSheepBehavior drops the categoryOf->CREATURE assert.** `categoryOf`
   (mob_category.go) is a SHARED Go file mapping only the pig today; extending it to sheep is a natural-spawn
   concern owned by a spawner plan, not MOB-PASS-02. Editing it here is a forbidden shared wave-2 edit. The
   behavior test asserts entity.Sheep.ID + a live AI + the sheep_food tempt scan instead (the CREATURE intent
   is already carried by base_type sheep + the jar attrs).

### Cited deferrals (inherited from 34-00, unchanged)
- EDIBLE_FOR_SHEEP tall-grass/fern branch → grass_block-below only (block tag not extracted).
- v1 WHITE sheep only (shear table shearing/sheep/white; DATA_WOOL low nibble 0; dye deferred).

## Deferred Issues (out of scope — NOT my files)

- `server/chicken_test.go` is an UNTRACKED (`??`) WIP from the sibling wave-2 chicken plan (34-03). It does
  not compile (`box.Upper.X undefined`, `e.item undefined`) and blocks the `server` test binary + `go vet
  ./server/`. It is NOT part of this plan and I did not touch it (restored exactly as found, still `??`). My
  own code builds + vets + tests clean when it is set aside. 34-03 owns fixing it. Logged here for the gate
  plan (34-04) — the new-mob test binary won't compile until the chicken WIP lands or is removed.

## Self-Check: PASSED
- plugins/vanilla_sheep/main.star, plugins/vanilla_sheep/plugin.toml: FOUND
- server/assets/vanilla_sheep/main.star, server/assets/vanilla_sheep/plugin.toml: FOUND
- server/sheep_test.go: FOUND
- Commit f8b2a96b: FOUND (5 files, +1461, no deletions; both embed pairs byte-identical in HEAD)
