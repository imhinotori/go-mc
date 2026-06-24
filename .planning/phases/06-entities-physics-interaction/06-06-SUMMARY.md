---
phase: 06-entities-physics-interaction
plan: 06
subsystem: gameplay
tags: [health, damage, respawn, combat, persistence, anvil, nbt, region, snapshot, tick-owned]

# Dependency graph
requires:
  - phase: 06-01
    provides: entityStore + EntityIDAllocator (the entity id space + the store snapshotted to the entities region)
  - phase: 06-05
    provides: component-slot Inventory (the tick-owned inventory translated to disk []save.Item)
  - phase: 05
    provides: commonPlayerSpawnInfoEncoder (the capture-diff-SEALED spawn-info encoder REUSED by ClientboundRespawn) + writePlayerPositionPacket (the re-teleport)
provides:
  - Server-owned health/damage/death loop (setHealth jar order, applyDamage, die -> PlayerCombatKill)
  - Respawn flow (ClientboundRespawn reusing the sealed encoder + dataToKeep, fresh re-teleport, full streamer reset)
  - ServerboundClientCommand(PERFORM_RESPAWN) dispatch route (defensive decode, dead-only)
  - Anvil player persistence (save.PlayerData -> world/playerdata/<uuid>.dat gzip NBT) with missing/corrupt-defaults
  - Anvil entity persistence (save.Entities -> entities/r.x.z.mca via save/region)
  - Off-tick save discipline (owner-side immutable snapshot -> off-tick IO), -race clean
affects: [physics, combat-attribution, world-persistence, multi-dimension-respawn]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Server-owned health: the client has NO health-setting packet; it only REQUESTS respawn via ServerboundClientCommand (T-6-05)"
    - "Reuse the Phase-5-sealed encoder for Respawn: commonPlayerSpawnInfoEncoder + a single trailing dataToKeep byte, no re-derivation"
    - "Snapshot-on-owner / IO-off-tick: removePlayer takes an immutable save.PlayerData value copy on the owner goroutine, RunSaveLoop does the disk IO off the tick (the Phase-4 chunk-result discipline, TICK-05/T-6-15)"
    - "Disk-NBT vs wire-component separation (Pitfall 6): inventory persists as save.Item (Count/Slot/string-ID) via the item registry; the wire component SlotData is never written to disk"

key-files:
  created:
    - server/combat.go
    - server/combat_test.go
    - server/persistence.go
    - server/persistence_test.go
  modified:
    - server/tick.go
    - server/gameplay_tick.go
    - cmd/sulfur/main.go

key-decisions:
  - "Respawn dataToKeep = 0 (full reset) for v1 — the death-then-fresh-spawn behavior; the vanilla keep-flags dimension-change respawn would set bits here, deferred"
  - "Respawn re-teleport uses a tick-owned nextTeleportID seeded at 1<<30 (respawnTeleportBase) so a respawn id never collides a join id issued by gameTick.teleportSeq"
  - "Save-on-leave snapshots in removePlayer (on the owner) and emits on a buffered leaveSnapshots channel; RunSaveLoop (off-tick) writes the .dat — keeping the snapshot on the owner and the IO off the tick"
  - "Entities region sector framing mirrors the chunk region: a compression byte (gzip=1) + gzip(NBT) of an entityRegion{Entities []save.Entities} compound"
  - "v1 load-on-join restores health/food/saturation; the persisted position round-trips on disk but is NOT applied to the live player (re-placing would need re-issuing the bootstrap teleport — deferred to a later plan)"

patterns-established:
  - "On-tick teleport-id producer (TickLoop.nextTeleportID) mirroring the off-tick gameTick.nextTeleportID for in-game re-teleports"
  - "Leave-snapshot channel: the only player value crossing the tick boundary on leave is an immutable save.PlayerData (no live tick-owned pointer)"

requirements-completed: [ENT-05, ENT-06]

# Metrics
duration: 35 min
completed: 2026-06-24
---

# Phase 6 Plan 06: Health/Damage/Respawn + Anvil Persistence Summary

**Server-owned health/damage/death/respawn (SetHealth jar order, PlayerCombatKill death screen, ClientboundRespawn reusing the Phase-5-sealed spawn-info encoder + a fresh re-teleport/re-stream) plus Anvil persistence of player .dat and a parallel entities region via save/save-region, with the disk IO off-tick over an immutable owner-side snapshot.**

## Performance

- **Duration:** 35 min
- **Tasks:** 2 (both TDD: RED test commit -> GREEN feat commit)
- **Files modified:** 7 (4 created, 3 modified)

## Accomplishments
- ENT-05: full server-driven health loop — `setHealth` (jar-verified Float/VarInt/Float), `applyDamage` (lowers tick-owned health, clamps at 0, sends SetHealth), `die` (PlayerCombatKill death screen + dead flag), `performRespawn` (Respawn reusing `commonPlayerSpawnInfoEncoder` + `dataToKeep`, a fresh re-teleport that re-arms the confirm gate, and a full streamer reset so the world re-streams). `ServerboundClientCommand(PERFORM_RESPAWN)` is routed defensively and honored only for a dead player.
- ENT-06: Anvil persistence reusing `save`/`save/region` wholesale — `savePlayer`/`loadPlayer` (`world/playerdata/<uuid>.dat`, gzip NBT, missing/corrupt -> spawn defaults) and `saveEntities`/`loadEntities` (`entities/r.x.z.mca` via `region.ReadSector`/`WriteSector`). The save path takes an immutable `snapshotPlayer` value copy on the owner and `RunSaveLoop` does the disk IO off the tick (TICK-05 / T-6-15).
- Jar verification: all three new clientbound layouts (SetHealth, PlayerCombatKill, Respawn) confirmed against `temp/cache/26.2-inner.jar` via `javap` — they matched the plan exactly, no field-order correction needed.
- Zero new dependencies; Docker `-race` over `./server/... ./save/...` clean.

## Task Commits

Each task was committed atomically (TDD RED -> GREEN):

1. **Task 1: Health/damage/death/respawn (ENT-05)** — `3ea78600` (test) -> `280b15b3` (feat)
2. **Task 2: Anvil persistence (ENT-06)** — `1dc0541a` (test) -> `5ea21a7f` (feat)

## Files Created/Modified
- `server/combat.go` (created) — `setHealth`, `applyDamage`, `die`, `playerCombatKill`, `respawnPacket` (reuses the sealed encoder + dataToKeep), `performRespawn`; the ClientCommand action enum.
- `server/combat_test.go` (created) — TestSetHealthWire / TestApplyDamage / TestDeath / TestRespawnFlow.
- `server/persistence.go` (created) — `snapshotPlayer`, `inventoryToItems`/`itemName`, `savePlayer`/`loadPlayer`, `saveEntities`/`loadEntities`, `RunSaveLoop`, `defaultPlayerData`, the entity-sector encode/decode.
- `server/persistence_test.go` (created) — TestPlayerDataRoundTrip / TestPlayerInventoryRoundTrip / TestLoadMissingDefaults / TestEntityRegionRoundTrip / TestSnapshotIsValueCopy.
- `server/tick.go` (modified) — tickPlayer gains health/food/saturation/dead + uuid; TickLoop gains spawnSurfaceY, the on-tick respawn teleport-id producer, the leaveSnapshots channel + SetSaveSink; SetSpawn/nextTeleportID; the ClientCommand dispatch case; removePlayer emits the owner-side leave snapshot.
- `server/gameplay_tick.go` (modified) — gameTick gains worldDir + SetWorldDir; AcceptPlayer sets the full-health defaults, the player uuid, and load-on-join (.dat health/food/sat restore before registration).
- `cmd/sulfur/main.go` (modified) — wires tick.SetSpawn, tick.SetSaveSink + go tick.RunSaveLoop, and gp.SetWorldDir(worldDir).

## Decisions Made
See `key-decisions` frontmatter. Headline: Respawn reuses the Phase-5-sealed `commonPlayerSpawnInfoEncoder` with `dataToKeep=0` (the only new byte); the save path takes the immutable snapshot on the owner and runs the disk IO off the tick via a buffered leave-snapshot channel + RunSaveLoop.

## Deviations from Plan

None - plan executed exactly as written.

The plan's task structure, file set, behaviors, and verification gates were followed verbatim. The plan flagged that the respawn re-teleport "confirm how the tick can allocate a teleport id on the owner" — resolved by adding a tick-owned `nextTeleportID` (seeded above the join id space) rather than reaching across to `gameTick.teleportSeq`, which is the on-tick analogue the plan anticipated.

## Known Stubs

**1. Persisted position not applied to the live player on join (v1 deferred — documented in plan)**
- **File:** `server/gameplay_tick.go` (load-on-join block)
- **Reason:** The bootstrap already teleports the joining client to the spawn column; applying a persisted position would require re-issuing the bootstrap teleport with the loaded coordinates. The position round-trips correctly on disk (proven by TestPlayerDataRoundTrip) — only the live application is deferred. v1 restores health/food/saturation, which is the death/respawn-relevant state. A later plan applies the persisted position to the spawn teleport.

This stub is intentional and does not block the plan goal (ENT-05/ENT-06: the health loop and the persistence round-trip both work).

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- ENT-05 and ENT-06 close Phase 6's remaining slices: the death->respawn loop completes server-side, and player/entity state persists to Anvil and reloads.
- Ready for verification (`/gsd-verify-work`). The deferred persisted-position application and combat damage-source attribution (the generic "You died" message) are natural follow-ups, not blockers.

---
*Phase: 06-entities-physics-interaction*
*Completed: 2026-06-24*

## Self-Check: PASSED

- Created files exist on disk: server/combat.go, server/combat_test.go, server/persistence.go, server/persistence_test.go, 06-06-SUMMARY.md — all FOUND.
- Task commits exist: 3ea78600 (test ENT-05), 280b15b3 (feat ENT-05), 1dc0541a (test ENT-06), 5ea21a7f (feat ENT-06) — all FOUND.
- ENT-05 gate (TestSetHealthWire|TestApplyDamage|TestDeath|TestRespawnFlow): PASS.
- ENT-06 gate (TestPlayerDataRoundTrip|TestLoadMissingDefaults|TestEntityRegionRoundTrip|TestSnapshotIsValueCopy): PASS.
- Full module `go test ./...`: PASS; `go vet ./...` + `go build ./...`: clean; go.mod/go.sum unchanged (zero new deps).
- Docker `-race` over ./server/... ./save/...: clean.
