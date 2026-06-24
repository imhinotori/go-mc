---
phase: 06-entities-physics-interaction
plan: 03
subsystem: physics
tags: [aabb, collision, gravity, swept-aabb, per-axis, bvh, tick-loop, anti-cheat, ENT-02]

# Dependency graph
requires:
  - phase: 06-01
    provides: tick-owned entity store (Entity, AABB(), entityStore.move re-bucketing), monotonic id allocator
  - phase: 06-02
    provides: synchronous entityTracker behind tracker.Tick() reading entities.near() (consumes post-physics positions)
  - phase: 04
    provides: world.ChunkManager (Get), level.Chunk/Section.GetBlock, the generator section/local mapping
  - phase: 05
    provides: applyInput position decode + teleport gate + maybeRecenter (the player-collide hook site)
provides:
  - "moveEntity: per-axis swept-AABB collision (clip dy/dx/dz independently) so entities land on floors (onGround) and are blocked by walls without tunneling"
  - "tickPhysics filled: drives gravity + air drag + horizontal friction + moveEntity for every store entity each tick, in its fixed pipeline slot"
  - "collidePlayer: authoritative per-axis validation of a client-sent player position (anti clip-through, T-6-06) wired into applyInput"
  - "blockSolidAt: read-only block-at-pos helper (non-air = solid) mirroring the generator section/local mapping; the READ counterpart 06-04 reuses for its WRITE API"
  - "sweepAxis: shared path-sampled + binary-refined single-axis sweep used by both entity and player collision"
affects: [06-04 block-interact, 07-ai-pathfinding, 08-async-optimizations]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-axis swept-AABB resolution (clip dy, dx, dz independently) — never full-vector-then-test (anti-tunneling, 06-RESEARCH Pitfall 4)"
    - "Path-sampled sweep (1/16-block coarse steps over the whole path) + binary refinement to seat flush against the obstructing face"
    - "Tick-owned physics: all mutation on the tick goroutine; position change routed through entityStore.move to keep the tracker's bucket consistent (TICK-05)"
    - "Authoritative-server position validation: server collides the client-claimed position rather than trusting it"

key-files:
  created:
    - server/physics.go
    - server/physics_test.go
  modified:
    - server/tick_phases.go
    - server/subtick.go

key-decisions:
  - "Explicit per-axis sweep with an explicit Z-interval test alongside bvh.AABB.Touch, because bvh.Vec3 Less/More compare only components 0/1 (X,Y) — Touch alone is X/Y-only, so a Z interval test is added to make overlap correct on all 3 axes without reimplementing box-overlap"
  - "No full-delta fast-path inside the sweep: a fast move whose endpoint is clear but whose PATH crosses a thin wall is still clipped at the wall (the anti-tunneling guarantee)"
  - "Binary refinement after the coarse path scan so an entity seats flush on a floor top / wall face instead of resting up to one coarse step short"
  - "collidePlayer returns the claimed position verbatim when there is no world or the path is clear, so open-air movement (and world-less Phase-5 tests) accept the exact decoded position"
  - "Physics constants (gravity 0.08, airDrag 0.98, friction 0.6x0.91, stepHeight 0.6) are named [ASSUMED]/tunable and wire-irrelevant (06-RESEARCH A1); stepHeight is recorded but not yet applied (v1 visible gate does not need stepping)"

patterns-established:
  - "Per-axis swept-AABB collision (moveEntity + sweepAxis) reused by entity physics and player position validation"
  - "blockSolidAt read helper as the single block-at-pos read mirroring the generator mapping (06-04 reuses for SetBlock)"

requirements-completed: [ENT-02]

# Metrics
duration: ~35 min
completed: 2026-06-24
---

# Phase 6 Plan 03: Gravity + Per-Axis Swept-AABB Collision Summary

**Filled the tickPhysics stub with gravity + per-axis swept-AABB collision (entities fall, land on floors with onGround, and are blocked by walls without tunneling) and wired authoritative per-axis collidePlayer so a client cannot clip through solid blocks — all tick-owned, reusing the bvh.AABB primitive and data/entity dims, zero new dependencies.**

## Performance

- **Duration:** ~35 min
- **Completed:** 2026-06-24
- **Tasks:** 2 (both TDD)
- **Files modified:** 4 (2 created, 2 edited)

## Accomplishments
- `moveEntity` resolves each axis INDEPENDENTLY (clip dy, then dx, then dz) via a path-sampled + binary-refined `sweepAxis` against solid world block boxes — entities land flush on floors (onGround), stop at walls, and a 10-block/tick velocity into a 1-block wall does NOT tunnel (TestNoTunnel).
- `tickPhysics` filled: applies gravity (vy -= g; vy *= drag), horizontal friction, then `moveEntity` for every entity in the tick-owned store, in its fixed pipeline slot (no reorder), routing position changes through `entities.move` so the tracker's `near()` bucket stays consistent.
- `collidePlayer` wired into `applyInput` (Pos + PosRot variants, after decode, before maybeRecenter): the server collides the client-claimed position per-axis and clamps it out of solid blocks (T-6-06), correcting clip-through rather than trusting the raw position. Teleport gate + hook ordering preserved.
- `blockSolidAt` read-only helper mirrors the generator section/local mapping (non-air = solid; air/unloaded/no-world = clear) — the READ counterpart 06-04 reuses for its block-edit WRITE API.
- Reused `server/internal/bvh.AABB` + the entity `data/entity` Width/Height dims for all boxes; zero new dependencies.

## Task Commits

Each task was committed atomically:

1. **Task 1: Per-axis swept-AABB moveEntity (gravity + collision)** - `563339f2` (feat)
2. **Task 2: Fill tickPhysics + wire authoritative collidePlayer** - `4b612ba5` (feat)

_TDD note: the test file (physics_test.go) holds all 7 tests and was written test-first; it landed with the Task-1 commit since both tasks share that file. Task 2's behavior (tickPhysics fill + applyInput collide hook) is the second commit._

## Files Created/Modified
- `server/physics.go` (created) - moveEntity, sweepAxis (shared path-scan + binary refine), clipAxis, boxOverlapsSolid (Touch + explicit Z), entityBoxAt, blockSolidAt, floorDiv/floorI, collidePlayer + playerBoxAt/clampPlayerAxis, tunable constants, dimMinY.
- `server/physics_test.go` (created) - TestEntityLands, TestEntityBlockedByWall, TestNoTunnel, TestGravityTunable, TestTickPhysicsRunsGravity, TestPlayerClipRejected + the test-side solid-overlap oracle and the by-hand world harness.
- `server/tick_phases.go` (modified) - filled tickPhysics (was a trace-only stub).
- `server/subtick.go` (modified) - applyInput collides the client-sent position via collidePlayer for the Pos/PosRot variants.

## Decisions Made
- **Explicit Z-interval test alongside `bvh.AABB.Touch`:** `bvh.Vec3` `Less`/`More` compare only components 0 and 1 (X, Y), so the generic 3D `Touch` is X/Y-only. Rather than reimplement box overlap, `boxOverlapsSolid` uses `Touch` for X/Y and adds an explicit Z interval test — correct on all three axes while still reusing the bvh primitive as the plan's key_link requires.
- **No full-delta fast-path in the sweep:** the sweep always walks the path in 1/16-block coarse steps so a fast move whose endpoint is clear but whose path crosses a thin wall is still clipped (the anti-tunneling guarantee). A binary refinement then seats the clamp flush against the face.
- **`collidePlayer` accepts the claim verbatim when clear / world-less:** keeps open-air movement exact (no floating-point sweep noise) and keeps the Phase-5 world-less tests (TestMovementDecode/TestTeleportGate) passing unchanged.
- **Constants are named/tunable/[ASSUMED]:** gravity/drag/friction/step are documented as training-derived, wire-irrelevant (06-RESEARCH A1). v1 targets the visible behaviors, not exact vanilla parity.

## Deviations from Plan

None - plan executed exactly as written.

The plan's Task 2 listed a `TestTickPhaseOrderUnchanged` test; the repo already has `TestTickPhaseOrder` (server/tick_test.go) asserting the exact fixed pipeline order, and `tickPhysics` was filled in place without touching the call site or ordering, so that existing test already proves the contract — no duplicate was added (it stays green). This is a naming alignment, not a behavioral deviation.

---

**Total deviations:** 0
**Impact on plan:** None. The two `bvh.Vec3` Z-blindness and tunneling-fast-path findings were design details handled within Task 1's intended scope (correct per-axis sweep), not unplanned rule-driven fixes.

## Issues Encountered
- **First test run: entities settled ~0.03 above the floor and a vx=10 entity tunneled the wall.** Root cause was (a) the coarse 1/16 step left a gap short of flush, and (b) an initial "destination clear → accept full delta" shortcut let a fast move skip over a thin wall. Resolved by extracting `sweepAxis`: it always path-samples the whole motion (no endpoint shortcut) and binary-refines between the last clear and first colliding sample to seat flush. All 7 tests then passed.

## Verification Results
- `go test ./server/ -run 'TestEntityLands|TestEntityBlockedByWall|TestNoTunnel|TestGravityTunable|TestTickPhysicsRunsGravity|TestPlayerClipRejected|TestTickPhaseOrder|TestMovementDecode|TestTeleportGate' -count=1` — PASS (new physics tests + Phase-5 movement/teleport/phase-order regressions).
- `go vet ./...` — clean.
- `go build ./...` — clean.
- Full `go test ./server/... ./world/... ./level/...` — PASS.
- Docker `-race` over `./server/... ./world/...` (`golang:1.26`) — PASS (tick-owned mutation, TICK-05 / T-6-08).
- `go.mod`/`go.sum` unchanged — zero new dependencies (no xsync/ants/conc).

## Next Phase Readiness
- ENT-02 complete: gravity + per-axis AABB collision for entities AND authoritative player anti-clip-through.
- `blockSolidAt` (read) is the natural pair for Plan 06-04's block-edit WRITE API (SetBlock) — 06-04 can reuse the same generator section/local mapping rather than duplicate it.
- `sweepAxis`/`moveEntity` are the substrate Phase 7 (AI movement) and Phase 8 (async) build on; entity hot fields stay snapshot-friendly (plain values), unchanged.

## Self-Check: PASSED

- Created files exist on disk: server/physics.go, server/physics_test.go, 06-03-SUMMARY.md.
- Modified files present: server/tick_phases.go, server/subtick.go.
- Task commits resolve in git: 563339f2 (Task 1), 4b612ba5 (Task 2).
- All plan `<verification>` commands re-run green (tests, vet, build, Docker -race, zero new deps).

---
*Phase: 06-entities-physics-interaction*
*Completed: 2026-06-24*
