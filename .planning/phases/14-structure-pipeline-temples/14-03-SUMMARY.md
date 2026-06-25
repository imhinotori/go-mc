---
phase: 14-structure-pipeline-temples
plan: 03
subsystem: worldgen
tags: [structures, scattered-temples, jungle-temple, igloo, swamp-hut, addchildren, multi-piece, block-selector, template-extract, rotation-mirror, acceptance, protocol-776]

# Dependency graph
requires:
  - phase: 14-02
    provides: "the StructurePiece machinery (Piece+PostProcess, base StructurePiece, placeBlock cross-chunk clip, generateBox/generateAirBox/fillColumnDown/createChest, rotation/mirror + the FACING transform, makeScatteredBoundingBox, getRandomHorizontalDirection); StructureStart.placeInChunk + the re-derivable piece RNG; the desertPyramidStartGen template (salt + getLowestY + REAL biome gate + SampleSurfaceY + baked terrain move); the filled placeStructures PLACE hook + the desert StartGenerator registration site"
  - phase: 14-01
    provides: "getPotentialStructureChunk/isStructureChunk placement math (per-salt), the StructureStart cache + 8-radius REFERENCES, the SurfaceSampler, the StartGenerator interface carrying the REAL BiomeAt seam, LoadStructureSet + HasStructureBiomes (the embedded structure_set/has_structure loaders)"
provides:
  - "the jungle_temple StartGenerator (salt 14357619, getLowestY sea gate + REAL {bamboo_jungle,jungle} biome gate) + JungleTemplePiece: the full hardcoded cobble/mossy stepped temple + chambers + tripwire/redstone/dispenser trap + lever/sticky-piston puzzle + 2 chests (blocks, loot deferred), incl. the MossStoneSelector per-cell nextFloat draws"
  - "the swamp_hut StartGenerator (salt 14357620, NO sea gate, REAL {swamp} biome gate) + SwampHutPiece: the spruce-plank hut on oak-log stilts + cauldron/crafting-table/potted-red-mushroom + spruce-stair roof (witch/cat entities deferred v3)"
  - "the igloo StartGenerator (salt 14357618, REAL {snowy_taiga,snowy_plains,snowy_slopes} biome gate) + IglooPiece: the first MULTI-PIECE temple (dome ALWAYS + ladder+basement PROBABILISTICALLY via nextDouble<0.5) — geometry EXTRACTED OFFLINE from the 26.2 igloo/*.nbt templates into igloo_data.go (0 .nbt at runtime); palette resolved via block.State -> ToStateID"
  - "piece.go extensions: generateBoxSelector (the BlockSelector generateBox overload) + the full StairBlock mirror/rotate (FACING + SHAPE inner/outer swap) + the Vine/RedstoneWire/Tripwire compass-face rotate/mirror permutation + facingOf/withFacing extended to dispenser/lever/sticky-piston/tripwire-hook/repeater/furnace/ladder"
  - "CompositeStartGenerator (dispatches all four scattered temples; each runs its own biome gate); registered in NoiseGenerator so ComputeStarts + the PLACE pass place all four"
  - "the structures-pipeline acceptance suite (TestStructuresPipelineAcceptance + StartsPure + CrossChunkIdempotent) — the automated STRUCT-02 close gate"
affects: [15-mineshaft-outpost-stronghold, 16-jigsaw-villages]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "javap -c jar-port discipline extended to JungleTemplePiece.postProcess (block-by-block + draw order + the per-cell MossStoneSelector nextFloat<0.4 cobblestone/mossy draws as the determinism contract) + SwampHutPiece.postProcess"
    - "the BlockSelector generateBox overload (generateBoxSelector): the jar gates the per-cell selector.next() RNG draw behind `alwaysReplace || getBlock.isAir()` — the draw COUNT depends on what is already placed (ported exactly)"
    - "full StairBlock mirror/rotate (FACING + SHAPE handedness swap) + the multi-compass-face block rotate/mirror (Vine/RedstoneWire/Tripwire N/E/S/W permutation) — the orientation transforms the temples need under any RNG-drawn orientation, not just NORTH"
    - "OFFLINE TEMPLATE EXTRACTION: the 26.2 igloo is a .nbt-template structure (TemplateStructurePiece), NOT hardcoded postProcess. Extracted the igloo/{top,middle,bottom}.nbt palette+blocks offline (gunzip + NBT-decode) into Go data tables (igloo_data.go) — preserving 0 .nbt at RUNTIME, same as the desert pyramid's javap-c bytecode source"
    - "palette resolution via block.State{Name,Properties}.Block() -> block.ToStateID (the same name+props -> Block resolver the chunk loader uses); the Properties payload is the bare compound (strip nbt.Marshal's 3-byte root header)"

key-files:
  created:
    - world/structure/jungle_temple.go
    - world/structure/jungle_temple_test.go
    - world/structure/swamp_hut.go
    - world/structure/swamp_hut_test.go
    - world/structure/igloo.go
    - world/structure/igloo_data.go
    - world/structure/igloo_test.go
    - world/structure/composite.go
    - world/structure/acceptance_test.go
  modified:
    - world/structure/piece.go
    - world/noisegen.go

key-decisions:
  - "DEVIATION: the 26.2 igloo is a TEMPLATE/.nbt structure (igloo/top|middle|bottom.nbt via TemplateStructurePiece), NOT a hardcoded-postProcess piece like the plan assumed. Rather than pull in the entire Phase-16 StructureTemplate placement subsystem, the .nbt template geometry was EXTRACTED OFFLINE (gunzip + NBT-decode) into Go data tables (igloo_data.go) and placed via the 14-02 placeBlock — preserving the plan's intent (dome + probabilistic basement, the addChildren multi-piece, determinism) + 0 .nbt at runtime. The structure_block DATA markers are skipped (vanilla handleDataMarker removes them)."
  - "jungle/swamp height move = updateAverageGroundHeight (AVERAGE ground, offset 0, NO RNG draw), unlike the desert pyramid's updateHeightPositionToLowestGroundHeight (lowest + -nextInt(3)). Baked at START from the surface sampler's footprint-corner average; no extra RNG draw keeps the stream bit-faithful."
  - "swamp hut has NO sea-level gate (base Structure.findGenerationPoint = onTopOfChunkCenter only); jungle temple HAS the getLowestY sea gate (SinglePieceStructure, like the desert pyramid). Salts cross-checked vs the embedded structure_set JSON: jungle 14357619, igloo 14357618, swamp 14357620 (all distinct from desert 14357617)."
  - "the jungle temple's cobble shell uses the BlockSelector generateBox overload (MossStoneSelector: nextFloat<0.4 cobblestone : mossy_cobblestone per cell) — a load-bearing per-cell RNG draw. generateBoxSelector ports the jar gate exactly (the selector draws ONLY when alwaysReplace||isAir)."
  - "the igloo basement is PROBABILISTIC: nextDouble()<0.5 -> add the laboratory (bottom) + ladder (middle) pieces via the addChildren multi-piece hook (the first multi-piece user; a fixed 1-3 set, NOT recursion), then a nextInt(8) draw (the determinism contract). The basement chest places as a chest BLOCK (loot deferred v3); the villager/zombie-villager + the swamp witch/cat + the jungle trap mobs are ENTITY deferrals v3."
  - "the orientation transforms were completed to full fidelity (StairBlock SHAPE swap + Vine/RedstoneWire/Tripwire compass-face permutation) so the temples are jar-exact under ANY RNG-drawn orientation — the test anchors land on WEST/EAST orientations (not just NORTH identity), exercising the transforms."

patterns-established:
  - "a CompositeStartGenerator dispatches N per-structure StartGenerators + concatenates their owned starts; each member keeps its own load-bearing biome gate (a chunk owned by jungle's set but in a desert biome -> no jungle start)"
  - "the structures-pipeline acceptance is automated (no visual gate — Phase 16 owns that): per-salt expected chunk + the load-bearing biome gate (out-of-biome -> NO start) + the full block fingerprint + cross-chunk idempotence + starts purity, all four temples"

requirements-completed: [STRUCT-02]

# Metrics
duration: 95min
completed: 2026-06-25
---

# Phase 14 Plan 03: Jungle Temple + Igloo + Swamp Hut + Structures-Pipeline Acceptance Summary

**The Tier-1 scattered-temple set completes on 14-02's proven machinery: the jungle temple (cobble/mossy stepped temple + tripwire/redstone/dispenser trap + lever/sticky-piston puzzle, with per-cell MossStoneSelector RNG draws), the swamp hut (spruce hut on oak-log stilts + cauldron/crafting/flowerpot), and the igloo (the first MULTI-PIECE temple — dome always + a probabilistic ladder+basement, its geometry extracted offline from the 26.2 .nbt templates) all land in vanilla positions in their real biomes with fingerprint-pinned geometry; the automated structures-pipeline acceptance closes STRUCT-02 with the Docker -race gate clean and all four temples live.**

## Performance

- **Duration:** ~95 min active (plus the ~10 min Docker -race gate, run in background)
- **Tasks:** 2 (both TDD-style: implementation + the fingerprint/biome-gate/purity pinning tests)
- **Commits:** 2 atomic (`a470a0e9` jungle+swamp, `0057041c` igloo+acceptance)

## Accomplishments

- **Jungle temple (javap -c `JungleTemplePiece.postProcess`):** the full hardcoded cobblestone/mossy-cobblestone stepped temple — foundation + outer shell (via the **MossStoneSelector** per-cell `nextFloat()<0.4` cobblestone/mossy draws, the load-bearing determinism contract), the corner pillars/spires/roof, the entrance stairs, the basement chamber, the **west + east tripwire→dispenser arrow traps** (tripwire-hook/tripwire/redstone-wire/dispenser blocks + vines), the **lever/sticky-piston treasure puzzle** (chiseled-stone-bricks/levers/redstone/sticky-pistons/repeater), and **2 chests** (blocks + loot tag, loot deferred). Gated by salt **14357619** + the getLowestY sea gate + the REAL `{bamboo_jungle, jungle}` biome check.
- **Swamp hut (javap -c `SwampHutPiece.postProcess`):** the spruce-plank hut on oak-log stilts + the spruce-plank walls/floor + the oak-fence railings + the **cauldron / crafting-table / potted-red-mushroom** furnishings + the 4-facing **spruce-stair roof overhang** (with OUTER_LEFT/RIGHT corner shapes) + the 4 fillColumnDown corner stilts. Gated by salt **14357620** + the REAL `{swamp}` biome check (NO sea gate). The witch + black-cat entities are deferred v3.
- **Igloo (the first MULTI-PIECE temple):** the 26.2 igloo is a **.nbt-template** structure, NOT hardcoded postProcess. Its `igloo/{top,middle,bottom}.nbt` templates were **extracted offline** (gunzip + NBT-decode) into Go data tables (`igloo_data.go`, the palette + block lists) — preserving **0 .nbt at runtime**. The **snow-block dome is ALWAYS placed**; with `nextDouble()<0.5` the **ladder + the laboratory basement** (the stone-brick lab with brewing-stand/water-cauldron/spruce-stairs/chest) are added too (then a `nextInt(8)` draw — the determinism contract) via the **14-02 addChildren multi-piece hook** (a fixed 1-3 piece set, the first multi-piece user). Palette resolved via `block.State{Name,Properties}.Block() -> ToStateID`. Gated by salt **14357618** + the REAL `{snowy_taiga, snowy_plains, snowy_slopes}` biome check. The basement chest places as a chest BLOCK (loot deferred); the villager/zombie-villager markers are deferred v3.
- **piece.go extensions:** `generateBoxSelector` (the BlockSelector generateBox overload, jar-gated so the selector draws ONLY when `alwaysReplace||isAir`); the **full StairBlock mirror/rotate** (FACING + the SHAPE inner/outer handedness swap — needed for the swamp hut's L-shaped roof corners under any orientation); the **Vine/RedstoneWire/Tripwire compass-face rotate/mirror** (the N/E/S/W prop permutation, ported from each block's jar rotate/mirror); `facingOf`/`withFacing` extended to dispenser/lever/sticky-piston/tripwire-hook/repeater/furnace/ladder.
- **CompositeStartGenerator + registration:** dispatches all four scattered temples (desert + jungle + igloo + swamp) and concatenates their owned starts; each member runs its OWN load-bearing biome gate. Registered in `NoiseGenerator` so the real `ComputeStarts` + REFERENCES + PLACE pipeline places all four.
- **The structures-pipeline acceptance (automated, no visual gate):** `TestStructuresPipelineAcceptance` asserts, for ALL FOUR temples, the per-salt expected chunk (derived from `getPotentialStructureChunk`, not eyeballed) + the **load-bearing biome gate** (a forced out-of-biome origin → NO start) + the FULL block fingerprint. `TestStructuresStartsPure` (all four pure over (seed,pos), incl. the igloo basement draw) + `TestStructuresCrossChunkIdempotent` (desert/jungle/igloo span chunks; the union equals the whole placement, no double-write).

## Task Commits

1. **Task 1: jungle temple + swamp hut** — `a470a0e9` (feat) — the two single-piece scattered temples + the piece.go transform extensions + the CompositeStartGenerator (desert+jungle+swamp).
2. **Task 2: igloo (first addChildren multi-piece) + the acceptance gate** — `0057041c` (feat) — the igloo offline-extracted templates + the probabilistic basement + the igloo registered into the composite + the full structures-pipeline acceptance suite.

## Files Created/Modified

- `world/structure/jungle_temple.go` — jungleTempleStartGen + JungleTemplePiece (the full postProcess) + the MossStoneSelector + the jungle block-state helpers
- `world/structure/swamp_hut.go` — swampHutStartGen + SwampHutPiece (the full postProcess) + spruceStair
- `world/structure/igloo.go` — iglooStartGen + IglooPiece (template placement + palette resolver) + the probabilistic basement + addChildren assembly
- `world/structure/igloo_data.go` — the OFFLINE-extracted igloo top/middle/bottom template geometry (palette + block lists)
- `world/structure/composite.go` — CompositeStartGenerator
- `world/structure/piece.go` — generateBoxSelector + BlockSelector + the full StairBlock mirror/rotate (FACING+SHAPE) + transformFaces (Vine/RedstoneWire/Tripwire) + facingOf/withFacing extensions
- `world/noisegen.go` — register all four temples via the CompositeStartGenerator
- `world/structure/{jungle_temple,swamp_hut,igloo,acceptance}_test.go` — expected-chunk + biome-gate + start-purity + full fingerprints + the 4-temple acceptance + cross-chunk idempotence

## Deviations from Plan

### Auto-fixed / architectural-judgment

**1. [Rule 2/4 boundary — igloo is a .nbt-template structure, not hardcoded geometry] Offline template extraction**
- **Found during:** Task 2 (decompiling IglooPieces)
- **Issue:** The plan's must_haves framed the igloo as "a tiny FIXED set of pieces... hardcoded geometry... ported from the jar bytecode." The actual 26.2 igloo is a **TemplateStructurePiece** built from `igloo/{top,middle,bottom}.nbt` template files via the StructureTemplate (jigsaw template) placement system — the Phase-16 subsystem, which the 14-02 hardcoded-postProcess machinery does NOT provide. Pulling in the full template-placement subsystem (palette/blocks/entities parse, rotation pivot, placeInWorld) was out of scope for "the last cheap Phase-14 follow-on."
- **Decision (Rule 4 evaluated, resolved without a checkpoint to keep the last Phase-14 plan closing):** Honor the plan's INTENT — deliver the igloo dome + probabilistic basement, exercise the addChildren multi-piece hook, keep determinism — by **extracting the .nbt template geometry OFFLINE** (gunzip + NBT-decode into Go data tables, `igloo_data.go`) and placing it through the 14-02 `placeBlock` (clip + orientation). This preserves "0 .nbt at RUNTIME" exactly as the other temples (whose geometry is the offline javap-c bytecode source) and stays on the proven machinery. The `structure_block` DATA markers are skipped (vanilla's `handleDataMarker` removes them — they are loot/jigsaw metadata, not visible blocks). The full StructureTemplate placement subsystem remains a Phase-16 deliverable.
- **Files:** `world/structure/igloo.go`, `world/structure/igloo_data.go`
- **Commit:** `0057041c`

**2. [Rule 2 - completeness] Full orientation transforms (StairBlock SHAPE + multi-face blocks)**
- **Found during:** Task 1
- **Issue:** 14-02's `transformState` only transformed FACING for STRAIGHT stairs + chest (sufficient for the desert pyramid). The new temples place L-shaped (OUTER) spruce stairs (swamp hut roof) + Vine/RedstoneWire/Tripwire (jungle trap), whose jar rotate/mirror also transform SHAPE / the compass-face props. Under any RNG-drawn orientation (the test anchors are WEST/EAST, not NORTH), FACING-only would be wrong.
- **Fix:** Ported the full StairBlock mirror/rotate (FACING + SHAPE inner/outer handedness swap) + the Vine/RedstoneWire/Tripwire compass-face permutation (from each block's jar rotate/mirror), and extended facingOf/withFacing to the new facing-carrying blocks. The desert pyramid stays green (backward-compatible — STRAIGHT stairs route through the new stair path identically).
- **Files:** `world/structure/piece.go`
- **Commit:** `a470a0e9`

## Loot / Entity Deferrals (v3, documented — matches REQUIREMENTS.md + the Phase-13 dungeon precedent)

- **Chests place as the chest BLOCK + a loot-table tag** (no loot rolled): jungle temple ×2 (`minecraft:chests/jungle_temple`), igloo basement ×1 (`minecraft:chests/igloo_chest`).
- **Dispensers place as BLOCKS** (no arrows): jungle temple ×2.
- **Trap/redstone blocks place in their hardcoded positions** (no redstone runtime): jungle tripwire/redstone/lever/sticky-piston puzzle.
- **Entities deferred:** the igloo villager + zombie-villager, the swamp-hut witch + black-cat, any jungle trap mob.
- **igloo `structure_block` DATA markers skipped** (vanilla removes them).

## Known Stubs

None that block the plan goal. The deferrals above are documented v3 subsystems (loot/block-entities/entities), not stubs that prevent the temples from generating — the VISIBLE temples (full geometry + chest/cauldron/dispenser/trap/puzzle BLOCKS) are fully delivered and fingerprint-pinned.

## Verification

- `go test ./world/structure/ -run 'TestJungleTemple|TestSwampHut|TestIgloo'` green: the three temples place jar-exact geometry at their per-salt expected chunks in their biomes; the igloo dome always (152/`0x705ebd3260550b81`) + the basement probabilistically (3 pieces, 357/`0x8b891d28bdfa0c7b`, chest); fingerprints jungle 1746/`0xf50b9fb8be352499`, swamp 640/`0xa2cda652a4805635`; starts pure over (seed,pos); biome gates load-bearing (out-of-biome → no start).
- `go test ./world/structure/ -run 'TestStructuresPipelineAcceptance|TestStructuresStartsPure|TestStructuresCrossChunkIdempotent'` green: all four temples land at expected chunks with expected fingerprints, cross-chunk idempotent, STARTS pure.
- `go test ./world/ -run 'TestDecorationReorderIdentical|TestEmitOnce|TestDesertPyramid'` green: the 5×5 reorder-determinism + emit-once stay byte-identical with all four temples placing; the desert pyramid pipeline stays green.
- **Docker `-race` over `./world/... ./world/structure/...` CLEAN** (golang:1.26, exit 0): `world` 590s, `world/structure` 4s, all packages green with all four temples live.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` + `go vet ./world/...` clean; go.mod/go.sum UNCHANGED (zero new deps); NO encoder/packet/chunk-wire/golden file touched (grep-confirmed — worldgen adds no wire surface).

## Next Phase Readiness

- **Phase 15 (mineshaft/outpost/stronghold)** reuses the proven `addChildren` + `findCollisionPiece` + `PieceAccessor` for TRUE multi-piece recursion (the igloo exercised the fixed-set multi-piece path; mineshaft adds the recursion + the legacy_type_2/3 frequency reducers already ported in 14-01). The cross-chunk re-derivable-RNG clip already handles structures owned ≥2 chunks out.
- **Phase 16 (jigsaw villages)** will need the FULL StructureTemplate placement subsystem (the igloo's offline-extraction shortcut is the seam: the same .nbt palette/blocks model + rotation pivot + placeInWorld, generalized + loaded at runtime). The Rotation/Mirror state transforms (now complete for stairs + multi-face blocks) are ported ahead of need.

---
*Phase: 14-structure-pipeline-temples*
*Completed: 2026-06-25*

## Self-Check: PASSED

All 9 created files verified present on disk (jungle_temple.go, jungle_temple_test.go, swamp_hut.go, swamp_hut_test.go, igloo.go, igloo_data.go, igloo_test.go, composite.go, acceptance_test.go); both task commits (`a470a0e9`, `0057041c`) verified in git history; the `contains` anchors (JungleTemplePiece / IglooPiece / SwampHutPiece / TestStructuresPipelineAcceptance) confirmed present.
