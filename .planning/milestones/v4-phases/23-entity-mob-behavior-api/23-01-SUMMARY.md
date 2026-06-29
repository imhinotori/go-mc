---
phase: 23-entity-mob-behavior-api
plan: 01
subsystem: api
tags: [starlark, plugin, attributes, mob-ai, capability-enforcement, handles, sub-attrib]

# Dependency graph
requires:
  - phase: 22-plugin-host-event-bus
    provides: "host.Manager + Manifest.Capabilities (parsed-but-unenforced field), the Starlark plugin host"
  - phase: 21-plugin-starlark-runtime
    provides: "go.starlark.net pin + the budget-bounded thread / builtin surface"
  - phase: 07-ai-pathfinding-commands-chat
    provides: "mobAI.setWantTarget -> groundNavigation seam, the Goal/goalSelector AI tick"
  - phase: v3.1 SUB-ATTRIB
    provides: "level/attribute (Map/Supplier/Builder, the 6 existing per-type suppliers)"
provides:
  - "SUB-ATTRIB coverage fix: per-type suppliers (pig/cow/sheep/chicken/skeleton/creeper/spider) + a LivingEntity fallback so NewMapForEntity never returns nil for a living type"
  - "thin entityHandle/worldHandle starlark.Value types (id-not-pointer; HasAttrs reads; tick-owned mutate seams)"
  - "capability vocabulary (entities.read/write, world.read/write, nav) + enforcement at the handle-op boundary"
affects: [23-02 declare_mob + wander gate, 24 vanilla-mob-as-plugin, 26 python-runtime]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "thin-handle: a starlark.Value wrapping id int32 + *TickLoop + capSet, NEVER a live *Entity; every read re-resolves t.entities.get(id) on the tick goroutine"
    - "read-vs-mutate seam distinction: HasAttrs.Attr returns frozen scalars for reads, bound *Builtin methods route mutates through existing tick-owned seams"
    - "capability enforcement at the handle-op boundary: each read/mutate checks the owning plugin's capSet, denial returns a Starlark error"
    - "living-fallback supplier gated on the data/entity MobCategory so non-living types stay attribute-less"

key-files:
  created:
    - server/plugin_entity.go
    - server/plugin_capability.go
    - server/plugin_entity_test.go
  modified:
    - level/attribute/defaults.go
    - level/attribute/defaults_test.go

key-decisions:
  - "Per-type supplier values come from the jar bytecode, NOT the plan's guesses: cow movement_speed is the float-widened double 0.20000000298023224, sheep 0.23000000417232513 + max_health 8.0, spider 0.30000001192092896, pig max_health 10.0 + speed 0.25 — all read directly from the ldc2_w constants"
  - "createAttributes lives on AbstractCow / AbstractSkeleton (not Cow/Skeleton) — ported there and registered under the concrete cow/skeleton names"
  - "isLivingType derives 'living' from the data/entity MobCategory (Type field) — the 7 non-misc categories — importing data/entity (a leaf data pkg) into level/attribute introduces no cycle"
  - "Option A import direction (LOCKED): handles live in package server (they need *TickLoop/*Entity/ChunkManager); server already imports go.starlark.net, one direction, no cycle"
  - "set_velocity is the single faithful direct field write (vx/vy/vz); every other mutate routes through a tick-owned seam"
  - "health is attribute-derived (e.getAttributeValue(MaxHealth)) — Sulfur has no separate health field yet"

patterns-established:
  - "thin-handle (id + *TickLoop + capSet, re-resolved per access, Freeze no-op)"
  - "capability-gated handle op (capSet.has at the op boundary, capError as a Starlark dynamic error)"
  - "living-fallback supplier composed from createLivingAttributes() (not a baked value) so a future per-type port just adds to the suppliers map"

requirements-completed: [PLUGIN-03]

# Metrics
duration: 40min
completed: 2026-06-28
---

# Phase 23 Plan 01: Entity/Mob Behavior API — Foundation Summary

**SUB-ATTRIB coverage fix (7 jar-exact per-type suppliers + a LivingEntity fallback so every living type gets real attributes) plus thin id-not-pointer entity/world Starlark handles with capability enforcement at the handle-op boundary.**

## Performance

- **Duration:** ~40 min
- **Started:** 2026-06-28T05:02Z
- **Completed:** 2026-06-28
- **Tasks:** 3
- **Files modified:** 5 (3 created, 2 modified)

## Accomplishments
- **SUB-ATTRIB coverage gap closed:** `NewMapForEntity` now returns a real, type-correct attribute map for any LIVING entity type. A pig gets `max_health 10.0` / `movement_speed 0.25` (its Animal createAttributes overrides), not the bare living default. Seven per-type suppliers (pig/cow/sheep/chicken/skeleton/creeper/spider) ported 1:1 from the 26.2 jar, each value read directly from the `ldc2_w` bytecode and cited; a `livingFallbackSupplier()` (= `LivingEntity.createLivingAttributes()`) backs any still-unported living type so it never degrades to nil. Non-living ("misc": item/arrow/boat) types stay nil.
- **Thin entity/world handles:** `entityHandle`/`worldHandle` are `starlark.Value` + `HasAttrs` carrying `id int32` + `*TickLoop` + `capSet` (never a `*Entity` pointer). Reads (x/y/z/yaw/pitch/on_ground/type/velocity/health/attribute, world.block_at/entities_near) re-resolve the store on the tick goroutine; a removed-entity read returns a clean `entity N no longer exists` error; `Freeze()` is a no-op.
- **Tick-owned mutate seams:** `move_to` -> `setWantTarget` (nav, never a raw position write), `set_velocity` -> direct `vx/vy/vz` (the LOCKED faithful exception), `set_attribute` -> `attribute.Map.GetInstance.SetBaseValue`, `world.set_block` -> `ChunkManager.SetBlock` + `broadcastBlockUpdate`.
- **Capability enforcement ON:** the manifest `capabilities` field (parsed-but-unenforced since Phase 22) is now enforced per-op. A plugin without `world.write` calling `set_block` gets a Starlark error and the block is unchanged; `move_to` requires `entities.write` AND `nav`; an unknown capability string is rejected loudly at parse.

## Task Commits

1. **Task 1: SUB-ATTRIB suppliers + fallback** — `8e3303f7` (test, RED) → `a98c72e5` (feat, GREEN)
2. **Task 2: thin entity/world handles** — `75e396e1` (feat, handles + capSet scaffolding + handle tests)
3. **Task 3: capability enforcement** — `7ccc0465` (test, denial/allow pairs + parseCapabilities vocab)

_TDD: Task 1 was a clean RED→GREEN cycle. Task 2/3 telescoped impl+test in one commit each (the handle file and the capability enforcement it consumes are interdependent), each verified GREEN before commit._

## Files Created/Modified
- `level/attribute/defaults.go` — 7 per-type suppliers (jar-cited), `livingFallbackSupplier()`, `isLivingType` (data/entity MobCategory predicate), `entityByName` index, rewritten `NewMapForEntity` (supplier → living fallback → nil for non-living)
- `level/attribute/defaults_test.go` — `TestSupplierCoverage`, `TestLivingFallback`, `TestNewMapForEntity_NonLivingNil` (replaces the old unported-type-returns-nil test)
- `server/plugin_entity.go` — `entityHandle`/`worldHandle` (starlark.Value + HasAttrs + bound mutate methods routing to tick-owned seams)
- `server/plugin_capability.go` — `capSet` vocab, `parseCapabilities`, `has`, `capError`
- `server/plugin_entity_test.go` — handle read/stale/freeze/mutator tests + capability denial/allow pairs + vocab test

## Decisions Made
- **Jar values override the plan's intermediate numbers.** The plan listed expected supplier values "to confirm"; several were wrong. Bytecode-verified actuals: pig (MH 10.0, MS 0.25), cow (MH 10.0, MS 0.20000000298023224), sheep (MH 8.0, MS 0.23000000417232513), chicken (MH 4.0, MS 0.25), skeleton (MS 0.25), creeper (MS 0.25), spider (MH 16.0, MS 0.30000001192092896). Float-widened doubles preserved bit-for-bit.
- **`createAttributes` lives on the abstract base for cow/skeleton** (`AbstractCow`, `AbstractSkeleton`) — ported there, registered under `cow`/`skeleton`.
- **`health` is attribute-derived** — Sulfur has no separate health field, so the handle reads `getAttributeValue(MaxHealth)`.
- **`block_at` returns a `(state_id, ok)` tuple** so an unloaded-column read is observable, not a silent 0.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Existing test asserted the OLD (now-fixed) nil behavior**
- **Found during:** Task 1
- **Issue:** `TestNewMapForEntity_UnknownType` asserted `NewMapForEntity("ender_dragon") == nil`. ender_dragon is a `monster` (living); the coverage fix deliberately makes living types return the fallback (non-nil), so the old assertion now contradicts the intended behavior.
- **Fix:** Replaced it with `TestNewMapForEntity_NonLivingNil` (item/arrow/boat/area_effect_cloud stay nil) and added `TestLivingFallback` (ender_dragon/fox/wolf get the living base set). This is the plan's intended behavior change, not a regression.
- **Files modified:** level/attribute/defaults_test.go
- **Verification:** `CGO_ENABLED=0 go test ./level/attribute/` green (all suites)
- **Committed in:** a98c72e5 (Task 1 GREEN commit)

**2. [Rule 3 - Blocking] No data/entity ByName index existed**
- **Found during:** Task 1
- **Issue:** `isLivingType` needs to resolve an entity's MobCategory from its registry name, but data/entity only ships `ByID` (keyed by numeric id).
- **Fix:** Built a package-level `entityByName` index once at init from `entity.ByID` (read-only after init; concurrent tick reads are safe).
- **Files modified:** level/attribute/defaults.go
- **Verification:** build + the coverage/fallback tests pass
- **Committed in:** a98c72e5

---

**Total deviations:** 2 auto-fixed (1 bug — stale test for the intended behavior change; 1 blocking — missing name index).
**Impact on plan:** Both necessary for the SUB-ATTRIB outcome. No scope creep. All ported values verified against the jar (the 1:1 mandate), not trusted from the plan.

## Issues Encountered
- The `-race` gate needs CGO=1, but the host has no gcc (CGO_ENABLED=0 ship default). Ran it in the project's standard `golang:1.26` Docker image instead — both `./level/attribute/` and the handle/capability suites are race-clean (the handles are race-safe by construction: id-not-pointer, re-resolve on the tick owner).

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- **23-02 (declare_mob + the wander gate) can build directly on this.** A declared mob's attributes come from the supplier coverage fix; its goal callbacks receive these handles. `buildAIFromDecl` will thread a real manifest-derived `capSet` (via `parseCapabilities(manifest.Capabilities)`) into the handles it constructs for goal callbacks — the plumbing point is documented in the handle constructors.
- Verification gates all green: `CGO_ENABLED=0 go build ./...`, `go vet ./level/attribute/ ./server/`, the three test suites, and the Docker `-race` gate.

## Self-Check: PASSED

All created files exist on disk (server/plugin_entity.go, server/plugin_capability.go, server/plugin_entity_test.go, level/attribute/defaults.go, 23-01-SUMMARY.md) and all four task commits (8e3303f7, a98c72e5, 75e396e1, 7ccc0465) are present in git history.

---
*Phase: 23-entity-mob-behavior-api*
*Completed: 2026-06-28*
