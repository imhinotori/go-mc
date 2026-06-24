---
phase: 05-player-session-in-world-first-playable
plan: 03
subsystem: testing
tags: [proto-776, capture-diff, playerinfoupdate, setdefaultspawnposition, forgetlevelchunk, tab-list, first-playable, race-fix, channelqueue]

# Dependency graph
requires:
  - phase: 05-player-session-in-world-first-playable (05-01)
    provides: movement decode + recenterRing + teleport gate + jar-derived ForgetLevelChunk
  - phase: 05-player-session-in-world-first-playable (05-02)
    provides: early-Play tail (abilities/held-slot/PlayerInfoUpdate-self/spawn-pos) + incrementing teleport id
  - phase: 04-world-chunk-system (04-04)
    provides: the vanilla 26.2 capture-diff harness/method + the pulled-forward Join Game bootstrap
provides:
  - "Authoritative byte-diff sealing the 3 MEDIUM-confidence Play encoders against a real vanilla 26.2 server (PlayerInfoUpdate self-entry, SetDefaultSpawnPosition RespawnData/GlobalPos, ForgetLevelChunk packed-Long) — no encoder divergence found"
  - "Committed golden fixtures + TestPlayBytesVsVanillaCapture so CI byte-diffs without booting Java"
  - "A concurrency fix: net/queue.ChannelQueue now serializes Close vs Push (the join-seam -race the capture-diff load surfaced)"
  - "PLAY-06 first-playable milestone: a real vanilla 26.2 client WALKS AROUND a following world, is listed, not kicked — the project is now playable end-to-end"
affects: [phase-06-entities, phase-07-blocks, phase-08-async-optimizations, any future Play-state encoder work]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Capture-diff seals uncertain proto-776 encoders against the real vanilla server (the only authoritative source); golden fixtures + a skip-if-absent diff test keep native CI green"
    - "Concurrent-close-safe bounded queue: mutex + closed flag serializes Close against a non-blocking try-Push (mirrors LinkedListQueue), Pull stays lock-free (close-vs-recv is safe)"

key-files:
  created:
    - .planning/phases/05-player-session-in-world-first-playable/05-CAPTURE-DIFF.md
    - server/play_join_capture_test.go
    - .planning/phases/05-player-session-in-world-first-playable/fixtures/vanilla-player-info-update.bin
    - .planning/phases/05-player-session-in-world-first-playable/fixtures/vanilla-set-default-spawn-position.bin
    - .planning/phases/05-player-session-in-world-first-playable/fixtures/vanilla-forget-level-chunk.bin
  modified:
    - net/queue/queue.go

key-decisions:
  - "The 3 MEDIUM-confidence Play encoders match vanilla byte-for-byte with NO change needed — the only differences are action-set width (vanilla 8-action creative vs Sulfur 3-action survival) and content values (spawn Y -60 vs -48), which are content, not framing"
  - "SetTime is NOT required for v1: vanilla sends it but the real-client walk-around confirmed no kick/hang without it; the captured 31-byte WorldClock+ClockNetworkState layout is documented for a later phase"
  - "Found + fixed a real net/queue.ChannelQueue close-vs-send data race (latent send-on-closed panic) the prior single-test -race runs never hit — surfaced under the capture-diff's added load"

patterns-established:
  - "Capture-diff as the authoritative Play-encoder gate: a self-round-trip cannot prove vanilla-correctness for proto 776; the byte-diff + the real-client visual are the two-sided proof (same lesson as Phase 2 NET-04 / Phase 4 WORLD-02/03)"
  - "Skip-if-fixture-absent diff test keeps native CI green while the committed golden fixtures hold the gate"

requirements-completed: [PLAY-01, PLAY-02, PLAY-03, PLAY-05, PLAY-06]

# Metrics
duration: 55min
completed: 2026-06-24
---

# Phase 5 Plan 03: Capture-Diff + First-Playable Walk-Around Summary

**Byte-diff sealed Sulfur's 3 uncertain proto-776 Play encoders against a real vanilla 26.2 server (no encoder bug), fixed a net/queue close-vs-send data race surfaced under load, and confirmed the PLAY-06 first-playable milestone — a real vanilla 26.2 client walks around a following world, is listed, and is not kicked.**

## Performance

- **Duration:** ~55 min
- **Started:** 2026-06-24 (Task 1 capture-diff)
- **Completed:** 2026-06-24 (Task 2 human-verify signed off)
- **Tasks:** 2 (1 autonomous capture-diff + 1 blocking human-verify)
- **Files modified:** 6 tracked (1 source fix + 1 test + 3 fixtures + 1 doc)

## Accomplishments

- **Sealed the 3 MEDIUM-confidence encoders byte-for-byte against real vanilla 26.2** (booted from `temp/cache/26.2-server.jar`, superflat seed 144, offline). A Play-state capture harness drove both the vanilla server and `cmd/sulfur` to Play and byte-diffed the early-Play tail:
  - **PlayerInfoUpdate (self tab-list entry):** the property-list count-prefix sub-encoding (`VarInt(0)`) and the 1-byte action-mask framing are byte-identical; vanilla's 8-action creative set vs Sulfur's 3-action survival set is the documented content difference, not a framing divergence.
  - **SetDefaultSpawnPosition (RespawnData/GlobalPos):** `Identifier dimension + packed-Long BlockPos + Float yaw + Float pitch` byte-identical for equal inputs (only the generator surface Y differs, -60 vs -48).
  - **ForgetLevelChunk:** byte-identical to the jar-derived `ChunkPos.pack` golden (x low 32 / z high 32), with a non-vacuity proof (x/z swap fails the test).
- **Fixed a real data race** (deviation, Rule 1): `net/queue.ChannelQueue` was a bare `chan T` whose `close(ch)` raced `ch <- v` between the tick's `flushOutbound` and a readLoop-triggered `Client.Close`. Converted to a mutex+closed-flag struct; `-race` clean at `-count=10` over the join seam.
- **PLAY-06 first-playable milestone confirmed:** the user connected an unmodified vanilla 26.2 client (PrismLauncher) to `localhost:25565` and walked around — the chunk ring FOLLOWS (new chunks at the moving edges, no void, no falling off the world), movement is not blocked after the initial Confirm Teleportation, the player appears in the tab list, and there is no kick/hang. *"si, funciona :)"*
- **Resolved all four Open Questions** (ForgetLevelChunk layout; PlayerInfoUpdate property count-prefix; SetTime necessity = not needed for v1; ServerboundPlayerLoaded = no-op).

## Task Commits

1. **Deviation (Rule 1): ChannelQueue close-vs-send race** — `c18789d3` (fix)
2. **Task 1: Capture-diff Play tail vs vanilla + golden fixtures** — `d067c720` (test)
3. **Task 2: Real-client walk-around** — human-verify checkpoint, signed off "approved" (no code; the gate is the real client)

**Plan metadata:** see the final docs commit (this SUMMARY + STATE/ROADMAP/REQUIREMENTS + CAPTURE-DIFF status flip).

## Files Created/Modified

- `server/play_join_capture_test.go` — `TestPlayBytesVsVanillaCapture`: loads the vanilla golden, walks it as a client (zero trailing bytes), and asserts Sulfur's encoders match the framing; skips cleanly if a fixture is absent.
- `.planning/phases/05-player-session-in-world-first-playable/fixtures/vanilla-player-info-update.bin` (33 B) — vanilla join PlayerInfoUpdate self-entry golden.
- `.planning/phases/05-player-session-in-world-first-playable/fixtures/vanilla-set-default-spawn-position.bin` (36 B) — vanilla RespawnData golden.
- `.planning/phases/05-player-session-in-world-first-playable/fixtures/vanilla-forget-level-chunk.bin` (8 B) — jar-derived ForgetLevelChunk golden.
- `.planning/phases/05-player-session-in-world-first-playable/05-CAPTURE-DIFF.md` — the reproducible capture method, per-field diff, jar derivation, Open-Question resolutions, the deviation record, and the real-client sign-off.
- `net/queue/queue.go` — `ChannelQueue` converted to a concurrent-close-safe struct.

## Decisions Made

- **No encoder change was needed** — the capture-diff confirmed Sulfur's three uncertain encoders already match vanilla. The plan budgeted for a possible fix in `server/play_join.go` / `world/packet.go`; none was required.
- **SetTime omitted for v1** — vanilla sends it, but the absence does not kick the client; the 31-byte layout is documented for a future day/night-cycle phase.
- **The race fix lives in the owning package** (`net/queue`), not in `Client.Send`'s recover-band-aid — the recover masked the panic but not the race; serializing Close against Push removes the race at the source.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] net/queue.ChannelQueue close-vs-send data race**
- **Found during:** Task 2 preparation (the Docker `-race` gate over `./server/... ./world/...`).
- **Issue:** `ChannelQueue` was a bare `chan T`; `Push` did `c <- v` and `Close` did `close(c)`. The tick's `flushOutbound` racing a readLoop-triggered `Client.Close` is a data race and a latent "send on closed channel" panic. `Client.Send`'s `closed.Load()` + `recover()` masked the panic but not the concurrent access the detector flags. The prior single-test `-race` runs never hit it; the capture-diff's added timing/load surfaced it (reproducible at `-count=5..8` over the join-ordering tests).
- **Fix:** Converted `ChannelQueue[T]` to a struct `{ ch chan T; mu sync.Mutex; closed bool }`, serializing `Close` against `Push` with a mutex + `closed` flag (mirroring the already-safe `LinkedListQueue`); closed `Push` is a no-op returning false, `Close` is idempotent, `Pull` stays lock-free. No call-site change.
- **Files modified:** `net/queue/queue.go`
- **Verification:** `-race` clean at `-count=10` over the join-ordering tests; full `-race` gate over `./server/... ./world/... ./net/...` clean; full `go test ./...` green.
- **Committed in:** `c18789d3`

---

**Total deviations:** 1 auto-fixed (1 bug — a real concurrency race).
**Impact on plan:** The fix was essential for correctness (a latent crash + a flagged race). No scope creep; confined to the owning package; zero new dependencies.

## Issues Encountered

- The capture harness initially overwrote a 2-byte PlayerInfoUpdate (an early empty-action packet) with the 33-byte ADD_PLAYER entry; the 33-byte entry is the load-bearing one and is what the fixture captures. No functional impact.
- Vanilla ran in `gamemode=creative`, so its PlayerAbilities flags (`0x0d`) and gameMode VarInt (`1`) differ from Sulfur's survival values — confirmed as content, not framing, after field-by-field decode.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- **Phase 5 (Player Session In-World / FIRST PLAYABLE) is COMPLETE.** PLAY-01..06 are all proven: Join Game → Play, proto-769+ Sync Position + Confirm Teleportation, filled ring radius ≥2 + LOAD_START, movement with a following ring, tab-list self-entry, and the real-client first-playable walk-around. **The project is now playable end-to-end** — a vanilla 26.2 client connects, logs in, and walks around a persistent, ticking world.
- Ready for Phase 6 (entities/inventory/persistence): the player-session seam (movement, tick-owned position, the streaming ring, the tab list) is in place to build on.
- Deferred (carried): the `KeepAlive.removePlayer` double-leave hardening (Phase 3). The pulled-forward Join Game bootstrap deferred-row is now CLOSED — Phase 5 completed the full Player Session that extended it.

## Self-Check: PASSED

All claimed files exist on disk (SUMMARY, capture-diff test, 3 golden fixtures, CAPTURE-DIFF.md, the queue fix) and both task commits (`c18789d3` fix, `d067c720` test) are present in git history.

---
*Phase: 05-player-session-in-world-first-playable*
*Completed: 2026-06-24*
