# Project Research Summary

**Project:** Sulfur v5 — Mob Behaviors & Living-Entity Subsystems
**Domain:** 1:1 jar-port mob/AI subsystems for a Minecraft Java 26.2 (proto 776) server in Go (Folia N=2 regionized, CGO=0, Starlark-plugin-authored mobs)
**Researched:** 2026-06-29
**Confidence:** HIGH

## Executive Summary

v5 is **not a design milestone — it is "finish porting the jar."** The shipped server (v1–v4) already has the entity model, AI driver (`serverAiStep`), goalSelector arbitration, navigation, attribute map, Folia regionization, and the dogfooded Starlark pig plugin. v5 builds the **four deferred subsystems** that the pig's remaining goals read — (S1) mob JumpControl + fluid detection, (S2) the mob damage/hurt pipeline, (S3) animal aging/breeding, (S4) held-item read + item tags — and then adds new mob types (passive: cow/sheep/chicken; hostile: zombie/skeleton/spider; neutral: wolf) as thin jar-faithful plugins once those subsystems exist. Every behavior is disassembled from `temp/cache/26.2-inner.jar` and re-expressed in idiomatic Go; nothing here is the kind of work a library does.

The recommended approach is **dependency-driven and convergent across all four research docs.** The mob **damage pipeline (S2) is the keystone** — it has the widest fan-out (PanicGoal for every passive, the entire target-selector family for every hostile, melee-deal-damage for hostiles + wolf, and wolf anger-on-hit all read `lastDamageSource`). It must be a **PARALLEL `*Entity` path (`combat_mob.go`/`actuallyHurtEntity`), NOT an interface-generalization of the player `*tickPlayer` pipeline** — the research discovered two separate attribute systems (player holder vs the real `*attribute.Map`), distinct death flows, and a v3-sealed/tested player path that an interface would put at risk. There are **NO new Go dependencies and NO new cgo** — the concurrency stack (`xsync`/`ants`/`conc`/`x/sync`) already in `go.mod` is the complete substrate, and CGO_ENABLED=0 is trivially preserved. The single real new dependency-graph item is **jar DATA, not a Go lib: a tag extractor** (item-food tags + damage-type tags) added to the `tools/` codegen pipeline — the shared prerequisite for TemptGoal and the `actuallyHurt` armor-bypass/panic-causing branches.

The dominant risk is the **bit-fragile pig oracle**: `TestPluginPigEqualsGoNativePig` ticks a Go-native and a Starlark pig from one shared per-entity seeded RNG and demands a byte-identical 500-tick observation. Any new RNG draw at the wrong point in the shared flow desyncs both streams and the oracle goes red. The mitigation, repeated across the docs, is absolute: **add each new RNG-drawing goal to the Go-native oracle AND the plugin pig IN LOCKSTEP, in the SAME plan, confined to the running-goal callbacks — never the shared `serverAiStep`/navigation flow, and never split the two halves across plans.** Aging decrements are RNG-free and therefore safe; goal draws (Float/Panic/Tempt/Breed-finalize) are the hazard. The second-order risk is Folia: the damage write is the first true cross-region mutation, so cross-region hits must be barrier-queued `damageIntent` routed via `regionForEntity`/owner, gated on Docker `-race` + `strictRegion`.

## Key Findings

### Recommended Stack

v5 adds **nothing to the server `go.mod`** and introduces **no cgo** — it is a pure 1:1 jar-port onto seams that already exist. Every subsystem is hand-ported Go against the existing entity/AI/attribute/combat scaffolding; pulling in a nav/spatial/tag library would itself violate the 1:1 mandate (it would not be the vanilla call chain). The only real new work outside hand-porting is **ONE codegen-pipeline addition: a tag extractor** — because the current `tools/` pipeline extracts registry *entries* but not registry *tag membership*. New mobs are authored as Starlark plugins (the v4 `vanilla_pig` pattern reused verbatim).

**Core technologies (all already present):**
- **Go 1.25.0 (go.mod) / 1.26.1 toolchain**: server + all hand-ported mob logic — generics/`min`/`max`/`sync` already cover everything; no language gap.
- **`Tnze/go-mc` fork (in-repo)**: entity IDs, item IDs, spawn/metadata/damage packet codec — v5 adds NO new packet types; mobs use the existing declared-mob wire path.
- **`go.starlark.net`**: the runtime each new mob is authored in (pure-Go, CGO=0, sandboxed) — new `.star` files, not new deps.
- **`xsync/v4` + `ants/v2` + `conc` + `x/sync`** (already in `go.mod`): the complete async substrate; reach for them ONLY as proven-neutral optimizations after the synchronous port is correct.

**The one real new dependency — jar DATA, not a library:**
- **`tools/java/GenTags.java` + `gen_tags.go` + `tags.go.tmpl` (NEW)**: extract item-food tags (`PIG_FOOD`, `COW_FOOD`, etc.) and damage-type tags (`PANIC_CAUSES`, `BYPASSES_ARMOR`, `IS_FIRE`, ...) into `data/tag/`. Verified absent from current output: `items.json` carries components but no tag field; `datapack.json` carries only `{elements,stable,tags}` booleans, not membership. Required for S4 (Tempt) and S2 (armor-bypass/panic branches).
- **Wolf attribute supplier (NEW)**: the ONE missing supplier in `level/attribute/defaults.go` (cow/sheep/chicken/pig/zombie/skeleton/spider exist; wolf must be ported 1:1 from `Wolf.createAttributes()`).

**CGO=0 gate (unchanged from v4):** `CGO_ENABLED=0 go build ./...` + `go list -deps ./... | grep -i gopy` must be EMPTY + `go test -race`.

### Expected Features

The four subsystems are the load-bearing prerequisites; each mob is a thin plugin once its subsystems exist. The dependency graph IS the build order.

**Must have (table stakes):**
- **S2 mob damage pipeline + `lastDamageSource` + PANIC_CAUSES tag** — a mob that can't take/deal damage isn't a mob; the keystone unblocks the most.
- **S1 JumpControl + fluid detection (FloatGoal)** — without it any mob in water sinks/suffocates (visibly broken); needed by every swimming mob; independent of S2 (can run in parallel).
- **S4 held-item read + ItemTags (TemptGoal)** — how a player leads/feeds; the same held-item read powers S3's love-on-feed.
- **S3 aging + breeding (BreedGoal/FollowParentGoal)** — a cow you can't breed is a decoration; depends on S4.
- **Pig parity** — wire the pig's 5 deferred goals onto the existing plugin; the 1:1 dogfood gate that PROVES S1–S4 before any new mob.
- **Cow / Sheep / Chicken** — thin plugins after S1–S4 (sheep adds EatBlockGoal; chicken adds aiStep egg/slow-fall; cow adds milking interact).
- **Shared hostile primitives + Zombie/Skeleton/Spider** — `MeleeAttackGoal`/`NearestAttackableTargetGoal`/`HurtByTargetGoal` built once; melee-only subset acceptable for skeleton/spider v1.
- **Hostile spawning (S5)** — a survival night must exist; MONSTER category + per-category cap + a day/night spawn pass.

**Should have (competitive / differentiator):**
- **Wolf** — a tameable companion is flagship; ship the WILD subset first (Float/Panic/Melee/target), tame/sit/owner/anger as a second pass.
- Sheep wool-regrow + dye; chicken egg-lay; spider wall-climb + light-gated aggression; skeleton bow (RangedBowAttackGoal + reassessWeaponGoal).
- Full **light-propagation cave spawning** (`isDarkEnoughToSpawn` with a real light engine).

**Defer (v2+):**
- Mob variants (Husk/Drowned/Stray/Bogged/CaveSpider/MushroomCow) — same skeletons.
- `MoveThroughVillageGoal` (needs village pathing); Brain/behavior-tree mobs (none of the six use Brain).
- Barrier-resolved full-fidelity cross-region breeding (v5 ships the same-region cut).

### Architecture Approach

v5 bolts onto five existing, shipped seams — it gets no greenfield design. The decisive discovery: **there are TWO attribute systems** — `*tickPlayer` carries its own `attributeHolder` (the entire `combat.go` pipeline is built on it), while `*Entity` carries the REAL `*attribute.Map`. This forces the keystone to be a parallel `*Entity` path. The shared CombatRules/clamp helpers (`combatRulesGetDamageAfterAbsorb`, `mthClampF`, `maxF`, NaN/Inf clamps) are already free functions and MUST be reused (not duplicated); only the pure math is factored, never the player flow itself.

**Major components:**
1. **Mob damage pipeline (`combat_mob.go` + `damage_source.go`, NEW)** — `applyDamageEntity`/`actuallyHurtEntity` reading the mob's real `*attribute.Map`; `lastDamageSource`/`health`/`lastHurt`/`invulnerableTime`/`hurtTime` as NEW tick-owned `*Entity` fields; a `damageIntent` barrier queue for cross-region hits; region-scoped `on_damage` Emit via `emitEntityEvent`. `handleAttack` gains a dual-resolve (player OR mob target).
2. **JumpControl + fluid (`ai_mob.go` field + `fluid_mob.go`, NEW)** — a `jumpControl{wantJump}` latch consumed in `serverAiStep`'s JUMP slot after `navigation.tick` (jar order), the real `jumpFromGround` impulse, and `mobIsInWater`/`mobFluidHeight` built on the existing `fluidAt` read.
3. **Aging/breeding (`*Entity` fields + `aging.go`, NEW)** — signed-int `age`/`forcedAge`/`inLove`/`loveCause`; `tickMobAging` (RNG-free decrements); baby spawn reuses `spawnDeclaredMob` verbatim (correct region routing + per-id reseed); **same-region partner search for v5** (cross-region full fidelity deferred).
4. **Held-item + tags (`item_tags.go`, NEW)** — `nearestPlayerAt` variant returning player id; `world.player_main_hand`; host-side `itemTagContains` returning frozen tag-bools across the Starlark boundary.
5. **New mobs as plugins** — `mobAI` gains a second `targetSelector goalSelector` (instance, not new mechanism) run in jar order; `buildAIFromDecl` splits TARGET-flag goals into it; `attackTargetID` thin-id field; `"wolf"` base type + supplier added; spawner gains a day/night + (proxy) light gate.

### Critical Pitfalls

1. **RNG draw-order divergence breaks the 500-tick pig oracle (the single biggest risk)** — add each new RNG-drawing goal to the Go-native oracle AND the plugin pig in the SAME plan, lockstep, draws confined to running-goal callbacks (never `serverAiStep`/`navigation.tick`); keep the oracle green as the exit gate of every goal-adding phase. Aging is RNG-free (safe); inserting the empty `targetSelector` tick is a verified no-op for the pig.
2. **Cross-region data races + wrong-region silent drops (damage + breeding)** — the damage write is the first true cross-region mutation; route ALL cross-entity mutation through the OWNER's region (barrier-queued `damageIntent` / `regionForEntity`), never the actor's `cur()` (which silently falls back to region 0). Gate on Docker `-race` + `strictRegion`.
3. **Combat-math paraphrase in the mob `actuallyHurt` port** — keep every op `float32`, every clamp/branch order verbatim; reuse the `combat.go` helpers; ship a REAL DamageType-tag table so `BYPASSES_ARMOR` and the panic-causing tags are genuine `source.is(tag)` reads, NOT `const false` (the keystone's actual deliverable).
4. **Hostile spawn flood/mis-gate without a light engine** — generalize the cap to per-category (MONSTER 70 vs CREATURE 10, keep `/289`), tally MONSTER in `countByCategory`, port `checkDespawn` (the real population bound), and EITHER build a minimal light read OR gate to a documented darkness-by-gametime proxy — never silently spawn monsters in daylight.
5. **Faked aging/breeding state** — port `AgeableMob.age` as the signed-int machine (-24000 baby start, ticks up; +6000 breed cooldown), the real `inLove` timer, baby half-scale hitbox, and `finalizeSpawnChildFromBreeding` (XP orb `1+nextInt(7)` + cooldown) — never an `isBaby bool`.

## Implications for Roadmap

Based on research, the suggested phase structure is the convergent dependency order from all four docs (Architecture S6, Features dependency graph, Pitfalls phase mapping). The keystone comes first; pig parity is the dogfood gate that must precede any new mob.

### Phase 1: Damage Keystone (S2)
**Rationale:** Widest fan-out — PanicGoal (all passives), the target-selector family (all hostiles), melee-deal-damage (hostiles + wolf), and wolf anger-on-hit ALL read `lastDamageSource`. Build first.
**Delivers:** `combat_mob.go` (`applyDamageEntity`/`actuallyHurtEntity`, parallel `*Entity` path), `damage_source.go` + the tag set, NEW `*Entity` fields, cross-region `damageIntent` barrier queue, `handleAttack` dual-resolve, region-scoped `on_damage` Emit, `was_hurt`/`last_damage_type` handle attrs.
**Depends on:** the NEW tag extractor (damage-type tags) — schedule the codegen tag-extractor work at/before this phase.
**Avoids:** Pitfalls 2 (cross-region race), 3 (combat-math paraphrase).
**Gate:** player can hit a Go-native mob; i-frame-gated damage; `on_damage` fires; `lastDamageSource` set; **pig oracle STILL GREEN** (no AI RNG touched); Docker `-race` + `strictRegion` clean.

### Phase 2: JumpControl + Fluid (S1)
**Rationale:** Independent of S2 (could parallelize) but sequenced after it to keep pig-plugin churn serial; needed by FloatGoal on every swimming mob.
**Delivers:** `mobAI.jumpControl` + `jumpFromGround` impulse in `serverAiStep`, `mobIsInWater`/`mobFluidHeight` on `fluidAt`, `nav.jump()`/`entity.in_water` handle ops, FloatGoal on the pig (oracle + plugin in lockstep).
**Avoids:** Pitfall 1 (FloatGoal `nextFloat()<0.8` draws — both pigs in one plan), Pitfall 6 (node evaluator from real dimensions).

### Phase 3: PanicGoal (S2 consumer)
**Rationale:** Depends on Phase 1's `lastDamageSource`.
**Delivers:** PanicGoal@1 on the pig (oracle + plugin) reading `entity.was_hurt`/`last_damage_type` + the panic-causing tag set.
**Avoids:** Pitfall 1 (water-search vs flee-pos draw order), Pitfall 3 (real `is(panicCausing)` read, not a faked flag).

### Phase 4: Held-Item + Item Tags (S4)
**Rationale:** Independent of S1–S3; required BEFORE breeding (Tempt feeding sets `in_love`).
**Delivers:** `itemTagContains` + embedded tag data, `world.player_main_hand`, `world.item_in_tag`, nearest-player-with-id; pig TemptGoal@4 x2 (PIG_FOOD + CARROT_ON_A_STICK).
**Depends on:** the NEW tag extractor (item-food tags).
**Avoids:** Pitfall 7 (frozen tag-bool returns, host-side `is(tag)`).

### Phase 5: Aging + Breeding (S3) — Pig Parity / DOGFOOD GATE
**Rationale:** Depends on S4 (feeding sets `in_love`); reuses `spawnDeclaredMob`. Completing it wires the pig's 5th deferred goal -> **pig parity complete**, the dogfood gate that proves S1–S4 1:1 before any new mob reuses them.
**Delivers:** signed-int `age`/`inLove` + `tickMobAging`, same-region partner search, `spawn_baby`, BreedGoal@3 + FollowParentGoal@5 on the pig (oracle + plugin).
**Avoids:** Pitfall 5 (signed age, baby scale, XP+cooldown), Pitfall 2 (same-region cut, baby via `regionForEntity`).
**Gate:** two fed pigs breed; baby follows parent; **full 8-goal pig oracle GREEN** (TestPluginPigEqualsGoNativePig).

### Phase 6: New Passive Mobs (Cow / Sheep / Chicken)
**Rationale:** Pure plugin authoring against the now-proven subsystem set; no new Go subsystems. MUST follow pig parity.
**Delivers:** cow/sheep/chicken `.star` plugins; `categoryOf` maps them to CREATURE; sheep EatBlockGoal + shear/wool; chicken aiStep egg/slow-fall; cow milking interact.
**Avoids:** Pitfall 6 (per-mob hitbox node sizing).

### Phase 7: Hostiles (Zombie / Skeleton / Spider) + Spawn Rules (S5)
**Rationale:** Depends on Phase 1 (mob->player damage) + the new `targetSelector`.
**Delivers:** `mobAI.targetSelector` + serverAiStep jar-order insert, `buildAIFromDecl` TARGET split, `attackTargetID`, shared `MeleeAttackGoal`/`NearestAttackableTargetGoal`/`HurtByTargetGoal`, per-category MONSTER cap + `checkDespawn` + day/night (proxy) light gate, the three plugins (melee-only subset OK for skeleton/spider v1).
**Avoids:** Pitfall 4 (per-category cap, despawn, documented light proxy), Pitfall 1 (target/attack goal draws in lockstep).

### Phase 8: Wolf (Neutral)
**Rationale:** Needs BOTH Phase 1's `lastDamageSource` (anger-on-hit) AND Phase 7's target machinery; most complex single mob. Last.
**Delivers:** `"wolf"` base type + NEW wolf attribute supplier; WILD subset first (Float/Panic/Avoid/Leap/Melee/target), then tame/sit/owner/anger second pass (TamableAnimal owner-state).
**Avoids:** Pitfall 1 (wolf goal draws lockstep - the discipline holds even without a wolf oracle).

### Phase Ordering Rationale
- **Damage is the keystone** because PanicGoal, the entire hostile target family, melee, and wolf-anger all read `lastDamageSource` — building anything else first leaves it built-but-unwired or faked (forbidden).
- **Held-item (Phase 4) precedes breeding (Phase 5)** because feeding sets `in_love`.
- **Pig parity (Phase 5) is the hard gate before new mobs (6-8)** — the subsystem set must be proven 1:1 against the oracle before reuse; the docs are unanimous that new mobs must NOT precede the dogfood.
- **Hostiles before wolf** — wolf depends on both damage and the target selector.
- The order keeps the shared `serverAiStep`/plugin-pig churn serial so the bit-fragile oracle is touched one goal at a time, lockstep on both halves.

### Research Flags

Phases likely needing deeper research (`/gsd-research-phase`) during planning:
- **Phase 1 (Damage Keystone):** the cross-region `damageIntent` barrier-queue design is the first true cross-region write — needs the exact barrier/inbox plumbing (the `transferIntent`/`asyncIn2` discipline) pinned before coding. Also confirm the existing registry extraction surfaces damage-type *entries* with their fields.
- **Phase 5 (Aging/Breeding):** javap-verify `AgeableMob.aging`/`setAge`/`ageUp`, `Animal.canMate`/`spawnChildFromBreeding`/`finalizeSpawnChildFromBreeding` (XP/cooldown draw order) before porting; the same-region-cut vs barrier-resolved decision must be forced.
- **Phase 7 (Hostiles + S5):** the light-engine vs documented darkness-proxy decision must be FORCED (not left silent); javap `isDarkEnoughToSpawn`/`checkMonsterSpawnRules`/`checkDespawn`; the per-category cap generalization.
- **Phase 8 (Wolf):** TamableAnimal owner/sit/anger state model + the OwnerHurt target chain; verify/add the wolf attribute supplier first.

Phases with standard patterns (lighter research):
- **Phase 6 (Passive mobs):** the pig plugin template generalizes directly; per-mob extras (milk/egg/wool) are small aiStep/interact ports. Mostly plugin authoring once S1–S5 are proven.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | `go.mod` read directly; tag-extractor gap verified against actual extracted JSONs (`items.json`/`datapack.json` lack membership); no new dep needed. |
| Features | HIGH | Every behavioral claim javap-disassembled from `26.2-inner.jar` this session; registerGoals + priorities + ctor args + aiStep constants cited inline. |
| Architecture | HIGH | Every integration point is a real `file:function` read this session; the dual-attribute-system discovery and the parallel-path decision are grounded in `combat.go`/`attributes.go`/`entity.go`. |
| Pitfalls | HIGH | Grounded in the actual codebase (ai_mob/ai_goal/combat/spawner/plugin_pig_test/region_transfer) plus prior-session post-mortems in deferred-goals.md / PROJECT.md. |

**Overall confidence:** HIGH

### Gaps to Address

- **Light engine vs darkness proxy (Phase 7):** a faithful `isDarkEnoughToSpawn` needs block+sky light the v1 spawner lacks. The roadmap MUST force the decision — build a minimal light read OR ship a documented gametime-darkness proxy (never a silent daylight flood). MEDIUM feasibility flagged in research.
- **Per-category MONSTER cap + checkDespawn (Phase 7):** cap formula exists (70 vs 10, `/289`); verify hostile types map to MONSTER in `countByCategory`, and port `checkDespawn` (cap gates spawns, not population). Force this in the phase.
- **Wolf attribute supplier (Phase 8):** the ONE missing supplier — verify/add via a 1:1 `Wolf.createAttributes()` port before wolf work.
- **Cross-region breeding fidelity (Phase 5):** v5 ships the same-region cut (partners must share a region); full-fidelity barrier-resolved match is deferred. The roadmap should state this as an accepted, documented deviation, not leave it silent.
- **Damage-type registry entries (Phase 1):** confirm the existing registry extraction surfaces damage-type entries with their fields (exponent/scaling/message-id); extend slightly if only IDs are present. The `DamageSource` math is a javap port regardless.

## Sources

### Primary (HIGH confidence)
- `temp/cache/26.2-inner.jar` via `javap -c -p` (Zulu 25) — all behavioral claims: FloatGoal/PanicGoal/TemptGoal/BreedGoal/FollowParentGoal bytecode, AgeableMob/Animal signatures, every mob `registerGoals`, MobCategory `<clinit>` caps (MONSTER 70/CREATURE 10), `isDarkEnoughToSpawn`/`checkMonsterSpawnRules`, aiStep constants.
- Codebase (read this session): `server/combat.go`, `attack_dispatch.go`, `attributes.go` (dual attribute system); `ai_mob.go`, `ai_goal.go`, `ai_random.go`, `navigation.go` (AI driver, goalSelector, per-entity RNG); `fluid.go` (`fluidAt`); `plugin_mob_decl.go`, `plugin_mob_ai.go`, `plugin_entity.go` (declare_mob, thin-id handle); `region_transfer.go`, `spawner.go` (Folia routing, cap, cross-region scan); `entity.go`; `plugin_pig_test.go` (the 500-tick oracle); `plugins/vanilla_pig/main.star`; `level/attribute/defaults.go` (suppliers; wolf absent).
- `tools/main.go`, `tools/java/ExtractAll.java`, `tools/gen_item_food.go`, `temp/jsons/26.2/{entities,items,datapack}.json` — verified tag membership is NOT currently extracted (the real new gap).
- `.planning/milestones/v4-phases/24-vanilla-mobs-as-plugins/deferred-goals.md` — the 5 deferred goals + cited missing subsystems + the "do NOT fake a hurt flag" rule (project ground truth).
- `.planning/PROJECT.md`, `CLAUDE.md` — v5 scope, 1:1 mandate, optimization-last, thin-id/regionize decisions, CGO=0 constraint (project ground truth).

### Secondary (MEDIUM confidence)
- Monster spawn light-rule port feasibility — MEDIUM (mechanism HIGH, but a faithful version needs a light engine the v1 spawner lacks; documented-proxy fallback flagged).
- `.planning/research/STACK.md`, `FEATURES.md`, `ARCHITECTURE.md`, `PITFALLS.md` — the four detailed research files this summary synthesizes.

---
*Research completed: 2026-06-29*
*Ready for roadmap: yes*
