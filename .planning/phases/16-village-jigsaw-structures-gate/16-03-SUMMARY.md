---
phase: 16-village-jigsaw-structures-gate
plan: 03
subsystem: testing
tags: [structures, village, jigsaw, acceptance, phase16-acceptance, all-structures-place, real-pipeline, cross-chunk, visual-gate, protocol-776, STRUCT-05, STRUCT-06]

# Dependency graph
requires:
  - phase: 16-01
    provides: "the .nbt StructureTemplate system (LoadTemplate + PlaceInWorld + Jigsaws + the palette resolver) the round-trip criterion re-asserts; the plains_small_house_1 template fixture (size [7,7,7], palette 24, blocks 343, NONE fp 326779301212395908)"
  - phase: 16-02
    provides: "the bounded-BFS JigsawPlacement.Placer (the three load-bearing bounds: maxDepth/size, max_distance_from_center 80, VoxelShape collision) + villageStartGen (salt 10387312/spacing 34/separation 8, the 5 weighted biome variants) registered into the CompositeStartGenerator"
  - phase: 15-03
    provides: "the phase15_acceptance shape mirrored here: algorithm-derived per-structure chunks, the placeStartFingerprint/placeStartBlockCount/assertCrossChunkIdempotent helpers, the p15Sampler oracle, the -timeout 1800s Docker -race finding"
  - phase: 14-03
    provides: "the structures-pipeline acceptance pattern: per-salt PotentialStructureChunk derivation, the fixed surface/biome oracle, the load-bearing biome gate, the temple fingerprints reused in TestAllStructuresPlace"
provides:
  - "TestPhase16Acceptance: every STRUCT-05/06 criterion as an algorithm-derived assertion (.nbt round-trip, Placer terminates+bounds, village places + deterministic fingerprint, cross-chunk idempotence, biome gate load-bearing)"
  - "TestAllStructuresPlace: EVERY v2 structure places at its algorithm-derived chunk with its pinned fingerprint in one pass — desert pyramid + jungle temple + igloo + swamp hut + mineshaft + stronghold + the 5 village biome variants"
  - "world/village_pipeline_test.go: TestVillagePlacesInPipeline + TestVillageCrossChunkIdempotent driving the REAL NoiseGenerator.Decorate pipeline (router + GetBiome + STARTS + REFERENCES + PLACE) for the seed-25 near-spawn snowy village, dirt_path as the clean village-street signature"
  - "visual_gate_coords_test.go: the VISUAL GATE coordinate print (seed 25) for the Task-2 human-verify gate — the village chunk + the stronghold ring re-derived from the placement algorithm"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the fold-back-aware no-overlap assertion: the jigsaw Placer DELIBERATELY places small filler/decoration pieces INSIDE a parent building's XZ footprint (the jar's localFree shape — a connection point inside the parent box expands against the parent only). So criterion-2's 'no two pieces overlap' is asserted as: no two INDEPENDENT-sibling boxes strict-overlap; a strict-overlap is allowed ONLY when one box's XZ footprint fully contains the other's (a fold-back child). The seed-0 plains village has exactly 5 such fold-backs (root [0] x five 1x3x1 children), all jar-faithful, NOT a bug."
    - "the village BFS depth bound is depth<=size+1, not depth<=size: a piece at depth==size (6) attaches only fallback/terminators; its children get genDepth size+1 but are NOT enqueued for further expansion (the `depth+1 <= maxDepth` enqueue guard). So the max genDepth is the boundary terminators at size+1 — asserted as <= villageSize+1."
    - "dirt_path as the village-pipeline signature: dirt_path (village street) is placed ONLY by village street pieces, never by ambient terrain (probed: 0 in a far chunk, 89 in the seed-25 village owner). A clean structure-exclusive 'did a village land' probe over the real Decorate output, distinct from a generic non-air count (which is dominated by terrain) — the mirror of the desert pyramid's sandstone-mass probe."
    - "the visual-gate seed (25) is DISTINCT from the acceptance anchor seed (0): seed 0's plains village at chunk (15,2) is the ROUTER-INDEPENDENT acceptance anchor (flat sampler + forced biome — pure over (seed,pos)); seed 25 is the LIVE-CLIENT gate anchor because its snowy village biome-gates IN 6 chunks from spawn under the REAL router (a near-spawn /tp target the human can reach)."

key-files:
  created:
    - world/structure/phase16_acceptance_test.go
    - world/structure/visual_gate_coords_test.go
    - world/village_pipeline_test.go
  modified: []

key-decisions:
  - "TEST-ONLY: no production code changed. 16-01 (.nbt templates) + 16-02 (Placer + villageStartGen) already landed every behavior; this plan is the acceptance + real-pipeline gate. No bug surfaced — the 5 strict-overlaps in the seed-0 village were INVESTIGATED (probe) and confirmed jar-faithful fold-back pieces, not a Placer collision bug, so no Rule-1 fix was needed (the assertion was written to honor the localFree fold-back semantics rather than forcing a false 'zero overlaps' invariant)."
  - "ALGORITHM-DERIVED chunks throughout: every expected chunk derives FROM the placement math — the temples via PotentialStructureChunk(per-salt), the mineshaft via ApplyFrequencyReducer(legacy_type_3 0.4%), the stronghold via RingPositions(), the village via IsStructureChunk(salt 10387312). The fingerprints are pinned (the completeness oracle); the village-variant fingerprints were derived by forcing each of the 5 biomes at the seed-0 anchor (router-independent)."
  - "VISUAL GATE seed 25 chosen by probing seeds 0..59 for the nearest-spawn village via the LIVE pipeline: seed 25 lands a SNOWY village at chunk (1,6) ~ world (24,104), dist 6 — plus an igloo (temple, dist 18), a mineshaft (dist 7), and the nearest stronghold ring (dist 86). The desert pyramid/jungle temple for seed 25 are 4000+ blocks out (printed but flagged DISTANT); the igloo covers the temple tier near spawn."

patterns-established:
  - "fold-back-aware piece-overlap assertion for any jigsaw structure: assert no INDEPENDENT-sibling strict-overlap, allow XZ-containment fold-backs (the localFree case) — the correct jar-faithful reading of the VoxelShape collision contract"

requirements-completed: [STRUCT-06]  # STRUCT-05 completed by 16-01+16-02; STRUCT-06 VISUAL GATE APPROVED 2026-06-25 (seed 25, real client confirmed villages + temples + mineshafts + strongholds reproduce per seed).

# Metrics
metrics:
  duration: ~75min
  tasks: 1  # Task 1 of 2 complete; Task 2 (visual gate) PENDING human-verify
  files-created: 3
  files-modified: 0
  completed: 2026-06-25
---

# Phase 16 Plan 03 (Task 1): Full Structures Acceptance + Real-Pipeline Village Summary

**The automated v2-closing acceptance lands: TestPhase16Acceptance pins every STRUCT-05/06 criterion as an algorithm-derived assertion, TestAllStructuresPlace asserts ALL 11 v2 structures place at their algorithm-derived chunks in one pass, and the real-pipeline village tests prove the data-driven jigsaw village generates through the full production Decorate path — all 7 verification gates green, with the Task-2 VISUAL GATE staged (coords printed) and PENDING human-verify.**

> STATUS: COMPLETE. Task 1 (automated acceptance) committed. Task 2 (the `autonomous:false`, blocking VISUAL GATE) APPROVED by the user 2026-06-25 on seed 25 — a real vanilla 26.2 client confirmed the snowy village + temples + mineshafts + strongholds generate in vanilla positions and reproduce per seed. STRUCT-06 met; milestone v2 CLOSED.

## Performance

- **Duration:** ~75 min active (plus the ~17 min Docker -race gate, run in background)
- **Tasks:** 1 of 2 complete (Task 2 = the human visual gate, PENDING)
- **Files created:** 3 (all test-only)
- **Commits:** 1 atomic (`e2243be1`)

## Accomplishments

- **TestPhase16Acceptance** — the 5 STRUCT-05/06 criteria, each an algorithm-derived assertion:
  1. **.nbt round-trip:** `plains_small_house_1` parses to size [7,7,7] / palette 24 / blocks 343 and places the pinned NONE fingerprint (343, 326779301212395908) — 16-01's contract at the acceptance level.
  2. **Placer terminates + bounds:** the seed-0 plains village (146 pieces, well under the 1000 cap) — every piece box within max_distance 80 of center (Chebyshev), the BFS depth <= size+1, and **no two INDEPENDENT-sibling boxes strict-overlap** (the only 5 strict-overlaps are root-fold-back filler children, allowed via XZ-containment — jar-faithful).
  3. **A village places:** chunk (15,2) verified as the salt-10387312 algorithm structure chunk; the placed-village fingerprint is determinism-stable + pinned (0xcd3845c885976c04).
  4. **Cross-chunk idempotence:** the village spans >=2 chunks; the union of per-chunk placeInChunk slices == the whole placement (the shared assertCrossChunkIdempotent helper).
  5. **Biome gate load-bearing:** an ocean origin -> 0 villages (no accept-by-default leak).
- **TestAllStructuresPlace** — every v2 structure at its algorithm-derived chunk with its pinned fingerprint, in one pass:
  - desert pyramid (57050/0x02570922c9bdc1bf), jungle temple (1746/0xf50b9fb8be352499), igloo (357/0x8b891d28bdfa0c7b), swamp hut (640/0xa2cda652a4805635) — the 14-03 anchors;
  - mineshaft (legacy-freq reducer chunk, deterministic graph + blocks), stronghold (ring chunk, exactly 1 PortalRoom, deterministic graph) — the 15-03 structures;
  - the 5 village variants at the (15,2) anchor, each forced into its biome: plains (5768/0xcd3845c885976c04), desert (3588/0x433db86bbc8929c9), savanna (7444/0x583d448a5a5d5334), snowy (11851/0x473a285f3db82b03), taiga (6278/0xbd5888cd0b4c30ca).
- **world/village_pipeline_test.go** — the village half of the structures-pipeline close, mirroring TestDesertPyramidPlacesInPipeline:
  - `TestVillagePlacesInPipeline`: the seed-25 snowy village places 89 dirt_path (village-street) blocks in its owner chunk (1,6) via the FULL production pipeline; a far chunk has 0 (the signature is village-exclusive).
  - `TestVillageCrossChunkIdempotent`: re-decorating the owner is byte-identical (re-derivable RNG + position-clipped writes); the overlapping neighbor (2,6) receives its own village slice.
- **The determinism gates stay byte-identical WITH villages live** — `TestDecorationReorderIdentical` (the 5x5 reorder) + `TestEmitOnce` + `TestEmitOnceUnderHold` all green.
- **visual_gate_coords_test.go** prints the Task-2 gate coordinates (below), the village chunk + the stronghold ring re-derived from the placement algorithm.

## THE VISUAL GATE COORDINATES (for Task 2 — the human-verify gate)

The gate seed is **25** (a snowy village 6 chunks from spawn). Travel to these world (X,Z) — use `/tp` or creative flight. Y is the surface; the stronghold/mineshaft are underground (dig/spectator down):

```
VISUAL GATE seed: 25, village (snowy)              at chunk (1,6)    ~ world (24,104)
VISUAL GATE seed: 25, igloo (temple)               at chunk (-9,-18) ~ world (-136,-280)
VISUAL GATE seed: 25, mineshaft (underground)      at chunk (7,-4)   ~ world (120,-56)
VISUAL GATE seed: 25, stronghold (deep underground) at ring chunk (-9,-86) ~ world (-136,-1368)
VISUAL GATE seed: 25, desert pyramid (DISTANT)     at chunk (310,271) ~ world (4968,4344)
```

- **Village (snowy)** — confirm houses, paths, a well, farms, lamp posts; snowy variant = spruce/snow. NO villagers / EMPTY chests are EXPECTED (the v3 deferral) — verify the BUILDINGS.
- **Igloo** — the snow-block dome (+ possibly a ladder/basement) in the snowy biome near spawn (the near-spawn temple-tier structure for this seed).
- **Mineshaft** — wooden supports, rails, cobwebs, underground around y~40 under world (120,-56).
- **Stronghold** — stone-brick corridors + the end_portal_frame ring + the silverfish spawner, deep underground; ~1368 blocks south (the nearest ring position).
- **Desert pyramid** — DISTANT (4968,4344); printed for completeness but ~6600 blocks out; the igloo covers the near-spawn temple tier.
- **DETERMINISM:** regenerate the same seed (fresh world dir, seed 25) and confirm the same structures at the same positions.

The gate PASSES on the village (snowy, reachable) + at least one confirmation per prior-tier structure (igloo/mineshaft/stronghold), reproducible on the same seed.

## Task Commits

1. **Task 1 (full structures acceptance + real-pipeline village):** `e2243be1` (test) — TestPhase16Acceptance (the 5 criteria), TestAllStructuresPlace (all 11 v2 structures), the village pipeline tests, the visual-gate coord print.

_Task 2 is the human VISUAL GATE — no executor commit; STRUCT-06 / v2 close on the human "approved"._

## Files Created/Modified

- `world/structure/phase16_acceptance_test.go` — TestPhase16Acceptance (5 criteria) + TestAllStructuresPlace (11 structures) + the chebyshev/xzContains/templeSalt/placeVillageFingerprint helpers.
- `world/village_pipeline_test.go` — TestVillagePlacesInPipeline + TestVillageCrossChunkIdempotent (real Decorate pipeline, seed 25) + countDirtPath.
- `world/structure/visual_gate_coords_test.go` — TestVisualGateCoords (prints + lightly re-derives the gate coords).

## Decisions Made

- **TEST-ONLY, no production change.** 16-01+16-02 already deliver the behavior; this is the acceptance gate. The investigation of the 5 seed-0 village overlaps (a probe confirmed them as jar-faithful root-fold-back filler pieces, the localFree case) meant the no-overlap assertion was written to honor the fold-back semantics, NOT to force a false zero-overlap invariant — so no Rule-1 fix was warranted.
- **Visual-gate seed 25, distinct from the acceptance seed 0** — seed 0's (15,2) plains village is the router-independent acceptance anchor; seed 25's snowy village biome-gates in near spawn under the real router (a reachable /tp target).

## Deviations from Plan

None — Task 1 executed exactly as written (test-only, algorithm-derived, all 7 gates green). No real bug surfaced (the village overlaps were investigated and confirmed correct, not a bug — see Decisions).

## Issues Encountered

- **The 5 strict-overlaps in the seed-0 village looked like a collision-bound violation** at first. A probe (root [0] x five 1x3x1 children, all inside the root's XZ box) confirmed they are the jar's deliberate localFree fold-back (decoration/filler pieces inside a building). Resolved by writing the assertion to allow XZ-containment fold-backs and reject only independent-sibling overlaps — the jar-faithful reading. No code change.
- **No near-spawn desert pyramid/jungle temple for any low gate seed** — seed 25's are 4000+ blocks out. Resolved by using the igloo (a near-spawn snowy temple) for the temple-tier gate confirmation and flagging the desert pyramid coords as DISTANT.

## Verification

All 7 gates GREEN:
1. `go build ./...` — exit 0.
2. `CGO_ENABLED=0 go build ./...` — exit 0.
3. `go vet ./world/...` — clean.
4. `go test ./world/structure/ -run 'TestPhase16Acceptance|TestAllStructuresPlace' -count=1` — PASS (1.9s).
5. `go test ./world/ -run 'TestVillage|TestDecorationReorderIdentical|TestEmitOnce' -count=1` — PASS (the village pipeline tests + the 5x5 reorder + emit-once, all green WITH villages live).
6. `cd tools && go build ./...` — exit 0.
7. **Docker `-race` CLEAN** on golang:1.26 with `-timeout 1800s`: `world` `ok ... 1008.541s` (the village live-pipeline tests add real recursive work atop the 15-03 834s baseline; a timeout-band cost, NOT a race — no DATA RACE marker), `world/structure` 33.966s, all 13 packages green.

`go.mod`/`go.sum` (+ `tools/`) UNCHANGED — zero new deps. No encoder/packet/chunk-wire/golden file touched (grep-confirmed — worldgen adds no wire surface, T-16-07 accept). Only 3 test files added.

## Loot / Entity Deferrals (v3, documented — unchanged from 14/15/16-01/16-02)

The village places the BUILDINGS (houses, paths, wells, farms, lamps) but NO villagers and EMPTY/absent chest loot — the documented v3 deferral. The visual gate verifies the structures, not the inhabitants.

## Known Stubs

None that block the plan goal. The loot/villager deferrals above are documented v3 subsystems, not stubs preventing generation — the visible v2 structure suite (the 5 village variants + the temples + mineshafts + strongholds) is fully delivered, fingerprint-pinned, and real-pipeline-verified.

## Next Phase Readiness

- **Task 2 (THE VISUAL GATE) is the only remaining work** — the orchestrator presents the blocking human-verify gate with the coords above. On "approved", STRUCT-06 completes and milestone v2 CLOSES.
- v3 owns the loot/villager/block-entity subsystems (the documented deferrals) — out of v2 scope.

---
*Phase: 16-village-jigsaw-structures-gate*
*Completed (Task 1): 2026-06-25 — Task 2 VISUAL GATE PENDING human-verify*

## Self-Check: PASSED
