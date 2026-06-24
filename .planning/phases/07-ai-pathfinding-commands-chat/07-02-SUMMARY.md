---
phase: 07-ai-pathfinding-commands-chat
plan: 02
subsystem: ai
tags: [pathfinding, a-star, navigation, path-region, snapshot, walk-node-evaluator, java-port, async-seam, tick-owned]

# Dependency graph
requires:
  - phase: 06-entities-physics-interaction
    provides: "moveEntity (per-axis swept collision + onGround + re-bucket via entities.move), blockSolidAt, dimMinY, floorDiv/floorI; world.ChunkManager.GetBlock; entityStore (add/move/near + per-column buckets); the entityTracker that auto-broadcasts a moved mob"
  - phase: 07-ai-pathfinding-commands-chat
    provides: "07-01: Entity.ai *mobAI, mobAI (goalSelector + wantTarget/hasTarget seam), serverAiStep driver, newPigAI; the stroll goal that SETS mobAI.wantTarget"
provides:
  - "server/path_region.go — the ported PathNavigationRegion: an IMMUTABLE pathRegion (a copied block-solidity snapshot of the box around mob->target) + snapshotRegion built ON the tick from world.ChunkManager.GetBlock. The request->snapshot->result boundary (the Phase-8 hinge)."
  - "server/pathfinder.go — computePath(req pathRequest) *Path: the PURE A* (a BinaryHeap open set with O(log n) decrease-key, g=node.distanceTo Euclidean, h=getBestH*1.5 FUDGING, f=g+h, reach via distanceManhattan<=reachRange, the follow-range gate, and a maxVisited BUDGET). Reads ONLY req — no *TickLoop, no live world."
  - "server/node_evaluator.go — the ported WalkNodeEvaluator: getNeighbors (4 cardinals via findAcceptedNode with step-up/step-down + 4 clockwise diagonals), getPathType (WALKABLE/BLOCKED/OPEN), the malus table, isNeighborValid/isDiagonalValid (corner-cut rejection)."
  - "server/navigation.go — the ported groundNavigation: requestPath (snapshot -> computePath -> Path, the seam call) + tick (advance the path node -> desired Δ -> the EXISTING moveEntity) + shouldRecomputePath throttle."
  - "server/ai_mob.go — mobAI gains a `navigation groundNavigation`; serverAiStep CONSUMES the wantTarget (07-01 seam) -> requestPath (throttled) -> navigation.tick. A stroll goal's target becomes a WALKED A* path."
affects: [07-03-spawner-tickAI, 08-async-pathfinding, OPT-01, ai, pathfinding]

# Tech tracking
tech-stack:
  added: []  # zero new third-party deps; only stdlib math + container-free hand-rolled heap
  patterns:
    - "request->snapshot->result seam (THE Phase-8 hinge): build an IMMUTABLE snapshot ON the tick, then run a PURE compute (computePath) over ONLY the copy — no tick-owned state captured, so OPT-01 swaps only the executor"
    - "Java-port pattern: each ported symbol cites its net.minecraft.* source read via javap -c; algorithm + constants (malus table, FUDGING 1.5, maxVisited budget) translated to idiomatic Go (non-1:1, no GPL paste)"
    - "path-following through the existing collision substrate: navigation.tick produces a desired Δ fed to moveEntity (re-bucket + no clip); the unchanged entityTracker auto-broadcasts — no new entity encoder (Pattern 3)"
    - "DoS-bounded A*: a maxVisited budget (++visited; if visited >= cap break) + a recompute throttle (MAX_TIME_RECOMPUTE) so an unreachable target cannot blow the MSPT budget (Pitfall 6 / T-7-04)"

key-files:
  created:
    - "server/path_region.go — pathRegion (immutable solidity snapshot) + snapshotRegion"
    - "server/pathfinder.go — node, binaryHeap, Path, pathRequest, computePath (the PURE A*)"
    - "server/node_evaluator.go — pathType + malus, getPathType, findAcceptedNode, getNeighbors, isNeighborValid/isDiagonalValid"
    - "server/navigation.go — groundNavigation (requestPath/tick/shouldRecomputePath) + clampStep"
    - "server/pathfinder_test.go — 5 A* tests over hand-built snapshots (incl. TestComputePathPure + TestPathNodeBudget)"
    - "server/navigation_test.go — 4 path-follow + serverAiStep-integration tests"
  modified:
    - "server/ai_mob.go — added navigation groundNavigation to mobAI; serverAiStep consumes wantTarget -> requestPath -> navigation.tick; newPigAI sets navigation.speed (pigWalkSpeed)"

key-decisions:
  - "THE PURITY CONTRACT (the Phase-8 hinge): computePath(req pathRequest) takes ONLY the request — no *TickLoop, no live entityStore/ChunkManager. The snapshot (req.region) is the immutable value built ON the tick; the A* runs over the copy. TestComputePathPure builds a snapshot by hand (no world) and gets a Path, proving it. In Phase 8 (OPT-01) the same pathRequest is handed to an ants pool, the Path rejoins via applyAsyncResults — ZERO logic change."
  - "A* shape ported EXACTLY from PathFinder.findPath bytecode: g = node.distanceTo (Mth.sqrt Euclidean) accumulated as parent.g + edge + neighbor.costMalus; h = getBestH (min distanceTo over targets) * 1.5f (the FUDGING constant); f = g + h; reach test = distanceManhattan(node, target) <= reachRange; the follow-range gate skips a popped node with distanceTo(start) >= followRange and a neighbor with walkedDistance >= followRange."
  - "maxVisited BUDGET ported from GroundPathNavigation: PathFinder ctor maxVisited = (int)(getMaxPathLength()*16.0) where getMaxPathLength = max(FOLLOW_RANGE attr, requiredPathLength) = 16 for a Pig; searchDepthMultiplier = maxVisitedNodesMultiplier (jar DEFAULT 0.5f). Effective cap = 16*16*0.5 = 128 expansions. The loop is `++visited; if visited >= cap break` (the check BEFORE the pop), so visited never exceeds the cap (Pitfall 6 / T-7-04)."
  - "The malus table is the jar's PathType static{} values: BLOCKED -1.0, OPEN 0.0, WALKABLE 0.0, FENCE -1.0, LAVA -1.0, WATER 8.0. v1 ports the BLOCKED/OPEN/WALKABLE subset (water/lava/fence/door deferred per A5 — superflat is flat stone). A negative malus = impassable; isNeighborValid rejects it."
  - "PathNavigationRegion's out-of-box read is treated as SOLID (a barrier), so the A* frontier never walks off the copied snapshot into uncopied space. The box is sized targetPos +/- (followRange + accuracy) per vanilla createPath, comfortably containing a valid path, so the edge-as-solid policy only forbids leaving the snapshot."
  - "Path-following collapses vanilla's moveControl.setWantedPosition -> aiStep -> travel -> move chain into a direct desired-Δ fed to the EXISTING moveEntity (Pattern 3). The mob never clips a wall and re-buckets (Pitfall 5); the unchanged tracker auto-broadcasts. Gravity is applied by tickPhysics AFTER tickAI — the pipeline order is preserved."

patterns-established:
  - "Immutable snapshot + pure compute: the single discipline that makes async pathfinding (Phase 8) a swap of the executor, not a rewrite"
  - "Faithful A* with a hand-rolled index-tracked binary heap mirroring vanilla's BinaryHeap (heapIdx stored on the node for O(log n) changeCost/decrease-key)"

requirements-completed: [AI-02]

# Metrics
duration: 41min
completed: 2026-06-24
---

# Phase 7 Plan 02: Synchronous A* Navigation Port (AI-02) Summary

**Ported the vanilla 26.2 PathNavigationRegion snapshot + PathFinder A* + WalkNodeEvaluator + GroundPathNavigation path-following into idiomatic Go — structured as the EXACT request->snapshot->result seam vanilla itself uses, so `computePath` is a PURE function of an immutable snapshot (the Phase-8 hinge), executed INLINE on the tick, with the mob walking its path through the existing moveEntity. All jar-confirmed via javap -c, Docker -race clean, zero new deps.**

## Performance

- **Duration:** ~41 min
- **Tasks:** 2 (both TDD)
- **Files created:** 6 (4 implementation, 2 test)
- **Files modified:** 1 (ai_mob.go — the navigation field + the serverAiStep wiring)

## Accomplishments

- **The request->snapshot->result seam (THE Phase-8 hinge, 07-RESEARCH Pitfall 1 / T-7-09):** `computePath(req pathRequest) *Path` reads ONLY the request — no `*TickLoop`, no live `entityStore`/`ChunkManager`. `snapshotRegion` COPIES world solidity ON the tick into an immutable `pathRegion`; the A* runs over the copy. `TestComputePathPure` builds a snapshot by hand (no world at all) and gets a Path — the structural proof that OPT-01 can hand the same `pathRequest` to an off-tick `ants` pool with ZERO logic change.
- **The A* core** ported from `PathFinder.findPath` bytecode: a hand-rolled index-tracked binary heap (mirroring vanilla's `BinaryHeap` decrease-key via a stored `heapIdx`), g = `node.distanceTo` (Euclidean) accumulated with the neighbor's `costMalus`, h = `getBestH` × 1.5 (the `FUDGING` constant), reach via `distanceManhattan <= reachRange`, the follow-range gate, and a `maxVisited` BUDGET so an unreachable target cannot blow the MSPT budget (Pitfall 6 / T-7-04). An unreachable target yields a best-effort partial toward the closest node — never a panic.
- **The WalkNodeEvaluator** ported: `getNeighbors` (4 cardinals via `findAcceptedNode` with step-up/step-down + 4 clockwise diagonals), `getPathType` (WALKABLE/BLOCKED/OPEN over the snapshot solidity), the jar malus table, and the corner-cut rejection (`isNeighborValid` / `isDiagonalValid`) so a mob routes AROUND a wall instead of cutting its corner.
- **The path follower** (`groundNavigation`) ported from `GroundPathNavigation`/`PathNavigation`: `requestPath` is the seam call (snapshot -> computePath -> Path); `tick` advances the waypoint index (followThePath's column-match), faces the heading, jumps onto a one-block ledge, and steps the mob through the EXISTING `moveEntity` (Pattern 3) — the re-bucket keeps the tracker's `near()` correct and the mob never clips a wall (Pitfall 5). The moved mob is AUTO-BROADCAST by the unchanged tracker — no new entity encoder.
- **The serverAiStep wiring:** `mobAI` gains a `navigation groundNavigation`; after goal arbitration (07-01), `serverAiStep` consumes the stroll goal's `wantTarget` -> `requestPath` (throttled by `shouldRecomputePath`) -> `navigation.tick`. A goal's target becomes a WALKED A* path. Everything inline on the tick (TICK-05); zero new deps.

## Java Sources Ported (the STANDING MANDATE — javap -c, temp/cache/26.2-inner.jar, this session)

**The A* loop** (`net.minecraft.world.level.pathfinder.PathFinder.findPath` bytecode):

```
// start.g = 0; start.h = getBestH(start, targets); start.f = start.h;
// openSet.clear(); openSet.insert(start);
// maxVisited = (int)(maxVisitedNodes * searchDepthMultiplier);   // the BUDGET
// while !openSet.isEmpty() && ++visited < maxVisited:
//   node = openSet.pop(); node.closed = true;
//   for each target: if node.distanceManhattan(target) <= reachRange: target.setReached();
//   if any reached: break;
//   if node.distanceTo(start) >= followRange: continue;          // the follow-range gate
//   count = nodeEvaluator.getNeighbors(neighbors[], node);
//   for each neighbor:
//     edge = distance(node, neighbor);                            // = node.distanceTo (Euclidean)
//     neighbor.walkedDistance = node.walkedDistance + edge;
//     g = node.g + edge + neighbor.costMalus;                     // the relaxed g
//     if neighbor.walkedDistance >= followRange: continue;
//     if !neighbor.inOpenSet() || g < neighbor.g:
//       neighbor.cameFrom = node; neighbor.g = g;
//       neighbor.h = getBestH(neighbor, targets) * 1.5f;          // FUDGING = 1.5f
//       if inOpenSet: openSet.changeCost(neighbor, g+h)  else: neighbor.f=g+h; openSet.insert
// distance(a,b) = a.distanceTo(b)  // Mth.sqrt(dx²+dy²+dz²)
// getBestH(node, targets) = min over targets of node.distanceTo(target)
```

**The malus table** (`net.minecraft.world.level.pathfinder.PathType` static{} bytecode — the quoted ldc floats):

```
// BLOCKED  -> ldc -1.0f      OPEN     -> fconst_0 (0.0)
// WALKABLE -> fconst_0 (0.0) FENCE    -> ldc -1.0f
// LAVA     -> ldc -1.0f      WATER    -> ldc  8.0f
// (FIRE 16.0, DAMAGING -1.0, … — the wider set deferred per A5)
```

v1 ports the BLOCKED(-1)/OPEN(0)/WALKABLE(0) subset (a negative malus = impassable).

**The neighbor validity** (`WalkNodeEvaluator.isNeighborValid` + `isDiagonalValid` bytecode):

```
// isNeighborValid(neighbor, node):
//   neighbor != null && !neighbor.closed && (neighbor.costMalus >= 0 || node.costMalus < 0)
// isDiagonalValid(node, sideA, sideB):   // the corner-cut rejection
//   sideA != null && sideB != null && !(both sides strictly above node) &&
//   neither side a WALKABLE_DOOR && (mob thin || both sides passable) && a reachable corner
// isDiagonalValid(corner):
//   corner != null && !corner.closed && corner.type != WALKABLE_DOOR && corner.costMalus >= 0
```

**The maxVisited budget** (`GroundPathNavigation.createPathFinder` + `PathNavigation.getMaxPathLength` + `createPath` bytecode):

```
// createPathFinder(maxVisited): new PathFinder(new WalkNodeEvaluator(), maxVisited)
//   maxVisited = (int)(getMaxPathLength() * 16.0)
//   getMaxPathLength() = max(FOLLOW_RANGE attr (=16 for a Pig), requiredPathLength)
// findPath(region, mob, targets, followRange, accuracy, maxVisitedNodesMultiplier=0.5f)
// => effective cap = 16 * 16 * 0.5 = 128 expansions (the DoS guard, Pitfall 6 / T-7-04)
// PathNavigationRegion box = targetPos.offset(-(followRange+accuracy), …) .. offset(+…)
```

**The path-follow tick** (`PathNavigation.tick` bytecode): `++tick; if isDone return; followThePath() (advance the node index when floor(mob.x),floor(mob.z) reaches the next node's column); moveControl.setWantedPosition(nextPos.x, groundY, nextPos.z, speed)`. v1 collapses the moveControl chain into a direct desired-Δ fed to `moveEntity` (Pattern 3).

## Task Commits

1. **Task 1: PathNavigationRegion snapshot + PathFinder A* + WalkNodeEvaluator** — `3c32b285` (feat, TDD test+impl)
2. **Task 2: GroundPathNavigation path-following + serverAiStep wiring** — `ec54bf5b` (feat, TDD test+impl)

## Files Created/Modified

- `server/path_region.go` — `pathRegion` (immutable solidity bitset over a box) + `snapshotRegion` (copies world solidity on the tick; out-of-box = solid)
- `server/pathfinder.go` — `node` (+ `nodeHash`/`distanceTo`/`distanceManhattan`), `binaryHeap` (insert/pop/changeCost with stored heapIdx), `Path` (done/nextNode/advance), `pathRequest`, `computePath`/`computePathDebug` (the PURE A*), `reconstructPath`
- `server/node_evaluator.go` — `pathType` + `malus`, `mobAirCells`, `getPathType`, `newEvalNode`, `findAcceptedNode` (step-up/down), `getNeighbors`, `isNeighborValid`, `isDiagonalValidSides`/`isDiagonalValidCorner`
- `server/navigation.go` — `groundNavigation` (requestPath/tick/shouldRecomputePath), `maxVisitedBudget`, `clampStep`, the ported nav constants (followRange 16, accuracy 1, multiplier 0.5, recompute cooldown 20)
- `server/ai_mob.go` — `mobAI.navigation`; `serverAiStep` consumes `wantTarget` -> `requestPath` (throttled) -> `navigation.tick`; `newPigAI` sets `navigation.speed = pigWalkSpeed`
- `server/pathfinder_test.go` — TestComputePathFlat, TestComputePathAroundObstacle, TestPathNodeBudget, TestComputePathPure, TestPathUnreachable
- `server/navigation_test.go` — TestNavigationFollow, TestNavigationMovesViaMoveEntity, TestRequestPathBuildsSnapshot, TestServerAiStepWalksToGoalTarget

## Verification

- Plan test set (9 tests) passes: `go test ./server/ -run 'TestComputePathFlat|TestComputePathAroundObstacle|TestPathNodeBudget|TestComputePathPure|TestPathUnreachable|TestNavigationFollow|TestNavigationMovesViaMoveEntity|TestRequestPathBuildsSnapshot|TestServerAiStepWalksToGoalTarget' -count=1` → ok
- Full server + world + command suites green (no regression): `go test ./server/... ./world/... -count=1` → all ok
- `go vet ./...` clean; `go build ./...` exits 0; go.mod/go.sum unchanged (zero new deps — NO ants/conc/xsync; the seam is async-READY but executed INLINE)
- Docker `-race` over `./server/...` clean: `MSYS_NO_PATHCONV=1 docker run --rm -v //d/ender://src -w //src golang:1.26 go test -race ./server/...` → all ok

## Decisions Made

See `key-decisions` frontmatter. The load-bearing ones: the PURITY contract (computePath reads only the request — the Phase-8 hinge, asserted by TestComputePathPure); the A* shape (g=distanceTo, h=getBestH×1.5, reach via distanceManhattan, the follow-range gate) ported from PathFinder.findPath bytecode; the maxVisited budget (16×16×0.5=128, `++visited; if >= cap break`) as the DoS guard; the jar malus table (BLOCKED/FENCE/LAVA −1, OPEN/WALKABLE 0, WATER 8); and path-following through the existing moveEntity (re-bucket, no clip, tracker auto-broadcast — Pattern 3).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] maxVisited budget off-by-one (visited could reach cap+1)**
- **Found during:** Task 1 (TestPathNodeBudget failed: visited 21 with a cap of 20)
- **Issue:** My first loop did `visited++; if visited > budget break`, which let the heap pop on the (budget+1)-th iteration before breaking — exceeding the cap by one.
- **Fix:** Matched the vanilla bytecode exactly — `++visited; if visited >= maxVisited break` (the check is BEFORE the pop), so `visited` never exceeds the budget. This is the faithful DoS-guard semantics (Pitfall 6 / T-7-04).
- **Files modified:** server/pathfinder.go
- **Verification:** TestPathNodeBudget passes (visited <= budget); all other A* tests still pass.
- **Committed in:** 3c32b285

---

**Total deviations:** 1 auto-fixed (1 bug). **Impact:** None on scope — the fix tightened the budget to the exact vanilla semantics; no surface change.

## Issues Encountered

- None blocking. (A transient filesystem write was re-applied during Task 1 — three of the four files initially appeared absent on disk and were re-written; the second write landed and the build/tests confirmed all four files present. No code consequence.)

## Threat Surface

The plan's `<threat_model>` mitigations are all delivered and asserted:
- **T-7-04 (DoS, unbounded A*):** `computePath` enforces the `maxVisited` budget and returns a best-effort partial on exhaustion; `shouldRecomputePath` throttles recompute. Asserted by TestPathNodeBudget + TestPathUnreachable.
- **T-7-09 (Phase-8 race readiness):** `computePath` is PURE over the immutable snapshot — no `*TickLoop`/live `ChunkManager`. Asserted by TestComputePathPure; proven race-clean by the Docker `-race` gate.
- **T-7-05 (wall clip / stale bucket):** path-following goes through the existing `moveEntity` (swept collision + `entities.move` re-bucket). Asserted by TestNavigationMovesViaMoveEntity.
- **T-7-08 (off-tick mutation):** `requestPath` + `tick` run on the tick goroutine, inline; the snapshot reads tick-owned chunks. Proven by the Docker `-race` gate.

No NEW security-relevant surface beyond the plan's threat register (no new serverbound packet — navigation is server-authoritative).

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- AI-02 is complete: a mob's goal target becomes a synchronous A* path it WALKS via the existing moveEntity, with the whole compute a pure function over an immutable snapshot.
- **Ready for 07-03 (spawner + tickAI call site + debug-pig retirement):** `serverAiStep(t, e)` already drives goals + navigation; 07-03 calls `tickAI() -> serverAiStep` for every AI mob and removes the sinusoidal debug mover. The Docker `-race` gate over `./server/... ./world/...` (which the plan formally schedules at the 07-03 call site) already passes for this plan's inline machinery.
- **Ready for Phase 8 (OPT-01, async pathfinding):** the seam is structured for the free swap — `computePath(pathRequest)` is pure; OPT-01 hands the same `pathRequest` to an `ants` pool and rejoins the `Path` via the existing `applyAsyncResults` seam. No logic change required, only the executor.

---
*Phase: 07-ai-pathfinding-commands-chat*
*Completed: 2026-06-24*

## Self-Check: PASSED

- All 6 created files (path_region.go, pathfinder.go, node_evaluator.go, navigation.go, pathfinder_test.go, navigation_test.go) + the modified ai_mob.go + this SUMMARY exist on disk.
- Both task commits (3c32b285, ec54bf5b) present in git history.
- computePath's signature takes ONLY pathRequest (no *TickLoop) — the Phase-8 purity contract, asserted by TestComputePathPure.
- Plan test set (9 tests) + full server/world/command suites green; vet + build clean; Docker -race over ./server/... clean; go.mod/go.sum unchanged (zero new deps).
