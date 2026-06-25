# v2 — Structure Generation (Minecraft 26.2 / protocol 776)

Research for porting the structure-generation subsystem 1:1 from the unobfuscated
server jar (`temp/cache/26.2-inner.jar`, read via `javap -c`) into Sulfur. Scope:
**dungeons, mineshafts, temples (desert pyramid + jungle temple), strongholds,
villages**. This feeds the v2 milestone requirements + roadmap.

Everything below is **jar-confirmed** unless marked `[INFERRED]`. Class citations are
the actual `net.minecraft.world.level.levelgen.structure.*` classes inspected.

---

## Standard Stack

The structure subsystem is **two cleanly separable concerns** the jar already splits:

1. **WHERE** a structure starts (per-chunk, deterministic, cheap) — `STRUCTURE_STARTS`.
2. **WHAT/HOW** it places blocks (per-chunk, piece-by-piece, expensive) — the
   `FEATURES` step (`applyBiomeDecoration`) reading the cached starts.

Port in this **strict easy→hard order**. Each tier reuses the prior tier's machinery,
so the sequence is also a dependency order, not just a difficulty ramp.

### Tier 0 — Dungeon (the simple mob-spawner room) — **NOT a Structure**

**CRITICAL SEQUENCING FACT (jar-confirmed):** The dungeon is
`net.minecraft.world.level.levelgen.feature.MonsterRoomFeature`, a **`Feature`**, not a
`Structure`. It has **no `StructureStart`, no placement set, no piece tree**. It runs in
the FEATURES step from a `placed_feature` (count/range/random placement modifiers), exactly
like ore veins or trees. `MonsterRoomFeature.place(FeaturePlaceContext)` carves a small
cobblestone room with a spawner + 1-2 chests in one method.

- **Implication:** the dungeon belongs to the **sibling FEATURES deferral**, not this
  milestone's structure pipeline. It needs the `placed_feature`/`configured_feature`
  plumbing, not `StructureStart`. **It is the wrong first structure** — do NOT model it as
  a structure. If features land first, it is trivial. List it here only to **explicitly
  exclude** it from the structure pipeline and hand it to the features track.
- If you DO want a standalone "first win" inside this milestone, the real Tier-1 below
  (a piece-based structure) is the correct first structure to port.

### Tier 1 — Desert Pyramid + Jungle Temple + Swamp Hut + Igloo (single-piece scattered)

`SinglePieceStructure` + `ScatteredFeaturePiece` subclasses
(`DesertPyramidPiece`, `JungleTemplePiece`, `SwampHutPiece`, `IglooPieces`). These are
the simplest *true* structures:

- Placement: `random_spread`, spacing 32 / separation 8, distinct salt per set
  (desert 14357617, igloo 14357618, swamp_hut 14357620). `jungle_pyramid` shares the
  desert set's machinery.
- A **single piece** (or a tiny fixed set for igloo), geometry fully **hardcoded in
  bytecode** (`postProcess` writes blocks with `generateBox`/`placeBlock`). **Zero `.nbt`
  files** ship for these (confirmed: `data/minecraft/structure/desert_pyramid/` etc. = 0
  entries) → pure logic port, no template embedding.
- Start at **desert pyramid**: biggest hardcoded `postProcess` but no recursion, no
  jigsaw, no neighbor pieces. It exercises the full STARTS→cache→FEATURES→`placeInChunk`
  →`postProcess` loop end-to-end with one piece, so it is the **ideal pipeline shakedown**.

### Tier 2 — Mineshaft (multi-piece recursive assembly, hardcoded geometry)

`MineshaftStructure` + `MineshaftPieces` (corridor / crossing / room / stairs).

- Placement: `random_spread` but **spacing 1, frequency 0.004,
  `frequency_reduction_method: legacy_type_3`** — i.e. EVERY chunk is a candidate, then a
  0.4% probability gate via `legacyArbitrarySaltProbabilityReducer`. Different code path
  from spacing-based sets; port the reducer (see Pitfalls).
- Assembly: `findGenerationPoint` seeds a `StructurePiecesBuilder`, adds a root corridor,
  then **recursive `addChildren`** grows the corridor/crossing graph with collision checks
  against already-placed pieces (`StructurePiece.findCollisionPiece`). Geometry is
  **hardcoded** (0 `.nbt`). This is the first multi-piece, bounded-recursion port.
- Chests via `createChest(..., ResourceKey<LootTable>)` — **loot is deferrable** (place
  the chest block + tag, skip loot-table resolution; see Deferrals).

### Tier 3 — Stronghold (architecturally unique placement + recursive pieces)

`StrongholdStructure` + `StrongholdPieces`.

- Placement: **`concentric_rings`** (count 128, distance 32, spread 3) — NOT per-chunk
  random spread. The set pre-computes a **global list of ~128 ring chunk positions** once
  (`ChunkGeneratorStructureState.generateRingPositions`, biome-validated against
  `#minecraft:stronghold_biased_to`), seeded from a dedicated `concentricRingsSeed`. A
  chunk "is a placement chunk" iff its `ChunkPos` is in that precomputed list
  (`ConcentricRingsStructurePlacement.isPlacementChunk` → `getRingPositionsFor(...).contains`).
- Assembly: the most elaborate hardcoded recursion (twisting corridors, stairs, portal
  room, library), but the **same `StructurePiece`/`addChildren` machinery as mineshaft** —
  once Tier 2 works, the piece logic is "more of the same." The hard part is the
  **placement model**, not the pieces.
- Port stronghold AFTER mineshaft (reuse piece machinery) but treat the concentric-rings
  placement as its own sub-task.

### Tier 4 — Village (data-driven JIGSAW) — **the hard one**

`village_*` structures are `type: minecraft:jigsaw` (`JigsawStructure`), assembled by
`JigsawPlacement` from `template_pool` JSON + `.nbt` `StructureTemplate` files.

- Placement: `random_spread`, spacing 34 / separation 8, salt 10387312, weighted choice
  over 5 biome variants (`villages.json` structure_set).
- Assembly: `JigsawPlacement.addPieces` → an inner **`Placer`** that does **breadth-first**
  growth (a `SequencedPriorityIterator` work queue, NOT stack recursion) bounded by
  `max_distance_from_center: 80` and `size: 6` (max depth). Each step reads a piece's
  jigsaw blocks, picks a target pool, finds an aligning template, collision-checks against
  a `VoxelShape` of placed pieces, and enqueues children.
- This is **mostly DATA** (483 village `.nbt` templates + 74 village `template_pool` JSONs +
  processor lists) and a **bounded amount of LOGIC** (the `Placer`, the
  `SinglePoolElement`/`LegacySinglePoolElement` template instantiation, the jigsaw-block
  alignment math, `StructureTemplate.placeInWorld`). See Don't Hand-Roll.
- Port LAST. It is the only tier needing the full `templatesystem` (.nbt parsing +
  `StructurePlaceSettings` + rotation/mirror + processors) and the pool/jigsaw machinery.

**Recommended sequence:** desert pyramid → (jungle temple / igloo / swamp hut as cheap
follow-ons) → mineshaft → stronghold → village-jigsaw. (Dungeon = features track, excluded.)

---

## Architecture Patterns

### The two-phase pipeline (ChunkStatus)

Vanilla's `ChunkStatusTasks` (jar-confirmed) runs these steps in order, each gated on
neighbor chunks being at the prior status:

```
... NOISE -> SURFACE -> CARVERS ->
STRUCTURE_STARTS   (generateStructureStarts) -> decide WHERE, cache StructureStart per chunk
STRUCTURE_REFS     (generateStructureReferences) -> record which neighbor starts touch THIS chunk
... BIOMES/NOISE already done ...
FEATURES           (generateFeatures -> applyBiomeDecoration) -> PLACE pieces into THIS chunk
```

> Note Sulfur already collapses NOISE→SURFACE→CARVERS into a single `Generate` pass
> (`world/noisegen.go`). Structures add **two new logical phases after carve**:
> a STARTS phase (compute + cache) and a PLACE phase (write blocks). They cannot both
> live inside the current single-shot `Generate(pos)` unchanged — see brownfield seam.

**Phase A — STRUCTURE_STARTS (`ChunkGenerator.createStructures` → `tryGenerateStructure`):**
For each structure set whose placement says "this chunk is a start chunk"
(`StructurePlacement.isStructureChunk`), run `Structure.generate(...)`:
- builds a `GenerationContext` whose RNG is seeded by
  `WorldgenRandom(LegacyRandomSource(0)).setLargeFeatureSeed(worldSeed, chunkX, chunkZ)`
  (jar-confirmed in `GenerationContext.makeRandom`) — **the determinism hinge**;
- calls `findValidGenerationPoint` → `findGenerationPoint` (per-structure) which builds a
  `StructurePiecesBuilder`, runs the piece assembly (hardcoded recursion OR jigsaw), and
  validates the biome at the chosen point;
- wraps the built `PiecesContainer` in a `StructureStart` and stores it via
  `StructureManager.setStartForStructure(sectionPos, structure, start, chunkAccess)`.

The `StructureStart` (`StructurePiece` tree + bounding box + chunkPos + reference count)
is **cached on the chunk** (`ChunkAccess.getAllStarts()` / `setStartForStructure`). It is
computed ONCE for the chunk that "owns" the start, even though the structure may span
many chunks.

**Phase B — STRUCTURE_REFERENCES (`ChunkGenerator.createReferences`):**
For chunk C, scan an **8-chunk-radius** box of neighbors (jar-confirmed: `±8` in
`createReferences`). For every `StructureStart` in any neighbor whose bounding box
`intersects` C's column, call `StructureManager.addReferenceForStructure(...)` — i.e. C
records "structure start at neighbor N reaches into me." This is how a multi-chunk
structure is found later from any chunk it overlaps. 8 chunks is the max structure radius
the engine assumes.

**Phase C — PLACE (`applyBiomeDecoration` → `StructureStart.placeInChunk`):**
During C's feature step, for each structure, `StructureManager.startsForStructure(C)`
returns the starts (owned-or-referenced) that touch C. For each, `placeInChunk(...)`:
- iterates ALL pieces of the start;
- for each piece whose bounding box `intersects` the **writable area of C** (a
  `BoundingBox` of C's 16×16 column, jar-confirmed `getWritableArea`: minX..minX+15,
  full height, minZ..minZ+15);
- calls `piece.postProcess(worldGenLevel, ..., writableBox, C.pos, ...)`.

`postProcess` writes blocks via `placeBlock`, which **clips every write to the piece's
own bounding box AND the `writableBox`** (`placeBlock` does `boundingBox.isInside(pos)`
then `canBeReplaced`, then `setBlock`). This is the cross-chunk mechanism: a piece
spanning chunks is placed **once per overlapping chunk**, each call writing only that
chunk's slice. Idempotent because the placement RNG is re-derivable and writes are
position-clipped.

### The StructurePiece bounding-box tree (Tiers 1-3)

`StructurePiece` (abstract): holds a `BoundingBox`, orientation/rotation/mirror, `genDepth`,
and a `StructurePieceType`. Key contract:
- `addChildren(parent, StructurePieceAccessor, RandomSource)` — recursive growth: a piece
  proposes child pieces, each child constructed at an offset, collision-checked
  (`StructurePiece.findCollisionPiece(list, box)`), and added to the
  `StructurePiecesBuilder` (which IS the `StructurePieceAccessor`). Recursion is bounded by
  `genDepth` (e.g. mineshaft caps corridor depth; stronghold caps `genDepth` at a fixed
  limit).
- `postProcess(...)` — places blocks for this piece into one chunk. Helpers:
  `placeBlock`, `generateBox`/`generateAirBox` (fill a sub-box), `generateMaybeBox`
  (probabilistic), `fillColumnDown`, `maybeGenerateBlock`, `createChest`/`createDispenser`.
- `StructurePiecesBuilder.build()` produces the immutable `PiecesContainer`. For scattered
  features it may `moveBelowSeaLevel`/offset to terrain height first.

**Port shape:** a Go `StructurePiece` interface { `BoundingBox()`, `PostProcess(level, box,
chunkPos)`; piece structs hold their own fields } plus a `PiecesBuilder` collecting them
and answering `findCollisionPiece`. Each structure's piece set is a Go file mirroring the
corresponding `*Pieces` class.

### JigsawPlacement (Tier 4)

`JigsawPlacement.addPieces` (public) → constructs `JigsawStructure.GenerationContext`, picks
a `start_pool` element, places the root template, then runs the private `Placer`:

```
Placer { pools, maxDepth, chunkGenerator, templateManager, pieces (output), random,
         placing: SequencedPriorityIterator<PieceState> }
addPieces:
  placer.tryPlacingChildren(rootPiece, shapeRef, maxDepth, ...)   // seed the queue
  while placer.placing.hasNext():
     state = placer.placing.next()
     placer.tryPlacingChildren(state.piece, ...)                  // BFS, not recursion
```

`tryPlacingChildren` (per piece): for each `JigsawBlockInfo` (jigsaw block) in the piece's
template (`StructureTemplate.getJigsaws(pos, rotation)`), resolve the target pool (with
`PoolAliasLookup`), shuffle candidate elements by weight, and for each candidate try every
rotation: compute the connected position (`StructureTemplate.calculateConnectedPosition`),
build the child's `BoundingBox`, reject if it leaves the world height band
(`isStartTooCloseToWorldHeightLimits`) or collides with the placed-piece `VoxelShape`
(rigid projection), else add a `PoolElementStructurePiece`, subtract its shape, and enqueue
it with depth-1. Depth 0 → only fallback/empty elements allowed.

Pool element types (jar): `SinglePoolElement`, `LegacySinglePoolElement` (village uses
this — legacy = keep jigsaw blocks as air rather than data blocks), `ListPoolElement`,
`FeaturePoolElement`, `EmptyPoolElement`. `projection` is `rigid` (placed at a fixed
height) or `terrain_matching`.

### The structure-start cache (brownfield seam)

Vanilla caches `StructureStart`s **on the `ChunkAccess`** (`getAllStarts`) and threads them
through `StructureManager` keyed by `SectionPos`. Sulfur has no `ChunkAccess`-equivalent
status object exposed to the worker; `world/worker.go` calls `gen.Generate(pos)` once and
hands back an immutable `*level.Chunk`.

**Recommended placement of the cache:** a new `world/structure` package owning a
**concurrent start cache** keyed by packed `int64` chunk pos (use the same
`xsync.Map[int64, *StructureStart]` style already blessed in CLAUDE.md for the entity/chunk
maps). It holds:
- `Starts map[int64][]*StructureStart` — starts OWNED by that chunk (computed in STARTS).
- `References map[int64][]int64` — for chunk C, the keys of neighbor chunks whose starts
  reach C (computed in REFERENCES), so PLACE can gather the relevant starts.

The STARTS computation is **pure over (seed, chunkPos)** (matches Sulfur's WORLD-04 purity
mandate), so the cache is a memoization, not shared mutable state in the dangerous sense —
two workers computing the same chunk's starts get identical results (guard with
`singleflight` exactly like chunk-gen dedup, already in `worker.go`).

### Brownfield: how STARTS + PLACE slot into the worker

The current single-shot `Generate(pos)` (fill→surface→carve) cannot place structures alone
because PLACE needs **neighbor starts** (a structure owned by chunk N writes into chunk C).
Two viable integrations:

1. **Two-pass in the worker (recommended).** Extend the worker so that for a requested
   chunk C it:
   - ensures STARTS are computed + cached for C **and all chunks in the 8-radius**
     (cheap: each is pure (seed,pos), `singleflight`-deduped);
   - runs the existing `Generate(pos)` (terrain);
   - then runs a new PLACE pass that, for each start touching C (own + referenced),
     calls `placeInChunk` with C's writable box, writing into the just-generated chunk
     before it is sealed/handed off.
   This keeps `Generate` as "terrain only" and adds a `placeStructures(ch, pos)` step in
   the same worker goroutine, preserving the single-owner handoff (`ChunkResult`).

2. **Status-staged generation.** Mirror vanilla's per-status neighbor gating more
   literally (STARTS for a radius, then REFERENCES, then FEATURES). Higher fidelity but a
   bigger worker rewrite; defer unless concurrency profiling demands it.

Pipeline order inside the worker becomes:
`fill → surface → carve → [compute+cache STARTS for C and neighbors] → [PLACE pieces into C]
→ finish`. The STARTS step has **no block writes** (pure geometry), so it does not disturb
the existing fill/surface/carve ordering; PLACE runs last (vanilla's FEATURES step is
after carve), so structures correctly overwrite terrain.

### The determinism hinge (seed derivation)

Two distinct seedings, BOTH on `WorldgenRandom(LegacyRandomSource(0))` (Java LCG — **NOT
xoroshiro**; see Pitfalls):

1. **Placement decision** (`RandomSpreadStructurePlacement.getPotentialStructureChunk` and
   `probabilityReducer`): `setLargeFeatureWithSalt(worldSeed, regionX, regionZ, salt)`:
   ```
   seed = regionX*341873128712 + regionZ*132897987541 + worldSeed + salt
   rng.setSeed(seed)            // LCG: (seed ^ 0x5DEECE66D) & ((1<<48)-1)
   ```
2. **Piece RNG** (`GenerationContext.makeRandom`): `setLargeFeatureSeed(worldSeed, chunkX,
   chunkZ)`:
   ```
   rng.setSeed(worldSeed)
   long a = rng.nextLong(); long b = rng.nextLong();
   long s = (chunkX * a) ^ (chunkZ * b) ^ worldSeed
   rng.setSeed(s)
   ```
All structure piece placement (which pieces, where, rotation, mineshaft branch choices,
jigsaw element selection) flows from #2. Match these bit-for-bit or structures land in
different positions/shapes than a vanilla world of the same seed.

---

## Don't Hand-Roll

**EMBED as data (`//go:embed`), never transcribe — extend `tools/extract_worldgen.go`:**

| Data | Count | Notes |
|------|-------|-------|
| `data/minecraft/structure/**/*.nbt` (gzip NBT templates) | **1212 total** (village **483**, bastion 167, woodland_mansion 73, ancient_city 58, end_city 20, pillager_outpost 11, igloo 3) | Jigsaw piece geometry. **Villages need 483.** Mineshaft/stronghold/desert_pyramid/jungle_temple/swamp_hut ship **0** `.nbt` (code-assembled). |
| `worldgen/structure/*.json` | 34 | Per-structure config (type, biomes, step, jigsaw start_pool/size/max_distance, scattered params). |
| `worldgen/structure_set/*.json` | 20 | Placement: spacing/separation/salt/frequency OR concentric-ring count/distance/spread. |
| `worldgen/template_pool/*.json` | 188 (village **74**) | Jigsaw pool element lists + weights + projection + processor refs. |
| `worldgen/processor_list/*.json` | 40 | Block-replace processors (mossify, zombify, block-age) referenced by pool elements. |

The extraction tool **already does exactly this pattern** (prefix-glob pure-unzip into an
embedded FS — `tools/extract_worldgen.go` + `world/levelgen/data/embed.go`). Add the five
prefixes above to `worldgenZipPrefixes` (well, a structure-data sibling) and the data falls
out for free. **Binary `.nbt` extraction is the only new wrinkle** — but it is *also* just
"copy zip entry verbatim" (`copyZipEntry` already streams bytes); the `.nbt` files are
embedded as-is and parsed at runtime, not at extract time.

**PORT as logic (idiomatic Go from bytecode, cite the class, no GPL paste):**

- The placement decision math: `setLargeFeatureWithSalt` / `setLargeFeatureSeed`,
  `getPotentialStructureChunk`, the four `probabilityReducer` variants, `isStructureChunk`,
  `ConcentricRingsStructurePlacement.generateRingPositions` + `isPlacementChunk`.
- `Structure.generate` / `findValidGenerationPoint` / `GenerationContext.makeRandom`.
- Per-structure `findGenerationPoint` + the hardcoded `*Pieces` assembly
  (`addChildren` recursion, `postProcess` block writes) for desert pyramid, jungle temple,
  igloo, swamp hut, mineshaft, stronghold.
- `StructurePiece` helpers (`placeBlock`, `generateBox`, `fillColumnDown`,
  `findCollisionPiece`, rotation/mirror handling).
- `StructureStart.placeInChunk` (the per-chunk piece loop) + `StructureManager`-equivalent
  + the references step (8-radius bbox-intersect scan).
- The jigsaw `Placer` (BFS queue, `tryPlacingChildren`, depth bound, VoxelShape collision)
  + `SinglePoolElement`/`LegacySinglePoolElement` instantiation + `StructureTemplate`
  parse/`placeInWorld` (rotation/mirror/processor application).

**Reuse what Sulfur already has:**
- **NBT:** Sulfur's own `nbt` package (`nbt.Unmarshal`, `nbt.NewDecoder`) reads the
  gzip-wrapped `.nbt` templates (confirmed `1f 8b` gzip magic → wrap in `gzip.NewReader`
  then `nbt.NewDecoder`). **Do not add a second NBT lib.**
- **xsync/singleflight/ants** are already vendored (go.mod) for the start cache + dedup +
  any async piece work.
- **LCG random:** Sulfur's `world/levelgen/random.go` is **xoroshiro-only**. You must add a
  `LegacyRandomSource` (java.util.Random LCG: multiplier `0x5DEECE66D`, addend `11`, 48-bit
  mask) — see Pitfalls. It is ~30 lines; port from `LegacyRandomSource.class` + a
  `WorldgenRandom` wrapper exposing `setLargeFeatureSeed`/`setLargeFeatureWithSalt`.

---

## Common Pitfalls

1. **Wrong RNG family (the #1 trap).** Structure placement + piece RNG use
   **`LegacyRandomSource`** (java.util.Random LCG), NOT the xoroshiro Sulfur already
   ported. `WorldgenRandom` wraps the LCG. `setSeed` = `(seed ^ 0x5DEECE66D) & ((1<<48)-1)`;
   `next(bits)` = `seed = (seed*0x5DEECE66D + 0xB) & mask; return (int)(seed >>> (48-bits))`.
   If you reuse xoroshiro here, every structure lands in the wrong place. (Surface/biome
   noise stays xoroshiro — two RNGs coexist.)

2. **Cross-chunk placement (the structural correctness trap).** A structure owned by chunk
   N writes into chunk C. If PLACE only ever sees C's own starts, multi-chunk structures
   get truncated. You MUST run the **references step** (8-chunk-radius bbox-intersect scan)
   so C knows about N's overlapping start, and `placeInChunk` must **clip writes to C's
   writable box** so the same piece placed from C's pass and N's pass produces identical,
   non-duplicated blocks. The clip is in `placeBlock` (`box.isInside` + `writableBox`).

3. **Spacing/separation/salt math off-by-something.** `getPotentialStructureChunk` uses
   `Math.floorDiv(chunkX, spacing)` (floor division — Go `/` truncates toward zero;
   implement floorDiv explicitly for negatives) then
   `regionX*spacing + spreadType.evaluate(rng, spacing-separation)`. `LINEAR` =
   `nextInt(bound)`; `TRIANGULAR` = `(nextInt(bound)+nextInt(bound))/... ` (two draws — get
   the draw count exactly right or the whole stream desyncs). The salt is added INSIDE the
   `setLargeFeatureWithSalt` seed, not XORed after.

4. **Mineshaft's special placement.** spacing 1 + frequency 0.004 +
   `legacy_type_3` reduction — it is NOT the spacing-grid path. Every chunk is a candidate;
   the gate is `legacyArbitrarySaltProbabilityReducer(seed, x, z, salt, frequency)` →
   `setLargeFeatureWithSalt(...)` then `nextFloat() < frequency`. Get the reducer variant
   right (there are FOUR: `probabilityReducer`, `legacyProbabilityReducerWithDouble`,
   `legacyArbitrarySaltProbabilityReducer`, `legacyPillagerOutpostReducer`); the
   `frequency_reduction_method` field in the structure_set JSON selects which.

5. **Stronghold's concentric rings are global, not per-chunk.** You cannot decide
   "is this a stronghold chunk?" locally — the ~128 ring positions are computed once for
   the whole world (a spiral over rings, each position biome-validated via a biome-source
   query, seeded from a dedicated `concentricRingsSeed`). Cache the ring list at
   world-gen-state init; `isPlacementChunk` is a `list.contains(chunkPos)`. The biome
   validation makes ring generation depend on the (already-ported) multi-noise biome
   source — wire it through.

6. **Jigsaw infinite recursion / runaway size.** The `Placer` is **bounded by `maxDepth`
   (`size`, e.g. 6) AND `max_distance_from_center` (e.g. 80 blocks) AND VoxelShape
   collision**. Drop any of the three and a village grows forever or overlaps itself. It is
   **BFS via a priority queue**, not stack recursion — replicate the queue ordering
   (`SequencedPriorityIterator`) or piece-selection RNG draws desync vs vanilla. Depth-0
   pieces may only attach fallback/empty pool elements (terminators).

7. **`.nbt` parsing details.** Templates are **gzip-compressed** NBT (`1f 8b`); decode =
   `gzip.NewReader` → `nbt.NewDecoder`. The format: `size` (3-int list), `palette`(s)
   (block states), `blocks` (list of {pos:[x,y,z], state:int index, optional nbt}),
   `entities`. `LegacySinglePoolElement` (village) treats jigsaw blocks specially (replaced
   with air on place). Rotation/mirror is applied at place time via `StructurePlaceSettings`
   — bake it into `placeInWorld`, do not pre-rotate the template. Sulfur already has the NBT
   reader; the work is mapping these tags to Go structs + the place transform.

8. **`floorDiv` / signed-shift / int-overflow parity.** Same class of bug already handled in
   `random.go` (e.g. `mthGetSeed` documents Java `>>` vs `>>>`). Region indices and seed
   math use Java `long` overflow + `floorDiv`; reuse the discipline from the noise port
   (explicit `int64`, explicit floor division for negatives).

9. **Heightmap dependency at STARTS time.** Scattered features + jigsaw `project_start_to_
   heightmap: WORLD_SURFACE_WG` need a terrain-height query for the chunk at STARTS time
   (before that chunk's blocks exist). Vanilla uses the noise generator's
   `getBaseHeight`/`getFirstOccupiedHeight` sampling, NOT the placed blocks. Sulfur must
   expose a cheap "sample surface Y at (x,z)" from the noise router (it already computes
   this for `SpawnSurfaceY` from a generated chunk — generalize to a column sampler that
   doesn't require the full chunk, or accept generating the owner chunk first).

---

## Code Examples

### Placement seed (LCG) — `setLargeFeatureWithSalt` (jar bytecode)
```
// RandomSpreadStructurePlacement.getPotentialStructureChunk / probabilityReducer
// WorldgenRandom.setLargeFeatureWithSalt(long seed, int x, int z, int salt):
seed64 := int64(x)*341873128712 + int64(z)*132897987541 + worldSeed + int64(salt)
lcg.SetSeed(seed64)   // (seed64 ^ 0x5DEECE66D) & ((1<<48)-1)
```

### Placement decision — `getPotentialStructureChunk` (jar bytecode, exact)
```
// floorDiv, not Go '/'
regX := floorDiv(chunkX, spacing)
regZ := floorDiv(chunkZ, spacing)
rng  := newWorldgenRandom(newLegacy(0))
rng.setLargeFeatureWithSalt(worldSeed, regX, regZ, salt)
bound := spacing - separation
ox := spreadType.evaluate(rng, bound)   // LINEAR: nextInt(bound); TRIANGULAR: (nextInt+nextInt)/...
oz := spreadType.evaluate(rng, bound)
startChunk := ChunkPos{ regX*spacing + ox, regZ*spacing + oz }
// isStructureChunk: startChunk == (chunkX, chunkZ)
```

### Piece RNG (the determinism hinge) — `GenerationContext.makeRandom` (jar bytecode)
```
// WorldgenRandom.setLargeFeatureSeed(long seed, int chunkX, int chunkZ):
rng.setSeed(worldSeed)
a := rng.nextLong(); b := rng.nextLong()
s := (int64(chunkX) * a) ^ (int64(chunkZ) * b) ^ worldSeed
rng.setSeed(s)   // every piece-placement draw flows from here
```

### Per-chunk placement — `StructureStart.placeInChunk` (jar bytecode)
```
pieces := start.pieceContainer.pieces
if len(pieces) == 0 { return }
for _, p := range pieces {
    if p.BoundingBox().Intersects(writableBox) {   // writableBox = THIS chunk's 16x16xH column
        p.PostProcess(level, structureManager, gen, rng, writableBox, chunkPos, refPos)
    }
}
structure.AfterPlace(...)   // post-processing (terrain beard, etc.) — can stub early
```

### A piece block write — `StructurePiece.placeBlock` (jar bytecode)
```
func (p *Piece) PlaceBlock(level WorldGenLevel, state BlockState, x,y,z int, box BoundingBox) {
    pos := p.getWorldPos(x, y, z)           // local -> world (applies rotation/mirror/orientation)
    if !box.IsInside(pos) { return }         // clip to the per-chunk writable box (cross-chunk!)
    if !p.canBeReplaced(level, x,y,z, box) { return }
    if p.mirror != NONE { state = state.Mirror(p.mirror) }
    if p.rotation != NONE { state = state.Rotate(p.rotation) }
    level.SetBlock(pos, state, 2)
    // + fluid fixup if the target column had fluid
}
```

### Jigsaw recursion skeleton — `JigsawPlacement.Placer` (jar bytecode)
```
func addPieces(ctx, startPool, ..., maxDepth, startPos, ...) Optional[Stub] {
    builder := NewPiecesBuilder()
    root := instantiate(startPool.randomElement(rng), startPos, rotation)  // place root template
    builder.add(root)
    placer := &Placer{pools, maxDepth, gen, templateMgr, pieces: builder, rng,
                      placing: NewSequencedPriorityIterator()}
    shape := MutableObject(VoxelShape(root.box))           // accumulated occupied volume
    placer.tryPlacingChildren(root, shape, maxDepth, ...)  // enqueue root's jigsaw children
    for placer.placing.HasNext() {                          // BFS, not stack recursion
        state := placer.placing.Next()
        placer.tryPlacingChildren(state.piece, state.shape, state.depth, ...)
    }
    return Stub(startPos, builder)
}
// tryPlacingChildren(piece, shapeRef, depth, ...):
//   for each jigsawBlock in piece.template.getJigsaws(piece.pos, piece.rotation):
//     targetPool := pools.resolve(jigsawBlock.pool, aliasLookup)
//     for each candidate in shuffleByWeight(targetPool, rng):           // depth==0 => fallback only
//       for each rotation:
//         childPos := template.calculateConnectedPosition(jigsawBlock, candidateJigsaw)
//         childBox := candidate.boundingBox(childPos, rotation)
//         if startTooCloseToHeightLimits(childBox) { continue }
//         if Shapes.joinIsNotEmpty(shapeRef, childBox) { continue }     // rigid collision reject
//         add PoolElementStructurePiece(candidate, childPos, rotation); subtract childBox from shapeRef
//         placer.placing.enqueue(child, depth-1); break to next jigsawBlock
```

---

## Confidence

| Area | Confidence | Basis |
|------|------------|-------|
| Two-phase pipeline (STARTS / REFERENCES / FEATURES) | **HIGH** | `ChunkStatusTasks`, `ChunkGenerator.createStructures/createReferences/applyBiomeDecoration` bytecode read directly. |
| Seed derivation (`setLargeFeatureSeed`/`WithSalt`, `makeRandom`) | **HIGH** | Exact bytecode of `WorldgenRandom`, `LegacyRandomSource`, `GenerationContext.makeRandom`. |
| Placement math (`getPotentialStructureChunk`, `probabilityReducer`, concentric rings) | **HIGH** | Bytecode confirmed incl. floorDiv, salt-in-seed, reducer variants, ring `contains` check. |
| `placeInChunk` cross-chunk clip + 8-radius references | **HIGH** | `StructureStart.placeInChunk`, `createReferences` (±8), `placeBlock`/`getWritableArea` bytecode. |
| StructurePiece tree (mineshaft/temple assembly) | **HIGH** | `StructurePiece` full API + `MineshaftPieces`/`DesertPyramidPiece`/`ScatteredFeaturePiece` shapes confirmed. |
| Data split (what's `.nbt` vs hardcoded) | **HIGH** | File counts per structure dir confirmed: villages 483 .nbt; mineshaft/stronghold/temples 0 .nbt. |
| Dungeon = Feature not Structure | **HIGH** | `MonsterRoomFeature extends Feature`, `place(FeaturePlaceContext)` — no StructureStart. |
| Jigsaw `Placer` BFS detail (queue ordering, depth/distance/shape bounds) | **HIGH** | `JigsawPlacement.addPieces` + `Placer.tryPlacingChildren` + `SequencedPriorityIterator` confirmed; exact RNG-draw ordering needs care at impl time. |
| `.nbt` tag schema + place transform | **MEDIUM** | Gzip+NBT confirmed; `StructureTemplate` tag constants + `placeInWorld` signature confirmed, but full per-tag mapping (palette/blocks/entities + processor application) is an impl-time deep-read, not yet byte-traced here. |
| Brownfield worker seam (two-pass placement) | **MEDIUM** `[INFERRED]` | Recommendation derived from current `world/worker.go` + `noisegen.go` structure; the exact worker refactor is a design choice, not jar-dictated. |
| Heightmap-at-STARTS sampling | **MEDIUM** | Vanilla samples noise height not blocks; Sulfur must expose a column sampler — confirmed need, exact API is design work. |

---

## Deferrals (push to v3)

- **Loot tables** — `createChest(..., ResourceKey<LootTable>)` places the chest **block**
  and records a loot-table reference; resolving loot needs the whole loot-table system
  (predicates, number providers, item registry). **Defer**: place chests empty (or block-
  only) in v2; structures are visually + structurally complete without loot. Low risk —
  the hook is a single call you can stub.
- **Ocean monument, woodland mansion, ancient city, trial chambers, bastion, end city,
  nether fortress/fossil, ruined portal, shipwreck, ocean ruins, buried treasure, pillager
  outpost, trail ruins** — out of this milestone's scope. Monument (huge fixed grid) and
  mansion (73 .nbt jigsaw-ish) are their own large efforts; ancient city/trial
  chambers/bastion are big jigsaw data sets (58/.../167 .nbt). **Defer all to v3**; the v2
  machinery (jigsaw + piece tree + placement) is exactly what they reuse, so v3 is mostly
  "more data + a few new piece types," not new architecture.
- **Nether/End structures** — depend on nether/end dimension generation (not yet present).
  **Defer** until those dimensions exist.
- **`afterPlace` terrain adaptation (`beard_thin`/`beard_box`)** — the post-place terrain
  smoothing under jigsaw structures (`Structure.afterPlace` + `TerrainAdjustment`). Villages
  set `terrain_adaptation: beard_thin`. **Defer/stub** initially: villages generate correctly
  but may float/clip slightly without beard smoothing; add in a polish pass. Flag as MEDIUM-
  priority follow-up, not a blocker.
- **Structure NBT persistence** (`StructureStart.createTag`/`loadStaticStart`) — saving
  computed starts to region files so they survive reload. v2 can recompute starts on demand
  (they are pure over seed+pos). **Defer** the on-disk caching to v3 unless reload
  determinism across versions matters sooner.
