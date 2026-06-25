---
phase: 15-mineshaft-stronghold
plan: 03
subsystem: worldgen-structures
tags: [structures, stronghold, stronghold-pieces, recursive-pieces, addchildren, findcollisionpiece, gendepth, portal-room, library, smoothstoneselector, end-portal-frame, cross-chunk, phase15-acceptance, protocol-776, STRUCT-04, STRUCT-03]

# Dependency graph
requires:
  - phase: 14-02
    provides: "the StructurePiece machinery (placeBlock cross-chunk clip, generateBox/generateBoxSelector/fillColumnDown/createChest, the BlockSelector per-cell chooser, PieceAccessor + FindCollisionPiece, StructureStart.placeInChunk + the re-derivable piece RNG)"
  - phase: 15-01
    provides: "the recursive multi-piece pattern (builder + child-context + generateAndAddPiece + FindCollisionPiece + the genDepth bound) the stronghold reuses verbatim; the cross-chunk clip proven on a genuinely-many-chunk structure; the SOUTH/dir-identity orientation lesson (writes must stay in-bbox or placeInChunk loses them)"
  - phase: 15-02
    provides: "the StrongholdRingState ring anchor + isPlacementChunk; the strongholdStartGen placement-half (piece-stubbed) this plan FILLS; the generateRingPositions algorithm the acceptance references"
provides:
  - "the recursive StrongholdPieces: StartPiece/Corridor(Straight)/Turn(Left,Right)/RoomCrossing/StraightStairsDown/StairsDown(spiral)/FiveCrossing/ChestCorridor/Prison/Library/PortalRoom on the 15-01 addChildren+FindCollisionPiece+genDepth(50)-bounded recursion"
  - "the weighted PieceWeight table + per-kind maxPlaceCount (exactly one PortalRoom — forced when the recursion doesn't draw one); the SmoothStoneSelector per-cell stone-brick-variant draw (the determinism contract)"
  - "the PortalRoom (12-frame end_portal_frame ring + silverfish spawner block + lava moat + iron bars) + the Library (bookshelf grid + smooth_stone_slab + ladder + chest block+tag) as VISIBLE geometry"
  - "the FILLED strongholdStartGen: assembleStronghold builds the real piece tree on a ring chunk + RecomputeBBox — the byte-inert 15-02 stub replaced by real blocks"
  - "the full Phase-15 acceptance (TestPhase15Acceptance): every STRUCT-03/04 criterion as an algorithm-derived assertion + a real-pipeline placement per structure"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "the stronghold reuses the 15-01 recursion verbatim with a stronghold-scoped builder/context/factory (renamed shGenerateAndAddPiece / strongholdChildPiece to avoid the mineshaft symbol collision in the same package): each piece's AddChildren proposes children at its doorways via the weighted+maxPlaceCount factory, FindCollisionPiece-prunes overlaps, genDepth(50)-bounds, recurses"
    - "the in-bbox-write discipline as a placeLocal guard: every stronghold decoration write is clipped to the piece bbox (placeLocal drops out-of-bbox local coords) + fillShell clamps its extents to the bbox dims — so placeInChunk's bbox-intersect reproduces every write per overlapping chunk (the cross-chunk clip). This generalizes the 15-01 stairs-orientation lesson into a reusable guard."
    - "IDEMPOTENT PostProcess (no per-call mutable state): the first cut had the PortalRoom set a hasPlacedSpawner flag, which made re-placement (and per-chunk placeInChunk) non-idempotent + the determinism fingerprint non-stable — removed. PostProcess must be a pure function of (piece, rng-seed, box); placeLocal's clip handles the once-per-chunk write."

key-files:
  created:
    - world/structure/stronghold_pieces.go
    - world/structure/stronghold_pieces_test.go
    - world/structure/phase15_acceptance_test.go
  modified:
    - world/structure/concentric_rings.go
    - world/structure/concentric_rings_test.go
    - world/structure/piece.go

key-decisions:
  - "GENDEPTH CAP 50 (strongholdGenDepthCap — the jar StrongholdPieces.generatePieceFromSmallDoor `if (genDepth > 50) return null` bound) + a defensive 1000-piece total cap + a Y window [10,200]: the twisting-corridor recursion TERMINATES. TestStrongholdTerminates pins a bounded piece count + no-overlap across 7 seeds."
  - "EXACTLY ONE PORTALROOM (maxPlaceCount=1): the PieceWeight table draws the weighted children (Straight 40 / Turns 20+20 / RoomCrossing 10 / Library 10 / the stairs+crossings+chest 5 each, each with its jar maxPlaceCount); the PortalRoom is NOT in the weighted table — it is FORCED off an existing piece's free doorway when the recursion finishes without one (assembleStronghold.forcePortalRoom). TestStrongholdExactlyOnePortalRoom pins n==1 across 8 seeds."
  - "THE SMOOTHSTONESELECTOR is the per-cell determinism contract: the shell edges draw nextFloat() -> mossy(<0.2) / cracked(<0.5) / infested(<0.55) / stone_bricks(else); interior cells return air. generateBoxSelector(alwaysReplace=true) fires the draw per cell in the jar y->x->z order, so the per-cell draw count + order is fixed. TestStrongholdFingerprint pins the placed-graph FNV hash is determinism-stable; a reorder/wrong-weight flips it."
  - "PORTALROOM EYES + SPAWNER + LOOT DEFERRED v3 (documented inline, matching the Phase-14/15-01 precedent): the 12 end_portal_frame blocks place with the EYE flag drawn per the jar nextFloat()<0.1, but NO end-portal activation/lighting logic + NO EndPortal blocks; the silverfish spawner is the BLOCK only (no spawn logic); the corridor/crossing/library chests place as the chest BLOCK + the loot tag (minecraft:chests/stronghold_corridor|crossing|library), no loot rolled; iron/oak doors place LOWER+UPPER half blocks only (no block-entity open/powered)."
  - "THE FILLED START GEN: strongholdStartGen.GenerateStarts (15-02's piece-stub) now seeds the piece RNG via SetLargeFeatureSeed(seed,cx,cz), samples the surface Y, anchors the StartPiece spiral ~24 blocks underground (clamped >=20), runs assembleStronghold to the genDepth bound, RecomputeBBox, and returns the populated start — the byte-inert stub became real blocks (mirroring 14-02 filling 14-01's empty PLACE hook)."
  - "STAIRS REGISTRATION: StoneBrickStairs added to piece.go stairOf/withStair so the stronghold stair treads get the correct mirror/rotate SHAPE transform under the piece orientation (the temples only registered sandstone/cobblestone/spruce)."

patterns-established:
  - "the placeLocal bbox-clip guard for any future hardcoded-geometry piece whose decorations might exceed the shell: clip local writes to (0..MaxX-MinX, 0..MaxY-MinY, 0..MaxZ-MinZ) so the cross-chunk placeInChunk reproduces them — the robust generalization of the 15-01 orientation fix"

requirements-completed: [STRUCT-03, STRUCT-04]

# Metrics
metrics:
  duration: ~95min
  tasks: 2
  files-created: 3
  files-modified: 3
  completed: 2026-06-25
---

# Phase 15 Plan 03: Stronghold Pieces + Phase-15 Close Summary

The recursive **StrongholdPieces** land on 15-02's proven concentric-ring anchor + 15-01's proven recursion machinery, and **Phase 15 is CLOSED** with a goal-shaped automated acceptance. Per the research, once the mineshaft recursion (15-01) and the global ring placement (15-02) worked, the stronghold pieces were "more of the same" — the same `addChildren` / `FindCollisionPiece` / genDepth-bounded recursion over a richer piece set.

## What landed

**The piece set (`stronghold_pieces.go`)** — StartPiece (the spiral StairsDown root), Straight Corridor, Left/Right Turn, RoomCrossing, StraightStairsDown, the spiral StairsDown, the 5-way FiveCrossing, ChestCorridor, Prison (iron-bar cells), Library (bookshelf room), and the signature PortalRoom — each embedding the Phase-14 `StructurePiece` + a doorway/door-style + genDepth. The weighted `PieceWeight` table + per-kind `maxPlaceCount` drives the child distribution; `SmoothStoneSelector` bricks the shell (the per-cell stone_bricks/mossy/cracked/infested draw).

**The recursion** — `shGenerateAndAddPiece` is the genDepth-50-bounded, collision-checked, weighted child factory (jar `generatePieceFromSmallDoor`); each piece's `AddChildren` proposes children at its doorways. The graph TERMINATES (cap 50 + 1000-piece guard + Y window) and has EXACTLY ONE PortalRoom (forced when the recursion doesn't draw one).

**The signature rooms** — the PortalRoom places the 12-frame `end_portal_frame` ring (eyes per `nextFloat()<0.1`, no activation), the silverfish spawner block, the lava moat, the iron bars; the Library places the bookshelf grid + `smooth_stone_slab` walkways + the tall-variant ladder/second-floor + 1-2 chests (block + `minecraft:chests/stronghold_library` tag).

**The filled start gen** — `strongholdStartGen.GenerateStarts` (15-02's byte-inert stub) now builds the REAL piece tree via `assembleStronghold` + `RecomputeBBox`. The stronghold places real blocks.

**The Phase-15 close (`phase15_acceptance_test.go`)** — `TestPhase15Acceptance` maps every STRUCT-03/04 criterion to an algorithm-derived assertion: the mineshaft frequency chunk (from the 15-01-CORRECTED `ApplyFrequencyReducer`) + graph fingerprint; the ~128 ring positions vs `generateRingPositions` + `isPlacementChunk`; the recursive stronghold graph (one PortalRoom + fingerprint); both deterministic; both cross-chunk idempotent (union==whole, spans >=2 chunks); a real-pipeline placement per structure.

## Task Commits

1. **Task 1 (recursive StrongholdPieces + filled start gen):** `e7627f06` (feat) — the piece set, the weighted+capped recursion, the PortalRoom/Library geometry, the SmoothStoneSelector, the filled strongholdStartGen, StoneBrickStairs registration; the stronghold-pieces acceptance.
2. **Task 2 (full Phase-15 acceptance):** `75055882` (test) — `TestPhase15Acceptance` (every STRUCT-03/04 criterion + cross-chunk + real-pipeline), the FiveCrossing chest, dead-code cleanup.

## Deviations from Plan

**1. [Rule 1 - bug, found+fixed in-task] PortalRoom `hasPlacedSpawner` mutation broke determinism + cross-chunk idempotence**
- **Found during:** Task 1 (TestStrongholdFingerprint non-deterministic + TestStrongholdCrossChunk diverging at one cell).
- **Issue:** The PortalRoom set a `hasPlacedSpawner` flag in `PostProcess`, so the second placement (and every per-chunk `placeInChunk` after the first) skipped the spawner — making `PostProcess` non-idempotent. The graph fingerprint flipped between two identical placements and the cross-chunk union diverged.
- **Fix:** Removed the flag; `PostProcess` is now a pure function of (piece, rng-seed, box). `placeLocal`'s bbox-clip handles the once-per-chunk write. Pinned by the now-passing fingerprint + cross-chunk tests.
- **Files:** `world/structure/stronghold_pieces.go`. **Commit:** `e7627f06`

**2. [Rule 1 - bug, found+fixed in-task] Library/PortalRoom decorations wrote outside their bbox -> cross-chunk truncation**
- **Found during:** Task 1 (an OOB-probe showed the Library/PortalRoom writing past `MaxX/MaxZ`).
- **Issue:** Hardcoded local extents exceeded the piece bbox dims, so `placeInChunk` (which only places a piece into chunks its bbox intersects) lost the escaped writes — the 15-01 stairs lesson, recurring.
- **Fix:** Added a `placeLocal` guard (drop out-of-bbox local coords) routed through every decoration write, and clamped `fillShell` extents to the bbox dims. All writes now stay in-bbox; the cross-chunk clip reproduces them per chunk.
- **Files:** `world/structure/stronghold_pieces.go`. **Commit:** `e7627f06`

**3. [Rule 3 - blocking] Updated 15-02's `TestStrongholdStart` (the byte-inert-stub assertion)**
- **Found during:** Task 1 (the 15-02 test asserted `len(Pieces)==0` for the stub).
- **Issue:** 15-03 fills the pieces, so the "0 pieces / byte-inert" assertion was stale.
- **Fix:** Re-pointed the test to assert `len(Pieces)>=1` + a non-empty BBox (the 15-03 contract).
- **Files:** `world/structure/concentric_rings_test.go`. **Commit:** `e7627f06`

**4. [Rule 1 - perf, owned not masked] The `-race` `./world` suite exceeds the DEFAULT 600s timeout**
- **Found during:** Verification (the Docker `-race` gate with the plan's exact default-timeout command).
- **Issue:** The stronghold now does REAL recursive assembly during decoration (vs the 15-02 byte-inert stub), so the full `./world` suite under `-race` (10-20x instrumented) ran past the default 600s `go test` timeout. The failure was a **test timeout dump, NOT a `DATA RACE` report** (confirmed: no `DATA RACE`/`WARNING: DATA` marker in the output; the first frame was `decorateSingle` mid-run).
- **Resolution:** Re-ran `./world` under `-race` with `-timeout 1800s` — **`ok ... 834.641s`, race-CLEAN**. `./world/structure/` is race-clean at 14.3s. There is NO data race; the cost is the genuine recursive work (the stronghold ring spiral is already `sync.Once`-cached per generator). The non-race suite is healthy (the gate subset 33s; full suite exit 0). Documented here so the verifier raises the `-race` Docker timeout rather than reads the default-timeout failure as a race.
- **Files:** none (runtime characteristic). 

## Loot / Entity / Portal Deferrals (v3, documented)

- The `end_portal_frame` ring places the 12 frame blocks (eyes per `nextFloat()<0.1`) but NO portal activation/lighting + NO `EndPortal` blocks.
- The silverfish spawner is the BLOCK only (no spawn logic / mob-entity wiring).
- The corridor/crossing/library chests place as the chest BLOCK + the loot tag (`minecraft:chests/stronghold_corridor|crossing|library`), no loot rolled.

## Known Stubs

None that block the plan goal. The deferrals above are documented v3 subsystems, not stubs preventing generation — the visible stronghold (the full corridor/stairs/crossing/room geometry + the PortalRoom end_portal_frame ring + spawner + lava + the Library bookshelves + the chest blocks) is fully delivered and fingerprint-pinned.

## Verification

- `go test ./world/structure/ -run 'TestStronghold|TestPhase15Acceptance'` green: the recursive graph (genDepth 50, one PortalRoom) + the PortalRoom/Library geometry + the chest block+tag + the full Phase-15 acceptance (mineshaft + stronghold, deterministic, cross-chunk idempotent, real-pipeline).
- `go test ./world/ -run 'TestDecorationReorderIdentical|TestEmitOnce|TestStructuresPipelineAcceptance|TestMineshaft|TestStronghold|TestDesertPyramid'` green (33s): the 5x5 reorder + emit-once stay byte-identical WITH both Phase-15 structures live.
- `go test ./world/ ./world/structure/` full suites green.
- `go build ./...` + `CGO_ENABLED=0 go build ./...` + `go vet ./world/...` clean; go.mod/go.sum UNCHANGED (zero new deps); no encoder/packet/chunk-wire/golden file touched; no import cycle (world -> world/structure one-directional).
- **Docker `-race` CLEAN** on `golang:1.26`: `./world/structure/` 14.3s; `./world/` `ok ... 834.641s` (needs `-timeout 1800s` — exceeds the default 600s purely on the new recursive work, NOT a race; see Deviation 4).

## The Phase-15 close

STRUCT-03 (mineshaft) + STRUCT-04 (stronghold placement + pieces) are COMPLETE. The full Phase-15 acceptance asserts every success criterion against algorithm-derived references. Phase 16 owns the visual gate (no human-verify here).

---
*Phase: 15-mineshaft-stronghold*
*Completed: 2026-06-25*

## Self-Check: PASSED
