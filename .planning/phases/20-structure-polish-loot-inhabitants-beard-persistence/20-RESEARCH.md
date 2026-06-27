# Phase 20: Structure polish — loot, inhabitants, beard, persistence - Research

**Researched:** 2026-06-26
**Domain:** Minecraft 26.2 (proto 776) worldgen structure finishing — loot evaluator, structure-mob spawning, terrain-beard density contribution, StructureStart NBT persistence. Go port (`D:\ender`).
**Confidence:** HIGH (every algorithm decompiled from `temp/cache/26.2-inner.jar`; the 5 target loot tables + block tables read from the embedded datagen JSON)

## Summary

Phase 20 finishes the v2 structures. The keystone is **STRUCT-POLISH-01 — a single shared loot evaluator** that both block drops (GAMEPLAY-06, currently the hardcoded `blockDropTable` map in `server/block_drop.go`) and structure/dungeon chests call. The evaluator is small and well-bounded: the 5 target chest groups (desert/jungle temples, igloo, mineshaft, dungeon, village) use only **3 functions** (`set_count`, `enchant_randomly`, `enchant_with_levels`), **1 number-provider** (`uniform`, plus the implicit `constant` from a bare float), and **2 entry types** (`item`, `empty`). Block tables add `alternatives` + `match_tool`/`survives_explosion` conditions + `apply_bonus`/`explosion_decay` functions. The determinism contract is exact and decompiled: `LootPool.addRandomItems` rolls `rolls.getInt()` times; each roll builds the eligible-entry list, sums weights, draws `rng.nextInt(totalWeight)`, and subtracts weights to pick. The RNG is a **`LegacyRandomSource` (java.util.Random LCG)** seeded from the chest's `lootTableSeed`, which `StructurePiece.createChest` derives via `random.nextLong()` at gen time. Vanilla rolls **lazily on first open** (`unpackLootTable`); the chest persists `(LootTable id, LootTableSeed)` in its block-entity NBT.

The other three reqs are narrower. **STRUCT-POLISH-02 (entities):** swamp-hut witch+cat and stronghold silverfish are hardcoded in `postProcess` (the silverfish is actually a **SPAWNER block**, not a live entity); village villagers/cats ride the jigsaw template `entityInfoList` (entity NBT baked into the `.nbt` templates). The hard part is the seam — worldgen runs OFF the tick (`world.Worker`), but the entity store is **tick-owned** (`server/entity_store.go`), so spawned mobs need a new channel on `ChunkResult` (which today carries only `Chunk`). **STRUCT-POLISH-03 (beard):** only **village (`beard_thin`)** and **stronghold (`bury`)** have `terrain_adaptation`; temples/igloo/mineshaft/swamp-hut are `NONE`. The beard is a density contribution (`Beardifier.compute`) that must run during the **FILL pass**, but Sulfur places structures during **Decorate (after fill)** — so the structure STARTS must be computed before `fillFromNoise`, an ordering change. **STRUCT-POLISH-04 (persistence):** `StructureStart.createTag` writes a flat `{id, ChunkX, ChunkZ, references, Children}` compound into the chunk's `structures.Starts` NBT; Sulfur currently recomputes-on-demand (`world/structure/cache.go`) and persists nothing.

**Primary recommendation:** Build the loot evaluator FIRST as a standalone pure package (`level/loot` or `world/loot`), wire GAMEPLAY-06's `block_drop.go` onto it (replacing the map) AND the chest path onto it — that is the shared evaluator. Then do entities (new ChunkResult seam), then persistence (cache↔region NBT seam), then beard (the one ordering-sensitive port) last.

## User Constraints

No CONTEXT.md exists for this phase (research is standalone / pre-discuss). The binding constraints come from `CLAUDE.md` + `REQUIREMENTS.md`:

### Locked Decisions (from CLAUDE.md + REQUIREMENTS.md)
- **1:1 vanilla port, ABSOLUTE.** Every loot/spawn/beard algorithm is a literal method-for-method copy of `temp/cache/26.2-inner.jar` (`javap -c -p`). Mirror RNG draw order, float casts, weight-subtraction selection EXACTLY — loot must reproduce vanilla chest contents per seed. Re-express in idiomatic Go, no GPL paste, cite class+method. Only optimization that provably preserves observable gameplay is permitted.
- **Embed the data the jar ships** (`//go:embed`). Loot-table JSONs are already extracted under `temp/cache/26.2-datagen/.../loot_table/` — embed them (or a curated subset), do NOT hand-transcribe.
- **`CGO_ENABLED=0` stays clean** — pure-Go static binary. No new native deps.
- **Shared evaluator (STRUCT-POLISH-01 ⟷ GAMEPLAY-06):** the loot engine must be the SINGLE evaluator that both block drops and chest loot call. `server/block_drop.go` Assumption A1 already flags its map as "superseded by STRUCT-POLISH-01's loot evaluator."

### Claude's Discretion
- Package placement of the loot evaluator (`level/loot`, `world/loot`, or `server/loot`) — pick to avoid import cycles (block drops live in `server`, chest fill is data-driven from worldgen-placed block entities; the evaluator should be importable by both).
- Whether chests roll at gen-time (write items into chest NBT) or lazily on first open. **Vanilla rolls lazily** — recommend matching vanilla (store table+seed, roll in the container-open path).
- Which subset of loot JSON to embed (all ~1200 block tables + the chest tables, vs only the needed set).

### Deferred Ideas (OUT OF SCOPE)
- Trade-rebalance datapack loot variants (the `datapacks/trade_rebalance/` copies) — vanilla default tables only.
- `apply_bonus`/fortune/silk-touch tool semantics are needed for FAITHFUL block drops but are not required by the 5 chest tables; scope them with block drops, not chests.
- Non-target structure loot (ancient city, bastion, shipwreck, trial chambers, etc.) — those structures are not generated by Sulfur.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| STRUCT-POLISH-01 | Loot tables (shared evaluator: block drops + 5 chest groups) | Subsystem 1 below — `LootTable`/`LootPool`/functions/conditions/providers all decompiled; function inventory scoped to the actual tables; the `block_drop.go` + `createChest` seams identified. |
| STRUCT-POLISH-02 | Structure inhabitants (villagers/witch/cat/silverfish) | Subsystem 2 — per-piece spawn mechanism decompiled (hardcoded vs jigsaw-template vs SPAWNER); the off-tick→tick `ChunkResult` seam identified. |
| STRUCT-POLISH-03 | `afterPlace` terrain-beard | Subsystem 3 — `Beardifier.forStructuresInChunk` + `Beardifier.compute` + `getBeardContribution`/`getBuryContribution` decompiled; only village+stronghold apply; the FILL-vs-Decorate ordering issue surfaced. |
| STRUCT-POLISH-04 | Structure-start NBT persistence | Subsystem 4 — `StructureStart.createTag`/`loadStaticStart` NBT keys decompiled; the `cache.go` ↔ region NBT seam identified. |

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Loot-table parse + evaluate (roll items) | Pure data/eval package (`level/loot` or `world/loot`) | — | Pure over (table, seed, luck); no tick state, no I/O. Must be importable by both `server` (block drops) and the chest-open path. |
| Block-break drops | `server` (tick-owned) | loot pkg | `spawnBlockDrop` already runs on the tick; swap its map for an `evaluator.Roll(blockTable, seed, ctx)` call. |
| Chest fill (lazy roll on open) | `server` (container-open path) | loot pkg + `level` (block-entity NBT) | The chest stores table+seed at gen; rolling happens when a player opens it (tick-side container path). |
| Chest block-entity write at gen | `world/structure` | `level` (BlockEntity NBT) | `createChest` already writes the chest block; extend to draw `nextLong()` + emit the BE NBT `{LootTable, LootTableSeed}`. |
| Structure mob spawn | `world/structure` (decides WHAT+WHERE, off-tick) | `server` entity store (tick-owned, does the add) | Worldgen is off-tick + immutable; the live entity add is tick-owned → needs a `ChunkResult.Spawns` handoff. |
| SPAWNER block (silverfish) | `world/structure` → `level` BlockEntity | — | Stronghold silverfish is a `MobSpawner` block-entity, NOT a live entity — pure block placement, no entity-store seam. |
| Terrain beard (density bump) | `world/levelgen` (fill pass) | `world/structure` (start geometry) | Beard is a `final_density` contribution sampled during `fillFromNoise`; needs starts computed pre-fill. |
| StructureStart persistence | `world/structure` cache ↔ `save/region` | `nbt`/`level` | The cache memoizes starts; persistence writes them to the chunk's `structures` NBT compound. |

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib `encoding/json` | 1.26.1 | Parse the embedded loot-table JSON into Go structs | Loot tables ship as JSON; stdlib is sufficient (no schema codecs needed). `[VERIFIED: data already JSON in temp/cache/26.2-datagen]` |
| `//go:embed` | 1.26.1 | Embed the loot-table JSONs into the binary | CLAUDE.md mandate; already the pattern (`world/levelgen/data/embed.go`, `server/registrydata/embed.go`). `[VERIFIED: grep go:embed]` |
| go-mc fork `nbt` / `dynbt` | in-repo | Chest block-entity NBT (`LootTable`/`LootTableSeed`) + StructureStart NBT compounds | Already used for `level/component/containerloot_gen.go` + `level.BlockEntity.Data`. `[VERIFIED: level/chunk.go BlockEntity.Data; component/blockentitydata_gen.go]` |
| go-mc fork `save`/`region` | in-repo | Persist StructureStart NBT into region files | The anvil region IO already exists (`save/region/`). `[VERIFIED: ls save/region]` |
| `world/levelgen` `LegacyRandomSource` | in-repo | The loot RNG (java.util.Random LCG seeded by lootTableSeed) | `RandomSource.create(long)` = `LegacyRandomSource` (decompiled). Sulfur already ports this (`world/levelgen/random.go`). `[VERIFIED javap: RandomSource.create → new LegacyRandomSource]` |

### Supporting
| Item | Purpose | When to Use |
|------|---------|-------------|
| `data/item` registry | Map loot `name` (`minecraft:diamond`) → item id for the rolled `ItemStack` | Every `item` entry. `[VERIFIED: data/item/item.go has 64 matches for the loot items]` |
| `data/entity` registry | Witch/Cat/Villager/Silverfish entity ids for spawns | STRUCT-POLISH-02. `[VERIFIED: grep — Cat/Silverfish/Villager/Witch all present]` |
| `data/registryid/loot*.go` | The generated loot type-id lists (function/condition/number-provider/entry/score/nbt) | Validate parsed type strings; error loudly on an unrecognized type (mirrors the Phase-11 `feature` parser discipline). `[VERIFIED: ls data/registryid]` |
| `level/component.ContainerLoot` | The `minecraft:container_loot` component (already generated) | The 26.2 chest stores loot as a component on the block entity; this component already round-trips. `[VERIFIED: level/component/containerloot_gen.go]` |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Lazy roll on open (vanilla) | Roll at gen-time, bake items into chest NBT | Gen-time changes the RNG draw timing vs vanilla and bloats chunk NBT; lazy matches vanilla exactly and is the recommendation. |
| `encoding/json` into structs | A datafixers-style codec | Overkill; the JSON is flat and the type set is tiny (3 functions, 1 provider). Stdlib + a small dispatch registry is faithful and simpler. |
| Embed all ~1200 block tables | Embed only the needed set | Block drops (GAMEPLAY-06) eventually want all block tables; embed the whole `loot_table/` tree once — it's small JSON and avoids a curation step. |

**Installation:** No new deps. `CGO_ENABLED=0 go build ./...` stays clean.

**Version verification:** No external packages added — nothing to `npm/go view`. All data is jar-derived and already vendored under `temp/cache/26.2-datagen`.

## Subsystem Breakdown

### Subsystem 1 — Loot evaluator (STRUCT-POLISH-01) — KEYSTONE, LARGE net-new port

**Vanilla classes (all `net.minecraft.world.level.storage.loot.*`, decompiled):**

| Class.method | Behavior (jar-exact) |
|--------------|----------------------|
| `LootTable.getRandomItems(LootParams, long seed, Consumer)` | Builds a `LootContext.Builder`, calls `withOptionalRandomSeed(seed)`, then `getRandomItemsRaw`. **This is the per-seed entry point.** `[VERIFIED javap]` |
| `LootContext$Builder.withOptionalRandomSeed(long)` | `if (seed != 0L) random = RandomSource.create(seed)`. `RandomSource.create(long)` = **`new LegacyRandomSource(seed)`** (java.util.Random LCG). `[VERIFIED javap]` |
| `LootTable.getRandomItemsRaw(LootContext, Consumer)` | Loops `pools`, calls `pool.addRandomItems(consumer, ctx)` for each. (Plus an infinite-loop visited-set guard + table-level `compositeFunction` — the chest tables have no table-level functions, so the composite is identity.) `[VERIFIED javap]` |
| `LootPool.addRandomItems(Consumer, LootContext)` | `if (!compositeCondition.test(ctx)) return;` then `int rolls = rolls.getInt(ctx) + Mth.floor(bonusRolls.getFloat(ctx) * ctx.getLuck());` then `for i in 0..rolls: addRandomItem(consumer, ctx)`. **bonusRolls defaults to ConstantValue.exactly(0)** (decompiled in the codec default), and chest tables set no luck → the bonus term is 0. `[VERIFIED javap]` |
| `LootPool.addRandomItem(Consumer, LootContext)` | RNG = `ctx.getRandom()`. Build eligible list: for each entry, `entry.expand(ctx, e -> { int w = e.getWeight(luck); if (w>0){ list.add(e); total.add(w);} })`. If total==0 or list empty → return. If list.size()==1 → that entry's `createItemStack`. Else `int r = rng.nextInt(total); for e in list: r -= e.getWeight(luck); if (r<0){ e.createItemStack(...); return; }`. **The weight-subtraction selection + the `nextInt(total)` draw is the determinism core.** `[VERIFIED javap]` |
| `LootPoolSingletonContainer.expand` | An `item`/`empty` entry expands to itself when its conditions pass (the chest entries have no per-entry conditions). `[ASSUMED from structure — singleton expand is trivial; verify if a per-entry condition appears]` |
| `LootItem.createItemStack` | Builds the `ItemStack(item, 1)`, runs the entry's `functions` (the `compositeFunction`) over it via `LootItemFunction.decorate`. `[CITED: LootItem / LootPoolSingletonContainer]` |

**Number providers (the only one used: `uniform`):**
- `UniformGenerator.getInt(ctx)` = `Mth.nextInt(rng, min.getInt(ctx), max.getInt(ctx))`. `Mth.nextInt(rng,min,max)` = `min >= max ? min : min + rng.nextInt(max-min+1)`. **INCLUSIVE on both ends.** `getFloat` = `Mth.nextFloat(rng,min,max)`. `[VERIFIED javap UniformGenerator + Mth.nextInt]`
- `ConstantValue` (implicit when `rolls`/`count` is a bare float like `4.0`): `getInt` = `Mth.floor(value)`, `getFloat` = `value`. `[CITED]`

**Functions (the only 3 used by the 5 chest groups):**
- `SetItemCountFunction.run` = `stack.setCount((add ? stack.getCount() : 0) + count.getInt(ctx))`. Chest tables use non-add form → `setCount(count.getInt(ctx))`. Draws via the count NumberProvider. `[VERIFIED javap]`
- `EnchantRandomlyFunction.run` = picks one compatible enchantment from `options` (a `#tag` holderset, e.g. `#minecraft:on_random_loot`) via `Util.getRandomSafe(list, rng)`, applies a random level. **Needs the enchantment registry + the `on_random_loot` tag** (already in `server/registrydata/tags/enchantment/on_random_loot.json`). `[VERIFIED javap + tag present]`
- `EnchantWithLevelsFunction.run` = enchants with a fixed level budget (`levels:30` for the jungle-temple book) — ports `EnchantmentHelper.enchantItem`. **Most complex function; used by exactly ONE entry (jungle_temple book).** `[VERIFIED javap signature]`

**Conditions:** the 5 chest groups use **no per-entry conditions** (the one `location_check` counted by the aggregate grep is in a non-chest sibling). So for chests, conditions are a NO-OP. Block tables need `match_tool` (silk-touch/enchantment predicate) + `survives_explosion` (`LootItemRandomChanceCondition`-family) — scope those with block drops.

**The chest-fill seam (decompiled, the determinism keystone):**
`StructurePiece.createChest(ServerLevelAccessor, box, RandomSource, BlockPos, ResourceKey<LootTable>, BlockState)`:
1. guard `box.isInside(pos)`, skip if already a chest;
2. `setBlock(pos, reorient(chest), 2)`;
3. get the `ChestBlockEntity`; call `chestBE.setLootTable(key, random.nextLong())`. **The `random.nextLong()` draw on the PIECE's RNG is what makes chest contents deterministic per (worldseed, chunk).** `[VERIFIED javap createChest]`
- The chest stores `lootTable` (id) + `lootTableSeed` (long) in its block-entity NBT and rolls **lazily**: `RandomizableContainerBlockEntity.getItem/setItem/...` each call `unpackLootTable(player)`, which (when `lootTable != null`) builds `LootParams` + `getRandomItems(params, lootTableSeed)` and fills the container, then clears the table. `[VERIFIED javap: getItem→unpackLootTable; createChest→setLootTable(key, nextLong())]`

**Sulfur integration seam:**
- `world/structure/piece.go:721 createChest` already records `LootChest{X,Y,Z,LootTable}` but writes only the chest block (no seed, no BE NBT). **EXTEND it:** draw `rng.nextLong()` (the piece's `RandomSource` — already threaded into `PostProcess`), and emit a chest `level.BlockEntity` carrying `{LootTable, LootTableSeed}` NBT into the chunk. (The existing `LootChest` record becomes the carrier or is replaced by a real BE.)
- `server/block_drop.go` `blockDropFor`/`blockDropTable`: **REPLACE** the map with `evaluator.Roll(blockLootTable(blockID), gameRng, blockCtx)`. The block table for a block is `minecraft:blocks/<name>` — embed those JSONs too.
- New package (recommend `level/loot` — importable by both `server` and the chest-open path without cycle): the parsed `LootTable` model + the `LootContext`/RNG + the function/condition/provider dispatch + `Roll(table, seed, ctx) []ItemStack`.

**Complexity:** LARGE. ~5-7 files: JSON model+parser, the roll engine (pool/entry/weighted-select), 3 functions, 1-2 providers, the enchantment-apply helper (the heaviest piece), the block-drop rewire, the chest-BE write. The enchant functions are the only hard part; `set_count` + `uniform` + weighted-select is straightforward.

### Subsystem 2 — Structure entities (STRUCT-POLISH-02) — MEDIUM (seam-heavy, port-light)

**Per-piece spawn mechanism (decompiled):**

| Structure | Mob(s) | Mechanism (jar-exact) | Sulfur seam |
|-----------|--------|----------------------|-------------|
| Swamp hut | **Witch** + **Cat** | Hardcoded in `SwampHutPiece.postProcess` tail: `spawnedWitch` guard → `EntityType.WITCH.create(level, STRUCTURE)` → `setPersistenceRequired()` → `snapTo(x,y,z)` → `finalizeSpawn(...)`; then `spawnCat(level, box)` → `EntityType.CAT...`. **One-shot guards (`spawnedWitch`/`spawnedCat`) persisted in piece NBT.** `[VERIFIED javap SwampHutPiece]` | `world/structure/swamp_hut.go` `PostProcess` already has the geometry + a "WITCH+CAT deferred v3" comment at the tail — fill it: emit a spawn request, NOT a live entity. |
| Stronghold | **Silverfish** | **NOT a live entity** — `StrongholdPieces$...` places a `Blocks.SPAWNER` block + sets the `SpawnerBlockEntity.setEntityId(SILVERFISH, rng)`. `hasPlacedSpawner` guard. `[VERIFIED javap: getstatic Blocks.SPAWNER + SpawnerBlockEntity.setEntityId SILVERFISH]` | Pure block + block-entity placement (`MobSpawnerEntity` exists in `level/block/blockentities.go`). **No entity-store seam needed** — it's a block. |
| Village | **Villagers**, **Cat** | Ride the jigsaw template `entityInfoList`: `StructureTemplate.placeEntities` reads entity NBT baked into the `.nbt` templates and `createEntityIgnoreException` spawns them. `[VERIFIED javap: StructureTemplate.placeEntities / entityInfoList / ENTITY_TAG_NBT]` | Sulfur's village uses extracted-offline template geometry (`igloo_data.go` pattern). The village template's entity list must be extracted offline + emitted as spawn requests at place time (`world/structure/village.go`/`template.go`). |

**The critical seam (off-tick → tick):** worldgen runs on `world.Worker` (off-tick, immutable `ChunkResult{Pos, Chunk, Err}` — `world/worker.go:22`). The entity store is **tick-owned** (`server/entity_store.go`, plain maps, TICK-05). A structure piece deciding "spawn a witch at (x,y,z)" off-tick CANNOT touch the store. **Add a `Spawns []SpawnRequest` field to `ChunkResult`** (a SpawnRequest = entity type id + pos + the one-shot finalize data); the tick drains it where it already drains `ChunkResult.Chunk`, allocates an entity id (`idAlloc.AllocID()`), builds the `*server.Entity`, and `entities.add(e)` — then GAMEPLAY-01's tracker broadcasts AddEntity for free (same path as `spawnBlockDrop`). `[VERIFIED: ChunkResult struct + entity_store.add + the block_drop tracker-rides-store pattern]`

**`finalizeSpawn`** (mob attribute/variant init, e.g. cat variant, witch held item) is a v3 stub risk — port the call but the attribute subsystem may need a cited-constant stub (per CLAUDE.md "stub behind a cited constant equal to the vanilla default").

**Complexity:** MEDIUM. The spawn DECISIONS are tiny (a few `EntityType.create` calls); the work is the `ChunkResult.Spawns` seam + the village template entity extraction + plumbing the one-shot guards into the (now-persisted, see Subsystem 4) start NBT so a reload doesn't double-spawn.

### Subsystem 3 — afterPlace / Beardifier (STRUCT-POLISH-03) — MEDIUM-LARGE (one ordering hazard)

**Scope is small:** only **village (`beard_thin`)** and **stronghold (`bury`)** declare `terrain_adaptation`. Temples, igloo, mineshaft, swamp-hut are `NONE` → no beard. `[VERIFIED: grep terrain_adaptation across worldgen/structure/*.json]`

**Two distinct mechanisms — do NOT conflate:**
1. **`Structure.afterPlace`** (post-placement block fixups: `BURY` fills the structure's footprint solid, leg/foundation cleanup) — runs AFTER pieces place. Signature: `afterPlace(WorldGenLevel, StructureManager, ChunkGenerator, RandomSource, BoundingBox, ChunkPos, PiecesContainer)`. `[VERIFIED javap signature]`
2. **`Beardifier`** (the density bump that raises/lowers terrain UNDER the structure at FILL time): `DensityFunctions$BeardifierMarker` is a marker in the noise router that the `NoiseChunk` replaces per-chunk with a `Beardifier.forStructuresInChunk(structureManager, chunkPos)` instance. `[VERIFIED javap: Beardifier in net.minecraft.world.level.levelgen]`

**`Beardifier.forStructuresInChunk` (decompiled):** gathers `structureManager.startsForStructure(chunkPos, hasTerrainAdaptation)`; for each start's pieces within 12 of the chunk, records a `Rigid{box, groundLevelDelta}` (and jigsaw junctions for pools). If none → `EMPTY` (returns density 0). The affected box is `union.inflatedBy(24)`. `[VERIFIED javap forStructuresInChunk]`

**`Beardifier.compute(ctx)` (decompiled):** if outside `affectedBox` → 0. Else sum over pieces:
- `getBuryContribution(dx,dy,dz)` = `Mth.clampedMap(Mth.length(dx,dy,dz), 0, 6, 1, 0)` (BURY/ENCAPSULATE). `[VERIFIED javap getBuryContribution]`
- `getBeardContribution(dx,dy,dz,delta)` (BEARD_THIN/BEARD_BOX): a kernel-range gate (`|coord+12|` in kernel) then `magnitude = -dy' / sqrt(lengthSquared+...)` style falloff (`getBeardContribution`/`computeBeardContribution`). `[VERIFIED javap getBeardContribution structure]`
The result adds into `final_density` so terrain rises to meet a floating structure / digs out around a buried one.

**Sulfur integration seam — THE HAZARD (Pitfall below):** Sulfur places structures during **`Decorate` (after `fillFromNoise`)** — `world/noisegen.go:346 placeStructures`. But the beard must influence `final_density` DURING fill. So:
- The structure STARTS for the chunk (+ its beard-relevant neighbors, the ±12 window) must be computed BEFORE `noisechunk.NewNoiseChunk`/`fillFromNoise`. The starts are pure geometry + already singleflight-memoized (`cache.ComputeStarts`), so computing them earlier is cheap and order-independent.
- Inject a `Beardifier`-backed density contribution into the router's `final_density` for the chunk (the `BeardifierMarker` substitution vanilla does in `NoiseChunk` ctor). Sulfur's `NoiseChunk` (`world/levelgen/noisechunk/`) drives per-marker interpolation already (09-09 `mapAll`) — the beard is a NON-interpolated additive term applied per-block in the fill loop, NOT a router-graph node, so it slots in at the `final_density` summation, not the interpolator.
- `afterPlace` (BURY block fill) runs in the PLACE pass alongside `PlaceStructures`.

**Complexity:** MEDIUM-LARGE. The `compute`/`getBuryContribution`/`getBeardContribution` math is a clean port (~1 file). The seam — moving START computation ahead of fill + threading the additive term into the fill loop — is the real work and touches the generator ordering (`noisegen.go` + `noisechunk`). Because only 2 structures beard, regressions are localized.

### Subsystem 4 — StructureStart NBT persistence (STRUCT-POLISH-04) — SMALL-MEDIUM

**NBT layout (decompiled `StructureStart.createTag`):** a flat compound:
- `id` : String — the structure id (`minecraft:village_plains`), or `"INVALID"` if the start has no pieces;
- `ChunkX` : Int, `ChunkZ` : Int — the owning chunk;
- `references` : Int — the reference count;
- `Children` : the `PiecesContainer.save()` list (each piece's serialized NBT).
`[VERIFIED javap StructureStart.createTag]`

**Read side (`loadStaticStart`):** reads `id`/`ChunkX`/`ChunkZ`/`references`/`Children`; `"INVALID"` id → empty start. `[VERIFIED javap loadStaticStart keys]`

**Where it lives in chunk NBT:** the chunk-level `structures` compound has `Starts` (this chunk's owned starts, keyed by structure id) + `References` (the long-array of owner chunk keys reaching in). Vanilla's `StructureCheck`/serialization reads/writes it.

**Sulfur seam:** `world/structure/cache.go` currently memoizes `starts` + `references` as `xsync.Map`s and **persists nothing** (recompute-on-demand, pure over (seed,pos)). The chunk already carries a `BlockEntity` list + region IO exists (`save/region/`). **Two-way seam:**
- WRITE: when a chunk is saved, serialize its cached `[]*StructureStart` (the `Pieces` need a per-piece `save`/`load` — each Sulfur piece type must gain NBT round-trip, the bulk of the work) into the chunk's `structures.Starts` compound + the `References` long-array.
- READ: on chunk load, if `structures.Starts` is present, populate the cache from NBT INSTEAD of recomputing — but **coherence (Pitfall below): the loaded starts MUST equal what recompute would produce**, since recompute is pure. The safe design: persist as an optimization, and on load either trust the NBT or recompute-and-assert in tests.

**Complexity:** SMALL-MEDIUM. The compound is trivial; the work is giving every Sulfur piece type (`SwampHutPiece`, mineshaft/stronghold/desert/jungle/igloo/jigsaw pieces) a `Save()/Load()` NBT method (`Children`), and wiring the cache↔region read/write. Because starts are pure-recomputable, persistence is a true optimization — a missing/garbled tag can always fall back to recompute (a robustness win).

## Loot Function/Condition Inventory

**Aggregated across the 5 target chest groups** (`desert_pyramid`, `jungle_temple` + `jungle_temple_dispenser`, `igloo_chest`, `abandoned_mineshaft`, `simple_dungeon`, all 16 `village/*`). `[VERIFIED: grep -oE over the actual JSON]`

| Element | Type | Count in target tables | MUST-PORT? | Notes |
|---------|------|------------------------|------------|-------|
| **Function** `set_count` | function | 148 | ✅ MUST | The workhorse. Non-add form: `setCount(count.getInt())`. |
| **Function** `enchant_randomly` | function | 3 | ✅ MUST | Book entries; needs enchantment registry + `#on_random_loot` tag (present). |
| **Function** `enchant_with_levels` | function | 1 | ✅ MUST | Exactly ONE entry (jungle_temple book, `levels:30`). Heaviest; ports `EnchantmentHelper.enchantItem`. |
| **Condition** `location_check` | condition | 0 in chests* | ❌ skip | The 1 aggregate hit is a non-chest sibling; the 5 chest groups use NO per-entry conditions. |
| **NumberProvider** `uniform` | number | 161 | ✅ MUST | `Mth.nextInt(rng,min,max)`, inclusive. Used for `rolls` + `set_count`. |
| **NumberProvider** `constant` | number | implicit | ✅ MUST | Bare float `rolls`/`count` (e.g. `4.0`) decode to `ConstantValue` → `Mth.floor`. |
| **Entry** `item` | entry | 235 | ✅ MUST | The only real entry. `weight` defaults to 1 when absent. |
| **Entry** `empty` | entry | 12 | ✅ MUST | A weighted no-drop slot — participates in weighted select, drops nothing. |
| `bonus_rolls` | pool field | 0 | ❌ (default 0) | No target table sets it; the `ConstantValue.exactly(0)` codec default applies. |
| `luck`/`quality` | entry field | 0 | ❌ | No target table uses luck-scaled weight. `getWeight(luck)` = `weight` (quality 0). |

**Block-table delta (for the GAMEPLAY-06 side of the shared evaluator — NOT in chests):**
| Element | Used by block tables | MUST-PORT for faithful drops? |
|---------|----------------------|-------------------------------|
| Entry `alternatives` | yes (silk-touch-or-drop) | ✅ — first-passing child wins. |
| Condition `match_tool` | yes (silk-touch / tool-tier gate) | ✅ — enchantment/tool predicate. |
| Condition `survives_explosion` | yes | ✅ — `LootItemRandomChanceCondition` family (TNT decay). |
| Function `apply_bonus` | yes (fortune `ore_drops`) | ✅ — fortune multiplier formula. |
| Function `explosion_decay` | yes | ✅ — explosion drop-loss. |

**Scoping recommendation:** the chest evaluator needs only the 7 ✅-MUST rows above. The block-table delta (5 more) is needed for FAITHFUL block drops; if Phase 20 keeps block drops at "drops something" parity (the existing v1 behavior), defer the block delta — but the shared evaluator should be SHAPED to accept conditions/alternatives so adding them later is data-only. Recommend porting the block delta too, since GAMEPLAY-06's map is explicitly a placeholder for this evaluator.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Loot RNG | A new PRNG | `world/levelgen` `LegacyRandomSource` | `RandomSource.create(seed)` IS `LegacyRandomSource` — per-seed reproduction REQUIRES the exact LCG + draw order. `[VERIFIED javap]` |
| Loot JSON parse | A hand-written tokenizer | stdlib `encoding/json` + the embedded files | Data already JSON; type set is tiny. |
| Chest block entity NBT | A bespoke serializer | `level.BlockEntity.Data` (dynbt) + `component.ContainerLoot` | The chunk BlockEntity list + the loot component already round-trip. `[VERIFIED]` |
| Entity broadcast for spawned mobs | New tracker code | `entityStore.add()` → existing tracker | Same store-add path `spawnBlockDrop` rides; tracker broadcasts AddEntity for free. `[VERIFIED block_drop.go]` |
| Silverfish "entity" | An entity-store spawn | A `MobSpawner` block-entity | Vanilla places a SPAWNER block, not a live silverfish. `[VERIFIED javap StrongholdPieces]` |
| StructureStart geometry | Re-deriving piece trees on load | The pure cache (recompute fallback) | Starts are pure over (seed,pos) — NBT is an optimization, recompute is always correct. `[VERIFIED cache.go]` |
| Beard as a router node | A new density-graph type | An additive per-block term in the fill loop | Vanilla's `BeardifierMarker` is replaced per-chunk in `NoiseChunk`, not a graph node. `[VERIFIED javap]` |

**Key insight:** every one of the four reqs has an existing Sulfur seam (the `LootChest` record, the `ChunkResult` handoff, the `cache.go` memoization, the `NoiseChunk` final_density). This is a WIRING-heavy phase with one genuinely large net-new port (the loot evaluator) and one ordering hazard (the beard).

## Runtime State Inventory

Not a rename/refactor phase — greenfield additions to an existing pipeline. Two persistence-coherence notes belong here:
- **Stored data:** structure-start NBT (STRUCT-POLISH-04) becomes a NEW persisted form in region files. Existing worlds have NO `structures.Starts` tag → the read path MUST tolerate absence (fall back to recompute). No migration needed (recompute is the source of truth).
- **Stored data:** chest block-entity NBT now carries `LootTable`+`LootTableSeed`. A chest persisted by an earlier Sulfur build has neither → reads as an empty chest (acceptable; only newly-generated structures get loot).

## Common Pitfalls

### Pitfall 1: Loot RNG seed / draw order diverges from vanilla
**What goes wrong:** chest contents don't match a real server for the same world seed.
**Why:** the seed comes from the PIECE's `RandomSource.nextLong()` at `createChest` time, and the roll uses a `LegacyRandomSource(lootTableSeed)` with an EXACT draw order: per pool, `rolls.getInt()` draws first, then per roll the weighted `nextInt(total)`, then each entry's functions draw their own `count`/enchant values IN ENTRY ORDER. Any reordering (e.g. computing all weights then rolling) breaks it.
**Avoid:** port `addRandomItem` literally — build list, sum, `nextInt(total)`, subtract-to-select; run functions in JSON order. Mirror `withOptionalRandomSeed`'s `seed != 0` guard. **Warning sign:** a golden test against a vanilla-captured chest at a known seed fails.

### Pitfall 2: Chest rolled at gen-time instead of lazily
**What goes wrong:** rolling during worldgen consumes RNG at the wrong moment and bloats chunk NBT; contents differ from vanilla (which rolls on first open).
**Avoid:** store `(LootTable, LootTableSeed)` in the chest BE; roll in the container-OPEN path (`unpackLootTable` analogue), not in `PostProcess`. **Warning sign:** chest NBT contains item lists at gen.

### Pitfall 3: Beard double-applies or applies after fill
**What goes wrong:** terrain doesn't adapt (beard ran after fill = no effect) OR adapts twice (computed per overlapping chunk without the affected-box gate).
**Avoid:** compute starts BEFORE `fillFromNoise`; gate `compute` on `affectedBox.isInside` (decompiled); the per-chunk `forStructuresInChunk` already scopes to this chunk's relevant pieces. **Warning sign:** villages float / strongholds aren't buried, or a moat appears around them.

### Pitfall 4: NBT-persistence vs recompute incoherence
**What goes wrong:** a loaded start disagrees with what recompute produces (e.g. a piece's RNG-derived field wasn't persisted), so REFERENCES/PLACE behave differently on reload.
**Avoid:** persist the FULL piece NBT (`Children`) including any RNG-derived geometry, OR treat NBT as a cache and recompute on any mismatch. Because starts are pure, a recompute-and-assert test is the coherence gate. **Warning sign:** a structure renders differently after a server restart.

### Pitfall 5: Spawning a live entity off the tick
**What goes wrong:** a structure piece calls `entityStore.add` from the worker goroutine → data race / TICK-05 violation.
**Avoid:** the worker only RECORDS spawn requests on `ChunkResult.Spawns`; the tick performs the `add`. **Warning sign:** `go test -race` flags `entity_store` under chunk load.

### Pitfall 6: Double-spawn on reload
**What goes wrong:** witch/cat/villager spawn again when a structure chunk reloads (the `spawnedWitch`/`spawnedCat`/`hasPlacedSpawner` one-shot guards weren't persisted).
**Avoid:** the one-shot guards live in the persisted piece NBT (Subsystem 4) — a reloaded piece reads `spawnedWitch=true` and skips. This couples STRUCT-POLISH-02 to -04. **Warning sign:** a swamp hut gains a second witch after a reload.

## Code Examples

### Weighted entry selection (the determinism core — port of `LootPool.addRandomItem`)
```go
// Source: javap net.minecraft.world.level.storage.loot.LootPool.addRandomItem
// rng is a LegacyRandomSource seeded by lootTableSeed (RandomSource.create(seed)).
func (p *LootPool) addRandomItem(rng levelgen.RandomSource, ctx *LootContext, emit func(ItemStack)) {
    var eligible []*entry
    total := 0
    for _, e := range p.entries {
        e.expand(ctx, func(en *entry) {
            w := en.weight // getWeight(luck): quality 0 → plain weight
            if w > 0 {
                eligible = append(eligible, en)
                total += w
            }
        })
    }
    if total == 0 || len(eligible) == 0 {
        return
    }
    if len(eligible) == 1 {
        eligible[0].createItemStack(rng, ctx, emit)
        return
    }
    r := rng.NextInt(total) // nextInt(total)
    for _, en := range eligible {
        r -= en.weight
        if r < 0 {
            en.createItemStack(rng, ctx, emit)
            return
        }
    }
}
```

### Roll count per pool (port of `LootPool.addRandomItems` head)
```go
// Source: javap LootPool.addRandomItems
// bonusRolls defaults to ConstantValue(0); chests set no luck → bonus term is 0.
rolls := p.rolls.GetInt(ctx) + mthFloor(p.bonusRolls.GetFloat(ctx)*ctx.Luck())
for i := 0; i < rolls; i++ {
    p.addRandomItem(rng, ctx, emit)
}
```

### Uniform provider (port of `UniformGenerator.getInt`)
```go
// Source: javap UniformGenerator.getInt → Mth.nextInt(rng, min, max) — INCLUSIVE both ends.
func (u Uniform) GetInt(ctx *LootContext) int {
    lo, hi := u.Min.GetInt(ctx), u.Max.GetInt(ctx)
    if lo >= hi {
        return lo
    }
    return lo + ctx.Random().NextInt(hi-lo+1)
}
```

## State of the Art

| Old (Sulfur v2 / GAMEPLAY-06) | New (Phase 20) | Impact |
|-------------------------------|----------------|--------|
| `blockDropTable` map (7 hardcoded blocks) | Shared loot evaluator over embedded block tables | Faithful per-block drops; the map is explicitly a placeholder. |
| Chests = block only, `LootChest` recorded, loot deferred | Chest BE with `LootTable`+`LootTableSeed`, lazy roll | Chests actually contain vanilla loot per seed. |
| Witch/cat/villager/silverfish DEFERRED in `postProcess` | Spawn requests via `ChunkResult.Spawns` (+ SPAWNER block for silverfish) | Structures have inhabitants. |
| No terrain adaptation (structures float/clip) | Beardifier density contribution (village+stronghold only) | Villages sit on terrain; strongholds bury. |
| Recompute-on-demand, persist nothing | StructureStart NBT in region (optimization, recompute fallback) | Starts survive reload without recompute; enables reload-safe one-shot spawn guards. |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | The 5 chest groups use NO per-entry `conditions` (the 1 aggregate `location_check` is a non-chest sibling). | Subsystem 1 / Inventory | LOW — if a village table has a condition, add the condition dispatch (already shaped for block tables). |
| A2 | `LootPoolSingletonContainer.expand` for `item`/`empty` is the trivial self-expand (no `dynamic`/`tag`/`loot_table` entries in the 5 groups). | Subsystem 1 | LOW — confirmed entry-type grep shows only `item`+`empty`. |
| A3 | Lazy-roll-on-open is the right choice for Sulfur (matches vanilla). | Architecture / Pitfall 2 | LOW — vanilla mechanism decompiled; gen-time would diverge. |
| A4 | The village template entity list (villagers/cat) can be extracted offline like the igloo geometry. | Subsystem 2 | MEDIUM — depends on whether Sulfur's village pieces carry template entity data; the extraction step may be net-new. |
| A5 | The beard additive term slots into `final_density` summation (not the per-marker interpolator). | Subsystem 3 | MEDIUM — `BeardifierMarker` is per-chunk-substituted; verify against Sulfur's `NoiseChunk.mapAll` placement during planning. |
| A6 | Persisting starts is a pure optimization (recompute is always a valid fallback). | Subsystem 4 / Pitfall 4 | LOW — `cache.go` already pure over (seed,pos). |

## Open Questions

1. **Loot evaluator package placement** — `level/loot` vs `world/loot` vs `server/loot`. Must be importable by `server` (block drops) AND by the chest-open path without an import cycle. Recommendation: `level/loot` (the lowest layer that both can import; it needs `data/item`, `data/registryid`, `world/levelgen` for the RNG — confirm no cycle with `world/levelgen` during planning).
2. **Block-drop faithfulness scope** — does Phase 20 port the full block-table delta (`alternatives`/`match_tool`/`apply_bonus`/`explosion_decay`) for fortune/silk-touch, or keep block drops at "drops something" and only do chests faithfully? Recommendation: port the delta (GAMEPLAY-06's map is explicitly a placeholder), but it's a scoping decision for discuss-phase.
3. **Village template entity extraction** — are the village `.nbt` templates already extracted with their entity lists, or is that a new offline extraction step (A4)?
4. **Chest-open container path** — does Sulfur have a chest-open → container-menu path today to hang `unpackLootTable` on, or does opening a generated chest need new wiring? (The inventory menu exists per ENT-04; the block-entity-container open path may be net-new.)

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `javap` (Zulu 25) | Offline jar decompile for the port | ✓ | Zulu 25 | — |
| Embedded loot JSON | Loot evaluator data | ✓ | `temp/cache/26.2-datagen/.../loot_table/` | — |
| go-mc fork `nbt`/`save`/`region` | Chest BE + StructureStart persistence | ✓ | in-repo | — |
| Enchantment registry + `on_random_loot` tag | `enchant_randomly`/`enchant_with_levels` | ✓ | `server/registrydata/.../enchantment/` | — |
| Go 1.26.1, CGO=0 | Build | ✓ | 1.26.1 | — |

**No missing dependencies.** Pure-Go, all data vendored.

## Validation Architecture

`workflow.nyquist_validation` is `true` → this section applies.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (+ `go test -race` in Docker `golang:1.26`, the standing CGO=0 gate) |
| Config file | none (idiomatic Go) |
| Quick run command | `go test ./level/loot/... ./world/structure/...` |
| Full suite command | Docker `-race` over `./server/... ./world/... ./level/... ./save/...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| STRUCT-POLISH-01 | A known table+seed rolls the EXACT vanilla item list | unit (golden) | `go test ./level/loot/ -run TestLootSeedReproduces` | ❌ Wave 0 |
| STRUCT-POLISH-01 | `set_count`/`uniform`/weighted-select draw order matches vanilla | unit | `go test ./level/loot/ -run TestPoolDrawOrder` | ❌ Wave 0 |
| STRUCT-POLISH-01 | Block drop uses the shared evaluator (stone→cobblestone via table) | unit | `go test ./server/ -run TestBlockDropViaLoot` | ❌ Wave 0 |
| STRUCT-POLISH-02 | Swamp hut emits witch+cat spawn requests; stronghold places a silverfish SPAWNER | unit | `go test ./world/structure/ -run TestSwampHutSpawns` | ❌ Wave 0 |
| STRUCT-POLISH-02 | Spawn requests reach the tick store; double-spawn guard holds on reload | unit + race | `go test ./server/ -run TestStructureSpawnSeam` | ❌ Wave 0 |
| STRUCT-POLISH-03 | Village beard raises terrain; stronghold buries; non-adapting structures unchanged | unit | `go test ./world/... -run TestBeardContribution` | ❌ Wave 0 |
| STRUCT-POLISH-04 | StructureStart round-trips NBT (createTag↔loadStaticStart); recompute-coherence | unit | `go test ./world/structure/ -run TestStartNBTRoundTrip` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./level/loot/... ./world/structure/...`
- **Per wave merge:** Docker `-race` over the touched trees (`./level/... ./world/... ./server/...`)
- **Phase gate:** full `-race` suite green + the worldgen capture-diff goldens still green (no encoder/wire file touched — this phase adds block entities + entities, which DO touch the chunk wire's BlockEntity list, so the capture-diff MAY need a re-seal: flag for planning).

### Wave 0 Gaps
- [ ] `level/loot/loot_test.go` — the seed-reproduces golden (capture a real vanilla chest at a known seed) — covers STRUCT-POLISH-01
- [ ] `world/structure/spawn_test.go` — per-piece spawn-request assertions — STRUCT-POLISH-02
- [ ] `world/structure/start_nbt_test.go` — createTag↔loadStaticStart round-trip + recompute coherence — STRUCT-POLISH-04
- [ ] Beard contribution test fixtures (village/stronghold density delta) — STRUCT-POLISH-03
- [ ] Golden loot vectors: capture vanilla chest contents for the 5 structures at a fixed seed (the ONLY way to prove per-seed reproduction — a self-consistent test cannot)

## Security Domain

`security_enforcement` absent → enabled. This phase has a narrow surface (no network/auth/crypto — that was Phase 18).

### Applicable ASVS Categories
| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V5 Input Validation | yes | Parsing embedded (trusted, vendored) loot JSON — validate type strings against `data/registryid/loot*`, error loudly on unknown (no silent-skip), bound `rolls`/`count` (a malformed huge `rolls` must not OOM). Region-NBT reads (untrusted on-disk) must tolerate absent/garbled `structures` tags → recompute fallback, never panic. |
| V6 Cryptography | no | No crypto in this phase. |
| V2/V3/V4 (auth/session/access) | no | No network surface. |

### Known Threat Patterns
| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Malformed/huge loot JSON or NBT `rolls` → resource exhaustion | DoS | Bound roll counts + entry counts; the data is vendored/trusted so the risk is a build-asset bug, but cap defensively. |
| Garbled region `structures` NBT (corrupt save / untrusted world) | Tampering | Tolerate + recompute (starts are pure) — never trust-and-crash; assert-on-mismatch only in tests. |
| Off-tick entity-store mutation | (data race, not security) | `ChunkResult.Spawns` handoff; `-race` gate. |

## Sources

### Primary (HIGH confidence)
- `temp/cache/26.2-inner.jar` via `javap -c -p` — `LootTable.getRandomItems/getRandomItemsRaw`, `LootPool.addRandomItems/addRandomItem`, `LootContext$Builder.withOptionalRandomSeed/create`, `UniformGenerator.getInt/getFloat`, `Mth.nextInt`, `SetItemCountFunction.run`, `EnchantRandomlyFunction.run`, `EnchantWithLevelsFunction`, `RandomSource.create`, `StructurePiece.createChest/createDispenser`, `RandomizableContainerBlockEntity` (setLootTable/unpackLootTable seam), `SwampHutPiece` (spawnedWitch/spawnedCat/postProcess), `StrongholdPieces` (SPAWNER+SILVERFISH), `StructureTemplate` (placeEntities/entityInfoList), `Structure.afterPlace`, `Beardifier.forStructuresInChunk/compute/getBuryContribution/getBeardContribution`, `TerrainAdjustment` (NONE/BURY/BEARD_THIN/BEARD_BOX/ENCAPSULATE), `StructureStart.createTag/loadStaticStart`.
- `temp/cache/26.2-datagen/generated/data/minecraft/loot_table/chests/{desert_pyramid,jungle_temple,jungle_temple_dispenser,igloo_chest,abandoned_mineshaft,simple_dungeon,village/*}.json` + `blocks/{stone,diamond_ore}.json` — the actual function/condition/provider/entry inventory.
- `temp/cache/26.2-datagen/.../worldgen/structure/{village_plains,stronghold,...}.json` — `terrain_adaptation` values.
- Sulfur source: `server/block_drop.go`, `world/structure/{cache,start,piece,swamp_hut}.go`, `world/noisegen.go`, `world/worker.go`, `server/entity_store.go`, `level/chunk.go`, `level/block/blockentities.go`, `level/component/containerloot_gen.go`, `data/registryid/lootfunctiontype.go`.

### Secondary
- `CLAUDE.md`, `.planning/REQUIREMENTS.md` (STRUCT-POLISH-01..04), `.planning/ROADMAP.md` (Phase 20), `.planning/STATE.md`, `.planning/config.json`.

## Metadata

**Confidence breakdown:**
- Loot engine + function inventory: HIGH — every class decompiled; the inventory is grepped from the actual JSON, not assumed.
- Loot RNG seed/draw order: HIGH — `createChest.nextLong` + `RandomSource.create`→`LegacyRandomSource` + `addRandomItem` selection all decompiled.
- Structure entities: HIGH on mechanism (decompiled per piece), MEDIUM on the village-template extraction effort (A4).
- Beard: HIGH on the `compute` math (decompiled), MEDIUM on the Sulfur fill-ordering integration (A5).
- StructureStart NBT: HIGH on the tag layout (decompiled), MEDIUM-SMALL effort (per-piece save/load is the bulk).

**Research date:** 2026-06-26
**Valid until:** stable (jar-pinned 26.2; no external moving deps) — re-verify only if the pinned jar version bumps.
