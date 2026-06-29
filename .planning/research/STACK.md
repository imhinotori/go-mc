# Stack Research

**Domain:** v5 Mob Behaviors & Living-Entity Subsystems (Minecraft Java 26.2 / proto 776 server in Go) — adding 4 deferred mob subsystems + new mob types to a shipped server
**Researched:** 2026-06-29
**Confidence:** HIGH

## Headline Finding (read this first)

**NO new Go dependencies are needed for v5. NO new cgo. CGO_ENABLED=0 is trivially preserved.**

v5 is a pure 1:1 jar-port milestone layered onto seams that already exist. Every subsystem
(JumpControl + fluid detection, mob damage/hurt pipeline, animal aging/breeding, held-item +
item-tags) is *server logic ported from the unobfuscated 26.2 jar*, re-expressed in idiomatic
Go on top of the existing entity/AI/attribute/plugin scaffolding. None of it is the kind of
work a third-party library does for you — and pulling one in would *violate* the 1:1 mandate
(an off-the-shelf nav/spatial/tag lib would not be the vanilla call chain).

The concurrency stack already in `go.mod` (`xsync/v4`, `ants/v2`, `conc`, `golang.org/x/sync`)
is the complete substrate for any async layering v5 might want. The only real work outside Go
hand-porting is **ONE codegen-pipeline addition: a tag extractor** (item tags + damage-type
tags), because the current `tools/` pipeline extracts registry *entries* but not registry *tag
membership*. That is jar-DATA extraction, not a dependency.

So the prescriptive answer for the roadmapper/planner:
1. Add nothing to the server `go.mod`.
2. Add ONE Java tag extractor to `tools/` + ONE Go generator (`gen_tags.go`) + ONE template.
3. Verify the CGO=0 default build (`go list -deps` shows zero gopy) after the milestone — same gate v4 used.

## Recommended Stack

### Core Technologies (ALL already present — no changes)

| Technology | Version | Purpose for v5 | Why no change |
|------------|---------|----------------|---------------|
| Go | 1.25.0 (go.mod) / 1.26.1 toolchain | Server + all hand-ported mob logic | Generics, `min`/`max`, mature `sync` already cover everything mob subsystems need. No language feature gap. |
| `Tnze/go-mc` fork (vendored in-repo as `data/`, `level/`, `server/`) | repo @ proto 776 | Entity registry IDs, item IDs, packet codec for spawn/metadata/damage-event packets | All wire surface for mobs (spawn entity, set entity metadata, entity event/hurt animation, entity velocity for knockback) already shipped in v1–v4. v5 adds no new packet types — mobs spawn via the existing declared-mob wire path. |
| `go.starlark.net` | `v0.0.0-20260613...` | The runtime each new mob is authored in (cow/sheep/chicken/zombie/skeleton/spider/wolf as jar-faithful plugins) | Pure-Go, CGO=0, sandboxed — the v4 dogfood pattern (`plugins/vanilla_pig/main.star`) is reused verbatim. New mobs are new `.star` files + new declared goal/subsystem seams, NOT new deps. |

### Supporting Libraries (concurrency — ALL already present, sufficient for v5)

| Library | Version (go.mod) | Purpose for v5 async layering | When to use in v5 |
|---------|------------------|-------------------------------|-------------------|
| `github.com/puzpuzpuz/xsync/v4` | v4.5.0 | Concurrent maps/queues for any new mob-side index (e.g. an in-love registry, a per-region target cache, a damage-event queue) | Only if a v5 subsystem needs a *concurrent* table. The damage pipeline + aging state live ON the entity (owner-thread, thin-id handle re-resolve), so most v5 state needs no concurrent map at all. Reach for xsync only if profiling shows contention. |
| `github.com/panjf2000/ants/v2` | v2.12.1 | Bounded pool for async target-selection / mob spawning if v5 adds it | Optional/deferred. Correctness-first: port the synchronous vanilla `NearestAttackableTargetGoal` / `Mob.canSee` first; layer an ants pool ONLY as a proven-neutral optimization later (the project's permitted-deviation rule). Do NOT introduce async target-selection before the faithful goal exists. |
| `github.com/sourcegraph/conc` | v0.3.0 | Tick-bounded fan-out/join — already the Folia region barrier (REGION-01) | The mob damage pipeline + aging tick run inside the existing region tick under the existing conc barrier. No new conc usage required; v5 logic slots into the established fan-out. |
| `golang.org/x/sync` | v0.21.0 | `singleflight`/`semaphore`/`errgroup` | Already available. Not specifically needed by v5 mob logic. |
| `github.com/google/uuid` | v1.3.0 | Baby mob UUIDs on breed-spawn, wolf owner UUID | Already present — breeding's child-spawn assigns a fresh UUID exactly as `Animal.spawnChildFromBreeding` does; wolf tame stores the owner's UUID. No new dep. |

### Development Tools — ONE addition to the codegen pipeline (`tools/`, separate module)

| Tool | Purpose | Notes |
|------|---------|-------|
| `tools/java/GenTags.java` (**NEW — add this**) | Extract registry tag membership: item tags (`ItemTags.PIG_FOOD`, `WOLF_FOOD`, `CHICKEN_FOOD`, `COW_FOOD`, `SHEEP_FOOD`-style per-animal food; `CARROT_ON_A_STICK` is a single-item predicate, not a tag) and damage-type tags (`DamageTypeTags.PANIC_CAUSES`, `PANIC_ENVIRONMENTAL_CAUSES`, `BYPASSES_ARMOR`, `IS_FIRE`, `IS_FREEZING`, etc.) | Reflect over `BuiltInRegistries.ITEM.getTagNames()` + `getOrCreateTag(tagKey)` and the `damage_type` registry's tag set after `Bootstrap.bootStrap()`. Emit `tags.json` (`{ "minecraft:item": { "minecraft:pig_food": ["minecraft:carrot", ...] }, "minecraft:damage_type": { "minecraft:panic_causes": [...] } }`). This is the ONLY new extractor v5 needs. |
| `tools/gen_tags.go` (**NEW — add this**) | Generate `data/tag/tags.go` (or `data/item/tags.go` + `data/damage/tags.go`) from `tags.json` | Mirror the existing `gen_item_food.go` shape: read the extracted JSON, emit `map[string][]string` (or membership-set) tables keyed by tag id. Register it in `main.go`'s `generators` slice. |
| `tools/templates/tags.go.tmpl` (**NEW — add this**) | Output template for the generated tag tables | Same pattern as `item.go.tmpl` / `entity.go.tmpl`. |
| `tools/java/ExtractAll.java` (**EDIT**) | Add `tags.json` to the output file list (currently lists `commands.json`, `datapack.json`, etc.) and invoke the tag dump | One-line-ish addition so a single `cd tools && go run . --version 26.2 --extract` produces tags alongside the rest. |
| `javap` / CFR | Verify every ported subsystem against `temp/cache/26.2-inner.jar` bytecode before writing Go | UNCHANGED. The authoritative behavior source for JumpControl, the hurt pipeline, `Animal` aging, and TemptGoal predicates is the jar, read with `/c/Program Files/Zulu/zulu-25/bin/javap -c -p`. Tags are the DATA; javap is the LOGIC. |
| `go test -race` | Quality gate on the new mob damage/aging tick paths | UNCHANGED, non-negotiable per project constraints — the hurt pipeline and aging mutate per-entity state on the owner thread; race-check the region-parallel path. |
| `go list -deps` (default build) | Confirm zero `gopy` / zero cgo in the default import graph after v5 | UNCHANGED — same CGO=0 gate v4 (PLUGIN-06) shipped with. v5 adds no import that could leak cgo. |

## Why each v5 subsystem needs NO library (it's a jar port onto an existing seam)

| v5 subsystem | What it ports (jar class) | Existing seam it builds on | New dep? |
|--------------|---------------------------|----------------------------|----------|
| Mob JumpControl + `isInWater`/`getFluidHeight`/`isInLava` | `net.minecraft.world.entity.ai.control.JumpControl`, `Mob.getJumpControl().jump()`, `Entity.isInWater`/`getFluidHeight(FluidTags.WATER)`/`getFluidJumpThreshold` | `mobAI` (server/ai_mob.go) already has navigation; `TickLoop.fluidAt(pos)` (server/fluid.go) is the position-based fluid primitive to wrap into a mob predicate | **NO** |
| Mob damage / hurt pipeline + `lastDamageSource` | `LivingEntity.hurtServer`/`actuallyHurt`, `DamageSource`, `Mob.getLastDamageSource` | `server/combat.go` `applyDamage`/`actuallyHurt` exist (player-typed) — generalize the same ported chain to `*Entity` with `.ai`; attribute reads (ARMOR, MAX_HEALTH) already exist | **NO** (needs damage-type TAG data — see codegen) |
| Animal aging + breeding | `net.minecraft.world.entity.AgeableMob` (age/`baby`), `Animal` (`inLove`, `canMate`, `spawnChildFromBreeding`), `BreedGoal`, `FollowParentGoal` | Per-entity state on `*Entity`; `uuid` for child; existing spawn/metadata wire path; existing goal-selector | **NO** |
| Held-item read + item tags | `Player.getMainHandItem`, `ItemStack.is(TagKey)`, `ItemTags.PIG_FOOD`, `TemptGoal` `Predicate<ItemStack>` | Existing inventory/slot model (component slots) + nearest-player scan (`world.nearest_player`) | **NO** (needs item-tag membership DATA — see codegen) |
| New mob TYPES (cow/sheep/chicken/zombie/skeleton/spider/wolf) | each `<Mob>.registerGoals()` + `<Mob>.createAttributes()` | Declared-mob plugin path (v4); `level/attribute/defaults.go` already has cow/sheep/chicken/pig/zombie/skeleton/spider attribute suppliers | **NO** (wolf attributes need a 1:1 port — not extracted; see gaps) |

## Jar-DATA the codegen pipeline must produce (the real v5 dependency)

This is the substantive finding. The current pipeline (`cd tools && go run . --version 26.2`)
runs 14 generators. Here is what v5 needs vs. what already exists, verified against the actual
extracted JSONs in `temp/jsons/26.2/`:

| v5 data need | Already extracted? | Source of truth | Action |
|--------------|--------------------|-----------------|--------|
| **Item tag membership** (`ItemTags.PIG_FOOD` → `[carrot, potato, beetroot]`, `WOLF_FOOD`, per-animal `*_FOOD`) | **NO** | `BuiltInRegistries.ITEM` tags (NOT in `items.json` — that report carries per-item data components only, no tag field; `datapack.json` carries only `"tags": true` booleans, not membership) | **NEW extractor** (`GenTags.java` → `tags.json` → `gen_tags.go`) |
| **Damage-type tags** (`DamageTypeTags.PANIC_CAUSES`, `PANIC_ENVIRONMENTAL_CAUSES`, `BYPASSES_ARMOR`, `IS_FIRE`, `IS_FREEZING`, `NO_KNOCKBACK`, …) | **NO** | `damage_type` registry tag set (registry `minecraft:damage_type` IS listed in `datapack.json`, but only its `elements/stable/tags` flags — no membership) | **SAME NEW extractor** (the `damage_type` half of `tags.json`) — required for PanicGoal's `lastDamageSource.is(panicCausingDamageTypes)` and for `actuallyHurt`'s armor-bypass branch |
| **Damage-type registry entries** (the damage types themselves: `mob_attack`, `in_fire`, `fall`, etc., with exponent/scaling/message-id) | **PARTIAL** — `minecraft:damage_type` appears in `registries.json`/`datapack.json` as a registry, IDs present | jar `damage_type` registry | Verify the existing registry extraction surfaces damage-type *entries* with their fields; if only IDs, extend slightly. Behavior (the `DamageSource` math) is a javap port regardless. |
| **MobCategory / spawn rules** (`category` per entity: monster/creature/ambient/water_creature…) | **YES** | `entities.json` already carries `"category"` per entity (verified: pig/cow=`creature`, zombie=`monster`, bat=`ambient`) | NONE — already generated by `gen_entity.go`. Day/night hostile spawn gating reads this. |
| **Entity dimensions** (width/height for collision/AABB target queries) | **YES** | `entities.json` width/height (verified present) | NONE |
| **Spawn-egg → entity mapping** | **Effectively handled** | `SpawnEggItem.useOn` logic exists in `server/item_use.go` / `server/block_interact.go` | NONE — the egg→type resolution is already wired in the item-use path; no new data table needed. (If a generated egg→entity map is wanted for cleanliness it can derive from the item registry naming, but it is NOT a blocker.) |
| **Mob attribute defaults** (MAX_HEALTH/MOVEMENT_SPEED/ATTACK_DAMAGE/ARMOR/FOLLOW_RANGE per type) | **YES, hand-ported** | `level/attribute/defaults.go` has cow/sheep/chicken/pig/zombie/skeleton/spider suppliers (verified) — each value read from the jar's `ldc2_w` constants | NONE for those types; **wolf is MISSING** — port `Wolf.createAttributes()` 1:1 into `defaults.go` (this is a code port, NOT extraction). |
| **Food item properties** (nutrition/saturation — if breeding/feeding cross-checks food) | **YES** | `data/item/food.go` via `gen_item_food.go` | NONE |

### Why tags can't be derived from what's already extracted (verified, not assumed)

- `items.json` (the `--all` report): each item entry is `{components: {...}}` — 11+ data-component
  keys (attribute_modifiers, max_stack_size, item_name, rarity…) but **no tag field**. Tag
  membership is a registry-level structure the per-item report does not serialize.
- `datapack.json`: top level is `{others, registries}`; every registry (including
  `minecraft:item` and `minecraft:damage_type`) carries only `{elements, stable, tags}` as
  **booleans** — i.e. "this registry has tags," not the tag→members map.
- `registries.json`: entries with protocol IDs, no tag membership.

So PIG_FOOD and the damage-type tags are genuinely absent from the current output. The tag
extractor is a true gap, not a re-derivation.

## Installation

```bash
# Server module (github.com/imhinotori/sulfur): NO CHANGES.
# Do NOT add any dependency for v5 mob subsystems.

# Codegen (re-run after adding the tag extractor; codegen deps live in the SEPARATE
# tools/go.mod and never touch the server import graph):
cd tools && go run . --version 26.2 --extract   # regenerates incl. NEW tags.json -> data/tag/

# CGO=0 verification gate (unchanged from v4 PLUGIN-06):
CGO_ENABLED=0 go build ./...
go list -deps ./... | grep -i gopy   # must be EMPTY (zero cgo/python in default graph)
go test -race ./server/...           # race-check the new hurt/aging/jump tick paths
```

## Alternatives Considered

| Recommended | Alternative | When the alternative would make sense |
|-------------|-------------|----------------------------------------|
| Hand-port `JumpControl` + wrap `fluidAt` into a mob `isInWater` predicate | A 3rd-party physics/movement lib | **Never.** Vanilla jump impulse, fluid-jump-threshold, and buoyancy are exact numeric ports; a lib cannot match the bytecode and would break the 1:1 mandate. |
| Extract item/damage tags from the jar (`GenTags.java`) | A community item-tag dataset (e.g. a PrismarineJS/`minecraft-data` JSON, or a hardcoded Go tag list) | **Never for parity.** External datasets drift from 26.2 and are not authoritative; the jar is the source of truth. A hardcoded list rots on the next version bump and defeats the codegen retarget guarantee. |
| Per-section grid bucketing (existing) for hostile-mob target selection / `Mob.canSee` AABB queries | A 3rd-party quad/octree (`s0rg/quadtree`, etc.) | Only if profiling proves grid bucketing is the bottleneck for `NearestAttackableTargetGoal` at scale. Vanilla itself buckets per-section; start there. Not a v5 dependency. |
| `ants/v2` (already present) for any async target-selection pool | `pond`, raw goroutines | If you ever async-ify target selection — but correctness-first: port the synchronous goal before optimizing. ants is already in `go.mod`. |
| Per-entity `lastDamageSource` field on `*Entity` (owner-thread) | `xsync.Map` keyed by entity id | Only if cross-region reads of another mob's last-damage-source emerge. The thin-id handle re-resolves on the owner thread, so a plain field is correct and faster. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| ANY new server `go.mod` dependency for v5 | v5 is a jar-port milestone; every behavior is ported, not delegated. New deps add risk and can leak cgo, breaking the static-binary value prop. | Hand-port onto existing seams; the concurrency stack (`xsync`/`ants`/`conc`/`x/sync`) is already complete. |
| A third-party item-tag / Minecraft-data library | Not authoritative for 26.2, drifts per version, may not match the jar's PIG_FOOD/damage-type tags exactly | The NEW `GenTags.java` extractor reading the jar's own `BuiltInRegistries` tags |
| Hardcoding PIG_FOOD / damage-type-tag members in Go source | Rots on every version bump; bypasses the codegen retarget guarantee; risks divergence from the jar | Generated `data/tag/tags.go` from the extractor (retarget = re-run `tools`) |
| A jump/pathfinding/AI library for JumpControl or target selection | Cannot reproduce vanilla numeric behavior; violates the 1:1 mandate | Port the jar's `JumpControl`/`NearestAttackableTargetGoal`/`Mob.canSee` bytecode |
| Any cgo dependency (incl. accidentally importing the `python` runtime in non-tagged code) | Breaks CGO_ENABLED=0 static binary; gopy is build-tag-gated for a reason | Keep all v5 code in the default (no-tag) graph; gate-check with `go list -deps` |
| Introducing async target-selection / async breeding BEFORE the synchronous port exists | Project rule: cannot async-optimize logic that doesn't exist yet; correctness precedes optimization | Port synchronous vanilla logic first; layer `ants`/`conc` async only as a proven behavior-neutral optimization |

## Stack Patterns by Variant

**For the 4 subsystems (JumpControl+fluid, hurt pipeline, aging/breeding, held-item+tags):**
- Pure Go hand-ports onto existing entity/AI/attribute/combat seams. Zero new deps.
- Only the held-item+tags and hurt-pipeline subsystems consume NEW generated data (item tags,
  damage-type tags) — wire them to the new `data/tag` package.

**For the new mob TYPES (passive/hostile/neutral):**
- Authored as Starlark plugins (the v4 `vanilla_pig` pattern), one `.star` per mob.
- Attribute suppliers: reuse existing `level/attribute/defaults.go` for cow/sheep/chicken/zombie/
  skeleton/spider; **add a 1:1 `Wolf.createAttributes()` port** (the one missing supplier).
- Hostile mobs (zombie/skeleton/spider) need the hurt pipeline (to deal/take damage) + target
  selection (port `NearestAttackableTargetGoal`) + MobCategory-driven day/night spawn gating
  (reads the already-extracted `entities.json` `category`). No new deps for any of it.
- Wolf (neutral) needs tame/owner/sit/anger-on-hit state — per-entity fields + owner UUID
  (`google/uuid`, already present) + the hurt pipeline's `lastDamageSource` for anger-on-hit.

**If profiling later flags target-selection or breeding scan cost:**
- Layer an `ants/v2` pool or `xsync` index — both already in `go.mod`. Optimization only,
  behavior-neutral, `-race` gated.

## Version Compatibility

| Package | Compatible With | Notes |
|---------|-----------------|-------|
| Go 1.25.0 (go.mod) / 1.26.1 toolchain | all current deps | No v5 feature needs a newer Go. |
| `xsync/v4` v4.5.0, `ants/v2` v2.12.1, `conc` v0.3.0, `x/sync` v0.21.0 | Go ≥1.22 (all far below current) | Already validated through v4; v5 adds no new usage that changes compatibility. |
| `tools/` module (separate `go.mod`, `replace => ../`) | server module | Codegen deps stay isolated; adding `GenTags.java`/`gen_tags.go` adds NO runtime dep to the server. The new extractor is plain stdlib Java + the existing Go template machinery. |
| `go.starlark.net` | pure-Go, CGO=0 | New mob `.star` plugins introduce no native code. |
| Generated `data/tag` package | server module | New generated Go source compiled into the default (CGO=0) build — no cgo, no new external import. |

## Sources

- `D:\ender\go.mod` — verified the complete server dependency set (xsync/v4 v4.5.0, ants/v2 v2.12.1, conc v0.3.0, x/sync v0.21.0, google/uuid v1.3.0, go.starlark.net; gopy build-tag-gated) — HIGH
- `D:\ender\tools\main.go` — verified the 14 generators in the pipeline; NO tag generator present — HIGH
- `D:\ender\tools\java\GenEntities.java` + `temp/jsons/26.2/entities.json` — verified MobCategory (`category`) + dimensions ARE extracted per entity — HIGH
- `D:\ender\tools\java\ExtractAll.java` — verified tags are NOT in the extraction output file list; only registries/commands/datapack/etc. — HIGH
- `D:\ender\temp\jsons\26.2\datapack.json` — verified `damage_type` registry present but tag membership absent (only `{elements, stable, tags}` booleans) — HIGH
- `D:\ender\temp\jsons\26.2\items.json` — verified per-item data components present, NO tag field; PIG_FOOD membership not derivable — HIGH
- `D:\ender\tools\gen_item_food.go` — confirmed the generator pattern to mirror for `gen_tags.go`; food properties already extracted — HIGH
- `D:\ender\level\attribute\defaults.go` — verified cow/sheep/chicken/pig/zombie/skeleton/spider attribute suppliers EXIST (hand-ported from jar `ldc2_w` constants); wolf ABSENT — HIGH
- `D:\ender\.planning\milestones\v4-phases\24-vanilla-mobs-as-plugins\deferred-goals.md` — the cited jar classes + exact missing subsystems v5 builds (FloatGoal/PanicGoal/BreedGoal/TemptGoal/FollowParentGoal) — HIGH (project ground truth)
- `D:\ender\.planning\PROJECT.md` — v5 milestone scope, 1:1 mandate, CGO=0 constraint, v4 plugin/regionization state — HIGH (project ground truth)
- `D:\ender\CLAUDE.md` — the absolute 1:1 jar-port mandate (authoritative source = jar bytecode, optimization-only deviation) — HIGH (project ground truth)

---
*Stack research for: v5 Mob Behaviors & Living-Entity Subsystems (Minecraft 26.2 Go server)*
*Researched: 2026-06-29*
