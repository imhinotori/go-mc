---
phase: 13-trees-dungeon-features-gate
plan: 04
subsystem: worldgen
tags: [dungeon, monster-room, features-acceptance, visual-gate, feat-05, feat-06, protocol-776]

# Dependency graph
requires:
  - phase: 13-trees-dungeon-features-gate
    plan: 03
    provides: "the FULL generatable-overworld tree set (oak/birch/spruce/acacia/jungle/dark_oak + cherry/mangrove/azalea/pale_oak) live through the tree featureBody — the biome-correct trees the visual gate confirms"
  - phase: 12-core-feature-types
    provides: "the featureBody dispatch seam (registerFeatureBody/lookupFeatureBody), bodyContext (view + registry), placeState/getState, the random_selector recursion, ParseProvider/ResolveBlockStateJSON"
  - phase: 11-feature-pipeline
    provides: "applyBiomeDecoration (the 11 GenerationStep.Decoration steps over the 3x3 biome set) + the FeatureSorter global per-step index + the placement modifiers; NoiseGenerator.Decorate wired into the worker's tryDecorate (the streamed-chunk path)"
provides:
  - "the 'monster_room' featureBody — MonsterRoomFeature.place ported jar-exact (cobblestone room + mossy-speckled floor + center spawner + 1-2 wall chests as BLOCKS, loot/mob deferred to v3), registered via registerFeatureBody, writing through bctx.view (cross-chunk + heightmap-live)"
  - "the full-features automated acceptance gate (TestFullFeaturesAcceptance): biome-correct trees place + the forest selector resolves to real trees + the dungeon places + determinism holds with ALL bodies live"
  - "the verified streamed-chunk path: NewNoiseGenerator's Decorate runs the full feature set (ores + ground cover + the full tree set + the dungeon) into every streamed chunk, deterministic per seed — NO worker/wire rewire (the dungeon body went live via init() registration)"
  - "FEAT-05 (the dungeon) + FEAT-06 (the features-milestone VISUAL GATE, approved on a real vanilla 26.2 client) — the FEATURES half of v2 is closed"
affects: [14-structures-starts-pipeline]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Registering a no-op feature type's body (registerFeatureBody) makes the EXISTING applyBiomeDecoration place it at the jar frequency — NO pipeline change. The dungeon went live with zero worker/wire edits (the placed_feature was already loaded + sorted; only the body was a no-op)."
    - "A v3-deferred RNG-consuming draw (the spawner mob roll) is STILL CONSUMED (the EntityType discarded) so the post-place rng state stays jar-identical — a deferral must not desync the global decoration sequence. A v3-deferred draw that takes ZERO draws in vanilla (the chest loot-table stamp) needs no replay."
    - "safeSetBlock (Feature.safeSetBlock + isReplaceable(#features_cannot_replace)) ported as a body helper: every dungeon write checks the existing block is replaceable (NOT bedrock/spawner/chest/...) — a feature never clobbers a pre-existing protected block."
  modified: []

key-files:
  created:
    - "world/feature_dungeon.go — the 'monster_room' featureBody: MonsterRoomFeature.place (the two nextInt(2) half-extents, the cavern-validity accept/reject, the air carve, the cobblestone shell + per-floor-cell nextInt(4) mossy speckle, the up-to-2 wall chests with StructurePiece.reorient facing, the center spawner) + the ported safeSetBlock/#features_cannot_replace gate + the conservative isSolid read + the consumed-but-discarded mob roll. Registered via init()."
    - "world/feature_dungeon_test.go — TestMonsterRoomPlaces (deterministic cobble room + spawner-at-center + mossy floor, re-run bit-identical + post-place rng fingerprint), TestMonsterRoomRejectsNoPartial (an all-air no-floor candidate -> false, ZERO writes), TestMonsterRoomCrossChunkEdge (an edge dungeon spills its shell into the +x neighbor), TestMonsterRoomMobRollConsumed (the deferred mob roll still consumes its nextInt(4))"
  modified:
    - "world/decoration_test.go — TestFullFeaturesAcceptance: biome-correct trees grow their biome-correct log+leaves through the live treeBody (oak/birch/spruce/acacia/jungle/dark_oak), the trees_plains random_selector resolves to a REAL oak (not a no-op), the monster_room dungeon places a cobble room + spawner, and a re-run with all bodies live is bit-identical"
    - "cmd/sulfur/main.go — documented that NewNoiseGenerator's Decorate streams FULLY-decorated chunks (per-biome trees + ground cover + decoration ores + dungeons) to the real client + the startup log notes the full feature pipeline is live; a no-op VERIFICATION (Phase 11 wired Decorate; the dungeon body registers via init()) — NO worker/wire rewire"

key-decisions:
  - "The dungeon is a FEATURE not a structure (research v2-structures Tier 0, HIGH confidence): MonsterRoomFeature extends Feature, place(FeaturePlaceContext), no StructureStart. It runs in the UNDERGROUND_STRUCTURES decoration step from its placed_feature (count=10/4 + height_range + in_square + biome) exactly like an ore. The structure STARTS/REFERENCES/PLACE pipeline (Phases 14-16) is NOT involved."
  - "The mossy-cobblestone floor speckle is the INVERSE of the naive guess: on the floor ring (j==-1) the jar draws nextInt(4) and places COBBLESTONE when ==0, MOSSY_COBBLESTONE otherwise (so ~3/4 of the floor is mossy). Transcribed from bytecode 427-477 (the ifeq inverts the sense), pinned by the test asserting a mossy speckle is present."
  - "The build loop's j iterates 3 DOWN TO -1 (jar `for(j=3;j>=-1;j--)`); the interior test (i not on X-bound, k not on Z-bound, j!=-1, j!=4) clears to CAVE_AIR via safeSetBlock; the shell either opens a void below (RAW setBlock(AIR) when pos.Y>=minY && !below.isSolid()) or lays cobble/mossy. The exact loop order + draw sequence is the determinism contract (T-13-09)."
  - "isSolid()/isSolidRender() use the established conservative mid-worldgen read (faceSturdyUp precedent: not air [incl. cave_air], not water) — the full BlockBehaviour collision shape is unavailable during decoration; this never reports solid over air/water so a dungeon never floats or roofs over a void."
  - "LOOT + SPAWNER-MOB DEFERRED to v3 (documented, not dropped — matches the structure chest deferrals in research v2-structures): the chest places as the reoriented CHEST BLOCK + the SIMPLE_DUNGEON loot ref is omitted (zero rng draws, no replay needed); the spawner places as the SPAWNER BLOCK + the randomEntityId mob roll is consumed (one nextInt(4), EntityType discarded) to keep the rng jar-faithful. The visible room is fully delivered."
  - "main.go is a no-op verification, NOT a re-wire: Phase 11 already wired NoiseGenerator.Decorate into the worker's tryDecorate, and buildDecorationData picks up every init()-registered body — so registering the dungeon body made it stream to a real client with no change to the worker/tick/Generator interface/chunk wire (all v1-sealed)."

requirements-completed: [FEAT-05, FEAT-06]

# Metrics
duration: ~30min
completed: 2026-06-25
---

# Phase 13 Plan 04: MonsterRoom Dungeon + the Full-Features VISUAL GATE Summary

**The MonsterRoomFeature dungeon ported jar-exact as the `monster_room` featureBody (cobblestone room + mossy-speckled floor + center spawner + 1-2 wall chests as blocks; loot/mob deferred to v3) — which, once registered, made the existing `applyBiomeDecoration` place dungeons at the jar frequency with no pipeline change. The full-features automated acceptance is green (biome-correct trees + the forest selector resolving to real trees + the dungeon + 5x5 reorder-determinism + emit-once byte-identical with ALL bodies live + the Docker -race gate clean), and the autonomous:false real-client VISUAL GATE is APPROVED — a vanilla 26.2 client confirmed trees + vines render. FEAT-05 + FEAT-06 met: the FEATURES half of v2 (Phases 10-13) is CLOSED.**

## Performance

- **Duration:** ~30min
- **Started:** 2026-06-25
- **Completed:** 2026-06-25
- **Tasks:** 3 (Task 1 TDD dungeon body; Task 2 acceptance gate + Docker -race; Task 3 the BLOCKING visual gate — approved)
- **Files modified:** 2 created + 2 modified

## Accomplishments

### The dungeon — MonsterRoomFeature.place (FEAT-05)
Ported jar-exact from `net.minecraft.world.level.levelgen.feature.MonsterRoomFeature.place` (`javap -c` against `temp/cache/26.2-inner.jar`) as the `monster_room` featureBody (the type was in the recognized roster but a no-op until now). The empty config `{}` is JAR-VERIFIED — the geometry is hardcoded in the method:

- **Extents:** `xr = nextInt(2)+2`, then `zr = nextInt(2)+2` (the two half-extent draws — the room is `2*xr+1` x `2*zr+1`, 4 tall + floor).
- **Cavern validity (no draws):** scan `i:[xrn..xrp] x j:[-1..4] x k:[zrn..zrp]`; require a solid floor (j==-1) + solid ceiling (j==4); count open sides (a wall cell at j==0 with air at it AND above — a cavern adjacency). Reject if `openCount<1 || >5` → **return false, NO partial room** (T-13-10).
- **Carve + shell (the build loop, j from 3 down to -1):** interior cells → CAVE_AIR (safeSetBlock, keeping a pre-existing chest/spawner); shell cells either open a void below (RAW `setBlock(AIR)` when `pos.Y>=minY && !below.isSolid()`) or lay COBBLESTONE — with the FLOOR ring (j==-1) drawing `nextInt(4)`: `==0` → cobblestone, else (3/4) → MOSSY_COBBLESTONE (the speckle).
- **Chests (up to 2, each up to 3 tries):** a random in-room position (`nextInt(2*xr+1)-xr`, `nextInt(2*zr+1)-zr` — 2 draws/try); placed iff the spot is empty AND has EXACTLY ONE solid horizontal neighbor (flush against a wall); reoriented to face away from the wall (`StructurePiece.reorient` — pure geometry, NO draw).
- **Spawner:** placed at the room center (origin).
- All writes go through `bctx.view.SetBlock` (cross-chunk + heightmap-live) via the ported `safeSetBlock` (honoring `#features_cannot_replace` = bedrock/spawner/chest/end_portal_frame/reinforced_deepslate/trial_spawner/vault, JAR-VERIFIED tag).

The EXACT draw order (extents → floor speckle → chest attempts → spawner mob roll) is the determinism contract (T-13-09), pinned by `TestMonsterRoomPlaces` (deterministic + post-place rng fingerprint) and the re-run bit-identity check.

### The full-features automated acceptance (the backstop the gate stands on)
`TestFullFeaturesAcceptance` (world/decoration_test.go) re-asserts the determinism contract with the FULL tree set (13-01/02/03) + the dungeon now live:
1. **biome-correct trees place** — oak/birch/spruce/acacia/jungle/dark_oak each grow their biome-correct log + leaves through the live treeBody;
2. **the forest selector resolves to a REAL tree** — the `trees_plains` random_selector grows a real oak (the no-op is gone);
3. **the dungeon places** — a cobble room + spawner appear;
4. **determinism holds** — a re-run with all bodies live is bit-identical.

The 5x5 reorder-determinism (`TestDecorationReorderIdentical`) + emit-once (`TestEmitOnce`/`TestEmitOnceUnderHold`) gates run the REAL worker pipeline with ALL bodies live (the dungeon registers via init()) and stay byte-identical. The full **Docker `-race`** gate (`MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./world/...`) is **GREEN** — every `world/...` package `ok`, no race detected (the `world` package itself ran 483s under race instrumentation with trees + dungeon + all bodies live).

### The streamed-chunk verification (no re-wire)
`cmd/sulfur/main.go` was confirmed (and documented) to already stream fully-decorated chunks: `NewNoiseGenerator` calls `buildDecorationData()` which picks up every init()-registered body, and the worker's `tryDecorate` runs `Decorate(view) → applyBiomeDecoration` before sealing each streamed chunk. Registering the dungeon body made it stream to a real client with NO worker/tick/Generator-interface/chunk-wire change (all v1-sealed). The startup log now notes the full feature pipeline is live.

### THE VISUAL GATE (FEAT-06) — APPROVED
The autonomous:false, gate="blocking" terminal task. A real unmodified vanilla 26.2 client (PrismLauncher) connected to the running server (fixed `worldSeed`, full noise + features gen) and CONFIRMED the ported pipeline renders the world: **"Hay árboles y vines"** (trees + vines render). The features-milestone gate — mirroring v1's 04-04/05-03/06-07/07-06/09-09 visual gates — is met. Worldgen added NO new wire surface (the chunk format was capture-diff-sealed in v1 Phase 4), so the gate was real-client VISUAL determinism, NOT a capture-diff; the v1 wire goldens stay the authority and stay green (no encoder/packet/golden file was touched — grep-confirmed).

## Deviations from Plan

**None — plan executed as written.** The two automated tasks (the dungeon body + the acceptance gate + the Docker -race) were executed fully and committed atomically; the terminal visual-gate task was held at the checkpoint and finalized only after the user approved on a real client (the autonomous:false discipline).

### Auth gates

None.

## Deferred Items (v3 — documented, not dropped)

| Category | Item | Why deferred | Visible impact |
|----------|------|--------------|----------------|
| Loot | Dungeon chest loot (`SIMPLE_DUNGEON` loot table) | Loot tables are a separate subsystem (predicates/number-providers/item registry); the chest is placed as a block + the loot ref is omitted (zero rng draws, no determinism impact) | Chests are empty — cosmetic, non-blocking |
| Mob | Dungeon spawner mob (`randomEntityId`: skeleton/zombie/zombie/spider) | Block entities (SpawnerBlockEntity) are not yet wired; the spawner is placed as an inert block + the mob roll is CONSUMED (rng kept jar-faithful) but the EntityType discarded | Spawner is inert — cosmetic, non-blocking |
| Decorator | `BeehiveDecorator` occupant (the bee NBT in oak/birch bee nests) | Block-entity occupant data is the same v3 subsystem; the bee_nest BLOCK is placed | Empty bee nests — cosmetic |
| Ground patch | `PaleMoss` ground `pale_moss_patch` (pale_garden floor moss) | A nested vegetation feature needing the chunk generator the pure DecoratorContext does not carry; the gate nextFloat is consumed (rng lockstep), the trunk/leaves `pale_hanging_moss` drape IS placed (13-03 Known Stub) | Missing floor moss carpet in pale_garden — cosmetic |

All deferrals are cosmetic / non-blocking — the structurally + visually complete world (biome-correct trees, vegetation, decoration ores, dungeons) is delivered. The STRUCTURES half of v2 (Phases 14-16: temples/mineshaft/stronghold/village via the STARTS/REFERENCES/PLACE pipeline) is the next milestone work.

## Known Stubs

The dungeon body itself places real blocks (cobblestone room + mossy floor + spawner + chests) — no stub flows to a UI as empty/placeholder. The v3 deferrals above (empty chests, inert spawner, empty bee nests, missing pale_garden floor moss) are the documented cosmetic stubs, each carried forward with the rng-lockstep discipline (a deferred draw is consumed, not dropped) so the same seed reproduces the same world.

## Self-Check: PASSED

- `world/feature_dungeon.go` — FOUND
- `world/feature_dungeon_test.go` — FOUND
- `world/decoration_test.go` — FOUND (modified)
- `cmd/sulfur/main.go` — FOUND (modified)
- commit `e4231b69` (feat: monster_room dungeon body) — FOUND
- commit `25ee071d` (test: full-features acceptance gate) — FOUND
- `go build ./...` exit 0 + `go test ./world/ -run 'TestMonsterRoom|TestFullFeaturesAcceptance|TestDecorationReorderIdentical|TestEmitOnce' -count=1` green + Docker `-race ./world/...` green
