# Pitfalls Research

**Domain:** Adding mob/living-entity subsystems + new mob types (passive/hostile/neutral) to Sulfur — a 1:1-jar-port, concurrency-first (Folia N=2), CGO=0 Minecraft Java 26.2 server in Go.
**Researched:** 2026-06-29
**Confidence:** HIGH (grounded in the actual codebase: ai_mob.go, ai_goal.go, combat.go, spawner.go, plugin_pig_test.go, region_transfer.go, ai_random.go, plugin_entity.go — plus the cited jar classes and prior-session post-mortems in deferred-goals.md / PROJECT.md Key Decisions)

This file is scoped to ADDING the v5 subsystems to THIS codebase. Generic Go advice is omitted; every pitfall names the concrete file/seam it bites and the phase that must guard it.

---

## Critical Pitfalls

### Pitfall 1: RNG draw-order divergence breaks the 500-tick pig oracle (THE single biggest risk)

**What goes wrong:**
`TestPluginPigEqualsGoNativePig` (server/plugin_pig_test.go:215) drives a Go-native `newPigAI` pig and a Starlark `vanilla_pig` pig from the SAME id-derived seed (`reseedMobAI(ai, 4242)`) and asserts a BYTE-IDENTICAL `pigObservation` (`hasTarget/wantX/wantY/wantZ/yaw/headYaw/x/y/z`) every tick for 500 ticks. Both pigs draw from the SAME per-entity seeded `*entityRandom` (ai_random.go) — a single shared PCG stream per mob. The moment you add a new goal (PanicGoal, TemptGoal, the hostile mobs' attack/target goals, or FloatGoal) that draws from `rand_int/rand_float/rand_double` at a different point in the tick than the Go oracle draws, the two streams desync. After the first divergent draw EVERY subsequent observation differs, and the test fails at "tick N" with a `+v` diff. Prior sessions burned multiple debug iterations on exactly this when touching the SHARED `serverAiStep`/navigation flow.

**Why it happens:**
The oracle's invariant is not "same behavior" — it is "same DRAW COUNT and same DRAW ORDER off one shared stream." Three subtle ways to perturb it:
1. **A new goal's `canUse` draws unconditionally** every tick (e.g. `rand_float()` before the cheap guards). Goals are ticked in priority order through `goalSelector.tick` (ai_goal.go:233) on BOTH paths; an extra draw on one path but not the other, or at a different priority slot, shifts the whole stream.
2. **The Go-native pig and the plugin pig add the new goal in DIFFERENT plans / at different fidelity** — so for a window of time the two pigs no longer have the identical goal set, and shared-stream equality can never hold.
3. **A draw moves from inside a running-goal callback to the always-run path** (or vice versa). The oracle relies on the interpreter firing ONLY while a goal is RUNNING (an idle pig makes zero draws). A draw added to `serverAiStep` itself, or to a `canUse` evaluated for a not-yet-running goal at a new priority, is an "always-on" draw the oracle did not budget for.

**How to avoid:**
- **Add the new RNG-drawing goal to the Go-native `newPigAI` AND the plugin `vanilla_pig/main.star` in the SAME plan, lockstep, ported from the same javap session.** Never ship the Go side in one plan and the Starlark side in another — the oracle is red the whole gap. (The cited prevention: "add new RNG only inside the goal's running callback, ported lockstep on both Go-native + plugin in the SAME plan.")
- **Confine every new draw to the goal's `can_use`/`start`/`tick` callbacks, never to the shared `serverAiStep` driver or `navigation.tick`.** The driver (ai_mob.go:105) and `navigation.tick` must stay draw-free so the only draws are goal-local and fire only while running.
- **Port the draw ORDER from the bytecode literally, with a `# DRAW N:` comment per draw** exactly as main.star already does (stroll: "DRAW 1 gate FIRST, then DRAWS 2,3,4 offsets"). PanicGoal: the water-search draws vs the random-flee-pos draws must match `PanicGoal.canUse`/`getFreePosition`. TemptGoal: the `nextFloat()` chance vs the player-scan order.
- **When the new goal is HIGHER priority than 6/7/8 (Panic@1, Float@0), it sits ABOVE stroll/look in the selector walk.** Its `canUse` is evaluated FIRST in `goalSelector.tick` pass 2 — so a draw there draws before stroll's gate, reordering the stream. Match it on both pigs by adding the identical goal at the identical priority; the selector walk order then matches automatically.
- **Keep the oracle GREEN as the gate for every goal-adding plan.** It is the canary: pass = identical draw order; fail = you reordered the stream.

**Warning signs:**
- The oracle fails at "tick N" where the FIRST differing field is `wantX`/`yaw` (a draw-driven field), not `x`/`y` (physics-driven) — fingerprints an RNG desync, not a movement bug.
- A new goal's `canUse` calls `rand_*` before its cheap precondition checks.
- A draw appears in `serverAiStep` or `navigation.tick` in a diff.
- The Go-native goal and the Starlark goal landed in different commits/plans.

**Phase to address:**
Every phase that adds an RNG-drawing goal — the **PanicGoal phase** (mob-damage subsystem), the **TemptGoal phase** (held-item subsystem), the **FloatGoal phase** (jump/fluid subsystem). The roadmap MUST make "the pig oracle stays green" an explicit exit criterion of each, and MUST forbid splitting the Go-native and Starlark halves of a goal across plans.

---

### Pitfall 2: Cross-region data races + wrong-region silent drops in the damage keystone and breeding

**What goes wrong:**
Folia regionization is LIVE at N=2 (`regionOf(col) = (col[0]^col[1])&1`, region_transfer.go:43). Two regions tick in PARALLEL goroutines. The damage keystone introduces the first cross-region WRITE hazard the codebase has not had: a player in region A attacks a mob in region B. A naive port that resolves the mob through `t.cur()` / `t.only()` from the ATTACKER's region goroutine will (a) read/mutate region B's entity store concurrently with region B's own tick goroutine — a data race `-race` catches in Docker — or worse (b) silently resolve to the WRONG region's store and miss the mob or hit a phantom. Writing `lastDamageSource`, firing the `on_damage` Emit, spawning a breeding baby, and searching for a breeding partner across the seam all share this shape. This is the exact `only()`-fallback bug class that already bit spawn/movement (PROJECT.md: "a goroutine that resolves the wrong region silently falls back to region 0, skipping regions 1..N-1").

**Why it happens:**
`cur()` falls back to `globalRegion` (region 0) when the calling goroutine has no region registered (tick.go:929). A damage path written as "resolve the target and subtract health" looks correct single-threaded but, called from the attacker's region fan-out goroutine, either resolves region 0 (wrong for a region-1 mob = silent miss) or touches region B's store concurrently (race). Breeding's partner search is worse: a naive `t.cur().entities.near()` only sees the SAME region's animals, so two mates straddling the seam never find each other (silent breeding failure), and a cross-region scan from a region goroutine races the neighbour.

**How to avoid:**
- **Route ALL cross-entity mutation through the OWNING entity's region, never the actor's `cur()`.** Use the established primitives: `regionForEntity(e)` (region_transfer.go:56) to find the target's region, `owningRegion(id)` (re-resolve by id, drops if gone), `forEachRegion`/`withRegion` for coordinator per-region work. A hit from region A on a mob in region B must be DEFERRED to the barrier (coordinator, quiescent) or routed to B's queue — never applied inline from A's goroutine.
- **Make `lastDamageSource` a tick-owned field on the mob `*Entity`, written ONLY on the goroutine that owns that mob.** The keystone mob damage-apply must run in the mob's region (or at the barrier), mirroring the player path's single-owner discipline (combat.go: "Runs on the tick goroutine over tick-owned state TICK-05"). The thin-id handle (PROJECT.md Key Decision: "id + re-resolves on the owner thread") is the existing primitive — reuse it for the damage target.
- **Breeding partner search uses the cross-region broad phase** (`entitiesNearAcrossRegions`, region_transfer.go:137 — the same one the spawn cap re-check uses), and the baby-spawn `entityStore.add` runs at the barrier through `regionForEntity(baby)` routing so the child lands in the column-correct region, not the parent's `cur()`.
- **Arm `strictRegion = true` in cross-region tests** (region_n2_regression_test.go pattern) so an unwrapped `cur()` from a region goroutine PANICS loudly instead of silently resolving region 0.
- **Run `-race` in Docker (CGO=1) on every new cross-region damage/breeding test** — the keystone is the first feature that genuinely exercises cross-region writes.

**Warning signs:**
- A damage or breeding path calls `t.cur()` / `t.only()` and is reachable from inside the region fan-out (not the coordinator barrier).
- `-race` (Docker) reports a read/write on an `entityStore.byID` map or an `*Entity` field from two goroutines.
- A mob on a region boundary takes no damage / never finds a mate / its baby spawns then vanishes (wrong-region add).
- A `strictRegion` test panics on the new path.

**Phase to address:**
The **mob damage pipeline phase (KEYSTONE)** must establish the cross-region damage-routing discipline (defer-to-barrier or route-to-owner). The **aging/breeding phase** must use the cross-region broad phase for partner search and `regionForEntity` for baby placement. Both need `-race` + `strictRegion` gates in the roadmap exit criteria.

---

### Pitfall 3: Combat-math paraphrase — the mob `actuallyHurt` port drops float casts / draw order / damage-type-tag branches

**What goes wrong:**
The player `actuallyHurt`/`applyDamage` (combat.go:188-321) is a meticulous 1:1 port: the `invulnerableTime > 10.0F` excess-only gate, the `amount <= lastHurt` no-op, the NaN/Infinity clamp to `Float.MAX_VALUE`, the armor curve (`f = 2.0 + armorToughness/4.0`, the `armor*0.2`/`20.0` clamp, `/25.0`), the absorption folding, and the `BYPASSES_ARMOR`/`BYPASSES_EFFECTS`/`BYPASSES_ENCHANTMENTS` constant-false guards left as structured seams. Porting this to MOBS invites paraphrase: writing `mob.health -= max(0, amount - armor)` "to keep it simple," dropping the i-frame excess gate, using `float64` where vanilla uses `float` (32-bit) casts, mis-ordering armor→magic→absorption, or — the v5-specific trap — STUBBING the damage-type tags (`BYPASSES_ARMOR`, the panic-causing damage types PanicGoal reads) as a bare bool instead of a real per-source `DamageType.is(tag)` read. That violates the 1:1 mandate AND breaks PanicGoal (which gates on `lastDamageSource.is(panicCausingDamageTypes.apply(mob))`, deferred-goals.md:65).

**Why it happens:**
Combat math "looks like arithmetic," so it invites simplification — but vanilla's observable damage values depend on exact float32 truncation, the clamp bounds, and the branch order. And because v1 wired no real DamageType table (combat.go: "v1 has no per-source DamageType wired here"), the temptation is to keep faking the tag checks as `const bypassesArmor = false` even when PanicGoal now genuinely NEEDS a real `damageSource.is(panicCausing)` read.

**How to avoid:**
- **Port `Mob.actuallyHurt`/`hurtServer`/`getDamageAfterArmorAbsorb` the SAME way combat.go did for the player** — reuse the verified helpers `combatRulesGetDamageAfterAbsorb`, `mthClampF`, `maxF`, `isNaN32/isInf32`. The mob path should DIFFER from the player path only where vanilla `LivingEntity` differs from `Player` (no `causeFoodExhaustion`; the `invulnerableTime` decrement is `LivingEntity.tick` for non-players, not the `ServerPlayer.tick` special case combat.go:105 mirrors).
- **Keep every numeric op `float32`**, every cast where vanilla casts, every clamp bound verbatim — `javap -c -p` the mob `actuallyHurt` BEFORE writing, as the constraint demands.
- **Build a REAL (minimal) DamageType/DamageSource carrying its tag set**, so `source.is(BYPASSES_ARMOR)` and `lastDamageSource.is(panicCausingDamageTypes)` are genuine reads, not `const false`. This is the KEYSTONE's actual deliverable — deferred-goals.md is explicit: "do NOT fake a hurt flag." The tag table is what makes PanicGoal faithful AND `BYPASSES_ARMOR` real for skeleton arrows / future sources.
- **Preserve the i-frame excess-gate for mobs** (`invulnerableTime`/`lastHurt`/`hurtDuration`/`hurtTime`) — hostile mobs hitting each other and players hitting mobs both depend on it.

**Warning signs:**
- A mob damage function uses `float64` for the armor/absorption math, or `math.Max` instead of the ported `maxF`.
- A `const bypassesArmor = false` survives into the mob path where a real damage-type read is required.
- The mob path lacks the `amount <= lastHurt` no-op or the `invulnerableTime > 10.0F` gate.
- PanicGoal's `shouldPanic` reads a bool flag instead of `lastDamageSource.is(panicCausing)`.
- No "verify against the jar" step in the plan.

**Phase to address:**
The **mob damage pipeline phase (KEYSTONE)** — port `Mob.actuallyHurt` 1:1 (reusing combat.go helpers), ship a real DamageType-tag table (so `BYPASSES_ARMOR` + panic-causing tags are real reads), javap-verify before writing. Exit criterion: a damage test asserting exact vanilla values across armor/absorption/i-frame/bypass-tag cases.

---

### Pitfall 4: Hostile spawn rules flood or mis-gate without a light engine

**What goes wrong:**
The spawner (spawner.go) ports a CREATURE-only subset with the load-bearing `MAGIC_NUMBER = 289` divisor (without it the cap was `maxInstancesPerChunk * spawnableChunkCount` ≈ 2890 creatures — "the world flooded with mobs," spawner.go:78). Adding HOSTILE mobs introduces a SECOND category (`MONSTER`, `maxInstancesPerChunk` = 70) plus day/night + light-level gating. The traps: (1) reusing `creatureCap` (hard-coded `categoryCreature`, spawner.go:88) for monsters → wrong cap, or forgetting to count MONSTER separately in `countByCategory` → monsters never gated / flood; (2) the spawner relaxed the light rule for v1 ("no light engine yet," spawner.go:48) — naively shipping hostile spawns with light still relaxed means monsters spawn in broad daylight on the surface (non-vanilla flood + grief); (3) the despawn rules (instant beyond 128 blocks, random 32–128, the persistence flag) are ENTIRELY unbuilt — without them monsters accumulate forever even under the spawn cap, because the cap gates SPAWNS, not POPULATION-over-time.

**Why it happens:**
The v1 spawner deliberately deferred multi-category density, light rules, and despawn (all "documented as deferred," spawner.go:312). Hostile mobs are the first consumer that NEEDS those pieces. Reusing the CREATURE-shaped helper for MONSTER is the path of least resistance and silently mis-caps. And "spawn cap" feels like "population cap" but isn't — without despawn, mobs that wander out of the cap's spawnable-column scan still exist and pile up.

**How to avoid:**
- **Generalize the cap to per-category**: replace the hard-coded `creatureCap`/`categoryCreature` with `categoryCap(cat, spawnableChunkCount)` using each category's real `maxInstancesPerChunk` (CREATURE 10, MONSTER 70), and tally EVERY category in `countByCategory` (it already does `categoryOf(e.typ)` — verify hostile types map to MONSTER). Keep the `/289` divisor for both.
- **Port the real light/sky check** (`Monster.isDarkEnoughToSpawn` / the blockLight ≤ threshold gate). This is BLOCKED on a light engine; if the engine isn't built, the roadmap must EITHER build a minimal light read first OR gate hostile spawns to a faithful proxy (sky-darkness by game-time + a documented relaxation) — flagged as a deviation, never silently spawning monsters in daylight.
- **Port the despawn rules** (`Mob.checkDespawn`: instant beyond `despawnDistance` 128², random roll 32²–128², `removeWhenFarAway`/persistence) — this is the actual population bound. Without it the spawn cap alone lets monsters accumulate.
- **Reuse the existing cross-region cap re-check** (`countByCategoryAcrossRegions`, `mobNearAcrossRegions`, spawner.go:113/131) for the new categories so N=2 doesn't double-spawn past the global cap.

**Warning signs:**
- `creatureCap` (singular) is called for a hostile spawn instead of a per-category cap.
- Hostile mobs visible on the surface at noon.
- Monster count climbs monotonically over a long run even though the spawn-cap log says "at cap" (despawn missing).
- A region-boundary monster spawns past the global MONSTER cap.
- `countByCategory` doesn't tally MONSTER.

**Phase to address:**
The **hostile-mobs phase** (zombie/skeleton/spider) — generalize the cap per-category, decide the light-gate (build a minimal light read or gate to a documented proxy), port `checkDespawn`. Exit criteria: a MONSTER cap test, a "no daylight surface spawn" test, a long-run population-stability test.

---

### Pitfall 5: Aging/breeding fakes the age/love/baby-scale state instead of porting it

**What goes wrong:**
The breeding subsystem (`Animal.age`/`inLove`/`canMate`, baby spawn, FollowParentGoal) is entirely unbuilt (deferred-goals.md:80: "`*Entity` carries no age/love state"). Naive ports get these wrong: (1) `age` is a SIGNED int — negative = baby (ticks UP toward 0 = adult), positive = the breeding cooldown counting down for adults — a port that uses a bare "isBaby bool" loses the gradual grow-up and the `ageUp(int)` from feeding; (2) the `inLove` 600-tick timer and the `canFallInLove` gating get faked as a flag; (3) the BABY hitbox/scale (`getScale` ~0.5) and reduced-attribute inheritance get skipped — so a baby pig has an adult hitbox (collision/pathfinding wrong, see Pitfall 6) and adult stats; (4) the breeding COOLDOWN (`age = 6000` post-breed) and the XP-orb reward (`1 + random.nextInt(7)`) get dropped; (5) int overflow — `age` increments unbounded if `ageUp`/the cooldown decrement is mis-ported.

**Why it happens:**
"Baby vs adult" reads as a boolean, so the signed-int age machine (vanilla `AgeableMob.age` with `forcedAge`/`ageUp`/the −24000 baby start) gets collapsed. The hitbox/scale and attribute inheritance are easy to forget because they live in `getDimensions`/`getScale`/the child's `createAttributes`, not in `Animal` itself.

**How to avoid:**
- **Port `AgeableMob.age` as the signed-int state machine**: baby starts at `BABY_START_AGE = -24000`, `aging()` increments toward 0, `ageUp(int)` jumps it (feeding), adult breeding sets `age = 6000` counting down. javap `AgeableMob.aging`/`setAge`/`ageUp` and `Animal.customServerAiStep` for the inLove decrement.
- **Port `Animal.canMate`/`canFallInLove`/`setInLove(player)`** with the real `inLove` 600-tick timer, the `BreedGoal` partner search → `Animal.spawnChildFromBreeding` → `finalizeSpawnChildFromBreeding` (XP orb `1 + nextInt(7)`, both parents `age = 6000`, baby `age = -24000`).
- **Port the baby hitbox/scale**: `getScale()` returns the baby factor; `getDimensions(pose).scale(...)` shrinks the AABB — the baby's collision + pathfinding + the wire `scale`/`baby` metadata must reflect it (an adult-hitbox baby climbs/clips wrong).
- **Port child attribute inheritance** where vanilla does it (most passive babies use the species `createAttributes`; verify per species).
- **Watch the int draw order** in `finalizeSpawnChildFromBreeding` (the XP-orb `nextInt(7)` is an RNG draw — keep breeding draws inside the goal/finalize callback so they don't perturb the oracle-style determinism, Pitfall 1).

**Warning signs:**
- An `isBaby bool` field instead of a signed `age` int.
- A baby pig with the same hitbox/eye-height as an adult (collision/path bugs).
- Breeding produces no XP orb, or both parents can immediately re-breed (no cooldown).
- `age` grows without bound in a long run.

**Phase to address:**
The **aging/breeding phase** — port the signed-int age machine, the inLove timer, the baby scale/hitbox, and `finalizeSpawnChildFromBreeding` (XP + cooldown) 1:1 from `AgeableMob`/`Animal`. Exit criteria: "baby grows to adult over N ticks," "baby hitbox is half," "breed → XP orb + 6000 cooldown."

---

### Pitfall 6: Pathfinder step-up + plant collision regressions for the new mob hitboxes

**What goes wrong:**
A prior session hit mobs "climbing grass/flowers or out from under blocks" (PROJECT.md known hazards: "IsSolid/blocksMotion + WalkNodeEvaluator jump-ceiling"). Adding mobs with DIFFERENT hitboxes — a tall zombie/skeleton, a wide spider (1.4 wide, 0.9 tall, climbs walls in vanilla), a baby (half-scale, Pitfall 5) — re-exposes this. A naive port reuses the pig's nav config (`m.navigation.speed = pigWalkSpeed`, ai_mob.go:148 — a single tunable) for every mob, so: a spider doesn't get its climb/wider-path behavior; a baby with an adult-sized path node clips; a tall mob's head-clearance check is wrong; and the `blocksMotion`/`IsSolid` plant fix (already tuned for grass/flowers) must hold for the new mobs' jump-ceiling.

**Why it happens:**
The pig's nav was tuned for one hitbox. Mob hitboxes vary (width/height/babyness), and the `WalkNodeEvaluator` jump/step-up/head-clearance reads depend on the mob's dimensions. Copy-pasting the pig's nav config silently mis-sizes every other mob.

**How to avoid:**
- **Drive the path-node evaluator from the mob's real dimensions**, not a per-species copy of `pigWalkSpeed`. The step-up height, head clearance, and node width must read the mob's AABB (and the baby scale).
- **Reuse the EXISTING grass/flower fix** (`blocksMotion`/`IsSolid` for plants) — verify it holds for the new hitboxes; don't re-introduce climbing on the wider spider.
- **Port spider wall-climbing as its own behavior** (`Spider.tick` sets the climbing flag from `horizontalCollision`) rather than forcing it through the ground navigator.
- **Test each new mob doesn't climb plants or pop out of blocks** with the existing bot gate (cmd/testbot) extended for the new types.

**Warning signs:**
- Every mob shares `pigWalkSpeed` / one `WalkNodeEvaluator` config.
- A spider/zombie standing on a flower, or a mob teleporting up through a non-solid block.
- A baby pathing as if adult-sized.

**Phase to address:**
The **JumpControl + fluid phase** (which touches navigation) and each **new-mob phase** — the new-mob phase must size the path evaluator from the mob's dimensions and re-run the plant-collision/step-up gate.

---

### Pitfall 7: New plugin handle ops (held-item, damage, jump, age) cross the frozen-Starlark boundary unsafely

**What goes wrong:**
The plugin entity handle (plugin_entity.go) is a THIN, region-bound, id-re-resolving value (NEVER a live `*Entity`), with a `scratch map[string]float64` for per-goal `get_state/set_state` and a `caps` capability set. Adding the v5 ops — read nearest player's held item + ItemTags (TemptGoal), read/apply mob damage + `lastDamageSource` (PanicGoal), `JumpControl.jump()` (FloatGoal), read/set `age`/`inLove` (Breed/FollowParent) — risks: (1) returning a live pointer (item stack, damage source) instead of frozen scalars across the tick boundary (the freeze contract); (2) an op that resolves `t.cur()` instead of `h.store()` (plugin_entity.go:58, the region-bound store) — the Pitfall 2 wrong-region drop in the plugin layer; (3) overloading the `scratch` float64 map for non-float state (an item id, an age int) — it's `map[string]float64`, so an item identity or a damage-type tag can't live there cleanly; (4) the new mobs' `declare_mob` render/wire-id: a custom mob renders via the base type's wire id ("custom = BEHAVIOR, not a new wire type," main.star:149) — a new type must pick a real `base_type` ("zombie"/"skeleton"/"spider"/"wolf"/"cow"/"sheep"/"chicken") or the client sees nothing.

**Why it happens:**
The existing ops are all float-scalar reads (`entity.x`, `rand_float`) and float `set_state`. Held-item and damage-type introduce NON-float, possibly-pointer data that doesn't fit the `map[string]float64` scratch or the "frozen scalar payload" Emit pattern. The region-binding (`h.region`, plugin_entity.go:38) was RETROFITTED onto the old ops — a new method written by analogy to a pre-region example could call `t.cur()`.

**How to avoid:**
- **Every new mutate/read op resolves through `h.store()`** (plugin_entity.go:58 — the region-bound store), NEVER `t.cur()` directly. This is the plugin-layer form of Pitfall 2.
- **Return frozen/immutable values across the boundary**: a held-item read returns the item id + tag-membership bools as frozen scalars (`is_pig_food`, `is_carrot_on_a_stick` computed HOST-side), NOT a mutable item handle. The ItemTags test (`ItemStack.is(PIG_FOOD)`) is done host-side and only the bool crosses — matching how `on_damage` Emits "plain frozen scalars" (combat.go:311).
- **Don't overload the float64 scratch for non-float state**; if a goal must remember an item identity or a target id, extend the scratch model deliberately (a typed bag), not by encoding an int into a float64.
- **The mob damage op fires the `on_damage` Emit at the LOCKED post-mitigation site** (the final landed amount, combat.go:304), region-scoped via `emitEntityEvent` (region_transfer.go:86), not a raw `t.plugins.Emit` from a region goroutine.
- **Each new mob `declare_mob` picks a valid `base_type`** so it renders on the declared-mob wire path; verify the wire spawn with the bot gate.
- **Enforce capabilities at the new op boundary** (`h.caps`, plugin_capability.go) — a damage/jump op is a higher capability than a read.

**Warning signs:**
- A new handle method calls `h.t.cur()` / `h.t.only()` instead of `h.store()`.
- A handle op returns a Starlark value wrapping a live `*Entity`/item pointer.
- An int (age, item id, type id) is stored into the `map[string]float64` scratch.
- A new declared mob's `base_type` is missing/invalid (client sees no entity).
- A mob `on_damage` Emit bypasses `emitEntityEvent` from inside the fan-out.

**Phase to address:**
Each subsystem phase that adds a handle op: **held-item phase** (frozen tag-bool reads), **damage phase** (region-scoped Emit + post-mitigation site), **jump phase**, **aging phase** (typed non-float scratch). The **new-mob phases** must each verify the `base_type` render via the bot gate.

---

### Pitfall 8: Stale binary / stale gopls — testing old code or trusting false diagnostics

**What goes wrong:**
Two recurring process traps (PROJECT.md known hazards): (1) `go build` then forgetting to rebuild `sulfur.exe` → the bot gate tests OLD code (a "fixed" bug reappears because the binary is stale); (2) gopls diagnostics are "STALE+FALSE" — phantom errors or missed real ones, especially across the CGO=0/CGO=1 (`-race`) split and the `python` build tag.

**Why it happens:**
The bot gate runs a built `sulfur.exe`; a change not rebuilt is invisible to it. gopls caches across the build-tag matrix and lags the real compiler.

**How to avoid:**
- **Gate ONLY on real `CGO_ENABLED=0 go build` + `go vet` + `go test`** (and `-race` in Docker), never on gopls.
- **Rebuild `sulfur.exe` before any bot-gate run** — a scripted step, not manual.
- **The `math/rand` grep gate** (forbidding the `math/rand` global; per-entity seeded `entityRandom` only) must run on every goal-adding plan so a new mob doesn't reintroduce shared global rand (the determinism + data-race source, ai_random.go:30).

**Warning signs:**
- A bug "comes back" after a fix; a gate result contradicts the source.
- gopls shows errors `go build` doesn't (or vice versa).
- A new goal/mob imports `math/rand` instead of drawing through the handle's `rand_*`.

**Phase to address:**
Every phase — a standing gate discipline. The roadmap should restate "real build + vet + test + Docker -race + math/rand grep; never gopls; rebuild sulfur.exe before the bot gate" as the verification contract for each plan.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Fake `BYPASSES_ARMOR`/panic-causing as `const false` in the MOB path | No DamageType table needed | Breaks PanicGoal (needs real `lastDamageSource.is(panic)`), violates 1:1 | NEVER for v5 — the keystone's job is the real tag table |
| `isBaby bool` instead of signed `age` int | Simpler | Loses grow-up, feeding ageUp, breeding cooldown, overflow safety | NEVER — port the int machine |
| Reuse `creatureCap`/`pigWalkSpeed` for every new mob | One code path | Wrong MONSTER cap, wrong hitbox pathing | NEVER for hostiles/varied-hitbox mobs |
| Relax the hostile light-gate (no light engine) | Ships hostiles sooner | Monsters spawn in daylight (flood/grief) | Only as a DOCUMENTED, cited proxy deviation, never silent |
| Skip `checkDespawn` | Spawning works | Monster population climbs unbounded | Only in an isolated dev test, never shipped |
| Apply cross-region damage inline from the attacker's goroutine | Less plumbing | Data race / wrong-region silent drop | NEVER — defer to barrier or route to owner |
| Split a goal's Go-native + Starlark halves across plans | Smaller plans | Pig oracle red the whole gap; merge-time RNG desync | NEVER — lockstep in one plan |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| O(mobs²) breeding partner search (every animal scans every animal) | Tick time spikes with herd size | Bounded cross-region broad phase (`entitiesNearAcrossRegions` / per-column buckets), not a full store range | A large herd of breedable animals |
| O(mobs²) hostile target selection (every monster scans every player+mob) | Tick spikes with mob count | Bounded `nearestPlayer`-style scan over the player list + per-section buckets, ported SYNCHRONOUS first | Many hostiles + many players |
| Per-tick fluid check for every mob (`isInWater`/`getFluidHeight` for FloatGoal) | Per-tick block reads ×N mobs | Port the fluid predicate SYNCHRONOUS + inline first (spawn-scan style), profile before any async | Many swimming mobs |
| Adding `ants`/`xsync`/`conc` to a new subsystem BEFORE the synchronous port works | Premature async over unverified logic | OPTIMIZATION-LAST: port synchronous + tick-owned (TICK-05), THEN layer async only if profiling demands (spawner inline-first → OPT-03 split is the template) | N/A — process rule |

The optimization-last mandate (PROJECT.md Key Decision: "Build vanilla logic first, Leaf optimizations last") governs: port every new subsystem SYNCHRONOUS and tick-owned first. The spawner shows the pattern — inline, then OPT-03 split only the read-only scan off-tick over an immutable snapshot. Do not reach for `ants`/`xsync` for target-selection, breeding, or fluid checks until a synchronous version is correct and profiled.

## "Looks Done But Isn't" Checklist

- [ ] **Mob damage pipeline:** Often missing the real DamageType-tag table — verify `BYPASSES_ARMOR` and the panic-causing tags are REAL `source.is(tag)` reads, not `const false`.
- [ ] **PanicGoal:** Often missing the `lastDamageSource.is(panicCausing)` gate — verify it reads a real damage source, not a faked hurt bool (deferred-goals.md rule).
- [ ] **Hostile spawns:** Often missing the per-category MONSTER cap, the light-gate, AND `checkDespawn` — verify all three (cap + no-daylight + population-stability).
- [ ] **Breeding:** Often missing the XP orb + the 6000-tick cooldown + the baby half-scale hitbox — verify each.
- [ ] **Cross-region damage/breeding:** Often missing owner-routing — verify with `strictRegion`+`-race` that no path resolves `cur()` from a region goroutine.
- [ ] **New goals:** Often desync the pig oracle — verify `TestPluginPigEqualsGoNativePig` is GREEN after each goal, with the Go-native + Starlark halves in the SAME plan.
- [ ] **New mobs:** Often missing a valid `base_type` (no client render) — verify the wire spawn with the bot gate.
- [ ] **New mob hitboxes:** Often climb plants / clip blocks — verify the plant-collision + step-up gate per new mob.
- [ ] **Every plan:** Often tested against a stale `sulfur.exe` — verify a rebuild precedes the bot gate; gate on real build/vet/test/-race, never gopls.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Pig oracle desync (1) | MEDIUM | Bisect to the goal that added a draw; move the draw inside the running-goal callback; make Go-native + Starlark identical at the same priority; re-run the 500-tick oracle |
| Cross-region race/drop (2) | HIGH | `-race` (Docker) to locate; reroute the mutation through `regionForEntity`/barrier; add a `strictRegion` test; re-verify N=2 |
| Combat-math paraphrase (3) | MEDIUM | javap the mob `actuallyHurt`; rewrite using the combat.go helpers; add exact-value damage tests across armor/absorption/i-frame/bypass |
| Hostile spawn flood (4) | MEDIUM | Generalize cap per-category; add the light-gate; port `checkDespawn`; add cap + daylight + population tests |
| Faked aging/breeding (5) | MEDIUM | Replace `isBaby` with signed `age`; port `finalizeSpawnChildFromBreeding`; add baby-scale + XP + cooldown tests |
| Plant-climb / step-up (6) | LOW-MEDIUM | Drive the node evaluator from real dimensions; re-run the plant-collision gate per mob |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| 1 — RNG draw-order desync | Each goal-adding phase (Panic/Tempt/Float) | `TestPluginPigEqualsGoNativePig` green; Go-native + Starlark goal in one plan; draws only in running-goal callbacks |
| 2 — Cross-region race/drop | Damage keystone + breeding phase | `strictRegion`+`-race` (Docker) tests; owner-routing via `regionForEntity`/`entitiesNearAcrossRegions`; boundary-mob damage/breed test |
| 3 — Combat-math paraphrase | Damage keystone phase | javap-verified port reusing combat.go helpers; real DamageType-tag table; exact-value damage tests |
| 4 — Hostile spawn flood/mis-gate | Hostile-mobs phase | Per-category MONSTER cap test; no-daylight-surface-spawn test; long-run population stability |
| 5 — Faked aging/breeding | Aging/breeding phase | Signed-age grow-up test; baby half-hitbox test; breed→XP+6000-cooldown test |
| 6 — Plant-climb / step-up | Jump/fluid phase + each new-mob phase | Per-mob plant-collision + step-up bot gate; baby/spider hitbox path test |
| 7 — Unsafe plugin handle ops | Each handle-op phase + new-mob phases | New ops resolve `h.store()` (not `cur()`); frozen-scalar returns; region-scoped Emit; valid `base_type` render via bot gate |
| 8 — Stale binary / false gopls | Every phase (standing gate) | Real CGO=0 build+vet+test, Docker -race, math/rand grep; rebuild sulfur.exe before bot gate; never gopls |

## Sources

- D:\ender\server\ai_mob.go — the shared `serverAiStep` driver + `newPigAI` + `reseedMobAI` (the flow that must not be perturbed) — HIGH (codebase)
- D:\ender\server\ai_goal.go — the GoalSelector arbitration / priority walk (how a new goal's `canUse` enters the draw stream) — HIGH (codebase)
- D:\ender\server\ai_random.go — the per-entity seeded `entityRandom` (the shared single stream the oracle pins) — HIGH (codebase)
- D:\ender\server\plugin_pig_test.go — `TestPluginPigEqualsGoNativePig` (the 500-tick byte-identical oracle) — HIGH (codebase)
- D:\ender\plugins\vanilla_pig\main.star — the plugin oracle side + the per-`# DRAW N` ordering discipline — HIGH (codebase)
- D:\ender\server\combat.go — the player `applyDamage`/`actuallyHurt`/CombatRules port to mirror for mobs (float casts, draw order, BYPASSES_* seams) — HIGH (codebase)
- D:\ender\server\spawner.go — the CREATURE cap + MAGIC_NUMBER 289 + the deferred light/density/despawn (the hostile-spawn prerequisites) — HIGH (codebase)
- D:\ender\server\region_transfer.go — `regionForEntity`/`owningRegion`/`forEachRegion`/`entitiesNearAcrossRegions`/`emitEntityEvent` (the cross-region routing primitives) — HIGH (codebase)
- D:\ender\server\plugin_entity.go — the thin region-bound handle + `store()`/`scratch`/`caps` (the frozen-boundary contract) — HIGH (codebase)
- D:\ender\.planning\milestones\v4-phases\24-vanilla-mobs-as-plugins\deferred-goals.md — the cited subsystems + the "do NOT fake a hurt flag" rule + each goal's missing prerequisite — HIGH (project ground truth)
- D:\ender\.planning\PROJECT.md — Current Milestone, Key Decisions (1:1 mandate, optimization-last, thin-id handle, regionize-a-working-seam), known hazards — HIGH (project ground truth)
- Unobfuscated 26.2 jar classes cited throughout (LivingEntity/Mob.actuallyHurt, AgeableMob/Animal, NaturalSpawner, Monster, PanicGoal/BreedGoal/TemptGoal/FloatGoal, WalkNodeEvaluator) — to be javap-verified BEFORE writing each port — HIGH (the standing mandate)

---
*Pitfalls research for: adding mob subsystems + new mob types to Sulfur (v5)*
*Researched: 2026-06-29*
