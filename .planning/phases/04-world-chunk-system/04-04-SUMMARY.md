---
phase: 04-world-chunk-system
plan: 04
subsystem: world
tags: [chunk, paletted-container, capture-diff, vanilla-golden, light, heightmaps, proto-776, superflat]

# Dependency graph
requires:
  - phase: 04-01
    provides: Section fluid-count short + 3 CLIENT heightmaps (the wire fixes under test)
  - phase: 04-02
    provides: world.Superflat generator + WriteLevelChunkWithLight encoder
  - phase: 04-03
    provides: center-out view-ring streaming with ChunkBatchStart/Finished framing
provides:
  - "Authoritative WORLD-02/03 byte-diff: Sulfur's ClientboundLevelChunkWithLight is byte-structurally identical to a real vanilla 26.2 superflat chunk (committed golden fixture)"
  - "Two paletted-container wire bugs fixed that every symmetric self-round-trip passed over (header bits-per-entry vs packed width; superflat biome phantom palette)"
  - "Real vanilla 26.2 client renders solid, walkable superflat ground (no void, no stripes) — WORLD-05 proven end-to-end"
  - "A reproducible capture harness + golden fixture so CI byte-diffs without booting Java"
affects: [05-player-session, 09-vanilla-parity-worldgen]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Capture-diff against the real vanilla jar is the authoritative wire gate (a symmetric self-round-trip proves nothing about vanilla-correctness)"
    - "Committed golden .bin fixture + a test that walks the body as a CLIENT (deriving long count from the header, no length prefix) and asserts zero trailing bytes"

key-files:
  created:
    - .planning/phases/04-world-chunk-system/fixtures/vanilla-superflat-chunk.bin
    - .planning/phases/04-world-chunk-system/WORLD-CAPTURE-DIFF.md
    - level/chunk_capture_test.go
  modified:
    - level/palette.go
    - world/generator.go

key-decisions:
  - "PaletteContainer.Set must write the FLOORED stored bits-per-entry as the wire header (not the requested width); the mismatch packed 4-bit data under a 1-bit header — the literal stripes/void misframe"
  - "Superflat sections build a single-valued plains biome container directly (matching vanilla) instead of per-cell Set, which left a phantom default-biome palette entry"
  - "fluidCount=0 for the fluid-free superflat matches vanilla byte-for-byte; ChunkBatchStart/Finished is sent (vanilla matches); the empty-light complement shortcut is byte-valid + self-consistent for the all-present superflat (a compactness, not correctness, divergence)"
  - "A minimal PLAY-01/02/03 slice (Join Game bootstrap) was pulled forward to enable the visual milestone (commit 0fd96850); the full Player Session remains Phase 5"

patterns-established:
  - "Wire-format correctness for proto 776 is sealed by a vanilla capture-diff + a real-client visual smoke, never by self-consistent tests"

requirements-completed: [WORLD-02, WORLD-03, WORLD-05]

# Metrics
duration: 70min
completed: 2026-06-24
---

# Phase 4 Plan 04: Vanilla 26.2 Chunk Capture-Diff + Real-Client Visual Gate Summary

**Sulfur's ClientboundLevelChunkWithLight is byte-diffed against a real vanilla 26.2 superflat chunk (committed golden fixture) — the diff caught two paletted-container wire bugs invisible to every symmetric round-trip, and an unmodified vanilla 26.2 client now renders solid, walkable superflat ground.**

## Performance

- **Duration:** ~70 min
- **Started:** 2026-06-24T01:42Z (approx)
- **Completed:** 2026-06-24T02:50Z
- **Tasks:** 2 (1 autonomous capture-diff + 1 blocking human-verify, signed off)
- **Files modified:** 5 (2 code, 3 planning artifacts) for Task 1; the Join Game bootstrap (6 files) landed in a separate pulled-forward commit

## Accomplishments

- **Stood up the real vanilla 26.2 server** (`temp/cache/26.2-server.jar`, sha1 `823e2250…`, Java 25, `level-type=minecraft:flat`, seed 144, offline, EULA, scratch dir) and captured its `ClientboundLevelChunkWithLight` for chunk (0,0) — committed as `fixtures/vanilla-superflat-chunk.bin` (7280 bytes).
- **Byte-diffed Sulfur's encoder output** field-by-field against the golden: two per-section shorts (blockCount + **fluidCount**) present and aligned, states container header `bits=4` with no length prefix + LSB 256 longs, single-valued biomes, 3 CLIENT heightmaps {1,4,5}, 24 sections, 2048-byte light arrays — **both bodies parse to zero trailing bytes**.
- **Caught and fixed two real wire bugs** the symmetric self-round-trip passed over (see Deviations).
- **Resolved the 3 open questions** (fluidCount=0 matches vanilla; batch framing matches; light complement shortcut is byte-valid/self-consistent — the size gap is content, not framing).
- **Real-client visual sign-off:** an unmodified vanilla 26.2 (PrismLauncher) client connected to `cmd/sulfur` at `localhost:25565`, built its `ClientLevel`, and the user confirmed they **see and stand on** solid superflat ground — no void, no stripes, no "Loading terrain" hang.

## Task Commits

1. **Task 1 (fix half): paletted-container header bits + single-value biomes** — `34f7cc45` (fix)
2. **Task 1 (test half): capture-diff test + golden fixture + analysis doc** — `8611ce87` (test)
3. **Enabling bootstrap (pulled-forward, separate commit): minimal Play-state Join Game** — `0fd96850` (feat)

**Plan metadata:** `docs(04-04): complete vanilla capture-diff plan + close Phase 4` (this commit)

## Files Created/Modified

- `level/chunk_capture_test.go` — `TestSectionWireVsVanillaCapture`: loads the golden fixture, walks both bodies as a client (deriving the section long count from the header — no prefix), asserts Sulfur's framing matches vanilla and zero trailing bytes; skips cleanly if the fixture is absent. **Proven non-vacuous** (reverting the fix → `unexpected EOF`).
- `.planning/phases/04-world-chunk-system/fixtures/vanilla-superflat-chunk.bin` — the committed vanilla golden (0,0) packet body.
- `.planning/phases/04-world-chunk-system/WORLD-CAPTURE-DIFF.md` — reproducible capture method, per-field diff table, the two fixes, the 3 open questions, and the APPROVED real-client sign-off.
- `level/palette.go` — `PaletteContainer.Set` resize now writes the floored stored bits as the container `bits` field (and thus the wire header).
- `world/generator.go` — superflat sections build a single-valued plains biome container directly.

## Decisions Made

- The wire header bits-per-entry MUST equal the floored stored width — the resize path previously wrote the requested width, packing 4-bit data under a 1-bit header (the stripes/void misframe).
- Match vanilla's single-valued biome container for a uniform section (no phantom palette entry).
- Keep the empty-light complement shortcut for v1; the capture shows it is byte-valid and the real client renders fully lit. Switching to vanilla's selective presence-based masks is a future compactness optimization, not a correctness fix.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] PaletteContainer.Set wrote the wrong wire header bits-per-entry (stripes/void misframe)**
- **Found during:** Task 1 (capture-diff against the vanilla golden)
- **Issue:** The resize path set the container `bits` field (which `WriteTo` emits as the `UnsignedByte` header) to the *requested* width `vv` returned by `palette.id()`, while the data BitStorage + palette used the *floored* width `config.bits(vv)`. For states, `vv=1` but the data was 4-bit/256 longs — so the header said 1 bit (client expects 64 longs) over a 256-long body. A vanilla client mis-frames after 64 longs → stripes/void. Symmetric `ReadFrom` re-floors on read, so every self-round-trip was green on a broken wire.
- **Fix:** set the container `bits` field and the data BitStorage to `config.bits(vv)` (the floored stored width).
- **Files modified:** `level/palette.go`
- **Verification:** post-fix Sulfur's section states encode `bits=4` + 256 longs, byte-identical to vanilla; the capture-diff test asserts `headerBits ⇒ packedLongCount` per section and FAILS with `unexpected EOF` if reverted.
- **Committed in:** `34f7cc45`

**2. [Rule 1 - Bug] Superflat biome container carried a phantom palette entry**
- **Found during:** Task 1 (capture-diff)
- **Issue:** The generator filled biomes via per-cell `Biomes.Set(cell, plains)` on the fresh single-value container; the first Set resizes single→linear and carries the stale default biome (id 0) as a phantom index-0 palette entry + a data long. Vanilla emits a single-valued biome container (`bits=0`, one id, zero longs) for a uniform section.
- **Fix:** construct `s.Biomes = level.NewBiomesPaletteContainer(4*4*4, g.plains)` directly (single-valued plains).
- **Files modified:** `world/generator.go`
- **Verification:** post-fix Sulfur's biomes encode `bits=0, palLen=1, longs=0`, byte-identical to vanilla.
- **Committed in:** `34f7cc45`

---

**Total deviations:** 2 auto-fixed (both Rule 1 bugs — wire-framing divergences caught by the capture-diff, exactly the threat T-4-08 the plan's threat model targets). No scope creep — both are correctness fixes in the owning codec/generator, re-diffed before sign-off.
**Impact on plan:** Essential. Without them a real client renders stripes (states bug) and a non-vanilla biome shape. Both are confined to `level/palette.go` and `world/generator.go`; no generated files hand-edited.

## Issues Encountered

- **Real-client NPE (Play-state sequencing gap, not a chunk bug):** after Task 1 proved the chunk bytes correct, the live PrismLauncher client hit an NPE in `handleSetChunkCacheCenter` (`this.level == null`) because the server streamed chunks before any Join Game packet, so the client never built its `ClientLevel`. **Resolved** by a separately-committed minimal Play-state join bootstrap (`0fd96850`): jar-verified `ClientboundLogin` (id 49, isFlat=true, `Holder<DimensionType>` overworld=VarInt(1)), `ClientboundGameEvent` LEVEL_CHUNKS_LOAD_START (id 38 / event 13), and `ClientboundPlayerPosition` (id 72, proto-769+ teleport-id-first layout), enqueued in order **Login → GameEvent → PlayerPosition** before the chunk stream. This deliberately pulls a MINIMAL slice of PLAY-01/02/03 forward to enable the Phase-4 visual milestone; the full Player Session (profile, abilities, inventory, real spawn, teleport-id validation, movement) remains Phase 5 — **Phase 5 should EXTEND `server/play_join.go`, not duplicate it.**

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- **Phase 4 (World & Chunk System) is COMPLETE.** WORLD-01..05 satisfied: WORLD-01 (04-02/04-03 off-tick worker + tick rejoin), WORLD-02/03 (04-01 fixes sealed by the 04-04 capture-diff), WORLD-04 (04-02 superflat generator), WORLD-05 (04-03 streaming + 04-04 real-client render).
- **Carry into Phase 5:** the pulled-forward Join Game bootstrap (`server/play_join.go`, commit `0fd96850`) is a minimal slice — Phase 5 extends it into the full Player Session rather than re-implementing Login/spawn/teleport.
- **Still deferred:** `KeepAlive.removePlayer` double-leave hardening (from Phase 3) — lands with real timeout-driven disconnects.

---
*Phase: 04-world-chunk-system*
*Completed: 2026-06-24*

## Self-Check: PASSED

- Created files verified on disk: `level/chunk_capture_test.go`, `fixtures/vanilla-superflat-chunk.bin`, `WORLD-CAPTURE-DIFF.md`, `04-04-SUMMARY.md`.
- Commits verified: `34f7cc45` (fix), `8611ce87` (test), `0fd96850` (enabling bootstrap).
- Plan verification re-run: `go test ./level/ -run 'TestSectionWireVsVanillaCapture|TestSectionRoundTrip|TestChunkHeightmapsClientSet' -count=1` PASS; `go build ./...`, `go vet ./level/...` clean; Docker `-race` over level/world/server clean.
